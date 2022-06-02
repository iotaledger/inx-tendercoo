package decoo

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/events"
	"github.com/iotaledger/hive.go/kvstore/mapdb"
	"github.com/iotaledger/hive.go/logger"
	"github.com/iotaledger/inx-app/nodebridge"
	"github.com/iotaledger/inx-tendercoo/pkg/daemon"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
	"github.com/iotaledger/inx-tendercoo/pkg/mselection"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
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
	// CfgCoordinatorBootstrap defines whether the network is bootstrapped.
	CfgCoordinatorBootstrap = "cooBootstrap"
	// CfgCoordinatorStartIndex defines the index of the first milestone at bootstrap.
	CfgCoordinatorStartIndex = "cooStartIndex"
	// CfgCoordinatorStartMilestoneID defines the previous milestone ID at bootstrap.
	CfgCoordinatorStartMilestoneID = "cooStartMilestoneID"
	// CfgCoordinatorStartMilestoneMessageID defines the previous milestone message ID at bootstrap.
	CfgCoordinatorStartMilestoneMessageID = "cooStartMilestoneMessageID"

	// names of the background worker
	tangleListenerWorkerName = "TangleListener"
	tendermintWorkerName     = "Tendermint Node"
	decooWorkerName          = "Coordinator"
)

// ValidatorsConfig defines the config options for one validator.
// TODO: what is the best way to define PubKey as a [32]byte
type ValidatorsConfig struct {
	PubKey string `json:"pubKey" koanf:"pubKey"`
	Power  int64  `json:"power" koanf:"power"`
}

func init() {
	flag.BoolVar(&bootstrap, CfgCoordinatorBootstrap, false, "whether the network is bootstrapped")
	flag.Uint32Var(&startIndex, CfgCoordinatorStartIndex, 1, "index of the first milestone at bootstrap")
	flag.Var(types.NewByte32(iotago.EmptyBlockID(), (*[32]byte)(&startMilestoneID)), CfgCoordinatorStartMilestoneID, "the previous milestone ID at bootstrap")
	flag.Var(types.NewByte32(iotago.EmptyBlockID(), (*[32]byte)(&startMilestoneMessageID)), CfgCoordinatorStartMilestoneMessageID, "previous milestone message ID at bootstrap")

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

	// config flags
	bootstrap               bool
	startIndex              uint32
	startMilestoneID        iotago.MilestoneID
	startMilestoneMessageID iotago.BlockID

	latestMilestone struct {
		sync.Mutex
		MilestoneInfo
	}
	newMilestoneSignal chan MilestoneInfo
	trackMessages      atomic.Bool

	// closures
	onMessageSolid              *events.Closure
	onConfirmedMilestoneChanged *events.Closure
)

type dependencies struct {
	dig.In
	Coordinator    *decoo.Coordinator
	Selector       *mselection.HeaviestSelector
	TendermintNode tmservice.Service
	NodeBridge     *nodebridge.NodeBridge
	TangleListener *nodebridge.TangleListener
}

func provide(c *dig.Container) error {
	// provide the coordinator private key
	if err := c.Provide(func() (ed25519.PrivateKey, error) {
		// TODO: is it really safe to provide the private key?
		return loadEd25519PrivateKeyFromEnvironment("COO_PRV_KEY")
	}); err != nil {
		return err
	}

	// provide the node bridge tangle listener
	if err := c.Provide(nodebridge.NewTangleListener); err != nil {
		return err
	}

	// provide the coordinator
	type coordinatorDeps struct {
		dig.In
		CoordinatorPrivateKey ed25519.PrivateKey
		NodeBridge            *nodebridge.NodeBridge
		TangleListener        *nodebridge.TangleListener
	}
	if err := c.Provide(func(deps coordinatorDeps) (*decoo.Coordinator, error) {
		CoreComponent.LogInfo("Providing Coordinator ...")
		defer CoreComponent.LogInfo("Providing Coordinator ... done")

		// extract all validator public keys
		var members []ed25519.PublicKey
		for name, validator := range ParamsCoordinator.Tendermint.Validators {
			var pubKey types.Byte32
			if err := pubKey.Set(validator.PubKey); err != nil {
				return nil, fmt.Errorf("invalid pubKey for tendermint validator %s: %w", strconv.Quote(name), err)
			}
			members = append(members, pubKey[:])
		}
		committee := decoo.NewCommittee(deps.CoordinatorPrivateKey, members...)

		// TODO: handle state storage
		coo, err := decoo.New(mapdb.NewMapDB(), committee, deps.NodeBridge, deps.TangleListener, CoreComponent.Logger())
		if err != nil {
			return nil, fmt.Errorf("failed to create: %w", err)
		}
		// load the state or initialize with the given values
		err = coo.InitState(bootstrap, &decoo.State{
			Height:                0,
			CurrentMilestoneIndex: startIndex,
			LastMilestoneID:       startMilestoneID,
			LastMilestoneMsgID:    startMilestoneMessageID,
		})
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
		CoordinatorPrivateKey ed25519.PrivateKey
		Coordinator           *decoo.Coordinator
	}
	if err := c.Provide(func(deps tendermintDeps) (tmservice.Service, error) {
		CoreComponent.LogInfo("Providing Tendermint ...")
		defer CoreComponent.LogInfo("Providing Tendermint ... done")

		conf, gen, err := loadTendermintConfig(deps.CoordinatorPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load config: %w", err)
		}
		// use a separate logger for Tendermint
		log := logger.NewLogger("Tendermint")
		lvl, err := zapcore.ParseLevel(ParamsCoordinator.Tendermint.LogLevel)
		if err != nil {
			return nil, fmt.Errorf("invalid level: %w", err)
		}
		// this replays blocks until Tendermint and Coordinator are synced
		return tmnode.New(conf, NewTenderLogger(log, lvl), abciclient.NewLocalCreator(deps.Coordinator), gen)
	}); err != nil {
		return err
	}

	// provide the heaviest branch selection strategy
	if err := c.Provide(func() *mselection.HeaviestSelector {
		return mselection.New(
			ParamsCoordinator.TipSel.MinHeaviestBranchUnreferencedBlocksThreshold,
			ParamsCoordinator.TipSel.MaxHeaviestBranchTipsPerCheckpoint,
			ParamsCoordinator.TipSel.RandomTipsPerCheckpoint,
			ParamsCoordinator.TipSel.HeaviestBranchSelectionTimeout,
		)
	}); err != nil {
		return err
	}

	return nil
}

func processMilestone(milestone *iotago.Milestone) {
	latestMilestone.Lock()
	defer latestMilestone.Unlock()

	if milestone.Index < latestMilestone.Index+1 {
		return
	}

	latestMilestone.Index = milestone.Index
	latestMilestone.MilestoneMsgID = decoo.MilestoneMessageID(milestone)
	newMilestoneSignal <- latestMilestone.MilestoneInfo
}

func configure() error {
	trackMessages.Store(true)
	newMilestoneSignal = make(chan MilestoneInfo, 1)
	if bootstrap {
		// initialized the latest milestone with the provided dummy values
		latestMilestone.Index = startIndex - 1
		latestMilestone.MilestoneMsgID = startMilestoneMessageID
		// trigger issuing a milestone for that index
		newMilestoneSignal <- latestMilestone.MilestoneInfo
	}

	// pass all new solid messages to the selector and preemptively trigger new milestone when needed
	onMessageSolid = events.NewClosure(func(metadata *inx.BlockMetadata) {
		if !trackMessages.Load() || metadata.GetShouldReattach() {
			return
		}
		// add tips to the heaviest branch selector
		// if there are too many messages, trigger the latest milestone again. This will trigger a new milestone.
		if trackedMessagesCount := deps.Selector.OnNewSolidBlock(metadata); trackedMessagesCount >= ParamsCoordinator.MaxTrackedBlocks {
			// if the lock is already acquired, we are about to signal a new milestone anyway and can skip
			if latestMilestone.TryLock() {
				defer latestMilestone.Unlock()

				// if a new milestone has already been received, there is no need to preemptively trigger it
				select {
				case newMilestoneSignal <- latestMilestone.MilestoneInfo:
				default:
				}
			}
		}
	})

	// after a new milestone is confirmed, restart the selector and process the new milestone
	onConfirmedMilestoneChanged = events.NewClosure(func(milestone *nodebridge.Milestone) {
		// the selector needs to be reset after the milestone was confirmed
		deps.Selector.Reset()
		// make sure that new solid messages are tracked
		trackMessages.Store(true)
		// process the milestone for the coordinator
		processMilestone(milestone.Milestone)
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
		CoreComponent.LogInfo("Starting " + tendermintWorkerName + " ... done")

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

type MilestoneInfo struct {
	Index          uint32
	MilestoneMsgID iotago.BlockID
}

func coordinatorLoop(ctx context.Context) {
	// if we are not bootstrapping, we need to make sure that the latest stored milestone gets processed
	if !bootstrap {
		go func() {
			milestone, err := deps.NodeBridge.LatestMilestone()
			if err != nil {
				CoreComponent.LogWarnf("failed to read latest milestone: %s", err)
			} else {
				// processing will trigger newMilestoneSignal to start the loop
				processMilestone(milestone.Milestone)
			}
		}()
	}

	timer := time.NewTimer(math.MaxInt64)
	defer timer.Stop()
	var info MilestoneInfo
	for {
		select {
		case <-timer.C: // propose a parent for the milestone with index
			tips, err := deps.Selector.SelectTips(1)
			if err != nil {
				CoreComponent.LogWarnf("failed to select tips: %s", err)
				// use the previous milestone message as fallback
				tips = iotago.BlockIDs{info.MilestoneMsgID}
			}
			// until the actual milestone is received, the job of the tip selector is done
			trackMessages.Store(false)
			if err := deps.Coordinator.ProposeParent(info.Index+1, tips[0]); err != nil {
				CoreComponent.LogWarnf("failed to propose parent: %s", err)
			}

		case info = <-newMilestoneSignal: // we have received a new milestone without proposing a parent
			// the timer has not yet fired, so we need to stop it before resetting
			if !timer.Stop() {
				<-timer.C
			}
			// TODO: reset to MS.time + interval
			timer.Reset(ParamsCoordinator.Interval)
			continue

		case <-ctx.Done(): // end the loop
			return
		}

		// when this select is reach, the timer has fired and a milestone was proposed
		select {
		case info = <-newMilestoneSignal: // the new milestone is confirmed, we can now reset the timer
			timer.Reset(ParamsCoordinator.Interval) // the timer has already fired, so we can safely reset it

		case <-ctx.Done(): // end the loop
			return
		}
	}
}

func attachEvents() {
	deps.TangleListener.Events.BlockSolid.Attach(onMessageSolid)
	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Attach(onConfirmedMilestoneChanged)
}

func detachEvents() {
	deps.TangleListener.Events.BlockSolid.Detach(onMessageSolid)
	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Detach(onConfirmedMilestoneChanged)
}

// loadEd25519PrivateKeyFromEnvironment loads ed25519 private keys from the given environment variable.
func loadEd25519PrivateKeyFromEnvironment(name string) (ed25519.PrivateKey, error) {
	value, exists := os.LookupEnv(name)
	if !exists {
		return nil, fmt.Errorf("environment variable '%s' not set", name)
	}
	var seed types.Byte32
	if err := seed.Set(value); err != nil {
		return nil, fmt.Errorf("environment variable '%s' contains an invalid private key: %w", name, err)
	}
	return ed25519.NewKeyFromSeed(seed[:]), nil
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return !os.IsNotExist(err)
}
