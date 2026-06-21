// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"

	"github.com/petenewcomb/streampool/internal/workq"
)

// boundTask is the internal interface every task work item satisfies.
// Launcher constructs boundTask values and feeds them through
// [Wave.newTaskWork] for execution on a worker.
type boundTask interface {
	Execute(ctx context.Context, group workq.GroupID, completedFn func())
	Free()
}
