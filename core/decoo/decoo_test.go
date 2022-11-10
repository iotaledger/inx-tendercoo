package decoo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/core/logger"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	"github.com/iotaledger/inx-tendercoo/pkg/selector"
	iotago "github.com/iotaledger/iota.go/v3"
)

type CoordinatorMock struct {
	mock.Mock
}

func (c *CoordinatorMock) Start(decoo.ABCIClient) error {
	panic("implement me")
}

func (c *CoordinatorMock) Stop() error {
	panic("implement me")
}

func (c *CoordinatorMock) ProposeParent(ctx context.Context, _ uint32, _ iotago.BlockID) error {
	fmt.Println(time.Now())
	select {
	case <-ctx.Done():
		return context.DeadlineExceeded
	}
}

func TestNewTenderLogger(t *testing.T) {
	log = logger.NewExampleLogger("app")
	require.NoError(t, configure())

	deps.Coordinator = new(CoordinatorMock)
	deps.Selector = selector.NewHeaviest(10, 1, 100)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinatorLoop(ctx)

	confirmedMilestoneSignal <- milestoneInfo{
		index:            0,
		timestamp:        time.Time{},
		milestoneBlockID: iotago.EmptyBlockID(),
	}

	time.Sleep(5 * time.Second)

	confirmedMilestoneSignal <- milestoneInfo{
		index:            1,
		timestamp:        time.Now(),
		milestoneBlockID: iotago.EmptyBlockID(),
	}

	time.Sleep(11 * time.Second)

}
