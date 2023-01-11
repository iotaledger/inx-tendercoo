//nolint:forcetypeassert // we don't care about these linters in test cases
package queue_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	"github.com/iotaledger/inx-tendercoo/pkg/decoo/queue"
)

var errTest = errors.New("test")

const (
	waitFor = time.Second
	tick    = 10 * time.Millisecond
)

func TestSimple(t *testing.T) {
	var r atomic.Uint32
	q := queue.New(func(i any) error {
		r.Store(i.(uint32))

		return nil
	})
	defer q.Stop()

	const testValue uint32 = 42
	q.Submit(0, testValue)
	require.Eventually(t, func() bool { return r.Load() == testValue }, waitFor, tick)
	require.Zero(t, q.Len())
}

func TestRetry(t *testing.T) {
	var counter atomic.Uint32
	q := queue.New(func(i any) error {
		if counter.Add(1) < 4 {
			return errTest
		}

		return nil
	})
	defer q.Stop()

	q.Submit(0, struct{}{})
	require.EqualValues(t, 1, q.Len())
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
}

func TestReplace(t *testing.T) {
	var r atomic.Uint32
	q := queue.New(func(i any) error {
		v := i.(uint32)
		r.Store(v)
		if v < 2 {
			return errTest
		}

		return nil
	})
	defer q.Stop()

	q.Submit(0, uint32(1))
	time.Sleep(2 * queue.RetryInterval)
	require.EqualValues(t, 1, r.Load())
	require.EqualValues(t, 1, q.Len())

	const testValue uint32 = 42
	q.Submit(0, testValue)
	require.Eventually(t, func() bool { return r.Load() == testValue }, waitFor, tick)
	require.Zero(t, q.Len())
}

func TestOrder(t *testing.T) {
	blocked := make(chan struct{})
	counter := 0
	q := queue.New(func(i any) error {
		t.Log(i)
		<-blocked
		if counter == 0 {
			counter++

			return errTest
		}
		require.EqualValues(t, counter, i)
		counter++

		return nil
	})
	defer q.Stop()

	q.Submit(0, 3)
	q.Submit(1, 1)
	q.Submit(2, 2)

	close(blocked)
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
}

func TestSubmitWhileExecuting(t *testing.T) {
	started := make(chan struct{})
	wait := make(chan struct{})
	q := queue.New(func(v any) error {
		if v.(bool) {
			close(started)
		}
		<-wait

		return nil
	})
	defer q.Stop()

	q.Submit(0, true)
	<-started
	q.Submit(1, false)
	require.EqualValues(t, 2, q.Len())
	close(wait)
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
}

func TestReplaceWhileExecuting(t *testing.T) {
	started := make(chan struct{})
	wait := make(chan struct{})
	counter := 0
	q := queue.New(func(v any) error {
		if v.(bool) {
			close(started)
		}
		<-wait
		counter++

		return nil
	})
	defer q.Stop()

	q.Submit(0, true)
	<-started
	q.Submit(0, false)
	require.EqualValues(t, 1, q.Len())
	close(wait)
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
	require.EqualValues(t, 2, counter)
}

func TestConcurrentSubmit(t *testing.T) {
	const numThreads = 10
	const capacity = 10
	q := queue.New(func(any) error { return errTest })
	defer q.Stop()

	var wg sync.WaitGroup
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < capacity; j++ {
				q.Submit(j, 0)
			}
		}()
	}
	wg.Wait()

	require.EqualValues(t, capacity, q.Len())
}
