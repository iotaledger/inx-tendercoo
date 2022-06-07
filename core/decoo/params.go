package decoo

import (
	"time"

	"github.com/iotaledger/hive.go/app"
)

type Tendermint struct {
	Root              string                      `default:"tendermint" usage:"root directory for all Tendermint data"`
	LogLevel          string                      `default:"info" usage:"root directory for all Tendermint data"`
	CreateEmptyBlocks bool                        `default:"false" usage:"root directory for all Tendermint data"`
	GenesisTime       int64                       `default:"0" usage:"genesis time of the Tendermint blockchain in Unix time"`
	ChainID           string                      `default:"tendercoo" usage:"human-readable ID of the Tendermint blockchain"`
	Validators        map[string]ValidatorsConfig `noflag:"true" usage:"defines the Tendermint validators"`
}

type ParametersCoordinator struct {
	Interval         time.Duration `default:"5s" usage:"the interval milestones are issued"`
	MaxTrackedBlocks int           `default:"10000" usage:"maximum amount of known blocks for milestone tipselection"`
	TipSel           struct {
		MinHeaviestBranchUnreferencedBlocksThreshold int           `default:"20" usage:"minimum threshold of unreferenced blocks in the heaviest branch"`
		MaxHeaviestBranchTipsPerCheckpoint           int           `default:"10" usage:"maximum amount of checkpoint blocks with heaviest branch tips that are picked if the heaviest branch is not below 'MinHeaviestBranchUnreferencedBlocksThreshold' before"`
		HeaviestBranchSelectionTimeout               time.Duration `default:"100ms" usage:"the maximum duration to select the heaviest branch tips"`
	} `name:"tipsel"`
	Tendermint Tendermint
}

var ParamsCoordinator = &ParametersCoordinator{
	Tendermint: Tendermint{
		Validators: map[string]ValidatorsConfig{},
	},
}

var params = &app.ComponentParams{
	Params: map[string]any{
		"coordinator": ParamsCoordinator,
	},
	Masked: nil,
}
