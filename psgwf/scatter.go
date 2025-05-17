// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go"
)

func scatter[T any](ctx context.Context, pool *psg.TaskPool, wf *Workflow, taskFn TaskFunc[T],
	launch func(context.Context, *psg.TaskPool, psg.TaskFunc[result[T]]) (bool, error),
) (bool, error) {

	// unref will happen in wrapGatherFunc or combinerAdapter.combine if launch
	// succeeds, the defer statement below if it does not.
	wf.ref()
	launched := false
	defer func() {
		if !launched {
			wf.unref(ctx)
		}
	}()

	var err error
	launched, err = launch(ctx, pool, wrapTaskFunc(wf, taskFn))
	return launched, err
}
