package decoo

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"
	"time"

	abcitypes "github.com/tendermint/tendermint/abci/types"
	tmcore "github.com/tendermint/tendermint/rpc/core/types"
	tmtypes "github.com/tendermint/tendermint/types"
	"go.uber.org/atomic"

	"github.com/iotaledger/hive.go/core/events"
	"github.com/iotaledger/hive.go/core/logger"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/queue"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
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
	// ErrInvalidParent is returned when a block cannot be the parent of a milestone.
	ErrInvalidParent = errors.New("invalid milestone parent")
)

// INXClient contains the functions used from the INX API.
type INXClient interface {
	ProtocolParameters() *iotago.ProtocolParameters
	LatestMilestone() (*iotago.Milestone, error)
	LatestMilestoneIndex() uint32

	BlockMetadata(context.Context, iotago.BlockID) (*inx.BlockMetadata, error)
	SubmitBlock(context.Context, *iotago.Block) (iotago.BlockID, error)
	ComputeWhiteFlag(ctx context.Context, index uint32, ts uint32, parents iotago.BlockIDs, lastID iotago.MilestoneID) ([]byte, []byte, error)
}

// TangleListener contains the functions used to listen to Tangle changes.
type TangleListener interface {
	RegisterBlockSolidCallback(context.Context, iotago.BlockID, func(*inx.BlockMetadata)) error
	ClearBlockSolidCallbacks()
}

// ABCIClient contains the functions used from the ABCI API.
type ABCIClient interface {
	BroadcastTxSync(context.Context, tmtypes.Tx) (*tmcore.ResultBroadcastTx, error)
}

// ProtocolParametersFunc should return the current valid protocol parameters.
type ProtocolParametersFunc = func() *iotago.ProtocolParameters

// Coordinator is a Tendermint based decentralized coordinator.
type Coordinator struct {
	abcitypes.BaseApplication // act as a ABCI application for Tendermint

	// committee defines the committee of validators.
	committee *Committee

	// maxRetainBlocks defines the block height offset from the current block being committed,
	// such that all blocks past this offset are pruned.
	// A value of 0 indicates that no blocks should be pruned.
	maxRetainBlocks uint
	// whiteFlagTimeout defines the timeout for the ComputeWhiteFlag INX call. Since ComputeWhiteFlag requires all
	// parents to be solid, waiting for them to be solid can take longer than other INX calls which use the INXTimeout.
	whiteFlagTimeout time.Duration

	inxClient INXClient
	listener  TangleListener
	log       *logger.Logger

	//nolint:containedctx // false positive
	ctx                          context.Context
	cancel                       context.CancelFunc
	protoParamsFunc              ProtocolParametersFunc
	stateMilestoneIndexSyncEvent *events.SyncEvent
	broadcastQueue               *queue.KeyedQueue

	// the coordinator ABCI application state controlled by the Tendermint blockchain
	checkState   AppState
	deliverState AppState

	cms CommitStore

	abciClient ABCIClient
	started    atomic.Bool
	stopOnce   sync.Once
}

// New creates a new Coordinator.
func New(committee *Committee, maxRetainBlocks uint, whiteFlagTimeout time.Duration, inxClient INXClient, listener TangleListener, log *logger.Logger) (*Coordinator, error) {
	// there must be space for at least one honest parent in each milestone
	if committee.F()+1 > iotago.BlockMaxParents-1 {
		return nil, ErrTooManyValidators
	}
	// there must be space for one signature per committee member
	if committee.N() > iotago.MaxSignaturesInAMilestone {
		return nil, ErrTooManyValidators
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &Coordinator{
		committee:                    committee,
		maxRetainBlocks:              maxRetainBlocks,
		whiteFlagTimeout:             whiteFlagTimeout,
		inxClient:                    inxClient,
		listener:                     listener,
		log:                          log,
		ctx:                          ctx,
		cancel:                       cancel,
		protoParamsFunc:              inxClient.ProtocolParameters,
		stateMilestoneIndexSyncEvent: events.NewSyncEvent(),
	}

	//nolint:forcetypeassert // we only submit []byte into the queue
	c.broadcastQueue = queue.New(func(i interface{}) error { return c.broadcastTx(i.([]byte)) })

	return c, nil
}

// Bootstrap bootstraps the coordinator with the give state.
func (c *Coordinator) Bootstrap(force bool, index uint32, milestoneID iotago.MilestoneID, milestoneBlockID iotago.BlockID) error {
	// validateLatest bootstrapping parameters against the latest milestone if not forced
	if !force {
		if err := c.validateLatest(index, milestoneID, milestoneBlockID); err != nil {
			return err
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
	c.log.Infow("Coordinator bootstrapped", "state", state)

	return nil
}

// InitState initializes the coordinator to the state corresponding to the given milestone.
func (c *Coordinator) InitState(ms *iotago.Milestone) error {
	state, err := NewStateFromMilestone(ms)
	if err != nil {
		return fmt.Errorf("milestone %d contains invalid metadata: %w", ms.Index, err)
	}

	c.initState(state.MilestoneHeight, state)
	c.log.Infow("Coordinator resumed", "state", state)

	return nil
}

// Start starts the coordinator using the provided Tendermint RPC client.
func (c *Coordinator) Start(client ABCIClient) error {
	c.abciClient = client
	c.started.Store(true)

	c.log.Infow("Coordinator started", "publicKey", c.committee.ID())

	return nil
}

// Stop stops the coordinator.
func (c *Coordinator) Stop() error {
	c.stopOnce.Do(func() {
		c.started.Store(false)
		c.cancel()
		c.broadcastQueue.Stop()
	})

	return nil
}

// Wait waits until the coordinator is stopped.
func (c *Coordinator) Wait() {
	<-c.ctx.Done()
	// call Stop() to assure that Wait() does not return before the termination Stop()
	_ = c.Stop()
}

// PublicKey returns the milestone public key of the instance.
func (c *Coordinator) PublicKey() ed25519.PublicKey {
	return c.committee.PublicKey()
}

// MilestoneIndex returns the milestone index of the current coordinator state.
func (c *Coordinator) MilestoneIndex() uint32 {
	c.checkState.Lock()
	defer c.checkState.Unlock()

	return c.checkState.MilestoneIndex
}

// ProposeParent proposes blockID as a parent for the milestone with the given index.
// It blocks until the proposal has been processed by Tendermint.
func (c *Coordinator) ProposeParent(ctx context.Context, index uint32, blockID iotago.BlockID) error {
	if !c.started.Load() {
		return ErrNotStarted
	}

	parent := &Parent{Index: index, BlockID: blockID}
	tx, err := MarshalTx(c.committee, parent)
	if err != nil {
		panic(err)
	}

	// wait until the state matches the proposal index
	if err := events.WaitForChannelClosed(ctx, c.registerStateMilestoneIndexEvent(index)); err != nil {
		return fmt.Errorf("failed to wait for milestone index %d: %w", index, err)
	}

	// perform a sanity check that the proposed parent is indeed valid
	meta, err := c.inxClient.BlockMetadata(ctx, blockID)
	if err != nil {
		return fmt.Errorf("failed to query BlockMetadata: %w", err)
	}
	if !ValidParent(meta) {
		return ErrInvalidParent
	}

	c.log.Debugw("broadcast tx", "parent", parent)
	res, err := c.abciClient.BroadcastTxSync(ctx, tx)
	if err != nil {
		return err
	}
	if res.Code != CodeTypeOK {
		return fmt.Errorf("failed to broadcast tx: %s", res.Log)
	}

	return nil
}

func (c *Coordinator) validateLatest(index uint32, milestoneID iotago.MilestoneID, milestoneBlockID iotago.BlockID) error {
	latest, err := c.inxLatestMilestone()
	if err != nil {
		return fmt.Errorf("failed to retrieve latest milestone: %w", err)
	}

	// assure that we do not re-bootstrap the network
	if latest != nil {
		if latest.Index != index-1 {
			return fmt.Errorf("latest milestone %d is incompatible: Index: expected=%d actual=%d", latest.Index, index-1, latest.Index)
		}
		if id := latest.MustID(); id != milestoneID {
			return fmt.Errorf("latest milestone %d is incompatible: MilestoneID: expected=%s actual=%s", latest.Index, milestoneID, id)
		}
		if id := MilestoneBlockID(latest); id != milestoneBlockID {
			return fmt.Errorf("latest milestone %d is incompatible: MilestoneBlockID: expected=%s actual=%s", latest.Index, milestoneBlockID, id)
		}
	}

	return nil
}

func (c *Coordinator) initState(height int64, state *State) {
	c.checkState.Lock()
	defer c.checkState.Unlock()
	c.deliverState.Lock()
	defer c.deliverState.Unlock()

	c.checkState.Reset(state)
	c.deliverState.Reset(state)
	c.cms.info = CommitInfo{Height: height, Hash: c.deliverState.Hash()}
}

func (c *Coordinator) registerStateMilestoneIndexEvent(index uint32) chan struct{} {
	ch := c.stateMilestoneIndexSyncEvent.RegisterEvent(index)
	if index <= c.MilestoneIndex() {
		c.stateMilestoneIndexSyncEvent.Trigger(index)
	}

	return ch
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
