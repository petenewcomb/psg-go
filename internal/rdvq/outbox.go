// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/omnipool"
)

type Outbox[T any] struct {
	ch        chan T
	wasFilled bool
	listeners Listeners
	refCount  atomic.Int32
}

func NewOutbox[T any]() *Outbox[T] {
	ob := omnipool.GetCustom(outboxTrait[T]{})
	ob.refCount.Store(1)
	return ob
}

func (ob *Outbox[T]) Free() {
	newValue := ob.refCount.Add(-1)
	if newValue < 0 {
		panic("reference count underflow")
	}
	if newValue == 0 {
		omnipool.PutCustom(outboxTrait[T]{}, ob)
	}
}

type outboxTrait[T any] struct{}

func (outboxTrait[T]) Make() *Outbox[T] {
	ob := &Outbox[T]{
		ch: make(chan T, 1),
	}
	ob.listeners.Init()
	return ob
}

func (outboxTrait[T]) Reset(ob *Outbox[T]) {
	ob.wasFilled = false
	ob.listeners.Reset()
}

// Ch returns the outbox's channel for use in select statements.
// Returns nil if the outbox itself is nil.
// Panics if called when no channel has been allocated, which should only
// happen if Ch() is called outside of a selectFn callback.
func (ob *Outbox[T]) Ch() chan<- T {
	if ob == nil {
		return nil
	}
	ch := ob.ch
	if ch == nil {
		panic("outbox channel is nil")
	}
	return ch
}

func (ob *Outbox[T]) fillPending() {
	ob.refCount.Add(1)
	ob.wasFilled = false
}

func (ob *Outbox[T]) Filled() {
	ob.wasFilled = true
}

func (ob *Outbox[T]) fillAttemptComplete() {
	if !ob.wasFilled {
		ob.Free()
	}
}

// WasFilled returns true if Filled() was called.
func (ob *Outbox[T]) WasFilled() bool {
	return ob.wasFilled
}

func (ob *Outbox[T]) emptied() {
	ob.listeners.Notify(nil)
	ob.Free()
}

// Listeners returns the outbox's listeners for subscription to availability notifications.
// Panics if called when no channel has been allocated, which should only
// happen if Listeners() is called outside of a selectFn callback.
func (ob *Outbox[T]) Listeners() *Listeners {
	if ob.ch == nil {
		panic("outbox channel is nil")
	}
	return &ob.listeners
}
