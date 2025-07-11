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

	g.upstream.Init()

	trace.Logf(context.Background(), traceRegion, "Governor=%p, upstream=%p", g, &g.upstream)
}

//nolint:contextcheck // background context used only for tracing
func (g *Governor) WrapUpstream(workFn WorkFunc, shouldBlockFn func(context.Context) BlockFunc) WorkFunc {
	traceRegion := "workq.Governor.WrapUpstream"
	shouldWaitFn := func() bool {
		if g.downstream.Load() != 0 {
			trace.Logf(context.Background(), traceRegion, "applying backpressure")
			return true
		}
		return false
	}
	return g.upstream.Wrap(workFn, shouldWaitFn, shouldBlockFn)
}

//nolint:contextcheck // background context used only for tracing
func (g *Governor) IncrementDownstreamWaiters() {
	traceRegion := "workq.Governor.IncrementDownstreamWaiters"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	newValue := g.downstream.Add(1)
	trace.Logf(context.Background(), traceRegion, "Governor=%p, newValue=%d", g, newValue)
}

//nolint:contextcheck // background context used only for tracing
func (g *Governor) DecrementDownstreamWaiters() {
	traceRegion := "workq.Governor.DecrementDownstreamWaiters"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	newValue := g.downstream.Add(-1)
	trace.Logf(context.Background(), traceRegion, "Governor=%p, newValue=%d", g, newValue)
	switch {
	case newValue < 0:
		panic("unbalanced decrement detected")
	case newValue == 0:
		g.upstream.NotifyAll()
	}
}
