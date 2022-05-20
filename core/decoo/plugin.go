package decoo

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gohornet/hornet/pkg/model/hornet"
	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/configuration"
	"github.com/iotaledger/hive.go/events"
	"github.com/iotaledger/hive.go/kvstore/mapdb"
	"github.com/iotaledger/hive.go/logger"
	"github.com/iotaledger/hive.go/serializer/v2"
	"github.com/iotaledger/inx-tendercoo/pkg/daemon"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	"github.com/iotaledger/inx-tendercoo/pkg/mselection"
	"github.com/iotaledger/inx-tendercoo/pkg/nodebridge"
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
	tendermintWorkerName = "Tendermint Node"
	decooWorkerName      = "Coordinator"
)

// ValidatorsConfig defines the config options for one validator.
// TODO: is there a better way to define multiple validators in the config?
type ValidatorsConfig struct {
	PubKey []byte `json:"pubKey" koanf:"pubKey"`
	Power  int64  `json:"power" koanf:"power"`
}

// Byte32HexValue holds a 32-byte array. It is used to store values from CLI flags.
// TODO: do we already have something like this to cleanly parse 32-byte arrays from flags?
type Byte32HexValue [32]byte

// NewByte32Hex creates a new Byte32HexValue from a reference 32-byte array and a default value.
func NewByte32Hex(defaultVal [32]byte, p *[32]byte) *Byte32HexValue {
	copy(p[:], defaultVal[:])
	return (*Byte32HexValue)(p)
}

func (v *Byte32HexValue) Type() string { return "byte32Hex" }

func (v *Byte32HexValue) String() string { return hex.EncodeToString(v[:]) }

func (v *Byte32HexValue) Set(val string) error {
	// pflag always trims string values, so we should do the same
	trimmed := strings.TrimSpace(val)
	if l := len(trimmed); l != hex.EncodedLen(len(Byte32HexValue{})) {
		return fmt.Errorf("invalid length: %d", l)
	}
	dec, err := hex.DecodeString(trimmed)
	if err != nil {
		return err
	}
	copy(v[:], dec)
	return nil
}

func init() {
	flag.BoolVar(bootstrap, CfgCoordinatorBootstrap, false, "whether the network is bootstrapped")
	flag.Uint32Var(startIndex, CfgCoordinatorStartIndex, 1, "index of the first milestone at bootstrap")
	flag.Var(NewByte32Hex(iotago.MessageID{}, startMilestoneID), CfgCoordinatorStartMilestoneID, "the previous milestone ID at bootstrap")
	flag.Var(NewByte32Hex(iotago.MessageID{}, startMilestoneMessageID), CfgCoordinatorStartMilestoneMessageID, "previous milestone message ID at bootstrap")

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
	bootstrap               *bool
	startIndex              *uint32
	startMilestoneID        *iotago.MilestoneID
	startMilestoneMessageID *iotago.MessageID

	// config parameters
	maxTrackedMessages int
	interval           time.Duration

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
		err = coo.InitState(*bootstrap, &decoo.State{
			Height:                0,
			CurrentMilestoneIndex: *startIndex,
			LastMilestoneID:       *startMilestoneID,
			LastMilestoneMsgID:    *startMilestoneMessageID,
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

func processMilestone(inxMilestone *inx.Milestone) {
	latestMilestone.Lock()
	defer latestMilestone.Unlock()

	if inxMilestone.GetMilestoneInfo().GetMilestoneIndex() < latestMilestone.Index+1 {
		return
	}

	milestone := &iotago.Milestone{}
	if _, err := milestone.Deserialize(inxMilestone.GetMilestone().GetData(), serializer.DeSeriModeNoValidation, nil); err != nil {
		CoreComponent.LogPanicf("failed to deserialize milestone: %s", err)
	}
	latestMilestone.Index = milestone.Index
	latestMilestone.MilestoneMsgID = decoo.MilestoneMessageID(milestone)
	newMilestoneSignal <- latestMilestone.MilestoneInfo
}

func configure() error {
	maxTrackedMessages = deps.AppConfig.Int(CfgCoordinatorMaxTrackedMessages)
	interval = deps.AppConfig.Duration(CfgCoordinatorInterval)

	trackMessages.Store(true)
	newMilestoneSignal = make(chan MilestoneInfo, 1)
	if *bootstrap {
		// initialized the latest milestone with the provided dummy values
		latestMilestone.Index = *startIndex - 1
		latestMilestone.MilestoneMsgID = *startMilestoneMessageID
		// trigger issuing a milestone for that index
		newMilestoneSignal <- latestMilestone.MilestoneInfo
	}

	// pass all new solid messages to the selector and preemptively trigger new milestone when needed
	onMessageSolid = events.NewClosure(func(metadata *inx.MessageMetadata) {
		if !trackMessages.Load() || metadata.GetShouldReattach() {
			return
		}
		// add tips to the heaviest branch selector
		// if there are too many messages, trigger the latest milestone again. This will trigger a new milestone.
		if trackedMessagesCount := deps.Selector.OnNewSolidMessage(metadata); trackedMessagesCount >= maxTrackedMessages {
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
	onConfirmedMilestoneChanged = events.NewClosure(func(milestone *inx.Milestone) {
		// the selector needs to be reset after the milestone was confirmed
		deps.Selector.Reset()
		// make sure that new solid messages are tracked
		trackMessages.Store(true)
		// process the milestone for the coordinator
		processMilestone(milestone)
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

type MilestoneInfo struct {
	Index          uint32
	MilestoneMsgID iotago.MessageID
}

func coordinatorLoop(ctx context.Context) {
	// if we are not bootstrapping, we need to make sure that the latest stored milestone gets processed
	if !*bootstrap {
		go func() {
			index := uint32(deps.NodeBridge.LatestMilestone().Index)
			milestone, err := deps.NodeBridge.Client.ReadMilestone(ctx, &inx.MilestoneRequest{MilestoneIndex: index})
			if err != nil {
				CoreComponent.LogWarnf("failed to read latest milestone: %s", err)
			} else {
				// processing will trigger newMilestoneSignal to start the loop
				processMilestone(milestone)
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
				tips = hornet.MessageIDs{info.MilestoneMsgID[:]}
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
			timer.Reset(interval)
			continue

		case <-ctx.Done(): // end the loop
			return
		}

		// when this select is reach, the timer has fired and a milestone was proposed
		select {
		case info = <-newMilestoneSignal: // the new milestone is confirmed, we can now reset the timer
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
	var seed Byte32HexValue
	if err := seed.Set(value); err != nil {
		return nil, fmt.Errorf("environment variable '%s' contains an invalid private key: %w", name, err)
	}
	return ed25519.NewKeyFromSeed(seed[:]), nil
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
