// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"

	"github.com/petenewcomb/psg-go/internal/state"
)

type backpressureProvider interface {
	ForJob(*Job) bool

	// Allows a pending non-blocking activity to execute. Returns true if one
	// was completed or false if none were available. Returns an error if the
	// existing activity should be aborted (for instance because a context is
	// canceled).
	Yield(ctx, ctx2 context.Context) (bool, error)

	// Returns true when the block was ended by a waiter notification or a
	// non-nil work function if the block was ended by work being available.
	// Returns an error if the existing activity should be aborted (for instance
	// because a context is canceled).
	Block(ctx, ctx2 context.Context, waiter state.Waiter, limitChangeCh <-chan struct{}) (blockResult, error)
}

type backpressureProviderContextValueType struct{}

var backpressureProviderContextValueKey any = backpressureProviderContextValueType{}

func withBackpressureProvider(ctx context.Context, bp backpressureProvider) context.Context {
	return context.WithValue(ctx, backpressureProviderContextValueKey, bp)
}

func withPoolBackpressureProvider(ctx context.Context, p *Pool) context.Context {
	j := p.job
	if j == nil {
		panic("pool not bound to a job")
	}
	return withDefaultBackpressureProvider(ctx, j)
}

func withDefaultBackpressureProvider(ctx context.Context, j *Job) context.Context {
	switch bp := ctx.Value(backpressureProviderContextValueKey).(type) {
	case nil:
		if j == nil || includesJob(ctx, j) {
			panic("no psg backpressure provider available")
		}
	case backpressureProvider:
		if bp.ForJob(j) {
			return ctx
		}
	default:
		panic(fmt.Sprintf("unexpected backpressure provider type: %T", bp))
	}
	return withBackpressureProvider(ctx, defaultBackpressureProvider{j: j})
}

func getBackpressureProvider(ctx context.Context, j *Job) backpressureProvider {
	bp := ctx.Value(backpressureProviderContextValueKey).(backpressureProvider)
	if !bp.ForJob(j) {
		panic("backpressure provider is for wrong job")
	}
	return bp
}

type defaultBackpressureProvider struct {
	j *Job
}

func (bp defaultBackpressureProvider) ForJob(j *Job) bool {
	return bp.j == j
}

func (bp defaultBackpressureProvider) Yield(ctx, ctx2 context.Context) (bool, error) {
	return bp.j.tryGatherOne(ctx, ctx2)
}

func (bp defaultBackpressureProvider) Block(ctx, ctx2 context.Context, waiter state.Waiter, limitCh <-chan struct{}) (blockResult, error) {
	res, err := bp.j.gatherOne(ctx, ctx2, waiter, limitCh)
	return blockResult{
		WaiterNotified: res.WaiterNotified,
		Work:           res.Work,
	}, err
}

type blockResult struct {
	WaiterNotified bool
	Work           workFunc
}

type workFunc func(context.Context) error
