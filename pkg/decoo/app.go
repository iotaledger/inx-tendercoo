package decoo

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	abcitypes "github.com/tendermint/tendermint/abci/types"

	"github.com/iotaledger/hive.go/serializer/v2"
	"github.com/iotaledger/inx-app/nodebridge"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/builder"
)

// Coordinator must implement all functions of an ABCI application.
var _ abcitypes.Application = (*Coordinator)(nil)

// ABCI return codes.
const (
	CodeTypeOK uint32 = iota
	CodeTypeSizeError
	CodeTypeSyntaxError
	CodeTypeStateError
	CodeTypeNotSupportedError
)

// Info is called during initialization to retrieve and validate application state.
// LastBlockHeight is used to determine which blocks need to be replayed to the application during syncing.
func (c *Coordinator) Info(req abcitypes.RequestInfo) abcitypes.ResponseInfo {
	c.log.Debugw("ABCI Info", "req", req)

	last := c.cms.LastCommitInfo()

	return abcitypes.ResponseInfo{
		Data:             fmt.Sprintf("{\"milestone_index\":%d}", c.deliverState.MilestoneIndex),
		AppVersion:       ProtocolVersion,
		LastBlockHeight:  last.Height,
		LastBlockAppHash: last.Hash,
	}
}

// Query queries the application for information about application state.
func (c *Coordinator) Query(req abcitypes.RequestQuery) abcitypes.ResponseQuery {
	c.log.Debugw("ABCI Query", "req", req)

	return abcitypes.ResponseQuery{Code: CodeTypeNotSupportedError}
}

// CheckTx controls whether a given transactions is considered for inclusion in a block.
// When a non-zero Code is returned, the transactions is discarded and not being gossiped to other peers.
// To prevent race conditions when CheckTx is calling during processing of a new block, the previous state must be used.
func (c *Coordinator) CheckTx(req abcitypes.RequestCheckTx) abcitypes.ResponseCheckTx {
	if len(req.Tx) > MaxTxBytes {
		return abcitypes.ResponseCheckTx{Code: CodeTypeSizeError}
	}

	c.checkState.Lock()
	defer c.checkState.Unlock()

	issuer, tx, err := UnmarshalTx(c.committee, c.checkState.MilestoneIndex, req.Tx)
	if err != nil {
		return abcitypes.ResponseCheckTx{Code: CodeTypeSyntaxError, Log: err.Error()}
	}

	if err := tx.Apply(issuer, &c.checkState); err != nil {
		return abcitypes.ResponseCheckTx{Code: CodeTypeStateError, Log: err.Error()}
	}

	return abcitypes.ResponseCheckTx{Code: CodeTypeOK}
}

// BeginBlock is the first method called for each new block.
func (c *Coordinator) BeginBlock(req abcitypes.RequestBeginBlock) abcitypes.ResponseBeginBlock {
	defer c.stopOnPanic()

	if err := c.cms.ValidateHeight(req.Header.Height); err != nil {
		panic(err)
	}

	c.deliverState.Lock()
	defer c.deliverState.Unlock()

	c.deliverState.blockHeader = req.Header

	return abcitypes.ResponseBeginBlock{}
}

// DeliverTx delivers transactions from Tendermint to the application.
// It is called for each transaction in a block and will always be called between BeginBlock and EndBlock.
// A block can contain invalid transactions if it was issued by a malicious peer, as such validity must
// be checked in a deterministic way.
func (c *Coordinator) DeliverTx(req abcitypes.RequestDeliverTx) abcitypes.ResponseDeliverTx {
	if len(req.Tx) > MaxTxBytes {
		return abcitypes.ResponseDeliverTx{Code: CodeTypeSizeError}
	}

	c.deliverState.Lock()
	defer c.deliverState.Unlock()

	issuer, tx, err := UnmarshalTx(c.committee, c.deliverState.MilestoneIndex, req.Tx)
	if err != nil {
		return abcitypes.ResponseDeliverTx{Code: CodeTypeSyntaxError, Log: err.Error()}
	}

	c.log.Debugw("DeliverTx", "tx", tx, "issuer", issuer)

	if err := tx.Apply(issuer, &c.deliverState); err != nil {
		return abcitypes.ResponseDeliverTx{Code: CodeTypeStateError, Log: err.Error()}
	}

	return abcitypes.ResponseDeliverTx{Code: CodeTypeOK}
}

// EndBlock signals the end of a block.
// It is called after all transactions for the current block have been delivered, prior to the block's Commit message.
// Updates to the consensus parameters can only be updated in EndBlock.
func (c *Coordinator) EndBlock(abcitypes.RequestEndBlock) abcitypes.ResponseEndBlock {
	defer c.stopOnPanic()
	c.deliverState.Lock()
	defer c.deliverState.Unlock()

	// try to create the milestone essence
	if c.deliverState.Milestone == nil {
		// collect parents that have sufficient proofs
		parentWeight := 0
		var parents iotago.BlockIDs
		for blockID, proofs := range c.deliverState.ProofIssuersByBlockID {
			// a parent is considered valid, if at least one honest peer issued a proof for it
			if c.deliverState.IssuerCountByParent[blockID] > 0 && len(proofs) > c.committee.F() {
				parentWeight += c.deliverState.IssuerCountByParent[blockID]
				// the last milestone block ID will be added later anyway, so ignore it here
				if blockID != c.deliverState.LastMilestoneBlockID {
					parents = append(parents, blockID)
				}
			}
		}

		// if enough parents have been confirmed, create the milestone essence
		if parentWeight > c.committee.F() {
			c.createMilestoneEssence(parents)
		}
	}

	// register callbacks for all received parents
	if c.deliverState.Milestone == nil {
		processed := map[iotago.BlockID]struct{}{} // avoid processing duplicate parents more than once
		for _, blockID := range c.deliverState.ParentByIssuer {
			if _, has := processed[blockID]; has {
				continue
			}
			processed[blockID] = struct{}{}

			// skip, if our proof is already part of the state
			if proof := c.deliverState.ProofIssuersByBlockID[blockID]; proof != nil {
				if _, has := proof[c.committee.ID()]; has {
					continue
				}
			}

			// register a callback when that block becomes solid
			c.log.Debugw("awaiting parent", "blockID", blockID)
			c.registerProcessParentOnSolid(blockID, c.deliverState.MilestoneIndex)
		}
	}

	return abcitypes.ResponseEndBlock{}
}

// Commit signals the application to persist the application state.
// Data must return the hash of the state after all changes from this block have been applied.
// It will be used to validate consistency between the applications.
func (c *Coordinator) Commit() abcitypes.ResponseCommit {
	defer c.stopOnPanic()
	c.checkState.Lock()
	defer c.checkState.Unlock()
	c.deliverState.Lock()
	defer c.deliverState.Unlock()

	// create and broadcast our partial signature
	if c.deliverState.Milestone != nil && // if we have an essence to sign
		len(c.deliverState.SignaturesByIssuer) < c.committee.T() && // and there are not enough partial signatures yet
		c.deliverState.SignaturesByIssuer[c.committee.ID()] == nil { // and our signatures is not yet part of the state

		// the PartialSignature tx only passes CheckTx once the checkState has been updated to contain the milestone
		// since during commit the mempool is locked, the broadcast tx will not be processed before the state is updated
		c.broadcastPartial()
	}

	// if we have a sufficient amount of signatures, the milestone is done
	if c.deliverState.Milestone != nil && len(c.deliverState.SignaturesByIssuer) >= c.committee.T() {
		// sort partial signatures to generate deterministic milestone payload
		signatures := make(iotago.Signatures, 0, len(c.deliverState.SignaturesByIssuer))
		for _, signature := range c.deliverState.SignaturesByIssuer {
			signatures = append(signatures, signature)
		}
		sort.Sort(signatures)
		// add the signatures to the milestone
		c.deliverState.Milestone.Signatures = signatures

		// submit the milestone in a separate go routine to unlock the Tendermint state as soon as possible
		ms := *c.deliverState.Milestone
		go func() {
			if err := c.submitMilestoneBlock(&ms); err != nil {
				panic(err)
			}
		}()

		// reset the state for the next milestone
		state := &State{
			MilestoneHeight:      c.deliverState.blockHeader.Height,
			MilestoneIndex:       c.deliverState.MilestoneIndex + 1,
			LastMilestoneID:      c.deliverState.Milestone.MustID(),
			LastMilestoneBlockID: MilestoneBlockID(c.deliverState.Milestone),
		}
		c.deliverState.Reset(state)

		// the callbacks are no longer relevant
		c.inxClearBlockSolidCallbacks()
		// trigger an event for the new milestone index
		c.stateMilestoneIndexSyncEvent.Trigger(state.MilestoneIndex)
	}

	// make a deep copy of the state
	c.checkState.Copy(&c.deliverState)
	// update the last commit info
	c.cms.Commit(&c.deliverState)

	return abcitypes.ResponseCommit{Data: c.cms.LastCommitInfo().Hash}
}

// stopOnPanic assures that the coordinator is stopped when a panic occurred.
// This should be deferred in any method for which Tendermint recovers the panic.
func (c *Coordinator) stopOnPanic() {
	if e := recover(); e != nil {
		c.log.Warn("stopping after panic")
		_ = c.Stop()
		// the panic is recovered by Tendermint, so we need to re-raise it
		panic(e)
	}
}

func (c *Coordinator) broadcastPartial() {
	essence, err := c.deliverState.Milestone.Essence()
	if err != nil {
		panic(err)
	}
	partial := &PartialSignature{
		Signature: c.committee.Sign(essence).Signature[:],
	}
	tx, err := MarshalTx(c.committee, partial)
	if err != nil {
		panic(err)
	}

	c.log.Debugw("broadcast tx", "partial", partial)
	c.broadcastQueue.Submit(tx)
}

func (c *Coordinator) registerProcessParentOnSolid(blockID iotago.BlockID, index uint32) {
	err := c.inxRegisterBlockSolidCallback(blockID, func(m *inx.BlockMetadata) { c.processParent(index, m) })
	// we can safely ignore ErrAlreadyRegistered, as each parent needs to be processed only once
	// since ClearBlockSolidCallbacks is called every time the milestone index changes, index will always be the same
	if err != nil && !errors.Is(err, nodebridge.ErrAlreadyRegistered) {
		panic(err)
	}
}

func (c *Coordinator) processParent(index uint32, meta *inx.BlockMetadata) {
	blockID := meta.UnwrapBlockID()
	// only create proofs for solid tips that are not below max depth
	if !meta.Solid || meta.ReferencedByMilestoneIndex > 0 || meta.ShouldReattach {
		c.log.Debugw("invalid tip", "parent", blockID)

		return
	}

	// create a proof referencing this valid parent
	proof := &Proof{Index: index, Parent: blockID}
	tx, err := MarshalTx(c.committee, proof)
	if err != nil {
		panic(err)
	}

	c.log.Debugw("broadcast tx", "proof", proof)
	c.broadcastQueue.Submit(tx)
}

func (c *Coordinator) createMilestoneEssence(parents iotago.BlockIDs) {
	// make sure that we have at most BlockMaxParents-1 parents
	if len(parents) > iotago.BlockMaxParents-1 {
		// sort the parents, first by IssuerCount and then by BlockID
		sort.Slice(parents,
			func(i, j int) bool { // return true, when the element at i must sort before the element at j
				a, b := parents[i], parents[j]
				cmp := c.deliverState.IssuerCountByParent[b] - c.deliverState.IssuerCountByParent[a]
				if cmp == 0 {
					return bytes.Compare(a[:], b[:]) < 0
				}

				return cmp < 0 // c.deliverState.IssuerCountByParent[b] < c.deliverState.IssuerCountByParent[a]
			})
		parents = parents[:iotago.BlockMaxParents-1]
	}
	// add the last milestone block ID and sort
	parents = append(parents, c.deliverState.LastMilestoneBlockID)
	parents = parents.RemoveDupsAndSort()

	c.log.Debugw("create milestone", "state", c.deliverState.State, "parents", parents)

	timestamp := uint32(c.deliverState.blockHeader.Time.Unix())

	inclMerkleRoot, appliedMerkleRoot, err := c.inxComputeWhiteFlag(
		c.deliverState.MilestoneIndex,
		timestamp,
		parents,
		c.deliverState.LastMilestoneID)
	if err != nil {
		panic(err)
	}

	// create milestone essence
	c.deliverState.Milestone = iotago.NewMilestone(
		c.deliverState.MilestoneIndex,
		timestamp,
		c.protoParamsFunc().Version,
		c.deliverState.LastMilestoneID,
		parents,
		types.Byte32FromSlice(inclMerkleRoot),
		types.Byte32FromSlice(appliedMerkleRoot))
	c.deliverState.Milestone.Metadata = c.deliverState.Metadata()
}

func (c *Coordinator) submitMilestoneBlock(ms *iotago.Milestone) error {
	// skip, if ms is not the latest milestone
	if lmi := c.inxLatestMilestoneIndex(); ms.Index <= lmi {
		c.log.Debugw("milestone skipped", "index", ms.Index, "latest", lmi)

		return nil
	}

	if err := ms.VerifySignatures(c.committee.T(), c.committee.Members(ms.Index)); err != nil {
		return fmt.Errorf("validating the signatures failed: %w", err)
	}
	block, err := buildMilestoneBlock(ms)
	if err != nil {
		return fmt.Errorf("building the block failed: %w", err)
	}
	if _, err := block.Serialize(serializer.DeSeriModePerformValidation, c.protoParamsFunc()); err != nil {
		return fmt.Errorf("serializing the block failed: %w", err)
	}

	latestMilestoneBlockID, err := c.inxSubmitBlock(block)
	if err != nil {
		// if SubmitBlock failed, check whether block is still present in the node, submitted by a different validator
		if _, blockErr := c.inxBlockMetadata(block.MustID()); blockErr != nil {
			// only report an error, if we couldn't submit, and it is not present
			return fmt.Errorf("submitting the milestone failed: %w", err)
		}
		// TODO: also report this as a metric
		c.log.Debugw("submit failed but block is already present", "milestoneIndex", ms.Index, "err", err)

		return nil
	}

	c.log.Debugw("milestone submitted", "blockID", latestMilestoneBlockID, "payload", block.Payload)

	return nil
}

func buildMilestoneBlock(ms *iotago.Milestone) (*iotago.Block, error) {
	return builder.NewBlockBuilder().ProtocolVersion(ms.ProtocolVersion).Parents(ms.Parents).Payload(ms).Build()
}

// MilestoneBlockID returns the block ID of the given milestone.
func MilestoneBlockID(ms *iotago.Milestone) iotago.BlockID {
	msg, err := buildMilestoneBlock(ms)
	if err != nil {
		panic(err)
	}

	return msg.MustID()
}
