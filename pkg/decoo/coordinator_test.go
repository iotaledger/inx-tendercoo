package decoo_test

import (
	"context"
	"crypto/ed25519"
	"sync"
	"testing"
	"time"

	"github.com/iotaledger/hive.go/events"
	"github.com/iotaledger/hive.go/logger"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/stretchr/testify/require"
	abcitypes "github.com/tendermint/tendermint/abci/types"
	proto "github.com/tendermint/tendermint/proto/tendermint/types"
	tmcore "github.com/tendermint/tendermint/rpc/coretypes"
	tmtypes "github.com/tendermint/tendermint/types"
)

var (
	seed      = [ed25519.SeedSize]byte{42}
	private   = ed25519.NewKeyFromSeed(seed[:])
	public    = private.Public().(ed25519.PublicKey)
	committee = decoo.NewCommittee(private, public)

	closed = func() chan struct{} {
		c := make(chan struct{})
		close(c)
		return c
	}()
)

func TestSingleValidator(t *testing.T) {
	inx := &INXMock{
		t:                   t,
		solidBlocks:         map[iotago.BlockID]struct{}{iotago.EmptyBlockID(): {}},
		blockSolidSyncEvent: events.NewSyncEvent(),
	}

	t.Run("bootstrap", func(t *testing.T) {
		c, err := decoo.New(committee, inx, inx, logger.NewExampleLogger(""))
		require.NoError(t, err)

		require.NoError(t, c.InitState(true, 1, [32]byte{}, [32]byte{}))
		require.NoError(t, c.Start(context.Background(), &ABCIMock{c}))

		for i := uint32(1); i < 10; i++ {
			require.NoError(t, c.ProposeParent(i, inx.LatestMilestoneBlockID()))
			require.Eventually(t, func() bool { return inx.LatestMilestoneIndex() == i }, time.Second, 10*time.Millisecond)
		}
		require.NoError(t, c.ProposeParent(inx.LatestMilestoneIndex()+1, inx.LatestMilestoneBlockID()))
		require.NoError(t, c.Stop())
	})

	t.Run("resume", func(t *testing.T) {
		c, err := decoo.New(committee, inx, inx, logger.NewExampleLogger(""))
		require.NoError(t, err)

		require.NoError(t, c.InitState(false, 0, [32]byte{}, [32]byte{}))
		require.NoError(t, c.Start(context.Background(), &ABCIMock{c}))
		require.NoError(t, c.Stop())
	})
}

type INXMock struct {
	sync.Mutex

	t require.TestingT

	milestones          []*iotago.Block
	solidBlocks         map[iotago.BlockID]struct{}
	blockSolidSyncEvent *events.SyncEvent
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

func (m *INXMock) SubmitBlock(_ context.Context, block *iotago.Block) (iotago.BlockID, error) {
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
		require.Equal(m.t, m.latestMilestoneID(), ms.PreviousMilestoneID)
		require.Contains(m.t, ms.Parents, m.latestMilestoneBlockID())
		require.Equal(m.t, block.Parents, ms.Parents)
		require.NoError(m.t, ms.VerifySignatures(committee.T(), committee.Members()))

		// state must be valid
		state, err := decoo.NewStateFromMilestone(ms)
		require.NoError(m.t, err)
		require.Equal(m.t, ms.Index, state.MilestoneIndex)
		require.Equal(m.t, m.latestMilestoneID(), state.LastMilestoneID)
		require.Equal(m.t, m.latestMilestoneBlockID(), state.LastMilestoneBlockID)

		m.milestones = append(m.milestones, block)
	}

	m.solidBlocks[id] = struct{}{}
	m.blockSolidSyncEvent.Trigger(id)
	return id, nil
}

func (m *INXMock) ComputeWhiteFlag(_ context.Context, _ uint32, _ uint32, _ iotago.BlockIDs, _ iotago.MilestoneID) ([]byte, []byte, error) {
	return make([]byte, iotago.MilestoneMerkleProofLength), make([]byte, iotago.MilestoneMerkleProofLength), nil
}

func (m *INXMock) RegisterBlockSolidEvent(id iotago.BlockID) chan struct{} {
	m.Lock()
	defer m.Unlock()
	if _, ok := m.solidBlocks[id]; ok {
		return closed
	}
	return m.blockSolidSyncEvent.RegisterEvent(id)
}

func (m *INXMock) DeregisterBlockSolidEvent(id iotago.BlockID) {
	m.blockSolidSyncEvent.DeregisterEvent(id)
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

type ABCIMock struct{ *decoo.Coordinator }

func (m *ABCIMock) BroadcastTxSync(_ context.Context, tx tmtypes.Tx) (*tmcore.ResultBroadcastTx, error) {
	// trigger a new block containing that transaction
	m.BeginBlock(abcitypes.RequestBeginBlock{Header: proto.Header{Time: time.Now()}})
	m.DeliverTx(abcitypes.RequestDeliverTx{Tx: tx})
	m.EndBlock(abcitypes.RequestEndBlock{})
	m.Commit()
	return nil, nil
}
