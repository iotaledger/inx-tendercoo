package decoo_test

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/tpkg"
)

func TestState(t *testing.T) {
	s := &decoo.State{
		MilestoneHeight:      rand.Int63(),
		MilestoneIndex:       rand.Uint32(),
		LastMilestoneID:      tpkg.Rand32ByteArray(),
		LastMilestoneBlockID: tpkg.Rand32ByteArray(),
	}
	ms := iotago.NewMilestone(s.MilestoneIndex, 0, 0, s.LastMilestoneID, []iotago.BlockID{iotago.EmptyBlockID()}, [32]byte{}, [32]byte{})
	ms.Metadata = s.Metadata()
	t.Logf("%+v\n", ms)

	s2, err := decoo.NewStateFromMilestone(ms)
	require.NoError(t, err)
	require.Equal(t, s, s2)
}
