package mselection

import (
	"container/list"
	"context"
	"sync"
	"time"

	"github.com/bits-and-blooms/bitset"
	"github.com/pkg/errors"

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

	// the minimum threshold of unreferenced blocks in the heaviest branch for milestone tipselection
	// if the value falls below that threshold, no more heaviest branch tips are picked
	minHeaviestBranchUnreferencedBlocksThreshold int
	// the maximum amount of checkpoint blocks with heaviest branch tips that are picked
	// if the heaviest branch is not below "UnreferencedBlocksThreshold" before
	maxHeaviestBranchTipsPerCheckpoint int
	// the amount of checkpoint blocks with random tips that are picked if a checkpoint is issued and at least
	// one heaviest branch tip was found, otherwise no random tips will be picked
	randomTipsPerCheckpoint int
	// the maximum duration to select the heaviest branch tips
	heaviestBranchSelectionTimeout time.Duration
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

// randomTip selects a random tip item from the trackedBlocksList.
func (il *trackedBlocksList) randomTip() (*trackedBlock, error) {
	if len(il.blocks) == 0 {
		return nil, ErrNoTipsAvailable
	}

	randomBlockIndex := randomInsecure(0, len(il.blocks)-1)

	for _, tip := range il.blocks {
		randomBlockIndex--

		// if randomBlockIndex is below zero, we return the given tip
		if randomBlockIndex < 0 {
			return tip, nil
		}
	}

	return nil, ErrNoTipsAvailable
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
func New(minHeaviestBranchUnreferencedBlocksThreshold int, maxHeaviestBranchTipsPerCheckpoint int, randomTipsPerCheckpoint int, heaviestBranchSelectionTimeout time.Duration) *HeaviestSelector {
	s := &HeaviestSelector{
		minHeaviestBranchUnreferencedBlocksThreshold: minHeaviestBranchUnreferencedBlocksThreshold,
		maxHeaviestBranchTipsPerCheckpoint:           maxHeaviestBranchTipsPerCheckpoint,
		randomTipsPerCheckpoint:                      randomTipsPerCheckpoint,
		heaviestBranchSelectionTimeout:               heaviestBranchSelectionTimeout,
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

// selectTip selects a tip to be used for the next checkpoint.
// it returns a tip, confirming the most blocks in the future cone,
// and the amount of referenced blocks of this tip, that were not referenced by previously chosen tips.
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

	// select a random tip from the provided slice of tips.
	selected := best.tips[randomInsecure(0, len(best.tips)-1)]

	return selected, best.count, nil
}

// SelectTips tries to collect tips that confirm the most recent blocks since the last reset of the selector.
// best tips are determined by counting the referenced blocks (heaviest branches) and by "removing" the
// blocks of the referenced cone of the already chosen tips in the bitsets of the available tips.
// only tips are considered that were present at the beginning of the SelectTips call,
// to prevent attackers from creating heavier branches while we are searching the best tips.
// "maxHeaviestBranchTipsPerCheckpoint" is the amount of tips that are collected if
// the current best tip is not below "UnreferencedBlocksThreshold" before.
// a minimum amount of selected tips can be enforced, even if none of the heaviest branches matches the
// "minHeaviestBranchUnreferencedBlocksThreshold" criteria.
// if at least one heaviest branch tip was found, "randomTipsPerCheckpoint" random tips are added
// to add some additional randomness to prevent parasite chain attacks.
// the selection is canceled after a fixed deadline. in this case, it returns the current collected tips.
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
	ctx, cancel := context.WithTimeout(context.Background(), s.heaviestBranchSelectionTimeout)
	defer cancel()

	deadlineExceeded := false

	for i := 0; i < s.maxHeaviestBranchTipsPerCheckpoint; i++ {
		// when the context has been canceled, stop collecting heaviest branch tips
		select {
		case <-ctx.Done():
			deadlineExceeded = true
		default:
		}

		tip, count, err := s.selectTip(tipsList)
		if err != nil {
			break
		}

		if (len(tips) > minRequiredTips) && ((count < uint(s.minHeaviestBranchUnreferencedBlocksThreshold)) || deadlineExceeded) {
			// minimum amount of tips reached and the heaviest tips do not confirm enough blocks or the deadline was exceeded
			// => no need to collect more
			break
		}

		tipsList.referenceTip(tip)
		tips = append(tips, tip.blockID)
	}

	if len(tips) == 0 {
		return nil, ErrNoTipsAvailable
	}

	// also pick random tips if at least one heaviest branch tip was found
	for i := 0; i < s.randomTipsPerCheckpoint; i++ {
		item, err := tipsList.randomTip()
		if err != nil {
			break
		}

		tipsList.referenceTip(item)
		tips = append(tips, item.blockID)
	}

	// reset the whole HeaviestSelector if valid tips were found
	s.Reset()

	return tips, nil
}

// OnNewSolidBlock adds a new block to be processed by s.
// The block must be solid and OnNewSolidBlock must be called in the order of solidification.
// The block must also not be below max depth.
func (s *HeaviestSelector) OnNewSolidBlock(blockMeta *inx.BlockMetadata) (trackedBlocksCount int) {
	s.Lock()
	defer s.Unlock()

	blockID := blockMeta.UnwrapBlockID()
	parents := blockMeta.UnwrapParents()

	// filter duplicate blocks
	if _, contains := s.trackedBlocks[blockID]; contains {
		return
	}

	parentItems := []*trackedBlock{}
	for _, parent := range parents {
		parentItem := s.trackedBlocks[parent]
		if parentItem == nil {
			continue
		}

		parentItems = append(parentItems, parentItem)
	}

	// compute the referenced blocks
	// all the known children in the HeaviestSelector are represented by a unique bit in a bitset.
	// if a new child is added, we expand the bitset by 1 bit and store the Union of the bitsets
	// of the parents for this child, to know which parts of the cone are referenced by this child.
	idx := uint(len(s.trackedBlocks))
	it := &trackedBlock{blockID: blockID, refs: bitset.New(idx + 1).Set(idx)}

	for _, parentItem := range parentItems {
		it.refs.InPlaceUnion(parentItem.refs)
	}
	s.trackedBlocks[it.blockID] = it

	// update tips
	for _, parentItem := range parentItems {
		s.removeTip(parentItem)
	}

	it.tip = s.tips.PushBack(it)

	return s.TrackedBlocksCount()
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

// TrackedBlocksCount returns the amount of known blocks.
func (s *HeaviestSelector) TrackedBlocksCount() (trackedBlocksCount int) {
	return len(s.trackedBlocks)
}
