// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"
)

type RenotifyFunc func()

// WaitSelectFunc handles select operations on wait channels, returning the the
// received RenotifyFunc or nil if the select exited without receiving one.
type WaitSelectFunc func(waitCh <-chan RenotifyFunc) RenotifyFunc

type NotifyFunc func(RenotifyFunc)

type Waiter struct {
	inbox Inbox[RenotifyFunc]
}

func (w *Waiter) waiter() *Waiter {
	return w
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
) RenotifyFunc {
	traceRegion := "rdvq.Waiter.WaitFuncWithOrphanHandler"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	if w == nil {
		return selectFn(nil)
	}
	var renotifyFn RenotifyFunc
	w.q.PopFrontFunc(
		&waiter.inbox,
		orphanFn,
		func(ch <-chan RenotifyFunc) SelectResult {
			if confirmFn() {
				renotifyFn = selectFn(ch)
				if renotifyFn != nil {
					return SelectInboxEmptied
				}
			}
			return SelectAborted
		},
	)
	return renotifyFn
}

func (w *Waiters) WaitFunc(waiter *Waiter, confirmFn func() bool, selectFn WaitSelectFunc) RenotifyFunc {
	return w.WaitFuncWithOrphanHandler(waiter, confirmFn, w.Notify, selectFn)
}

func (w *Waiters) WaitWithOrphanHandler(
	ctx context.Context,
	waiter *Waiter,
	confirmFn func() bool,
	orphanFn NotifyFunc,
) (RenotifyFunc, error) {
	traceRegion := "rdvq.Waiter.WaitWithOrphanHandler"

	var err error
	renotifyFn := w.WaitFuncWithOrphanHandler(waiter, confirmFn, orphanFn, func(waitCh <-chan RenotifyFunc) RenotifyFunc {
		trace.Logf(ctx, traceRegion, "entering select: waitCh=%p", waitCh)
		select {
		case renotifyFn := <-waitCh:
			trace.Logf(ctx, traceRegion, "received renotifyFn from waitCh=%p", waitCh)
			return renotifyFn
		case <-ctx.Done():
			trace.Logf(ctx, traceRegion, "received context done signal")
			err = ctx.Err()
		}
		return nil
	})
	return renotifyFn, err
}

func (w *Waiters) Wait(ctx context.Context, waiter *Waiter, confirmFn func() bool) (RenotifyFunc, error) {
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
func (w *Waiters) Notify(renotifyFn RenotifyFunc) {
	traceRegion := "rdvq.Waiters.Notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	if renotifyFn == nil {
		renotifyFn = noopRenotify
	}

	if !w.q.TryPushBack(renotifyFn) {
		renotifyFn()
	}
}

//nolint:contextcheck // background context used only for tracing
func (w *Waiters) NotifyAll() {
	traceRegion := "rdvq.Waiters.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	for w.q.TryPushBack(noopRenotify) {
		// Keep notifying until we can't anymore
	}
}

func noopRenotify() {
	// noopRenotify is a no-op function used as a default renotify function to
	// avoid nil checks in Notify.
}
