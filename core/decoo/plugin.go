package decoo

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/configuration"
	"github.com/iotaledger/hive.go/events"
	"github.com/iotaledger/hive.go/kvstore/mapdb"
	"github.com/iotaledger/hive.go/logger"
	"github.com/iotaledger/inx-tendercoo/pkg/daemon"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	"github.com/iotaledger/inx-tendercoo/pkg/mselection"
	"github.com/iotaledger/inx-tendercoo/pkg/nodebridge"
	inx "github.com/iotaledger/inx/go"
	flag "github.com/spf13/pflag"
	abciclient "github.com/tendermint/tendermint/abci/client"
	tmservice "github.com/tendermint/tendermint/libs/service"
	tmnode "github.com/tendermint/tendermint/node"
	rpclocal "github.com/tendermint/tendermint/rpc/client/local"
	"go.uber.org/atomic"
	"go.uber.org/dig"
	"go.uber.org/zap/zapcore"
)

const (
	// CfgCoordinatorBootstrap defines whether to bootstrap the network.
	CfgCoordinatorBootstrap = "cooBootstrap"
	// CfgCoordinatorStartIndex defines the index of the first milestone at bootstrap.
	CfgCoordinatorStartIndex = "cooStartIndex"

	// names of the background worker
	tendermintWorkerName = "Tendermint Node"
	decooWorkerName      = "Coordinator"
)

// ValidatorsConfig defines the config options for one validator.
// TODO: is there a better way to define multiple validators in the config?
type ValidatorsConfig struct {
	PubKey []byte `json:"pubKey" koanf:"pubKey"`
	Power  int64  `json:"power" koanf:"power"`
}

func init() {
	CoreComponent = &app.CoreComponent{
		Component: &app.Component{
			Name:      "DeCoo",
			DepsFunc:  func(cDeps dependencies) { deps = cDeps },
			Params:    params,
			Provide:   provide,
			Configure: configure,
			Run:       run,
		},
	}
}

var (
	CoreComponent *app.CoreComponent
	deps          dependencies

	// coordinator config
	bootstrap          = flag.Bool(CfgCoordinatorBootstrap, false, "bootstrap the network")
	startIndex         = flag.Uint32(CfgCoordinatorStartIndex, 0, "index of the first milestone at bootstrap")
	maxTrackedMessages int
	interval           time.Duration

	trackMessages            atomic.Bool
	confirmedMilestoneSignal chan uint32

	// closures
	onMessageSolid              *events.Closure
	onConfirmedMilestoneChanged *events.Closure
)

type dependencies struct {
	dig.In
	AppConfig      *configuration.Configuration `name:"appConfig"`
	NodeBridge     *nodebridge.NodeBridge
	Selector       *mselection.HeaviestSelector
	Coordinator    *decoo.Coordinator
	TendermintNode tmservice.Service
}

func provide(c *dig.Container) error {
	// provide the coordinator private key
	if err := c.Provide(func() (ed25519.PrivateKey, error) {
		// TODO: is it really safe to provide the private key?
		return loadEd25519PrivateKeyFromEnvironment("COO_PRV_KEY")
	}); err != nil {
		return err
	}

	// provide the coordinator
	type coordinatorDeps struct {
		dig.In
		AppConfig             *configuration.Configuration `name:"appConfig"`
		CoordinatorPrivateKey ed25519.PrivateKey
		NodeBridge            *nodebridge.NodeBridge
	}
	if err := c.Provide(func(deps coordinatorDeps) (*decoo.Coordinator, error) {
		CoreComponent.LogInfo("Providing Coordinator ...")
		defer CoreComponent.LogInfo("Providing Coordinator ... done")

		validators, err := loadValidatorsFromConfig(deps.AppConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to parse validators: %w", err)
		}

		// extract all validator public keys
		var members []ed25519.PublicKey
		for _, validator := range validators {
			members = append(members, validator.PubKey)
		}
		committee := decoo.NewCommittee(deps.CoordinatorPrivateKey, members...)

		// TODO: handle state storage
		coo, err := decoo.New(mapdb.NewMapDB(), committee, deps.NodeBridge, CoreComponent.Logger())
		if err != nil {
			return nil, fmt.Errorf("failed to create: %w", err)
		}
		// load the state or initialize with the given values
		err = coo.InitState(*bootstrap, *startIndex, deps.NodeBridge.LatestMilestone().MilestoneID)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize: %w", err)
		}
		return coo, nil
	}); err != nil {
		return err
	}

	// provide Tendermint
	type tendermintDeps struct {
		dig.In
		AppConfig             *configuration.Configuration `name:"appConfig"`
		CoordinatorPrivateKey ed25519.PrivateKey
		Coordinator           *decoo.Coordinator
	}
	if err := c.Provide(func(deps tendermintDeps) (tmservice.Service, error) {
		CoreComponent.LogInfo("Providing Tendermint ...")
		defer CoreComponent.LogInfo("Providing Tendermint ... done")

		conf, gen, err := loadTendermintConfig(deps.CoordinatorPrivateKey, deps.AppConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to load config: %w", err)
		}
		// use a separate logger for Tendermint
		log := logger.NewLogger("Tendermint")
		lvl, err := zapcore.ParseLevel(deps.AppConfig.String(CfgCoordinatorTendermintLogLevel))
		if err != nil {
			return nil, fmt.Errorf("invalid level: %w", err)
		}
		// this replays blocks until Tendermint and Coordinator are synced
		// TODO: do we need to issue the milestones of replayed blocks?
		return tmnode.New(conf, NewTenderLogger(log, lvl), abciclient.NewLocalCreator(deps.Coordinator), gen)
	}); err != nil {
		return err
	}

	// provide the heaviest branch selection strategy
	type selectorDeps struct {
		dig.In
		AppConfig *configuration.Configuration `name:"appConfig"`
	}
	if err := c.Provide(func(deps selectorDeps) *mselection.HeaviestSelector {
		return mselection.New(
			deps.AppConfig.Int(CfgCoordinatorTipselectMinHeaviestBranchUnreferencedMessagesThreshold),
			deps.AppConfig.Int(CfgCoordinatorTipselectMaxHeaviestBranchTipsPerCheckpoint),
			deps.AppConfig.Int(CfgCoordinatorTipselectRandomTipsPerCheckpoint),
			deps.AppConfig.Duration(CfgCoordinatorTipselectHeaviestBranchSelectionTimeout),
		)
	}); err != nil {
		return err
	}

	return nil
}

func configure() error {
	maxTrackedMessages = deps.AppConfig.Int(CfgCoordinatorMaxTrackedMessages)
	interval = deps.AppConfig.Duration(CfgCoordinatorInterval)

	confirmedMilestoneSignal = make(chan uint32, 1)

	// pass all new solid messages to the selector
	onMessageSolid = events.NewClosure(func(metadata *inx.MessageMetadata) {
		if !trackMessages.Load() || metadata.GetShouldReattach() {
			return
		}
		// add tips to the heaviest branch selector
		if trackedMessagesCount := deps.Selector.OnNewSolidMessage(metadata); trackedMessagesCount >= maxTrackedMessages {
			// if there are too many messages, trigger the latest milestone again. This will trigger a new milestone.
			latestMilestoneIndex := uint32(deps.NodeBridge.LatestMilestone().Index)
			select {
			case confirmedMilestoneSignal <- latestMilestoneIndex:
			default:
			}
		}
	})

	onConfirmedMilestoneChanged = events.NewClosure(func(_ *inx.Milestone) {
		// the selector needs to be reset after the milestone was confirmed, otherwise
		// it could contain tips that are already below max depth.
		deps.Selector.Reset()
		// make sure that new solid messages are tracked
		trackMessages.Store(true)
	})

	return nil
}

func run() error {
	if err := CoreComponent.Daemon().BackgroundWorker(decooWorkerName, func(ctx context.Context) {
		CoreComponent.LogInfo("Starting " + decooWorkerName + " ...")
		attachEvents()
		defer detachEvents()

		rpc, err := rpclocal.New(deps.TendermintNode.(rpclocal.NodeService))
		if err != nil {
			CoreComponent.LogPanicf("invalid Tendermint node: %s", err)
		}
		if err := deps.Coordinator.Start(ctx, rpc); err != nil {
			CoreComponent.LogPanicf("failed to start: %s", err)
		}
		CoreComponent.LogInfo("Starting " + decooWorkerName + " ... done")

		// run the coordinator and issue milestones
		coordinatorLoop(ctx)

		CoreComponent.LogInfo("Stopping " + decooWorkerName + " server ...")
		if err := deps.Coordinator.Stop(); err != nil {
			CoreComponent.LogWarn(err)
		}
		CoreComponent.LogInfo("Stopping " + decooWorkerName + " server ... done")
	}, daemon.PriorityStopCoordinator); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

	if err := CoreComponent.Daemon().BackgroundWorker(tendermintWorkerName, func(ctx context.Context) {
		CoreComponent.LogInfo("Starting " + tendermintWorkerName + " ...")
		if err := deps.TendermintNode.Start(); err != nil {
			CoreComponent.LogPanicf("failed to start: %s", err)
		}
		CoreComponent.LogInfo("Starting " + tendermintWorkerName + " ... done")

		<-ctx.Done()
		CoreComponent.LogInfo("Stopping " + tendermintWorkerName + " server ...")
		if err := deps.TendermintNode.Stop(); err != nil {
			CoreComponent.LogWarn(err)
		}
		CoreComponent.LogInfo("Stopping " + tendermintWorkerName + " server ... done")
	}, daemon.PriorityStopTendermint); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

	return nil
}

func coordinatorLoop(ctx context.Context) {
	latestMilestoneIndex := uint32(deps.NodeBridge.LatestMilestone().Index)
	onConfirmedMilestoneChanged = events.NewClosure(func(ms *inx.Milestone) {
		// milestone index must not be old
		index := ms.GetMilestoneInfo().GetMilestoneIndex()
		if index <= latestMilestoneIndex {
			return
		}
		// update the index and signal
		latestMilestoneIndex = index
		confirmedMilestoneSignal <- index

		/*
			var payload iotago.Milestone
			payload.Deserialize(ms.GetMilestone().GetData(), serializer.DeSeriModeNoValidation, nil)
			msg, _ := builder.NewMessageBuilder(payload.ProtocolVersion).Payload(&payload).ParentsMessageIDs(payload.Parents).Build()
			latestMilestoneMessageID := msg.MustID()
		*/
	})

	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Attach(onConfirmedMilestoneChanged)
	defer deps.NodeBridge.Events.ConfirmedMilestoneChanged.Detach(onConfirmedMilestoneChanged)

	// TODO: we need a default tip
	// a) when bootstrapping without a milestone, it can be any solid message. Genesis is not registered as solid?
	// b) when starting with a milestone, it can be any solid message containing the latest milestone
	// c) when running, we can use ConfirmedMilestoneChanged to recreate the latest milestone message

	timer := time.NewTimer(0)
	defer timer.Stop()
	index := latestMilestoneIndex
	for {
		select {
		case <-timer.C: // propose a parent for the milestone with index
			// TODO: how can we make sure that the previous milestone is always in the past-cone
			tips, err := deps.Selector.SelectTips(1)
			if err != nil {
				// TODO: use the default tip as fallback
				CoreComponent.LogPanicf("failed to select tips: %s", err)
			}
			// until the actual milestone is received, the job of the tip selector is done
			trackMessages.Store(false)
			if err := deps.Coordinator.ProposeParent(index+1, tips[0]); err != nil {
				CoreComponent.LogWarnf("failed to propose parent: %s", err)
			}

		case index = <-confirmedMilestoneSignal: // we have received a new milestone without proposing a parent
			// the timer has not yet fired, so we need to stop it before resetting
			if !timer.Stop() {
				<-timer.C
			}
			// TODO: reset to MS.time + interval
			timer.Reset(interval)
			continue

		case <-ctx.Done(): // end the loop
			return
		}

		// when this select is reach, the timer has fired and a milestone was proposed
		select {
		case index = <-confirmedMilestoneSignal: // the new milestone is confirmed, we can now reset the timer
			timer.Reset(interval) // the timer has already fired, so we can safely reset it

		case <-ctx.Done(): // end the loop
			return
		}
	}
}

func attachEvents() {
	deps.NodeBridge.Events.MessageSolid.Attach(onMessageSolid)
	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Attach(onConfirmedMilestoneChanged)
}

func detachEvents() {
	deps.NodeBridge.Events.MessageSolid.Detach(onMessageSolid)
	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Detach(onConfirmedMilestoneChanged)
}

// loadEd25519PrivateKeyFromEnvironment loads ed25519 private keys from the given environment variable.
func loadEd25519PrivateKeyFromEnvironment(name string) (ed25519.PrivateKey, error) {
	value, exists := os.LookupEnv(name)
	if !exists {
		return nil, fmt.Errorf("environment variable '%s' not set", name)
	}
	if l := len(value); l != hex.EncodedLen(ed25519.SeedSize) {
		return nil, fmt.Errorf("environment variable '%s' has invalid length: actual=%d expected=%d", name, l, hex.EncodedLen(ed25519.SeedSize))
	}

	// TODO: using the seed is the correct way, do we need to make it compatible with private key?
	seed, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("environment variable '%s' contains an invalid private key: %w", name, err)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func loadValidatorsFromConfig(config *configuration.Configuration) (map[string]ValidatorsConfig, error) {
	validators := map[string]ValidatorsConfig{}
	for _, name := range config.MapKeys(CfgCoordinatorTendermintValidators) {
		configKey := CfgCoordinatorTendermintValidators + "." + name

		validatorConfig := ValidatorsConfig{}
		if err := config.Unmarshal(configKey, &validatorConfig); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", strconv.Quote(configKey), err)
		}
		if l := len(validatorConfig.PubKey); l != hex.EncodedLen(ed25519.PublicKeySize) {
			return nil, fmt.Errorf("invalid key length: %d", l)
		}

		var err error
		validatorConfig.PubKey, err = hex.DecodeString(string(validatorConfig.PubKey))
		if err != nil {
			return nil, err
		}
		validators[name] = validatorConfig
	}
	return validators, nil
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return !os.IsNotExist(err)
}
