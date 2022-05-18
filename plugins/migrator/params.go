package migrator

import (
	"time"

	"github.com/gohornet/inx-coordinator/pkg/migrator"
	"github.com/iotaledger/hive.go/app"
)

type ParametersMigrator struct {
	StateFilePath       string        `default:"migrator.state" usage:"path to the state file of the migrator"`
	ReceiptMaxEntries   int           `usage:"the max amount of entries to embed within a receipt"`
	QueryCooldownPeriod time.Duration `default:"5s" usage:"the cooldown period for the service to ask for new data from the legacy node in case the migrator encounters an error"`
}

var ParamsMigrator = &ParametersMigrator{
	ReceiptMaxEntries: migrator.SensibleMaxEntriesCount,
}

type ParametersReceipts struct {
	Validator struct {
		API struct {
			Address string        `default:"http://localhost:14266" usage:"address of the legacy node API to query for white-flag confirmation data"`
			Timeout time.Duration `default:"5s" usage:"timeout of API calls"`
		} `name:"api"`
		Coordinator struct {
			Address         string `default:"UDYXTZBE9GZGPM9SSQV9LTZNDLJIZMPUVVXYXFYVBLIEUHLSEWFTKZZLXYRHHWVQV9MNNX9KZC9D9UZWZ" usage:"address of the legacy coordinator"`
			MerkleTreeDepth int    `default:"24" usage:"depth of the Merkle tree of the coordinator"`
		}
	}
}

var ParamsReceipts = &ParametersReceipts{}

var params = &app.ComponentParams{
	Params: map[string]any{
		"migrator": ParamsMigrator,
		"receipts": ParamsReceipts,
	},
	Masked: nil,
}
