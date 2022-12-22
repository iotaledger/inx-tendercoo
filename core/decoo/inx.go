package decoo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/iotaledger/inx-app/nodebridge"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

// ErrInvalidIndex is returned when the provided milestone index is invalid.
var ErrInvalidIndex = errors.New("invalid milestone index")

// INXClient is a wrapper around nodebridge.NodeBridge to provide the functionality used by the coordinator.
type INXClient struct{ *nodebridge.NodeBridge }

// This must match the hornet error when the index of ComputeWhiteFlag is invalid.
const errTextComputeWhiteFlagInvalidIndex = "node is not synchronized"

// INXClient must implement the corresponding interface.
var _ decoo.INXClient = (*INXClient)(nil)

// LatestMilestone returns the latest milestone received by the node.
func (c *INXClient) LatestMilestone() (*iotago.Milestone, error) {
	latest, err := c.NodeBridge.LatestMilestone()
	if err != nil {
		return nil, err
	}

	if latest != nil {
		return latest.Milestone, nil
	}

	//nolint:nilnil // nil, nil is ok in this context, even if it is not go idiomatic
	return nil, nil
}

// ComputeWhiteFlag returns the white-flag merkle tree hashes for the corresponding milestone.
// The caller needs to make sure that parents are all solid.
func (c *INXClient) ComputeWhiteFlag(ctx context.Context, index uint32, timestamp uint32, parents iotago.BlockIDs, previousMilestoneID iotago.MilestoneID) ([]byte, []byte, error) {
	cmi := c.ConfirmedMilestoneIndex()
	if index > cmi+1 {
		return nil, nil, ErrInvalidIndex
	}

	// for a past milestone we don't need to compute anything and can query the existing information
	if cmi > 0 && index <= cmi {
		return c.queryWhiteFlag(ctx, index, parents)
	}

	req := &inx.WhiteFlagRequest{
		MilestoneIndex:      index,
		MilestoneTimestamp:  timestamp,
		Parents:             inx.NewBlockIds(parents),
		PreviousMilestoneId: inx.NewMilestoneId(previousMilestoneID),
	}
	res, err := c.Client().ComputeWhiteFlag(ctx, req)
	if err != nil {
		// there could be a race condition, where ComputeWhiteFlag fails as the cmi got updated in the meantime
		// in this case, we check for that particular error message and query
		if strings.Contains(err.Error(), errTextComputeWhiteFlagInvalidIndex) {
			return c.queryWhiteFlag(ctx, index, parents)
		}

		return nil, nil, fmt.Errorf("failed to query ComputeWhiteFlag: %w", err)
	}

	return res.GetMilestoneInclusionMerkleRoot(), res.GetMilestoneAppliedMerkleRoot(), nil
}

func (c *INXClient) queryWhiteFlag(ctx context.Context, index uint32, parents iotago.BlockIDs) ([]byte, []byte, error) {
	ms, err := c.Milestone(ctx, index)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query milestone %d: %w", index, err)
	}
	// do a sanity check for the milestone parents
	if !equal(ms.Milestone.Parents, parents) {
		return nil, nil, fmt.Errorf("parents to not match milestone %d", index)
	}

	return ms.Milestone.InclusionMerkleRoot[:], ms.Milestone.AppliedMerkleRoot[:], nil
}

func equal(a, b iotago.BlockIDs) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
