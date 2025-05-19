// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go"
)

type TaskFunc[T any] = func(context.Context, *Workflow) (T, error)

func wrapTaskFunc[T any](wf *Workflow, taskFn TaskFunc[T]) psg.TaskFunc[result[T]] {
	return func(ctx context.Context) (res result[T], err error) {
		res.Workflow = wf
		res.Value, err = taskFn(ctx, wf)
		return
	}
}

type result[T any] struct {
	Workflow *Workflow
	Value    T
}
