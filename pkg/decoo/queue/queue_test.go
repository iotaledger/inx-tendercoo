//nolint:forcetypeassert // we don't care about these linters in test cases
package queue_test

import (
	"context"
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
	retryInterval = 100 * time.Millisecond

	waitFor = time.Second
	tick    = 10 * time.Millisecond
)

func TestSubmit(t *testing.T) {
	var a atomic.Uint32
	q := queue.New(retryInterval, func(_ context.Context, i any) error {
		a.Store(i.(uint32))

		return nil
	})
	defer q.Stop()

	const testValue uint32 = 42
	q.Submit(0, testValue)
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
	require.EqualValues(t, testValue, a.Load())
}

func TestRetry(t *testing.T) {
	counter := 0
	q := queue.New(retryInterval, func(context.Context, any) error {
		counter++
		if counter < 3 {
			return errTest
		}

		return nil
	})
	defer q.Stop()

	q.Submit(0, struct{}{})
	require.EqualValues(t, 1, q.Len())
	time.Sleep(retryInterval)
	require.EqualValues(t, 1, q.Len())
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
}

func TestReplace(t *testing.T) {
	counter := 0
	q := queue.New(retryInterval, func(_ context.Context, v any) error {
		i, ok := v.(int)
		if !ok {
			return errTest
		}
		counter += i

		return nil
	})
	defer q.Stop()

	q.Submit(0, struct{}{})
	time.Sleep(retryInterval)
	require.EqualValues(t, 1, q.Len())

	const testValue = 42
	q.Submit(0, testValue)
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
	require.EqualValues(t, testValue, counter)
}

func TestOrder(t *testing.T) {
	blocked := make(chan struct{})
	counter := 0
	q := queue.New(retryInterval, func(_ context.Context, i any) error {
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
	executed := make(chan struct{})
	barrier := make(chan struct{})
	counter := 0
	q := queue.New(retryInterval, func(_ context.Context, v any) error {
		// close the channel on the first execution
		select {
		case <-executed:
		default:
			close(executed)
		}

		<-barrier
		counter += v.(int)

		return nil
	})
	defer q.Stop()

	q.Submit(0, 1)
	// wait until the first execution has started
	<-executed
	q.Submit(1, 1)
	require.EqualValues(t, 2, q.Len())
	// allow all executions to finish
	close(barrier)
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
	require.EqualValues(t, 2, counter)
}

func TestReplaceWhileExecuting(t *testing.T) {
	executed := make(chan struct{})
	barrier := make(chan struct{})
	counter := 0
	q := queue.New(retryInterval, func(_ context.Context, v any) error {
		// close the channel on the first execution
		select {
		case <-executed:
		default:
			close(executed)
		}

		<-barrier
		i, ok := v.(int)
		if !ok {
			return errTest
		}
		counter += i

		return nil
	})
	defer q.Stop()

	q.Submit(0, struct{}{})
	// wait until the first execution has started
	<-executed
	q.Submit(0, 1)
	q.Submit(1, 1)
	require.EqualValues(t, 3, q.Len())
	// allow all executions to finish
	close(barrier)
	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
	require.EqualValues(t, 2, counter)
}

func TestWaitSingle(t *testing.T) {
	executed := make(chan struct{})
	barrier := make(chan struct{})
	counter := 0
	q := queue.New(retryInterval, func(context.Context, any) error {
		// close the channel on the first execution
		select {
		case <-executed:
		default:
			close(executed)
		}
		<-barrier
		counter++

		return nil
	})

	q.Submit(0, struct{}{})
	// wait until the first execution has started
	<-executed
	q.Submit(0, struct{}{})
	q.Submit(0, struct{}{})

	close(barrier)

	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
	require.EqualValues(t, 2, counter)
}

func TestCancelRunning(t *testing.T) {
	executed := make(chan struct{})
	counter := 0
	q := queue.New(retryInterval, func(ctx context.Context, v any) error {
		// close the channel on the first execution
		select {
		case <-executed:
		default:
			close(executed)
		}

		// for the first value, we wait until cancellation
		if first := v.(bool); first {
			<-ctx.Done()

			return ctx.Err()
		}
		counter++

		return nil
	})

	q.Submit(0, true)
	// wait until the first execution has started
	<-executed
	q.Submit(0, false)

	require.Eventually(t, func() bool { return q.Len() == 0 }, waitFor, tick)
	require.EqualValues(t, 1, counter)
}

func TestConcurrentSubmit(_ *testing.T) {
	const numThreads = 10
	const capacity = 10
	q := queue.New(retryInterval, func(context.Context, any) error { return errTest })
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
}
