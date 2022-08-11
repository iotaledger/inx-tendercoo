package decoo

import (
	"context"
	"fmt"
	"sort"

	abcitypes "github.com/tendermint/tendermint/abci/types"

	"github.com/iotaledger/hive.go/serializer/v2"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/builder"
)

// the coordinator must implement all functions of an ABCI application
var _ abcitypes.Application = (*Coordinator)(nil)

// ABCI return codes
const (
	CodeTypeOK uint32 = iota
	CodeTypeSyntaxError
	CodeTypeStateError
	CodeTypeNotSupportedError
)

// Info is called during initialization to retrieve and validate application state.
// LastBlockHeight is used to determine which blocks need to be replayed to the application during syncing.
func (c *Coordinator) Info(req abcitypes.RequestInfo) abcitypes.ResponseInfo {
	c.log.Debugw("ABCI info", "req", req)

	c.deliverState.Lock()
	defer c.deliverState.Unlock()

	return abcitypes.ResponseInfo{
		Data:             fmt.Sprintf("{\"milestone_index\":%d}", c.deliverState.MilestoneIndex),
		AppVersion:       ProtocolVersion,
		LastBlockHeight:  c.deliverState.Height,
		LastBlockAppHash: c.deliverState.Hash(),
	}
}

// Query queries the application for information about application state.
func (c *Coordinator) Query(req abcitypes.RequestQuery) abcitypes.ResponseQuery {
	c.log.Debugw("ABCI query", "req", req)

	return abcitypes.ResponseQuery{Code: CodeTypeNotSupportedError}
}

// CheckTx controls whether a given transactions is considered for inclusion in a block.
// When a non-zero Code is returned, the transactions is discarded and not being gossiped to other peers.
// To prevent race conditions when CheckTx is calling during processing of a new block, the previous state must be used.
func (c *Coordinator) CheckTx(req abcitypes.RequestCheckTx) abcitypes.ResponseCheckTx {
	issuer, tx, err := UnmarshalTx(c.committee, req.Tx)
	if err != nil {
		return abcitypes.ResponseCheckTx{Code: CodeTypeSyntaxError, Log: err.Error()}
	}

	c.checkState.Lock()
	defer c.checkState.Unlock()
	if err := tx.Apply(issuer, &c.checkState); err != nil {
		return abcitypes.ResponseCheckTx{Code: CodeTypeStateError, Log: err.Error()}
	}
	return abcitypes.ResponseCheckTx{Code: CodeTypeOK}
}

// BeginBlock is the first method called for each new block.
func (c *Coordinator) BeginBlock(req abcitypes.RequestBeginBlock) abcitypes.ResponseBeginBlock {
	c.deliverState.Lock()
	defer c.deliverState.Unlock()

	c.blockTime = req.Header.Time
	return abcitypes.ResponseBeginBlock{}
}

// DeliverTx delivers transactions from Tendermint to the application.
// It is called for each transaction in a block and will always be called between BeginBlock and EndBlock.
// A block can contain invalid transactions if it was issued by a malicious peer, as such validity must
// be checked in a deterministic way.
func (c *Coordinator) DeliverTx(req abcitypes.RequestDeliverTx) abcitypes.ResponseDeliverTx {
	issuer, tx, err := UnmarshalTx(c.committee, req.Tx)
	if err != nil {
		return abcitypes.ResponseDeliverTx{Code: CodeTypeSyntaxError, Log: err.Error()}
	}

	c.log.Debugw("DeliverTx", "tx", tx, "issuer", issuer)

	c.deliverState.Lock()
	defer c.deliverState.Unlock()
	if err := tx.Apply(issuer, &c.deliverState); err != nil {
		return abcitypes.ResponseDeliverTx{Code: CodeTypeStateError, Log: err.Error()}
	}
	return abcitypes.ResponseDeliverTx{Code: CodeTypeOK}
}

// EndBlock signals the end of a block.
// It is called after all transactions for the current block have been delivered, prior to the block's Commit message.
// Updates to the consensus parameters can only be updated in EndBlock.
func (c *Coordinator) EndBlock(abcitypes.RequestEndBlock) abcitypes.ResponseEndBlock {
	c.deliverState.Lock()
	defer c.deliverState.Unlock()

	// try to create the milestone essence
	if c.deliverState.Milestone == nil {
		// collect parents that have sufficient proofs
		parentWeight := 0
		var parents iotago.BlockIDs
		for blockID, preMsProofs := range c.deliverState.ProofIssuersByBlockID {
			if c.deliverState.IssuerCountByParent[blockID] > 0 && len(preMsProofs) > c.committee.N()/3 {
				parentWeight += c.deliverState.IssuerCountByParent[blockID]
				// the last milestone block ID will be added later anyway
				if blockID != c.deliverState.LastMilestoneBlockID {
					parents = append(parents, blockID)
				}
			}
		}

		// if enough parents have been confirmed, create the milestone essence
		if parentWeight > c.committee.N()/3 {
			if len(parents) > iotago.BlockMaxParents-1 {
				parents = parents[:iotago.BlockMaxParents-1]
			}
			// always add the previous milestone as a parent
			parents = append(parents, c.deliverState.LastMilestoneBlockID)
			parents = parents.RemoveDupsAndSort()

			c.log.Debugw("create milestone", "state", c.deliverState.State, "parents", parents)

			timestamp := uint32(c.blockTime.Unix())
			// the local context is only set when the coordinator gets started; so we have to use context.Background() here
			inclMerkleRoot, appliedMerkleRoot, err := c.inxClient.ComputeWhiteFlag(context.Background(), c.deliverState.MilestoneIndex, timestamp, parents, c.deliverState.LastMilestoneID)
			if err != nil {
				panic(err)
			}

			// create milestone essence
			c.deliverState.Milestone = iotago.NewMilestone(c.deliverState.MilestoneIndex, timestamp, c.protoParamsFunc().Version, c.deliverState.LastMilestoneID, parents, types.Byte32FromSlice(inclMerkleRoot), types.Byte32FromSlice(appliedMerkleRoot))
			c.deliverState.Milestone.Metadata = c.deliverState.Metadata()
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
			index := c.deliverState.MilestoneIndex
			_ = c.listener.RegisterBlockSolidCallback(blockID, func(m *inx.BlockMetadata) { c.processParent(index, m) })
		}
	}

	return abcitypes.ResponseEndBlock{}
}

// Commit signals the application to persist the application state.
// Data must return the hash of the state after all changes from this block have been applied.
// It will be used to validate consistency between the applications.
func (c *Coordinator) Commit() abcitypes.ResponseCommit {
	c.checkState.Lock()
	defer c.checkState.Unlock()
	c.deliverState.Lock()
	defer c.deliverState.Unlock()

	// update the block height
	c.deliverState.Height++

	// create and broadcast our partial signature
	if c.deliverState.Milestone != nil && // if we have an essence to sign
		len(c.deliverState.SignaturesByIssuer) < c.committee.T() && // and there are not enough partial signatures yet
		c.deliverState.SignaturesByIssuer[c.committee.ID()] == nil { // and our signatures is not yet part of the state

		// create the partial signature for that essence
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

		// the partial signature tx only passes CheckTx once the checkState has been updated to contain the milestone
		// during commit the mempool is locked, thus broadcasting it here assures that checkState is updated
		c.log.Debugw("broadcast tx", "partial", partial)
		c.broadcastQueue.Submit(tx)
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

		// create and issue the milestone, if it is newer than the latest available milestone
		latest, err := c.inxClient.LatestMilestone()
		if err != nil {
			c.log.Errorf("failed to get latest milestone: %s", err)
		}
		if latest == nil || c.deliverState.Milestone.Index > latest.Index {
			ms := *c.deliverState.Milestone
			go func() {
				if err := c.createAndSendMilestone(c.ctx, ms); err != nil {
					panic(err)
				}
			}()
		} else {
			c.log.Debugw("milestone skipped", "index", c.deliverState.Milestone.Index, "latest", latest.Index)
		}

		// reset the state for the next milestone
		state := &State{
			MilestoneHeight:      c.deliverState.Height,
			MilestoneIndex:       c.deliverState.MilestoneIndex + 1,
			LastMilestoneID:      c.deliverState.Milestone.MustID(),
			LastMilestoneBlockID: MilestoneBlockID(c.deliverState.Milestone),
		}
		c.deliverState.Reset(c.deliverState.Height, state)

		// the callbacks are no longer relevant
		c.listener.ClearBlockSolidCallbacks()
		// trigger an event for the new milestone index
		c.stateMilestoneIndexSyncEvent.Trigger(state.MilestoneIndex)
	}

	// make a deep copy of the state
	c.checkState.Copy(&c.deliverState)
	return abcitypes.ResponseCommit{Data: c.deliverState.Hash()}
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

func (c *Coordinator) createAndSendMilestone(ctx context.Context, ms iotago.Milestone) error {
	if err := ms.VerifySignatures(c.committee.T(), c.committee.Members()); err != nil {
		return fmt.Errorf("validating the signatures failed: %w", err)
	}
	msg, err := buildMilestoneBlock(&ms)
	if err != nil {
		return fmt.Errorf("building the block failed: %w", err)
	}
	if _, err := msg.Serialize(serializer.DeSeriModePerformValidation, c.protoParamsFunc()); err != nil {
		return fmt.Errorf("serializing the block failed: %w", err)
	}

	latestMilestoneBlockID, err := c.inxClient.SubmitBlock(ctx, msg)
	if err != nil {
		return fmt.Errorf("emitting the block failed: %w", err)
	}
	c.log.Debugw("milestone issued", "blockID", latestMilestoneBlockID, "payload", msg.Payload)

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
