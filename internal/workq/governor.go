// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/trace"
)

type Governor struct {
	upstream   rdvq.Notifier
	downstream atomic.Int32

	upstreamShouldWaitFn func() bool // avoid reallocating closure
}

//nolint:contextcheck // background context used only for tracing
func (g *Governor) Init() {
	traceRegion := "workq.Governor.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Governor=%p, upstream=%p", g, &g.upstream)

	g.upstream.Init()
	g.upstreamShouldWaitFn = g.upstreamShouldWait
}

type BlockBehavior struct {
	ShouldBlock func(context.Context) BlockFunc
}

func (g *Governor) Execute(ctx context.Context, ex Execution, deadline time.Time,
	behavior BlockBehavior, workFn WorkFunc) error {
	traceRegion := "workq.Governor.Execution"
	defer trace.StartRegion(ctx, traceRegion).End()
	wb := WaitBehavior{
		BlockBehavior: behavior,
		ShouldWait:    g.upstreamShouldWaitFn,
	}
	return ExecuteOrWait(ctx, ex, deadline, &g.upstream, wb, workFn)
}

//nolint:contextcheck // background context used only for tracing
func (g *Governor) upstreamShouldWait() bool {
	traceRegion := "workq.Governor.upstreamShouldWait"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	downstream := g.downstream.Load()
	if downstream > 0 {
		if trace.IsEnabled() {
			trace.Logf(context.Background(), traceRegion,
				"Governor=%p has %d downstream waiters, applying backpressure",
				g, downstream)
		}
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
	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion,
			"Governor=%p added downstream waiter, total now %d", g, downstream)
	}
}

//nolint:contextcheck // background context used only for tracing
func (g *Governor) decrementDownstream() {
	traceRegion := "workq.Governor.decrementDownstream"
	newValue := g.downstream.Add(-1)
	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion,
			"Governor=%p removed downstream waiter, total now %d", g, newValue)
	}
	switch {
	case newValue < 0:
		panic("unbalanced decrement detected")
	case newValue == 0:
		g.upstream.NotifyAll()
	case newValue%2 == 0:
		// This clause releases backpressure gradually rather than only all at
		// once, smoothing out governed work execution and thus reducing
		// resource demand spikes. This in turn reduces resource allocation and
		// increases utilization.
		g.upstream.Notify(nil)
	}
}
