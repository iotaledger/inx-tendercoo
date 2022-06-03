package decoo

import (
	"context"
	"crypto"
	"encoding"
	"errors"
	"fmt"
	"io"

	"github.com/iotaledger/hornet/pkg/whiteflag"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"

	// import implementation
	_ "golang.org/x/crypto/blake2b"
)

var merkle = whiteflag.NewHasher(crypto.BLAKE2b_256)

func (c *Coordinator) computeMerkleTreeHash(ctx context.Context, msIndex uint32, msTimestamp uint32, parents iotago.BlockIDs, previousMilestoneID iotago.MilestoneID) (inclMerkleProof iotago.MilestoneMerkleProof, appliedMerkleProof iotago.MilestoneMerkleProof, err error) {
	latest, err := c.nodeBridge.LatestMilestone()
	if err != nil {
		return inclMerkleProof, appliedMerkleProof, fmt.Errorf("failed to query latest milestone: %w", err)
	}
	// if the node already contains that particular milestone, query it
	if latest != nil && latest.Milestone.Index >= msIndex {
		includedMerkleRoot, appliedMerkleRoot, err := c.recomputeWhiteFlag(ctx, msIndex)
		if err != nil {
			return inclMerkleProof, appliedMerkleProof, fmt.Errorf("failed to read milestone cone %d: %w", msIndex, err)
		}

		copy(inclMerkleProof[:], includedMerkleRoot)
		copy(appliedMerkleProof[:], appliedMerkleRoot)

		return inclMerkleProof, appliedMerkleProof, nil
	}

	req := &inx.WhiteFlagRequest{
		MilestoneIndex:      msIndex,
		MilestoneTimestamp:  msTimestamp,
		Parents:             inx.NewBlockIds(parents),
		PreviousMilestoneId: inx.NewMilestoneId(previousMilestoneID),
	}
	res, err := c.nodeBridge.Client().ComputeWhiteFlag(ctx, req)
	if err != nil {
		return inclMerkleProof, appliedMerkleProof, err
	}

	copy(inclMerkleProof[:], res.GetMilestoneInclusionMerkleRoot())
	copy(appliedMerkleProof[:], res.GetMilestoneAppliedMerkleRoot())

	return inclMerkleProof, appliedMerkleProof, nil
}

func (c *Coordinator) recomputeWhiteFlag(ctx context.Context, index uint32) ([]byte, []byte, error) {
	req := &inx.MilestoneRequest{
		MilestoneIndex: index,
	}
	stream, err := c.nodeBridge.Client().ReadMilestoneConeMetadata(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	// extract block IDs from milestone cone
	var includedBlockIDs, appliedBlockIDs []encoding.BinaryMarshaler
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
		if payload.GetLedgerInclusionState() == inx.BlockMetadata_INCLUDED {
			appliedBlockIDs = append(appliedBlockIDs, blockID)
		}
	}

	includedMerkleRoot, err := merkle.Hash(includedBlockIDs)
	if err != nil {
		panic(err)
	}
	appliedMerkleRoot, err := merkle.Hash(appliedBlockIDs)
	if err != nil {
		panic(err)
	}
	return includedMerkleRoot, appliedMerkleRoot, nil
}
