package decoo

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/events"
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
	"github.com/tendermint/tendermint/config"
	tmservice "github.com/tendermint/tendermint/libs/service"
	tmnode "github.com/tendermint/tendermint/node"
	"github.com/tendermint/tendermint/privval"
	rpclocal "github.com/tendermint/tendermint/rpc/client/local"
	"go.uber.org/dig"
	"go.uber.org/zap/zapcore"
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

	// names of the background worker
	tangleListenerWorkerName = "TangleListener"
	tendermintWorkerName     = "Tendermint Node"
	decooWorkerName          = "Coordinator"
)

func init() {
	flag.BoolVar(&bootstrap, CfgCoordinatorBootstrap, false, "whether the network is bootstrapped")
	flag.Uint32Var(&startIndex, CfgCoordinatorStartIndex, 1, "index of the first milestone at bootstrap")
	flag.Var(types.NewByte32(iotago.EmptyBlockID(), (*[32]byte)(&startMilestoneID)), CfgCoordinatorStartMilestoneID, "the previous milestone ID at bootstrap")
	flag.Var(types.NewByte32(iotago.EmptyBlockID(), (*[32]byte)(&startMilestoneBlockID)), CfgCoordinatorStartMilestoneBlockID, "previous milestone block ID at bootstrap")

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

	// config flags
	bootstrap             bool
	startIndex            uint32
	startMilestoneID      iotago.MilestoneID
	startMilestoneBlockID iotago.BlockID

	confirmedMilestone struct {
		sync.Mutex
		milestoneInfo
	}
	newMilestoneSignal chan milestoneInfo

	// closures
	onBlockSolid                *events.Closure
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
		for name, validator := range Parameters.Tendermint.Validators {
			var pubKey types.Byte32
			if err := pubKey.Set(validator.PubKey); err != nil {
				return nil, fmt.Errorf("invalid pubKey for tendermint validator %s: %w", strconv.Quote(name), err)
			}
			members = append(members, pubKey[:])
		}

		committee := decoo.NewCommittee(deps.CoordinatorPrivateKey, members...)
		coo, err := decoo.New(committee, &INXClient{deps.NodeBridge}, deps.TangleListener, CoreComponent.Logger())
		if err != nil {
			return nil, fmt.Errorf("failed to create: %w", err)
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
		NodeBridge            *nodebridge.NodeBridge
	}
	if err := c.Provide(func(deps tendermintDeps) (tmservice.Service, error) {
		CoreComponent.LogInfo("Providing Tendermint ...")
		defer CoreComponent.LogInfo("Providing Tendermint ... done")

		conf, gen, err := loadTendermintConfig(deps.CoordinatorPrivateKey)
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
			return nil, fmt.Errorf("invalid level: %w", err)
		}
		// this replays blocks until Tendermint and Coordinator are synced
		return tmnode.New(conf, NewTenderLogger(log, lvl), abciclient.NewLocalCreator(deps.Coordinator), gen)
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
		if err := coordinator.Bootstrap(startIndex, startMilestoneID, startMilestoneBlockID); err != nil {
			return fmt.Errorf("bootstrap failed: %w", err)
		}
		return nil
	}

	pv, _ := privval.LoadFilePV(conf.PrivValidator.KeyFile(), conf.PrivValidator.StateFile())
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
		ms, err = nodeBridge.Milestone(state.MilestoneIndex - 1)
		if err != nil || ms == nil {
			return fmt.Errorf("milestone %d cannot be retrieved: %w", state.MilestoneIndex-1, err)
		}
	}

	if err := coordinator.InitState(ms.Milestone); err != nil {
		return fmt.Errorf("resume failed: %w", err)
	}
	return nil
}

func configure() error {
	newMilestoneSignal = make(chan milestoneInfo, 1)
	if bootstrap {
		// initialized the latest milestone with the provided dummy values
		confirmedMilestone.index = startIndex - 1
		confirmedMilestone.timestamp = time.Now()
		confirmedMilestone.milestoneBlockID = startMilestoneBlockID
		// trigger issuing a milestone for that index
		newMilestoneSignal <- confirmedMilestone.milestoneInfo
	}

	// pass all new solid blocks to the selector and preemptively trigger new milestone when needed
	onBlockSolid = events.NewClosure(func(metadata *inx.BlockMetadata) {
		if metadata.GetShouldReattach() {
			return
		}
		// add tips to the heaviest branch selector
		// if there are too many blocks, trigger the latest milestone again. This will trigger a new milestone.
		if trackedBlocksCount := deps.Selector.OnNewSolidBlock(metadata); trackedBlocksCount >= Parameters.MaxTrackedBlocks {
			CoreComponent.LogInfo("trigger next milestone preemptively")
			// if the lock is already acquired, we are about to signal a new milestone anyway and can skip
			if confirmedMilestone.TryLock() {
				defer confirmedMilestone.Unlock()

				// if a new milestone has already been received, there is no need to preemptively trigger it
				select {
				case newMilestoneSignal <- confirmedMilestone.milestoneInfo:
				default:
				}
			}
		}
	})

	// after a new milestone is confirmed, restart the selector and process the new milestone
	onConfirmedMilestoneChanged = events.NewClosure(func(milestone *nodebridge.Milestone) {
		CoreComponent.LogInfof("new confirmed milestone: %d", milestone.Milestone.Index)
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

type milestoneInfo struct {
	index            uint32
	timestamp        time.Time
	milestoneBlockID iotago.BlockID
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
	var info milestoneInfo
	for {
		select {
		case <-timer.C: // propose a parent for the next milestone
			CoreComponent.LogInfof("proposing parent for milestone %d", info.index+1)
			tips, err := deps.Selector.SelectTips(1)
			if err != nil {
				CoreComponent.LogWarnf("defaulting to last milestone as tip: %s", err)
				// use the previous milestone block as fallback
				tips = iotago.BlockIDs{info.milestoneBlockID}
			}
			// make sure that at least one second has passed since the last milestone
			if d := time.Until(info.timestamp.Add(time.Second)); d > 0 {
				time.Sleep(d)
			}
			// propose a random tip as parent for the next milestone
			if err := deps.Coordinator.ProposeParent(info.index+1, tips[rand.Intn(len(tips))]); err != nil {
				CoreComponent.LogWarnf("failed to propose parent: %s", err)
			}

		case info = <-newMilestoneSignal: // we have received a new milestone without proposing a parent
			// SelectTips also resets the tips; since this did not happen in this case, we manually reset the selector
			deps.Selector.Reset()
			// the timer has not yet fired, so we need to stop it before resetting
			if !timer.Stop() {
				<-timer.C
			}
			// reset the timer to match the interval since the lasts milestone
			timer.Reset(remainingInterval(info.timestamp))
			continue

		case <-ctx.Done(): // end the loop
			return
		}

		// when this select is reach, the timer has fired and a milestone was proposed
		select {
		case info = <-newMilestoneSignal: // the new milestone is confirmed, we can now reset the timer
			// reset the timer to match the interval since the lasts milestone
			timer.Reset(remainingInterval(info.timestamp))

		case <-ctx.Done(): // end the loop
			return
		}
	}
}

func attachEvents() {
	deps.TangleListener.Events.BlockSolid.Attach(onBlockSolid)
	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Attach(onConfirmedMilestoneChanged)
}

func detachEvents() {
	deps.TangleListener.Events.BlockSolid.Detach(onBlockSolid)
	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Detach(onConfirmedMilestoneChanged)
}

func processMilestone(milestone *iotago.Milestone) {
	confirmedMilestone.Lock()
	defer confirmedMilestone.Unlock()

	if milestone.Index < confirmedMilestone.index+1 {
		return
	}

	confirmedMilestone.index = milestone.Index
	confirmedMilestone.timestamp = time.Unix(int64(milestone.Timestamp), 0)
	confirmedMilestone.milestoneBlockID = decoo.MilestoneBlockID(milestone)
	newMilestoneSignal <- confirmedMilestone.milestoneInfo
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

func remainingInterval(ts time.Time) time.Duration {
	d := Parameters.Interval - time.Since(ts)
	if d < 0 {
		d = 0
	}
	return d
}
