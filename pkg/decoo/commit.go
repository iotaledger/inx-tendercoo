package decoo

import (
	"fmt"
	"sync"
)

// CommitInfo defines commit information used by the ABCI application when committing a block.
type CommitInfo struct {
	Height int64
	Hash   []byte
}

// CommitStore provides easier handling for CommitInfo.
type CommitStore struct {
	sync.Mutex

	info CommitInfo
}

// LastCommitInfo returns the last commit information.
func (c *CommitStore) LastCommitInfo() CommitInfo {
	c.Lock()
	defer c.Unlock()

	return c.info
}

// ValidateHeight checks the given height against the last committed height.
func (c *CommitStore) ValidateHeight(height int64) error {
	info := c.LastCommitInfo()

	expectedHeight := info.Height + 1
	if height != expectedHeight {
		return fmt.Errorf("invalid height: %d; expected: %d", height, expectedHeight)
	}

	return nil
}

// Commit commits a new AppState.
func (c *CommitStore) Commit(state *AppState) {
	c.Lock()
	defer c.Unlock()

	c.info = CommitInfo{
		Height: state.blockHeader.Height,
		Hash:   state.Hash(),
	}
}
