package nodebridge

import (
	iotago "github.com/iotaledger/iota.go/v3"
)

// LatestMilestoneInfo contains the info of the latest milestone the connected node knows.
type LatestMilestoneInfo struct {
	Index       uint32
	Timestamp   uint32
	MilestoneID iotago.MilestoneID
}

// LatestTreasuryOutput represents the latest treasury output created by the last milestone that contained a migration
type LatestTreasuryOutput struct {
	MilestoneID iotago.MilestoneID
	Amount      uint64
}

// MilestoneMerkleRoots contains the merkle roots calculated by whiteflag confirmation.
type MilestoneMerkleRoots struct {
	// InclusionMerkleRoot is the root of the merkle tree containing the hash of all included blocks.
	InclusionMerkleRoot iotago.MilestoneMerkleProof
	// AppliedMerkleRoot is the root of the merkle tree containing the hash of all include blocks that mutate the ledger.
	AppliedMerkleRoot iotago.MilestoneMerkleProof
}
