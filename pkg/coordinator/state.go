package coordinator

import (
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/gohornet/hornet/pkg/model/hornet"
	"github.com/gohornet/hornet/pkg/model/milestone"
	iotago "github.com/iotaledger/iota.go/v3"
)

// State stores the latest state of the coordinator.
type State struct {
	LatestMilestoneIndex     milestone.Index
	LatestMilestoneMessageID hornet.MessageID
	LatestMilestoneID        iotago.MilestoneID
	LatestMilestoneTime      time.Time
}

// jsoncoostate is the JSON representation of a coordinator state.
type jsoncoostate struct {
	LatestMilestoneIndex     uint32 `json:"latestMilestoneIndex"`
	LatestMilestoneMessageID string `json:"latestMilestoneMessageID"`
	LatestMilestoneID        string `json:"latestMilestoneID"`
	LatestMilestoneTime      int64  `json:"latestMilestoneTime"`
}

func (cs *State) MarshalJSON() ([]byte, error) {
	return json.Marshal(&jsoncoostate{
		LatestMilestoneIndex:     uint32(cs.LatestMilestoneIndex),
		LatestMilestoneMessageID: hex.EncodeToString(cs.LatestMilestoneMessageID),
		LatestMilestoneID:        hex.EncodeToString(cs.LatestMilestoneID[:]),
		LatestMilestoneTime:      cs.LatestMilestoneTime.UnixNano(),
	})
}

func (cs *State) UnmarshalJSON(data []byte) error {
	jsonCooState := &jsoncoostate{}
	if err := json.Unmarshal(data, jsonCooState); err != nil {
		return err
	}

	var err error
	cs.LatestMilestoneMessageID, err = hex.DecodeString(jsonCooState.LatestMilestoneMessageID)
	if err != nil {
		return err
	}

	latestMilestoneIDBytes, err := hex.DecodeString(jsonCooState.LatestMilestoneID)
	if err != nil {
		return err
	}
	cs.LatestMilestoneID = iotago.MilestoneID{}
	copy(cs.LatestMilestoneID[:], latestMilestoneIDBytes)

	cs.LatestMilestoneIndex = milestone.Index(jsonCooState.LatestMilestoneIndex)
	cs.LatestMilestoneTime = time.Unix(0, jsonCooState.LatestMilestoneTime)

	return nil
}
