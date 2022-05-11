package nodebridge

import (
	"github.com/gohornet/hornet/pkg/model/milestone"
	"github.com/iotaledger/hive.go/events"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

type TangleListener struct {
	messageSolidSyncEvent       *events.SyncEvent
	milestoneConfirmedSyncEvent *events.SyncEvent
}

func newTangleListener() *TangleListener {
	return &TangleListener{
		messageSolidSyncEvent:       events.NewSyncEvent(),
		milestoneConfirmedSyncEvent: events.NewSyncEvent(),
	}
}

func (t *TangleListener) MessageSolidSyncEvent() *events.SyncEvent {
	return t.messageSolidSyncEvent
}

func (t *TangleListener) MilestoneConfirmedSyncEvent() *events.SyncEvent {
	return t.milestoneConfirmedSyncEvent
}

func (t *TangleListener) RegisterMessageSolidEvent(messageID iotago.MessageID) chan struct{} {
	return t.messageSolidSyncEvent.RegisterEvent(string(messageID[:]))
}

func (t *TangleListener) DeregisterMessageSolidEvent(messageID iotago.MessageID) {
	t.messageSolidSyncEvent.DeregisterEvent(string(messageID[:]))
}

func (t *TangleListener) RegisterMilestoneConfirmedEvent(msIndex milestone.Index) chan struct{} {
	return t.milestoneConfirmedSyncEvent.RegisterEvent(msIndex)
}

func (t *TangleListener) DeregisterMilestoneConfirmedEvent(msIndex milestone.Index) {
	t.milestoneConfirmedSyncEvent.DeregisterEvent(msIndex)
}

func (t *TangleListener) processSolidMessage(metadata *inx.MessageMetadata) {
	t.messageSolidSyncEvent.Trigger(string(metadata.GetMessageId().GetId()))
}

func (t *TangleListener) processConfirmedMilestone(ms *inx.Milestone) {
	t.milestoneConfirmedSyncEvent.Trigger(milestone.Index(ms.GetMilestoneInfo().GetMilestoneIndex()))
}
