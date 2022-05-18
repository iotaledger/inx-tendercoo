package coordinator

import (
	"time"

	"github.com/gohornet/inx-coordinator/pkg/coordinator"
	"github.com/iotaledger/hive.go/app"
)

type Quorum struct {
	Enabled bool                                         `default:"false" usage:"whether the coordinator quorum is enabled"`
	Groups  map[string][]*coordinator.QuorumClientConfig `noflag:"true" usage:"defines the quorum groups used to ask other nodes for correct ledger state of the coordinator."`
	Timeout time.Duration                                `default:"2s" usage:"the timeout until a node in the quorum must have answered"`
}

type ParametersCoordinator struct {
	StateFilePath string        `default:"coordinator.state" usage:"the path to the state file of the coordinator"`
	Interval      time.Duration `default:"10s" usage:"the interval milestones are issued"`
	Signing       struct {
		Provider      string        `default:"local" usage:"the signing provider the coordinator uses to sign a milestone (local/remote)"`
		RemoteAddress string        `default:"localhost:12345" usage:"the address of the remote signing provider (insecure connection!)"`
		RetryTimeout  time.Duration `default:"2s" usage:"defines the timeout between signing retries"`
		RetryAmount   int           `default:"10" usage:"defines the number of signing retries to perform before shutting down the node"`
	}
	Quorum      Quorum
	Checkpoints struct {
		MaxTrackedBlocks int `default:"10000" usage:"maximum amount of known blocks for milestone tipselection. If this limit is exceeded, a new checkpoint is issued."`
	}
	TipSel struct {
		MinHeaviestBranchUnreferencedBlocksThreshold int           `default:"20" usage:"minimum threshold of unreferenced blocks in the heaviest branch"`
		MaxHeaviestBranchTipsPerCheckpoint           int           `default:"10" usage:"maximum amount of checkpoint blocks with heaviest branch tips that are picked if the heaviest branch is not below 'MinHeaviestBranchUnreferencedBlocksThreshold' before"`
		RandomTipsPerCheckpoint                      int           `default:"3" usage:"amount of checkpoint blocks with random tips that are picked if a checkpoint is issued and at least one heaviest branch tip was found, otherwise no random tips will be picked"`
		HeaviestBranchSelectionTimeout               time.Duration `default:"100ms" usage:"the maximum duration to select the heaviest branch tips"`
	} `name:"tipsel"`
}

var ParamsCoordinator = &ParametersCoordinator{
	Quorum: Quorum{
		Groups: make(map[string][]*coordinator.QuorumClientConfig),
	},
}

var params = &app.ComponentParams{
	Params: map[string]any{
		"coordinator": ParamsCoordinator,
	},
	Masked: nil,
}
