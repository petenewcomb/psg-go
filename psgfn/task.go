// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgfn

import (
	"context"
)

// Task is the named function adapter for the no-input case: a body
// that runs without a value or upstream err. Satisfies
// [Handler][struct{}], so it plugs into a Launcher (the common use)
// or a Skimmer (rare; a value-less sink).
//
// Short-circuit semantics: Handle returns the upstream err
// immediately when it is non-nil, without invoking the wrapped
// closure. This matches the convenience-adapter contract — a Task
// closure that declined to accept an err arg almost certainly
// didn't plan to run when one was already in flight. Escape
// hatches:
//
//   - To run on err and handle it, use [ErrHandler].
//   - To run regardless of err (cleanup, always-fire side effects),
//     write a [HandlerFunc][struct{}] that ignores err, or
//     implement [Handler][struct{}] directly on a struct.
//
// There is no paired Task interface: no-arg bodies almost always
// close over state from the surrounding scope, so the struct-
// implementation pattern that justifies [Handler] as an interface
// doesn't pay off strongly for T = struct{}. Users who do want to
// implement the no-arg case on a struct write [Handler][struct{}]
// directly with Handle(ctx, _ struct{}, err error) error and choose
// their own err policy.
type Task func(context.Context) error

// Handle satisfies [Handler][struct{}]. Returns err immediately
// when non-nil; otherwise invokes the wrapped closure.
func (f Task) Handle(ctx context.Context, _ struct{}, err error) error {
	if err != nil {
		return err
	}
	return f(ctx)
}
