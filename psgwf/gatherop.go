// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
)

type GenericGatherFunc[T, C any] func(context.Context, *GenericWorkflow[C], T, error) error
type GatherFunc[T any] = GenericGatherFunc[T, context.Context]

type GenericGatherOp[T, C any] psg.GatherOp[result[T, C]]
type GatherOp[T any] = GenericGatherOp[T, context.Context]

// NewGatherOp creates a [psg.GatherOp] workalike set up to receive and propagate
// workflow context from tasks or combines.
func NewGatherOp[T, C any](gatherFn GenericGatherFunc[T, C]) GenericGatherOp[T, C] {
	return GenericGatherOp[T, C](psg.NewGatherOp(wrapGatherFunc(gatherFn)))
}

func (g GenericGatherOp[T, C]) Scatter(ctx context.Context, pool *psg.TaskPool,
	wf *GenericWorkflow[C], taskFn GenericTaskFunc[T, C]) error {
	_, err := scatter(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psgfn.Task[result[T, C]]) (bool, error) {
			err := g.inner().Scatter(ctx, pool, taskFn)
			return err == nil, err
		},
	)
	return err
}

func (g GenericGatherOp[T, C]) TryScatter(ctx context.Context, deadline time.Time,
	pool *psg.TaskPool, wf *GenericWorkflow[C], taskFn GenericTaskFunc[T, C]) (bool, error) {
	return scatter(ctx, pool, wf, taskFn,
		func(ctx context.Context, pool *psg.TaskPool, taskFn psgfn.Task[result[T, C]]) (bool, error) {
			return g.inner().TryScatter(ctx, deadline, pool, taskFn)
		},
	)
}

func (g GenericGatherOp[T, C]) inner() psg.GatherOp[result[T, C]] {
	return psg.GatherOp[result[T, C]](g)
}

func wrapGatherFunc[T, C any](gatherFn GenericGatherFunc[T, C]) psgfn.Gather[result[T, C]] {
	return func(ctx context.Context, res result[T, C], err error) error {
		// Reference happened in scatter (in scatter.go)
		defer res.Workflow.unref(ctx)
		return gatherFn(ctx, res.Workflow, res.Value, err)
	}
}
