package mselection

import (
	"container/list"
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/bits-and-blooms/bitset"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
)

var (
	// ErrNoTipsAvailable is returned when no tips are available in the node.
	ErrNoTipsAvailable = errors.New("no tips available")
)

// HeaviestSelector implements the heaviest branch selection strategy.
type HeaviestSelector struct {
	sync.Mutex

	// maximum amount of tips returned by SelectTips
	maxTips int
	// timeout after which SelectTips is cancelled
	timeout time.Duration
	// when the fraction of newly referenced blocks compared to the best is below this limit, SelectTips is cancelled
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

	var trackedParents []*trackedBlock
	for _, parent := range meta.UnwrapParents() {
		trackedParent := s.trackedBlocks[parent]
		if trackedParent == nil {
			continue
		}
		trackedParents = append(trackedParents, trackedParent)
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
	// create a working list with the current tips to release the lock to allow faster iteration
	// and to get a frozen view of the tangle, so an attacker can't
	// create heavier branches while we are searching the best tips
	// caution: the tips are not copied, do not mutate!
	tipsList := s.tipsToList()

	// tips could be empty after a reset
	if tipsList.Len() == 0 {
		return nil, ErrNoTipsAvailable
	}

	var tips iotago.BlockIDs

	// run the tip selection for at most 0.1s to keep the view on the tangle recent; this should be plenty
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	var first uint
	for i := 0; i < s.maxTips; i++ {
		select {
		case <-ctx.Done():
			// stop if we already have sufficient number of tips
			if len(tips) > minRequiredTips {
				break
			}
		default:
		}

		tip, count, err := s.selectTip(tipsList)
		if err != nil {
			break
		}
		if i == 0 {
			first = count
		}

		// stop if the latest tip confirms too few transactions compared to the first
		if len(tips) > minRequiredTips && float64(count)/float64(first) < s.reducedConfirmationLimit {
			break
		}

		tipsList.referenceTip(tip)
		tips = append(tips, tip.blockID)
	}

	if len(tips) == 0 {
		return nil, ErrNoTipsAvailable
	}

	// reset the whole HeaviestSelector if valid tips were found
	s.Reset()

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
		tip := e.Value.(*trackedBlock)
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

	var best = struct {
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
