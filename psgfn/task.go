// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgfn

import (
	"context"
)

// A Task represents a task to be executed asynchronously within the context
// of a Pool. It returns a result of type T and an error value. The provided
// context should be respected for cancellation. Any other inputs to the task
// are expected to be provided by specifying the Task as a function
// literal that references and therefore captures local variables via lexical
// closure.
//
// Each Task is executed in a new goroutine spawned by the Scatter
// function and must therefore be thread-safe. This includes access to any
// captured variables.
//
// Also because they are executed in their own goroutines, if a Task panics,
// the whole program will terminate as per Handling panics in The Go
// Programming Language Specification. If you need to avoid this behavior,
// recover from the panic within the task function itself and then return
// whatever results you want to passed to the associated Gather function to
// represent the failure.
//
// WARNING: If a Task needs to spawn new tasks, it must not call Scatter
// directly as this would lead to deadlock when a concurrency limit is reached.
// Instead, Scatter should be called from the associated Gather function after
// the Task completes. Scatter attempts to recognize this situation and
// panic, but this detection works only if the context passed to Scatter is
// the one passed to the Task or is a subcontext thereof.
//
// A Task may however, create its own sub-Pool within which to run
// concurrent tasks. This serves a different use case: tasks created in such a
// sub-job should complete or be canceled before the outer Task returns,
// while tasks spawned from a Gather function on behalf of a Task necessarily
// form a sequence (or pipeline). Both patterns can be used together as needed.
type Task[T any] = func(context.Context) (T, error)
