package nodebridge

import (
	"context"
	"io"
	"sync"
	"time"

	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/gohornet/hornet/pkg/keymanager"
	"github.com/gohornet/hornet/pkg/model/hornet"
	"github.com/gohornet/hornet/pkg/model/milestone"
	"github.com/gohornet/inx-coordinator/pkg/coordinator"
	"github.com/gohornet/inx-coordinator/pkg/utils"
	"github.com/iotaledger/hive.go/events"
	"github.com/iotaledger/hive.go/logger"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

type NodeBridge struct {
	Logger             *logger.Logger
	Client             inx.INXClient
	ProtocolParameters *inx.ProtocolParameters
	tangleListener     *TangleListener
	Events             *Events

	isSyncedMutex      sync.RWMutex
	latestMilestone    *inx.MilestoneInfo
	confirmedMilestone *inx.MilestoneInfo

	enableTreasuryUpdates bool
	treasuryOutputMutex   sync.RWMutex
	latestTreasuryOutput  *inx.TreasuryOutput
}

type Events struct {
	MessageSolid              *events.Event
	ConfirmedMilestoneChanged *events.Event
}

func INXMessageMetadataCaller(handler interface{}, params ...interface{}) {
	handler.(func(metadata *inx.MessageMetadata))(params[0].(*inx.MessageMetadata))
}

func INXMilestoneCaller(handler interface{}, params ...interface{}) {
	handler.(func(metadata *inx.Milestone))(params[0].(*inx.Milestone))
}

func NewNodeBridge(ctx context.Context, client inx.INXClient, enableTreasuryUpdates bool, logger *logger.Logger) (*NodeBridge, error) {
	logger.Info("Connecting to node and reading protocol parameters...")

	retryBackoff := func(_ uint) time.Duration {
		return 1 * time.Second
	}

	protocolParams, err := client.ReadProtocolParameters(ctx, &inx.NoParams{}, grpc_retry.WithMax(5), grpc_retry.WithBackoff(retryBackoff))
	if err != nil {
		return nil, err
	}

	nodeStatus, err := client.ReadNodeStatus(ctx, &inx.NoParams{})
	if err != nil {
		return nil, err
	}

	return &NodeBridge{
		Logger:             logger,
		Client:             client,
		ProtocolParameters: protocolParams,
		tangleListener:     newTangleListener(),
		Events: &Events{
			MessageSolid:              events.NewEvent(INXMessageMetadataCaller),
			ConfirmedMilestoneChanged: events.NewEvent(INXMilestoneCaller),
		},
		latestMilestone:       nodeStatus.GetLatestMilestone(),
		confirmedMilestone:    nodeStatus.GetConfirmedMilestone(),
		enableTreasuryUpdates: enableTreasuryUpdates,
	}, nil
}

func (n *NodeBridge) DeserializationParameters() *iotago.DeSerializationParameters {
	return &iotago.DeSerializationParameters{
		RentStructure: &iotago.RentStructure{
			VByteCost:    n.ProtocolParameters.RentStructure.GetVByteCost(),
			VBFactorData: iotago.VByteCostFactor(n.ProtocolParameters.RentStructure.GetVByteFactorData()),
			VBFactorKey:  iotago.VByteCostFactor(n.ProtocolParameters.RentStructure.GetVByteFactorKey()),
		},
	}
}

func (n *NodeBridge) MilestonePublicKeyCount() int {
	return int(n.ProtocolParameters.GetMilestonePublicKeyCount())
}

func (n *NodeBridge) KeyManager() *keymanager.KeyManager {
	keyManager := keymanager.New()
	for _, keyRange := range n.ProtocolParameters.GetMilestoneKeyRanges() {
		keyManager.AddKeyRange(keyRange.GetPublicKey(), milestone.Index(keyRange.GetStartIndex()), milestone.Index(keyRange.GetEndIndex()))
	}
	return keyManager
}

func (n *NodeBridge) Run(ctx context.Context) {
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	go n.listenToConfirmedMilestone(c, cancel)
	go n.listenToLatestMilestone(c, cancel)
	go n.listenToSolidMessages(c, cancel)
	if n.enableTreasuryUpdates {
		go n.listenToTreasuryUpdates(c, cancel)
	}
	<-c.Done()
}

func (n *NodeBridge) IsNodeSynced() bool {
	n.isSyncedMutex.RLock()
	defer n.isSyncedMutex.RUnlock()

	if n.confirmedMilestone == nil || n.latestMilestone == nil {
		return false
	}

	return n.latestMilestone.GetMilestoneIndex() == n.confirmedMilestone.GetMilestoneIndex()
}

func (n *NodeBridge) LatestMilestone() *coordinator.LatestMilestoneInfo {
	n.isSyncedMutex.RLock()
	defer n.isSyncedMutex.RUnlock()
	return &coordinator.LatestMilestoneInfo{
		Index:     milestone.Index(n.latestMilestone.GetMilestoneIndex()),
		Timestamp: n.latestMilestone.GetMilestoneTimestamp(),
	}
}

func (n *NodeBridge) LatestTreasuryOutput() (*coordinator.LatestTreasuryOutput, error) {
	n.treasuryOutputMutex.RLock()
	defer n.treasuryOutputMutex.RUnlock()

	if n.latestTreasuryOutput == nil {
		return nil, errors.New("haven't received any treasury outputs yet")
	}

	return &coordinator.LatestTreasuryOutput{
		MilestoneID: n.latestTreasuryOutput.UnwrapMilestoneID(),
		Amount:      n.latestTreasuryOutput.GetAmount(),
	}, nil
}

func (n *NodeBridge) ComputeMerkleTreeHash(ctx context.Context, msIndex milestone.Index, msTimestamp uint32, parents hornet.MessageIDs, lastMilestoneID iotago.MilestoneID) (*coordinator.MilestoneMerkleRoots, error) {
	req := &inx.WhiteFlagRequest{
		MilestoneIndex:     uint32(msIndex),
		MilestoneTimestamp: msTimestamp,
		Parents:            utils.INXMessageIDsFromMessageIDs(parents),
		LastMilestoneId:    inx.NewMilestoneId(lastMilestoneID),
	}

	res, err := n.Client.ComputeWhiteFlag(ctx, req)
	if err != nil {
		return nil, err
	}

	proof := &coordinator.MilestoneMerkleRoots{
		ConfirmedMerkleRoot: iotago.MilestoneMerkleProof{},
		AppliedMerkleRoot:   iotago.MilestoneMerkleProof{},
	}
	copy(proof.ConfirmedMerkleRoot[:], res.GetMilestoneConfirmedMerkleRoot())
	copy(proof.AppliedMerkleRoot[:], res.GetMilestoneAppliedMerkleRoot())

	return proof, nil
}

func (n *NodeBridge) EmitMessage(ctx context.Context, message *iotago.Message) (iotago.MessageID, error) {

	msg, err := inx.WrapMessage(message)
	if err != nil {
		return iotago.MessageID{}, err
	}

	response, err := n.Client.SubmitMessage(ctx, msg)
	if err != nil {
		return iotago.MessageID{}, err
	}

	return response.Unwrap(), nil
}

func (n *NodeBridge) MessageMetadata(ctx context.Context, messageID iotago.MessageID) (*inx.MessageMetadata, error) {
	return n.Client.ReadMessageMetadata(ctx, inx.NewMessageId(messageID))
}

func (n *NodeBridge) listenToSolidMessages(ctx context.Context, cancel context.CancelFunc) error {
	defer cancel()
	filter := &inx.MessageFilter{}
	stream, err := n.Client.ListenToSolidMessages(ctx, filter)
	if err != nil {
		return err
	}
	for {
		messageMetadata, err := stream.Recv()
		if err != nil {
			if err == io.EOF || status.Code(err) == codes.Canceled {
				break
			}
			n.Logger.Errorf("listenToSolidMessages: %s", err.Error())
			break
		}
		if ctx.Err() != nil {
			break
		}
		n.processSolidMessage(messageMetadata)
	}
	return nil
}

func (n *NodeBridge) listenToLatestMilestone(ctx context.Context, cancel context.CancelFunc) error {
	defer cancel()
	stream, err := n.Client.ListenToLatestMilestone(ctx, &inx.NoParams{})
	if err != nil {
		return err
	}
	for {
		milestone, err := stream.Recv()
		if err != nil {
			if err == io.EOF || status.Code(err) == codes.Canceled {
				break
			}
			n.Logger.Errorf("listenToLatestMilestone: %s", err.Error())
			break
		}
		if ctx.Err() != nil {
			break
		}
		n.processLatestMilestone(milestone)
	}
	return nil
}

func (n *NodeBridge) listenToConfirmedMilestone(ctx context.Context, cancel context.CancelFunc) error {
	defer cancel()
	stream, err := n.Client.ListenToConfirmedMilestone(ctx, &inx.NoParams{})
	if err != nil {
		return err
	}
	for {
		milestone, err := stream.Recv()
		if err != nil {
			if err == io.EOF || status.Code(err) == codes.Canceled {
				break
			}
			n.Logger.Errorf("listenToConfirmedMilestone: %s", err.Error())
			break
		}
		if ctx.Err() != nil {
			break
		}
		n.processConfirmedMilestone(milestone)
	}
	return nil
}

func (n *NodeBridge) listenToTreasuryUpdates(ctx context.Context, cancel context.CancelFunc) error {
	defer cancel()
	stream, err := n.Client.ListenToTreasuryUpdates(ctx, &inx.LedgerRequest{})
	if err != nil {
		return err
	}
	for {
		update, err := stream.Recv()
		if err != nil {
			if err == io.EOF || status.Code(err) == codes.Canceled {
				break
			}
			n.Logger.Errorf("listenToTreasuryUpdates: %s", err.Error())
			break
		}
		if ctx.Err() != nil {
			break
		}
		n.processTreasuryUpdate(update)
	}
	return nil
}

func (n *NodeBridge) processSolidMessage(metadata *inx.MessageMetadata) {
	n.tangleListener.processSolidMessage(metadata)
	n.Events.MessageSolid.Trigger(metadata)
}

func (n *NodeBridge) processLatestMilestone(ms *inx.Milestone) {
	n.isSyncedMutex.Lock()
	n.latestMilestone = ms.GetMilestoneInfo()
	n.isSyncedMutex.Unlock()
}

func (n *NodeBridge) processConfirmedMilestone(ms *inx.Milestone) {
	n.isSyncedMutex.Lock()
	n.confirmedMilestone = ms.GetMilestoneInfo()
	n.isSyncedMutex.Unlock()

	n.tangleListener.processConfirmedMilestone(ms)
	n.Events.ConfirmedMilestoneChanged.Trigger(ms)
}

func (n *NodeBridge) processTreasuryUpdate(update *inx.TreasuryUpdate) {
	n.treasuryOutputMutex.Lock()
	defer n.treasuryOutputMutex.Unlock()
	created := update.GetCreated()
	milestoneID := created.UnwrapMilestoneID()
	n.Logger.Infof("Updating TreasuryOutput at %d: MilestoneID: %s, Amount: %d ", update.GetMilestoneIndex(), iotago.EncodeHex(milestoneID[:]), created.GetAmount())
	n.latestTreasuryOutput = created
}

func (n *NodeBridge) RegisterMessageSolidEvent(ctx context.Context, messageID iotago.MessageID) chan struct{} {
	messageSolidChan := n.tangleListener.RegisterMessageSolidEvent(messageID)

	// check if the message is already solid
	metadata, err := n.MessageMetadata(ctx, messageID)
	if err == nil {
		if metadata.Solid {
			// trigger the sync event, because the message is already solid
			n.tangleListener.MessageSolidSyncEvent().Trigger(messageID)
		}
	}

	return messageSolidChan
}

func (n *NodeBridge) DeregisterMessageSolidEvent(messageID iotago.MessageID) {
	n.tangleListener.DeregisterMessageSolidEvent(messageID)
}

func (n *NodeBridge) RegisterMilestoneConfirmedEvent(ctx context.Context, msIndex milestone.Index) chan struct{} {
	milestoneConfirmedChan := n.tangleListener.RegisterMilestoneConfirmedEvent(msIndex)

	// check if the milestone is already confirmed
	nodeStatus, err := n.Client.ReadNodeStatus(ctx, &inx.NoParams{})
	if err == nil {
		if milestone.Index(nodeStatus.ConfirmedMilestone.GetMilestoneIndex()) >= msIndex {
			// trigger the sync event, because the milestone is already confirmed
			n.tangleListener.MilestoneConfirmedSyncEvent().Trigger(msIndex)
		}
	}

	return milestoneConfirmedChan
}

func (n *NodeBridge) DeregisterMilestoneConfirmedEvent(msIndex milestone.Index) {
	n.tangleListener.DeregisterMilestoneConfirmedEvent(msIndex)
}
