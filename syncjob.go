// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"sync"
)

// SyncJob represents a thread-safe scatter-gather execution environment. Like
// [Job], it tracks tasks launched with [Scatter] across a set of [Pool]
// instances and provides methods for gathering their results. [SyncJob.Cancel]
// allows the caller to terminate the environment early and ensure cleanup when
// the environment is no longer needed. In addition to thread-safe versions of
// the gathering methods provided by Job, SyncJob provides multi-threaded
// gathering methods with configurable parallelism.
//
// A SyncJob must be created with [NewSyncJob], see that function for caveats
// and important details.
//
// See [Job] and [NewJob] if you need only concurrent task execution and not
// multi-threaded scattering and gathering.
type SyncJob struct {
	impl Job
}

// NewSyncJob creates a thread-safe scatter-gather execution environment with
// the specified context and set of pools. The context passed to NewSyncJob is
// used as the root of the context that will be passed to all task functions.
// (See [TaskFunc] and [SyncJob.Cancel] for more detail.)
//
// A call to NewSyncJob itself is not completely thread-safe, however, because
// it provides only best-effort checking that the provided pools have not been
// and are not concurrently being passed to another call to NewSyncJob (or
// [NewJob]). Though not guaranteed to work across goroutines, a panic will be
// raised if such pool sharing is detected. As long as the sets of pools are
// distinct, it is safe to call NewSyncJob concurrently from multiple
// goroutines. Each call will create an independent thread-safe scatter-gather
// execution environment.
//
// Each call to NewSyncJob should typically be followed by a deferred call to
// [SyncJob.Cancel] to ensure that an early exit from the calling function does
// not leave any outstanding tasks running.
func NewSyncJob(
	ctx context.Context,
	pools ...*Pool,
) *SyncJob {
	j := &SyncJob{}
	j.impl.init(ctx, newSyncInFlightCounter, pools...)
	return j
}

// See [Job.Cancel].
func (j *SyncJob) Cancel() {
	j.impl.Cancel()
}

// See [Job.GatherOne].
func (j *SyncJob) GatherOne(ctx context.Context) (bool, error) {
	return j.impl.GatherOne(ctx)
}

// See [Job.TryGatherOne].
func (j *SyncJob) TryGatherOne(ctx context.Context) (bool, error) {
	return j.impl.TryGatherOne(ctx)
}

// See [Job.GatherAll].
func (j *SyncJob) GatherAll(ctx context.Context) error {
	return j.impl.GatherAll(ctx)
}

// See [Job.TryGatherAll].
func (j *SyncJob) TryGatherAll(ctx context.Context) error {
	return j.impl.TryGatherAll(ctx)
}

// SyncJob.MultiGatherAll executes [SyncJob.GatherAll] in the specified number of
// new goroutines, continuing until there are no more tasks in flight or an
// error occurs. Only the first error detected will be returned, and an error
// found in one goroutine will be used to cancel all other goroutines.
// Regardless of error, SyncJob.MultiGatherAll always waits for all launched
// goroutines to terminate before returning.
//
// See [SyncJob.GatherAll] for more information.
func (j *SyncJob) MultiGatherAll(ctx context.Context, parallelism int) error {
	return j.multiGatherAll(ctx, parallelism, true)
}

// SyncJob.MultiTryGatherAll executes [SyncJob.TryGatherAll] in the specified number
// of new goroutines, continuing until there are no more task results
// immediately available or an error occurs. Only the first error detected will
// be returned, and an error found in one goroutine will be used to cancel all
// other goroutines. Regardless of error, MultiTryGatherAll always waits for all
// launched goroutines to terminate before returning.
//
// See [SyncJob.TryGatherAll] for more information.
func (j *SyncJob) MultiTryGatherAll(ctx context.Context, parallelism int) error {
	return j.multiGatherAll(ctx, parallelism, false)
}

func (j *SyncJob) multiGatherAll(ctx context.Context, parallelism int, block bool) error {
	if parallelism < 1 {
		panic("parallelism is less than one")
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)

	var wg sync.WaitGroup
	wg.Add(parallelism)
	for _ = range parallelism {
		go func() {
			defer wg.Done()
			err := j.impl.gatherAll(ctx, block)
			if err != nil {
				cancel(err)
			}
		}()
	}
	wg.Wait()
	return ctx.Err()
}
