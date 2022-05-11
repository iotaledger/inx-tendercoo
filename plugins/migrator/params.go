package migrator

import (
	"time"

	flag "github.com/spf13/pflag"

	"github.com/gohornet/inx-coordinator/pkg/migrator"
	"github.com/iotaledger/hive.go/app"
)

const (
	// CfgMigratorStateFilePath configures the path to the state file of the migrator.
	CfgMigratorStateFilePath = "migrator.stateFilePath"
	// CfgMigratorReceiptMaxEntries defines the max amount of entries to embed within a receipt.
	CfgMigratorReceiptMaxEntries = "migrator.receiptMaxEntries"
	// CfgMigratorQueryCooldownPeriod configures the cooldown period for the service to ask for new data
	// from the legacy node in case the migrator encounters an error.
	CfgMigratorQueryCooldownPeriod = "migrator.queryCooldownPeriod"

	// CfgReceiptsValidatorAPIAddress configures the address of the legacy node API to query for white-flag confirmation data.
	CfgReceiptsValidatorAPIAddress = "receipts.validator.api.address"
	// CfgReceiptsValidatorAPITimeout configures the timeout of API calls.
	CfgReceiptsValidatorAPITimeout = "receipts.validator.api.timeout"
	// CfgReceiptsValidatorCoordinatorAddress configures the address of the legacy coordinator.
	CfgReceiptsValidatorCoordinatorAddress = "receipts.validator.coordinator.address"
	// CfgReceiptsValidatorCoordinatorMerkleTreeDepth configures the depth of the Merkle tree of the legacy coordinator.
	CfgReceiptsValidatorCoordinatorMerkleTreeDepth = "receipts.validator.coordinator.merkleTreeDepth"
)

var params = &app.ComponentParams{
	Params: func(fs *flag.FlagSet) {
		fs.String(CfgMigratorStateFilePath, "migrator.state", "path to the state file of the migrator")
		fs.Int(CfgMigratorReceiptMaxEntries, migrator.SensibleMaxEntriesCount, "the max amount of entries to embed within a receipt")
		fs.Duration(CfgMigratorQueryCooldownPeriod, 5*time.Second, "the cooldown period of the service to ask for new data")

		fs.String(CfgReceiptsValidatorAPIAddress, "http://localhost:14266", "address of the legacy node API")
		fs.Duration(CfgReceiptsValidatorAPITimeout, 5*time.Second, "timeout of API calls")
		fs.String(CfgReceiptsValidatorCoordinatorAddress, "UDYXTZBE9GZGPM9SSQV9LTZNDLJIZMPUVVXYXFYVBLIEUHLSEWFTKZZLXYRHHWVQV9MNNX9KZC9D9UZWZ", "address of the legacy coordinator")
		fs.Int(CfgReceiptsValidatorCoordinatorMerkleTreeDepth, 24, "depth of the Merkle tree of the coordinator")
	},
	Masked: nil,
}
