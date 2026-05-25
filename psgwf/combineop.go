// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
)

type GenericCombineOp[I, O, C any] psg.Combiner[result[I, C], result[O, C]]
type CombineOp[I, O any] = GenericCombineOp[I, O, context.Context]

// NewCombineOp creates a psg.Combiner that propagates workflow contexts through the combine chain.
// This ensures workflow context values and cancellation flow from inputs to outputs.
//
// Named NewCombineOp (not NewCombiner) within psgwf to avoid clashing with the
// distinct Combiner interface alias in psgwf/combiner.go. Both psgwf names will
// be revisited when the broader Combiner/Accumulator renames land.
func NewCombineOp[I, O, C any](
	gatherer GenericGatherOp[O, C],
	combinerPool *psg.CombinerPool,
	combinerFactory GenericCombinerFactory[I, O, C],
) GenericCombineOp[I, O, C] {
	return GenericCombineOp[I, O, C](psg.NewCombiner(
		psg.Gatherer[result[O, C]](gatherer),
		combinerPool,
		wrapCombinerFactory(combinerFactory),
	))
}

func (c GenericCombineOp[I, O, C]) Start(ctx context.Context, pool *psg.TaskPool,
	wf *GenericWorkflow[C], taskFn GenericTaskFunc[I, C]) error {
	_, err := scatterTask(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psgfn.Task[result[I, C]]) (bool, error) {
			err := c.inner().Start(ctx, pool, taskFn)
			return err == nil, err
		},
	)
	return err
}

func (c GenericCombineOp[I, O, C]) TryStart(
	ctx context.Context,
	deadline time.Time,
	pool *psg.TaskPool,
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[I, C],
) (bool, error) {
	return scatterTask(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psgfn.Task[result[I, C]]) (bool, error) {
			return c.inner().TryStart(ctx, deadline, pool, taskFn)
		},
	)
}

func (c GenericCombineOp[I, O, C]) inner() *psg.Combiner[result[I, C], result[O, C]] {
	return (*psg.Combiner[result[I, C], result[O, C]])(&c)
}
