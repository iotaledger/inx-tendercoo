package decoo

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/gohornet/hornet/pkg/model/hornet"
	"github.com/gohornet/hornet/pkg/model/milestone"
	"github.com/iotaledger/hive.go/serializer/v2"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/proto/tendermint"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/builder"
	"github.com/tendermint/tendermint/abci/types"
	"golang.org/x/crypto/blake2b"
)

// the coordinator must implement all functions of an ABCI application
var _ types.Application = (*Coordinator)(nil)

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
func (c *Coordinator) Info(req types.RequestInfo) types.ResponseInfo {
	c.log.Debugw("ABCI info", "req", req)

	c.lastAppState.RLock()
	defer c.lastAppState.RUnlock()

	return types.ResponseInfo{
		Data:             fmt.Sprintf("{\"milestone_index\":%d}", c.lastAppState.CurrentMilestoneIndex),
		AppVersion:       ProtocolVersion,
		LastBlockHeight:  c.lastAppState.Height,
		LastBlockAppHash: c.lastAppStateHash,
	}
}

// Query queries the application for information about application state.
func (c *Coordinator) Query(req types.RequestQuery) types.ResponseQuery {
	c.log.Debugw("ABCI query", "req", req)

	return types.ResponseQuery{Code: CodeTypeNotSupportedError}
}

// CheckTx controls whether a given transactions is considered for inclusion in a block.
// When a non-zero Code is returned, the transactions is discarded and not being gossiped to other peers.
// To prevent race conditions when CheckTx is calling during processing of a new block, the previous state must be used.
func (c *Coordinator) CheckTx(req types.RequestCheckTx) types.ResponseCheckTx {
	_, msg, err := c.unmarshalTx(req.Tx)
	if err != nil {
		return types.ResponseCheckTx{Code: CodeTypeSyntaxError}
	}

	// all checks in CheckTx need to be performed against lastAppState to avoid race conditions
	c.lastAppState.RLock()
	defer c.lastAppState.RUnlock()

	switch p := msg.(type) {
	case *tendermint.Parent:
		return types.ResponseCheckTx{Code: c.lastAppState.CheckParent(p)}
	case *tendermint.Proof:
		return types.ResponseCheckTx{Code: c.lastAppState.CheckProof(p)}
	case *tendermint.PartialSignature:
		return types.ResponseCheckTx{Code: c.lastAppState.CheckPartial(p)}
	default: // invalid tx type
		return types.ResponseCheckTx{Code: CodeTypeSyntaxError}
	}
}

// BeginBlock is the first method called for each new block.
func (c *Coordinator) BeginBlock(req types.RequestBeginBlock) types.ResponseBeginBlock {
	c.currAppState.Lock()
	defer c.currAppState.Unlock()

	// use the timestamp from the header rounded down to seconds
	c.currAppState.Timestamp = uint32(req.Header.Time.Unix())
	return types.ResponseBeginBlock{}
}

// DeliverTx delivers transactions from Tendermint to the application.
// It is called for each transaction in a block and will always be called between BeginBlock and EndBlock.
// A block can contain invalid transactions if it was issued by a malicious peer, as such validity must
// be checked in a deterministic way.
func (c *Coordinator) DeliverTx(req types.RequestDeliverTx) types.ResponseDeliverTx {
	issuer, msg, err := c.unmarshalTx(req.Tx)
	if err != nil {
		return types.ResponseDeliverTx{Code: CodeTypeSyntaxError}
	}

	// DeliverTx needs to rerun syntactic check against currAppState and perform semantic checks
	c.currAppState.Lock()
	defer c.currAppState.Unlock()

	switch p := msg.(type) {
	case *tendermint.Parent:
		if code := c.currAppState.CheckParent(p); code != 0 {
			return types.ResponseDeliverTx{Code: code}
		}
		return types.ResponseDeliverTx{Code: c.currAppState.DeliverParent(issuer, p, c.committee)}
	case *tendermint.Proof:
		if code := c.currAppState.CheckProof(p); code != 0 {
			return types.ResponseDeliverTx{Code: code}
		}
		return types.ResponseDeliverTx{Code: c.currAppState.DeliverProof(issuer, p, c.committee)}
	case *tendermint.PartialSignature:
		if code := c.currAppState.CheckPartial(p); code != 0 {
			return types.ResponseDeliverTx{Code: code}
		}
		return types.ResponseDeliverTx{Code: c.currAppState.DeliverPartial(issuer, p, c.committee)}
	default: // invalid tx type
		return types.ResponseDeliverTx{Code: CodeTypeSyntaxError}
	}
}

// EndBlock signals the end of a block.
// It is called after all transactions for the current block have been delivered, prior to the block's Commit message.
// Updates to the consensus parameters can only be updated in EndBlock.
func (c *Coordinator) EndBlock(types.RequestEndBlock) types.ResponseEndBlock {
	c.currAppState.Lock()
	defer c.currAppState.Unlock()

	// collect parents that have sufficient proofs
	parentWeight := 0
	var parents hornet.MessageIDs
	for msgID, preMsProofs := range c.currAppState.ProofsByMsgID {
		if c.currAppState.IssuerCountByParent[msgID] > 0 && len(preMsProofs) > c.committee.N()/3 {
			parentWeight += c.currAppState.IssuerCountByParent[msgID]
			// the last milestone message ID will be added later anyway
			if msgID != c.currAppState.LastMilestoneMsgID {
				parents = append(parents, msgID[:])
			}
		}
	}

	// create the final milestone essence, if enough parents have been confirmed
	if c.currAppState.Milestone == nil && parentWeight > c.committee.N()/3 {
		if len(parents) > iotago.MaxParentsInAMessage-1 {
			parents = parents[:iotago.MaxParentsInAMessage-1]
		}
		// always add the previous milestone as a parent
		parents = append(parents, c.currAppState.LastMilestoneMsgID[:])
		parents = parents.RemoveDupsAndSortByLexicalOrder()

		// compute merkle tree root
		merkleProof, err := c.nodeBridge.ComputeMerkleTreeHash(context.Background(), milestone.Index(c.currAppState.CurrentMilestoneIndex), c.currAppState.Timestamp, parents, c.currAppState.LastMilestoneID)
		if err != nil {
			panic(err)
		}

		// create milestone essence
		c.currAppState.Milestone = iotago.NewMilestone(c.currAppState.CurrentMilestoneIndex, c.currAppState.Timestamp, c.protoParas.Version, c.currAppState.LastMilestoneID, parents.ToSliceOfArrays(), merkleProof.ConfirmedMerkleRoot, merkleProof.AppliedMerkleRoot)

		// proofs are no longer needed for this milestone
		c.currAppState.ProofsByMsgID = nil
	}

	// create and broadcast our partial signature
	if c.currAppState.Milestone != nil && // if we have an essence to sign
		len(c.currAppState.SignaturesByIssuer) < c.committee.T() && // and there are not enough partial signatures yet
		c.currAppState.SignaturesByIssuer[c.committee.ID()] == nil { // and our signatures is not yet part of the state

		// create the partial signature for that essence
		essence, err := c.currAppState.Milestone.Essence()
		if err != nil {
			panic(err)
		}
		partial := &tendermint.PartialSignature{
			Index:              c.currAppState.CurrentMilestoneIndex,
			MilestoneSignature: c.committee.Sign(essence).Signature[:],
		}
		tx, err := c.marshalTx(partial)
		if err != nil {
			panic(err)
		}

		type stripped *tendermint.PartialSignature // ignore ugly protobuf String() method
		c.log.Debugw("broadcast tx", "partial", stripped(partial))

		// submit the partial signature for broadcast
		// keep at most one partial signature in the queue
		c.broadcastQueue.Submit(PartialKey, tx)
	}

	return types.ResponseEndBlock{}
}

// Commit signals the application to persist the application state.
// Data must return the hash of the state after all changes from this block have been applied.
// It will be used to validate consistency between the applications.
func (c *Coordinator) Commit() types.ResponseCommit {
	c.currAppState.Lock()
	defer c.currAppState.Unlock()
	c.lastAppState.Lock()
	defer c.lastAppState.Unlock()

	// update the block height
	c.currAppState.Height++

	// for all newly received parents, wait until they are solid
	if len(c.currAppState.IssuerCountByParent) > len(c.lastAppState.IssuerCountByParent) {
		processed := map[iotago.MessageID]struct{}{} // avoid processing duplicate parents more than once
		for issuer, msgID := range c.currAppState.ParentByIssuer {
			// skip duplicates and parents already present in the previous block
			if _, has := processed[msgID]; has || c.lastAppState.IssuerCountByParent[msgID] > 0 {
				continue
			}
			c.log.Debugw("awaiting parent", "msgID", msgID)

			issuer, index := issuer, c.currAppState.CurrentMilestoneIndex
			c.registry.RegisterCallback(msgID, func(msgID iotago.MessageID) {
				c.processParent(issuer, index, msgID)
			})

			processed[msgID] = struct{}{}
		}
	}

	// the milestone is done, if we have enough partial signatures
	if len(c.currAppState.SignaturesByIssuer) >= c.committee.T() {
		// create and issue the milestone, if it's index is new and the coordinator is running
		if c.started.Load() && c.currAppState.CurrentMilestoneIndex > uint32(c.nodeBridge.LatestMilestone().Index) {
			// sort partial signatures to generate deterministic a deterministic milestone payload
			partials := make(iotago.Signatures, 0, len(c.currAppState.SignaturesByIssuer))
			for _, signature := range c.currAppState.SignaturesByIssuer {
				partials = append(partials, signature)
			}
			sort.Slice(partials, func(i, j int) bool {
				return bytes.Compare(
					partials[i].(*iotago.Ed25519Signature).PublicKey[:],
					partials[j].(*iotago.Ed25519Signature).PublicKey[:]) < 0
			})

			// TODO: what do we do if this fails?
			go c.createAndSendMilestone(c.ctx, *c.currAppState.Milestone, partials)
		}

		// reset the state for the next milestone
		state := State{
			Height:                c.currAppState.Height,
			CurrentMilestoneIndex: c.currAppState.CurrentMilestoneIndex + 1,
			LastMilestoneID:       c.currAppState.MilestoneID(),
			LastMilestoneMsgID:    MilestoneMessageID(c.currAppState.Milestone),
		}
		c.currAppState.Reset(state)
		c.registry.Clear()
	}

	// persist the application state to DB
	stateBytes := mustMarshal(&c.currAppState)
	if err := c.db.Set(stateDBKey, stateBytes); err != nil {
		c.log.Fatalf("failed to set database coordinator status: %e", err)
	}
	// make a deep copy of the state
	c.lastAppState.Reset(State{})
	if err := c.lastAppState.UnmarshalBinary(stateBytes); err != nil {
		panic(err)
	}
	// compute the hash of that state
	hash := blake2b.Sum256(stateBytes)
	c.lastAppStateHash = hash[:]

	return types.ResponseCommit{Data: c.lastAppStateHash}
}

func (c *Coordinator) processParent(issuer iotago.MilestonePublicKey, index uint32, msgID iotago.MessageID) {
	// ignore parents older than the index in the application state
	if index < c.StateMilestoneIndex() {
		return
	}

	// create a proof referencing this parent
	proof := &tendermint.Proof{Index: index, ParentId: msgID[:]}
	tx, err := c.marshalTx(proof)
	if err != nil {
		panic(err)
	}

	c.log.Debugw("broadcast tx", "proof", proof)

	// submit the proof for broadcast
	// keep at most one parent per issuer in the queue
	member, ok := c.committee.MemberIndex(issuer)
	if !ok {
		panic("coordinator: issuer has no index")
	}
	c.broadcastQueue.Submit(ProofKey+member, tx)
}

func (c *Coordinator) createAndSendMilestone(ctx context.Context, ms iotago.Milestone, signatures iotago.Signatures) error {
	// add the signatures to the milestone
	ms.Signatures = signatures

	if err := ms.VerifySignatures(c.committee.T(), c.committee.Members()); err != nil {
		return fmt.Errorf("validating the signatures failed: %w", err)
	}
	msg, err := builder.NewMessageBuilder(c.protoParas.Version).ParentsMessageIDs(ms.Parents).Payload(&ms).Build()
	if err != nil {
		return fmt.Errorf("building the message failed: %w", err)
	}
	if _, err := msg.Serialize(serializer.DeSeriModePerformValidation, c.protoParas); err != nil {
		return fmt.Errorf("serializing the message failed: %w", err)
	}

	latestMilestoneMessageID, err := c.nodeBridge.EmitMessage(ctx, msg)
	if err != nil {
		return fmt.Errorf("emitting the message failed: %w", err)
	}
	c.log.Debugw("milestone issued", "msgID", latestMilestoneMessageID, "payload", msg.Payload)

	return nil
}

func MilestoneMessageID(ms *iotago.Milestone) iotago.MessageID {
	msg, err := builder.NewMessageBuilder(ms.ProtocolVersion).ParentsMessageIDs(ms.Parents).Payload(ms).Build()
	if err != nil {
		panic(err)
	}
	return msg.MustID()
}
