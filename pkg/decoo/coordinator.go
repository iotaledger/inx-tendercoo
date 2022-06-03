package decoo

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/iotaledger/hive.go/logger"
	"github.com/iotaledger/inx-app/nodebridge"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/proto/tendermint"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/queue"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/registry"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/rpc/client"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
)

const (
	// ProtocolVersion defines the version of the coordinator Tendermint application.
	ProtocolVersion uint64 = 0x1
)

var (
	// ErrTooManyValidators is returned when the committee size is too large.
	ErrTooManyValidators = errors.New("too many validators")
	// ErrNotStarted is returned when the coordinator has not been started.
	ErrNotStarted = errors.New("coordinator not started")
	// ErrIndexBehindAppState is returned when the proposed index is older than the app state.
	ErrIndexBehindAppState = errors.New("milestone index behind app state")
)

// keys for the broadcastQueue
const (
	ParentKey = iota
	PartialKey

	ProofKey = 0xff // proofs are special as there can be multiple proofs in the queue at the same time
)

// Coordinator is a Tendermint based decentralized coordinator.
type Coordinator struct {
	types.BaseApplication // act as a ABCI application for Tendermint

	committee  *Committee
	nodeBridge *nodebridge.NodeBridge
	log        *logger.Logger

	// the coordinator ABCI application state controlled by the Tendermint blockchain
	currAppState AppState
	lastAppState AppState

	ctx     context.Context
	client  client.Client // Tendermint RPC
	started atomic.Bool

	broadcastQueue *queue.KeyedQueue
	protoParas     *iotago.ProtocolParameters
	registry       *registry.Registry
}

// New creates a new Coordinator.
func New(committee *Committee, nodeBridge *nodebridge.NodeBridge, tangleListener *nodebridge.TangleListener, log *logger.Logger) (*Coordinator, error) {
	// there must be at least one honest parent in each milestone
	if committee.T() > iotago.BlockMaxParents-1 {
		return nil, ErrTooManyValidators
	}
	// there must be space for one signature per committee member
	if committee.N() > iotago.MaxSignaturesInAMilestone {
		return nil, ErrTooManyValidators
	}

	c := &Coordinator{
		committee:  committee,
		nodeBridge: nodeBridge,
		log:        log,
		protoParas: nodeBridge.NodeConfig.UnwrapProtocolParameters(),
		registry:   registry.New(tangleListener),
	}
	c.broadcastQueue = queue.New(func(i interface{}) error { return c.broadcastTx(i.([]byte)) })
	return c, nil
}

// InitState initializes the Coordinator.
// It needs to be called before Start.
func (c *Coordinator) InitState(bootstrap bool, index uint32, milestoneID iotago.MilestoneID, milestoneBlockID iotago.BlockID) error {
	latest, err := c.nodeBridge.LatestMilestone()
	if err != nil {
		return fmt.Errorf("failed to get latest milestone: %w", err)
	}

	// try to resume the network
	if !bootstrap {
		if latest == nil {
			return fmt.Errorf("resume failed: no milestone available")
		}
		state, err := NewStateFromMilestone(latest.Milestone)
		if err != nil {
			return fmt.Errorf("resume failed: milestone %d contains invalid metadata: %w", latest.Milestone.Index, err)
		}

		c.initState(state.MilestoneHeight, state)
		c.log.Infow("coordinator resumed", "state", state)
		return nil
	}

	// assure that we do not re-bootstrap a network
	if latest != nil {
		if _, err := NewStateFromMilestone(latest.Milestone); err == nil {
			return fmt.Errorf("bootstrap failed: milestone %d contains a valid state", latest.Milestone.Index)
		}
	}

	// create a genesis state
	state := &State{
		MilestoneHeight:      0,
		MilestoneIndex:       index,
		LastMilestoneID:      milestoneID,
		LastMilestoneBlockID: milestoneBlockID,
	}
	c.initState(0, state)
	c.log.Infow("coordinator bootstrapped", "state", state)
	return nil
}

// Start starts the coordinator using the provided Tendermint RPC client.
func (c *Coordinator) Start(ctx context.Context, cl client.Client) error {
	c.ctx = ctx
	c.client = cl
	c.started.Store(true)

	c.log.Infow("coordinator started", "pubKey", c.committee.ID())
	return nil
}

// Stop stops the coordinator.
func (c *Coordinator) Stop() error {
	c.started.Store(false)
	if err := c.registry.Close(); err != nil {
		return err
	}
	c.broadcastQueue.Stop()
	return nil
}

// StateMilestoneIndex returns the milestone index of the ABCI application state.
func (c *Coordinator) StateMilestoneIndex() uint32 {
	c.lastAppState.RLock()
	defer c.lastAppState.RUnlock()
	return c.lastAppState.MilestoneIndex
}

// ProposeParent proposes a parent for the milestone with the given index.
func (c *Coordinator) ProposeParent(index uint32, blockID iotago.BlockID) error {
	// ignore this request, if the index is in the past
	if index < c.StateMilestoneIndex() {
		return ErrIndexBehindAppState
	}
	if !c.started.Load() {
		return ErrNotStarted
	}

	parent := &tendermint.Parent{Index: index, BlockId: blockID[:]}
	tx, err := c.marshalTx(parent)
	if err != nil {
		panic(err)
	}

	c.log.Debugw("broadcast parent", "Index", index, "BlockId", blockID)
	c.broadcastQueue.Submit(ParentKey, tx)
	return nil
}

func (c *Coordinator) initState(height int64, state *State) {
	c.currAppState.Lock()
	defer c.currAppState.Unlock()
	c.lastAppState.Lock()
	defer c.lastAppState.Unlock()

	c.currAppState.Reset(height, state)
	c.lastAppState.Reset(height, state)
}

// unmarshalTx parses the wire-format message in b and returns the verified issuer as well as the message m.
func (c *Coordinator) unmarshalTx(b []byte) (ed25519.PublicKey, proto.Message, error) {
	txRaw := &tendermint.TxRaw{}
	if err := proto.Unmarshal(b, txRaw); err != nil {
		return nil, nil, err
	}
	if err := c.committee.VerifySingle(txRaw.GetEssence(), txRaw.GetPublicKey(), txRaw.GetSignature()); err != nil {
		return nil, nil, err
	}

	txEssence := &tendermint.Essence{}
	if err := proto.Unmarshal(txRaw.GetEssence(), txEssence); err != nil {
		return nil, nil, err
	}
	msg, err := txEssence.Unwrap()
	if err != nil {
		return nil, nil, err
	}
	return txRaw.GetPublicKey(), msg, nil
}

// marshalTx returns the wire-format encoding of m which can then be passed to Tendermint.
func (c *Coordinator) marshalTx(m proto.Message) ([]byte, error) {
	txEssence := &tendermint.Essence{}
	if err := txEssence.Wrap(m); err != nil {
		return nil, err
	}

	essence, err := proto.Marshal(txEssence)
	if err != nil {
		return nil, err
	}
	txRaw := &tendermint.TxRaw{
		Essence:   essence,
		PublicKey: c.committee.PublicKey(),
		Signature: c.committee.Sign(essence).Signature[:],
	}
	return proto.Marshal(txRaw)
}

func (c *Coordinator) broadcastTx(tx []byte) error {
	if !c.started.Load() {
		return ErrNotStarted
	}
	_, err := c.client.BroadcastTxSync(c.ctx, tx)
	return err
}
