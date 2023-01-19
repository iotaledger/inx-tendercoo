package queue

import (
	"context"
	"time"
)

// Single represents a KeyedQueue where all values have the same key.
type Single[V any] struct{ *KeyedQueue }

// NewSingle creates a new KeyedQueue with retry denoting the duration after retrying and the execution function f.
func NewSingle[V any](retry time.Duration, f func(context.Context, V) error) *Single[V] {
	return &Single[V]{New(retry, func(ctx context.Context, a any) error {
		//nolint:forcetypeassert // we only submit V into the queue
		return f(ctx, a.(V))
	})}
}

// Submit adds a new value.
// This overrides any previous not yet executed value.
// When a value is currently executed its context will be canceled.
func (s *Single[V]) Submit(value V) {
	s.KeyedQueue.Submit(struct{}{}, value)
}
