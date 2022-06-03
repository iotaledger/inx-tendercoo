package decoo

import (
	"fmt"

	"github.com/iotaledger/hive.go/serializer/v2"
	iotago "github.com/iotaledger/iota.go/v3"
)

// State is the coordinator state that needs to be persisted.
type State struct {
	// MilestoneHeight denotes the Tendermint height of the first block for this milestone.
	MilestoneHeight int64
	// MilestoneIndex denotes the index of this milestone.
	MilestoneIndex uint32
	// LastMilestoneID denotes the ID of the previous milestone.
	LastMilestoneID iotago.MilestoneID
	// LastMilestoneBlockID denotes the ID of a block containing the previous milestone.
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
