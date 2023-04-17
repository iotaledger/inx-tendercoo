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
	"github.com/tendermint/tendermint/libs/service"
	rpclocal "github.com/tendermint/tendermint/rpc/client/local"
	"go.uber.org/dig"

	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/logger"
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

	tangleListenerWorkerName = "TangleListener"
	deCooWorkerName          = "Decentralized Coordinator"
)

func init() {
	flag.BoolVar(&bootstrap, CfgCoordinatorBootstrap, false, "whether the network is bootstrapped")
	flag.Uint32Var(&startIndex, CfgCoordinatorStartIndex, 1, "index of the first milestone at bootstrap")
	flag.Var(types.NewByte32(iotago.EmptyBlockID(), (*[32]byte)(&startMilestoneID)), CfgCoordinatorStartMilestoneID, "the previous milestone ID at bootstrap")
	flag.Var(types.NewByte32(iotago.EmptyBlockID(), (*[32]byte)(&startMilestoneBlockID)), CfgCoordinatorStartMilestoneBlockID, "previous milestone block ID at bootstrap")
	flag.BoolVar(&bootstrapForce, CfgCoordinatorBootstrapForce, false, "whether the network bootstrap is forced")

	Component = &app.Component{
		Name:      AppName,
		DepsFunc:  func(cDeps dependencies) { deps = cDeps },
		Params:    params,
		Provide:   provide,
		Configure: configure,
		Run:       run,
	}
}

var (
	Component *app.Component
	deps      dependencies

	log *logger.Logger // shortcut for CoreComponent.Logger

	// config flags.
	bootstrap             bool
	startIndex            uint32
	startMilestoneID      iotago.MilestoneID
	startMilestoneBlockID iotago.BlockID
	bootstrapForce        bool
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
	DeCoo              *decoo.Coordinator
	Coordinator        Coordinator
}

func provide(c *dig.Container) error {
	// initialize the logger
	log = Component.Logger()

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

	return nil
}

func configure() error {
	confirmedMilestoneSignal = make(chan milestoneInfo, 1)
	triggerNextMilestone = make(chan struct{}, 1)

	return nil
}

func run() error {
	// start the TangleListener for working Tangle events
	if err := Component.Daemon().BackgroundWorker(tangleListenerWorkerName, func(ctx context.Context) {
		log.Info("Starting " + tangleListenerWorkerName + " ... done")
		deps.TangleListener.Run(ctx)
		log.Info("Stopping " + tangleListenerWorkerName + " ... done")
	}, daemon.PriorityStopTangleListener); err != nil {
		log.Panicf("failed to start worker: %s", err)
	}

	// create and start Tendermint and run the coordinator loop to issue new milestones
	if err := Component.Daemon().BackgroundWorker(deCooWorkerName, func(ctx context.Context) {
		log.Info("Starting " + deCooWorkerName + " ...")

		conf, err := initTendermintConfig()
		if err != nil {
			log.Panicf("failed to initialize Tendermint config: %s", err)
		}

		// initialize the coordinator state matching the configured Tendermint state
		if err := initCoordinatorState(ctx, conf); err != nil {
			log.Panicf("failed to initialize coordinator: %s", err)
		}

		node, err := newTendermintNode(conf)
		if err != nil {
			log.Panicf("failed to create Tendermint node: %s", err)
		}

		// make sure the Tendermint node is stopped gracefully when the coordinator terminates unexpectedly
		go func() {
			deps.DeCoo.Wait()
			// if the daemon is stopped, the coordinator stop was expectedly
			if Component.Daemon().IsStopped() {
				return
			}
			// otherwise stop the Tendermint node and panic
			if err := node.Stop(); err != nil && !errors.Is(err, service.ErrAlreadyStopped) {
				log.Warnf("failed to stop Tendermint node: %s", err)
			}
			log.Panic("coordinator stopped unexpectedly")
		}()

		if err := initializeEvents(); err != nil {
			log.Panicf("failed to initialize: %s", err)
		}

		// for each solid block, add it to the selector and preemptively trigger the next milestone if needed
		// This assumes that solid block events are always triggered in the correct order, and that solid block events in
		// the past cone of a milestone are generally triggered before its milestone confirmation event.
		// Race conditions, i.e. solid blocks that come after their confirming milestone event, are included in the selector
		// even though they are no longer valid parents. In the very rare case, where this happens and the SelectTips is
		// also called before the milestone's solid block event has been processed, it could cause Coordinator.ProposeParent
		// to propose such an invalid parent, fail and be retried.
		onBlockSolid := func(metadata *inx.BlockMetadata) {
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
				log.Info("Trigger next milestone preemptively")
				select {
				case triggerNextMilestone <- struct{}{}:
				default:
					// if the channel is full, there is already one unprocessed trigger, and we don't need to signal again
				}
			}
		}

		// for each confirmed milestone, reset the selector and signal its arrival for the coordinator loop
		onConfirmedMilestoneChanged := func(milestone *nodebridge.Milestone) {
			// reset the tip selection after each new milestone to only add unconfirmed blocks to the selector
			deps.Selector.Reset()

			// ignore new confirmed milestones during syncing
			if lmi := deps.NodeBridge.LatestMilestoneIndex(); lmi > milestone.Milestone.Index {
				log.Debugf("node is not synced; latest=%d confirmed=%d", lmi, milestone.Milestone.Index)

				return
			}
			log.Infof("New confirmed milestone: %d", milestone.Milestone.Index)
			processConfirmedMilestone(milestone.Milestone)
		}

		// register events
		unhook := lo.Batch(
			deps.TangleListener.Events.BlockSolid.Hook(onBlockSolid).Unhook,
			deps.NodeBridge.Events.ConfirmedMilestoneChanged.Hook(onConfirmedMilestoneChanged).Unhook,
		)

		defer unhook()

		if err := deps.DeCoo.Start(rpclocal.New(node)); err != nil {
			log.Panicf("failed to start coordinator: %s", err)
		}

		if err := node.Start(); err != nil {
			log.Panicf("failed to start Tendermint node: %s", err)
		}

		addr, _ := node.NodeInfo().NetAddress()
		pubKey, _ := node.PrivValidator().GetPubKey()
		log.Infof("Started "+deCooWorkerName+": Address=%s, ConsensusPublicKey=%s",
			addr, types.Byte32FromSlice(pubKey.Bytes()))

		// issue milestones until the context is canceled
		coordinatorLoop(ctx)

		log.Info("Stopping " + deCooWorkerName + " ...")

		if err := node.Stop(); err != nil {
			log.Warnf("failed to stop Tendermint node: %s", err)
		}
		if err := deps.Coordinator.Stop(); err != nil {
			log.Warnf("failed to stop Coordinator: %s", err)
		}

		log.Info("Stopping " + deCooWorkerName + " ... done")
	}, daemon.PriorityStopDeCoo); err != nil {
		log.Panicf("failed to start worker: %s", err)
	}

	return nil
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
