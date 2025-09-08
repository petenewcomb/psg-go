// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/omnipool"
)

// Outbox provides per-sender buffering for overflow items when no receivers
// are immediately available. Each Outbox is dedicated to sending items from
// a specific Sender to a specific Queue.
type Outbox[T any] struct {
	ch        chan T
	wasFilled bool
	listeners Listeners
	refCount  atomic.Int32
}

func newOutbox[T any]() *Outbox[T] {
	ob := omnipool.GetCustom(outboxTrait[T]{})
	ob.refCount.Store(1)
	return ob
}

func (ob *Outbox[T]) free() {
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

// Filled marks the outbox as having been successfully filled with a value.
// This must be called by the sender after successfully sending to the outbox channel.
func (ob *Outbox[T]) Filled() {
	ob.wasFilled = true
}

func (ob *Outbox[T]) fillAttemptComplete() {
	if !ob.wasFilled {
		ob.free()
	}
}

func (ob *Outbox[T]) emptied() {
	ob.listeners.Notify(nil)
	ob.free()
}
