// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"
)

// WaitSelectFunc handles select operations on wait channels. The callback
// MUST call waitInbox.Emptied() if a RenotifyFunc is received.
type WaitSelectFunc func(waitInbox *WaitInbox)

// BasicWaitSelect provides a standard implementation of WaitSelectFunc that waits
// for a notification or context cancellation.
//
// Parameters:
//   - ctx: Context for cancellation
//   - waitInbox: Wait inbox to receive notifications from
//
// Returns a RenotifyFunc if notified, or an error if the context was cancelled.
// Automatically calls waitInbox.Emptied() when a notification is received.
func BasicWaitSelect(ctx context.Context, waitInbox *WaitInbox) (RenotifyFunc, error) {
	traceRegion := "rdvq.BasicWaitSelect"
	waitCh := waitInbox.Ch()
	trace.Logf(ctx, traceRegion, "entering select: waitInbox=%p, waitCh=%p", waitInbox, waitCh)
	select {
	case renotifyFn := <-waitCh:
		waitInbox.Emptied()
		trace.Logf(ctx, traceRegion, "received renotifyFn from waitInbox=%p, waitCh=%p", waitInbox, waitCh)
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
func (w *Waiters) Init() {
	traceRegion := "rdvq.Waiters.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	w.q.Init()
}

// WaitFuncWithOrphanHandler registers a waiter and handles the blocking wait with custom
// orphan notification handling. This is the lower-level function that other Wait methods wrap.
//
// Parameters:
//   - waiter: Waiter instance for this goroutine
//   - confirmFn: Function called to verify conditions after registration but before blocking
//   - orphanFn: Custom function to handle orphaned renotify functions
//   - selectFn: Custom select function for handling the wait operation
//
// The confirmFn prevents race conditions by re-checking conditions after the waiter
// is registered but before blocking. If confirmFn returns false, the wait is aborted.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) WaitFuncWithOrphanHandler(
	waiter *Waiter,
	confirmFn func() bool,
	orphanFn NotifyFunc,
	selectFn WaitSelectFunc,
) {
	traceRegion := "rdvq.Waiters.WaitFuncWithOrphanHandler"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	if w == nil {
		selectFn(nil)
		return
	}

	waitInbox := waitInboxFor(waiter, w)
	w.q.PopFrontFunc(
		waitInbox,
		func(renotifyFn RenotifyFunc) {
			if !orphanFn(renotifyFn) {
				renotifyFn()
			}
		},
		func(inbox *Inbox[RenotifyFunc]) {
			if confirmFn() {
				selectFn(waitInbox)
			}
		},
	)
}

// WaitFunc registers a waiter and handles the blocking wait with custom select handling.
// This is a convenience wrapper around WaitFuncWithOrphanHandler that uses the default
// orphan handler (w.Notify).
//
// Parameters:
//   - waiter: Waiter instance for this goroutine
//   - confirmFn: Function called to verify conditions after registration but before blocking
//   - selectFn: Custom select function for handling the wait operation
func (w *Waiters) WaitFunc(waiter *Waiter, confirmFn func() bool, selectFn WaitSelectFunc) {
	w.WaitFuncWithOrphanHandler(waiter, confirmFn, w.Notify, selectFn)
}

// WaitWithOrphanHandler registers a waiter and blocks until notified or context cancelled,
// with custom orphan notification handling.
//
// Parameters:
//   - ctx: Context for cancellation
//   - waiter: Waiter instance for this goroutine
//   - confirmFn: Function called to verify conditions after registration but before blocking
//   - orphanFn: Custom function to handle orphaned renotify functions
//
// Returns a RenotifyFunc if notified, or an error if the context was cancelled.
// The confirmFn prevents race conditions by re-checking conditions after registration.
func (w *Waiters) WaitWithOrphanHandler(
	ctx context.Context,
	waiter *Waiter,
	confirmFn func() bool,
	orphanFn NotifyFunc,
) (RenotifyFunc, error) {
	var renotifyFn RenotifyFunc
	var err error
	w.WaitFuncWithOrphanHandler(waiter, confirmFn, orphanFn, func(waitInbox *WaitInbox) {
		renotifyFn, err = BasicWaitSelect(ctx, waitInbox)
	})
	return renotifyFn, err
}

// Wait registers a waiter and blocks until notified or context cancelled.
// This is a convenience wrapper around WaitWithOrphanHandler that uses the default
// orphan handler (w.Notify).
//
// Parameters:
//   - ctx: Context for cancellation
//   - waiter: Waiter instance for this goroutine
//   - confirmFn: Function called to verify conditions after registration but before blocking
//
// Returns a RenotifyFunc if notified, or an error if the context was cancelled.
// The confirmFn prevents race conditions by re-checking conditions after registration.
func (w *Waiters) Wait(ctx context.Context, waiter *Waiter, confirmFn func() bool) (RenotifyFunc, error) {
	return w.WaitWithOrphanHandler(ctx, waiter, confirmFn, w.Notify)
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
