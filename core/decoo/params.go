package decoo

import (
	"time"

	"github.com/iotaledger/hive.go/core/app"
)

// ParametersDefinition contains the definition of configuration parameters used by the decoo app.
type ParametersDefinition struct {
	Interval         time.Duration `default:"5s" usage:"the interval in which milestones are issued"`
	MaxTrackedBlocks int           `default:"10000" usage:"maximum amount of blocks tracked by the milestone tip selection"`
	TipSel           struct {
		MaxTips                  int           `default:"7" usage:"maximum amount of tips returned"`
		ReducedConfirmationLimit float64       `default:"0.5" usage:"only select tips that newly reference more than this limit compared to the best tip"`
		Timeout                  time.Duration `default:"100ms" usage:"timeout after which tip selection is canceled"`
	} `name:"tipsel"`
	Tendermint struct {
		BindAddress string                      `default:"0.0.0.0:26656" usage:"binding address to listen for incoming connections"`
		Root        string                      `default:"tendermint" usage:"root directory for all Tendermint data"`
		LogLevel    string                      `default:"info" usage:"root directory for all Tendermint data"`
		GenesisTime int64                       `default:"0" usage:"genesis time of the Tendermint blockchain in Unix time"`
		ChainID     string                      `default:"tendercoo" usage:"human-readable ID of the Tendermint blockchain"`
		Validators  map[string]ValidatorsConfig `noflag:"true" usage:"defines the Tendermint validators"`

		Consensus struct {
			CreateEmptyBlocks         bool          `default:"false" usage:"EmptyBlocks mode"`
			CreateEmptyBlocksInterval time.Duration `default:"0s" usage:"possible interval between empty blocks"`
			BlockInterval             time.Duration `default:"1s" usage:"how long we wait after committing a block, before starting on the new height"`
			SkipBlockTimeout          bool          `default:"false" usage:"make progress as soon as we have all the precommits"`
		}

		Prometheus struct {
			Enabled     bool   `default:"false" usage:"toggle for Prometheus metrics collection"`
			BindAddress string `default:"localhost:26660" usage:"Prometheus listening address binding"`
		}
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
