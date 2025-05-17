// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go"
)

type Combine[I, O any] psg.Combine[result[I], result[O]]

// Combine creates a psg.Combine that propagates workflow contexts through the combine chain.
// This ensures workflow context values and cancellation flow from inputs to outputs.
func NewCombine[I, O any](
	gather *Gather[O],
	combinerPool *psg.CombinerPool,
	combinerFactory CombinerFactory[I, O],
) *Combine[I, O] {
	return (*Combine[I, O])(psg.NewCombine(
		(*psg.Gather[result[O]])(gather),
		combinerPool,
		wrapCombinerFactory(combinerFactory),
	))
}

func (c *Combine[I, O]) SetMinHoldTime(d time.Duration) {
	c.inner().SetMinHoldTime(d)
}

func (c *Combine[I, O]) SetMaxHoldTime(d time.Duration) {
	c.inner().SetMaxHoldTime(d)
}

func (c *Combine[I, O]) Scatter(ctx context.Context, pool *psg.TaskPool, wf Workflow, taskFn TaskFunc[I]) error {
	_, err := scatter(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psg.TaskFunc[result[I]]) (bool, error) {
			err := c.inner().Scatter(ctx, pool, taskFn)
			return err == nil, err
		},
	)
	return err
}

func (c *Combine[I, O]) TryScatter(ctx context.Context, pool *psg.TaskPool, wf Workflow, taskFn TaskFunc[I]) (bool, error) {
	return scatter(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psg.TaskFunc[result[I]]) (bool, error) {
			return c.inner().TryScatter(ctx, pool, taskFn)
		},
	)
}

func (c *Combine[I, O]) inner() *psg.Combine[result[I], result[O]] {
	return (*psg.Combine[result[I], result[O]])(c)
}
