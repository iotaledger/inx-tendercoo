package decoo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/iotaledger/hive.go/core/timeutil"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/queue"
	iotago "github.com/iotaledger/iota.go/v3"
)

// milestoneInfo contains the information we need from a milestone.
type milestoneInfo struct {
	index            uint32
	timestamp        time.Time
	milestoneBlockID iotago.BlockID
}

var (
	// confirmedMilestone keeps track of the latest confirmed milestone.
	confirmedMilestone struct {
		sync.Mutex
		milestoneInfo
	}
	// confirmedMilestoneSignal signals a new confirmed milestone.
	confirmedMilestoneSignal chan milestoneInfo
	// triggerNextMilestone signals a preemptive milestone trigger to avoid overflows in the tip selector.
	triggerNextMilestone chan struct{}
)

func initialize() error {
	if bootstrap {
		confirmedMilestone.Lock()
		defer confirmedMilestone.Unlock()

		// initialized the latest milestone with the provided dummy values
		confirmedMilestone.index = startIndex - 1
		confirmedMilestone.timestamp = time.Now()
		confirmedMilestone.milestoneBlockID = startMilestoneBlockID
		// trigger confirmedMilestoneSignal to start the loop and issue the first milestone
		confirmedMilestoneSignal <- confirmedMilestone.milestoneInfo

		return nil
	}

	// using latest instead of confirmed milestone assures that it exists and prevents unneeded calls during syncing
	milestone, err := deps.NodeBridge.LatestMilestone()
	if err != nil {
		return fmt.Errorf("failed to query latest milestone: %w", err)
	}
	// trigger confirmedMilestoneSignal to start the loop and issue the next milestone
	processConfirmedMilestone(milestone.Milestone)

	return nil
}

func coordinatorLoop(ctx context.Context) {
	// start a timer such that it does not fire before confirmedMilestoneSignal was received
	timer := time.NewTimer(math.MaxInt64)
	defer timer.Stop()
	// propose the next parent; this automatically cancels obsolete proposes and retries on error
	proposer := queue.NewSingle[milestoneInfo](Parameters.Interval/2, proposeParent)
	defer proposer.Stop()

	var info milestoneInfo
	for {
		select {
		case <-timer.C: // propose a parent for the next milestone
			// check that the node is synced, i.e. the latest milestone matches the confirmed milestone
			// we cannot use deps.NodeBridge.IsNodeSynced() here, as this is always false during bootstrapping
			if lmi, cmi := getMilestoneIndex(); lmi != cmi {
				CoreComponent.LogWarnf("node is not synced; latest=%d confirmed=%d; retrying in %s", lmi, cmi, SyncRetryInterval)
				timer.Reset(SyncRetryInterval)

				continue
			}
			// add the proposeParent call for that milestone to the queue
			proposer.Submit(info)

		case <-triggerNextMilestone: // reset the timer to propose a parent right away
			resetRunningTimer(timer, 0)

		case info = <-confirmedMilestoneSignal: // we have received a new milestone without proposing a parent
			// reset the timer to fire when the next milestone is due
			resetRunningTimer(timer, remainingInterval(info.timestamp))

			continue

		case <-ctx.Done(): // end the loop
			return
		}

		// when this select is reach, the timer has fired and a milestone was proposed
		select {
		case <-triggerNextMilestone: // reset the timer to propose a parent right away
			timer.Reset(0)

		case info = <-confirmedMilestoneSignal: // the new milestone is confirmed, we can now reset the timer
			// reset the timer to match the interval since the lasts milestone
			timer.Reset(remainingInterval(info.timestamp))

		case <-ctx.Done(): // end the loop
			return
		}
	}
}

func proposeParent(ctx context.Context, info milestoneInfo) error {
	// sanity check: make sure that at least one second has passed since the last milestone
	if d := time.Until(info.timestamp.Add(time.Second)); d > 0 && !timeutil.Sleep(ctx, d) {
		return ctx.Err()
	}

	// if the confirmed milestone index has progressed further than the milestone index, we can cancel
	if _, cmi := getMilestoneIndex(); cmi > info.index {
		CoreComponent.LogInfof("no need to propose parent for milestone %d: cmi=%d", info.index+1, cmi)

		return nil
	}

	CoreComponent.LogInfof("proposing parent for milestone %d", info.index+1)
	tips, err := deps.Selector.SelectTips(ctx, 1)
	if errors.Is(err, context.Canceled) {
		return err
	}
	if err != nil {
		CoreComponent.LogWarnf("defaulting to last milestone as tip: %s", err)
		// use the previous milestone block as fallback
		tips = iotago.BlockIDs{info.milestoneBlockID}
	}

	// propose a random tip as a parent for the next milestone
	//nolint:gosec // we don't care about weak random numbers here
	err = deps.Coordinator.ProposeParent(ctx, info.index+1, tips[rand.Intn(len(tips))])
	// only log a warning when the context was not canceled
	if err != nil && ctx.Err() != nil {
		CoreComponent.LogWarnf("failed to propose parent for %d: %s", info.index+1, err)
	}

	return err
}

func processConfirmedMilestone(milestone *iotago.Milestone) {
	confirmedMilestone.Lock()
	defer confirmedMilestone.Unlock()

	if milestone.Index <= confirmedMilestone.index {
		return
	}

	confirmedMilestone.index = milestone.Index
	confirmedMilestone.timestamp = time.Unix(int64(milestone.Timestamp), 0)
	confirmedMilestone.milestoneBlockID = decoo.MilestoneBlockID(milestone)
	confirmedMilestoneSignal <- confirmedMilestone.milestoneInfo
}

func getMilestoneIndex() (uint32, uint32) {
	// store the nodeStatus to prevent race-conditions between the two GetMilestoneIndex calls
	nodeStatus := deps.NodeBridge.NodeStatus()
	lmi := nodeStatus.GetLatestMilestone().GetMilestoneInfo().GetMilestoneIndex()
	cmi := nodeStatus.GetConfirmedMilestone().GetMilestoneInfo().GetMilestoneIndex()

	return lmi, cmi
}

func resetRunningTimer(timer *time.Timer, d time.Duration) {
	// the timer has not yet fired, so we need to stop it before resetting
	if !timer.Stop() {
		<-timer.C
	}
	timer.Reset(d)
}

func remainingInterval(t time.Time) time.Duration {
	d := Parameters.Interval - time.Since(t)
	if d < 0 {
		d = 0
	}

	return d
}
