// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/streampool/internal/trace"
)

// WaitSelectFunc handles the select operation for a Waiters wait. It receives
// the wait channel and returns the Notification that came across, or the zero
// value if it received from some other case (e.g., ctx.Done). The Waiters caller
// handles Emptied() bookkeeping; the user does not need to call it.
type WaitSelectFunc func(waitCh <-chan Notification) Notification

// BasicWaitSelect provides a standard implementation of [WaitSelectFunc] that
// selects on the wait channel and ctx.Done(). Returns the Notification and a
// nil error on success; returns the zero Notification and a non-nil error if the
// context was cancelled.
func BasicWaitSelect(ctx context.Context, waitCh <-chan Notification) (Notification, error) {
	traceRegion := "rdvq.BasicWaitSelect"
	trace.Logf(ctx, traceRegion, "entering select: waitCh=%p", waitCh)
	select {
	case m := <-waitCh:
		trace.Logf(ctx, traceRegion, "received notification from waitCh=%p", waitCh)
		return m, nil
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		return Notification{}, ctx.Err()
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
	q inboxQueueQueue[Notification]
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
//   - confirmFn: Function called to verify conditions after registration but
//     before blocking. The confirmFn prevents missed notifications by
//     re-checking conditions after the waiter is registered. If it returns
//     false, the wait is aborted (selectFn is not called).
//   - selectFn: Custom select function for handling the wait operation
//
// Returns the Notification that selectFn received, or the zero value if no
// notification arrived (e.g., the wait was aborted by confirmFn or selectFn
// picked some other case such as ctx.Done).
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) WaitFunc(confirmFn func() bool, selectFn WaitSelectFunc) Notification {
	traceRegion := "rdvq.Waiters.WaitFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	if w == nil {
		// No waiters infrastructure to register against; let selectFn run
		// against a nil channel (its non-channel cases — ctx.Done, etc. —
		// can still fire).
		return selectFn(nil)
	}

	waitInbox := w.q.borrowInbox()
	// PopFrontFunc always leaves the inbox free (received, abandoned, or orphan-drained),
	// so the owning receiver always reclaims it — recycling abandoned inboxes too (the
	// generation-stamped protocol makes the lingering hint inert).
	defer w.q.reclaimInbox(waitInbox)
	var m Notification
	w.q.PopFrontFunc(
		waitInbox,
		func(orphan Notification) {
			// Stranded notification from an abandoned inbox: re-offer it to another
			// waiter, or run its fallback directly if none are available. Notify is
			// total, so a single call both re-offers and, failing that, conserves.
			w.Notify(orphan.fallback)
		},
		func(ib *inbox[Notification]) {
			if confirmFn() {
				m = selectFn(ib.channel())
				if m.Received() {
					ib.emptied()
				}
			}
		},
	)
	return m
}

// Wait registers a waiter and blocks until notified or context cancelled.
func (w *Waiters) Wait(ctx context.Context, confirmFn func() bool) (Notification, error) {
	var err error
	m := w.WaitFunc(confirmFn, func(waitCh <-chan Notification) Notification {
		var got Notification
		got, err = BasicWaitSelect(ctx, waitCh)
		return got
	})
	return m, err
}

// Notify delivers a wake to one waiting goroutine, running fallback if no waiter takes it
// (total conservation). A nil fallback defaults to noop. The waiter receives a
// waiter-style (terminal) Notification: a Forward it cannot use runs fallback.
func (w *Waiters) Notify(fallback func()) {
	if fallback == nil {
		fallback = noop
	}
	if !w.deliver(Notification{fallback: fallback}) {
		fallback()
	}
}

// Deliver hands an already-formed Notification to one waiting goroutine, returning
// whether a waiter took it. Unlike [Waiters.Notify] it neither defaults nil nor runs a
// fallback on a miss — the caller decides what a miss means. It is the re-injection
// primitive a [Listener] uses to route a notification from a Listeners set into this
// waiter set ([Listener.Notify] = someWaiters.Deliver), preserving the incoming
// notification's re-circulation identity (a listener-style m keeps re-circulating
// through its origin Notifier on a later Forward).
func (w *Waiters) Deliver(m Notification) bool {
	return w.deliver(m)
}

// deliver pushes m to a parked waiter, returning whether one took it.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) deliver(m Notification) bool {
	traceRegion := "rdvq.Waiters.deliver"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	return w.q.TryPushBack(m)
}

// NotifyAll signals all waiting goroutines to re-check for work.
// This is typically used during shutdown or when conditions change globally.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) NotifyAll() {
	traceRegion := "rdvq.Waiters.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	for w.q.TryPushBack(Notification{fallback: noop}) {
		// Keep notifying until we can't anymore
	}
}
