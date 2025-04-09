// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"sync/atomic"
)

// A Pool defines a virtual set of task execution slots and optionally places a
// limit on its size. Use [Scatter] to launch tasks into a Pool. A Pool must be
// bound to a [Job] or [SyncJob] before a task can be launched into it.
//
// The zero value of Pool is unbound and has a limit of zero. [NewPool]
// provides a convenient way to create a new pool with a non-zero limit.
type Pool struct {
	limit    atomic.Int64
	job      *Job
	inFlight inFlightCounter
}

// Creates a new [Pool] with the given limit. See [Pool.SetLimit] for the range
// of allowed values and their semantics.
func NewPool(limit int) *Pool {
	p := &Pool{}
	p.limit.Store(int64(limit))
	return p
}

// Sets the active concurrency limit for the pool. A negative value means no
// limit. Zero means no new tasks will be launched (i.e., [Scatter] will block
// indefinitely) until SetLimit is called with a non-zero value. SetLimit is
// always thread-safe, even for a Pool in a single-threaded [Job].
func (p *Pool) SetLimit(limit int) {
	if p.limit.Swap(int64(limit)) == 0 && limit != 0 {
		j := p.job
		if j != nil {
			j.wakeGatherers()
		}
	}
}

func (p *Pool) launch(ctx context.Context, task boundTaskFunc, block bool) (bool, error) {

	j := p.job
	if j == nil {
		panic("pool not bound to a job")
	}

	if j.isTaskContext(ctx) {
		// Don't launch if the provided context is a task context within the
		// current job, since that may lead to deadlock.
		panic("psg.Scatter called from within TaskFunc; move call to GatherFunc instead")
	}

	// Don't launch if the provided context has been canceled.
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// Don't launch if the job context has been canceled.
	if err := j.ctx.Err(); err != nil {
		return false, err
	}

	// Register the task with the job to make sure that any calls to gather will
	// block until the task is completed.
	j.inFlight.Increment()

	// Apply backpressure if launching a new task would exceed the pool's
	// concurrency limit.
	for !p.underLimit() {
		if !block {
			// Bookkeeping: we had incremented the job-scope count above
			// but are not going to launch after all.
			j.decrementInFlight()
			return false, nil
		}
		// Gather a result to make room to launch the new task. As long as there
		// wasn't an error, we don't care whether a task was actually gathered
		// by this call. Either way, it's time to re-check the in-flight count
		// for this pool.
		if _, err := j.GatherOne(ctx); err != nil {
			// Bookkeeping: we had incremented the job-scope count above
			// but are not going to launch after all.
			j.decrementInFlight()
			return false, err
		}
	}

	// Launch the task in a new goroutine.
	go task(j.ctx)

	return true, nil
}

type boundTaskFunc func(ctx context.Context)

func (p *Pool) underLimit() bool {
	limit := p.limit.Load()
	switch {
	case limit < 0:
		return true
	case limit == 0:
		return false
	default:
		return p.inFlight.IncrementIfUnder(int(limit))
	}
}

func (p *Pool) postGather(gather boundGatherFunc) {
	wrappedGather := func(ctx context.Context) error {
		// Decrement the pool's in-flight count BEFORE calling gatherFunc.
		// This makes it safe for gatherFunc to call `Scatter` with this
		// same `Pool` instance without deadlock, as there is guaranteed to
		// be at least one slot available.
		p.inFlight.Decrement()
		return gather(ctx)
	}

	j := p.job
	select {
	case j.gatherChannel <- wrappedGather:
	case <-j.ctx.Done():
	}
}
