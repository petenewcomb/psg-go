// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
)

// Task is the no-input case of [Handler]: a body that runs without
// a value or upstream err. Alias for [Handler][struct{}], provided
// as a name to refer to the no-arg-Launcher shape directly.
type Task = Handler[struct{}]

// TaskFunc is the named function adapter for the no-input case.
// Satisfies [Task] (i.e. [Handler][struct{}]), so it plugs into a
// Launcher (the common use) or a Skimmer (rare; a value-less sink).
//
// Short-circuit semantics: Handle returns the upstream err
// immediately when it is non-nil, without invoking the wrapped
// closure. This matches the convenience-adapter contract — a
// TaskFunc closure that declined to accept an err arg almost
// certainly didn't plan to run when one was already in flight.
// Escape hatches:
//
//   - To run on err and handle it, use [ErrHandler].
//   - To run regardless of err (cleanup, always-fire side effects),
//     write a [HandlerFunc][struct{}] that ignores err, or
//     implement [Handler][struct{}] directly on a struct.
type TaskFunc func(context.Context) error

// Handle satisfies [Handler][struct{}]. Returns err immediately
// when non-nil; otherwise invokes the wrapped closure.
func (f TaskFunc) Handle(ctx context.Context, _ struct{}, err error) error {
	if err != nil {
		return err
	}
	return f(ctx)
}

// NewTask is the convenience constructor for a closure-based no-arg
// handler. Parallel to [NewHandler] / [NewAccumulator] /
// [NewAccumulatorFactory]; there is no type-parameter inference here
// (Task has no T), but the named constructor reads cleanly at the
// call site.
func NewTask(task func(context.Context) error) TaskFunc {
	return TaskFunc(task)
}
