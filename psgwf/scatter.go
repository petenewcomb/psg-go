// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
)

// GenericTaskRunner dispatches a workflow-aware task onto a [psg.Pool].
// The runner owns the workflow ref/unref lifecycle: each [Start] takes
// one reference to the workflow; the matching unref happens when the
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
	wf       *GenericWorkflow[C]
	taskFn   GenericTaskFunc[T, C]
	opts     []psg.OpOption
	submitFn func(context.Context, *psg.Wave, T, error) error
}

// TaskRunner is the convenience alias for [GenericTaskRunner] over the
// default [Context] type. See [GenericTaskRunner] for semantics.
type TaskRunner[T any] = GenericTaskRunner[T, Context]

// NewGenericTaskRunner constructs a Wave-independent
// [GenericTaskRunner] that wraps the workflow context propagation and
// downstream submission to the supplied Gatherer sink. Pass psg op
// options (e.g. [psg.WithLimits]) via opts to throttle dispatch. The
// caller supplies a [psg.Wave] at each [GenericTaskRunner.Start] call.
func NewGenericTaskRunner[T, C any](
	sink GenericGatherOp[T, C],
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
	opts ...psg.OpOption,
) GenericTaskRunner[T, C] {
	return newGenericTaskRunner(wf, taskFn, opts, func(ctx context.Context, wave *psg.Wave, value T, err error) error {
		return sink.inner().SubmitErr(ctx, wave, result[T, C]{Workflow: wf, Value: value}, err)
	})
}

// NewGenericTaskRunnerForCombiner constructs a Wave-independent
// [GenericTaskRunner] that wraps workflow context propagation and
// forwards the result to the supplied Combiner sink. Pass psg op
// options (e.g. [psg.WithLimits]) via opts to throttle dispatch. The
// caller supplies a [psg.Wave] at each [GenericTaskRunner.Start] call.
// The Combiner itself is not Wave-bound; the Wave argument is ignored
// in the submit step.
func NewGenericTaskRunnerForCombiner[T, C any](
	sink GenericCombineOp[T, C],
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
	opts ...psg.OpOption,
) GenericTaskRunner[T, C] {
	combiner := sink.inner()
	return newGenericTaskRunner(wf, taskFn, opts, func(ctx context.Context, _ *psg.Wave, value T, err error) error {
		return combiner.SubmitErr(ctx, result[T, C]{Workflow: wf, Value: value}, err)
	})
}

func newGenericTaskRunner[T, C any](
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
	opts []psg.OpOption,
	submitFn func(context.Context, *psg.Wave, T, error) error,
) GenericTaskRunner[T, C] {
	return GenericTaskRunner[T, C]{
		wf:       wf,
		taskFn:   taskFn,
		opts:     opts,
		submitFn: submitFn,
	}
}

// Start dispatches the wrapped task on wave. The workflow ref taken
// here is balanced by the unref that fires when the downstream sink
// processes the result.
func (r GenericTaskRunner[T, C]) Start(ctx context.Context, wave *psg.Wave) error {
	r.wf.ref()
	wf := r.wf
	taskFn := r.taskFn
	submitFn := r.submitFn
	body := psgfn.TaskFunc0(func(ctx context.Context) error {
		value, taskErr := taskFn(ctx, wf)
		return submitFn(ctx, wave, value, taskErr)
	})
	inner := psg.NewTaskRunner0(body, r.opts...)
	err := inner.Start(ctx, wave)
	if err != nil {
		r.wf.unref(ctx)
	}
	return err
}
