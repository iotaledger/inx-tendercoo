package decoo

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	flag "github.com/spf13/pflag"
	"github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/service"
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
	"github.com/iotaledger/hive.go/core/timeutil"
	"github.com/iotaledger/inx-app/pkg/nodebridge"
	"github.com/iotaledger/inx-tendercoo/pkg/daemon"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
	"github.com/iotaledger/inx-tendercoo/pkg/selector"
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
	// INXTimeout defines the timeout after which INX API calls are canceled.
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

	log *logger.Logger // shortcut for CoreComponent.Logger

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

// Coordinator contains the methods used from a coordinator.
type Coordinator interface {
	Start(decoo.ABCIClient) error
	Stop() error

	ProposeParent(context.Context, uint32, iotago.BlockID) error
}

// MilestoneIndexFuc is a function returning the latest and confirmed milestone index.
type MilestoneIndexFuc func() (uint32, uint32)

type dependencies struct {
	dig.In
	Selector           selector.TipSelector
	NodeBridge         *nodebridge.NodeBridge
	MilestoneIndexFunc MilestoneIndexFuc
	TangleListener     *nodebridge.TangleListener
	Coordinator        Coordinator
	TendermintNode     *tmnode.Node
}

func provide(c *dig.Container) error {
	// initialize the logger
	log = CoreComponent.Logger()

	// provide the heaviest branch selection strategy
	if err := c.Provide(func() selector.TipSelector {
		return selector.NewHeaviest(Parameters.TipSel.MaxTips, Parameters.TipSel.ReducedConfirmationLimit, Parameters.TipSel.Timeout)
	}); err != nil {
		return err
	}

	// provide a function returning the latest and confirmed milestone index
	if err := c.Provide(func(nodeBridge *nodebridge.NodeBridge) MilestoneIndexFuc {
		return func() (uint32, uint32) {
			nodeStatus := nodeBridge.NodeStatus()
			lmi := nodeStatus.GetLatestMilestone().GetMilestoneInfo().GetMilestoneIndex()
			cmi := nodeStatus.GetConfirmedMilestone().GetMilestoneInfo().GetMilestoneIndex()

			return lmi, cmi
		}
	}); err != nil {
		return err
	}

	// provide the node bridge tangle listener
	if err := c.Provide(nodebridge.NewTangleListener); err != nil {
		return err
	}

	// provide the decentralized coordinator
	type deCooDeps struct {
		dig.In
		NodeBridge     *nodebridge.NodeBridge
		TangleListener *nodebridge.TangleListener
	}
	if err := c.Provide(func(deps deCooDeps) (*decoo.Coordinator, error) {
		log.Info("Providing Coordinator ...")
		defer log.Info("Providing Coordinator ... done")

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
		maxRetainBlocks := Parameters.Tendermint.MaxRetainBlocks
		whiteFlagTimeout := Parameters.WhiteFlagParentsSolidTimeout
		coo, err := decoo.New(committee,
			maxRetainBlocks,
			whiteFlagTimeout,
			&INXClient{deps.NodeBridge},
			deps.TangleListener,
			log)
		if err != nil {
			return nil, fmt.Errorf("failed to provide coordinator: %w", err)
		}

		return coo, nil
	}); err != nil {
		return err
	}

	// provide the coordinator abstraction
	if err := c.Provide(func(deCoo *decoo.Coordinator) Coordinator { return deCoo }); err != nil {
		return err
	}

	// provide Tendermint
	type tendermintDeps struct {
		dig.In
		DeCoo          *decoo.Coordinator
		NodeBridge     *nodebridge.NodeBridge
		TangleListener *nodebridge.TangleListener
	}
	if err := c.Provide(func(deps tendermintDeps) (*tmnode.Node, error) {
		log.Info("Providing Tendermint ...")
		defer log.Info("Providing Tendermint ... done")

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

		// initialize the coordinator matching the configured Tendermint state
		ctx := CoreComponent.Daemon().ContextStopped()
		if err := initCoordinator(ctx, deps.DeCoo, deps.NodeBridge, deps.TangleListener, conf); err != nil {
			return nil, fmt.Errorf("failed to initialize coordinator: %w", err)
		}

		// use a separate logger for Tendermint
		lvl, err := zapcore.ParseLevel(Parameters.Tendermint.LogLevel)
		if err != nil {
			return nil, fmt.Errorf("invalid log level: %w", err)
		}
		tenderLogger := NewTenderLogger(CoreComponent.App().NewLogger("Tendermint"), lvl)

		pval := privval.LoadFilePV(conf.PrivValidatorKeyFile(), conf.PrivValidatorStateFile())
		nodeKey, err := p2p.LoadNodeKey(conf.NodeKeyFile())
		if err != nil {
			return nil, fmt.Errorf("failed to load node key %s: %w", conf.NodeKeyFile(), err)
		}

		// start Tendermint, this replays blocks until Tendermint and Coordinator are synced
		node, err := tmnode.NewNode(conf,
			pval,
			nodeKey,
			proxy.NewLocalClientCreator(deps.DeCoo),
			func() (*tmtypes.GenesisDoc, error) { return gen, nil },
			tmnode.DefaultDBProvider,
			tmnode.DefaultMetricsProvider(conf.Instrumentation),
			tenderLogger)
		if err != nil {
			return nil, fmt.Errorf("failed to provide Tendermint: %w", err)
		}

		// make sure that Tendermint is stopped gracefully when the coordinator terminates unexpectedly
		go func() {
			deps.DeCoo.Wait()
			// if the daemon is stopped, the coordinator stop was expectedly
			if CoreComponent.Daemon().IsStopped() {
				return
			}
			// otherwise stop the Tendermint node and panic
			if err := node.Stop(); err != nil && !errors.Is(err, service.ErrAlreadyStopped) {
				log.Warnf("failed to stop Tendermint: %s", err)
			}
			log.Panic("Coordinator unexpectedly stopped")
		}()

		return node, nil
	}); err != nil {
		return err
	}

	return nil
}

func initCoordinator(ctx context.Context, coordinator *decoo.Coordinator, nodeBridge *nodebridge.NodeBridge, listener *nodebridge.TangleListener, conf *config.Config) error {
	if bootstrap {
		// assure that the startMilestoneBlockID is solid before bootstrapping the coordinator
		if err := waitUntilBlockSolid(ctx, listener, startMilestoneBlockID); err != nil {
			return err
		}

		if err := coordinator.Bootstrap(bootstrapForce, startIndex, startMilestoneID, startMilestoneBlockID); err != nil {
			log.Warnf("Fail-safe prevented bootstrapping with these parameters. If you know what you are doing, "+
				"you can additionally use the %s flag to disable any fail-safes.", strconv.Quote(CfgCoordinatorBootstrapForce))

			return fmt.Errorf("bootstrap failed: %w", err)
		}

		return nil
	}

	// for the coordinator the Tendermint app state corresponds to the Tangle
	// when the plugin was stopped but the corresponding node was running, this can lead to a situation where the latest
	// milestone is newer than the core Tendermint state of the plugin
	// as this is illegal in the Tendermint API, we retrieve the block height from the Tendermint core and try to find
	// the corresponding milestone to use for the coordinator app state
	pv := privval.LoadFilePV(conf.PrivValidatorKeyFile(), conf.PrivValidatorStateFile())
	// in the worst case, the height can be off by one, as the state files gets written before the DB
	// as it is hard to detect this particular case, we always subtract one to be on the safe side
	tendermintHeight := pv.LastSignState.Height - 1
	// this should not happen during resuming, as Tendermint needs to sign at least two blocks to produce a milestone
	if tendermintHeight < 0 {
		return errors.New("resume failed: no Tendermint state available")
	}

	// creating a new Tendermint node, starts the replay of blocks
	// in order to correctly validate those Tendermint blocks, we need to be synced
	for {
		nodeStatus := nodeBridge.NodeStatus()
		lmi := nodeStatus.GetLatestMilestone().GetMilestoneInfo().GetMilestoneIndex()
		cmi := nodeStatus.GetConfirmedMilestone().GetMilestoneInfo().GetMilestoneIndex()
		if lmi > 0 && lmi == cmi {
			break
		}
		log.Warnf("node is not synced; retrying in %s", SyncRetryInterval)
		if !timeutil.Sleep(ctx, SyncRetryInterval) {
			return ctx.Err()
		}
	}

	// start from the latest confirmed milestone as the node should contain its previous milestones
	ms, err := nodeBridge.ConfirmedMilestone()
	if err != nil {
		return fmt.Errorf("failed to retrieve latest milestone: %w", err)
	}
	if ms == nil {
		return errors.New("failed to retrieve latest milestone: no milestone available")
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
		ms, err = getMilestone(ctx, nodeBridge, state.MilestoneIndex-1)
		if err != nil {
			return fmt.Errorf("milestone %d cannot be retrieved: %w", state.MilestoneIndex-1, err)
		}
	}

	if err := coordinator.InitState(ms.Milestone); err != nil {
		return fmt.Errorf("resume failed: %w", err)
	}

	return nil
}

func waitUntilBlockSolid(ctx context.Context, listener *nodebridge.TangleListener, blockID iotago.BlockID) error {
	ctx, cancel := context.WithTimeout(ctx, INXTimeout)
	defer cancel()

	solidEvent, err := listener.RegisterBlockSolidEvent(ctx, blockID)
	if err != nil {
		return err
	}

	log.Infof("waiting for block %s to become solid", blockID)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-solidEvent:
		return nil
	}
}

func getMilestone(ctx context.Context, nodeBridge *nodebridge.NodeBridge, index uint32) (*nodebridge.Milestone, error) {
	ctx, cancel := context.WithTimeout(ctx, INXTimeout)
	defer cancel()

	ms, err := nodeBridge.Milestone(ctx, index)
	if err != nil {
		return nil, err
	}

	return ms, nil
}

func configure() error {
	confirmedMilestoneSignal = make(chan milestoneInfo, 1)
	triggerNextMilestone = make(chan struct{}, 1)

	// for each solid block, add it to the selector and preemptively trigger the next milestone if needed
	// This assumes that solid block events are always triggered in the correct order, and that solid block events in
	// the past cone of a milestone are generally triggered before its milestone confirmation event.
	// Race conditions, i.e. solid blocks that come after their confirming milestone event, are included in the selector
	// even though they are no longer valid parents. In the very rare case, where this happens and the SelectTips is
	// also called before the milestone's solid block event has been processed, it could cause Coordinator.ProposeParent
	// to propose such an invalid parent, fail and be retried.
	onBlockSolid = events.NewClosure(func(metadata *inx.BlockMetadata) {
		// do not track more blocks than the configured limit
		if deps.Selector.TrackedBlocks() >= Parameters.MaxTrackedBlocks {
			return
		}
		// ignore blocks that are not valid parents
		if !decoo.ValidParent(metadata) {
			return
		}

		// add tips to the heaviest branch selector, if there are too many blocks, trigger a new milestone
		if trackedBlocksCount := deps.Selector.OnNewSolidBlock(metadata); trackedBlocksCount >= Parameters.MaxTrackedBlocks {
			log.Info("trigger next milestone preemptively")
			select {
			case triggerNextMilestone <- struct{}{}:
			default:
				// if the channel is full, there is already one unprocessed trigger, and we don't need to signal again
			}
		}
	})

	// for each confirmed milestone, reset the selector and signal its arrival for the coordinator loop
	onConfirmedMilestoneChanged = events.NewClosure(func(milestone *nodebridge.Milestone) {
		// reset the tip selection after each new milestone to only add unconfirmed blocks to the selector
		deps.Selector.Reset()

		// ignore new confirmed milestones during syncing
		if lmi := deps.NodeBridge.LatestMilestoneIndex(); lmi > milestone.Milestone.Index {
			log.Debugf("node is not synced; latest=%d confirmed=%d", lmi, milestone.Milestone.Index)

			return
		}
		log.Infof("new confirmed milestone: %d", milestone.Milestone.Index)
		processConfirmedMilestone(milestone.Milestone)
	})

	return nil
}

func run() error {
	if err := CoreComponent.Daemon().BackgroundWorker(tangleListenerWorkerName, func(ctx context.Context) {
		log.Info("Starting " + tangleListenerWorkerName + " ... done")
		deps.TangleListener.Run(ctx)
		log.Info("Stopping " + tangleListenerWorkerName + " ... done")
	}, daemon.PriorityStopTangleListener); err != nil {
		log.Panicf("failed to start worker: %s", err)
	}

	if err := CoreComponent.Daemon().BackgroundWorker(tendermintWorkerName, func(ctx context.Context) {
		log.Info("Starting " + tendermintWorkerName + " ...")
		if err := deps.TendermintNode.Start(); err != nil {
			log.Panicf("failed to start: %s", err)
		}

		addr, _ := deps.TendermintNode.NodeInfo().NetAddress()
		pubKey, _ := deps.TendermintNode.PrivValidator().GetPubKey()
		log.Infof("Started "+tendermintWorkerName+": Address=%s, ConsensusPublicKey=%s",
			addr, types.Byte32FromSlice(pubKey.Bytes()))

		<-ctx.Done()
		log.Info("Stopping " + tendermintWorkerName + " ...")
		if err := deps.TendermintNode.Stop(); err != nil {
			log.Warn(err)
		}
		log.Info("Stopping " + tendermintWorkerName + " ... done")
	}, daemon.PriorityStopTendermint); err != nil {
		log.Panicf("failed to start worker: %s", err)
	}

	if err := CoreComponent.Daemon().BackgroundWorker(decooWorkerName, func(ctx context.Context) {
		log.Info("Starting " + decooWorkerName + " ...")

		if err := initialize(); err != nil {
			log.Panicf("failed to initialize: %s", err)
		}

		// it is now safe to attach the events
		attachEvents()
		defer detachEvents()

		rpc := rpclocal.New(deps.TendermintNode)
		if err := deps.Coordinator.Start(rpc); err != nil {
			log.Panicf("failed to start: %s", err)
		}
		log.Info("Starting " + decooWorkerName + " ... done")

		// run the coordinator and issue milestones
		coordinatorLoop(ctx)

		log.Info("Stopping " + decooWorkerName + " ...")
		if err := deps.Coordinator.Stop(); err != nil {
			log.Warn(err)
		}
		log.Info("Stopping " + decooWorkerName + " ... done")
	}, daemon.PriorityStopCoordinator); err != nil {
		log.Panicf("failed to start worker: %s", err)
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
