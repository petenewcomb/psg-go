// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
)

// GenericLauncher dispatches a workflow-aware task onto a [psg.Pool].
// The runner owns the workflow ref/unref lifecycle: each [Start] takes
// one reference to the workflow; the matching unref happens when the
// downstream Skimmer (or Funnel) sink processes the result that the
// task produces.
//
// If [Start] fails (returns a non-nil error), the reference is released
// before Start returns — the task did not run. If the task panics or
// returns an error from its body without successfully forwarding to the
// configured sink, the workflow reference is still released by the
// framework error path (the sink receives the wrapped result with the
// task's error attached, and unrefs in its handler).
type GenericLauncher[T, C any] struct {
	wf       *GenericWorkflow[C]
	taskFn   GenericTaskFunc[T, C]
	opts     []psg.OpOption
	submitFn func(context.Context, *psg.Wave, T, error) error
}

// Launcher is the convenience alias for [GenericLauncher] over the
// default [Context] type. See [GenericLauncher] for semantics.
type Launcher[T any] = GenericLauncher[T, Context]

// NewGenericLauncher constructs a Wave-independent
// [GenericLauncher] that wraps the workflow context propagation and
// downstream submission to the supplied Skimmer sink. Pass psg op
// options (e.g. [psg.WithLimits]) via opts to throttle dispatch. The
// caller supplies a [psg.Wave] at each [GenericLauncher.Start] call.
func NewGenericLauncher[T, C any](
	sink GenericSkimOp[T, C],
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
	opts ...psg.OpOption,
) GenericLauncher[T, C] {
	return newGenericLauncher(wf, taskFn, opts, func(ctx context.Context, wave *psg.Wave, value T, err error) error {
		return sink.inner().SubmitErr(ctx, wave, result[T, C]{Workflow: wf, Value: value}, err)
	})
}

// NewGenericLauncherForFunnel constructs a Wave-independent
// [GenericLauncher] that wraps workflow context propagation and
// forwards the result to the supplied Funnel sink. Pass psg op
// options (e.g. [psg.WithLimits]) via opts to throttle dispatch. The
// caller supplies a [psg.Wave] at each [GenericLauncher.Start] call.
// The Funnel itself is not Wave-bound; the Wave argument is ignored
// in the submit step.
func NewGenericLauncherForFunnel[T, C any](
	sink GenericFunnelOp[T, C],
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
	opts ...psg.OpOption,
) GenericLauncher[T, C] {
	funnel := sink.inner()
	return newGenericLauncher(wf, taskFn, opts, func(ctx context.Context, _ *psg.Wave, value T, err error) error {
		return funnel.SubmitErr(ctx, result[T, C]{Workflow: wf, Value: value}, err)
	})
}

func newGenericLauncher[T, C any](
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
	opts []psg.OpOption,
	submitFn func(context.Context, *psg.Wave, T, error) error,
) GenericLauncher[T, C] {
	return GenericLauncher[T, C]{
		wf:       wf,
		taskFn:   taskFn,
		opts:     opts,
		submitFn: submitFn,
	}
}

// Start dispatches the wrapped task on wave. The workflow ref taken
// here is balanced by the unref that fires when the downstream sink
// processes the result.
func (r GenericLauncher[T, C]) Start(ctx context.Context, wave *psg.Wave) error {
	r.wf.ref()
	wf := r.wf
	taskFn := r.taskFn
	submitFn := r.submitFn
	body := psgfn.TaskFunc0(func(ctx context.Context) error {
		value, taskErr := taskFn(ctx, wf)
		return submitFn(ctx, wave, value, taskErr)
	})
	inner := psg.NewLauncher0(body, r.opts...)
	err := inner.Start(ctx, wave)
	if err != nil {
		r.wf.unref(ctx)
	}
	return err
}
