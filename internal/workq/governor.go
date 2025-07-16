// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/trace"
)

type Governor struct {
	upstream   Waiters
	downstream atomic.Int32
}

//nolint:contextcheck // background context used only for tracing
func (g *Governor) Init() {
	traceRegion := "workq.Governor.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Governor=%p, upstream=%p", g, &g.upstream)

	g.upstream.Init()
}

type BlockBehavior interface {
	ShouldBlock(context.Context) BlockFunc
}

func (g *Governor) Execute(ctx context.Context, ex Execution, behavior BlockBehavior, workFn WorkFunc) error {
	traceRegion := "workq.Governor.Execution"
	defer trace.StartRegion(ctx, traceRegion).End()
	wb := &upstreamWaitBehavior{
		BlockBehavior: behavior,
		governor:      g,
	}
	return g.upstream.Execute(ctx, ex, wb, workFn)
}

type upstreamWaitBehavior struct {
	BlockBehavior
	governor *Governor
}

//nolint:contextcheck // background context used only for tracing
func (wb *upstreamWaitBehavior) ShouldWait() bool {
	traceRegion := "workq.upstreamWaitBehavior.ShouldWait"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	downstream := wb.governor.downstream.Load()
	if downstream > 0 {
		trace.Logf(context.Background(), traceRegion,
			"Governor=%p has %d downstream waiters, applying backpressure",
			wb.governor, downstream)
		return true
	}
	return false
}

type DownstreamWork struct {
	waiting *Governor
}

//nolint:contextcheck // background context used only for tracing
func (dw *DownstreamWork) Waiting(governor *Governor) {
	traceRegion := "workq.DownstreamWork.Waiting"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	if dw.waiting == nil {
		governor.incrementDownstream()
		dw.waiting = governor
	}
}

//nolint:contextcheck // background context used only for tracing
func (dw *DownstreamWork) Execute(ctx context.Context, ex Execution, governor *Governor, work Work) error {
	traceRegion := "workq.DownstreamWork.Execute"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	// If the work function does not call ex.Starting but could have, then
	// execution is deferred and we should increment the downstream waiter count
	// if we haven't already.
	started := false
	defer func() {
		if !started && ex.ShouldBlockOrSubscribe() {
			dw.Waiting(governor)
		}
	}()

	originalStarting := ex.Starting
	ex.Starting = func() {
		started = true
		dw.release()
		originalStarting()
	}

	return work.Execute(ctx, ex)
}

func (dw *DownstreamWork) Close() {
	traceRegion := "workq.DownstreamWork.Close"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	dw.release()
}

func (dw *DownstreamWork) release() {
	governor := dw.waiting
	if governor != nil {
		dw.waiting = nil
		governor.decrementDownstream()
	}
}

//nolint:contextcheck // background context used only for tracing
func (g *Governor) incrementDownstream() {
	traceRegion := "workq.Governor.incrementDownstream"
	downstream := g.downstream.Add(1)
	trace.Logf(context.Background(), traceRegion,
		"Governor=%p added downstream waiter, total now %d", g, downstream)
}

//nolint:contextcheck // background context used only for tracing
func (g *Governor) decrementDownstream() {
	traceRegion := "workq.Governor.decrementDownstream"
	newValue := g.downstream.Add(-1)
	trace.Logf(context.Background(), traceRegion,
		"Governor=%p removed downstream waiter, total now %d", g, newValue)
	switch {
	case newValue < 0:
		panic("unbalanced decrement detected")
	case newValue == 0:
		g.upstream.NotifyAll()
	}
}
