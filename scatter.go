// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/workq"
)

// TaskPoolOrJob represents either a TaskPool or a Pool.
// When scattering directly to a Pool, tasks are not subject to any concurrency limit.
type TaskPoolOrJob interface {
	// getJob returns the Pool associated with this target
	getJob() *Pool
	// newScatterWork creates work for scattering a task
	newScatterWork(group workq.GroupID, deadline time.Time, task boundTask) workq.Work
}

type boundTask interface {
	Execute(ctx context.Context, group workq.GroupID, completedFn func(), taskWorkerSender *rdvq.Sender)
	Free()
}
