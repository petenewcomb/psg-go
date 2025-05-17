// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go"
)

type GatherFunc[T any] = func(context.Context, Workflow, T, error) error

type Gather[T any] psg.Gather[result[T]]

// NewGather creates a [psg.Gather] workalike set up to receive and propagate
// workflow context from tasks or combines.
func NewGather[T any](gatherFn GatherFunc[T]) *Gather[T] {
	return (*Gather[T])(psg.NewGather(wrapGatherFunc(gatherFn)))
}

func (g *Gather[T]) Scatter(ctx context.Context, pool *psg.TaskPool, wf Workflow, taskFn TaskFunc[T]) error {
	_, err := scatter(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psg.TaskFunc[result[T]]) (bool, error) {
			err := g.inner().Scatter(ctx, pool, taskFn)
			return err == nil, err
		},
	)
	return err
}

func (g *Gather[T]) TryScatter(ctx context.Context, pool *psg.TaskPool, wf Workflow, taskFn TaskFunc[T]) (bool, error) {
	return scatter(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psg.TaskFunc[result[T]]) (bool, error) {
			return g.inner().TryScatter(ctx, pool, taskFn)
		},
	)
}

func (g *Gather[T]) inner() *psg.Gather[result[T]] {
	return (*psg.Gather[result[T]])(g)
}

func wrapGatherFunc[T any](gatherFn GatherFunc[T]) psg.GatherFunc[result[T]] {
	return func(ctx context.Context, res result[T], err error) error {

		// Reference happened in scatter (in scatter.go)
		defer res.Workflow.Unref()

		return gatherFn(ctx, res.Workflow, res.Value, err)
	}
}
