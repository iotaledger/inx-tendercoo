package decoo

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"io"

	"github.com/iotaledger/inx-app/nodebridge"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/merklehasher"
)

var merkle = merklehasher.NewHasher(crypto.BLAKE2b_256)

// INXClient is a wrapper around nodebridge.NodeBridge to provide the functionality used by the coordinator.
type INXClient struct{ *nodebridge.NodeBridge }

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
func (c *INXClient) ComputeWhiteFlag(ctx context.Context, index uint32, timestamp uint32, parents iotago.BlockIDs, previousMilestoneID iotago.MilestoneID) ([]byte, []byte, error) {
	latest, err := c.LatestMilestone()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query latest milestone: %w", err)
	}

	// if the node already contains that particular milestone, query it
	if latest != nil && latest.Index >= index {
		return c.recomputeWhiteFlag(ctx, index)
	}

	req := &inx.WhiteFlagRequest{
		MilestoneIndex:      index,
		MilestoneTimestamp:  timestamp,
		Parents:             inx.NewBlockIds(parents),
		PreviousMilestoneId: inx.NewMilestoneId(previousMilestoneID),
	}
	res, err := c.Client().ComputeWhiteFlag(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	return res.GetMilestoneInclusionMerkleRoot(), res.GetMilestoneAppliedMerkleRoot(), nil
}

func (c *INXClient) recomputeWhiteFlag(ctx context.Context, index uint32) ([]byte, []byte, error) {
	req := &inx.MilestoneRequest{
		MilestoneIndex: index,
	}
	stream, err := c.Client().ReadMilestoneConeMetadata(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	// extract block IDs from milestone cone
	var includedBlockIDs, appliedBlockIDs []iotago.BlockID
	for {
		payload, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, nil, err
		}

		blockID := payload.UnwrapBlockID()
		includedBlockIDs = append(includedBlockIDs, blockID)
		// BlockMetadata_INCLUDED is set when the block contains a transaction that mutates the ledger
		if payload.GetLedgerInclusionState() == inx.BlockMetadata_LEDGER_INCLUSION_STATE_INCLUDED {
			appliedBlockIDs = append(appliedBlockIDs, blockID)
		}
	}

	return merkle.HashBlockIDs(includedBlockIDs), merkle.HashBlockIDs(appliedBlockIDs), nil
}
