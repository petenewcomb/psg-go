// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
)

func scatter[T, C any](ctx context.Context, pool *psg.TaskPool, wf *GenericWorkflow[C], taskFn GenericTaskFunc[T, C],
	launchFn func(context.Context, *psg.TaskPool, psgfn.Task[result[T, C]]) (bool, error),
) (bool, error) {

	// unref will happen in wrapGather or combinerAdapter.combine if launch
	// succeeds, the defer statement below if it does not.
	wf.ref()
	launched := false
	defer func() {
		if !launched {
			wf.unref(ctx)
		}
	}()

	var err error
	launched, err = launchFn(ctx, pool, wrapTaskFunc(wf, taskFn))
	return launched, err
}

func scatterTask[T, C any](ctx context.Context, pool *psg.TaskPool, wf *GenericWorkflow[C],
	taskFn GenericTaskFunc[T, C],
	launchFn func(context.Context, *psg.TaskPool, psgfn.Task[result[T, C]]) (bool, error),
) (bool, error) {

	// unref will happen in wrapGather or combinerAdapter.combine if launch
	// succeeds, the defer statement below if it does not.
	wf.ref()
	launched := false
	defer func() {
		if !launched {
			wf.unref(ctx)
		}
	}()

	var err error
	launched, err = launchFn(ctx, pool, wrapTaskFunc(wf, taskFn))
	return launched, err
}
