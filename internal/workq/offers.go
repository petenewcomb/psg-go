// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"
)

// pending is a private type alias for Pending to prevent direct access to base methods
type pending = Pending

type Offers struct {
	pending
	waiters Waiters
}

//nolint:contextcheck // background context used only for tracing
func (q *Offers) Init() {
	traceRegion := "workq.Offers.Init"

	q.pending.Init()
	q.waiters.Init()

	trace.Logf(context.Background(), traceRegion, "Offers=%p, pending=%p, waiters=%p", q, &q.pending, &q.waiters)
}

// TryPopFront wraps Pending.TryPopFront and notifies waiters when successful
//
//nolint:contextcheck // background context used only for tracing
func (q *Offers) TryPopFront() (WorkFunc, bool) {
	defer trace.StartRegion(context.Background(), "workq.TryPopFront").End()
	workFn, ok := q.pending.TryPopFront()
	if ok {
		// Notify waiters that there's now space available in the queue
		trace.Logf(context.Background(), "workq.TryPopFront", "notifying waiters after successful pop of workFn=%p", workFn)
		q.waiters.Notify(func() {})
	} else {
		trace.Logf(context.Background(), "workq.TryPopFront", "no work found")
	}
	return workFn, ok
}

// PopFrontFunc wraps Pending.PopFrontFunc and notifies waiters when work is successfully consumed
//
//nolint:contextcheck // background context used only for tracing
func (q *Offers) PopFrontFunc(queueFn QueueWorkFunc, selectFn PopSelectFunc) {
	// Wrap queueFn to notify waiters whenever work is successfully consumed
	wrappedQueueFn := func(workFn WorkFunc) {
		trace.Logf(context.Background(), "workq.PopFrontFunc", "notifying waiters after work consumption")
		q.waiters.Notify(func() {})
		queueFn(workFn)
	}
	q.pending.PopFrontFunc(wrappedQueueFn, selectFn)
}

// PopFront wraps Pending.PopFront and notifies waiters when work is successfully consumed
func (q *Offers) PopFront(ctx context.Context, queueFn QueueWorkFunc) error {
	// Wrap queueFn to notify waiters whenever work is successfully consumed
	wrappedQueueFn := func(workFn WorkFunc) {
		trace.Logf(ctx, "workq.PopFront", "notifying waiters after work consumption")
		q.waiters.Notify(func() {})
		queueFn(workFn)
	}
	return q.pending.PopFront(ctx, wrappedQueueFn)
}

// PopFrontExcessFunc wraps Pending.PopFrontExcessFunc and notifies waiters when work is successfully consumed
//
//nolint:contextcheck // background context used only for tracing
func (q *Offers) PopFrontExcessFunc(selectFn WaitSelectFunc) (WorkFunc, bool) {
	defer trace.StartRegion(context.Background(), "workq.PopFrontExcessFunc").End()
	workFn, ok := q.pending.PopFrontExcessFunc(selectFn)
	if ok {
		// Notify waiters that there's now space available in the queue
		trace.Logf(context.Background(), "workq.PopFrontExcessFunc", "notifying waiters after successful pop")
		q.waiters.Notify(func() {})
	}
	return workFn, ok
}

// PopFrontExcess wraps Pending.PopFrontExcess and notifies waiters when work is successfully consumed
func (q *Offers) PopFrontExcess(ctx context.Context) (WorkFunc, error) {
	defer trace.StartRegion(ctx, "workq.PopFrontExcess").End()
	workFn, err := q.pending.PopFrontExcess(ctx)
	if err == nil {
		// Notify waiters that there's now space available in the queue
		trace.Logf(ctx, "workq.PopFrontExcess", "notifying waiters after successful pop")
		q.waiters.Notify(func() {})
	}
	return workFn, err
}

// WithOutboxFunc passes an [Outbox] to the given function, guaranteeing
// exclusive access to the outbox for the duration of the call. This allows the
// work function created by [NewOffer] to access an outbox specific to the
// context in which it is executed.
type WithOutboxFunc func(context.Context, func(*Outbox))

// NewPostOfferWork creates a new work function that will push the given work function
// into this Offers queue.
//
//nolint:contextcheck // background context used only for tracing
func (q *Offers) NewPostOfferWork(workFn WorkFunc, withOutboxFn WithOutboxFunc) WorkFunc {
	traceRegion := "workq.Offers.NewPostOfferWork"
	trace.Logf(context.Background(), traceRegion, "Offers=%p", q)
	pendingWorkFn := func(ctx context.Context, ex Execution) error {
		traceRegion := traceRegion + ".pendingWorkFn"
		defer trace.StartRegion(ctx, traceRegion).End()
		trace.Logf(context.Background(), traceRegion, "Offers=%p", q)
		originalStarting := ex.Starting
		ex.Starting = func() {
			trace.Logf(ctx, traceRegion, "notifying waiters")
			q.waiters.Notify(func() {})
			originalStarting()
		}
		return workFn(ctx, ex)
	}
	return func(ctx context.Context, ex Execution) error {
		traceRegion := traceRegion + ".workFn"
		defer trace.StartRegion(ctx, traceRegion).End()
		trace.Logf(context.Background(), traceRegion, "Offers=%p", q)
		var pushed bool
		withOutboxFn(ctx, func(outbox *Outbox) {
			pushed = q.TryPushBack(outbox, pendingWorkFn)
			if !pushed && ex.ReadyFn != nil {
				q.waiters.Add(func(renotifyFn RenotifyFunc) {
					ex.ReadyFn(func() {
						q.waiters.Notify(renotifyFn)
					})
				})
				// Retry push after registering for notification in case
				// something happened in between
				pushed = q.TryPushBack(outbox, pendingWorkFn)
			}
		})
		if pushed {
			// Since we can't know if we're going to be able to push until we
			// successfully do so, we have to call Starting after the fact.
			ex.Starting()
		}
		return nil
	}
}
