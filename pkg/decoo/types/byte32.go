package types

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// Byte32 holds a 32-byte array that marshals/unmarshals as a string in hexadecimal encoding.
// It can also be used to store values from CLI flags.
type Byte32 [32]byte

// Byte32FromSlice creates a Byte32 from a byte slice.
// It panics when the slice has invalid length.
func Byte32FromSlice(slice []byte) (b Byte32) {
	if len(slice) != len(Byte32{}) {
		panic("invalid byte slice length")
	}
	copy(b[:], slice)

	return b
}

// NewByte32 creates a new Byte32 from a reference 32-byte array and a default value.
func NewByte32(defaultVal [32]byte, p *[32]byte) *Byte32 {
	copy(p[:], defaultVal[:])

	return (*Byte32)(p)
}

// MarshalText encodes the receiver into UTF-8-encoded text and returns the result.
func (b Byte32) MarshalText() ([]byte, error) {
	dst := make([]byte, hex.EncodedLen(len(Byte32{})))
	hex.Encode(dst, b[:])

	return dst, nil
}

// UnmarshalText must be able to decode the form generated by MarshalText.
func (b *Byte32) UnmarshalText(text []byte) error {
	_, err := hex.Decode(b[:], text)

	return err
}

// Set sets a 32-byte array from the given string.
// If the given string is not parsable, an error is returned.
func (b *Byte32) Set(s string) error {
	// flag always trims string values, so we should do the same
	trimmed := strings.TrimSpace(s)
	if l := len(trimmed); l != hex.EncodedLen(len(Byte32{})) {
		return fmt.Errorf("invalid length: %d", l)
	}
	dec, err := hex.DecodeString(trimmed)
	if err != nil {
		return err
	}
	copy(b[:], dec)

	return nil
}

// Type returns the type of the option.
func (b Byte32) Type() string { return "byte32Hex" }

func (b Byte32) String() string { return hex.EncodeToString(b[:]) }
