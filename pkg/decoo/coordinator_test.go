package decoo

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	abcitypes "github.com/tendermint/tendermint/abci/types"
	tmcore "github.com/tendermint/tendermint/rpc/core/types"
	tmtypes "github.com/tendermint/tendermint/types"

	"github.com/iotaledger/hive.go/core/logger"
	inxutils "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/builder"
)

const (
	waitFor = 2 * time.Second
	tick    = 10 * time.Millisecond
)

func TestSingleValidator(t *testing.T) {
	inx := &INXMock{
		t:                   t,
		solidBlocks:         map[iotago.BlockID]struct{}{iotago.EmptyBlockID(): {}},
		blockSolidCallbacks: map[iotago.BlockID]func(*inxutils.BlockMetadata){},
	}
	abci := &ABCIMock{}
	privates, publics := generateTestKeys(1)
	committee := NewCommittee(privates[0], publics...)

	t.Run("bootstrap", func(t *testing.T) {
		c, err := New(committee, inx, inx, logger.NewExampleLogger(""))
		require.NoError(t, err)

		require.NoError(t, c.Bootstrap(false, 1, [32]byte{}, [32]byte{}))
		abci.AddCoordinator(c)
		require.NoError(t, c.Start(abci))

		for i := uint32(1); i < 10; i++ {
			require.NoError(t, c.ProposeParent(i, inx.LatestMilestoneBlockID()))
			require.Eventually(t, func() bool { return inx.LatestMilestoneIndex() == i }, waitFor, tick)
		}
		require.NoError(t, c.ProposeParent(inx.LatestMilestoneIndex()+1, inx.LatestMilestoneBlockID()))
		require.NoError(t, c.Stop())
	})

	t.Run("resume", func(t *testing.T) {
		c, err := New(committee, inx, inx, logger.NewExampleLogger(""))
		require.NoError(t, err)

		// init from state and replay missing transactions
		ms, err := inx.LatestMilestone()
		require.NoError(t, err)
		require.NoError(t, c.InitState(ms))
		abci.AddCoordinator(c)
		abci.Replay()
		require.NoError(t, c.Start(abci))

		index := inx.LatestMilestoneIndex() + 1
		require.NoError(t, c.ProposeParent(index, inx.LatestMilestoneBlockID()))
		require.Eventually(t, func() bool { return inx.LatestMilestoneIndex() == index }, waitFor, tick)
		require.NoError(t, c.Stop())
	})
}

func TestManyValidator(t *testing.T) {
	inx := &INXMock{
		t:                   t,
		solidBlocks:         map[iotago.BlockID]struct{}{iotago.EmptyBlockID(): {}},
		blockSolidCallbacks: map[iotago.BlockID]func(*inxutils.BlockMetadata){},
	}
	abci := &ABCIMock{}

	const N = 10
	privates, publics := generateTestKeys(N)
	for i := 0; i < N; i++ {
		committee := NewCommittee(privates[i], publics...)
		c, err := New(committee, inx, inx, logger.NewExampleLogger(fmt.Sprintf("coo-%d", i)))
		require.NoError(t, err)

		require.NoError(t, c.Bootstrap(false, 1, [32]byte{}, [32]byte{}))
		abci.AddCoordinator(c)
		require.NoError(t, c.Start(abci))
		committee.f = N - 1 // require each node to send a proof and parent
	}

	for i, c := range abci.Apps {
		payload := &iotago.TaggedData{Data: make([]byte, binary.MaxVarintLen64)}
		binary.PutVarint(payload.Data, int64(i))
		block, err := builder.NewBlockBuilder().Payload(payload).Parents(iotago.BlockIDs{inx.LatestMilestoneBlockID()}).ProofOfWork(context.Background(), nil, 0.01, 1).Build()
		require.NoError(t, err)

		id, err := inx.SubmitBlock(context.Background(), block)
		require.NoError(t, err)

		require.NoError(t, c.ProposeParent(1, id))
	}

	require.Eventually(t, func() bool { return inx.LatestMilestoneIndex() == 1 }, waitFor, tick)
}

func generateTestKeys(n int) ([]ed25519.PrivateKey, []ed25519.PublicKey) {
	var privates []ed25519.PrivateKey
	var publics []ed25519.PublicKey
	for i := 0; i < n; i++ {
		var seed [ed25519.SeedSize]byte
		binary.PutVarint(seed[:], int64(i))
		private := ed25519.NewKeyFromSeed(seed[:])

		privates = append(privates, private)
		publics = append(publics, private.Public().(ed25519.PublicKey))
	}

	return privates, publics
}

type INXMock struct {
	sync.Mutex

	t require.TestingT

	milestones          []*iotago.Block
	solidBlocks         map[iotago.BlockID]struct{}
	blockSolidCallbacks map[iotago.BlockID]func(*inxutils.BlockMetadata)
}

func (m *INXMock) ProtocolParameters() *iotago.ProtocolParameters {
	return &iotago.ProtocolParameters{}
}

func (m *INXMock) LatestMilestone() (*iotago.Milestone, error) {
	m.Lock()
	defer m.Unlock()
	if block := m.latestMilestoneBlock(); block != nil {
		return block.Payload.(*iotago.Milestone), nil
	}
	return nil, nil
}

func (m *INXMock) ValidTip(id iotago.BlockID) (bool, error) {
	m.Lock()
	defer m.Unlock()
	_, solid := m.solidBlocks[id]
	return solid, nil
}

func (m *INXMock) BlockMetadata(iotago.BlockID) (*inxutils.BlockMetadata, error) {
	panic("not implemented")
}

func (m *INXMock) SubmitBlock(ctx context.Context, block *iotago.Block) (iotago.BlockID, error) {
	require.NotNil(m.t, ctx)

	m.Lock()
	defer m.Unlock()
	id := block.MustID()
	if _, ok := m.solidBlocks[id]; ok {
		return id, nil
	}
	// block must be solid
	for _, parent := range block.Parents {
		require.Contains(m.t, m.solidBlocks, parent)
	}

	// handle milestones
	if ms, ok := block.Payload.(*iotago.Milestone); ok {
		// milestone must be valid
		require.EqualValues(m.t, len(m.milestones)+1, ms.Index)
		require.Equal(m.t, block.ProtocolVersion, ms.ProtocolVersion)
		require.Equal(m.t, m.latestMilestoneID(), ms.PreviousMilestoneID)
		require.Contains(m.t, ms.Parents, m.latestMilestoneBlockID())
		require.Equal(m.t, block.Parents, ms.Parents)

		// state must be valid
		state, err := NewStateFromMilestone(ms)
		require.NoError(m.t, err)
		require.Equal(m.t, ms.Index, state.MilestoneIndex)
		require.Equal(m.t, m.latestMilestoneID(), state.LastMilestoneID)
		require.Equal(m.t, m.latestMilestoneBlockID(), state.LastMilestoneBlockID)

		m.milestones = append(m.milestones, block)
	}

	m.solidBlocks[id] = struct{}{}
	if f, ok := m.blockSolidCallbacks[id]; ok {
		go f(&inxutils.BlockMetadata{BlockId: inxutils.NewBlockId(id), Solid: true})
		delete(m.blockSolidCallbacks, id)
	}
	return id, nil
}

func (m *INXMock) ComputeWhiteFlag(ctx context.Context, index uint32, _ uint32, _ iotago.BlockIDs, _ iotago.MilestoneID) ([]byte, []byte, error) {
	require.NotNil(m.t, ctx)
	require.LessOrEqual(m.t, index, m.LatestMilestoneIndex()+1)

	return make([]byte, iotago.MilestoneMerkleProofLength), make([]byte, iotago.MilestoneMerkleProofLength), nil
}

func (m *INXMock) RegisterBlockSolidCallback(id iotago.BlockID, f func(*inxutils.BlockMetadata)) error {
	m.Lock()
	defer m.Unlock()
	if _, ok := m.solidBlocks[id]; ok {
		go f(&inxutils.BlockMetadata{BlockId: inxutils.NewBlockId(id), Solid: true})
	}
	m.blockSolidCallbacks[id] = f
	return nil
}

func (m *INXMock) ClearBlockSolidCallbacks() {
	m.Lock()
	defer m.Unlock()
	m.blockSolidCallbacks = map[iotago.BlockID]func(*inxutils.BlockMetadata){}
}

func (m *INXMock) LatestMilestoneBlockID() iotago.BlockID {
	m.Lock()
	defer m.Unlock()
	return m.latestMilestoneBlockID()
}

func (m *INXMock) LatestMilestoneIndex() uint32 {
	m.Lock()
	defer m.Unlock()
	if block := m.latestMilestoneBlock(); block != nil {
		return block.Payload.(*iotago.Milestone).Index
	}
	return 0
}

func (m *INXMock) latestMilestoneBlock() *iotago.Block {
	if len(m.milestones) == 0 {
		return nil
	}
	return m.milestones[len(m.milestones)-1]
}

func (m *INXMock) latestMilestoneBlockID() iotago.BlockID {
	if block := m.latestMilestoneBlock(); block != nil {
		return block.MustID()
	}
	return iotago.EmptyBlockID()
}

func (m *INXMock) latestMilestoneID() iotago.MilestoneID {
	if block := m.latestMilestoneBlock(); block != nil {
		id, _ := block.Payload.(*iotago.Milestone).ID()
		return id
	}
	return [32]byte{}
}

type ABCIMock struct {
	sync.Mutex

	Apps []*Coordinator
	Txs  []tmtypes.Tx
}

func (m *ABCIMock) AddCoordinator(app *Coordinator) {
	m.Lock()
	defer m.Unlock()

	m.Apps = append(m.Apps, app)
}

func (m *ABCIMock) BroadcastTxSync(ctx context.Context, tx tmtypes.Tx) (*tmcore.ResultBroadcastTx, error) {
	m.Lock()
	defer m.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// trigger a new block containing that transaction on every application
	for _, app := range m.Apps {
		app.BeginBlock(abcitypes.RequestBeginBlock{})
		app.DeliverTx(abcitypes.RequestDeliverTx{Tx: tx})
		app.EndBlock(abcitypes.RequestEndBlock{})
		app.Commit()
	}

	m.Txs = append(m.Txs, tx)
	return &tmcore.ResultBroadcastTx{}, nil
}

func (m *ABCIMock) Replay() {
	m.Lock()
	defer m.Unlock()

	for _, app := range m.Apps {
		resp := app.Info(abcitypes.RequestInfo{})
		for i := resp.LastBlockHeight; i < int64(len(m.Txs)); i++ {
			app.BeginBlock(abcitypes.RequestBeginBlock{})
			app.DeliverTx(abcitypes.RequestDeliverTx{Tx: m.Txs[i]})
			app.EndBlock(abcitypes.RequestEndBlock{})
			app.Commit()
		}
	}
}
