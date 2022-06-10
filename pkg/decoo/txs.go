package decoo

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/iotaledger/inx-tendercoo/pkg/decoo/proto/tendermint"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
	iotago "github.com/iotaledger/iota.go/v3"
	"google.golang.org/protobuf/proto"
)

// ErrInvalidBlockID is returned when the transaction contains an invalid block ID.
var ErrInvalidBlockID = errors.New("invalid block ID")

// Tx defines the common interface of a Tendermint transaction.
type Tx interface {
	// Apply applies the transaction from issuer to state and returns an error code.
	Apply(issuer ed25519.PublicKey, state *AppState) uint32
}

type Parent struct {
	Index   uint32
	BlockID iotago.BlockID
}

func (p *Parent) Apply(issuer ed25519.PublicKey, state *AppState) uint32 {
	// proof must match the current milestone index
	if p.Index != state.MilestoneIndex {
		return CodeTypeStateError
	}
	// proofs are only relevant before we created a milestone
	if state.Milestone != nil {
		return CodeTypeStateError
	}
	// there must be at most one parent per issuer
	issuerKey := types.Byte32FromSlice(issuer)
	if _, has := state.ParentByIssuer[issuerKey]; has {
		return CodeTypeReplayError
	}

	// add the parent to the state
	state.ParentByIssuer[issuerKey] = p.BlockID
	state.IssuerCountByParent[types.Byte32(p.BlockID)]++
	return CodeTypeOK
}

type Proof struct {
	Index  uint32
	Parent iotago.BlockID
}

func (p *Proof) Apply(issuer ed25519.PublicKey, state *AppState) uint32 {
	// proof must match the current milestone index
	if p.Index != state.MilestoneIndex {
		return CodeTypeStateError
	}
	// proofs are only relevant before we created a milestone
	if state.Milestone != nil {
		return CodeTypeStateError
	}
	// the referenced block must be a parent
	if state.IssuerCountByParent[types.Byte32(p.Parent)] < 1 {
		return CodeTypeStateError
	}
	// check that the same proof was not issued already
	proofs := state.ProofsByBlockID[types.Byte32(p.Parent)]
	if proofs == nil {
		proofs = map[types.Byte32]struct{}{}
		state.ProofsByBlockID[types.Byte32(p.Parent)] = proofs
	}
	if _, has := proofs[types.Byte32FromSlice(issuer)]; has {
		return CodeTypeReplayError
	}

	// add the proof to the state
	proofs[types.Byte32FromSlice(issuer)] = struct{}{}
	return CodeTypeOK
}

type PartialSignature struct {
	Signature []byte
}

func (p *PartialSignature) Apply(issuer ed25519.PublicKey, state *AppState) uint32 {
	// there must be a milestone essence to sign
	if state.Milestone == nil {
		return CodeTypeStateError
	}
	// there must be at most one signature per issuer
	if _, has := state.SignaturesByIssuer[types.Byte32FromSlice(issuer)]; has {
		return CodeTypeReplayError
	}
	// the signature must be valid
	essence, err := state.Milestone.Essence()
	if err != nil {
		panic(err)
	}
	if !ed25519.Verify(issuer, essence, p.Signature) {
		return CodeTypeSyntaxError
	}

	sig := &iotago.Ed25519Signature{}
	copy(sig.PublicKey[:], issuer)
	copy(sig.Signature[:], p.Signature)

	// add the partial signature to the state
	state.SignaturesByIssuer[types.Byte32FromSlice(issuer)] = sig
	return CodeTypeOK
}

// MarshalTx returns the wire-format encoding of m which can then be passed to Tendermint.
func MarshalTx(c *Committee, tx Tx) ([]byte, error) {
	txEssence := &tendermint.Essence{}
	switch tx := tx.(type) {
	case *Parent:
		txEssence.Message = &tendermint.Essence_Parent{
			Parent: &tendermint.Parent{
				Index:   tx.Index,
				BlockId: tx.BlockID[:],
			},
		}
	case *Proof:
		txEssence.Message = &tendermint.Essence_Proof{
			Proof: &tendermint.Proof{Index: tx.Index,
				ParentBlockId: tx.Parent[:],
			},
		}
	case *PartialSignature:
		txEssence.Message = &tendermint.Essence_PartialSignature{
			PartialSignature: &tendermint.PartialSignature{
				Signature: tx.Signature,
			},
		}
	default:
		return nil, fmt.Errorf("unknown message: %T", tx)
	}

	essence, err := proto.Marshal(txEssence)
	if err != nil {
		return nil, err
	}
	txRaw := &tendermint.TxRaw{
		Essence:   essence,
		PublicKey: c.PublicKey(),
		Signature: c.Sign(essence).Signature[:],
	}
	return proto.Marshal(txRaw)
}

// UnmarshalTx parses the wire-format message in b and returns the verified issuer as well as the message m.
func UnmarshalTx(c *Committee, b []byte) (ed25519.PublicKey, Tx, error) {
	txRaw := &tendermint.TxRaw{}
	if err := proto.Unmarshal(b, txRaw); err != nil {
		return nil, nil, err
	}
	if err := c.VerifySingle(txRaw.GetEssence(), txRaw.GetPublicKey(), txRaw.GetSignature()); err != nil {
		return nil, nil, err
	}

	txEssence := &tendermint.Essence{}
	if err := proto.Unmarshal(txRaw.GetEssence(), txEssence); err != nil {
		return nil, nil, err
	}

	var tx Tx
	switch message := txEssence.Message.(type) {
	case *tendermint.Essence_Parent:
		var blockID iotago.BlockID
		if len(message.Parent.BlockId) != len(blockID) {
			return nil, nil, ErrInvalidBlockID
		}
		copy(blockID[:], message.Parent.BlockId)
		tx = &Parent{
			Index:   message.Parent.Index,
			BlockID: blockID,
		}
	case *tendermint.Essence_Proof:
		var parent iotago.BlockID
		if len(message.Proof.ParentBlockId) != len(parent) {
			return nil, nil, ErrInvalidBlockID
		}
		copy(parent[:], message.Proof.ParentBlockId)
		tx = &Proof{
			Index:  message.Proof.Index,
			Parent: parent,
		}
	case *tendermint.Essence_PartialSignature:
		tx = &PartialSignature{Signature: message.PartialSignature.Signature}
	default:
		return nil, nil, fmt.Errorf("unknown message: %T", message)
	}

	return txRaw.GetPublicKey(), tx, nil
}
