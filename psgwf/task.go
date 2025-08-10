// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go/psgfn"
)

type GenericTaskFunc[T, C any] func(context.Context, *GenericWorkflow[C]) (T, error)
type TaskFunc[T any] = GenericTaskFunc[T, Context]

func wrapTaskFunc[T, C any](wf *GenericWorkflow[C], taskFn GenericTaskFunc[T, C]) psgfn.Task[result[T, C]] {
	return func(ctx context.Context) (res result[T, C], err error) {
		res.Workflow = wf
		res.Value, err = taskFn(ctx, wf)
		return
	}
}

type result[T, C any] struct {
	Workflow *GenericWorkflow[C]
	Value    T
}
