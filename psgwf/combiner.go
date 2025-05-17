// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go"
)

// Combiner is the interface for workflow-aware combiners.
// It mirrors psg.Combiner but uses the workflow Context type.
type Combiner[I, O any] interface {
	// Combine processes a single input and optionally emits outputs
	Combine(ctx context.Context, wf *Workflow, input I, inputErr error, emit CombinerEmitFunc[O])

	// Flush emits any pending aggregated results. Since flush gets called
	// independent of any specific workflow, it receives a job context but no
	// workflow context. However, it must provide both to the emit function.
	// It's up to the implementation to decide what workflow context to use for
	// emit, but it cannot be nil.
	Flush(ctx context.Context, wf *Workflow, emit CombinerEmitFunc[O])
}

type CombinerEmitFunc[T any] = func(context.Context, *Workflow, T, error)

type CombinerFactory[I, O any] = func() Combiner[I, O]

func wrapCombinerFactory[I, O any](
	combinerFactory CombinerFactory[I, O],
) psg.CombinerFactory[result[I], result[O]] {
	return func() psg.Combiner[result[I], result[O]] {
		return &combinerAdapter[I, O]{
			inner: combinerFactory(),
		}
	}
}

// combinerAdapter adapts a psgwf.Combiner to work as a psg.Combiner
type combinerAdapter[I, O any] struct {
	inner Combiner[I, O]
}

func (c combinerAdapter[I, O]) Combine(ctx context.Context, input result[I], inputErr error, emit psg.CombinerEmitFunc[result[O]]) {
	defer input.Workflow.unref(ctx)
	c.inner.Combine(ctx, input.Workflow, input.Value, inputErr, wrapCombinerEmitFunc(emit))
}

func (c combinerAdapter[I, O]) Flush(ctx context.Context, emit psg.CombinerEmitFunc[result[O]]) {
	wf := New(ctx)
	defer wf.unref(ctx)
	c.inner.Flush(ctx, wf, wrapCombinerEmitFunc(emit))
}

func wrapCombinerEmitFunc[O any](emit psg.CombinerEmitFunc[result[O]]) CombinerEmitFunc[O] {
	return func(ctx context.Context, wf *Workflow, output O, outputErr error) {
		wf.ref()
		emit(ctx, result[O]{Workflow: wf, Value: output}, outputErr)
	}
}
