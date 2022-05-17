package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/iotaledger/hive.go/events"
	iotago "github.com/iotaledger/iota.go/v3"
)

// ErrAlreadyRegistered is returned when a callback for the same message ID has already been registered.
var ErrAlreadyRegistered = errors.New("message ID is already registered")

type EventRegisterer interface {
	RegisterMessageSolidEvent(context.Context, iotago.MessageID) chan struct{}
	DeregisterMessageSolidEvent(iotago.MessageID)
}

// Registry represents a convenient way to register callbacks when messages become solid.
type Registry struct {
	sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc

	registerer EventRegisterer
	registered map[iotago.MessageID]struct{}
	cleared    map[iotago.MessageID]struct{}
}

// New creates a new Registry.
func New(ctx context.Context, registerer EventRegisterer) *Registry {
	ctx, cancel := context.WithCancel(ctx)
	return &Registry{
		ctx:        ctx,
		cancel:     cancel,
		registerer: registerer,
		registered: map[iotago.MessageID]struct{}{},
		cleared:    map[iotago.MessageID]struct{}{},
	}
}

// Close closes the Registry and removes all registered callbacks without calling them.
func (r *Registry) Close() error {
	r.Lock()
	defer r.Unlock()

	r.cancel()
	r.clear()
	return nil
}

// RegisterCallback registers a callback for when a message with id becomes solid.
// If another callback for the same ID has already been registered, an error is returned.
func (r *Registry) RegisterCallback(id iotago.MessageID, f func(iotago.MessageID)) error {
	r.Lock()
	defer r.Unlock()

	if err := r.ctx.Err(); err != nil {
		return err
	}
	if _, ok := r.registered[id]; ok {
		return fmt.Errorf("%w: message %x", ErrAlreadyRegistered, id)
	}
	r.registered[id] = struct{}{}

	go func() {
		c := r.registerer.RegisterMessageSolidEvent(r.ctx, id)
		_ = events.WaitForChannelClosed(r.ctx, c)

		r.Lock()
		defer r.Unlock()

		// if the message has been cleared in the meantime, there is nothing to do
		if _, ok := r.cleared[id]; ok {
			delete(r.cleared, id)
			return
		}
		delete(r.registered, id)

		// only run the callback, if the context is not yet cancelled
		if r.ctx.Err() == nil {
			f(id)
		}
	}()
	return nil
}

// DeregisterCallback removes a previously registered callback.
func (r *Registry) DeregisterCallback(id iotago.MessageID) {
	r.Lock()
	defer r.Unlock()

	if _, ok := r.registered[id]; ok {
		r.cleared[id] = struct{}{}
		delete(r.registered, id)
		r.registerer.DeregisterMessageSolidEvent(id)
	}
}

// Clear removes all registered callbacks.
func (r *Registry) Clear() {
	r.Lock()
	defer r.Unlock()
	r.clear()
}

func (r *Registry) clear() {
	// add all registered IDs to cleared
	for msgID := range r.cleared {
		r.registered[msgID] = struct{}{}
	}
	r.cleared = r.registered
	// clear all elements of registered
	r.registered = map[iotago.MessageID]struct{}{}
	for msgID := range r.cleared {
		r.registerer.DeregisterMessageSolidEvent(msgID)
	}
}
