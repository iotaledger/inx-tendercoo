package decoo

import (
	"context"

	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

func (c *Coordinator) computeMerkleTreeHash(ctx context.Context, msIndex uint32, msTimestamp uint32, parents iotago.BlockIDs, previousMilestoneId iotago.MilestoneID) (inclMerkleProof iotago.MilestoneMerkleProof, appliedMerkleRoot iotago.MilestoneMerkleProof, err error) {
	req := &inx.WhiteFlagRequest{
		MilestoneIndex:      msIndex,
		MilestoneTimestamp:  msTimestamp,
		Parents:             inx.NewBlockIds(parents),
		PreviousMilestoneId: inx.NewMilestoneId(previousMilestoneId),
	}

	res, err := c.nodeBridge.Client().ComputeWhiteFlag(ctx, req)
	if err != nil {
		return inclMerkleProof, appliedMerkleRoot, err
	}

	copy(inclMerkleProof[:], res.GetMilestoneInclusionMerkleRoot())
	copy(appliedMerkleRoot[:], res.GetMilestoneAppliedMerkleRoot())

	return inclMerkleProof, appliedMerkleRoot, nil
}
