package decoo

import (
	"time"

	"github.com/iotaledger/hive.go/app"
)

// ParametersDefinition contains the definition of configuration parameters used by the decoo app.
type ParametersDefinition struct {
	Interval         time.Duration `default:"5s" usage:"the interval in which milestones are issued"`
	MaxTrackedBlocks int           `default:"10000" usage:"maximum amount of blocks tracked by the milestone tip selection"`
	TipSel           struct {
		MaxTips                  int           `default:"7" usage:"maximum amount of tips returned"`
		ReducedConfirmationLimit float64       `default:"0.5" usage:"only select tips that newly reference more than this limit compared to the best tip"`
		Timeout                  time.Duration `default:"100ms" usage:"timeout after which tip selection is cancelled"`
	} `name:"tipsel"`
	Tendermint struct {
		Root              string                      `default:"tendermint" usage:"root directory for all Tendermint data"`
		LogLevel          string                      `default:"info" usage:"root directory for all Tendermint data"`
		CreateEmptyBlocks bool                        `default:"false" usage:"root directory for all Tendermint data"`
		GenesisTime       int64                       `default:"0" usage:"genesis time of the Tendermint blockchain in Unix time"`
		ChainID           string                      `default:"tendercoo" usage:"human-readable ID of the Tendermint blockchain"`
		Validators        map[string]ValidatorsConfig `noflag:"true" usage:"defines the Tendermint validators"`
	}
}

// ValidatorsConfig defines the config options for one validator.
// TODO: what is the best way to define PubKey as a [32]byte
type ValidatorsConfig struct {
	PubKey  string
	Power   int64
	Address string
}

// Parameters contains the configuration parameters of the decoo app.
var Parameters = &ParametersDefinition{}

var params = &app.ComponentParams{
	Params: map[string]any{
		"coordinator": Parameters,
	},
}
