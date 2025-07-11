// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"
)

type RenotifyFunc func()

// Waiters provides a blocking wait and notification system for coordinating
// between senders and receivers. It's used internally by Required to prevent
// race conditions when checking outboxes and blocking on channels, but can be
// used anywhere a multi-party blocking wait mechanism is needed.
//
// The design reuses Optional to provide lock-free notification delivery. When
// senders add items to outboxes, they call Notify() to wake up one waiting
// receiver. Receivers register with verification functions that re-check for
// work after registration but before blocking.
type Waiters struct {
	q Optional[RenotifyFunc] // Reuses Optional for lock-free notification delivery
}

func (w *Waiters) Init() {
	w.q.Init(wp)
}

// New creates a waiter that will use the given verification function to prevent
// race conditions. The confirmFn should return true if the waiter should continue
// waiting, false if work has become available and waiting is no longer needed.
//
// The verification function is called after the waiter registers but before it
// starts blocking, ensuring that no notifications are missed due to race conditions.
func (w *Waiters) New(confirmFn func() bool) Waiter {
	return Waiter{
		w:         w,
		confirmFn: confirmFn,
	}
}

// Notify signals one waiting receiver to re-check for work. This should be
// called by senders when they add items to outboxes, ensuring that waiting
// receivers are awakened to process the new work.
//
// Returns true if a receiver was successfully notified, false if no receivers
// were available to notify.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) Notify(renotifyFn RenotifyFunc) {
	traceRegion := "rdvq.Waiters.Notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	if !w.q.TryPushBack(wp, renotifyFn) {
		renotifyFn()
	}
}

//nolint:contextcheck // background context used only for tracing
func (w *Waiters) NotifyAll() {
	traceRegion := "rdvq.Waiters.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	for w.q.TryPushBack(wp, func() {}) {
		// Keep notifying until we can't anymore
	}
}

var wp = &Pool[RenotifyFunc]{}
