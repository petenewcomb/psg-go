// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgfn

import (
	"context"
)

// Task0, Task[T], and Task2[T1, T2] are user-supplied work bodies that a
// TaskRunner dispatches on a worker goroutine. Tasks are argument-taking:
// inputs arrive through Run's parameters, and any results the task wants
// to deliver downstream are explicitly submitted to a Skimmer or
// Funnel from within Run.
//
// Run is invoked synchronously on a worker. Each invocation runs in its
// own goroutine, so Run and any state it touches (captured variables on
// closure-based tasks, fields on struct-based tasks) must be safe for
// concurrent use.
//
// If Run panics, the whole program will terminate as per "Handling
// panics" in The Go Programming Language Specification. To avoid this,
// recover within Run and translate the panic into either a returned
// error or a downstream Submit of an error-flavored value.
//
// If Run returns a non-nil error, the framework treats it as an
// unexpected failure and routes it through SkimAll on the owning
// Pool. Errors that are expected as part of business logic should be
// passed to Submit (or SubmitErr) on a sink the task captures rather
// than returned from Run.
//
// WARNING: A Task must not synchronously dispatch into the same Pool's
// worker by calling TaskRunner.Start, since this can deadlock when a
// concurrency limit is reached. Instead, Start should be called from
// the associated Skim function (or from Accumulate / Flush on an
// Accumulator) after the Task completes. Start attempts to recognize
// this situation and panic, but this detection works only if the ctx
// passed to Start descends from the ctx passed to Run.
//
// A Task may, however, create its own sub-Pool within which to run
// concurrent tasks. This serves a different use case: tasks created in
// such a sub-Pool should complete or be canceled before the outer Task
// returns, while tasks spawned from a Skim function on behalf of a
// Task necessarily form a sequence (or pipeline). Both patterns can be
// used together as needed.
type Task0 interface {
	Run(ctx context.Context) error
}

// Task[T] is the single-argument Task interface. See [Task0] for shared
// semantics.
type Task[T any] interface {
	Run(ctx context.Context, arg T) error
}

// Task2[T1, T2] is the two-argument Task interface. See [Task0] for
// shared semantics.
type Task2[T1, T2 any] interface {
	Run(ctx context.Context, arg1 T1, arg2 T2) error
}

// TaskFunc0 adapts a no-arg function into a [Task0]. Use this for the
// closure-based convenience case; for stateful tasks, implement [Task0]
// directly on a struct so per-invocation arguments avoid closure
// allocations.
type TaskFunc0 func(context.Context) error

// Run satisfies [Task0].
func (f TaskFunc0) Run(ctx context.Context) error { return f(ctx) }

// TaskFunc[T] adapts a function into a [Task]. See [TaskFunc0].
type TaskFunc[T any] func(context.Context, T) error

// Run satisfies [Task[T]].
func (f TaskFunc[T]) Run(ctx context.Context, arg T) error { return f(ctx, arg) }

// TaskFunc2[T1, T2] adapts a two-arg function into a [Task2]. See
// [TaskFunc0].
type TaskFunc2[T1, T2 any] func(context.Context, T1, T2) error

// Run satisfies [Task2[T1, T2]].
func (f TaskFunc2[T1, T2]) Run(ctx context.Context, arg1 T1, arg2 T2) error {
	return f(ctx, arg1, arg2)
}
