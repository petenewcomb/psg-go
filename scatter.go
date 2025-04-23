// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
)

func scatter[T any](
	ctx context.Context,
	pool *Pool,
	taskFunc TaskFunc[T],
	mode backpressureMode,
	postResult func(T, error),
) (bool, error) {
	if taskFunc == nil {
		panic("task function must be non-nil")
	}

	// Bind the task and gather functions together into a top-level function for
	// the new goroutine and hand it to the pool to launch.
	return pool.launch(ctx, mode, func(ctx context.Context) {
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

		// Decrement the pool's in-flight count BEFORE waiting on the gather
		// channel. This makes it safe for gatherFunc to call `Scatter` with this
		// same `Pool` instance without deadlock, as there is guaranteed to be at
		// least one slot available.
		pool.decrementInFlight()

		postResult(value, err)
	})
}
