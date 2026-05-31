// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
)

type GenericGatherFunc[T, C any] func(context.Context, *GenericWorkflow[C], T, error) error
type GatherFunc[T any] = GenericGatherFunc[T, context.Context]

type GenericGatherOp[T, C any] psg.Gatherer[result[T, C]]
type Gatherer[T any] = GenericGatherOp[T, context.Context]

// NewGatherer creates a [psg.Gatherer] workalike set up to receive and propagate
// workflow context from tasks or combines.
func NewGatherer[T, C any](gatherFn GenericGatherFunc[T, C]) GenericGatherOp[T, C] {
	return GenericGatherOp[T, C](psg.NewGatherer(wrapGatherFunc(gatherFn)))
}

func (g GenericGatherOp[T, C]) inner() psg.Gatherer[result[T, C]] {
	return psg.Gatherer[result[T, C]](g)
}

func wrapGatherFunc[T, C any](gatherFn GenericGatherFunc[T, C]) psgfn.HandlerFunc[result[T, C]] {
	return func(ctx context.Context, res result[T, C], err error) error {
		// Reference happened in GenericTaskRunner.Start (in scatter.go).
		defer res.Workflow.unref(ctx)
		return gatherFn(ctx, res.Workflow, res.Value, err)
	}
}
