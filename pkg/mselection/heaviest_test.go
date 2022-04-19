package mselection

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gohornet/hornet/pkg/model/hornet"
	"github.com/gohornet/inx-coordinator/pkg/utils"
	"github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

const (
	CfgCoordinatorTipselectMinHeaviestBranchUnreferencedMessagesThreshold = 20
	CfgCoordinatorTipselectMaxHeaviestBranchTipsPerCheckpoint             = 10
	CfgCoordinatorTipselectRandomTipsPerCheckpoint                        = 3
	CfgCoordinatorTipselectHeaviestBranchSelectionTimeoutMilliseconds     = 100

	numTestMsgs      = 32 * 100
	numBenchmarkMsgs = 5000
)

func init() {
	rand.Seed(0)
}

func newHPS() *HeaviestSelector {

	hps := New(
		CfgCoordinatorTipselectMinHeaviestBranchUnreferencedMessagesThreshold,
		CfgCoordinatorTipselectMaxHeaviestBranchTipsPerCheckpoint,
		CfgCoordinatorTipselectRandomTipsPerCheckpoint,
		CfgCoordinatorTipselectHeaviestBranchSelectionTimeoutMilliseconds,
	)

	return hps
}

func randBytes(length int) []byte {
	var b []byte
	for i := 0; i < length; i++ {
		b = append(b, byte(rand.Intn(256)))
	}
	return b
}

func randMessageID() hornet.MessageID {
	return randBytes(iotago.MessageIDLength)
}

func newMetadata(parents hornet.MessageIDs) (*inx.MessageMetadata, hornet.MessageID) {
	msgID := randMessageID()
	return &inx.MessageMetadata{
		MessageId: inx.NewMessageId(msgID.ToArray()),
		Parents:   utils.INXMessageIDsFromMessageIDs(parents),
		Solid:     true,
	}, msgID
}

func TestHeaviestSelector_SelectTipsChain(t *testing.T) {
	hps := newHPS()

	// create a chain
	lastMsgID := hornet.NullMessageID()
	for i := 1; i <= numTestMsgs; i++ {
		metadata, msgID := newMetadata(hornet.MessageIDs{lastMsgID})
		hps.OnNewSolidMessage(metadata)
		lastMsgID = msgID
	}

	tips, err := hps.SelectTips(1)
	assert.NoError(t, err)
	assert.Len(t, tips, 1)

	// check if the tip on top was picked
	assert.ElementsMatch(t, lastMsgID, tips[0])

	// check if trackedMessages are resetted after tipselect
	assert.Len(t, hps.trackedMessages, 0)
}

func TestHeaviestSelector_CheckTipsRemoved(t *testing.T) {
	hps := newHPS()

	count := 8

	messages := make(hornet.MessageIDs, count)
	for i := 0; i < count; i++ {
		metadata, msgID := newMetadata(hornet.MessageIDs{hornet.NullMessageID()})
		hps.OnNewSolidMessage(metadata)
		messages[i] = msgID
	}

	// check if trackedMessages match the current count
	assert.Len(t, hps.trackedMessages, count)

	// check if the current tips match the current count
	list := hps.tipsToList()
	assert.Len(t, list.msgs, count)

	// issue a new message that references the old ones
	metadata, msgID := newMetadata(messages)
	hps.OnNewSolidMessage(metadata)

	// old tracked messages should remain, plus the new one
	assert.Len(t, hps.trackedMessages, count+1)

	// all old tips should be removed, except the new one
	list = hps.tipsToList()
	assert.Len(t, list.msgs, 1)

	// select a tip
	tips, err := hps.SelectTips(1)
	assert.NoError(t, err)
	assert.Len(t, tips, 1)

	// check if the tip on top was picked
	assert.ElementsMatch(t, msgID, tips[0])

	// check if trackedMessages are resetted after tipselect
	assert.Len(t, hps.trackedMessages, 0)

	list = hps.tipsToList()
	assert.Len(t, list.msgs, 0)
}

func TestHeaviestSelector_SelectTipsChains(t *testing.T) {
	hps := newHPS()

	numChains := 2
	lastMsgIDs := make(hornet.MessageIDs, 2)
	for i := 0; i < numChains; i++ {
		lastMsgIDs[i] = hornet.NullMessageID()
		for j := 1; j <= numTestMsgs; j++ {
			metadata, msgID := newMetadata(hornet.MessageIDs{lastMsgIDs[i]})
			hps.OnNewSolidMessage(metadata)
			lastMsgIDs[i] = msgID
		}
	}

	// check if all messages are tracked
	assert.Equal(t, numChains*numTestMsgs, hps.TrackedMessagesCount())

	tips, err := hps.SelectTips(2)
	assert.NoError(t, err)
	assert.Len(t, tips, 2)

	// check if the tips on top of both branches were picked
	assert.ElementsMatch(t, lastMsgIDs, tips)

	// check if trackedMessages are resetted after tipselect
	assert.Len(t, hps.trackedMessages, 0)
}

func BenchmarkHeaviestSelector_OnNewSolidMessage(b *testing.B) {
	hps := newHPS()

	msgIDs := hornet.MessageIDs{hornet.NullMessageID()}
	msgs := make([]*inx.MessageMetadata, numBenchmarkMsgs)
	for i := 0; i < numBenchmarkMsgs; i++ {
		tipCount := 1 + rand.Intn(7)
		if tipCount > len(msgIDs) {
			tipCount = len(msgIDs)
		}
		tips := make(hornet.MessageIDs, tipCount)
		for j := 0; j < tipCount; j++ {
			tips[j] = msgIDs[rand.Intn(len(msgIDs))]
		}
		tips = tips.RemoveDupsAndSortByLexicalOrder()

		msgs[i], msgIDs[i] = newMetadata(tips)
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		hps.OnNewSolidMessage(msgs[i%numBenchmarkMsgs])
	}
	hps.Reset()
}
