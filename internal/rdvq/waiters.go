// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Waiters provides a notification system for coordinating between senders and receivers.
// It's used internally by Required to prevent race conditions when checking outboxes
// and blocking on channels.
//
// The design reuses Optional[struct{}] to provide lock-free notification delivery.
// When senders add items to outboxes, they call Notify() to wake up one waiting
// receiver. Receivers register with verification functions that re-check for work
// after registration but before blocking.
type Waiters struct {
	inner Optional[struct{}] // Reuses Optional for lock-free notification delivery
}

func (q *Waiters) Init() {
	q.inner.Init(wp)
}

// New creates a waiter that will use the given verification function to prevent
// race conditions. The verifyFn should return true if the waiter should continue
// waiting, false if work has become available and waiting is no longer needed.
//
// The verification function is called after the waiter registers but before it
// starts blocking, ensuring that no notifications are missed due to race conditions.
func (q *Waiters) New(verifyFn func() bool) Waiter {
	return Waiter{
		q:        q,
		verifyFn: verifyFn,
	}
}

// Notify signals one waiting receiver to re-check for work. This should be
// called by senders when they add items to outboxes, ensuring that waiting
// receivers are awakened to process the new work.
//
// If no receivers are waiting, the notification is silently dropped.
func (q *Waiters) Notify() {
	_ = q.inner.TryPushBack(wp, struct{}{})
}

func (q *Waiters) NotifyAll() {
	for q.inner.TryPushBack(wp, struct{}{}) {
		// Keep notifying until we can't anymore
	}
}

var wp = &Pool[struct{}]{}
