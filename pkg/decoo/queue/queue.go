package queue

import (
	"container/ring"
	"math"
	"sync"
	"time"
)

// RetryInterval defines the time between two tries.
const RetryInterval = 100 * time.Millisecond

// The KeyedQueue holds at most one value per key and retries the execution of each such key-pair until it succeeds.
type KeyedQueue struct {
	f func(any) error

	byKey map[any]*ring.Ring
	ring  *ring.Ring
	timer *time.Timer
	mu    sync.Mutex

	wg       sync.WaitGroup
	shutdown chan struct{}
}

type entry struct {
	key   any
	value any
}

// New creates a new KeyedQueue with the execution function f.
func New(f func(any) error) *KeyedQueue {
	q := &KeyedQueue{
		f:        f,
		byKey:    map[any]*ring.Ring{},
		ring:     nil,
		timer:    time.NewTimer(math.MaxInt64),
		shutdown: make(chan struct{}),
	}
	q.timer.Stop() // make sure that the timer is not running
	q.wg.Add(1)
	go q.loop()

	return q
}

// Stop stops the queue.
func (q *KeyedQueue) Stop() {
	close(q.shutdown)
	q.wg.Wait()
}

// Len returns the number of values in the queue.
func (q *KeyedQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return len(q.byKey)
}

// Submit adds a new keyed value to the queue.
func (q *KeyedQueue) Submit(key any, value any) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if p, has := q.byKey[key]; has {
		// this updates the value in q.ring as well.
		p.Value = &entry{key, value}

		return
	}
	if q.ring == nil {
		q.timer.Reset(0)
	}
	q.byKey[key] = q.ringPush(&entry{key, value})
}

func (q *KeyedQueue) loop() {
	defer q.wg.Done()
	for {
		select {
		case <-q.timer.C:
			q.process()
		case <-q.shutdown:
			return
		}
	}
}

func (q *KeyedQueue) current() *entry {
	q.mu.Lock()
	defer q.mu.Unlock()

	//nolint:forcetypeassert // we only submit *entry into the ring
	return q.ring.Value.(*entry)
}

func (q *KeyedQueue) process() {
	e := q.current()

	//nolint:ifshort // false positive
	err := q.f(e.value)

	q.mu.Lock()
	defer q.mu.Unlock()

	// if there was an error, proceed with the next value after a short grace period
	if err != nil {
		q.ring = q.ring.Next()
		q.timer.Reset(RetryInterval)

		return
	}
	// if the value has been replaced, proceed with the next value right away
	if q.byKey[e.key].Value != any(e) {
		q.ring = q.ring.Next()
		q.timer.Reset(0)

		return
	}
	// otherwise, remove the current element from the ring and the map
	q.ringPop()
	delete(q.byKey, e.key)
	if q.ring != nil {
		q.timer.Reset(0)
	}
}

// ATTENTION: the lock must be acquired outside.
func (q *KeyedQueue) ringPop() {
	n := q.ring.Next()
	if n == q.ring {
		q.ring = nil

		return
	}
	q.ring.Prev().Link(n)
	q.ring = n
}

// ATTENTION: the lock must be acquired outside.
func (q *KeyedQueue) ringPush(e *entry) *ring.Ring {
	p := ring.New(1)
	p.Value = e
	if q.ring == nil {
		q.ring = p

		return p
	}

	return p.Link(q.ring)
}
