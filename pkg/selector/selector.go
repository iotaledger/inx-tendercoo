package selector

import (
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

type TipSelector interface {
	OnNewSolidBlock(*inx.BlockMetadata) int
	SelectTips(int) (iotago.BlockIDs, error)
	Reset()
}
