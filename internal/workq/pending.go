// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/rdvq"
)

// Specialization of [rdvq.Required] for [WorkFunc]. Same interface as Required
// but without the pool arguments, since Pending automatically uses a common
// global [rdvq.Pool] specialized for work functions.
type Pending struct {
	rdvq.Required[WorkFunc]
}

// See [rdvq.Required.Init]
func (q *Pending) Init() {
	q.Required.Init(pendingPool)
}

// See [rdvq.Outbox]
type Outbox = rdvq.Outbox[WorkFunc]

// See [rdvq.PushSelectFunc]
type PushSelectFunc = rdvq.PushSelectFunc[WorkFunc]

// See [rdvq.Required.PushBackFunc]
func (q *Pending) PushBackFunc(outbox *Outbox, workFn WorkFunc, selectFn PushSelectFunc) {
	q.Required.PushBackFunc(pendingPool, outbox, workFn, selectFn)
}

// See [rdvq.Required.PushBack]
func (q *Pending) PushBack(ctx context.Context, outbox *Outbox, workFn WorkFunc) error {
	return q.Required.PushBack(ctx, pendingPool, outbox, workFn)
}

// See [rdvq.Required.TryPushBack]
//
//nolint:contextcheck // background context used only for tracing
func (q *Pending) TryPushBack(outbox *Outbox, workFn WorkFunc) bool {
	traceRegion := "Pending.TryPushBack"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Pending=%p, outbox=%p", q, outbox)
	return q.Required.TryPushBack(pendingPool, outbox, workFn)
}

// See [rdvq.RequiredPopSelectFunc]
type PopSelectFunc = rdvq.RequiredPopSelectFunc[WorkFunc]

// See [rdvq.Required.PopFrontFunc]
func (q *Pending) PopFrontFunc(queueFn QueueWorkFunc, selectFn PopSelectFunc) {
	q.Required.PopFrontFunc(pendingPool, queueFn, selectFn)
}

// See [rdvq.Required.PopFront]
func (q *Pending) PopFront(ctx context.Context, queueFn QueueWorkFunc) error {
	return q.Required.PopFront(ctx, pendingPool, queueFn)
}

// See [rdvq.Required.TryPopFront]
//
//nolint:contextcheck // background context used only for tracing
func (q *Pending) TryPopFront() (WorkFunc, bool) {
	traceRegion := "Pending.TryPopFront"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Pending=%p", q)
	return q.Required.TryPopFront(pendingPool)
}

// See [rdvq.PushSelectFunc]
type WaitSelectFunc = rdvq.WaitSelectFunc

// See [rdvq.Required.PopFrontExcessFunc]
func (q *Pending) PopFrontExcessFunc(selectFn WaitSelectFunc) (WorkFunc, bool) {
	return q.Required.PopFrontExcessFunc(pendingPool, selectFn)
}

// See [rdvq.Required.PopFrontExcess]
func (q *Pending) PopFrontExcess(ctx context.Context) (WorkFunc, error) {
	return q.Required.PopFrontExcess(ctx, pendingPool)
}

var pendingPool = &rdvq.Pool[WorkFunc]{}
