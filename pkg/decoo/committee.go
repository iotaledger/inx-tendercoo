package decoo

import (
	"crypto/ed25519"
	"errors"

	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
	iotago "github.com/iotaledger/iota.go/v3"
)

// Errors returned by Committee.
var (
	ErrNonApplicablePublicKey = errors.New("non applicable public key")
	ErrInvalidSignature       = errors.New("invalid signature")
)

// PeerID is a simple peer ID.
type PeerID = types.Byte32

// Committee defines a committee of signers.
type Committee struct {
	privateKey ed25519.PrivateKey
	idSet      map[PeerID]struct{}
}

// IDFromPublicKey returns the ID from a given public key.
func IDFromPublicKey(publicKey ed25519.PublicKey) PeerID {
	return types.Byte32FromSlice(publicKey)
}

// NewCommittee creates a new committee.
// The given private key must belong to one member.
func NewCommittee(privateKey ed25519.PrivateKey, members ...ed25519.PublicKey) *Committee {
	ids := make(map[PeerID]struct{}, len(members))
	for _, member := range members {
		id := IDFromPublicKey(member)
		if _, has := ids[id]; has {
			panic("duplicate member")
		}
		ids[id] = struct{}{}
	}
	id := IDFromPublicKey(privateKey.Public().(ed25519.PublicKey))
	if _, has := ids[id]; !has {
		panic("validation: private key not a member")
	}
	return &Committee{privateKey, ids}
}

// N returns the number of members in the committee.
func (v *Committee) N() int { return len(v.idSet) }

// T returns the threshold t required for valid signatures.
// Currently, this corresponds to n-f, i.e. at least all honest members.
func (v *Committee) T() int { return v.N()*2/3 + 1 }

// PublicKey returns the public key of the local member.
func (v *Committee) PublicKey() ed25519.PublicKey {
	return v.privateKey.Public().(ed25519.PublicKey)
}

// ID returns the ID of the local member.
func (v *Committee) ID() PeerID {
	return IDFromPublicKey(v.PublicKey())
}

// Members returns a set of all members public keys.
func (v *Committee) Members() iotago.MilestonePublicKeySet {
	set := make(iotago.MilestonePublicKeySet, len(v.idSet))
	for id := range v.idSet {
		set[id] = struct{}{}
	}
	return set
}

// Sign signs the message with the local private key and returns a signature.
func (v *Committee) Sign(message []byte) *iotago.Ed25519Signature {
	edSigs := &iotago.Ed25519Signature{}
	copy(edSigs.PublicKey[:], v.PublicKey())
	copy(edSigs.Signature[:], ed25519.Sign(v.privateKey, message))
	return edSigs
}

// VerifySingle verifies a single signature from a committee member.
func (v *Committee) VerifySingle(message []byte, publicKey ed25519.PublicKey, signature []byte) error {
	if _, has := v.idSet[IDFromPublicKey(publicKey)]; !has {
		return ErrNonApplicablePublicKey
	}
	if !ed25519.Verify(publicKey, message, signature) {
		return ErrInvalidSignature
	}
	return nil
}
