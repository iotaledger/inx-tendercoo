package decoo

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
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
	triggerNextMilestone     chan struct{}
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

	var info milestoneInfo
	for {
		select {
		case <-timer.C: // propose a parent for the next milestone
			// check that the node is synced
			lmi := deps.NodeBridge.LatestMilestoneIndex()
			cmi := deps.NodeBridge.ConfirmedMilestoneIndex()
			if lmi != cmi {
				CoreComponent.LogWarnf("node is not synced; latest=%d confirmed=%d; retrying in %s", lmi, cmi, SyncRetryInterval)
				timer.Reset(SyncRetryInterval)

				continue
			}

			if cmi != info.index {
				CoreComponent.LogWarnf("node is not synced; confirmed=%d, current coo index=%d; retrying in %s", cmi, info.index, SyncRetryInterval)
				timer.Reset(SyncRetryInterval)

				continue
			}

			CoreComponent.LogInfof("proposing parent for milestone %d", info.index+1)
			tips, err := deps.Selector.SelectTips(1)
			if err != nil {
				CoreComponent.LogWarnf("defaulting to last milestone as tip: %s", err)
				// use the previous milestone block as fallback
				tips = iotago.BlockIDs{info.milestoneBlockID}
			}
			// make sure that at least one second has passed since the last milestone
			if d := time.Until(info.timestamp.Add(time.Second)); d > 0 {
				time.Sleep(d)
			}
			// propose a random tip as parent for the next milestone
			//nolint:gosec // we don't care about weak random numbers here
			if err := deps.Coordinator.ProposeParent(info.index+1, tips[rand.Intn(len(tips))]); err != nil {
				CoreComponent.LogWarnf("failed to propose parent: %s", err)
			}

		case <-triggerNextMilestone: // reset the timer to propose a parent right away
			// the timer has not yet fired, so we need to stop it before resetting
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(0)

		case info = <-confirmedMilestoneSignal: // we have received a new milestone without proposing a parent
			// SelectTips also resets the tips; since this did not happen in this case, we manually reset the selector
			deps.Selector.Reset()
			// the timer has not yet fired, so we need to stop it before resetting
			if !timer.Stop() {
				<-timer.C
			}
			// reset the timer to match the interval since the lasts milestone
			timer.Reset(remainingInterval(info.timestamp))

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

func remainingInterval(ts time.Time) time.Duration {
	d := Parameters.Interval - time.Since(ts)
	if d < 0 {
		d = 0
	}

	return d
}
