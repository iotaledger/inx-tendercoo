package utils

import (
	"github.com/gohornet/hornet/pkg/model/hornet"
	inx "github.com/iotaledger/inx/go"
)

func INXMessageIDsFromMessageIDs(messageIDs hornet.MessageIDs) []*inx.MessageId {
	result := make([]*inx.MessageId, len(messageIDs))
	for i := range messageIDs {
		result[i] = inx.NewMessageId(messageIDs[i].ToArray())
	}
	return result
}

func MessageIDsFromINXMessageIDs(messageIDs []*inx.MessageId) hornet.MessageIDs {
	result := make([]hornet.MessageID, len(messageIDs))
	for i := range messageIDs {
		result[i] = hornet.MessageIDFromArray(messageIDs[i].Unwrap())
	}
	return result
}
