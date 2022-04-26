package coordinator

import (
	"crypto/ed25519"
	"fmt"

	"github.com/pkg/errors"
	flag "github.com/spf13/pflag"
	"go.uber.org/dig"
	"golang.org/x/net/context"

	"github.com/gohornet/hornet/pkg/common"
	"github.com/gohornet/hornet/pkg/keymanager"
	"github.com/gohornet/hornet/pkg/model/hornet"
	"github.com/gohornet/hornet/pkg/model/milestone"
	"github.com/gohornet/hornet/pkg/node"
	"github.com/gohornet/hornet/pkg/shutdown"
	"github.com/gohornet/hornet/pkg/utils"
	"github.com/gohornet/inx-coordinator/pkg/coordinator"
	"github.com/gohornet/inx-coordinator/pkg/daemon"
	"github.com/gohornet/inx-coordinator/pkg/migrator"
	"github.com/gohornet/inx-coordinator/pkg/mselection"
	"github.com/gohornet/inx-coordinator/pkg/nodebridge"
	"github.com/gohornet/inx-coordinator/pkg/todo"
	"github.com/iotaledger/hive.go/configuration"
	"github.com/iotaledger/hive.go/events"
	"github.com/iotaledger/hive.go/syncutils"
	"github.com/iotaledger/hive.go/timeutil"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

const (
	// whether to bootstrap the network
	CfgCoordinatorBootstrap = "cooBootstrap"
	// the index of the first milestone at bootstrap
	CfgCoordinatorStartIndex = "cooStartIndex"
	// the maximum limit of additional tips that fit into a milestone (besides the last milestone and checkpoint hash)
	MilestoneMaxAdditionalTipsLimit = 6
)

var (
	ErrDatabaseTainted = errors.New("database is tainted. delete the coordinator database and start again with a snapshot")
)

func init() {
	_ = flag.CommandLine.MarkHidden(CfgCoordinatorBootstrap)
	_ = flag.CommandLine.MarkHidden(CfgCoordinatorStartIndex)

	CorePlugin = &node.CorePlugin{
		Pluggable: node.Pluggable{
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
	CorePlugin *node.CorePlugin
	deps       dependencies

	bootstrap  = flag.Bool(CfgCoordinatorBootstrap, false, "bootstrap the network")
	startIndex = flag.Uint32(CfgCoordinatorStartIndex, 0, "index of the first milestone at bootstrap")

	maxTrackedMessages int

	nextCheckpointSignal chan struct{}
	nextMilestoneSignal  chan struct{}

	heaviestSelectorLock syncutils.RWMutex

	lastCheckpointIndex     int
	lastCheckpointMessageID hornet.MessageID
	lastMilestoneMessageID  hornet.MessageID

	// closures
	onMessageSolid              *events.Closure
	onConfirmedMilestoneChanged *events.Closure
	onIssuedCheckpoint          *events.Closure
	onIssuedMilestone           *events.Closure
)

type dependencies struct {
	dig.In
	AppConfig       *configuration.Configuration `name:"appConfig"`
	Coordinator     *coordinator.Coordinator
	Selector        *mselection.HeaviestSelector
	NodeBridge      *nodebridge.NodeBridge
	ShutdownHandler *shutdown.ShutdownHandler
}

func provide(c *dig.Container) {

	type selectorDeps struct {
		dig.In
		AppConfig *configuration.Configuration `name:"appConfig"`
	}

	if err := c.Provide(func(deps selectorDeps) *mselection.HeaviestSelector {
		// use the heaviest branch tip selection for the milestones
		return mselection.New(
			deps.AppConfig.Int(CfgCoordinatorTipselectMinHeaviestBranchUnreferencedMessagesThreshold),
			deps.AppConfig.Int(CfgCoordinatorTipselectMaxHeaviestBranchTipsPerCheckpoint),
			deps.AppConfig.Int(CfgCoordinatorTipselectRandomTipsPerCheckpoint),
			deps.AppConfig.Duration(CfgCoordinatorTipselectHeaviestBranchSelectionTimeout),
		)
	}); err != nil {
		CorePlugin.LogPanic(err)
	}

	type coordinatorDeps struct {
		dig.In
		MigratorService *migrator.MigratorService    `optional:"true"`
		AppConfig       *configuration.Configuration `name:"appConfig"`
		NodeBridge      *nodebridge.NodeBridge
	}

	if err := c.Provide(func(deps coordinatorDeps) *coordinator.Coordinator {

		initCoordinator := func() (*coordinator.Coordinator, error) {

			signingProvider, err := initSigningProvider(
				deps.AppConfig.String(CfgCoordinatorSigningProvider),
				deps.AppConfig.String(CfgCoordinatorSigningRemoteAddress),
				deps.NodeBridge.KeyManager(),
				deps.NodeBridge.MilestonePublicKeyCount(),
			)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize signing provider: %s", err)
			}

			quorumGroups, err := initQuorumGroups(deps.AppConfig)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize coordinator quorum: %s", err)
			}

			if deps.AppConfig.Bool(CfgCoordinatorQuorumEnabled) {
				CorePlugin.LogInfo("running coordinator with quorum enabled")
			}

			if deps.MigratorService == nil {
				CorePlugin.LogInfo("running coordinator without migration enabled")
			}

			coo, err := coordinator.New(
				deps.NodeBridge.ComputeMerkleTreeHash,
				deps.NodeBridge.IsNodeSynced,
				iotago.NetworkIDFromString(deps.NodeBridge.ProtocolParameters.GetNetworkName()),
				deps.NodeBridge.DeserializationParameters(),
				signingProvider,
				deps.MigratorService,
				deps.NodeBridge.LatestTreasuryOutput,
				sendMessage,
				coordinator.WithLogger(CorePlugin.Logger()),
				coordinator.WithStateFilePath(deps.AppConfig.String(CfgCoordinatorStateFilePath)),
				coordinator.WithMilestoneInterval(deps.AppConfig.Duration(CfgCoordinatorInterval)),
				coordinator.WithQuorum(deps.AppConfig.Bool(CfgCoordinatorQuorumEnabled), quorumGroups, deps.AppConfig.Duration(CfgCoordinatorQuorumTimeout)),
				coordinator.WithSigningRetryAmount(deps.AppConfig.Int(CfgCoordinatorSigningRetryAmount)),
				coordinator.WithSigningRetryTimeout(deps.AppConfig.Duration(CfgCoordinatorSigningRetryTimeout)),
			)
			if err != nil {
				return nil, err
			}

			if err := coo.InitState(*bootstrap, milestone.Index(*startIndex), deps.NodeBridge.LatestMilestone()); err != nil {
				return nil, err
			}

			// don't issue milestones or checkpoints in case the node is running hot
			coo.AddBackPressureFunc(todo.IsNodeTooLoaded)

			return coo, nil
		}

		coo, err := initCoordinator()
		if err != nil {
			CorePlugin.LogPanic(err)
		}
		return coo
	}); err != nil {
		CorePlugin.LogPanic(err)
	}
}

func configure() {

	databasesTainted, err := todo.AreDatabasesTainted()
	if err != nil {
		CorePlugin.LogPanic(err)
	}

	if databasesTainted {
		CorePlugin.LogPanic(ErrDatabaseTainted)
	}

	nextCheckpointSignal = make(chan struct{})

	// must be a buffered channel, otherwise signal gets
	// lost if checkpoint is generated at the same time
	nextMilestoneSignal = make(chan struct{}, 1)

	maxTrackedMessages = deps.AppConfig.Int(CfgCoordinatorCheckpointsMaxTrackedMessages)

	configureEvents()
}

// handleError checks for critical errors and returns true if the node should shutdown.
func handleError(err error) bool {
	if err == nil {
		return false
	}

	if err := common.IsCriticalError(err); err != nil {
		deps.ShutdownHandler.SelfShutdown(fmt.Sprintf("coordinator plugin hit a critical error: %s", err))
		return true
	}

	if err := common.IsSoftError(err); err != nil {
		CorePlugin.LogWarn(err)
		deps.Coordinator.Events.SoftError.Trigger(err)
		return false
	}

	// this should not happen! errors should be defined as a soft or critical error explicitly
	CorePlugin.LogPanicf("coordinator plugin hit an unknown error type: %s", err)
	return true
}

func run() {

	// create a background worker that signals to issue new milestones
	if err := CorePlugin.Daemon().BackgroundWorker("Coordinator[MilestoneTicker]", func(ctx context.Context) {
		CorePlugin.LogInfo("Start MilestoneTicker")
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
		CorePlugin.LogPanicf("failed to start worker: %s", err)
	}

	// create a background worker that issues milestones
	if err := CorePlugin.Daemon().BackgroundWorker("Coordinator", func(ctx context.Context) {
		attachEvents()

		// bootstrap the network if not done yet
		milestoneMessageID, err := deps.Coordinator.Bootstrap()
		if handleError(err) {
			// critical error => stop worker
			detachEvents()
			return
		}

		// init the last milestone message ID
		lastMilestoneMessageID = milestoneMessageID

		// init the checkpoints
		lastCheckpointMessageID = milestoneMessageID
		lastCheckpointIndex = 0

	coordinatorLoop:
		for {
			select {
			case <-nextCheckpointSignal:
				// check the thresholds again, because a new milestone could have been issued in the meantime
				if trackedMessagesCount := deps.Selector.TrackedMessagesCount(); trackedMessagesCount < maxTrackedMessages {
					continue
				}

				func() {
					// this lock is necessary, otherwise a checkpoint could be issued
					// while a milestone gets confirmed. In that case the checkpoint could
					// contain messages that are already below max depth.
					heaviestSelectorLock.RLock()
					defer heaviestSelectorLock.RUnlock()

					tips, err := deps.Selector.SelectTips(0)
					if err != nil {
						// issuing checkpoint failed => not critical
						if !errors.Is(err, mselection.ErrNoTipsAvailable) {
							CorePlugin.LogWarn(err)
						}
						return
					}

					// issue a checkpoint
					checkpointMessageID, err := deps.Coordinator.IssueCheckpoint(lastCheckpointIndex, lastCheckpointMessageID, tips)
					if err != nil {
						// issuing checkpoint failed => not critical
						CorePlugin.LogWarn(err)
						return
					}
					lastCheckpointIndex++
					lastCheckpointMessageID = checkpointMessageID
				}()

			case <-nextMilestoneSignal:
				var milestoneTips hornet.MessageIDs

				// issue a new checkpoint right in front of the milestone
				checkpointTips, err := deps.Selector.SelectTips(1)
				if err != nil {
					// issuing checkpoint failed => not critical
					if !errors.Is(err, mselection.ErrNoTipsAvailable) {
						CorePlugin.LogWarn(err)
					}
				} else {
					if len(checkpointTips) > MilestoneMaxAdditionalTipsLimit {
						// issue a checkpoint with all the tips that wouldn't fit into the milestone (more than MilestoneMaxAdditionalTipsLimit)
						checkpointMessageID, err := deps.Coordinator.IssueCheckpoint(lastCheckpointIndex, lastCheckpointMessageID, checkpointTips[MilestoneMaxAdditionalTipsLimit:])
						if err != nil {
							// issuing checkpoint failed => not critical
							CorePlugin.LogWarn(err)
						} else {
							// use the new checkpoint message ID
							lastCheckpointMessageID = checkpointMessageID
						}

						// use the other tips for the milestone
						milestoneTips = checkpointTips[:MilestoneMaxAdditionalTipsLimit]
					} else {
						// do not issue a checkpoint and use the tips for the milestone instead since they fit into the milestone directly
						milestoneTips = checkpointTips
					}
				}

				milestoneTips = append(milestoneTips, hornet.MessageIDs{lastMilestoneMessageID, lastCheckpointMessageID}...)

				milestoneMessageID, err := deps.Coordinator.IssueMilestone(milestoneTips)
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
					lastCheckpointMessageID = lastMilestoneMessageID
					lastCheckpointIndex = 0

					continue
				}

				// remember the last milestone message ID
				lastMilestoneMessageID = milestoneMessageID

				// reset the checkpoints
				lastCheckpointMessageID = milestoneMessageID
				lastCheckpointIndex = 0

			case <-ctx.Done():
				break coordinatorLoop
			}
		}

		detachEvents()
	}, daemon.PriorityStopCoordinator); err != nil {
		CorePlugin.LogPanicf("failed to start worker: %s", err)
	}

}

func initSigningProvider(signingProviderType string, remoteEndpoint string, keyManager *keymanager.KeyManager, milestonePublicKeyCount int) (coordinator.MilestoneSignerProvider, error) {

	switch signingProviderType {
	case "local":
		privateKeys, err := utils.LoadEd25519PrivateKeysFromEnvironment("COO_PRV_KEYS")
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

func initQuorumGroups(appConfig *configuration.Configuration) (map[string][]*coordinator.QuorumClientConfig, error) {
	// parse quorum groups config
	quorumGroups := make(map[string][]*coordinator.QuorumClientConfig)
	for _, groupName := range appConfig.MapKeys(CfgCoordinatorQuorumGroups) {
		configKey := CfgCoordinatorQuorumGroups + "." + groupName

		groupConfig := []*coordinator.QuorumClientConfig{}
		if err := appConfig.Unmarshal(configKey, &groupConfig); err != nil {
			return nil, fmt.Errorf("failed to parse group: %s, %s", configKey, err)
		}

		if len(groupConfig) == 0 {
			return nil, fmt.Errorf("invalid group: %s, no entries", configKey)
		}

		for _, entry := range groupConfig {
			if entry.BaseURL == "" {
				return nil, fmt.Errorf("invalid group: %s, missing baseURL in entry", configKey)
			}
		}

		quorumGroups[groupName] = groupConfig
	}

	return quorumGroups, nil
}

func sendMessage(message *iotago.Message, msIndex ...milestone.Index) (hornet.MessageID, error) {

	var err error

	var milestoneConfirmedEventChan chan struct{}

	if len(msIndex) > 0 {
		milestoneConfirmedEventChan = deps.NodeBridge.RegisterMilestoneConfirmedEvent(context.Background(), msIndex[0])
	}

	defer func() {
		if err != nil {
			if len(msIndex) > 0 {
				deps.NodeBridge.DeregisterMilestoneConfirmedEvent(msIndex[0])
			}
		}
	}()

	messageID, err := deps.NodeBridge.EmitMessage(CorePlugin.Daemon().ContextStopped(), message)
	if err != nil {
		return nil, err
	}

	msgSolidEventChan := deps.NodeBridge.RegisterMessageSolidEvent(context.Background(), messageID)

	defer func() {
		if err != nil {
			deps.NodeBridge.DeregisterMessageSolidEvent(messageID)
		}
	}()

	// wait until the message is solid
	if err = utils.WaitForChannelClosed(context.Background(), msgSolidEventChan); err != nil {
		return nil, err
	}

	if len(msIndex) > 0 {
		// if it was a milestone, also wait until the milestone was confirmed
		if err = utils.WaitForChannelClosed(context.Background(), milestoneConfirmedEventChan); err != nil {
			return nil, err
		}
	}

	return hornet.MessageIDFromArray(messageID), nil
}

func configureEvents() {
	// pass all new solid messages to the selector
	onMessageSolid = events.NewClosure(func(metadata *inx.MessageMetadata) {

		if metadata.GetShouldReattach() {
			// ignore tips that are below max depth
			return
		}

		// add tips to the heaviest branch selector
		if trackedMessagesCount := deps.Selector.OnNewSolidMessage(metadata); trackedMessagesCount >= maxTrackedMessages {
			CorePlugin.LogDebugf("Coordinator Tipselector: trackedMessagesCount: %d", trackedMessagesCount)

			// issue next checkpoint
			select {
			case nextCheckpointSignal <- struct{}{}:
			default:
				// do not block if already another signal is waiting
			}
		}
	})

	onConfirmedMilestoneChanged = events.NewClosure(func(_ *inx.Milestone) {
		heaviestSelectorLock.Lock()
		defer heaviestSelectorLock.Unlock()

		// the selector needs to be reset after the milestone was confirmed, otherwise
		// it could contain tips that are already below max depth.
		deps.Selector.Reset()

		// the checkpoint also needs to be reset, otherwise
		// a checkpoint could have been issued in the meantime,
		// which could contain messages that are already below max depth.
		lastCheckpointMessageID = lastMilestoneMessageID
		lastCheckpointIndex = 0
	})

	onIssuedCheckpoint = events.NewClosure(func(checkpointIndex int, tipIndex int, tipsTotal int, messageID hornet.MessageID) {
		CorePlugin.LogInfof("checkpoint (%d) message issued (%d/%d): %v", checkpointIndex+1, tipIndex+1, tipsTotal, messageID.ToHex())
	})

	onIssuedMilestone = events.NewClosure(func(index milestone.Index, milestoneID iotago.MilestoneID, messageID hornet.MessageID) {
		CorePlugin.LogInfof("milestone issued (%d) MilestoneID: %s, MessageID: %v", index, iotago.EncodeHex(milestoneID[:]), messageID.ToHex())
	})
}

func attachEvents() {
	deps.NodeBridge.Events.MessageSolid.Attach(onMessageSolid)
	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Attach(onConfirmedMilestoneChanged)
	deps.Coordinator.Events.IssuedCheckpointMessage.Attach(onIssuedCheckpoint)
	deps.Coordinator.Events.IssuedMilestone.Attach(onIssuedMilestone)
}

func detachEvents() {
	deps.NodeBridge.Events.MessageSolid.Detach(onMessageSolid)
	deps.NodeBridge.Events.ConfirmedMilestoneChanged.Detach(onConfirmedMilestoneChanged)
	deps.Coordinator.Events.IssuedCheckpointMessage.Detach(onIssuedCheckpoint)
	deps.Coordinator.Events.IssuedMilestone.Detach(onIssuedMilestone)
}
