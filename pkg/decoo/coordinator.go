package decoo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/iotaledger/hive.go/logger"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/queue"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/registry"
	iotago "github.com/iotaledger/iota.go/v3"
	abcitypes "github.com/tendermint/tendermint/abci/types"
	tmcore "github.com/tendermint/tendermint/rpc/coretypes"
	tmtypes "github.com/tendermint/tendermint/types"
	"go.uber.org/atomic"
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
)

// keys for the broadcastQueue
const (
	ParentKey = iota
	PartialKey

	ProofKey = 0xff // proofs are special as there can be multiple proofs in the queue at the same time
)

// INXClient contains the functions used from the INX API.
type INXClient interface {
	ProtocolParameters() *iotago.ProtocolParameters
	LatestMilestone() (*iotago.Milestone, error)
	ValidTip(iotago.BlockID) (bool, error)

	SubmitBlock(context.Context, *iotago.Block) (iotago.BlockID, error)
	ComputeWhiteFlag(ctx context.Context, index uint32, timestamp uint32, parents iotago.BlockIDs, lastID iotago.MilestoneID) ([]byte, []byte, error)
}

// ABCIClient contains the functions used from the ABCI API.
type ABCIClient interface {
	BroadcastTxSync(context.Context, tmtypes.Tx) (*tmcore.ResultBroadcastTx, error)
}

// Coordinator is a Tendermint based decentralized coordinator.
type Coordinator struct {
	abcitypes.BaseApplication // act as a ABCI application for Tendermint

	committee      *Committee
	inxClient      INXClient
	registry       *registry.Registry
	log            *logger.Logger
	protoParas     *iotago.ProtocolParameters
	broadcastQueue *queue.KeyedQueue

	// the coordinator ABCI application state controlled by the Tendermint blockchain
	checkState   AppState
	deliverState AppState

	blockTime time.Time

	ctx        context.Context
	abciClient ABCIClient
	started    atomic.Bool
}

// New creates a new Coordinator.
func New(committee *Committee, inxClient INXClient, registerer registry.EventRegisterer, log *logger.Logger) (*Coordinator, error) {
	// there must be space for at least one honest parent in each milestone
	if committee.N()/3+1 > iotago.BlockMaxParents-1 {
		return nil, ErrTooManyValidators
	}
	// there must be space for one signature per committee member
	if committee.N() > iotago.MaxSignaturesInAMilestone {
		return nil, ErrTooManyValidators
	}

	c := &Coordinator{
		committee:  committee,
		inxClient:  inxClient,
		registry:   registry.New(registerer),
		log:        log,
		protoParas: inxClient.ProtocolParameters(),
	}
	c.broadcastQueue = queue.New(func(i interface{}) error { return c.broadcastTx(i.([]byte)) })
	return c, nil
}

// InitState initializes the Coordinator.
// It needs to be called before Start.
func (c *Coordinator) InitState(bootstrap bool, index uint32, milestoneID iotago.MilestoneID, milestoneBlockID iotago.BlockID) error {
	latest, err := c.inxClient.LatestMilestone()
	if err != nil {
		return fmt.Errorf("failed to get latest milestone: %w", err)
	}

	// try to resume the network
	if !bootstrap {
		if latest == nil {
			return fmt.Errorf("resume failed: no milestone available")
		}
		state, err := NewStateFromMilestone(latest)
		if err != nil {
			return fmt.Errorf("resume failed: milestone %d contains invalid metadata: %w", latest.Index, err)
		}

		c.initState(state.MilestoneHeight, state)
		c.log.Infow("coordinator resumed", "state", state)
		return nil
	}

	// assure that we do not re-bootstrap a network
	if latest != nil {
		if _, err := NewStateFromMilestone(latest); err == nil {
			return fmt.Errorf("bootstrap failed: milestone %d contains a valid state", latest.Index)
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
func (c *Coordinator) Start(ctx context.Context, client ABCIClient) error {
	c.ctx = ctx
	c.abciClient = client
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

// ProposeParent proposes a parent for the milestone with the given index.
func (c *Coordinator) ProposeParent(index uint32, blockID iotago.BlockID) error {
	if !c.started.Load() {
		return ErrNotStarted
	}

	parent := &Parent{Index: index, BlockID: blockID}
	tx, err := MarshalTx(c.committee, parent)
	if err != nil {
		panic(err)
	}

	c.log.Debugw("broadcast tx", "parent", parent)
	_, err = c.abciClient.BroadcastTxSync(c.ctx, tx)
	return err
}

func (c *Coordinator) initState(height int64, state *State) {
	c.checkState.Lock()
	defer c.checkState.Unlock()
	c.deliverState.Lock()
	defer c.deliverState.Unlock()

	c.checkState.Reset(height, state)
	c.deliverState.Reset(height, state)
}

func (c *Coordinator) broadcastTx(tx []byte) error {
	if !c.started.Load() {
		return ErrNotStarted
	}
	res, err := c.abciClient.BroadcastTxSync(c.ctx, tx)
	if err == nil && res.Code != CodeTypeOK {
		c.log.Warnf("broadcast did not pass CheckTx: %s", res.Log)
	}
	return err
}
