// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"
)

// pending is a private type alias for Pending to prevent direct access to base methods
type pending = Pending

// Offers adds to [Pending] the ability to create work items that offer other
// work items to a [Pending] queue. This allows the offers themselves to be
// managed in a non-blocking fashion by an [Accepted] work queue and thus
// facilitates asynchronous posting of work items from one work queue to
// another.
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
	traceRegion := "Offers.TryPopFront"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Offers=%p", q)
	workFn, ok := q.pending.TryPopFront()
	if ok {
		q.waiters.Notify(func() {})
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
	traceRegion := "Offers.PopFrontExcessFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Offers=%p", q)
	workFn, ok := q.pending.PopFrontExcessFunc(selectFn)
	if ok {
		q.waiters.Notify(func() {})
	}
	return workFn, ok
}

// PopFrontExcess wraps Pending.PopFrontExcess and notifies waiters when work is successfully consumed
func (q *Offers) PopFrontExcess(ctx context.Context) (WorkFunc, error) {
	traceRegion := "Offers.PopFrontExcess"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Offers=%p", q)
	workFn, err := q.pending.PopFrontExcess(ctx)
	if err == nil {
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
	return func(ctx context.Context, ex Execution) error {
		traceRegion := traceRegion + ".workFn"
		defer trace.StartRegion(ctx, traceRegion).End()
		trace.Logf(context.Background(), traceRegion, "Offers=%p", q)

		if ex.Starting == nil {
			// If the posting is abandoned, so too must be the work to be
			// posted.
			return workFn(ctx, Execution{})
		}

		var pushed bool
		withOutboxFn(ctx, func(outbox *Outbox) {
			pushed = q.TryPushBack(outbox, workFn)
			if !pushed && ex.Subscribe != nil {
				ex.Subscribe(&q.waiters.Coordinator)
				// Retry push after registering for notification in case
				// something happened in between
				pushed = q.TryPushBack(outbox, workFn)
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
