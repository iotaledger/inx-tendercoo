package coordinator

import (
	"time"

	"github.com/iotaledger/hive.go/serializer/v2"
	iotago "github.com/iotaledger/iota.go/v3"
	builder "github.com/iotaledger/iota.go/v3/builder"
)

// createCheckpoint creates a checkpoint block.
func (coo *Coordinator) createCheckpoint(parents iotago.BlockIDs) (*iotago.Block, error) {

	iotaBlock, err := builder.
		NewBlockBuilder(coo.protoParas.Version).
		ParentsBlockIDs(parents).
		Build()
	if err != nil {
		return nil, err
	}

	// Validate
	_, err = iotaBlock.Serialize(serializer.DeSeriModePerformValidation, coo.protoParas)
	if err != nil {
		return nil, err
	}

	return iotaBlock, nil
}

// createMilestone creates a signed milestone block.
func (coo *Coordinator) createMilestone(index uint32, timestamp uint32, parents iotago.BlockIDs, receipt *iotago.ReceiptMilestoneOpt, previousMilestoneID iotago.MilestoneID, merkleProof *MilestoneMerkleRoots) (*iotago.Block, error) {
	milestoneIndexSigner := coo.signerProvider.MilestoneIndexSigner(index)
	pubKeys := milestoneIndexSigner.PublicKeys()

	confMerkleRoot := [iotago.MilestoneMerkleProofLength]byte{}
	copy(confMerkleRoot[:], merkleProof.InclusionMerkleRoot[:])
	appliedMerkleRoot := [iotago.MilestoneMerkleProofLength]byte{}
	copy(appliedMerkleRoot[:], merkleProof.AppliedMerkleRoot[:])

	msPayload := iotago.NewMilestone(index, timestamp, coo.protoParas.Version, previousMilestoneID, parents, confMerkleRoot, appliedMerkleRoot)

	if receipt != nil {
		msPayload.Opts = iotago.MilestoneOpts{receipt}
	}

	iotaBlock, err := builder.
		NewBlockBuilder(coo.protoParas.Version).
		ParentsBlockIDs(parents).
		Payload(msPayload).
		Build()
	if err != nil {
		return nil, err
	}

	if err := msPayload.Sign(pubKeys, coo.createSigningFuncWithRetries(milestoneIndexSigner.SigningFunc())); err != nil {
		return nil, err
	}

	if err = msPayload.VerifySignatures(coo.signerProvider.PublicKeysCount(), milestoneIndexSigner.PublicKeysSet()); err != nil {
		return nil, err
	}

	// Perform validation
	if _, err := iotaBlock.Serialize(serializer.DeSeriModePerformValidation, coo.protoParas); err != nil {
		return nil, err
	}

	return iotaBlock, nil
}

// wraps the given MilestoneSigningFunc into a with retries enhanced version.
func (coo *Coordinator) createSigningFuncWithRetries(signingFunc iotago.MilestoneSigningFunc) iotago.MilestoneSigningFunc {
	return func(pubKeys []iotago.MilestonePublicKey, msEssence []byte) (sigs []iotago.MilestoneSignature, err error) {
		if coo.opts.signingRetryAmount <= 0 {
			return signingFunc(pubKeys, msEssence)
		}
		for i := 0; i < coo.opts.signingRetryAmount; i++ {
			sigs, err = signingFunc(pubKeys, msEssence)
			if err != nil {
				if i+1 != coo.opts.signingRetryAmount {
					coo.LogWarnf("signing attempt failed: %s, retrying in %v, retries left %d", err, coo.opts.signingRetryTimeout, coo.opts.signingRetryAmount-(i+1))
					time.Sleep(coo.opts.signingRetryTimeout)
				}
				continue
			}
			return sigs, nil
		}
		coo.LogWarnf("signing failed after %d attempts: %s ", coo.opts.signingRetryAmount, err)
		return
	}
}
