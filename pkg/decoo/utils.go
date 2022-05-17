package decoo

import (
	"encoding"
)

func mustMarshal(e encoding.BinaryMarshaler) []byte {
	buf, err := e.MarshalBinary()
	if err != nil {
		panic(err)
	}
	return buf
}
