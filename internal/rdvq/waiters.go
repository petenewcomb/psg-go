// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"
)

// WaitSelectFunc handles select operations on wait channels. The callback
// MUST call waiter.Notified(renotifyFn) if a RenotifyFunc is received.
type WaitSelectFunc func(waiter *Waiter)

func BasicWaitSelect(ctx context.Context, waiter *Waiter) error {
	traceRegion := "rdvq.BasicWaitSelect"
	waitCh := waiter.Ch()
	trace.Logf(ctx, traceRegion, "entering select: waiter=%p, waitCh=%p", waiter, waitCh)
	select {
	case renotifyFn := <-waitCh:
		waiter.Notified(renotifyFn)
		trace.Logf(ctx, traceRegion, "received renotifyFn from waiter=%p, waitCh=%p", waiter, waitCh)
		return nil
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		return ctx.Err()
	}
}

// Waiters provides a blocking wait and notification system for coordinating
// between senders and receivers. It's used internally by Required to prevent
// race conditions when checking outboxes and blocking on channels, but can be
// used anywhere a multi-party blocking wait mechanism is needed.
//
// The design reuses Optional to provide lock-free notification delivery. For
// example, when senders add items to outboxes via Required, it calls Notify()
// on its filledOutboxes Waiters instance to wake up one waiting receiver by
// passing a signal to that receiver's inbox. When notification receivers
// register with Waiters to provide an inbox, they also provide confirmation
// functions that allow the waiter to re-check to confirm the wait after
// registration but before blocking.
type Waiters struct {
	q Optional[RenotifyFunc] // Reuses Optional for lock-free notification delivery
}

func (w *Waiters) Init() {
	traceRegion := "rdvq.Waiters.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	w.q.Init()
}

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

	waiter.renotifyFn = nil

	if w == nil {
		selectFn(nil)
		return
	}

	w.q.PopFrontFunc(
		&waiter.inbox,
		func(renotifyFn RenotifyFunc) {
			if !orphanFn(renotifyFn) {
				renotifyFn()
			}
		},
		func(inbox *Inbox[RenotifyFunc]) {
			if confirmFn() {
				selectFn(waiter)
			}
		},
	)
}

func (w *Waiters) WaitFunc(waiter *Waiter, confirmFn func() bool, selectFn WaitSelectFunc) {
	w.WaitFuncWithOrphanHandler(waiter, confirmFn, w.Notify, selectFn)
}

func (w *Waiters) WaitWithOrphanHandler(
	ctx context.Context,
	waiter *Waiter,
	confirmFn func() bool,
	orphanFn NotifyFunc,
) error {
	var err error
	w.WaitFuncWithOrphanHandler(waiter, confirmFn, orphanFn, func(waiter *Waiter) {
		err = BasicWaitSelect(ctx, waiter)
	})
	return err
}

func (w *Waiters) Wait(ctx context.Context, waiter *Waiter, confirmFn func() bool) error {
	return w.WaitWithOrphanHandler(ctx, waiter, confirmFn, w.Notify)
}

// Notify signals one waiting receiver to re-check for work. This should be
// called by senders when they add items to outboxes, ensuring that waiting
// receivers are awakened to process the new work.
//
// Returns true if a receiver was successfully notified, false if no receivers
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

//nolint:contextcheck // background context used only for tracing
func (w *Waiters) NotifyAll() {
	traceRegion := "rdvq.Waiters.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	for w.q.TryPushBack(NoopRenotify) {
		// Keep notifying until we can't anymore
	}
}
