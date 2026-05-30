// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
)

// GenericTaskRunner dispatches a workflow-aware task onto a [psg.TaskPool].
// The runner owns the workflow ref/unref lifecycle: each [Start] takes one
// reference to the workflow; the matching unref happens when the
// downstream Gatherer (or Combiner) sink processes the result that the
// task produces.
//
// If [Start] fails (returns a non-nil error), the reference is released
// before Start returns — the task did not run. If the task panics or
// returns an error from its body without successfully forwarding to the
// configured sink, the workflow reference is still released by the
// framework error path (the sink receives the wrapped result with the
// task's error attached, and unrefs in its handler).
type GenericTaskRunner[T, C any] struct {
	inner psg.TaskRunner0
	wf    *GenericWorkflow[C]
}

// TaskRunner is the convenience alias for [GenericTaskRunner] over the
// default [Context] type. See [GenericTaskRunner] for semantics.
type TaskRunner[T any] = GenericTaskRunner[T, Context]

// NewGenericTaskRunner constructs a [GenericTaskRunner] that dispatches
// taskFn against pool, wrapping the workflow context propagation and
// downstream submission to the supplied Gatherer sink.
func NewGenericTaskRunner[T, C any](
	pool *psg.TaskPool,
	sink GenericGatherOp[T, C],
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
) GenericTaskRunner[T, C] {
	return newGenericTaskRunner(pool, wf, taskFn, func(ctx context.Context, value T, err error) error {
		return sink.inner().Submit(ctx, pool.Pool(), result[T, C]{Workflow: wf, Value: value}, err)
	})
}

// NewGenericTaskRunnerForCombiner constructs a [GenericTaskRunner] that
// dispatches taskFn against pool, wrapping workflow context propagation
// and forwarding the result to the supplied Combiner sink.
func NewGenericTaskRunnerForCombiner[T, C any](
	pool *psg.TaskPool,
	sink GenericCombineOp[T, C],
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
) GenericTaskRunner[T, C] {
	combiner := sink.inner()
	return newGenericTaskRunner(pool, wf, taskFn, func(ctx context.Context, value T, err error) error {
		return combiner.Submit(ctx, result[T, C]{Workflow: wf, Value: value}, err)
	})
}

func newGenericTaskRunner[T, C any](
	pool *psg.TaskPool,
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
	submitFn func(context.Context, T, error) error,
) GenericTaskRunner[T, C] {
	body := psgfn.TaskFunc0(func(ctx context.Context) error {
		value, taskErr := taskFn(ctx, wf)
		return submitFn(ctx, value, taskErr)
	})
	return GenericTaskRunner[T, C]{
		inner: psg.NewTaskRunner0(pool, body),
		wf:    wf,
	}
}

// Start dispatches the wrapped task. The workflow ref taken here is
// balanced by the unref that fires when the downstream sink processes
// the result.
func (r GenericTaskRunner[T, C]) Start(ctx context.Context) error {
	r.wf.ref()
	err := r.inner.Start(ctx)
	if err != nil {
		r.wf.unref(ctx)
	}
	return err
}
