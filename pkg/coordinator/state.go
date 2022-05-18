package coordinator

import (
	"encoding/json"
	"time"

	iotago "github.com/iotaledger/iota.go/v3"
)

// State stores the latest state of the coordinator.
type State struct {
	LatestMilestoneIndex   uint32
	LatestMilestoneBlockID iotago.BlockID
	LatestMilestoneID      iotago.MilestoneID
	LatestMilestoneTime    time.Time
}

// jsoncoostate is the JSON representation of a coordinator state.
type jsoncoostate struct {
	LatestMilestoneIndex   uint32 `json:"latestMilestoneIndex"`
	LatestMilestoneBlockID string `json:"latestMilestoneBlockID"`
	LatestMilestoneID      string `json:"latestMilestoneID"`
	LatestMilestoneTime    int64  `json:"latestMilestoneTime"`
}

func (cs *State) MarshalJSON() ([]byte, error) {
	return json.Marshal(&jsoncoostate{
		LatestMilestoneIndex:   cs.LatestMilestoneIndex,
		LatestMilestoneBlockID: cs.LatestMilestoneBlockID.ToHex(),
		LatestMilestoneID:      cs.LatestMilestoneID.ToHex(),
		LatestMilestoneTime:    cs.LatestMilestoneTime.UnixNano(),
	})
}

func (cs *State) UnmarshalJSON(data []byte) error {
	jsonCooState := &jsoncoostate{}
	if err := json.Unmarshal(data, jsonCooState); err != nil {
		return err
	}

	var err error
	cs.LatestMilestoneBlockID, err = iotago.BlockIDFromHexString(jsonCooState.LatestMilestoneBlockID)
	if err != nil {
		return err
	}

	latestMilestoneIDBytes, err := iotago.DecodeHex(jsonCooState.LatestMilestoneID)
	if err != nil {
		return err
	}
	cs.LatestMilestoneID = iotago.MilestoneID{}
	copy(cs.LatestMilestoneID[:], latestMilestoneIDBytes)

	cs.LatestMilestoneIndex = jsonCooState.LatestMilestoneIndex
	cs.LatestMilestoneTime = time.Unix(0, jsonCooState.LatestMilestoneTime)

	return nil
}
