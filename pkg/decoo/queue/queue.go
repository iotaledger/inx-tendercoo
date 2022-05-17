package queue

import (
	"container/ring"
	"sync"
	"time"
)

// RetryInterval defines the time between two tries.
const RetryInterval = 100 * time.Millisecond

// The KeyedQueue holds at most one value per key and retries the execution of each such value until it succeeds.
type KeyedQueue struct {
	f func(interface{}) error

	byKey map[int]*ring.Ring
	ring  *ring.Ring
	timer *time.Timer
	mu    sync.Mutex

	shutdown chan struct{}
}

type entry struct {
	key   int
	value interface{}
	nonce uint
}

// New creates a new KeyedQueue with the execution function f.
func New(f func(interface{}) error) *KeyedQueue {
	q := &KeyedQueue{
		f:        f,
		byKey:    map[int]*ring.Ring{},
		ring:     nil,
		timer:    time.NewTimer(0),
		shutdown: make(chan struct{}),
	}
	// drain the channel and start the loop
	<-q.timer.C
	go q.loop()
	return q
}

// Stop stops the queue.
func (q *KeyedQueue) Stop() {
	close(q.shutdown)
}

// Len returns the number of values in the queue.
func (q *KeyedQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.byKey)
}

// Submit adds a new keyed value to the queue.
func (q *KeyedQueue) Submit(key int, value interface{}) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if p, has := q.byKey[key]; has {
		// increase the nonce when an entry gets updated
		p.Value = entry{key, value, p.Value.(entry).nonce + 1}
		return
	}
	if q.ring == nil {
		q.timer.Reset(0)
	}
	q.byKey[key] = q.ringInsert(entry{key, value, 0})
}

func (q *KeyedQueue) loop() {
	for {
		select {
		case <-q.shutdown:
			return
		case <-q.timer.C:
			q.process()
		}
	}
}

func (q *KeyedQueue) current() entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.ring.Value.(entry)
}

func (q *KeyedQueue) process() {
	e := q.current()
	err := q.f(e.value)

	q.mu.Lock()
	defer q.mu.Unlock()

	// if the execution failed, proceed with the next value after a short grace period
	if err != nil {
		q.ring = q.ring.Next()
		q.timer.Reset(RetryInterval)
		return
	}
	// if the value has been replaced, proceed with the next value right away
	if q.ring.Value.(entry).nonce != e.nonce {
		q.ring = q.ring.Next()
		q.timer.Reset(0)
		return
	}
	// otherwise, remove the current element from the ring and the map
	q.ringRemove(q.ring)
	delete(q.byKey, e.key)
	if q.ring != nil {
		q.timer.Reset(0)
	}
}

func (q *KeyedQueue) ringRemove(r *ring.Ring) {
	n := q.ring.Next()
	if r == q.ring {
		if n == q.ring {
			q.ring = nil
			return
		}
		q.ring = n
	}
	r.Prev().Link(n)
}

func (q *KeyedQueue) ringInsert(e entry) *ring.Ring {
	p := ring.New(1)
	p.Value = e
	if q.ring == nil {
		q.ring = p
		return p
	}
	return p.Link(q.ring)
}
