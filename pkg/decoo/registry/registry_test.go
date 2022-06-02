package registry_test

import (
	"context"
	"testing"
	"time"

	"github.com/iotaledger/inx-tendercoo/pkg/decoo/registry"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"
)

var testId = iotago.BlockID{42}

type NodeBridgeMock struct{ mock.Mock }

func (m *NodeBridgeMock) RegisterBlockSolidEvent(id iotago.BlockID) chan struct{} {
	return m.Called(id).Get(0).(chan struct{})
}

func (m *NodeBridgeMock) DeregisterBlockSolidEvent(id iotago.BlockID) {
	m.Called(id)
}

func TestNew(t *testing.T) {
	o := &NodeBridgeMock{}
	r := registry.New(o)
	require.NoError(t, r.Close())
	o.AssertExpectations(t)
}

func TestRegistry_RegisterCallback(t *testing.T) {
	o := &NodeBridgeMock{}
	c := make(chan struct{})
	o.On("RegisterBlockSolidEvent", testId).Return(c).Once()

	r := registry.New(o)

	var called atomic.Bool
	require.NoError(t, r.RegisterCallback(testId, func(iotago.BlockID) { called.Store(true) }))
	require.ErrorIs(t, r.RegisterCallback(testId, func(iotago.BlockID) {}), registry.ErrAlreadyRegistered)
	require.Never(t, called.Load, 500*time.Millisecond, 10*time.Millisecond)
	close(c)
	require.Eventually(t, called.Load, 500*time.Millisecond, 10*time.Millisecond)

	require.NoError(t, r.Close())
	o.AssertExpectations(t)
}

func TestRegistry_DeregisterCallback(t *testing.T) {
	o := &NodeBridgeMock{}
	c := make(chan struct{})
	o.On("RegisterBlockSolidEvent", testId).Return(c).Once()
	o.On("DeregisterBlockSolidEvent", testId).Run(func(mock.Arguments) { close(c) }).Once()

	r := registry.New(o)

	var called atomic.Bool
	require.NoError(t, r.RegisterCallback(testId, func(iotago.BlockID) { called.Store(true) }))
	require.Never(t, called.Load, 500*time.Millisecond, 10*time.Millisecond)

	r.DeregisterCallback(testId)
	require.Never(t, called.Load, 500*time.Millisecond, 10*time.Millisecond)

	require.NoError(t, r.Close())
	o.AssertExpectations(t)
}

func TestRegistry_Clear(t *testing.T) {
	o := &NodeBridgeMock{}

	closed := make(chan struct{})
	close(closed)
	o.On("RegisterBlockSolidEvent", iotago.BlockID{}).Return(closed).Once()

	testChan := make(chan struct{})
	o.On("RegisterBlockSolidEvent", testId).Return(testChan).Twice()
	o.On("DeregisterBlockSolidEvent", testId).Once().Run(func(mock.Arguments) {
		close(testChan)
	})

	r := registry.New(o)

	require.NoError(t, r.RegisterCallback(iotago.BlockID{}, func(iotago.BlockID) {}))
	var called atomic.Bool
	require.NoError(t, r.RegisterCallback(testId, func(iotago.BlockID) { called.Store(true) }))
	time.Sleep(500 * time.Millisecond)

	r.Clear()
	require.Never(t, called.Load, 500*time.Millisecond, 10*time.Millisecond)

	require.NoError(t, r.RegisterCallback(testId, func(iotago.BlockID) { called.Store(true) }))
	require.Eventually(t, called.Load, 500*time.Millisecond, 10*time.Millisecond)

	require.NoError(t, r.Close())
	o.AssertExpectations(t)
}

func TestRegistry_Close(t *testing.T) {
	o := &NodeBridgeMock{}
	o.On("RegisterBlockSolidEvent", testId).Return(chan struct{}(nil)).Once()
	o.On("DeregisterBlockSolidEvent", testId).Once()

	r := registry.New(o)

	var called atomic.Bool
	require.NoError(t, r.RegisterCallback(testId, func(iotago.BlockID) { called.Store(true) }))
	require.NoError(t, r.Close())
	require.ErrorIs(t, r.RegisterCallback(testId, func(iotago.BlockID) {}), context.Canceled)
	require.Never(t, called.Load, 500*time.Millisecond, 10*time.Millisecond)

	o.AssertExpectations(t)
}
