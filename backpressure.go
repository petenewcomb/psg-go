// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"

	"github.com/petenewcomb/psg-go/internal/waitq"
)

type backpressureProviderKeyField bool
type backpressureProviderKey *backpressureProviderKeyField

type backpressureProvider interface {
	ForJob(*Job) bool
	Key() backpressureProviderKey

	// Allows a pending operation to execute. Returns true if there might be
	// more pending operations to execute, false if not. Returns an error if the
	// yielding activity should be aborted (for instance because a context is
	// canceled).
	Yield(vettedContext) (bool, error)

	// Returns true when the block was ended by waiter notification, false
	// otherwise. Returns an error if the waiting activity should be aborted
	// (for instance because a context is canceled).
	Block(ctx context.Context, waiter waitq.Waiter, changeCh <-chan struct{}) (bool, error)

	// Queues work to be executed in the appropriate context (job-level or combiner-level)
	QueueWork(workFn func(context.Context) error)
}

type backpressureProviderContextValueKeyType struct{}

var backpressureProviderContextValueKey any = backpressureProviderContextValueKeyType{}

func withBackpressureProvider(ctx context.Context, bp backpressureProvider) context.Context {
	return context.WithValue(ctx, backpressureProviderContextValueKey, bp)
}

func hasBackpressureProviderForJob(ctx context.Context, j *Job) bool {
	switch bp := ctx.Value(backpressureProviderContextValueKey).(type) {
	case nil:
		if j == nil || includesJob(ctx, j, jobContextValueKey) {
			panic("no psg backpressure provider available")
		}
	case backpressureProvider:
		if bp.ForJob(j) {
			return true
		}
	default:
		panic(fmt.Sprintf("unexpected backpressure provider type: %T", bp))
	}
	return false
}

func withNewBackpressureProvider(ctx context.Context, j *Job) context.Context {
	ctx, cancel := context.WithCancel(ctx)
	context.AfterFunc(j.ctx, cancel)
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
	j   *Job
	key backpressureProviderKeyField
}

func (bp defaultBackpressureProvider) ForJob(j *Job) bool {
	return bp.j == j
}

func (bp defaultBackpressureProvider) Key() backpressureProviderKey {
	return &bp.key
}

func (bp defaultBackpressureProvider) Yield(vetted vettedContext) (bool, error) {
	return bp.j.tryQueueGather(), nil
}

func (bp defaultBackpressureProvider) Block(ctx context.Context, waiter waitq.Waiter, limitCh <-chan struct{}) (bool, error) {
	return bp.j.gather(ctx, waiter, limitCh)
}

func (bp defaultBackpressureProvider) QueueWork(workFn func(context.Context) error) {
	bp.j.queueWork(workFn)
}
