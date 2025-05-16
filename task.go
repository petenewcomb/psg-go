// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
)

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
