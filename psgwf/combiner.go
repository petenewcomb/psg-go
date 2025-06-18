// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go/psgfn"
)

// Combiner is the interface for workflow-aware combiners.
// It mirrors psg.Combiner but uses the workflow Context type.
type Combiner[I, O any] interface {
	// Combine processes a single input and optionally emits outputs
	Combine(ctx context.Context, wf *Workflow, input I, inputErr error, emit Emit[O])

	// Flush emits any pending aggregated results. Since flush gets called
	// independent of any specific workflow, it receives a job context but no
	// workflow context. However, it must provide both to the emit function.
	// It's up to the implementation to decide what workflow context to use for
	// emit, but it cannot be nil.
	Flush(ctx context.Context, wf *Workflow, emit Emit[O])
}

type Emit[T any] = func(context.Context, *Workflow, T, error)

type CombinerFactory[I, O any] = func() Combiner[I, O]

func wrapCombinerFactory[I, O any](
	combinerFactory CombinerFactory[I, O],
) psgfn.CombinerFactory[result[I], result[O]] {
	return func() psgfn.Combiner[result[I], result[O]] {
		innerCombiner := combinerFactory()
		return psgfn.Combiner[result[I], result[O]]{
			CombineFn: func(ctx context.Context, input result[I], inputErr error, emit psgfn.Emit[result[O]]) {
				defer input.Workflow.unref(ctx)
				innerCombiner.Combine(ctx, input.Workflow, input.Value, inputErr, wrapEmit(emit))
			},
			FlushFn: func(ctx context.Context, emit psgfn.Emit[result[O]]) {
				wf := New(ctx)
				defer wf.unref(ctx)
				innerCombiner.Flush(ctx, wf, wrapEmit(emit))
			},
		}
	}
}

func wrapEmit[O any](emit psgfn.Emit[result[O]]) Emit[O] {
	return func(ctx context.Context, wf *Workflow, output O, outputErr error) {
		wf.ref()
		emit(ctx, result[O]{Workflow: wf, Value: output}, outputErr)
	}
}
