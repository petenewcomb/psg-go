// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"
)

// GenericTaskFunc is the user-supplied body of a workflow-aware task.
// Each invocation receives the workflow it was dispatched from; results
// are returned by value and routed downstream by the framework wrapper
// that owns the task ([GenericLauncher] etc.).
type GenericTaskFunc[T, C any] func(context.Context, *GenericWorkflow[C]) (T, error)
type TaskFunc[T any] = GenericTaskFunc[T, Context]

type result[T, C any] struct {
	Workflow *GenericWorkflow[C]
	Value    T
}
