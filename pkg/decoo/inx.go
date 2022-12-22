package decoo

import (
	"context"
	"time"

	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

// INXTimeout defines the timeout after which INX API calls are canceled.
const INXTimeout = 2 * time.Second

func (c *Coordinator) inxLatestMilestone() (*iotago.Milestone, error) {
	return c.inxClient.LatestMilestone()
}

func (c *Coordinator) inxLatestMilestoneIndex() uint32 {
	return c.inxClient.LatestMilestoneIndex()
}

func (c *Coordinator) inxBlockMetadata(blockID iotago.BlockID) (*inx.BlockMetadata, error) {
	ctx, cancel := context.WithTimeout(c.ctx, INXTimeout)
	defer cancel()

	return c.inxClient.BlockMetadata(ctx, blockID)
}

func (c *Coordinator) inxSubmitBlock(block *iotago.Block) (iotago.BlockID, error) {
	ctx, cancel := context.WithTimeout(c.ctx, INXTimeout)
	defer cancel()

	return c.inxClient.SubmitBlock(ctx, block)
}

func (c *Coordinator) inxComputeWhiteFlag(index uint32, ts uint32, parents iotago.BlockIDs, lastID iotago.MilestoneID) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(c.ctx, c.whiteFlagTimeout)
	defer cancel()

	return c.inxClient.ComputeWhiteFlag(ctx, index, ts, parents, lastID)
}

func (c *Coordinator) inxRegisterBlockSolidCallback(blockID iotago.BlockID, f func(*inx.BlockMetadata)) error {
	ctx, cancel := context.WithTimeout(c.ctx, INXTimeout)
	defer cancel()

	return c.listener.RegisterBlockSolidCallback(ctx, blockID, f)
}

func (c *Coordinator) inxClearBlockSolidCallbacks() {
	c.listener.ClearBlockSolidCallbacks()
}
