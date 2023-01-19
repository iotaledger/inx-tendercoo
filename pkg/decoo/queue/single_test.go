package queue_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	"github.com/iotaledger/inx-tendercoo/pkg/decoo/queue"
)

func TestSingle_Submit(t *testing.T) {
	var a atomic.Uint32
	q := queue.NewSingle[uint32](retryInterval, func(_ context.Context, i uint32) error {
		a.Store(i)

		return nil
	})
	defer q.Stop()

	const testValue uint32 = 42
	q.Submit(testValue)
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
	require.EqualValues(t, testValue, a.Load())
}
