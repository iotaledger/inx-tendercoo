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

// KeyManager contains the functions used from the iotago.KeyManager.
type KeyManager interface {
	PublicKeysSetForMilestoneIndex(iotago.MilestoneIndex) iotago.MilestonePublicKeySet
}

// PeerID is a simple peer ID.
type PeerID = types.Byte32

// Committee defines a committee of signers.
type Committee struct {
	privateKey ed25519.PrivateKey
	n, f, t    int
	manager    KeyManager
}

// IDFromPublicKey returns the ID from a given public key.
func IDFromPublicKey(publicKey ed25519.PublicKey) PeerID {
	return types.Byte32FromSlice(publicKey)
}

// NewCommitteeFromManager creates a new committee.
// The parameter n denotes the total number of committee members and t denotes the signature threshold.
func NewCommitteeFromManager(privateKey ed25519.PrivateKey, n int, t int, manager KeyManager) *Committee {
	return &Committee{privateKey, n, maxFaulty(n), t, manager}
}

// NewCommittee creates a new committee.
// The function panics, if privateKey does not match a publicKey or when publicKeys contains duplicates.
func NewCommittee(privateKey ed25519.PrivateKey, publicKeys ...ed25519.PublicKey) *Committee {
	n := len(publicKeys)
	t := n - maxFaulty(n)

	keySet := make(singleKeyRange, n)
	for _, member := range publicKeys {
		publicKey := iotago.MilestonePublicKey(types.Byte32FromSlice(member))
		if _, has := keySet[publicKey]; has {
			panic("Committee: duplicate public key")
		}
		keySet[publicKey] = struct{}{}
	}
	publicKey := iotago.MilestonePublicKey(types.Byte32FromSlice(privateKey.Public().(ed25519.PublicKey)))
	if _, has := keySet[publicKey]; !has {
		panic("Committee: private key does not belong to a public key")
	}

	return NewCommitteeFromManager(privateKey, n, t, keySet)
}

// N returns the number of members in the committee.
func (v *Committee) N() int { return v.n }

// F returns the number of Byzantine members in the committee.
func (v *Committee) F() int { return v.f }

// T returns the threshold t of valid signatures that are required.
func (v *Committee) T() int { return v.t }

// PublicKey returns the public key of the local member.
func (v *Committee) PublicKey() ed25519.PublicKey {
	return v.privateKey.Public().(ed25519.PublicKey)
}

// ID returns the ID of the local member.
func (v *Committee) ID() PeerID {
	return IDFromPublicKey(v.PublicKey())
}

// Members returns a set of all members public keys.
func (v *Committee) Members(msIndex iotago.MilestoneIndex) iotago.MilestonePublicKeySet {
	return v.manager.PublicKeysSetForMilestoneIndex(msIndex)
}

// Sign signs the message with the local private key and returns a signature.
func (v *Committee) Sign(message []byte) *iotago.Ed25519Signature {
	edSigs := &iotago.Ed25519Signature{}
	copy(edSigs.PublicKey[:], v.PublicKey())
	copy(edSigs.Signature[:], ed25519.Sign(v.privateKey, message))
	return edSigs
}

// VerifySingle verifies a single signature from a committee member.
func (v *Committee) VerifySingle(msIndex iotago.MilestoneIndex, message []byte, publicKey ed25519.PublicKey, signature []byte) error {
	set := v.manager.PublicKeysSetForMilestoneIndex(msIndex)
	if _, has := set[IDFromPublicKey(publicKey)]; !has {
		return ErrNonApplicablePublicKey
	}
	if !ed25519.Verify(publicKey, message, signature) {
		return ErrInvalidSignature
	}
	return nil
}

func maxFaulty(n int) int {
	if n <= 0 {
		panic("Committee: at least one member required")
	}
	return (n - 1) / 3
}

type singleKeyRange iotago.MilestonePublicKeySet

func (r singleKeyRange) PublicKeysSetForMilestoneIndex(iotago.MilestoneIndex) iotago.MilestonePublicKeySet {
	return r
}
