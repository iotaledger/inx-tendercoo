package migrator

import (
	"context"
	"fmt"
	"net/http"

	flag "github.com/spf13/pflag"
	"go.uber.org/dig"

	"github.com/gohornet/hornet/pkg/common"
	validator "github.com/gohornet/hornet/pkg/model/migrator"
	"github.com/gohornet/hornet/pkg/model/utxo"
	"github.com/gohornet/hornet/pkg/node"
	"github.com/gohornet/hornet/pkg/shutdown"
	"github.com/gohornet/inx-coordinator/pkg/daemon"
	"github.com/gohornet/inx-coordinator/pkg/migrator"
	"github.com/iotaledger/hive.go/configuration"
	"github.com/iotaledger/hive.go/timeutil"
	legacyapi "github.com/iotaledger/iota.go/api"
	iotago "github.com/iotaledger/iota.go/v3"
)

const (
	// CfgMigratorBootstrap configures whether the migration process is bootstrapped.
	CfgMigratorBootstrap = "migratorBootstrap"
	// CfgMigratorStartIndex configures the index of the first milestone to migrate.
	CfgMigratorStartIndex = "migratorStartIndex"
)

func init() {
	_ = flag.CommandLine.MarkHidden(CfgMigratorBootstrap)
	_ = flag.CommandLine.MarkHidden(CfgMigratorStartIndex)

	Plugin = &node.Plugin{
		Status: node.StatusDisabled,
		Pluggable: node.Pluggable{
			Name:      "Migrator",
			DepsFunc:  func(cDeps dependencies) { deps = cDeps },
			Params:    params,
			Provide:   provide,
			Configure: configure,
			Run:       run,
		},
	}
}

var (
	Plugin *node.Plugin
	deps   dependencies

	bootstrap  = flag.Bool(CfgMigratorBootstrap, false, "bootstrap the migration process")
	startIndex = flag.Uint32(CfgMigratorStartIndex, 1, "index of the first milestone to migrate")
)

type dependencies struct {
	dig.In
	UTXOManager     *utxo.Manager
	AppConfig       *configuration.Configuration `name:"appConfig"`
	MigratorService *migrator.MigratorService
	ShutdownHandler *shutdown.ShutdownHandler
}

// provide provides the MigratorService as a singleton.
func provide(c *dig.Container) {

	type validatorDeps struct {
		dig.In
		AppConfig *configuration.Configuration `name:"appConfig"`
	}

	if err := c.Provide(func(deps validatorDeps) *validator.Validator {
		legacyAPI, err := legacyapi.ComposeAPI(legacyapi.HTTPClientSettings{
			URI:    deps.AppConfig.String(CfgReceiptsValidatorAPIAddress),
			Client: &http.Client{Timeout: deps.AppConfig.Duration(CfgReceiptsValidatorAPITimeout)},
		})
		if err != nil {
			Plugin.LogPanicf("failed to initialize API: %s", err)
		}
		return validator.NewValidator(
			legacyAPI,
			deps.AppConfig.String(CfgReceiptsValidatorCoordinatorAddress),
			deps.AppConfig.Int(CfgReceiptsValidatorCoordinatorMerkleTreeDepth),
		)
	}); err != nil {
		Plugin.LogPanic(err)
	}

	type serviceDeps struct {
		dig.In
		AppConfig *configuration.Configuration `name:"appConfig"`
		Validator *validator.Validator
	}

	if err := c.Provide(func(deps serviceDeps) *migrator.MigratorService {

		maxReceiptEntries := deps.AppConfig.Int(CfgMigratorReceiptMaxEntries)
		switch {
		case maxReceiptEntries > iotago.MaxMigratedFundsEntryCount:
			Plugin.LogPanicf("%s (set to %d) can be max %d", CfgMigratorReceiptMaxEntries, maxReceiptEntries, iotago.MaxMigratedFundsEntryCount)
		case maxReceiptEntries <= 0:
			Plugin.LogPanicf("%s must be greather than 0", CfgMigratorReceiptMaxEntries)
		}

		return migrator.NewService(
			deps.Validator,
			deps.AppConfig.String(CfgMigratorStateFilePath),
			deps.AppConfig.Int(CfgMigratorReceiptMaxEntries),
		)
	}); err != nil {
		Plugin.LogPanic(err)
	}
}

func configure() {

	var msIndex *uint32
	if *bootstrap {
		msIndex = startIndex
	}

	if err := deps.MigratorService.InitState(msIndex, deps.UTXOManager); err != nil {
		Plugin.LogFatalf("failed to initialize migrator: %s", err)
	}
}

func run() {

	if err := Plugin.Node.Daemon().BackgroundWorker(Plugin.Name, func(ctx context.Context) {
		Plugin.LogInfof("Starting %s ... done", Plugin.Name)
		deps.MigratorService.Start(ctx, func(err error) bool {

			if err := common.IsCriticalError(err); err != nil {
				deps.ShutdownHandler.SelfShutdown(fmt.Sprintf("migrator plugin hit a critical error: %s", err))
				return false
			}

			if err := common.IsSoftError(err); err != nil {
				deps.MigratorService.Events.SoftError.Trigger(err)
			}

			// lets just log the err and halt querying for a configured period
			Plugin.LogWarn(err)
			return timeutil.Sleep(ctx, deps.AppConfig.Duration(CfgMigratorQueryCooldownPeriod))
		})
		Plugin.LogInfof("Stopping %s ... done", Plugin.Name)
	}, daemon.PriorityStopMigrator); err != nil {
		Plugin.LogPanicf("failed to start worker: %s", err)
	}
}
