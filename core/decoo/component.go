package decoo

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"strconv"
	"time"

	flag "github.com/spf13/pflag"
	"github.com/tendermint/tendermint/config"
	tmnode "github.com/tendermint/tendermint/node"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	"github.com/tendermint/tendermint/proxy"
	rpclocal "github.com/tendermint/tendermint/rpc/client/local"
	tmtypes "github.com/tendermint/tendermint/types"
	"go.uber.org/dig"
	"go.uber.org/zap/zapcore"

	"github.com/iotaledger/hive.go/core/app"
	"github.com/iotaledger/hive.go/core/events"
	"github.com/iotaledger/hive.go/core/logger"
	"github.com/iotaledger/inx-app/nodebridge"
	"github.com/iotaledger/inx-tendercoo/pkg/daemon"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
	"github.com/iotaledger/inx-tendercoo/pkg/mselection"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/keymanager"
)

const (
	// AppName is the name of the decoo plugin.
	AppName = "Coordinator"

	// CfgCoordinatorBootstrap defines whether the network is bootstrapped.
	CfgCoordinatorBootstrap = "cooBootstrap"
	// CfgCoordinatorStartIndex defines the index of the first milestone at bootstrap.
	CfgCoordinatorStartIndex = "cooStartIndex"
	// CfgCoordinatorStartMilestoneID defines the previous milestone ID at bootstrap.
	CfgCoordinatorStartMilestoneID = "cooStartMilestoneID"
	// CfgCoordinatorStartMilestoneBlockID defines the previous milestone block ID at bootstrap.
	CfgCoordinatorStartMilestoneBlockID = "cooStartMilestoneBlockID"
	// CfgCoordinatorBootstrapForce defines whether the network bootstrap is forced, disabling all fail-safes.
	CfgCoordinatorBootstrapForce = "cooBootstrapForce"

	// EnvMilestonePrivateKey defines the name of the environment variable containing the key used for signing milestones.
	EnvMilestonePrivateKey = "COO_PRV_KEY"
	// SyncRetryInterval defines the time to wait before retrying an un-synced node.
	SyncRetryInterval = 2 * time.Second
	// INXTimeout defines the timeout after which INX API calls are cancelled.
	INXTimeout = 5 * time.Second

	tangleListenerWorkerName = "TangleListener"
	tendermintWorkerName     = "Tendermint Node"
	decooWorkerName          = "Coordinator"
)

func init() {
	flag.BoolVar(&bootstrap, CfgCoordinatorBootstrap, false, "whether the network is bootstrapped")
	flag.Uint32Var(&startIndex, CfgCoordinatorStartIndex, 1, "index of the first milestone at bootstrap")
	flag.Var(types.NewByte32(iotago.EmptyBlockID(), (*[32]byte)(&startMilestoneID)), CfgCoordinatorStartMilestoneID, "the previous milestone ID at bootstrap")
	flag.Var(types.NewByte32(iotago.EmptyBlockID(), (*[32]byte)(&startMilestoneBlockID)), CfgCoordinatorStartMilestoneBlockID, "previous milestone block ID at bootstrap")
	flag.BoolVar(&bootstrapForce, CfgCoordinatorBootstrapForce, false, "whether the network bootstrap is forced")

	CoreComponent = &app.CoreComponent{
		Component: &app.Component{
			Name:      AppName,
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

	// config flags.
	bootstrap             bool
	startIndex            uint32
	startMilestoneID      iotago.MilestoneID
	startMilestoneBlockID iotago.BlockID
	bootstrapForce        bool

	// closures.
	onBlockSolid                *events.Closure
	onConfirmedMilestoneChanged *events.Closure
)

type dependencies struct {
	dig.In
	Coordinator    *decoo.Coordinator
	Selector       *mselection.HeaviestSelector
	TendermintNode *tmnode.Node
	NodeBridge     *nodebridge.NodeBridge
	TangleListener *nodebridge.TangleListener
}

func provide(c *dig.Container) error {
	// provide the node bridge tangle listener
	if err := c.Provide(nodebridge.NewTangleListener); err != nil {
		return err
	}

	// provide the coordinator
	type coordinatorDeps struct {
		dig.In
		NodeBridge     *nodebridge.NodeBridge
		TangleListener *nodebridge.TangleListener
	}
	if err := c.Provide(func(deps coordinatorDeps) (*decoo.Coordinator, error) {
		CoreComponent.LogInfo("Providing Coordinator ...")
		defer CoreComponent.LogInfo("Providing Coordinator ... done")

		// load the private key used for singing milestones
		coordinatorPrivateKey, err := privateKeyFromEnvironment(EnvMilestonePrivateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load coordinator private key: %w", err)
		}
		// load the public keys for validating milestones
		keyManager := keymanager.New()
		for _, keyRange := range deps.NodeBridge.NodeConfig.GetMilestoneKeyRanges() {
			keyManager.AddKeyRange(keyRange.GetPublicKey(), keyRange.GetStartIndex(), keyRange.GetEndIndex())
		}
		// load the consensus parameters
		n := len(Parameters.Tendermint.Validators)
		t := int(deps.NodeBridge.NodeConfig.GetMilestonePublicKeyCount())

		committee := decoo.NewCommitteeFromManager(coordinatorPrivateKey, n, t, keyManager)
		coo, err := decoo.New(committee, &INXClient{deps.NodeBridge}, deps.TangleListener, CoreComponent.Logger())
		if err != nil {
			return nil, fmt.Errorf("failed to provide coordinator: %w", err)
		}

		return coo, nil
	}); err != nil {
		return err
	}

	// provide Tendermint
	type tendermintDeps struct {
		dig.In
		Coordinator *decoo.Coordinator
		NodeBridge  *nodebridge.NodeBridge
	}
	if err := c.Provide(func(deps tendermintDeps) (*tmnode.Node, error) {
		CoreComponent.LogInfo("Providing Tendermint ...")
		defer CoreComponent.LogInfo("Providing Tendermint ... done")

		consensusPrivateKey, err := privateKeyFromString(Parameters.Tendermint.ConsensusPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load consensus private key: %w", err)
		}
		nodePrivateKey, err := privateKeyFromString(Parameters.Tendermint.NodePrivateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load node private key: %w", err)
		}
		networkName := deps.NodeBridge.ProtocolParameters().NetworkName

		conf, gen, err := loadTendermintConfig(consensusPrivateKey, nodePrivateKey, networkName)
		if err != nil {
			return nil, fmt.Errorf("failed to load config: %w", err)
		}
		// initialize the coordinator compatible with the configured Tendermint state
		if err := initCoordinator(deps.Coordinator, deps.NodeBridge, conf); err != nil {
			return nil, fmt.Errorf("failed to initialize coordinator: %w", err)
		}

		// use a separate logger for Tendermint
		log := logger.NewLogger("Tendermint")
		lvl, err := zapcore.ParseLevel(Parameters.Tendermint.LogLevel)
		if err != nil {
			return nil, fmt.Errorf("invalid log level: %w", err)
		}

		pval := privval.LoadFilePV(conf.PrivValidatorKeyFile(), conf.PrivValidatorStateFile())
		nodeKey, err := p2p.LoadNodeKey(conf.NodeKeyFile())
		if err != nil {
			return nil, fmt.Errorf("failed to load node key %s: %w", conf.NodeKeyFile(), err)
		}

		// start Tendermint, this replays blocks until Tendermint and Coordinator are synced
		node, err := tmnode.NewNode(conf,
			pval,
			nodeKey,
			proxy.NewLocalClientCreator(deps.Coordinator),
			func() (*tmtypes.GenesisDoc, error) { return gen, nil },
			tmnode.DefaultDBProvider,
			tmnode.DefaultMetricsProvider(conf.Instrumentation),
			NewTenderLogger(log, lvl))
		if err != nil {
			return nil, fmt.Errorf("failed to provide Tendermint: %w", err)
		}

		return node, nil
	}); err != nil {
		return err
	}

	// provide the heaviest branch selection strategy
	if err := c.Provide(func() *mselection.HeaviestSelector {
		return mselection.New(Parameters.TipSel.MaxTips, Parameters.TipSel.ReducedConfirmationLimit, Parameters.TipSel.Timeout)
	}); err != nil {
		return err
	}

	return nil
}

func initCoordinator(coordinator *decoo.Coordinator, nodeBridge *nodebridge.NodeBridge, conf *config.Config) error {
	if bootstrap {
		if err := coordinator.Bootstrap(bootstrapForce, startIndex, startMilestoneID, startMilestoneBlockID); err != nil {
			CoreComponent.LogWarnf("Fail-safe prevented bootstrapping with these parameters. If you know what you are doing, "+
				"you can additionally use the %s flag to disable any fail-safes.", strconv.Quote(CfgCoordinatorBootstrapForce))

			return fmt.Errorf("bootstrap failed: %w", err)
		}

		return nil
	}

	pv := privval.LoadFilePV(conf.PrivValidatorKeyFile(), conf.PrivValidatorStateFile())
	tendermintHeight := pv.LastSignState.Height

	// start from the latest confirmed milestone as the node should contain its previous milestones
	ms, err := nodeBridge.ConfirmedMilestone()
	if err != nil {
		return fmt.Errorf("failed to retrieve latest milestone: %w", err)
	}

	// find a milestone that is compatible with the Tendermint block height
	for {
		state, err := decoo.NewStateFromMilestone(ms.Milestone)
		if err != nil {
			return fmt.Errorf("milestone %d contains invalid metadata: %w", ms.Milestone.Index, err)
		}
		if tendermintHeight >= state.MilestoneHeight {
			break
		}

		// try the previous milestone
		ms, err = getMilestone(nodeBridge, state.MilestoneIndex-1)
		if err != nil {
			return fmt.Errorf("milestone %d cannot be retrieved: %w", state.MilestoneIndex-1, err)
		}
	}

	if err := coordinator.InitState(ms.Milestone); err != nil {
		return fmt.Errorf("resume failed: %w", err)
	}

	return nil
}

func getMilestone(nodeBridge *nodebridge.NodeBridge, index uint32) (*nodebridge.Milestone, error) {
	ctx, cancel := context.WithTimeout(context.Background(), INXTimeout)
	defer cancel()

	ms, err := nodeBridge.Milestone(ctx, index)
	if err != nil || ms == nil {
		return nil, err
	}

	return ms, nil
}

func configure() error {
	newMilestoneSignal = make(chan milestoneInfo, 1)

	// pass all new solid blocks to the selector and preemptively trigger new milestone when needed
	onBlockSolid = events.NewClosure(func(metadata *inx.BlockMetadata) {
		// ignore blocks that are too old
		if metadata.GetShouldReattach() {
			return
		}
		// add tips to the heaviest branch selector
		// if there are too many blocks, trigger the latest milestone again. This will trigger a new milestone.
		if trackedBlocksCount := deps.Selector.OnNewSolidBlock(metadata); trackedBlocksCount >= Parameters.MaxTrackedBlocks {
			CoreComponent.LogInfo("trigger next milestone preemptively")
			triggerNextMilestone()
		}
	})

	// pass all new confirmed milestones to the coordinator loop
	onConfirmedMilestoneChanged = events.NewClosure(func(milestone *nodebridge.Milestone) {
		// ignore new confirmed milestones during syncing
		if !deps.NodeBridge.IsNodeSynced() {
			return
		}
		CoreComponent.LogInfof("new confirmed milestone: %d", milestone.Milestone.Index)
		processConfirmedMilestone(milestone.Milestone)
	})

	return nil
}

func run() error {
	if err := CoreComponent.Daemon().BackgroundWorker(tangleListenerWorkerName, func(ctx context.Context) {
		CoreComponent.LogInfo("Starting " + tangleListenerWorkerName + " ... done")
		deps.TangleListener.Run(ctx)
		CoreComponent.LogInfo("Stopping " + tangleListenerWorkerName + " ... done")
	}, daemon.PriorityStopTangleListener); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

	if err := CoreComponent.Daemon().BackgroundWorker(tendermintWorkerName, func(ctx context.Context) {
		CoreComponent.LogInfo("Starting " + tendermintWorkerName + " ...")
		if err := deps.TendermintNode.Start(); err != nil {
			CoreComponent.LogPanicf("failed to start: %s", err)
		}

		addr, _ := deps.TendermintNode.NodeInfo().NetAddress()
		pubKey, _ := deps.TendermintNode.PrivValidator().GetPubKey()
		CoreComponent.LogInfof("Started "+tendermintWorkerName+": Address=%s, ConsensusPublicKey=%s",
			addr, types.Byte32FromSlice(pubKey.Bytes()))

		<-ctx.Done()
		CoreComponent.LogInfo("Stopping " + tendermintWorkerName + " ...")
		if err := deps.TendermintNode.Stop(); err != nil {
			CoreComponent.LogWarn(err)
		}
		CoreComponent.LogInfo("Stopping " + tendermintWorkerName + " ... done")
	}, daemon.PriorityStopTendermint); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

	if err := CoreComponent.Daemon().BackgroundWorker(decooWorkerName, func(ctx context.Context) {
		CoreComponent.LogInfo("Starting " + decooWorkerName + " ...")

		if err := initialize(); err != nil {
			CoreComponent.LogPanicf("failed to initialize: %s", err)
		}

		// it is now safe to attach the events
		attachEvents()
		defer detachEvents()

		rpc := rpclocal.New(deps.TendermintNode)
		if err := deps.Coordinator.Start(rpc); err != nil {
			CoreComponent.LogPanicf("failed to start: %s", err)
		}
		CoreComponent.LogInfo("Starting " + decooWorkerName + " ... done")

		// run the coordinator and issue milestones
		coordinatorLoop(ctx)

		CoreComponent.LogInfo("Stopping " + decooWorkerName + " ...")
		if err := deps.Coordinator.Stop(); err != nil {
			CoreComponent.LogWarn(err)
		}
		CoreComponent.LogInfo("Stopping " + decooWorkerName + " ... done")
	}, daemon.PriorityStopCoordinator); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

	return nil
}

func attachEvents() {
	deps.TangleListener.Events.BlockSolid.Hook(onBlockSolid)
	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Hook(onConfirmedMilestoneChanged)
}

func detachEvents() {
	deps.TangleListener.Events.BlockSolid.Detach(onBlockSolid)
	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Detach(onConfirmedMilestoneChanged)
}

// privateKeyFromEnvironment loads ed25519 private keys from the given environment variable.
func privateKeyFromEnvironment(name string) (ed25519.PrivateKey, error) {
	value, exists := os.LookupEnv(name)
	if !exists {
		return nil, fmt.Errorf("environment variable %s not set", strconv.Quote(name))
	}
	key, err := privateKeyFromString(value)
	if err != nil {
		return nil, fmt.Errorf("environment variable %s contains an invalid private key: %w", strconv.Quote(name), err)
	}

	return key, nil
}

func privateKeyFromString(s string) (ed25519.PrivateKey, error) {
	var seed types.Byte32
	if err := seed.Set(s); err != nil {
		return nil, err
	}

	return ed25519.NewKeyFromSeed(seed[:]), nil
}

func fileExists(name string) bool {
	_, err := os.Stat(name)

	return !os.IsNotExist(err)
}
