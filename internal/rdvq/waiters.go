// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/streampool/internal/trace"
)

// WaitSelectFunc handles the select operation for a Waiters wait. It receives
// the wait channel and returns whether it received a wake from it (false if it
// took some other case, e.g. ctx.Done). The Waiters caller handles the inbox
// bookkeeping; the user does not need to.
type WaitSelectFunc = func(waitCh <-chan struct{}) bool

// BasicWaitSelect provides a standard implementation of [WaitSelectFunc] that
// selects on the wait channel and ctx.Done(). Returns whether a wake was
// received and a non-nil error if the context was cancelled instead.
func BasicWaitSelect(ctx context.Context, waitCh <-chan struct{}) (bool, error) {
	traceRegion := "rdvq.BasicWaitSelect"
	trace.Logf(ctx, traceRegion, "entering select: waitCh=%p", waitCh)
	select {
	case <-waitCh:
		trace.Logf(ctx, traceRegion, "received wake from waitCh=%p", waitCh)
		return true, nil
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		return false, ctx.Err()
	}
}

// Waiters provides a blocking wait and wake system for coordinating between
// goroutines. It can be used anywhere a multi-party blocking wait mechanism is
// needed.
//
// Waiters uses FIFO selection to ensure fairness - the waiter that has been
// waiting longest gets woken first.
//
// When goroutines register to wait, they provide a verification function that
// re-checks conditions after registration but before blocking, preventing
// missed wakes due to race conditions.
type Waiters struct {
	q inboxQueueQueue[struct{}]
}

// Init initializes the Waiters for use. Must be called before any other operations.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) Init() {
	traceRegion := "rdvq.Waiters.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	w.q.Init()
}

// dropWake is the orphan handler for the payloadless wake queue: a wake landing
// in an abandoned registration is simply dropped — the abandoner is running,
// and a running worker attempts everything before parking.
func dropWake(struct{}) {}

// WaitFunc registers a waiter and handles the blocking wait with custom
// select handling.
//
// Parameters:
//   - confirmFn: Function called to verify conditions after registration but
//     before blocking. The confirmFn prevents missed wakes by re-checking
//     conditions after the waiter is registered. If it returns false, the wait
//     is aborted (selectFn is not called).
//   - selectFn: Custom select function for handling the wait operation
//
// Returns whether a wake was received (false if the wait was aborted by
// confirmFn or selectFn picked some other case such as ctx.Done).
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) WaitFunc(confirmFn func() bool, selectFn WaitSelectFunc) bool {
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
	received := false
	w.q.PopFrontFunc(
		waitInbox,
		dropWake,
		func(ib *inbox[struct{}]) {
			if confirmFn() {
				received = selectFn(ib.channel())
				if received {
					ib.emptied()
				}
			}
		},
	)
	return received
}

// Wait registers a waiter and blocks until woken or context cancelled.
func (w *Waiters) Wait(ctx context.Context, confirmFn func() bool) error {
	var err error
	w.WaitFunc(confirmFn, func(waitCh <-chan struct{}) bool {
		var received bool
		received, err = BasicWaitSelect(ctx, waitCh)
		return received
	})
	return err
}

// Notify wakes one waiting goroutine, running fallback if no waiter is parked.
// A nil fallback defaults to noop. This is both the queue relay (a listener's
// wake-one-worker action, with the queue's spawn hook as fallback on
// spawn-capable queues) and the work-supply wake.
func (w *Waiters) Notify(fallback func()) {
	if fallback == nil {
		fallback = noop
	}
	if !w.wakeOne() {
		fallback()
	}
}

// wakeOne pushes a wake to a parked waiter, returning whether one took it.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) wakeOne() bool {
	traceRegion := "rdvq.Waiters.wakeOne"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	return w.q.TryPushBack(struct{}{})
}

// NotifyAll wakes all waiting goroutines to re-check for work.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) NotifyAll() {
	traceRegion := "rdvq.Waiters.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	for w.q.TryPushBack(struct{}{}) {
		// Keep waking until no parked waiter remains
	}
}
