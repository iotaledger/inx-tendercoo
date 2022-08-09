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
	capacity = 10

	waitFor = time.Second
	tick    = 10 * time.Millisecond
)

func TestSubmit(t *testing.T) {
	sum := 0
	q := queue.New(capacity, func(v any) error {
		sum += v.(int)
		return nil
	})
	defer q.Stop()

	for i := 1; i <= capacity; i++ {
		q.Submit(i)
	}
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
	require.EqualValues(t, capacity*(capacity+1)/2, sum)
}

func TestOrder(t *testing.T) {
	var a atomic.Uint32
	q := queue.New(capacity, func(v any) error {
		i := v.(uint32)
		require.True(t, a.CAS(i-1, i))
		return nil
	})
	defer q.Stop()

	for i := uint32(1); i <= capacity; i++ {
		q.Submit(i)
	}
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
}

func TestRetry(t *testing.T) {
	counter := 0
	q := queue.New(capacity, func(any) error {
		counter++
		if counter < 3 {
			return errTest
		}
		return nil
	})
	defer q.Stop()

	q.Submit(struct{}{})
	require.EqualValues(t, 1, q.Len())
	time.Sleep(queue.RetryInterval)
	require.EqualValues(t, 1, q.Len())
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
}

func TestSkip(t *testing.T) {
	var a atomic.Uint32
	q := queue.New(capacity, func(v any) error {
		i, ok := v.(uint32)
		if !ok {
			return errTest
		}
		a.Store(i)
		return nil
	})
	defer q.Stop()

	const testValue uint32 = 42
	q.Submit(struct{}{})
	q.Submit(testValue)
	require.Eventually(t, func() bool { return a.Load() == testValue }, waitFor, tick)
	require.EqualValues(t, 1, q.Len())
}

func TestCapacity(t *testing.T) {
	started := make(chan struct{}, capacity)
	wait := make(chan struct{})
	q := queue.New(capacity, func(v any) error {
		started <- struct{}{}
		<-wait
		require.True(t, v.(bool))
		return nil
	})
	defer q.Stop()

	q.Submit(true)
	<-started // assure that the first element is being executed
	for i := 1; i < capacity; i++ {
		q.Submit(false)
	}
	for i := 0; i < capacity; i++ {
		q.Submit(true)
	}
	require.EqualValues(t, capacity, q.Len())
	close(wait)
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
}

func TestSubmitWhileExecuting(t *testing.T) {
	started := make(chan struct{})
	wait := make(chan struct{})
	q := queue.New(capacity, func(v any) error {
		if v.(bool) {
			close(started)
		}
		<-wait
		return nil
	})
	defer q.Stop()

	q.Submit(true)
	<-started
	q.Submit(false)
	require.EqualValues(t, 2, q.Len())
	close(wait)
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
}

func TestConcurrentSubmit(t *testing.T) {
	const numThreads = 10
	q := queue.New(numThreads*capacity, func(any) error { return errTest })
	defer q.Stop()

	var wg sync.WaitGroup
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < capacity; j++ {
				q.Submit(0)
			}
		}()
	}
	wg.Wait()

	require.EqualValues(t, numThreads*capacity, q.Len())
}
