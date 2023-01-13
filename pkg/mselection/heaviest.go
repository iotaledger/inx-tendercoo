//nolint:gosec // we don't care about weak random numbers here
package mselection

import (
	"container/list"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/bits-and-blooms/bitset"
	"go.uber.org/atomic"

	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

// ErrNoTipsAvailable is returned when no tips are available in the node.
var ErrNoTipsAvailable = errors.New("no tips available")

// HeaviestSelector implements the heaviest branch selection strategy.
type HeaviestSelector struct {
	sync.Mutex

	// maximum amount of tips returned by SelectTips
	maxTips int
	// timeout after which SelectTips is canceled
	timeout time.Duration
	// when the fraction of newly referenced blocks compared to the best is below this limit, SelectTips is canceled
	reducedConfirmationLimit float64

	// map of all tracked blocks
	trackedBlocks map[iotago.BlockID]*trackedBlock
	// list of available tips
	tips *list.List
}

type trackedBlock struct {
	blockID iotago.BlockID // block ID of the corresponding block
	tip     *list.Element  // pointer to the element in the tip list
	refs    *bitset.BitSet // BitSet of all the referenced blocks
}

type trackedBlocksList struct {
	blocks map[iotago.BlockID]*trackedBlock
}

// Len returns the length of the inner blocks slice.
func (il *trackedBlocksList) Len() int {
	return len(il.blocks)
}

// referenceTip removes the tip and set all bits of all referenced
// blocks of the tip in all existing tips to zero.
// this way we can track which parts of the cone would already be referenced by this tip, and
// correctly calculate the weight of the remaining tips.
func (il *trackedBlocksList) referenceTip(tip *trackedBlock) {
	il.removeTip(tip)

	// set all bits of all referenced blocks in all existing tips to zero
	for _, otherTip := range il.blocks {
		otherTip.refs.InPlaceDifference(tip.refs)
	}
}

// removeTip removes the tip from the map.
func (il *trackedBlocksList) removeTip(tip *trackedBlock) {
	delete(il.blocks, tip.blockID)
}

// New creates a new HeaviestSelector instance.
func New(maxTips int, reducedConfirmationLimit float64, timeout time.Duration) *HeaviestSelector {
	s := &HeaviestSelector{
		maxTips:                  maxTips,
		timeout:                  timeout,
		reducedConfirmationLimit: reducedConfirmationLimit,
	}
	s.Reset()

	return s
}

// NumTips returns the number of tips.
func (s *HeaviestSelector) NumTips() int {
	return s.tips.Len()
}

// TrackedBlocks returns the number of tracked blocks.
func (s *HeaviestSelector) TrackedBlocks() int {
	return len(s.trackedBlocks)
}

// Reset resets the tracked blocks map and tips list of s.
func (s *HeaviestSelector) Reset() {
	s.Lock()
	defer s.Unlock()

	// create an empty map
	s.trackedBlocks = make(map[iotago.BlockID]*trackedBlock)

	// create an empty list
	s.tips = list.New()
}

// OnNewSolidBlock adds a new block to the HeaviestSelector, it returns the total number of blocks tracked.
// The block must be solid and OnNewSolidBlock must be called in the order of solidification.
// The block must also not be below max depth.
func (s *HeaviestSelector) OnNewSolidBlock(meta *inx.BlockMetadata) int {
	s.Lock()
	defer s.Unlock()

	blockID := meta.UnwrapBlockID()

	// filter duplicate blocks
	if _, contains := s.trackedBlocks[blockID]; contains {
		return len(s.trackedBlocks)
	}

	parents := meta.UnwrapParents()
	trackedParents := make([]*trackedBlock, 0, len(parents))
	for _, parent := range parents {
		if block, ok := s.trackedBlocks[parent]; ok {
			trackedParents = append(trackedParents, block)
		}
	}

	// compute the referenced blocks
	// each known blocks in the HeaviestSelector is represented by a unique bit in a bitset
	// if a new block is added, we expand the bitset by 1 bit and store the Union of the bitsets of its parents
	idx := uint(len(s.trackedBlocks))
	it := &trackedBlock{blockID: blockID, refs: bitset.New(idx + 1).Set(idx)}
	for _, parentItem := range trackedParents {
		it.refs.InPlaceUnion(parentItem.refs)
	}
	s.trackedBlocks[it.blockID] = it

	// update tip list
	for _, parentItem := range trackedParents {
		s.removeTip(parentItem)
	}
	// store a pointer to the tip in the list
	it.tip = s.tips.PushBack(it)

	return len(s.trackedBlocks)
}

// SelectTips tries to collect tips that confirm the most recent blocks since the last call of Reset.
// The returned tips are computed using a greedy strategy, where in each state the heaviest branch, i.e. the tip
// referencing the most blocks, is selected and removed including its complete past cone.
// Only tips are considered that were present at the beginning of the SelectTips call, in order to prevent attackers
// from creating heavier branches while selection is in progress.
// The cancellation parameters unreferencedBlocksThreshold and timeout are only considered when at least
// minRequiredTips tips have been selected.
func (s *HeaviestSelector) SelectTips(minRequiredTips int) (iotago.BlockIDs, error) {
	if minRequiredTips < 1 {
		panic("HeaviestSelector: at least one tip must be required")
	}

	// create a working list with the current tips to release the lock to allow faster iteration
	// and to get a frozen view of the tangle, so an attacker can't
	// create heavier branches while we are searching the best tips
	// caution: the tips are not copied, do not mutate!
	tipsList := s.tipsToList()

	tips, err := s.selectGreedy(minRequiredTips, tipsList)
	if err != nil {
		return nil, fmt.Errorf("failed to select tips: %w", err)
	}

	if len(tips) == 0 {
		return nil, ErrNoTipsAvailable
	}

	return tips, nil
}

// selectGreedy selects the best tips greedily from tipsList.
func (s *HeaviestSelector) selectGreedy(minRequiredTips int, tipsList *trackedBlocksList) (iotago.BlockIDs, error) {
	// use a timeout for the selection to keep the view on the tangle recent
	var expired atomic.Bool
	timer := time.AfterFunc(s.timeout, func() { expired.Store(true) })
	defer timer.Stop()

	var (
		tips           iotago.BlockIDs
		lastTip        *trackedBlock
		bestReferenced uint
	)
	for i := 0; i < s.maxTips; i++ {
		// stop if the timeout was reached
		if len(tips) >= minRequiredTips && expired.Load() {
			return tips, nil
		}

		// remove the last tip from the list
		if lastTip != nil {
			tipsList.referenceTip(lastTip)
		}

		// get the tip confirming the most blocks
		tip, numReferenced, err := s.selectTip(tipsList)
		if err != nil {
			// ignore the error, if we have enough tips
			if len(tips) >= minRequiredTips {
				return tips, nil
			}

			return tips, err
		}
		if i == 0 {
			bestReferenced = numReferenced
		}

		// stop if the latest tip confirms too few transactions compared to the first
		if len(tips) >= minRequiredTips && float64(numReferenced)/float64(bestReferenced) < s.reducedConfirmationLimit {
			return tips, nil
		}

		tips = append(tips, tip.blockID)
		lastTip = tip
	}

	return tips, nil
}

// removeTip removes the tip item from s.
func (s *HeaviestSelector) removeTip(it *trackedBlock) {
	if it == nil || it.tip == nil {
		return
	}
	s.tips.Remove(it.tip)
	it.tip = nil
}

// tipsToList returns a new list containing the current tips.
func (s *HeaviestSelector) tipsToList() *trackedBlocksList {
	s.Lock()
	defer s.Unlock()

	result := make(map[iotago.BlockID]*trackedBlock)
	for e := s.tips.Front(); e != nil; e = e.Next() {
		tip, ok := e.Value.(*trackedBlock)
		if !ok {
			panic(fmt.Sprintf("invalid type: expected *trackedBlock, got %T", e.Value))
		}
		result[tip.blockID] = tip
	}

	return &trackedBlocksList{blocks: result}
}

// selectTip selects a tip to be used for the next checkpoint.
// It returns the tip referencing the most tracked blocks and the number of these blocks.
func (s *HeaviestSelector) selectTip(tipsList *trackedBlocksList) (*trackedBlock, uint, error) {
	if tipsList.Len() == 0 {
		return nil, 0, ErrNoTipsAvailable
	}

	best := struct {
		tips  []*trackedBlock
		count uint
	}{
		tips:  []*trackedBlock{},
		count: 0,
	}

	// loop through all tips and find the one with the most referenced blocks
	for _, tip := range tipsList.blocks {
		c := tip.refs.Count()
		if c > best.count {
			// tip with heavier branch found
			best.tips = []*trackedBlock{
				tip,
			}
			best.count = c
		} else if c == best.count {
			// add the tip to the slice of currently best tips
			best.tips = append(best.tips, tip)
		}
	}

	if len(best.tips) == 0 {
		return nil, 0, ErrNoTipsAvailable
	}

	// select a random tip from the set of best tips
	selected := best.tips[rand.Intn(len(best.tips))]

	return selected, best.count, nil
}
