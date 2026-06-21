// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/streampool"
)

// GenericLauncher dispatches a workflow-aware task onto a [streampool.Wave].
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
	wf    *GenericWorkflow[C]
	inner streampool.TaskLauncher
}

// Launcher is the convenience alias for [GenericLauncher] over the
// default [Context] type. See [GenericLauncher] for semantics.
type Launcher[T any] = GenericLauncher[T, Context]

// NewGenericLauncher binds a workflow-aware task to wave. wave may
// be nil to defer wave binding to the dispatching ctx at
// [GenericLauncher.Start] time (see [streampool.NewLauncher0]). Pass psg op
// options (e.g. [streampool.WithLimits]) via opts to throttle dispatch.
func NewGenericLauncher[T, C any](
	wave *streampool.Wave,
	sink GenericSkimOp[T, C],
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
	opts ...streampool.OpOption,
) GenericLauncher[T, C] {
	return newGenericLauncher(wave, wf, taskFn, opts, func(ctx context.Context, value T, err error) error {
		return sink.inner().SubmitResult(ctx, result[T, C]{Workflow: wf, Value: value}, err)
	})
}

// NewGenericLauncherForFunnel binds a workflow-aware task to wave,
// forwarding results to the supplied Funnel sink. wave may be nil to
// defer wave binding to the dispatching ctx. Pass psg op options
// (e.g. [streampool.WithLimits]) via opts to throttle dispatch.
func NewGenericLauncherForFunnel[T, C any](
	wave *streampool.Wave,
	sink GenericFunnelOp[T, C],
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
	opts ...streampool.OpOption,
) GenericLauncher[T, C] {
	funnel := sink.inner()
	return newGenericLauncher(wave, wf, taskFn, opts, func(ctx context.Context, value T, err error) error {
		return funnel.SubmitResult(ctx, result[T, C]{Workflow: wf, Value: value}, err)
	})
}

func newGenericLauncher[T, C any](
	wave *streampool.Wave,
	wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
	opts []streampool.OpOption,
	submitFn func(context.Context, T, error) error,
) GenericLauncher[T, C] {
	body := streampool.NewTask(func(ctx context.Context) error {
		value, taskErr := taskFn(ctx, wf)
		return submitFn(ctx, value, taskErr)
	})
	return GenericLauncher[T, C]{
		wf:    wf,
		inner: streampool.NewLauncher(wave, body, opts...),
	}
}

// Start dispatches the wrapped task on the bound Wave. The workflow
// ref taken here is balanced by the unref that fires when the
// downstream sink processes the result.
func (r GenericLauncher[T, C]) Start(ctx context.Context) error {
	r.wf.ref()
	err := r.inner.Start(ctx)
	if err != nil {
		r.wf.unref(ctx)
	}
	return err
}
