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

// ID is a simple peer ID.
type ID = types.Byte32

// Committee defines a committee of signers.
type Committee struct {
	sk        ed25519.PrivateKey
	indexByID map[ID]int
}

// IDFromPublicKey returns the ID from a given public key.
func IDFromPublicKey(publicKey ed25519.PublicKey) (id ID) {
	copy(id[:], publicKey)
	return id
}

// NewCommittee creates a new committee.
// The private key sk must belong to one member.
func NewCommittee(sk ed25519.PrivateKey, members ...ed25519.PublicKey) *Committee {
	ids := make(map[ID]int, len(members))
	for i, member := range members {
		id := IDFromPublicKey(member)
		if _, has := ids[id]; has {
			panic("duplicate member")
		}
		ids[id] = i
	}
	id := IDFromPublicKey(sk.Public().(ed25519.PublicKey))
	if _, has := ids[id]; !has {
		panic("validation: private key not a member")
	}
	return &Committee{sk, ids}
}

// N returns the number of members in the committee.
func (v *Committee) N() int { return len(v.indexByID) }

// T returns the threshold t required for valid signatures.
// Currently, this corresponds to f+1, i.e. at least one honest member.
func (v *Committee) T() int { return v.N()/3 + 1 }

// PublicKey returns the public key of the local member.
func (v *Committee) PublicKey() ed25519.PublicKey {
	return v.sk.Public().(ed25519.PublicKey)
}

// ID returns the ID of the local member.
func (v *Committee) ID() ID {
	return IDFromPublicKey(v.PublicKey())
}

// Members returns a set of all members public keys.
func (v *Committee) Members() iotago.MilestonePublicKeySet {
	set := make(iotago.MilestonePublicKeySet, len(v.indexByID))
	for id := range v.indexByID {
		set[id] = struct{}{}
	}
	return set
}

// MemberIndex returns the index of the provided peer.
// Certain applications require a consecutive number instead of a more cryptic public key.
func (v *Committee) MemberIndex(id ID) (int, bool) {
	i, has := v.indexByID[id]
	return i, has
}

// Sign signs the message with the local private key and returns a signature.
func (v *Committee) Sign(message []byte) *iotago.Ed25519Signature {
	edSigs := &iotago.Ed25519Signature{}
	copy(edSigs.PublicKey[:], v.PublicKey())
	copy(edSigs.Signature[:], ed25519.Sign(v.sk, message))
	return edSigs
}

// VerifySingle verifies a single signature from a committee member.
func (v *Committee) VerifySingle(message []byte, publicKey ed25519.PublicKey, signature []byte) error {
	if _, has := v.indexByID[IDFromPublicKey(publicKey)]; !has {
		return ErrNonApplicablePublicKey
	}
	if !ed25519.Verify(publicKey, message, signature) {
		return ErrInvalidSignature
	}
	return nil
}
