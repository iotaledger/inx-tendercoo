package decoo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	"github.com/iotaledger/hive.go/core/logger"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

const gracePeriod = 100 * time.Millisecond

func init() {
	log = logger.NewExampleLogger("decoo")
}

var milestoneIndex atomic.Uint32

func setTestDependencies() *CoordinatorMock {
	m := new(CoordinatorMock)
	deps.Coordinator = m
	deps.Selector = &SelectorMock{}

	milestoneIndex.Store(0)
	deps.MilestoneIndexFunc = func() (uint32, uint32) {
		index := milestoneIndex.Load()

		return index, index
	}

	return m
}

func TestCoordinatorLoop(t *testing.T) {
	require.NoError(t, configure())

	Parameters.Interval = time.Second

	m := setTestDependencies()
	m.On("ProposeParent", uint32(1), iotago.EmptyBlockID()).Once().Return(nil)
	m.On("ProposeParent", uint32(2), iotago.EmptyBlockID()).Once().Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinatorLoop(ctx)

	// initialize
	confirmedMilestoneSignal <- milestoneInfo{}

	time.Sleep(Parameters.Interval + gracePeriod)
	m.AssertExpectations(t)
}

// TestRetryProposeParent tests whether the ProposeParent call is retried, after an error.
func TestRetryProposeParent(t *testing.T) {
	require.NoError(t, configure())

	Parameters.Interval = time.Second

	m := setTestDependencies()
	m.On("ProposeParent", uint32(1), iotago.EmptyBlockID()).Once().Return(errors.New("mock error"))
	m.On("ProposeParent", uint32(1), iotago.EmptyBlockID()).Once().Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinatorLoop(ctx)

	// initialize
	confirmedMilestoneSignal <- milestoneInfo{}

	time.Sleep(gracePeriod)
	require.EqualValues(t, 0, milestoneIndex.Load())

	time.Sleep(Parameters.Interval / 2)
	m.AssertExpectations(t)
}

// TestTriggerNextMilestone tests whether the preemptive triggering of milestones works.
func TestTriggerNextMilestone(t *testing.T) {
	require.NoError(t, configure())

	// set the milestone interval very long so that it needs to be triggered
	Parameters.Interval = time.Minute

	m := setTestDependencies()
	m.On("ProposeParent", uint32(1), iotago.EmptyBlockID()).Once().Return(nil)
	m.On("ProposeParent", uint32(2), iotago.EmptyBlockID()).Once().Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinatorLoop(ctx)

	// initialize
	confirmedMilestoneSignal <- milestoneInfo{}

	time.Sleep(time.Second + gracePeriod)
	require.EqualValues(t, 1, milestoneIndex.Load())

	triggerNextMilestone <- struct{}{}

	time.Sleep(gracePeriod)
	m.AssertExpectations(t)
}

type CoordinatorMock struct{ mock.Mock }

func (c *CoordinatorMock) Start(decoo.ABCIClient) error { panic("not implemented") }
func (c *CoordinatorMock) Stop() error                  { panic("not implemented") }

func (c *CoordinatorMock) ProposeParent(_ context.Context, index uint32, block iotago.BlockID) error {
	arg := c.Called(index, block)
	if err := arg.Error(0); err != nil {
		return err
	}

	// update the next milestone
	milestoneIndex.CompareAndSwap(index-1, index)
	confirmedMilestoneSignal <- milestoneInfo{
		index:            index,
		timestamp:        time.Now(),
		milestoneBlockID: iotago.EmptyBlockID(),
	}

	return nil
}

type SelectorMock struct{}

func (s SelectorMock) TrackedBlocks() int                     { panic("not implemented") }
func (s SelectorMock) OnNewSolidBlock(*inx.BlockMetadata) int { panic("not implemented") }
func (s SelectorMock) Reset()                                 { panic("not implemented") }

func (s SelectorMock) SelectTips(context.Context, int) (iotago.BlockIDs, error) {
	// always return the empty block ID
	return iotago.BlockIDs{iotago.EmptyBlockID()}, nil
}
