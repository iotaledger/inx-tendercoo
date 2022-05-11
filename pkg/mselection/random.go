package mselection

import (
	"math/rand"
	"time"

	"github.com/iotaledger/hive.go/syncutils"
)

var (
	seededRand = rand.New(rand.NewSource(time.Now().UnixNano()))
	randLock   = &syncutils.Mutex{}
)

// randomInsecure returns a random int in the range of min to max.
// the result is not cryptographically secure.
// randomInsecure is inclusive max value.
func randomInsecure(min int, max int) int {
	// Rand needs to be locked: https://github.com/golang/go/issues/3611
	randLock.Lock()
	defer randLock.Unlock()
	return seededRand.Intn(max+1-min) + min
}
