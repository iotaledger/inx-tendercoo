// Package tendermint contains the protobuf definitions for Tendermint transactions.
package tendermint

import (
	"fmt"

	"google.golang.org/protobuf/proto"
)

// generate protobuf sources
//go:generate protoc --go_out=paths=source_relative:. tx.proto

// Wrap wraps a message inside Essence.
func (x *Essence) Wrap(pb proto.Message) error {
	switch msg := pb.(type) {
	case *Parent:
		x.Message = &Essence_Parent{Parent: msg}
	case *Proof:
		x.Message = &Essence_Proof{Proof: msg}
	case *PartialSignature:
		x.Message = &Essence_PartialSignature{PartialSignature: msg}
	default:
		return fmt.Errorf("unknown message: %T", msg)
	}

	return nil
}

// Unwrap returns the message wrapped inside the Essence.
func (x *Essence) Unwrap() (proto.Message, error) {
	if x == nil {
		return nil, nil
	}
	switch message := x.Message.(type) {
	case *Essence_Parent:
		return message.Parent, nil
	case *Essence_Proof:
		return message.Proof, nil
	case *Essence_PartialSignature:
		return message.PartialSignature, nil
	default:
		return nil, fmt.Errorf("unknown message: %T", message)
	}
}
