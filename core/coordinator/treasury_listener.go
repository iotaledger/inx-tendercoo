package coordinator

import (
	"context"
	"io"
	"sync"

	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/gohornet/inx-app/nodebridge"
	"github.com/gohornet/inx-coordinator/pkg/coordinator"
	"github.com/iotaledger/hive.go/logger"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

type TreasuryListener struct {
	*logger.WrappedLogger

	nodeBridge *nodebridge.NodeBridge

	treasuryOutputMutex  sync.RWMutex
	latestTreasuryOutput *inx.TreasuryOutput
}

func NewTreasuryListener(log *logger.Logger, nodeBridge *nodebridge.NodeBridge) *TreasuryListener {
	return &TreasuryListener{
		WrappedLogger:        logger.NewWrappedLogger(log),
		nodeBridge:           nodeBridge,
		latestTreasuryOutput: nil,
	}
}

func (n *TreasuryListener) processTreasuryUpdate(update *inx.TreasuryUpdate) {
	n.treasuryOutputMutex.Lock()
	defer n.treasuryOutputMutex.Unlock()
	created := update.GetCreated()
	milestoneID := created.UnwrapMilestoneID()
	n.LogInfof("Updating TreasuryOutput at %d: MilestoneID: %s, Amount: %d ", update.GetMilestoneIndex(), iotago.EncodeHex(milestoneID[:]), created.GetAmount())
	n.latestTreasuryOutput = created
}

func (n *TreasuryListener) LatestTreasuryOutput() (*coordinator.LatestTreasuryOutput, error) {
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

func (n *TreasuryListener) listenToTreasuryUpdates(ctx context.Context, cancel context.CancelFunc) error {
	defer cancel()
	stream, err := n.nodeBridge.Client().ListenToTreasuryUpdates(ctx, &inx.MilestoneRangeRequest{})
	if err != nil {
		return err
	}
	for {
		update, err := stream.Recv()
		if err != nil {
			if err == io.EOF || status.Code(err) == codes.Canceled {
				break
			}
			n.LogErrorf("listenToTreasuryUpdates: %s", err.Error())
			break
		}
		if ctx.Err() != nil {
			break
		}
		n.processTreasuryUpdate(update)
	}
	return nil
}

func (n *TreasuryListener) Run(ctx context.Context) {
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	go n.listenToTreasuryUpdates(c, cancel)
	<-c.Done()
}
