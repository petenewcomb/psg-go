// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
	"github.com/petenewcomb/psg-go/psgopt"
)

type CombineOp[I, O any] psg.CombineOp[result[I], result[O]]

// NewCombineOp creates a psg.CombineOp that propagates workflow contexts through the combine chain.
// This ensures workflow context values and cancellation flow from inputs to outputs.
func NewCombineOp[I, O any](
	gather *GatherOp[O],
	combinerPool *psg.CombinerPool,
	combinerFactory CombinerFactory[I, O],
	options ...psgopt.CombineOpOption,
) *CombineOp[I, O] {
	return (*CombineOp[I, O])(psg.NewCombineOp(
		(*psg.GatherOp[result[O]])(gather),
		combinerPool,
		wrapCombinerFactory(combinerFactory),
		options...,
	))
}

func (c *CombineOp[I, O]) SetOptions(options ...psgopt.CombineOpOption) {
	c.inner().SetOptions(options...)
}

func (c *CombineOp[I, O]) Scatter(ctx context.Context, pool *psg.TaskPool, wf *Workflow, taskFn Task[I]) error {
	_, err := scatterTask(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psgfn.Task[result[I]]) (bool, error) {
			err := c.inner().Scatter(ctx, pool, taskFn)
			return err == nil, err
		},
	)
	return err
}

func (c *CombineOp[I, O]) TryScatter(ctx context.Context, pool *psg.TaskPool, wf *Workflow, taskFn Task[I]) (bool, error) {
	return scatterTask(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psgfn.Task[result[I]]) (bool, error) {
			return c.inner().TryScatter(ctx, pool, taskFn)
		},
	)
}

func (c *CombineOp[I, O]) inner() *psg.CombineOp[result[I], result[O]] {
	return (*psg.CombineOp[result[I], result[O]])(c)
}
