// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgfn

import (
	"context"
)

// A Gather is a function that processes the result of a completed
// Task. It receives the result and error values from the Task
// execution, allowing it to handle both successful and failed task executions.
//
// The Gather is called when completed task results are processed by
// Scatter, Pool.Gather, Pool.TryGather, Pool.GatherAll, or
// Pool.TryGatherAll. Execution of a Gather will block processing of
// subsequent task results, adding to backpressure. If such backpressure is
// undesirable, consider launching expensive gathering logic in another
// asynchronous task using Scatter. Unlike Task, it is safe to call
// Scatter from within a Gather.
//
// If multiple goroutines may call Scatter, Pool.Gather,
// Pool.TryGather, Pool.GatherAll, or Pool.TryGatherAll concurrently, then
// every Gather used in the job must be thread-safe.
type Gather[T any] = func(context.Context, T, error) error
