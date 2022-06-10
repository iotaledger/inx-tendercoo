package decoo_test

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/tpkg"
	"github.com/stretchr/testify/require"
)

type void = struct{}

func TestAppState_Encoding(t *testing.T) {
	s := randState()
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

func TestAppState_Reset(t *testing.T) {
	s := randState()
	s.Reset(0, &decoo.State{})
	require.NotEqual(t, &decoo.AppState{}, s)

	// all maps must be initialized
	v := reflect.Indirect(reflect.ValueOf(s))
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if v := v.Field(i); v.CanSet() {
			if v.Kind() == reflect.Map {
				require.Falsef(t, v.IsNil(), "map %s is not initialized", name)
			}
		}
	}
}

func TestAppState_Copy(t *testing.T) {
	// test that a random state is copied
	s := &decoo.AppState{}
	random := randState()
	s.Copy(random)
	require.Equal(t, random, s)

	// test that an empty state is copied
	s = randState()
	s.Copy(&decoo.AppState{})
	require.Equal(t, &decoo.AppState{}, s)

	// test that a reset state is copied
	s = randState()
	reset := &decoo.AppState{}
	reset.Reset(0, &decoo.State{})
	s.Copy(reset)
	require.Equal(t, reset, s)
}

func randState() *decoo.AppState {
	return &decoo.AppState{
		Height: rand.Int63(),
		State: decoo.State{
			MilestoneHeight:      rand.Int63(),
			MilestoneIndex:       rand.Uint32(),
			LastMilestoneID:      tpkg.Rand32ByteArray(),
			LastMilestoneBlockID: tpkg.Rand32ByteArray(),
		},
		ParentByIssuer: map[types.Byte32]iotago.BlockID{
			tpkg.Rand32ByteArray(): tpkg.Rand32ByteArray(),
			tpkg.Rand32ByteArray(): tpkg.Rand32ByteArray(),
		},
		IssuerCountByParent: map[types.Byte32]int{
			tpkg.Rand32ByteArray(): rand.Int(),
			tpkg.Rand32ByteArray(): rand.Int(),
		},
		ProofsByBlockID: map[types.Byte32]map[types.Byte32]void{
			tpkg.Rand32ByteArray(): {tpkg.Rand32ByteArray(): void{}, tpkg.Rand32ByteArray(): void{}},
			tpkg.Rand32ByteArray(): {tpkg.Rand32ByteArray(): void{}, tpkg.Rand32ByteArray(): void{}},
		},
		Milestone: tpkg.RandMilestone(nil),
		SignaturesByIssuer: map[types.Byte32]*iotago.Ed25519Signature{
			tpkg.Rand32ByteArray(): tpkg.RandEd25519Signature(),
			tpkg.Rand32ByteArray(): tpkg.RandEd25519Signature(),
		},
	}
}
