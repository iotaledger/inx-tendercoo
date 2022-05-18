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
	"github.com/gohornet/hornet/pkg/model/milestone"
	"github.com/gohornet/inx-coordinator/pkg/coordinator"
	"github.com/iotaledger/hive.go/events"
	"github.com/iotaledger/hive.go/logger"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

type NodeBridge struct {
	Logger         *logger.Logger
	Client         inx.INXClient
	NodeConfig     *inx.NodeConfiguration
	tangleListener *TangleListener
	Events         *Events

	isSyncedMutex      sync.RWMutex
	latestMilestone    *inx.MilestoneInfo
	confirmedMilestone *inx.MilestoneInfo

	enableTreasuryUpdates bool
	treasuryOutputMutex   sync.RWMutex
	latestTreasuryOutput  *inx.TreasuryOutput
}

type Events struct {
	BlockSolid                *events.Event
	ConfirmedMilestoneChanged *events.Event
}

func INXBlockMetadataCaller(handler interface{}, params ...interface{}) {
	handler.(func(metadata *inx.BlockMetadata))(params[0].(*inx.BlockMetadata))
}

func INXMilestoneCaller(handler interface{}, params ...interface{}) {
	handler.(func(metadata *inx.Milestone))(params[0].(*inx.Milestone))
}

func NewNodeBridge(ctx context.Context, client inx.INXClient, enableTreasuryUpdates bool, logger *logger.Logger) (*NodeBridge, error) {
	logger.Info("Connecting to node and reading protocol parameters...")

	retryBackoff := func(_ uint) time.Duration {
		return 1 * time.Second
	}

	nodeConfig, err := client.ReadNodeConfiguration(ctx, &inx.NoParams{}, grpc_retry.WithMax(5), grpc_retry.WithBackoff(retryBackoff))
	if err != nil {
		return nil, err
	}

	nodeStatus, err := client.ReadNodeStatus(ctx, &inx.NoParams{})
	if err != nil {
		return nil, err
	}

	return &NodeBridge{
		Logger:         logger,
		Client:         client,
		NodeConfig:     nodeConfig,
		tangleListener: newTangleListener(),
		Events: &Events{
			BlockSolid:                events.NewEvent(INXBlockMetadataCaller),
			ConfirmedMilestoneChanged: events.NewEvent(INXMilestoneCaller),
		},
		latestMilestone:       nodeStatus.GetLatestMilestone(),
		confirmedMilestone:    nodeStatus.GetConfirmedMilestone(),
		enableTreasuryUpdates: enableTreasuryUpdates,
	}, nil
}

func (n *NodeBridge) MilestonePublicKeyCount() int {
	return int(n.NodeConfig.GetMilestonePublicKeyCount())
}

func (n *NodeBridge) KeyManager() *keymanager.KeyManager {
	keyManager := keymanager.New()
	for _, keyRange := range n.NodeConfig.GetMilestoneKeyRanges() {
		keyManager.AddKeyRange(keyRange.GetPublicKey(), milestone.Index(keyRange.GetStartIndex()), milestone.Index(keyRange.GetEndIndex()))
	}
	return keyManager
}

func (n *NodeBridge) Run(ctx context.Context) {
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	go n.listenToConfirmedMilestone(c, cancel)
	go n.listenToLatestMilestone(c, cancel)
	go n.listenToSolidBlocks(c, cancel)
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
		Index:     n.latestMilestone.GetMilestoneIndex(),
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

func (n *NodeBridge) ComputeMerkleTreeHash(ctx context.Context, msIndex uint32, msTimestamp uint32, parents iotago.BlockIDs, previousMilestoneId iotago.MilestoneID) (*coordinator.MilestoneMerkleRoots, error) {
	req := &inx.WhiteFlagRequest{
		MilestoneIndex:      msIndex,
		MilestoneTimestamp:  msTimestamp,
		Parents:             inx.NewBlockIds(parents),
		PreviousMilestoneId: inx.NewMilestoneId(previousMilestoneId),
	}

	res, err := n.Client.ComputeWhiteFlag(ctx, req)
	if err != nil {
		return nil, err
	}

	proof := &coordinator.MilestoneMerkleRoots{
		InclusionMerkleRoot: iotago.MilestoneMerkleProof{},
		AppliedMerkleRoot:   iotago.MilestoneMerkleProof{},
	}
	copy(proof.InclusionMerkleRoot[:], res.GetMilestoneInclusionMerkleRoot())
	copy(proof.AppliedMerkleRoot[:], res.GetMilestoneAppliedMerkleRoot())

	return proof, nil
}

func (n *NodeBridge) SubmitBlock(ctx context.Context, block *iotago.Block) (iotago.BlockID, error) {

	blk, err := inx.WrapBlock(block)
	if err != nil {
		return iotago.BlockID{}, err
	}

	response, err := n.Client.SubmitBlock(ctx, blk)
	if err != nil {
		return iotago.BlockID{}, err
	}

	return response.Unwrap(), nil
}

func (n *NodeBridge) BlockMetadata(ctx context.Context, blockID iotago.BlockID) (*inx.BlockMetadata, error) {
	return n.Client.ReadBlockMetadata(ctx, inx.NewBlockId(blockID))
}

func (n *NodeBridge) listenToSolidBlocks(ctx context.Context, cancel context.CancelFunc) error {
	defer cancel()
	filter := &inx.BlockFilter{}
	stream, err := n.Client.ListenToSolidBlocks(ctx, filter)
	if err != nil {
		return err
	}
	for {
		metadata, err := stream.Recv()
		if err != nil {
			if err == io.EOF || status.Code(err) == codes.Canceled {
				break
			}
			n.Logger.Errorf("listenToSolidBlocks: %s", err.Error())
			break
		}
		if ctx.Err() != nil {
			break
		}
		n.processSolidBlock(metadata)
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

func (n *NodeBridge) processSolidBlock(metadata *inx.BlockMetadata) {
	n.tangleListener.processSolidBlock(metadata)
	n.Events.BlockSolid.Trigger(metadata)
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

func (n *NodeBridge) RegisterBlockSolidEvent(ctx context.Context, blockID iotago.BlockID) chan struct{} {
	blockSolidChan := n.tangleListener.RegisterBlockSolidEvent(blockID)

	// check if the block is already solid
	metadata, err := n.BlockMetadata(ctx, blockID)
	if err == nil {
		if metadata.Solid {
			// trigger the sync event, because the block is already solid
			n.tangleListener.processSolidBlock(metadata)
		}
	}

	return blockSolidChan
}

func (n *NodeBridge) DeregisterBlockSolidEvent(blockID iotago.BlockID) {
	n.tangleListener.DeregisterBlockSolidEvent(blockID)
}

func (n *NodeBridge) RegisterMilestoneConfirmedEvent(msIndex uint32) chan struct{} {
	return n.tangleListener.RegisterMilestoneConfirmedEvent(msIndex)
}

func (n *NodeBridge) DeregisterMilestoneConfirmedEvent(msIndex uint32) {
	n.tangleListener.DeregisterMilestoneConfirmedEvent(msIndex)
}
