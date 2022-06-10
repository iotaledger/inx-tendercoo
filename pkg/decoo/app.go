package decoo

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/iotaledger/hive.go/serializer/v2"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/builder"
	abcitypes "github.com/tendermint/tendermint/abci/types"
)

// the coordinator must implement all functions of an ABCI application
var _ abcitypes.Application = (*Coordinator)(nil)

// ABCI return codes
const (
	CodeTypeOK uint32 = iota
	CodeTypeSyntaxError
	CodeTypeStateError
	CodeTypeReplayError
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
		return abcitypes.ResponseCheckTx{Code: CodeTypeSyntaxError}
	}

	c.checkState.Lock()
	defer c.checkState.Unlock()
	return abcitypes.ResponseCheckTx{Code: tx.Apply(issuer, &c.checkState)}
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
		return abcitypes.ResponseDeliverTx{Code: CodeTypeSyntaxError}
	}

	c.deliverState.Lock()
	defer c.deliverState.Unlock()
	return abcitypes.ResponseDeliverTx{Code: tx.Apply(issuer, &c.deliverState)}
}

// EndBlock signals the end of a block.
// It is called after all transactions for the current block have been delivered, prior to the block's Commit message.
// Updates to the consensus parameters can only be updated in EndBlock.
func (c *Coordinator) EndBlock(abcitypes.RequestEndBlock) abcitypes.ResponseEndBlock {
	c.deliverState.Lock()
	defer c.deliverState.Unlock()

	// register callbacks for all received parents
	if c.deliverState.Milestone == nil {
		processed := map[iotago.BlockID]struct{}{} // avoid processing duplicate parents more than once
		for issuer, blockID := range c.deliverState.ParentByIssuer {
			if _, has := processed[blockID]; has {
				continue
			}
			c.log.Debugw("awaiting parent", "blockID", blockID)

			issuer, index := issuer, c.deliverState.MilestoneIndex
			_ = c.registry.RegisterCallback(blockID, func(id iotago.BlockID) { c.processParent(issuer, index, id) })
			processed[blockID] = struct{}{}
		}
	}

	if c.deliverState.Milestone == nil {
		// collect parents that have sufficient proofs
		parentWeight := 0
		var parents iotago.BlockIDs
		for blockIDKey, preMsProofs := range c.deliverState.ProofsByBlockID {
			if c.deliverState.IssuerCountByParent[blockIDKey] > 0 && len(preMsProofs) > c.committee.N()/3 {
				parentWeight += c.deliverState.IssuerCountByParent[blockIDKey]
				blockID := iotago.BlockID(blockIDKey)
				// the last milestone block ID will be added later anyway
				if blockID != c.deliverState.LastMilestoneBlockID {
					parents = append(parents, blockID)
				}
			}
		}

		// create the final milestone essence, if enough parents have been confirmed
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
			c.deliverState.Milestone = iotago.NewMilestone(c.deliverState.MilestoneIndex, timestamp, c.protoParas.Version, c.deliverState.LastMilestoneID, parents, types.Byte32FromSlice(inclMerkleRoot), types.Byte32FromSlice(appliedMerkleRoot))
			c.deliverState.Milestone.Metadata = c.deliverState.Metadata()

			// proofs are no longer needed for this milestone
			c.deliverState.ProofsByBlockID = nil
		}
	}

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

		c.log.Debugw("broadcast tx", "partial", partial)
		c.broadcastQueue.Submit(PartialKey, tx)
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

	// if we have a sufficient amount of signatures, the milestone is done
	if len(c.deliverState.SignaturesByIssuer) > c.committee.N()*2/3 {
		// sort partial signatures to generate deterministic milestone payload
		signatures := make(iotago.Signatures, 0, len(c.deliverState.SignaturesByIssuer))
		for _, signature := range c.deliverState.SignaturesByIssuer {
			signatures = append(signatures, signature)
		}
		sort.Slice(signatures, func(i, j int) bool {
			return bytes.Compare(
				signatures[i].(*iotago.Ed25519Signature).PublicKey[:],
				signatures[j].(*iotago.Ed25519Signature).PublicKey[:]) < 0
		})
		// add the signatures to the milestone
		c.deliverState.Milestone.Signatures = signatures

		// create and issue the milestone block, if its index is new and the coordinator is running
		if c.started.Load() {
			latest, err := c.inxClient.LatestMilestone()
			if err != nil {
				c.log.Errorf("failed to get latest milestone: %s", err)
			}
			if latest == nil || c.deliverState.MilestoneIndex > latest.Index {
				ms := *c.deliverState.Milestone
				go func() {
					if err := c.createAndSendMilestone(c.ctx, ms); err != nil {
						panic(err)
					}
				}()
			}
		}

		// reset the state for the next milestone
		state := &State{
			MilestoneHeight:      c.deliverState.Height,
			MilestoneIndex:       c.deliverState.MilestoneIndex + 1,
			LastMilestoneID:      MilestoneID(c.deliverState.Milestone),
			LastMilestoneBlockID: MilestoneBlockID(c.deliverState.Milestone),
		}
		c.deliverState.Reset(c.deliverState.Height, state)
		c.registry.Clear()
	}

	// make a deep copy of the state
	c.checkState.Copy(&c.deliverState)

	return abcitypes.ResponseCommit{Data: c.deliverState.Hash()}
}

func (c *Coordinator) processParent(issuer iotago.MilestonePublicKey, index uint32, blockID iotago.BlockID) {
	// create a proof referencing this parent
	proof := &Proof{Index: index, Parent: blockID}
	tx, err := MarshalTx(c.committee, proof)
	if err != nil {
		panic(err)
	}

	// submit the proof for broadcast
	// keep at most one parent per issuer in the queue
	member, ok := c.committee.MemberIndex(issuer)
	if !ok {
		panic("coordinator: issuer has no index")
	}

	c.log.Debugw("broadcast tx", "proof", proof, "member", member)
	c.broadcastQueue.Submit(ProofKey+member, tx)
}

func (c *Coordinator) createAndSendMilestone(ctx context.Context, ms iotago.Milestone) error {
	if err := ms.VerifySignatures(c.committee.T(), c.committee.Members()); err != nil {
		return fmt.Errorf("validating the signatures failed: %w", err)
	}
	msg, err := builder.NewBlockBuilder(ms.ProtocolVersion).ParentsBlockIDs(ms.Parents).Payload(&ms).Build()
	if err != nil {
		return fmt.Errorf("building the block failed: %w", err)
	}
	if _, err := msg.Serialize(serializer.DeSeriModePerformValidation, c.protoParas); err != nil {
		return fmt.Errorf("serializing the block failed: %w", err)
	}

	latestMilestoneBlockID, err := c.inxClient.SubmitBlock(ctx, msg)
	if err != nil {
		return fmt.Errorf("emitting the block failed: %w", err)
	}
	c.log.Debugw("milestone issued", "blockID", latestMilestoneBlockID, "payload", msg.Payload)

	return nil
}

// MilestoneID returns the block ID of the given milestone.
func MilestoneID(ms *iotago.Milestone) iotago.MilestoneID {
	id, err := ms.ID()
	if err != nil {
		panic(err)
	}
	return id
}

// MilestoneBlockID returns the block ID of the given milestone.
func MilestoneBlockID(ms *iotago.Milestone) iotago.BlockID {
	msg, err := builder.NewBlockBuilder(ms.ProtocolVersion).ParentsBlockIDs(ms.Parents).Payload(ms).Build()
	if err != nil {
		panic(err)
	}
	return msg.MustID()
}
