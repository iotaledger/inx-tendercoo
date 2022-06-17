package decoo

import (
	"encoding/json"
	"sync"

	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
	iotago "github.com/iotaledger/iota.go/v3"
	"golang.org/x/crypto/blake2b"
)

// AppState holds the global application state.
// The AppState must have a well-defined hash. For this, the AppState is marshalled into json and the resulting bytes
// are then hashed using BLAKE2b. The standard json implementation assures, that the marshaling is always deterministic.
type AppState struct {
	sync.Mutex

	// Height denotes the height of the Tendermint blockchain.
	Height int64

	// State contains the coordinator state.
	State

	// ParentByIssuer contains the proposed block IDs sorted by the proposer's public key.
	ParentByIssuer map[types.Byte32]iotago.BlockID
	// IssuerCountByParent counts the issuers of each parent.
	IssuerCountByParent map[iotago.BlockID]int

	// ProofIssuersByBlockID contains the public key of each proof sorted by its block ID.
	ProofIssuersByBlockID map[iotago.BlockID]map[types.Byte32]struct{}

	// Milestone contains the constructed Milestone, or nil if we are still collecting proofs.
	Milestone *iotago.Milestone
	// SignaturesByIssuer contains the milestone signatures sorted by the signer's public key.
	SignaturesByIssuer map[types.Byte32]*iotago.Ed25519Signature
}

// MarshalBinary provides deterministic marshalling of the state.
func (a *AppState) MarshalBinary() ([]byte, error) { return json.Marshal(a) }

// UnmarshalBinary must be able to decode the form generated by MarshalBinary.
func (a *AppState) UnmarshalBinary(data []byte) error { return json.Unmarshal(data, a) }

// Reset resets the complete state after a new milestone has been issued.
func (a *AppState) Reset(height int64, state *State) {
	a.Height = height
	a.State = *state
	a.ParentByIssuer = map[types.Byte32]iotago.BlockID{}
	a.IssuerCountByParent = map[iotago.BlockID]int{}
	a.ProofIssuersByBlockID = map[iotago.BlockID]map[types.Byte32]struct{}{}
	a.Milestone = nil
	a.SignaturesByIssuer = map[types.Byte32]*iotago.Ed25519Signature{}
}

// Copy sets the AppState to a deep copy of o.
func (a *AppState) Copy(o *AppState) {
	data, err := o.MarshalBinary()
	if err != nil {
		panic(err)
	}
	a.Reset(o.Height, &o.State)
	if err := a.UnmarshalBinary(data); err != nil {
		panic(err)
	}
}

// Hash returns the BLAKE2b-256 hash of the state.
func (a *AppState) Hash() []byte {
	buf, err := a.MarshalBinary()
	if err != nil {
		panic(err)
	}
	hash := blake2b.Sum256(buf)
	return hash[:]
}
