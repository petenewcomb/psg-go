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

func (g *Governor) WrapDownstream(workFn WorkFunc, delayIncrement bool) WorkFunc {
	traceRegion := "workq.Governor.WrapDownstream"

	incremented := false
	if !delayIncrement {
		g.incrementDownstreamWaiters()
		incremented = true
	}

	return func(ctx context.Context, ex Execution) error {
		traceRegion := traceRegion + ".workFn"
		defer trace.StartRegion(ctx, traceRegion).End()

		started := false
		originalStarting := ex.Starting
		if originalStarting == nil {
			if incremented {
				g.decrementDownstreamWaiters()
			}
		} else {
			ex.Starting = func() {
				traceRegion := traceRegion + ".Starting"
				defer trace.StartRegion(ctx, traceRegion).End()

				started = true
				if incremented {
					incremented = false
					g.decrementDownstreamWaiters()
				}

				originalStarting()
			}
		}

		// If the work function does not call ex.Starting but could have, then
		// execution is deferred and we should increment the downstream waiter
		// count if we haven't already.
		defer func() {
			if !started && originalStarting != nil && !incremented {
				incremented = true
				g.incrementDownstreamWaiters()
			}
		}()

		return workFn(ctx, ex)
	}
}

//nolint:contextcheck // background context used only for tracing
func (g *Governor) incrementDownstreamWaiters() {
	traceRegion := "workq.Governor.IncrementDownstreamWaiters"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	newValue := g.downstream.Add(1)
	trace.Logf(context.Background(), traceRegion, "Governor=%p, newValue=%d", g, newValue)
}

//nolint:contextcheck // background context used only for tracing
func (g *Governor) decrementDownstreamWaiters() {
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
