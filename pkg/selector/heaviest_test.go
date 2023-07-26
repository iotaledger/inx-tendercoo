//nolint:gosec // we don't care about these linters in test cases
package selector_test

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/iotaledger/inx-tendercoo/pkg/selector"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/tpkg"
)

const (
	TestMaxTips                  = 10
	TestReducedConfirmationLimit = 1
	TestTimeout                  = 100 * time.Millisecond

	numTestBlocks      = 32 * 100
	numBenchmarkBlocks = 5000
)

func init() {
	//nolint: staticcheck, (mathrand.Seed is deprecated)
	rand.Seed(0)
}

func newHPS() *selector.Heaviest {
	return selector.NewHeaviest(TestMaxTips, TestReducedConfirmationLimit, TestTimeout)
}

func newMetadata(parents iotago.BlockIDs) (*inx.BlockMetadata, iotago.BlockID) {
	blockID := tpkg.Rand32ByteArray()

	return &inx.BlockMetadata{
		BlockId: inx.NewBlockId(blockID),
		Parents: inx.NewBlockIds(parents),
		Solid:   true,
	}, blockID
}

func TestHeaviestSelector_Reset(t *testing.T) {
	hps := newHPS()
	// add a single block
	metadata, _ := newMetadata(iotago.BlockIDs{iotago.EmptyBlockID()})
	hps.OnNewSolidBlock(metadata)

	assert.Equal(t, 1, hps.TrackedBlocks())
	assert.Equal(t, 1, hps.NumTips())

	hps.Reset()
	assert.Zero(t, hps.TrackedBlocks())
	assert.Zero(t, hps.NumTips())
}

func TestHeaviestSelector_SelectTipsChain(t *testing.T) {
	hps := newHPS()
	// create a chain
	lastBlockID := iotago.EmptyBlockID()
	for i := 1; i <= numTestBlocks; i++ {
		metadata, blockID := newMetadata(iotago.BlockIDs{lastBlockID})
		hps.OnNewSolidBlock(metadata)
		lastBlockID = blockID
	}
	assert.Equal(t, numTestBlocks, hps.TrackedBlocks())
	assert.Equal(t, 1, hps.NumTips())

	tips, err := hps.SelectTips(context.Background(), 1)
	assert.NoError(t, err)
	assert.Len(t, tips, 1)

	// check if the tip on top was picked
	assert.ElementsMatch(t, lastBlockID, tips[0])
}

func TestHeaviestSelector_CheckTipsRemoved(t *testing.T) {
	const numBlocks = 8

	hps := newHPS()
	blockIDs := make(iotago.BlockIDs, numBlocks)
	var numTrackedBlocks int
	for i := 0; i < numBlocks; i++ {
		metadata, blockID := newMetadata(iotago.BlockIDs{iotago.EmptyBlockID()})
		numTrackedBlocks = hps.OnNewSolidBlock(metadata)
		blockIDs[i] = blockID
	}

	// check if all blocks have been tracked
	assert.Equal(t, numBlocks, numTrackedBlocks)
	assert.Equal(t, numBlocks, hps.TrackedBlocks())

	// check if the current tips match the current count
	assert.EqualValues(t, numBlocks, hps.NumTips())

	// issue a new block that references the old ones
	metadata, blockID := newMetadata(blockIDs)
	// old tracked blockIDs should remain, plus the new one
	assert.Equal(t, numBlocks+1, hps.OnNewSolidBlock(metadata))

	// all old tips should be removed, except the new one
	assert.EqualValues(t, 1, hps.NumTips())

	// select a tip
	tips, err := hps.SelectTips(context.Background(), 1)
	assert.NoError(t, err)
	assert.Len(t, tips, 1)

	// check if the tip on top was picked
	assert.ElementsMatch(t, blockID, tips[0])
}

func TestHeaviestSelector_SelectTipsChains(t *testing.T) {
	const numChains = 2

	hps := newHPS()
	lastBlockIDs := make(iotago.BlockIDs, 2)
	var numTrackedBlocks int
	for i := 0; i < numChains; i++ {
		lastBlockIDs[i] = iotago.EmptyBlockID()
		for j := 1; j <= numTestBlocks; j++ {
			metadata, blockID := newMetadata(iotago.BlockIDs{lastBlockIDs[i]})
			numTrackedBlocks = hps.OnNewSolidBlock(metadata)
			lastBlockIDs[i] = blockID
		}
	}

	// check if all blocks have been tracked
	assert.Equal(t, numChains*numTestBlocks, numTrackedBlocks)
	assert.Equal(t, numChains*numTestBlocks, hps.TrackedBlocks())

	tips, err := hps.SelectTips(context.Background(), 2)
	assert.NoError(t, err)
	assert.Len(t, tips, 2)

	// check if the tips on top of both branches were picked
	assert.ElementsMatch(t, lastBlockIDs, tips)
}

func BenchmarkHeaviestSelector_OnNewSolidBlock(b *testing.B) {
	hps := newHPS()

	blockIDs := iotago.BlockIDs{iotago.EmptyBlockID()}
	blocks := make([]*inx.BlockMetadata, numBenchmarkBlocks)
	for i := 0; i < numBenchmarkBlocks; i++ {
		tipCount := 1 + rand.Intn(7)
		if tipCount > len(blockIDs) {
			tipCount = len(blockIDs)
		}
		tips := make(iotago.BlockIDs, tipCount)
		for j := 0; j < tipCount; j++ {
			tips[j] = blockIDs[rand.Intn(len(blockIDs))]
		}
		tips = tips.RemoveDupsAndSort()

		blocks[i], blockIDs[i] = newMetadata(tips)
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		hps.OnNewSolidBlock(blocks[i%numBenchmarkBlocks])
	}
	hps.Reset()
}
