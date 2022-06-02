package decoo

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/hive.go/logger"
	"github.com/iotaledger/inx-app/nodebridge"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/proto/tendermint"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/queue"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/registry"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/rpc/client"
	"go.uber.org/atomic"
	"golang.org/x/crypto/blake2b"
	"google.golang.org/protobuf/proto"
)

const (
	// ProtocolVersion defines the version of the coordinator Tendermint application.
	ProtocolVersion uint64 = 0x1
)

var (
	// ErrNetworkBootstrapped is returned when the flag for bootstrap network was given, but a state file already exists.
	ErrNetworkBootstrapped = errors.New("network already bootstrapped")
	// ErrTooManyValidators is returned when the committee size is too large.
	ErrTooManyValidators = errors.New("too many validators")
	// ErrNotStarted is returned when the coordinator has not been started.
	ErrNotStarted = errors.New("coordinator not started")
	// ErrIndexBehindAppState is returned when the proposed index is older than the app state.
	ErrIndexBehindAppState = errors.New("milestone index behind app state")
)

var (
	stateDBKey = []byte("dbState")
)

// keys for the broadcastQueue
const (
	ParentKey = iota
	PartialKey

	ProofKey = 0xff // proofs are special as there can be multiple proofs in the queue at the same time
)

type State struct {
	// Height denotes the height of the current Tendermint blockchain.
	Height int64
	// CurrentMilestoneIndex denotes the index of the milestone that is currently being constructed.
	CurrentMilestoneIndex uint32
	// LastMilestoneID denotes the ID of the previous milestone.
	LastMilestoneID iotago.MilestoneID
	// LastMilestoneMsgID denotes the ID of a message containing the previous milestone.
	LastMilestoneMsgID iotago.BlockID
}

// Coordinator is a Tendermint based decentralized coordinator.
type Coordinator struct {
	types.BaseApplication // act as a ABCI application for Tendermint

	db         kvstore.KVStore
	committee  *Committee
	nodeBridge *nodebridge.NodeBridge
	log        *logger.Logger

	// the coordinator ABCI application state controlled by the Tendermint blockchain
	currAppState     AppState
	lastAppState     AppState
	lastAppStateHash []byte

	ctx     context.Context
	client  client.Client // Tendermint RPC
	started atomic.Bool

	broadcastQueue *queue.KeyedQueue
	protoParas     *iotago.ProtocolParameters
	registry       *registry.Registry
}

// New creates a new Coordinator.
func New(db kvstore.KVStore, committee *Committee, nodeBridge *nodebridge.NodeBridge, tangleListener *nodebridge.TangleListener, log *logger.Logger) (*Coordinator, error) {
	// there must be at least one honest parent in each milestone
	if committee.T() > iotago.BlockMaxParents-1 {
		return nil, ErrTooManyValidators
	}
	// there must be space for one signature per committee member
	if committee.N() > iotago.MaxSignaturesInAMilestone {
		return nil, ErrTooManyValidators
	}

	c := &Coordinator{
		db:         db,
		committee:  committee,
		nodeBridge: nodeBridge,
		log:        log,
		protoParas: nodeBridge.NodeConfig.UnwrapProtocolParameters(),
		// TODO: fix context
		registry: registry.New(context.Background(), tangleListener),
	}
	c.broadcastQueue = queue.New(func(i interface{}) error { return c.broadcastTx(i.([]byte)) })
	return c, nil
}

// InitState initializes the Coordinator.
// It needs to be called before Start.
func (c *Coordinator) InitState(bootstrap bool, state *State) error {
	stateExists, err := c.db.Has(stateDBKey)
	if err != nil {
		return err
	}

	c.currAppState.Lock()
	defer c.currAppState.Unlock()
	c.lastAppState.Lock()
	defer c.lastAppState.Unlock()

	if bootstrap {
		if stateExists {
			return ErrNetworkBootstrapped
		}

		// there is no need to specify the Tendermint block height during bootstrapping
		// if a Tendermint blockchain is present, the state can be reconstructed from it, and it is not bootstrapping
		c.currAppState.Reset(*state)
		c.lastAppState.Reset(*state)
		c.lastAppStateHash = c.lastAppState.Hash()

		c.log.Infow("coordinator bootstrapped", "state", state)
		return nil
	}

	if err := c.loadAppState(); err != nil {
		return err
	}

	c.log.Infow("coordinator resumed", "index", c.currAppState.CurrentMilestoneIndex, "previousMilestoneID", c.currAppState.LastMilestoneID)
	return nil
}

// Start starts the coordinator using the provided Tendermint RPC client.
func (c *Coordinator) Start(ctx context.Context, cl client.Client) error {
	if !cl.IsRunning() {
		return errors.New("client not running")
	}
	c.ctx = ctx
	c.client = cl
	c.started.Store(true)

	c.log.Infow("coordinator stated", "pubKey", c.committee.ID(), "validators", c.committee)
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
	return c.lastAppState.CurrentMilestoneIndex
}

// ProposeParent proposes a parent for the milestone with the given index.
func (c *Coordinator) ProposeParent(index uint32, msgID iotago.BlockID) error {
	// ignore this request, if the index is in the past
	if index < c.StateMilestoneIndex() {
		return ErrIndexBehindAppState
	}
	if !c.started.Load() {
		return ErrNotStarted
	}

	parent := &tendermint.Parent{Index: index, MessageId: msgID[:]}
	tx, err := c.marshalTx(parent)
	if err != nil {
		panic(err)
	}

	type stripped *tendermint.Parent // ignore ugly protobuf String() method
	c.log.Debugw("broadcast tx", "parent", stripped(parent))
	c.broadcastQueue.Submit(ParentKey, tx)
	return nil
}

func (c *Coordinator) loadAppState() error {
	data, err := c.db.Get(stateDBKey)
	if err != nil {
		return fmt.Errorf("failed to get database Coordinator status: %w", err)
	}

	if err := c.currAppState.UnmarshalBinary(data); err != nil {
		return fmt.Errorf("invalid database Coordinator status data: %w", err)
	}
	if err := c.lastAppState.UnmarshalBinary(data); err != nil {
		return fmt.Errorf("invalid database Coordinator status data: %w", err)
	}
	hash := blake2b.Sum256(data)
	c.lastAppStateHash = hash[:]
	return nil
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
		return errors.New("not running")
	}
	_, err := c.client.BroadcastTxSync(c.ctx, tx)
	return err
}
