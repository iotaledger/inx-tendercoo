package coordinator

import (
	iotago "github.com/iotaledger/iota.go/v3"
)

// CheckpointCaller is used to signal issued checkpoints.
func CheckpointCaller(handler interface{}, params ...interface{}) {
	handler.(func(checkpointIndex int, tipIndex int, tipsTotal int, blockID iotago.BlockID))(params[0].(int), params[1].(int), params[2].(int), params[3].(iotago.BlockID))
}

// MilestoneCaller is used to signal issued milestones.
func MilestoneCaller(handler interface{}, params ...interface{}) {
	handler.(func(index uint32, milestoneID iotago.MilestoneID, blockID iotago.BlockID))(params[0].(uint32), params[1].(iotago.MilestoneID), params[2].(iotago.BlockID))
}

// QuorumFinishedCaller is used to signal a finished quorum call.
func QuorumFinishedCaller(handler interface{}, params ...interface{}) {
	handler.(func(result *QuorumFinishedResult))(params[0].(*QuorumFinishedResult))
}
