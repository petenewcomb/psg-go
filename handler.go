// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
)

// Handler[T] is the universal user-supplied body interface — invoked
// by Launcher on a worker goroutine, by Skimmer during a Wave's
// drain, and by anywhere else the framework needs to dispatch a
// (value, err) pair to user code. Handle is called synchronously in
// the framework's chosen goroutine; the surrounding op type
// determines the runtime context.
//
// The (value, err) pair carries an upstream value and any error
// associated with producing it. Submit-without-err sites pass nil
// err; sites that explicitly forward an upstream error (e.g.,
// SubmitErr) pass the actual error. A handler decides what to do
// with each — log, transform, short-circuit, or propagate further
// downstream via its own Submit.
//
// If Handle panics, the whole program will terminate as per
// "Handling panics" in The Go Programming Language Specification.
// Recover within Handle and translate the panic into either a
// returned error or a downstream Submit of an error-flavored value.
//
// Handle and any state it touches must be safe for concurrent use,
// because the framework may invoke it from multiple goroutines.
type Handler[T any] interface {
	Handle(ctx context.Context, value T, err error) error
}

// HandlerFunc[T] is the canonical function adapter for Handler[T] —
// matches Handler.Handle exactly. Use this when you want a closure-
// based handler with both a value and an upstream err. For stateful
// handlers, implement Handler[T] directly on a struct so per-call
// state lives in fields and avoids closure allocations on the hot
// path.
type HandlerFunc[T any] func(ctx context.Context, value T, err error) error

// Handle satisfies [Handler[T]].
func (f HandlerFunc[T]) Handle(ctx context.Context, value T, err error) error {
	return f(ctx, value, err)
}

// ErrHandler is the err-receiving void case of [Handler]. Alias
// for [Handler][struct{}] — same underlying type as [Task] but
// named to signal the err-handling-sink role at the call site
// (typically: a Launcher or Skimmer constructed with
// [ErrHandlerFunc]).
type ErrHandler = Handler[struct{}]

// ErrHandlerFunc is the named func adapter for the no-value,
// with-err case: a body that receives only an upstream err.
// Satisfies [ErrHandler] / [Task] / [Handler][struct{}]. Named
// descriptively rather than "ErrTaskFunc" because "handle" reads
// naturally in both Launcher and Skimmer contexts.
//
// Unlike [TaskFunc], ErrHandlerFunc does NOT short-circuit on
// non-nil err — it always invokes the wrapped closure, passing err
// through. The user opted into the err-receiving signature
// precisely because they want the err to reach their code.
type ErrHandlerFunc func(ctx context.Context, err error) error

// Handle satisfies [Handler][struct{}].
func (f ErrHandlerFunc) Handle(ctx context.Context, _ struct{}, err error) error {
	return f(ctx, err)
}

// NewErrHandler is the convenience constructor for a closure-based
// err-receiving handler. Parallel to [NewTask] / [NewHandler];
// equivalent to [ErrHandlerFunc][type-cast] at the call site.
func NewErrHandler(handle func(ctx context.Context, err error) error) ErrHandlerFunc {
	return ErrHandlerFunc(handle)
}

// NewHandler is the type-inference-friendly constructor for a
// closure-based [Handler]. T is inferred from the closure's value
// parameter, sparing the user the [T] annotation. Returns the
// concrete [HandlerFunc][T] (which satisfies [Handler][T]).
func NewHandler[T any](
	handle func(ctx context.Context, value T, err error) error,
) HandlerFunc[T] {
	return HandlerFunc[T](handle)
}

// See [Task] for the no-input, no-err case: a named func adapter
// (`func(ctx) error`) satisfying Handler[struct{}] with
// short-circuit-on-err semantics.
