package selector

import (
	"context"

	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

// TipSelector is the interface implemented by all tip selectors.
type TipSelector interface {
	TrackedBlocks() int
	OnNewSolidBlock(*inx.BlockMetadata) int
	SelectTips(context.Context, int) (iotago.BlockIDs, error)
	Reset()
}
