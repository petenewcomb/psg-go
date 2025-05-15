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

	// Allows a pending operation to execute. Returns true if there might be
	// more pending operations to execute, false if not. Returns an error if the
	// yielding activity should be aborted (for instance because a context is
	// canceled).
	Yield(context.Context) (bool, error)

	// Returns true when the block was ended by waiter notification, false
	// otherwise. Returns an error if the waiting activity should be aborted
	// (for instance because a context is canceled).
	Block(ctx context.Context, waiter state.Waiter, limitChangeCh <-chan struct{}) (bool, error)
}

type backpressureProviderContextValueType struct{}

var backpressureProviderContextValueKey any = backpressureProviderContextValueType{}

func withBackpressureProvider(ctx context.Context, bp backpressureProvider) context.Context {
	return context.WithValue(ctx, backpressureProviderContextValueKey, bp)
}

func withTaskPoolBackpressureProvider(ctx context.Context, p *TaskPool) context.Context {
	j := p.job
	if j == nil {
		panic("task pool not bound to a job")
	}
	return withDefaultBackpressureProvider(ctx, j)
}

func withDefaultBackpressureProvider(ctx context.Context, j *Job) context.Context {
	switch bp := ctx.Value(backpressureProviderContextValueKey).(type) {
	case nil:
		if j == nil || includesJob(ctx, j, jobContextValueKey) {
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

func (bp defaultBackpressureProvider) Yield(ctx context.Context) (bool, error) {
	return bp.j.tryGatherOne(ctx)
}

func (bp defaultBackpressureProvider) Block(ctx context.Context, waiter state.Waiter, limitCh <-chan struct{}) (bool, error) {
	return bp.j.gatherOne(ctx, waiter, limitCh)
}
