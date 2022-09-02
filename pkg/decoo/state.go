package decoo

import (
	"fmt"

	"github.com/iotaledger/hive.go/serializer/v2"
	iotago "github.com/iotaledger/iota.go/v3"
)

// State is the coordinator state that needs to be persisted.
// The State is persisted inside the Metadata field of each milestone.
// It is serialized in the following way:
//
// | Name                 | Type          | Description                                                |
// | -------------------- | ------------- | ---------------------------------------------------------- |
// | MilestoneHeight      | int64         | Block height of the milestone's first block in Tendermint. |
// | LastMilestoneBlockID | ByteArray[32] | Block ID of the previous Milestone.                        |
//
// It is not necessary to persis MilestoneIndex and LastMilestoneID as these fields are already present
// in the iotago.Milestone.
// It is therefore possible to reconstruct the State from a given milestone.
type State struct {
	// MilestoneHeight denotes the Tendermint block height when the application state was reset for this milestone.
	MilestoneHeight int64
	// MilestoneIndex denotes the index of this milestone.
	MilestoneIndex uint32
	// LastMilestoneID denotes the ID of the previous milestone.
	LastMilestoneID iotago.MilestoneID
	// LastMilestoneBlockID denotes the block ID of the previous Milestone.
	LastMilestoneBlockID iotago.BlockID
}

// Metadata returns the metadata of the state.
func (s *State) Metadata() []byte {
	// the metadata only contains the height and the last block ID, all other fields are already part of a milestone
	bytes, err := serializer.NewSerializer().
		WriteNum(s.MilestoneHeight, func(err error) error {
			return fmt.Errorf("failed to serialize milestone height: %w", err)
		}).
		WriteBytes(s.LastMilestoneBlockID[:], func(err error) error {
			return fmt.Errorf("failed to serialize last milestone block ID: %w", err)
		}).
		Serialize()
	if err != nil {
		panic(err)
	}

	return bytes
}

// NewStateFromMilestone creates the coordinator state from a given milestone.
func NewStateFromMilestone(milestone *iotago.Milestone) (*State, error) {
	var height int64
	var lastBlockID serializer.ArrayOf32Bytes
	_, err := serializer.NewDeserializer(milestone.Metadata).
		ReadNum(&height, func(err error) error {
			return fmt.Errorf("failed to deserialize milestone height: %w", err)
		}).
		ReadArrayOf32Bytes(&lastBlockID, func(err error) error {
			return fmt.Errorf("failed to deserialize last milestone block ID: %w", err)
		}).
		ConsumedAll(func(left int, err error) error {
			return fmt.Errorf("%w: %d bytes are still available", err, left)
		}).
		Done()
	if err != nil {
		return nil, fmt.Errorf("failed to desiralize metadata: %w", err)
	}

	return &State{
		MilestoneHeight:      height,
		MilestoneIndex:       milestone.Index,
		LastMilestoneID:      milestone.PreviousMilestoneID,
		LastMilestoneBlockID: lastBlockID,
	}, nil
}
