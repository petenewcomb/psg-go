// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
)

type GenericCombineOp[T, C any] psg.Combiner[result[T, C]]
type CombineOp[T any] = GenericCombineOp[T, context.Context]

// NewCombineOp creates a psg.Combiner that propagates workflow contexts
// through the combine chain. The user's Accumulator body routes
// downstream submissions itself; the Workflow ref/unref balancing
// happens inside the wrapped Accumulator.
//
// Named NewCombineOp (not NewCombiner) within psgwf to avoid clashing
// with the distinct Combiner interface alias in psgwf/combiner.go.
// Both psgwf names will be revisited when psgwf consolidates into Flow
// (Wave 7).
func NewCombineOp[T, C any](
	combinerPool *psg.CombinerPool,
	combinerFactory GenericCombinerFactory[T, C],
) GenericCombineOp[T, C] {
	return GenericCombineOp[T, C](psg.NewCombiner(
		combinerPool,
		wrapCombinerFactory(combinerFactory),
	))
}

func (c GenericCombineOp[T, C]) Start(ctx context.Context, pool *psg.TaskPool,
	wf *GenericWorkflow[C], taskFn GenericTaskFunc[T, C]) error {
	_, err := scatterTask(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psgfn.Task[result[T, C]]) (bool, error) {
			err := c.inner().Start(ctx, pool, taskFn)
			return err == nil, err
		},
	)
	return err
}

func (c GenericCombineOp[T, C]) TryStart(
	ctx context.Context,
	deadline time.Time,
	pool *psg.TaskPool,
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
) (bool, error) {
	return scatterTask(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psgfn.Task[result[T, C]]) (bool, error) {
			return c.inner().TryStart(ctx, deadline, pool, taskFn)
		},
	)
}

func (c GenericCombineOp[T, C]) inner() *psg.Combiner[result[T, C]] {
	return (*psg.Combiner[result[T, C]])(&c)
}
