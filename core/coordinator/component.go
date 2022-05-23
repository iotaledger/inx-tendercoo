package coordinator

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"
	flag "github.com/spf13/pflag"
	"go.uber.org/dig"

	"github.com/gohornet/hornet/pkg/common"
	"github.com/gohornet/hornet/pkg/keymanager"
	"github.com/gohornet/hornet/pkg/model/milestone"
	"github.com/gohornet/inx-app/nodebridge"
	"github.com/gohornet/inx-coordinator/pkg/coordinator"
	"github.com/gohornet/inx-coordinator/pkg/daemon"
	"github.com/gohornet/inx-coordinator/pkg/migrator"
	"github.com/gohornet/inx-coordinator/pkg/mselection"
	"github.com/gohornet/inx-coordinator/pkg/todo"
	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/app/core/shutdown"
	"github.com/iotaledger/hive.go/crypto"
	"github.com/iotaledger/hive.go/events"
	"github.com/iotaledger/hive.go/syncutils"
	"github.com/iotaledger/hive.go/timeutil"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

const (
	// CfgCoordinatorBootstrap defines whether to bootstrap the network
	CfgCoordinatorBootstrap = "cooBootstrap"
	// CfgCoordinatorStartIndex defines the index of the first milestone at bootstrap
	CfgCoordinatorStartIndex = "cooStartIndex"
	// MilestoneMaxAdditionalTipsLimit defines the maximum limit of additional tips that fit into a milestone (besides the last milestone and checkpoint hash)
	MilestoneMaxAdditionalTipsLimit = 6
)

var (
	ErrDatabaseTainted = errors.New("database is tainted. delete the coordinator database and start again with a snapshot")
)

func init() {
	CoreComponent = &app.CoreComponent{
		Component: &app.Component{
			Name:      "Coordinator",
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

	bootstrap  = flag.Bool(CfgCoordinatorBootstrap, false, "bootstrap the network")
	startIndex = flag.Uint32(CfgCoordinatorStartIndex, 0, "index of the first milestone at bootstrap")

	nextCheckpointSignal chan struct{}
	nextMilestoneSignal  chan struct{}

	heaviestSelectorLock syncutils.RWMutex

	lastCheckpointIndex   int
	lastCheckpointBlockID iotago.BlockID
	lastMilestoneBlockID  iotago.BlockID

	// closures
	onBlockSolid                *events.Closure
	onConfirmedMilestoneChanged *events.Closure
	onIssuedCheckpoint          *events.Closure
	onIssuedMilestone           *events.Closure
)

type dependencies struct {
	dig.In
	Coordinator      *coordinator.Coordinator
	Selector         *mselection.HeaviestSelector
	NodeBridge       *nodebridge.NodeBridge
	ShutdownHandler  *shutdown.ShutdownHandler
	TangleListener   *nodebridge.TangleListener
	TreasuryListener *TreasuryListener `optional:"true"`
}

func provide(c *dig.Container) error {

	if err := c.Provide(func() *mselection.HeaviestSelector {
		// use the heaviest branch tip selection for the milestones
		return mselection.New(
			ParamsCoordinator.TipSel.MinHeaviestBranchUnreferencedBlocksThreshold,
			ParamsCoordinator.TipSel.MaxHeaviestBranchTipsPerCheckpoint,
			ParamsCoordinator.TipSel.RandomTipsPerCheckpoint,
			ParamsCoordinator.TipSel.HeaviestBranchSelectionTimeout,
		)
	}); err != nil {
		return err
	}

	type coordinatorDeps struct {
		dig.In
		MigratorService *migrator.MigratorService `optional:"true"`
		NodeBridge      *nodebridge.NodeBridge
	}

	type coordinatorDepsOut struct {
		dig.Out
		Coordinator      *coordinator.Coordinator
		TangleListener   *nodebridge.TangleListener
		TreasuryListener *TreasuryListener `optional:"true"`
	}

	if err := c.Provide(func(deps coordinatorDeps) coordinatorDepsOut {

		var treasuryListener *TreasuryListener
		if deps.MigratorService != nil {
			treasuryListener = NewTreasuryListener(CoreComponent.Logger(), deps.NodeBridge)
		}

		initCoordinator := func() (*coordinator.Coordinator, error) {

			keyManager := keymanager.New()
			for _, keyRange := range deps.NodeBridge.NodeConfig.GetMilestoneKeyRanges() {
				keyManager.AddKeyRange(keyRange.GetPublicKey(), milestone.Index(keyRange.GetStartIndex()), milestone.Index(keyRange.GetEndIndex()))
			}

			signingProvider, err := initSigningProvider(
				ParamsCoordinator.Signing.Provider,
				ParamsCoordinator.Signing.RemoteAddress,
				keyManager,
				int(deps.NodeBridge.NodeConfig.GetMilestonePublicKeyCount()),
			)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize signing provider: %s", err)
			}

			if ParamsCoordinator.Quorum.Enabled {
				CoreComponent.LogInfo("running coordinator with quorum enabled")
			}

			if deps.MigratorService == nil {
				CoreComponent.LogInfo("running coordinator without migration enabled")
			}

			coo, err := coordinator.New(
				ComputeMerkleTreeHash,
				deps.NodeBridge.IsNodeSynced,
				deps.NodeBridge.ProtocolParameters(),
				signingProvider,
				deps.MigratorService,
				treasuryListener.LatestTreasuryOutput,
				sendBlock,
				coordinator.WithLogger(CoreComponent.Logger()),
				coordinator.WithStateFilePath(ParamsCoordinator.StateFilePath),
				coordinator.WithMilestoneInterval(ParamsCoordinator.Interval),
				coordinator.WithQuorum(ParamsCoordinator.Quorum.Enabled, ParamsCoordinator.Quorum.Groups, ParamsCoordinator.Quorum.Timeout),
				coordinator.WithSigningRetryAmount(ParamsCoordinator.Signing.RetryAmount),
				coordinator.WithSigningRetryTimeout(ParamsCoordinator.Signing.RetryTimeout),
			)
			if err != nil {
				return nil, err
			}

			latestMilestone := &coordinator.LatestMilestoneInfo{
				Index:       0,
				Timestamp:   0,
				MilestoneID: iotago.MilestoneID{},
			}

			ms, err := deps.NodeBridge.LatestMilestone()
			if err != nil {
				return nil, err
			}
			if ms != nil {
				latestMilestone = &coordinator.LatestMilestoneInfo{
					Index:       ms.Milestone.Index,
					Timestamp:   ms.Milestone.Timestamp,
					MilestoneID: ms.MilestoneID,
				}
			}
			if err := coo.InitState(*bootstrap, *startIndex, latestMilestone); err != nil {
				return nil, err
			}

			// don't issue milestones or checkpoints in case the node is running hot
			coo.AddBackPressureFunc(todo.IsNodeTooLoaded)

			return coo, nil
		}

		coo, err := initCoordinator()
		if err != nil {
			CoreComponent.LogPanic(err)
		}
		return coordinatorDepsOut{
			Coordinator:      coo,
			TangleListener:   nodebridge.NewTangleListener(deps.NodeBridge),
			TreasuryListener: treasuryListener,
		}
	}); err != nil {
		return err
	}
	return nil
}

func configure() error {

	databasesTainted, err := todo.AreDatabasesTainted()
	if err != nil {
		CoreComponent.LogPanic(err)
	}

	if databasesTainted {
		CoreComponent.LogPanic(ErrDatabaseTainted)
	}

	nextCheckpointSignal = make(chan struct{})

	// must be a buffered channel, otherwise signal gets
	// lost if checkpoint is generated at the same time
	nextMilestoneSignal = make(chan struct{}, 1)

	configureEvents()
	return nil
}

// handleError checks for critical errors and returns true if the node should shutdown.
func handleError(err error) bool {
	if err == nil {
		return false
	}

	if err := common.IsCriticalError(err); err != nil {
		deps.ShutdownHandler.SelfShutdown(fmt.Sprintf("coordinator plugin hit a critical error: %s", err), true)
		return true
	}

	if err := common.IsSoftError(err); err != nil {
		CoreComponent.LogWarn(err)
		deps.Coordinator.Events.SoftError.Trigger(err)
		return false
	}

	// this should not happen! errors should be defined as a soft or critical error explicitly
	CoreComponent.LogPanicf("coordinator plugin hit an unknown error type: %s", err)
	return true
}

func run() error {

	// create a background worker that signals to issue new milestones
	if err := CoreComponent.Daemon().BackgroundWorker("Coordinator[MilestoneTicker]", func(ctx context.Context) {
		CoreComponent.LogInfo("Start MilestoneTicker")
		ticker := timeutil.NewTicker(func() {
			// issue next milestone
			select {
			case nextMilestoneSignal <- struct{}{}:
			default:
				// do not block if already another signal is waiting
			}
		}, deps.Coordinator.Interval(), ctx)
		ticker.WaitForGracefulShutdown()
	}, daemon.PriorityStopCoordinatorMilestoneTicker); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

	if err := CoreComponent.Daemon().BackgroundWorker("Coordinator[TangleListener]", func(ctx context.Context) {
		CoreComponent.LogInfo("Start TangleListener")
		deps.TangleListener.Run(ctx)
		CoreComponent.LogInfo("Stopped TangleListener")
	}, daemon.PriorityStopTangleListener); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

	if deps.TreasuryListener != nil {
		if err := CoreComponent.Daemon().BackgroundWorker("Coordinator[TreasuryListener]", func(ctx context.Context) {
			CoreComponent.LogInfo("Start TreasuryListener")
			deps.TreasuryListener.Run(ctx)
			CoreComponent.LogInfo("Stopped TreasuryListener")
		}, daemon.PriorityStopTreasuryListener); err != nil {
			CoreComponent.LogPanicf("failed to start worker: %s", err)
		}
	}

	// create a background worker that issues milestones
	if err := CoreComponent.Daemon().BackgroundWorker("Coordinator", func(ctx context.Context) {
		attachEvents()

		// bootstrap the network if not done yet
		milestoneBlockID, err := deps.Coordinator.Bootstrap()
		if handleError(err) {
			// critical error => stop worker
			detachEvents()
			return
		}

		// init the last milestone block ID
		lastMilestoneBlockID = milestoneBlockID

		// init the checkpoints
		lastCheckpointBlockID = milestoneBlockID
		lastCheckpointIndex = 0

	coordinatorLoop:
		for {
			select {
			case <-nextCheckpointSignal:
				// check the thresholds again, because a new milestone could have been issued in the meantime
				if trackedBlocksCount := deps.Selector.TrackedBlocksCount(); trackedBlocksCount < ParamsCoordinator.Checkpoints.MaxTrackedBlocks {
					continue
				}

				func() {
					// this lock is necessary, otherwise a checkpoint could be issued
					// while a milestone gets confirmed. In that case the checkpoint could
					// contain blocks that are already below max depth.
					heaviestSelectorLock.RLock()
					defer heaviestSelectorLock.RUnlock()

					tips, err := deps.Selector.SelectTips(0)
					if err != nil {
						// issuing checkpoint failed => not critical
						if !errors.Is(err, mselection.ErrNoTipsAvailable) {
							CoreComponent.LogWarn(err)
						}
						return
					}

					// issue a checkpoint
					checkpointBlockID, err := deps.Coordinator.IssueCheckpoint(lastCheckpointIndex, lastCheckpointBlockID, tips)
					if err != nil {
						// issuing checkpoint failed => not critical
						CoreComponent.LogWarn(err)
						return
					}
					lastCheckpointIndex++
					lastCheckpointBlockID = checkpointBlockID
				}()

			case <-nextMilestoneSignal:
				var milestoneTips iotago.BlockIDs

				// issue a new checkpoint right in front of the milestone
				checkpointTips, err := deps.Selector.SelectTips(1)
				if err != nil {
					// issuing checkpoint failed => not critical
					if !errors.Is(err, mselection.ErrNoTipsAvailable) {
						CoreComponent.LogWarn(err)
					}
				} else {
					if len(checkpointTips) > MilestoneMaxAdditionalTipsLimit {
						// issue a checkpoint with all the tips that wouldn't fit into the milestone (more than MilestoneMaxAdditionalTipsLimit)
						checkpointBlockID, err := deps.Coordinator.IssueCheckpoint(lastCheckpointIndex, lastCheckpointBlockID, checkpointTips[MilestoneMaxAdditionalTipsLimit:])
						if err != nil {
							// issuing checkpoint failed => not critical
							CoreComponent.LogWarn(err)
						} else {
							// use the new checkpoint block ID
							lastCheckpointBlockID = checkpointBlockID
						}

						// use the other tips for the milestone
						milestoneTips = checkpointTips[:MilestoneMaxAdditionalTipsLimit]
					} else {
						// do not issue a checkpoint and use the tips for the milestone instead since they fit into the milestone directly
						milestoneTips = checkpointTips
					}
				}

				milestoneTips = append(milestoneTips, iotago.BlockIDs{lastMilestoneBlockID, lastCheckpointBlockID}...)

				milestoneBlockID, err := deps.Coordinator.IssueMilestone(milestoneTips)
				if handleError(err) {
					// critical error => quit loop
					break coordinatorLoop
				}
				if err != nil {
					// non-critical errors
					if errors.Is(err, common.ErrNodeNotSynced) {
						// Coordinator is not synchronized, trigger the solidifier manually
						todo.TriggerSolidifier()
					}

					// reset the checkpoints
					lastCheckpointBlockID = lastMilestoneBlockID
					lastCheckpointIndex = 0

					continue
				}

				// remember the last milestone block ID
				lastMilestoneBlockID = milestoneBlockID

				// reset the checkpoints
				lastCheckpointBlockID = milestoneBlockID
				lastCheckpointIndex = 0

			case <-ctx.Done():
				break coordinatorLoop
			}
		}

		detachEvents()
	}, daemon.PriorityStopCoordinator); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}
	return nil
}

// loadEd25519PrivateKeysFromEnvironment loads ed25519 private keys from the given environment variable.
func loadEd25519PrivateKeysFromEnvironment(name string) ([]ed25519.PrivateKey, error) {

	keys, exists := os.LookupEnv(name)
	if !exists {
		return nil, fmt.Errorf("environment variable '%s' not set", name)
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("environment variable '%s' not set", name)
	}

	var privateKeys []ed25519.PrivateKey
	for _, key := range strings.Split(keys, ",") {
		privateKey, err := crypto.ParseEd25519PrivateKeyFromString(key)
		if err != nil {
			return nil, fmt.Errorf("environment variable '%s' contains an invalid private key '%s'", name, key)

		}
		privateKeys = append(privateKeys, privateKey)
	}

	return privateKeys, nil
}

func initSigningProvider(signingProviderType string, remoteEndpoint string, keyManager *keymanager.KeyManager, milestonePublicKeyCount int) (coordinator.MilestoneSignerProvider, error) {

	switch signingProviderType {
	case "local":
		privateKeys, err := loadEd25519PrivateKeysFromEnvironment("COO_PRV_KEYS")
		if err != nil {
			return nil, err
		}

		if len(privateKeys) == 0 {
			return nil, errors.New("no private keys given")
		}

		for _, privateKey := range privateKeys {
			if len(privateKey) != ed25519.PrivateKeySize {
				return nil, errors.New("wrong private key length")
			}
		}

		return coordinator.NewInMemoryEd25519MilestoneSignerProvider(privateKeys, keyManager, milestonePublicKeyCount), nil

	case "remote":
		if remoteEndpoint == "" {
			return nil, errors.New("no address given for remote signing provider")
		}

		return coordinator.NewInsecureRemoteEd25519MilestoneSignerProvider(remoteEndpoint, keyManager, milestonePublicKeyCount), nil

	default:
		return nil, fmt.Errorf("unknown milestone signing provider: %s", signingProviderType)
	}
}

func sendBlock(block *iotago.Block, msIndex ...uint32) (iotago.BlockID, error) {

	var err error

	var milestoneConfirmedEventChan chan struct{}

	if len(msIndex) > 0 {
		milestoneConfirmedEventChan = deps.TangleListener.RegisterMilestoneConfirmedEvent(msIndex[0])
		defer func() {
			if err != nil {
				deps.TangleListener.DeregisterMilestoneConfirmedEvent(msIndex[0])
			}
		}()
	}

	var blockID iotago.BlockID
	blockID, err = deps.NodeBridge.SubmitBlock(CoreComponent.Daemon().ContextStopped(), block)
	if err != nil {
		return iotago.EmptyBlockID(), err
	}

	blockSolidEventChan := deps.TangleListener.RegisterBlockSolidEvent(blockID)

	defer func() {
		if err != nil {
			deps.TangleListener.DeregisterBlockSolidEvent(blockID)
		}
	}()

	// wait until the block is solid
	if err = events.WaitForChannelClosed(context.Background(), blockSolidEventChan); err != nil {
		return iotago.EmptyBlockID(), err
	}

	if len(msIndex) > 0 {
		// if it was a milestone, also wait until the milestone was confirmed
		if err = events.WaitForChannelClosed(context.Background(), milestoneConfirmedEventChan); err != nil {
			return iotago.EmptyBlockID(), err
		}
	}

	return blockID, nil
}

func configureEvents() {
	// pass all new solid blocks to the selector
	onBlockSolid = events.NewClosure(func(metadata *inx.BlockMetadata) {

		if metadata.GetShouldReattach() {
			// ignore tips that are below max depth
			return
		}

		// add tips to the heaviest branch selector
		if trackedBlocksCount := deps.Selector.OnNewSolidBlock(metadata); trackedBlocksCount >= ParamsCoordinator.Checkpoints.MaxTrackedBlocks {
			CoreComponent.LogDebugf("Coordinator Tipselector: trackedBlocksCount: %d", trackedBlocksCount)

			// issue next checkpoint
			select {
			case nextCheckpointSignal <- struct{}{}:
			default:
				// do not block if already another signal is waiting
			}
		}
	})

	onConfirmedMilestoneChanged = events.NewClosure(func(_ *nodebridge.Milestone) {
		heaviestSelectorLock.Lock()
		defer heaviestSelectorLock.Unlock()

		// the selector needs to be reset after the milestone was confirmed, otherwise
		// it could contain tips that are already below max depth.
		deps.Selector.Reset()

		// the checkpoint also needs to be reset, otherwise
		// a checkpoint could have been issued in the meantime,
		// which could contain blocks that are already below max depth.
		lastCheckpointBlockID = lastMilestoneBlockID
		lastCheckpointIndex = 0
	})

	onIssuedCheckpoint = events.NewClosure(func(checkpointIndex int, tipIndex int, tipsTotal int, blockID iotago.BlockID) {
		CoreComponent.LogInfof("checkpoint (%d) block issued (%d/%d): %v", checkpointIndex+1, tipIndex+1, tipsTotal, blockID.ToHex())
	})

	onIssuedMilestone = events.NewClosure(func(index uint32, milestoneID iotago.MilestoneID, blockID iotago.BlockID) {
		CoreComponent.LogInfof("milestone issued (%d) MilestoneID: %s, BlockID: %v", index, iotago.EncodeHex(milestoneID[:]), blockID.ToHex())
	})
}

func attachEvents() {
	deps.TangleListener.Events.BlockSolid.Attach(onBlockSolid)
	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Attach(onConfirmedMilestoneChanged)
	deps.Coordinator.Events.IssuedCheckpointBlock.Attach(onIssuedCheckpoint)
	deps.Coordinator.Events.IssuedMilestone.Attach(onIssuedMilestone)
}

func detachEvents() {
	deps.TangleListener.Events.BlockSolid.Detach(onBlockSolid)
	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Detach(onConfirmedMilestoneChanged)
	deps.Coordinator.Events.IssuedCheckpointBlock.Detach(onIssuedCheckpoint)
	deps.Coordinator.Events.IssuedMilestone.Detach(onIssuedMilestone)
}
