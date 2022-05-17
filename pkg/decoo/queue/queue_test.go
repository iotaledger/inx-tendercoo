package queue_test

import (
	"errors"
	"testing"
	"time"

	"github.com/iotaledger/inx-tendercoo/pkg/decoo/queue"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"
)

var errTest = errors.New("test")

func TestSimple(t *testing.T) {
	var r atomic.Uint32
	q := queue.New(func(i interface{}) error {
		r.Store(i.(uint32))
		return nil
	})
	defer q.Stop()

	const testValue uint32 = 42
	q.Submit(0, testValue)
	require.Eventually(t, func() bool { return r.Load() == testValue }, time.Second, 10*time.Millisecond)
	require.Zero(t, q.Len())
}

func TestRetry(t *testing.T) {
	var counter atomic.Uint32
	q := queue.New(func(i interface{}) error {
		if counter.Add(1) < 4 {
			return errTest
		}
		return nil
	})
	defer q.Stop()

	q.Submit(0, struct{}{})
	require.EqualValues(t, 1, q.Len())
	require.Eventually(t, func() bool { return q.Len() == 0 }, time.Second, 10*time.Millisecond)
}

func TestReplace(t *testing.T) {
	var r atomic.Uint32
	q := queue.New(func(i interface{}) error {
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
	require.Eventually(t, func() bool { return r.Load() == testValue }, time.Second, 10*time.Millisecond)
	require.Zero(t, q.Len())
}

func TestBlocking(t *testing.T) {
	blocked := make(chan error)
	q := queue.New(func(i interface{}) error {
		t.Log(i)
		return <-blocked
	})
	defer q.Stop()

	q.Submit(0, 0)
	time.Sleep(queue.RetryInterval)
	require.EqualValues(t, 1, q.Len())

	q.Submit(1, 1)
	time.Sleep(queue.RetryInterval)
	require.EqualValues(t, 2, q.Len())

	q.Submit(2, 2)
	time.Sleep(queue.RetryInterval)
	require.EqualValues(t, 3, q.Len())

	close(blocked)
	require.Eventually(t, func() bool { return q.Len() == 0 }, time.Second, 10*time.Millisecond)
}

func TestBlockingReplace(t *testing.T) {
	blocked := make(chan error)
	q := queue.New(func(i interface{}) error {
		t.Log(i)
		return <-blocked
	})
	defer q.Stop()

	q.Submit(0, 0)
	time.Sleep(queue.RetryInterval)
	require.EqualValues(t, 1, q.Len())

	q.Submit(0, 1)
	time.Sleep(queue.RetryInterval)
	require.EqualValues(t, 1, q.Len())

	close(blocked)
	require.Eventually(t, func() bool { return q.Len() == 0 }, time.Second, 10*time.Millisecond)
}
