// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/streampool"
)

type GenericSkimFunc[T, C any] func(context.Context, *GenericWorkflow[C], T, error) error
type SkimFunc[T any] = GenericSkimFunc[T, context.Context]

type GenericSkimOp[T, C any] streampool.Skimmer[result[T, C]]
type Skimmer[T any] = GenericSkimOp[T, context.Context]

// NewSkimmer creates a [streampool.Skimmer] workalike set up to receive and propagate
// workflow context from tasks or funnels. wave may be nil to defer wave
// binding to the dispatching ctx (see [streampool.NewSkimmer]).
func NewSkimmer[T, C any](wave *streampool.Wave, skimFn GenericSkimFunc[T, C]) GenericSkimOp[T, C] {
	return GenericSkimOp[T, C](streampool.NewSkimmer(wave, wrapSkimFunc(skimFn)))
}

func (g GenericSkimOp[T, C]) inner() streampool.Skimmer[result[T, C]] {
	return streampool.Skimmer[result[T, C]](g)
}

func wrapSkimFunc[T, C any](skimFn GenericSkimFunc[T, C]) streampool.HandlerFunc[result[T, C]] {
	return func(ctx context.Context, res result[T, C], err error) error {
		// Reference happened in GenericLauncher.Start (in scatter.go).
		defer res.Workflow.unref(ctx)
		return skimFn(ctx, res.Workflow, res.Value, err)
	}
}
