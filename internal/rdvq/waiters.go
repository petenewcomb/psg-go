// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"
)

// WaitSelectFunc handles the select operation for a Waiters wait. It receives
// the wait channel and returns the RenotifyFunc that came across, or nil if
// it received from some other case (e.g., ctx.Done). The Waiters caller
// handles Emptied() bookkeeping; the user does not need to call it.
type WaitSelectFunc func(waitCh <-chan RenotifyFunc) RenotifyFunc

// BasicWaitSelect provides a standard implementation of [WaitSelectFunc] that
// selects on the wait channel and ctx.Done(). Returns the RenotifyFunc and a
// nil error on success; returns nil RenotifyFunc and a non-nil error if the
// context was cancelled.
func BasicWaitSelect(ctx context.Context, waitCh <-chan RenotifyFunc) (RenotifyFunc, error) {
	traceRegion := "rdvq.BasicWaitSelect"
	trace.Logf(ctx, traceRegion, "entering select: waitCh=%p", waitCh)
	select {
	case renotifyFn := <-waitCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from waitCh=%p", waitCh)
		return renotifyFn, nil
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		return nil, ctx.Err()
	}
}

// Waiters provides a blocking wait and notification system for coordinating
// between goroutines. It can be used anywhere a multi-party blocking wait
// mechanism is needed.
//
// Waiters uses FIFO selection to ensure fairness - the waiter that has been
// waiting longest gets notified first.
//
// When goroutines register to wait, they provide a verification function that
// re-checks conditions after registration but before blocking, preventing
// missed notifications due to race conditions.
type Waiters struct {
	q inboxQueueQueue[RenotifyFunc]
}

// Init initializes the Waiters for use. Must be called before any other operations.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) Init() {
	traceRegion := "rdvq.Waiters.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	w.q.Init()
}

// WaitFunc registers a waiter and handles the blocking wait with custom
// select handling.
//
// Parameters:
//   - waiter: Waiter instance for this goroutine
//   - confirmFn: Function called to verify conditions after registration but
//     before blocking. The confirmFn prevents missed notifications by
//     re-checking conditions after the waiter is registered. If it returns
//     false, the wait is aborted (selectFn is not called).
//   - selectFn: Custom select function for handling the wait operation
//
// Returns the RenotifyFunc that selectFn received, or nil if no notification
// arrived (e.g., the wait was aborted by confirmFn or selectFn picked some
// other case such as ctx.Done).
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) WaitFunc(waiter *Waiter, confirmFn func() bool, selectFn WaitSelectFunc) RenotifyFunc {
	traceRegion := "rdvq.Waiters.WaitFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	if w == nil {
		// No waiters infrastructure to register against; let selectFn run
		// against a nil channel (its non-channel cases — ctx.Done, etc. —
		// can still fire).
		return selectFn(nil)
	}

	waitInbox := waitInboxFor(waiter, w)
	var rf RenotifyFunc
	w.q.PopFrontFunc(
		waitInbox,
		func(renotifyFn RenotifyFunc) {
			// Stranded renotifyFn from an abandoned inbox: re-queue it for
			// another waiter, or invoke directly if no waiters are available.
			if !w.Notify(renotifyFn) {
				renotifyFn()
			}
		},
		func(ib *inbox[RenotifyFunc]) {
			if confirmFn() {
				rf = selectFn(ib.channel())
				if rf != nil {
					ib.emptied()
				}
			}
		},
	)
	return rf
}

// Wait registers a waiter and blocks until notified or context cancelled.
func (w *Waiters) Wait(ctx context.Context, waiter *Waiter, confirmFn func() bool) (RenotifyFunc, error) {
	var err error
	rf := w.WaitFunc(waiter, confirmFn, func(waitCh <-chan RenotifyFunc) RenotifyFunc {
		var got RenotifyFunc
		got, err = BasicWaitSelect(ctx, waitCh)
		return got
	})
	return rf, err
}

// Notify signals one waiting goroutine to re-check for work.
// Returns true if a waiter was successfully notified, false if no waiters
// were available to notify.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) Notify(renotifyFn RenotifyFunc) bool {
	traceRegion := "rdvq.Waiters.Notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	if renotifyFn == nil {
		renotifyFn = NoopRenotify
	}
	return w.q.TryPushBack(renotifyFn)
}

// NotifyAll signals all waiting goroutines to re-check for work.
// This is typically used during shutdown or when conditions change globally.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) NotifyAll() {
	traceRegion := "rdvq.Waiters.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	for w.q.TryPushBack(NoopRenotify) {
		// Keep notifying until we can't anymore
	}
}
