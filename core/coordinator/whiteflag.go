package coordinator

import (
	"context"

	"github.com/gohornet/inx-coordinator/pkg/coordinator"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

func ComputeMerkleTreeHash(ctx context.Context, msIndex uint32, msTimestamp uint32, parents iotago.BlockIDs, previousMilestoneId iotago.MilestoneID) (*coordinator.MilestoneMerkleRoots, error) {
	req := &inx.WhiteFlagRequest{
		MilestoneIndex:      msIndex,
		MilestoneTimestamp:  msTimestamp,
		Parents:             inx.NewBlockIds(parents),
		PreviousMilestoneId: inx.NewMilestoneId(previousMilestoneId),
	}

	res, err := deps.NodeBridge.Client().ComputeWhiteFlag(ctx, req)
	if err != nil {
		return nil, err
	}

	proof := &coordinator.MilestoneMerkleRoots{
		InclusionMerkleRoot: iotago.MilestoneMerkleProof{},
		AppliedMerkleRoot:   iotago.MilestoneMerkleProof{},
	}
	copy(proof.InclusionMerkleRoot[:], res.GetMilestoneInclusionMerkleRoot())
	copy(proof.AppliedMerkleRoot[:], res.GetMilestoneAppliedMerkleRoot())

	return proof, nil
}
