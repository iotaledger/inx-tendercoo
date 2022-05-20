package decoo_test

import (
	"math/rand"
	"testing"

	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/tpkg"
	"github.com/stretchr/testify/require"
)

type void = struct{}

func TestState_Encoding(t *testing.T) {
	// create random state
	s := &decoo.AppState{
		State: decoo.State{
			Height:                rand.Int63(),
			CurrentMilestoneIndex: rand.Uint32(),
			LastMilestoneID:       tpkg.Rand32ByteArray(),
			LastMilestoneMsgID:    tpkg.Rand32ByteArray(),
		},
		Timestamp: rand.Uint32(),
		ParentByIssuer: map[types.Byte32]iotago.BlockID{
			tpkg.Rand32ByteArray(): tpkg.Rand32ByteArray(),
			tpkg.Rand32ByteArray(): tpkg.Rand32ByteArray(),
		},
		IssuerCountByParent: map[types.Byte32]int{
			tpkg.Rand32ByteArray(): rand.Int(),
			tpkg.Rand32ByteArray(): rand.Int(),
		},
		ProofsByMsgID: map[types.Byte32]map[types.Byte32]void{
			tpkg.Rand32ByteArray(): {tpkg.Rand32ByteArray(): void{}, tpkg.Rand32ByteArray(): void{}},
			tpkg.Rand32ByteArray(): {tpkg.Rand32ByteArray(): void{}, tpkg.Rand32ByteArray(): void{}},
		},
		Milestone: tpkg.RandMilestone(nil),
		SignaturesByIssuer: map[types.Byte32]*iotago.Ed25519Signature{
			tpkg.Rand32ByteArray(): tpkg.RandEd25519Signature(),
			tpkg.Rand32ByteArray(): tpkg.RandEd25519Signature(),
		},
	}

	b, err := s.MarshalBinary()
	require.NoError(t, err)
	t.Logf("%s", b)

	// the unmarshalled state must match the original
	s2 := &decoo.AppState{}
	require.NoError(t, s2.UnmarshalBinary(b))
	require.Equal(t, s, s2)

	// test that the serialization is deterministic
	for i := 0; i < 100; i++ {
		// create a new deep copy
		cpy := &decoo.AppState{}
		require.NoError(t, cpy.UnmarshalBinary(b))
		// serialization must be identical
		cpyBytes, err := cpy.MarshalBinary()
		require.NoError(t, err)
		require.Equal(t, b, cpyBytes)
	}
}
