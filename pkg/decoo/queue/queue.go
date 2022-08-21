package queue

import (
	"container/ring"
	"math"
	"sync"
	"time"
)

// RetryInterval defines the time between two tries.
const RetryInterval = 100 * time.Millisecond

// The Queue stores elements in a ring buffer. The elements are input to f in that order until they are successful.
// Queue only keeps track of the capacity most recent elements.
type Queue struct {
	capacity int
	f        func(any) error

	timer *time.Timer // timer until the next element is processed
	ring  *ring.Ring  // ring buffer of all elements
	size  int         // number of elements in the buffer
	mu    sync.Mutex

	wg       sync.WaitGroup
	shutdown chan struct{}
}

// New creates a new Queue with the execution function f.
func New(capacity int, f func(any) error) *Queue {
	if capacity < 1 {
		panic("queue: capacity must be at least 1")
	}
	q := &Queue{
		capacity: capacity,
		f:        f,
		timer:    time.NewTimer(math.MaxInt64),
		ring:     nil,
		size:     0,
		shutdown: make(chan struct{}),
	}
	q.timer.Stop() // make sure that the timer is not running
	q.wg.Add(1)
	go q.loop()

	return q
}

// Stop stops the queue.
// It blocks until the last execution function has finished.
func (q *Queue) Stop() {
	close(q.shutdown)
	q.wg.Wait()
}

// Len returns the number of elements in the queue.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.size
}

// Submit adds a element to the queue.
func (q *Queue) Submit(value any) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.size == 0 {
		q.timer.Reset(0)
	} else if q.size == q.capacity {
		q.ringPop()
	}
	q.ringPush(value)
}

func (q *Queue) loop() {
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

func (q *Queue) process() {
	q.mu.Lock()
	r := q.ring
	q.mu.Unlock()
	err := q.f(r.Value) // execute f without locking the queue

	q.mu.Lock()
	defer q.mu.Unlock()

	// if there was an error, pause the next execution
	if err != nil {
		if q.ring == r {
			q.ring = q.ring.Next() // move to the back of the queue
		}
		q.timer.Reset(RetryInterval)

		return
	}

	if q.ring == r {
		q.ringPop() // remove successful elements
	}
	if q.size > 0 {
		q.timer.Reset(0)
	}
}

func (q *Queue) ringPop() {
	q.size--
	n := q.ring.Next()
	if n == q.ring {
		q.ring = nil
		return
	}
	q.ring.Prev().Link(n)
	q.ring = n
}

func (q *Queue) ringPush(val any) {
	q.size++
	p := ring.New(1)
	p.Value = val
	if q.ring == nil {
		q.ring = p
		return
	}
	p.Link(q.ring)
}
