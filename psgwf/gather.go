// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
)

type Gather[T any] = func(context.Context, *Workflow, T, error) error

type GatherOp[T any] psg.GatherOp[result[T]]

// NewGatherOp creates a [psg.GatherOp] workalike set up to receive and propagate
// workflow context from tasks or combines.
func NewGatherOp[T any](gatherFn Gather[T]) *GatherOp[T] {
	return (*GatherOp[T])(psg.NewGatherOp(wrapGather(gatherFn)))
}

func (g *GatherOp[T]) Scatter(ctx context.Context, pool *psg.TaskPool, wf *Workflow, taskFn Task[T]) error {
	_, err := scatter(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psgfn.Task[result[T]]) (bool, error) {
			err := g.inner().Scatter(ctx, pool, taskFn)
			return err == nil, err
		},
	)
	return err
}

func (g *GatherOp[T]) TryScatter(ctx context.Context, pool *psg.TaskPool, wf *Workflow, taskFn Task[T]) (bool, error) {
	return scatter(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psgfn.Task[result[T]]) (bool, error) {
			return g.inner().TryScatter(ctx, pool, taskFn)
		},
	)
}

func (g *GatherOp[T]) inner() *psg.GatherOp[result[T]] {
	return (*psg.GatherOp[result[T]])(g)
}

func wrapGather[T any](gatherFn Gather[T]) psgfn.Gather[result[T]] {
	return func(ctx context.Context, res result[T], err error) error {

		// Reference happened in scatter (in scatter.go)
		defer res.Workflow.unref(ctx)

		return gatherFn(ctx, res.Workflow, res.Value, err)
	}
}
