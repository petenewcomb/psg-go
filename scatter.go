// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
)

// Scatter initiates asynchronous execution of the provided task function in a
// new goroutine. After the task completes, the task's result and error will be
// passed to the provided gather function within a subsequent call to Scatter or
// any of the gathering methods of [Job].
//
// Scatter blocks to delay launch as needed to ensure compliance with the
// concurrency limit for the given pool. This backpressure is applied by
// gathering other tasks in the job until the a slot becomes available. The
// context passed to Scatter may be used to cancel (e.g., with a timeout) both
// gathering and launch, but only the context associated with the pool's job
// will be passed to the task.
//
// WARNING: Scatter must not be called from within a TaskFunc launched the same
// job as this may lead to deadlock when a concurrency limit is reached.
// Instead, call Scatter from the associated GatherFunc after the TaskFunc
// completes.
//
// Scatter will panic if the given pool is not yet associated with a job.
// Scatter returns a non-nil error if the context is canceled or if a non-nil
// error is returned by a gather function. If the returned error is non-nil, the
// task function supplied to the call will not have been launched will therefore
// also not result in a call to the supplied gather function.
//
// See [TaskFunc] and [GatherFunc] for important caveats and additional detail.
func Scatter[T any](
	ctx context.Context,
	pool *Pool,
	taskFunc TaskFunc[T],
	gatherFunc GatherFunc[T],
) error {
	_, err := scatter(ctx, pool, taskFunc, gatherFunc, true)
	return err
}

// TryScatter attempts to initiate asynchronous execution of the provided task
// function in a new goroutine like [Scatter]. Unlike Scatter, TryScatter will
// return instead of blocking if the given pool is already at its concurrency
// limit.
//
// Returns (true, nil) if the task was successfully launched, (false, nil) if
// the pool was at its limit, and (false, non-nil) if the task could not be
// launched for any other reason.
//
// See Scatter for more detail about how scattering works.
func TryScatter[T any](
	ctx context.Context,
	pool *Pool,
	taskFunc TaskFunc[T],
	gatherFunc GatherFunc[T],
) (bool, error) {
	return scatter(ctx, pool, taskFunc, gatherFunc, false)
}

func scatter[T any](
	ctx context.Context,
	pool *Pool,
	taskFunc TaskFunc[T],
	gatherFunc GatherFunc[T],
	block bool,
) (bool, error) {
	if taskFunc == nil {
		panic("task function must be non-nil")
	}
	if gatherFunc == nil {
		panic("gather function must be non-nil")
	}

	// Bind the task and gather functions together into a top-level function for
	// the new goroutine and hand it to the pool to launch.
	return pool.launch(ctx, func(ctx context.Context) {
		// Don't launch if the context has been canceled by the time the
		// goroutine starts.
		if ctx.Err() != nil {
			return
		}

		// Actually execute the task function. Since this is the top-level
		// function of a goroutine, if the task function panics the whole
		// program will terminate. The user can avoid this behavior by
		// recovering from the panic within the task function itself and then
		// returning normally with whatever results they want to pass to the
		// GatherFunc to represent the failure. We therefore do not defer
		// posting a gather to the job's channel or otherwise attempt to
		// maintain the integrity of the pool or overall job in case of task
		// panics.
		value, err := taskFunc(ctx)

		// Build the gather function, binding the supplied gatherFunc to the
		// result.
		gather := func(ctx context.Context) error {
			return gatherFunc(ctx, value, err)
		}

		// Post the gather to the gather channel.
		pool.postGather(gather)
	}, block)
}

// A TaskFunc represents a task to be executed asynchronously within the context
// of a [Pool]. It returns a result of type T and an error value. The provided
// context should be respected for cancellation. Any other inputs to the task
// are expected to be provided by specifying the TaskFunc as a [function
// literal] that references and therefore captures local variables via [lexical
// closure].
//
// Each TaskFunc is executed in a new goroutine spawned by the [Scatter]
// function and must therefore be thread-safe. This includes access to any
// captured variables.
//
// Also because they are executed in their own goroutines, if a TaskFunc panics,
// the whole program will terminate as per [Handling panics] in The Go
// Programming Language Specification. If you need to avoid this behavior,
// recover from the panic within the task function itself and then return
// whatever results you want to passed to the associated [GatherFunc] to
// represent the failure.
//
// WARNING: If a TaskFunc needs to spawn new tasks, it must not call [Scatter]
// directly as this would lead to deadlock when a concurrency limit is reached.
// Instead, [Scatter] should be called from the associated [GatherFunc] after
// the TaskFunc completes. [Scatter] attempts to recognize this situation and
// panic, but this detection works only if the context passed to [Scatter] is
// the one passed to the TaskFunc or is a subcontext thereof.
//
// A TaskFunc may however, create its own sub-[Job] within which to run
// concurrent tasks. This serves a different use case: tasks created in such a
// sub-job should complete or be canceled before the outer TaskFunc returns,
// while tasks spawned from a GatherFunc on behalf of a TaskFunc necessarily
// form a sequence (or pipeline). Both patterns can be used together as needed.
//
// [function literal]: https://go.dev/ref/spec#Function_literals
// [lexical closure]: https://en.wikipedia.org/wiki/Closure_(computer_programming)
// [Handling panics]: https://go.dev/ref/spec#Handling_panics
type TaskFunc[T any] = func(context.Context) (T, error)

// A GatherFunc is a function that processes the result of a completed
// [TaskFunc]. It receives the result and error values from the [TaskFunc]
// execution, allowing it to handle both successful and failed task executions.
//
// The GatherFunc is called when completed task results are processed by
// [Scatter], [Job.GatherOne], or [Job.GatherAll]. Execution of a GatherFunc
// will block processing of subsequent task results, adding to backpressure. If
// such backpressure is undesirable, consider launching expensive gathering
// logic in another asynchronous task using [Scatter]. Unlike [TaskFunc], it is
// safe to call [Scatter] from within a GatherFunc.
//
// If multiple goroutines may call [Scatter], [Job.GatherOne], or
// [Job.GatherAll] concurrently, then every GatherFunc used in the job must be
// thread-safe.
type GatherFunc[T any] = func(context.Context, T, error) error
