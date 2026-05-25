// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/omnipool"
)

// outbox provides per-sender buffering for overflow items when no receivers
// are immediately available. Each outbox is dedicated to sending items from
// a specific Sender to a specific Queue.
type outbox[T any] struct {
	ch        chan T
	wasFilled bool
	listeners Listeners
	refCount  atomic.Int32
}

func newOutbox[T any]() *outbox[T] {
	ob := omnipool.GetCustom(outboxTrait[T]{})
	ob.refCount.Store(1)
	return ob
}

func (ob *outbox[T]) free() {
	newValue := ob.refCount.Add(-1)
	if newValue < 0 {
		panic("reference count underflow")
	}
	if newValue == 0 {
		// Drain any pending listener subscriptions before recycling.
		// A producer-side work item may have registered for "outbox
		// drained" notification but never received its retry callback
		// (e.g., its hosting goroutine exited). Without this drain,
		// the leftover listener trips the Listeners.Reset panic when
		// the outbox is returned to the pool.
		ob.listeners.NotifyAll()
		omnipool.PutCustom(outboxTrait[T]{}, ob)
	}
}

type outboxTrait[T any] struct{}

func (outboxTrait[T]) Make() *outbox[T] {
	ob := &outbox[T]{
		ch: make(chan T, 1),
	}
	ob.listeners.Init()
	return ob
}

func (outboxTrait[T]) Reset(ob *outbox[T]) {
	ob.wasFilled = false
	ob.listeners.Reset()
}

func (ob *outbox[T]) fillPending() {
	ob.refCount.Add(1)
	ob.wasFilled = false
}

// filled marks the outbox as having been successfully filled with a value.
// Must be called by PushBackFunc after a successful send to ob.ch.
func (ob *outbox[T]) filled() {
	ob.wasFilled = true
}

func (ob *outbox[T]) fillAttemptComplete() {
	if !ob.wasFilled {
		ob.free()
	}
}

func (ob *outbox[T]) emptied() {
	ob.listeners.Notify(nil)
	ob.free()
}
