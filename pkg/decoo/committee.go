package decoo

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	iotago "github.com/iotaledger/iota.go/v3"
)

// Errors returned by Committee.
var (
	ErrNonApplicablePublicKey = errors.New("non applicable public key")
	ErrInvalidSignature       = errors.New("invalid signature")
)

// Committee defines a committee of signers.
type Committee struct {
	sk        ed25519.PrivateKey
	indexByID map[Key32]int
}

// NewCommittee creates a new committee.
// The private key sk must belong to one member.
func NewCommittee(sk ed25519.PrivateKey, members ...ed25519.PublicKey) *Committee {
	ids := make(map[Key32]int, len(members))
	for i, member := range members {
		id := Key32FromBytes(member)
		if _, has := ids[id]; has {
			panic("duplicate member")
		}
		ids[id] = i
	}
	id := Key32FromBytes(sk.Public().(ed25519.PublicKey))
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
func (v *Committee) ID() Key32 {
	return Key32FromBytes(v.PublicKey())
}

// Members returns a set of all members public keys.
func (v *Committee) Members() iotago.MilestonePublicKeySet {
	set := make(iotago.MilestonePublicKeySet, len(v.indexByID))
	for id := range v.indexByID {
		set[iotago.MilestonePublicKey(id)] = struct{}{}
	}
	return set
}

func (v *Committee) String() string {
	return fmt.Sprint(v.indexByID)
}

// MemberIndex returns the index of the provided peer.
// Certain applications require a consecutive number instead of a more cryptic public key.
func (v *Committee) MemberIndex(id Key32) (int, bool) {
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
	if _, has := v.indexByID[Key32FromBytes(publicKey)]; !has {
		return ErrNonApplicablePublicKey
	}
	if !ed25519.Verify(publicKey, message, signature) {
		return ErrInvalidSignature
	}
	return nil
}
