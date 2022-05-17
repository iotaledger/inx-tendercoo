package decoo

import (
	"time"

	flag "github.com/spf13/pflag"

	"github.com/iotaledger/hive.go/app"
)

const (
	// CfgCoordinatorInterval is the interval at which milestones are issued.
	CfgCoordinatorInterval = "coordinator.interval"
	// CfgCoordinatorMaxTrackedMessages defines the maximum amount of known messages for milestone tipselection. If this limit is exceeded, a new checkpoint is issued.
	CfgCoordinatorMaxTrackedMessages = "coordinator.maxTrackedMessages"
	// CfgCoordinatorTipselectMinHeaviestBranchUnreferencedMessagesThreshold defines the minimum threshold of unreferenced messages in the heaviest branch for milestone tipselection
	// if the value falls below that threshold, no more heaviest branch tips are picked.
	CfgCoordinatorTipselectMinHeaviestBranchUnreferencedMessagesThreshold = "coordinator.tipsel.minHeaviestBranchUnreferencedMessagesThreshold"
	// CfgCoordinatorTipselectMaxHeaviestBranchTipsPerCheckpoint defines the maximum amount of checkpoint messages with heaviest branch tips that are picked
	// if the heaviest branch is not below "UnreferencedMessagesThreshold" before.
	CfgCoordinatorTipselectMaxHeaviestBranchTipsPerCheckpoint = "coordinator.tipsel.maxHeaviestBranchTipsPerCheckpoint"
	// CfgCoordinatorTipselectRandomTipsPerCheckpoint defines the amount of checkpoint messages with random tips that are picked if a checkpoint is issued and at least
	// one heaviest branch tip was found, otherwise no random tips will be picked.
	CfgCoordinatorTipselectRandomTipsPerCheckpoint = "coordinator.tipsel.randomTipsPerCheckpoint"
	// CfgCoordinatorTipselectHeaviestBranchSelectionTimeout defines the maximum duration to select the heaviest branch tips.
	CfgCoordinatorTipselectHeaviestBranchSelectionTimeout = "coordinator.tipsel.heaviestBranchSelectionTimeout"
	// CfgCoordinatorTendermintRoot defines the root dir for all Tendermint data.
	CfgCoordinatorTendermintRoot = "coordinator.tendermint.root"
	// CfgCoordinatorTendermintLogLevel defines the log level for the Tendermint node.
	CfgCoordinatorTendermintLogLevel = "coordinator.tendermint.logLevel"
	// CfgCoordinatorTendermintCreateEmptyBlocks defines whether Tendermint creates empty blocks.
	CfgCoordinatorTendermintCreateEmptyBlocks = "coordinator.tendermint.createEmptyBlocks"
	// CfgCoordinatorTendermintGenesisTime defines the genesis time of the Tendermint blockchain in Unix time.
	CfgCoordinatorTendermintGenesisTime = "coordinator.tendermint.genesisTime"
	// CfgCoordinatorTendermintChainID defines the human-readable ID of the Tendermint blockchain.
	CfgCoordinatorTendermintChainID = "coordinator.tendermint.chainID"
	// CfgCoordinatorTendermintValidators defines the Tendermint validators.
	CfgCoordinatorTendermintValidators = "coordinator.tendermint.validators"
)

var params = &app.ComponentParams{
	Params: func(fs *flag.FlagSet) {
		fs.Duration(CfgCoordinatorInterval, 10*time.Second, "the interval milestones are issued")
		fs.Int(CfgCoordinatorMaxTrackedMessages, 10000, "maximum amount of known messages for milestone tipselection")
		fs.Int(CfgCoordinatorTipselectMinHeaviestBranchUnreferencedMessagesThreshold, 20, "minimum threshold of unreferenced messages in the heaviest branch")
		fs.Int(CfgCoordinatorTipselectMaxHeaviestBranchTipsPerCheckpoint, 10, "maximum amount of checkpoint messages with heaviest branch tips")
		fs.Int(CfgCoordinatorTipselectRandomTipsPerCheckpoint, 3, "amount of checkpoint messages with random tips")
		fs.Duration(CfgCoordinatorTipselectHeaviestBranchSelectionTimeout, 100*time.Millisecond, "the maximum duration to select the heaviest branch tips")
		fs.String(CfgCoordinatorTendermintRoot, "tendermint", "root dir for all Tendermint data")
		fs.String(CfgCoordinatorTendermintLogLevel, "info", "log level for the Tendermint node")
		fs.Bool(CfgCoordinatorTendermintCreateEmptyBlocks, false, "whether Tendermint creates empty blocks")
		fs.Int64(CfgCoordinatorTendermintGenesisTime, 0, "the genesis time of the Tendermint blockchain in Unix time")
		fs.String(CfgCoordinatorTendermintChainID, "tendercoo", "human-readable ID of the Tendermint blockchain")
	},
	Masked: nil,
}
