package decoo

import (
	"time"

	"github.com/iotaledger/hive.go/core/app"
)

// ParametersDefinition contains the definition of configuration parameters used by the plugin.
type ParametersDefinition struct {
	Interval                     time.Duration `default:"5s" usage:"target interval in which milestones are issued"`
	MaxTrackedBlocks             int           `default:"10000" usage:"maximum number of blocks tracked by the milestone tip selection"`
	WhiteFlagParentsSolidTimeout time.Duration `default:"2s" usage:"timeout for the ComputeWhiteFlag INX call"`

	TipSel struct {
		MaxTips                  int           `default:"7" usage:"maximum number of tips returned"`
		ReducedConfirmationLimit float64       `default:"0.6" usage:"stop selection, when tips reference less additional blocks than this fraction (compared to the best tip)"`
		Timeout                  time.Duration `default:"100ms" usage:"timeout after which tip selection is canceled"`
	} `name:"tipsel"`

	Tendermint struct {
		BindAddress         string `default:"0.0.0.0:26656" usage:"bind address for incoming connections"`
		ConsensusPrivateKey string `usage:"node's private key used for consensus"`
		NodePrivateKey      string `usage:"node's private key used for P2P communication"`

		Root            string `default:"tendermint" usage:"root folder to store config and database"`
		LogLevel        string `default:"info" usage:"logging level of the Tendermint Core; cannot be lower than the global level"`
		GenesisTime     int64  `default:"0" usage:"time the blockchain started or will start in Unix time using seconds"`
		ChainID         string `default:"tendercoo" usage:"identifier of the blockchain; every chain must have a unique ID"`
		MaxRetainBlocks uint   `default:"0" usage:"maximum number of blocks to keep in the database (0=no pruning)"`

		Peers      []string                    `usage:"addresses of the Tendermint nodes to connect to (ID@Host:Port)"`
		Validators map[string]ValidatorsConfig `noflag:"true" usage:"set of validators"`

		Consensus struct {
			CreateEmptyBlocks         bool          `default:"false" usage:"whether empty blocks are created"`
			CreateEmptyBlocksInterval time.Duration `default:"0s" usage:"create empty blocks after waiting this long without receiving anything"`
			BlockInterval             time.Duration `default:"700ms" usage:"delay between blocks"`
			SkipBlockTimeout          bool          `default:"false" usage:"make progress as soon as we have all the precommits"`
		}

		Prometheus struct {
			Enabled     bool   `default:"false" usage:"toggle for Prometheus metrics collection"`
			BindAddress string `default:"localhost:26660" usage:"Prometheus listening address binding"`
		}
	}
}

// ValidatorsConfig defines the config options for one validator.
type ValidatorsConfig struct {
	PublicKey string `usage:"consensus key of the validator"`
	Power     int64  `usage:"voting power of the validator"`
}

// Parameters contains the configuration parameters of the decoo app.
var Parameters = &ParametersDefinition{}

var params = &app.ComponentParams{
	Params: map[string]any{
		"coordinator": Parameters,
	},
}
