// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgfn

import (
	"context"
)

// A Skim is a function that processes the result of a completed
// Task. It receives the result and error values from the Task
// execution, allowing it to handle both successful and failed task executions.
//
// The Skim is called when completed task results are processed by
// Start, Pool.Skim, Pool.TrySkim, Pool.SkimAll, or
// Pool.TrySkimAll. Execution of a Skim will block processing of
// subsequent task results, adding to backpressure. If such backpressure is
// undesirable, consider launching expensive skimming logic in another
// asynchronous task using Start. Unlike Task, it is safe to call
// Start from within a Skim.
//
// If multiple goroutines may call Start, Pool.Skim,
// Pool.TrySkim, Pool.SkimAll, or Pool.TrySkimAll concurrently, then
// every Skim used in the job must be thread-safe.
type Skim[T any] = func(context.Context, T, error) error
