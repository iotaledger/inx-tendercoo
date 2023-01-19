package queue

import (
	"container/list"
	"context"
	"math"
	"sync"
	"time"
)

// The KeyedQueue holds at most one value per key and executes each such value one after another.
// The values will get executed in the order they have been added (FIFO). If the execution of one value fails,
// it will be pushed to the back and retried. Each value will be retried indefinitely until it succeeds or
// is replaced by a new value of the same key.
type KeyedQueue struct {
	running *call
	queue   *list.List            // the actual queue of entries
	byKey   map[any]*list.Element // referencing each queue element by its key
	len     int                   // current queue length, this includes entries that are currently processed
	timer   *time.Timer           // timer to schedule retries
	mu      sync.Mutex            // mutex protecting all the above fields

	retry time.Duration // time to wait before executing the next value after a failure
	f     CallbackFunc  // callback function when processing a value

	wg       sync.WaitGroup
	shutdown chan struct{}
}

// The CallbackFunc is the function that is executed for every value in the queue.
type CallbackFunc func(context.Context, any) error

// call represents the parameters of the currently executed function.
type call struct {
	key    any
	cancel context.CancelFunc
}

// pair represents a key-value pair.
type pair struct {
	key   any
	value any
}

// New creates a new KeyedQueue with retry denoting the duration after retrying and the execution function f.
func New(retry time.Duration, f CallbackFunc) *KeyedQueue {
	q := &KeyedQueue{
		queue:    list.New(),
		byKey:    map[any]*list.Element{},
		len:      0,
		retry:    retry,
		f:        f,
		timer:    time.NewTimer(math.MaxInt64),
		shutdown: make(chan struct{}),
	}

	// make sure that the timer is not running, since the queue is empty
	q.timer.Stop()
	// start the main loop
	q.wg.Add(1)
	go q.loop()

	return q
}

// Stop stops the queue.
// The function blocks until the current value has finished execution.
func (q *KeyedQueue) Stop() {
	close(q.shutdown)
	// cancel any running function
	q.mu.Lock()
	if q.running != nil {
		q.running.cancel()
	}
	q.mu.Unlock()

	// wait for the main loop to finish
	q.wg.Wait()
	// assure the timer is stopped
	q.timer.Stop()
}

// Len returns the number of values in the queue.
// This includes elements that are currently being executed, even if they concurrently have been replaced.
func (q *KeyedQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.len
}

// Submit adds a new keyed value to the queue.
// This overrides any previous not yet executed value with the same key.
func (q *KeyedQueue) Submit(key any, value any) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// if this is the first element, make sure that the timer triggers right away
	if q.len == 0 {
		q.timer.Reset(0)
	}

	// if we are currently executing that key, cancel the execution
	if q.running != nil && q.running.key == key {
		q.running.cancel()
	}

	// if the same key already exists, remove the corresponding element from the queue
	if p, has := q.byKey[key]; has {
		q.queue.Remove(p)
		q.len--
	}
	// add the element to the queue and assign it to the corresponding key
	q.byKey[key] = q.queue.PushBack(&pair{key, value})
	q.len++
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

// process executes the current first element in the queue.
func (q *KeyedQueue) process() {
	// do not execute anything when we are shutting down
	select {
	case <-q.shutdown:
		return
	default:
	}

	ctx, p := q.popFront()

	// make sure that the callback is executed without an acquired lock
	// this allows new vales to be submitted even during execution
	err := q.f(ctx, p.value) //nolint:ifshort // we must lock before the if-clause

	q.mu.Lock()
	defer q.mu.Unlock()

	// decrease the length after the execution is done
	q.len--
	q.running = nil

	// if the execution function failed and was not canceled, add the element back to queue and restart the timer
	if err != nil && ctx.Err() == nil {
		// only add it back to the queue if no new value with the same key was added
		if _, has := q.byKey[p.key]; !has {
			q.byKey[p.key] = q.queue.PushBack(p)
			q.len++
		}
		// at this point there will always be at least one element in the queue
		q.timer.Reset(q.retry)

		return
	}

	// if the execution was successful restart the timer for the next element, if present
	if q.len > 0 {
		q.timer.Reset(0)
	}
}

// popFront extracts the next element from the queue and prepares running.
func (q *KeyedQueue) popFront() (context.Context, *pair) {
	// create a context that can be canceled
	ctx, cancel := context.WithCancel(context.Background())

	q.mu.Lock()
	defer q.mu.Unlock()

	front := q.queue.Remove(q.queue.Front())
	p := front.(*pair) //nolint:forcetypeassert // we only add *pair to the queue
	delete(q.byKey, p.key)

	q.running = &call{p.key, cancel}

	return ctx, p
}
