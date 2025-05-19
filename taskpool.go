// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/dynval"
	"github.com/petenewcomb/psg-go/internal/state"
	"github.com/petenewcomb/psg-go/internal/waitq"
)

// A TaskPool defines a virtual set of task execution slots and optionally places a
// limit on its size. Use [Scatter] to launch tasks into a TaskPool.
//
// TaskPools are created using [NewTaskPool] with a job and concurrency limit.
type TaskPool struct {
	j                *Job
	concurrencyLimit dynval.Value[int]
	inFlight         state.InFlightCounter
	waiterQueue      waitq.Queue
}

// Creates a new [TaskPool] bound to the specified job with the given concurrency limit.
// See [TaskPool.SetLimit] for the range of allowed values and their semantics.
//
// Panics if the job is nil or in the done state.
func NewTaskPool(job *Job, limit int) *TaskPool {
	if job == nil {
		panic("job must be non-nil")
	}

	// Check if the job is done
	job.panicIfDone()

	p := &TaskPool{
		j: job,
	}
	p.concurrencyLimit.Store(limit)
	p.waiterQueue.Init()
	return p
}

// job returns the Job associated with this TaskPool.
// Panics if the TaskPool was not created properly via NewTaskPool.
func (p *TaskPool) job() *Job {
	if p.j == nil {
		panic("task pool not bound to a job")
	}
	return p.j
}

// withBackpressureProvider returns a context with the backpressure provider for this TaskPool
func (p *TaskPool) withBackpressureProvider(ctx context.Context) context.Context {
	return p.j.withBackpressureProvider(ctx)
}

// Sets the active concurrency limit for the pool. A negative value means no
// limit (tasks will always be launched regardless of how many are currently
// running). Zero means no new tasks will be launched (i.e., [Scatter] will block
// indefinitely) until SetLimit is called with a non-zero value.
//
// This method is safe to call at any time. The new limit takes effect immediately
// for subsequent task launches and may unblock existing blocked Scatter calls.
func (p *TaskPool) SetLimit(limit int) {
	p.concurrencyLimit.Store(limit)
}

func (p *TaskPool) launch(ctx context.Context, applyBackpressure backpressureFunc, task boundTaskFunc) (bool, error) {
	j := p.j

	// Try to add to the pool
	limit, _ := p.concurrencyLimit.Load()
	if !p.incrementInFlightIfUnder(limit) {
		if applyBackpressure == nil {
			return false, nil
		}

		wait := func() (bool, error) {
			waiter := p.waiterQueue.Add()
			defer waiter.Close()

			// Check again after registering as a waiter, in case capacity
			// became available between the last check and this one.
			limit, limitChangeCh := p.concurrencyLimit.Load()
			if p.incrementInFlightIfUnder(limit) {
				return true, nil
			}
			err := applyBackpressure(ctx, waiter, limitChangeCh)
			return false, err
		}
		for {
			proceed, err := wait()
			if err != nil {
				return false, err
			}
			if proceed {
				break
			}
		}
	}

	j.startTask(ctx, func(ctx context.Context) {
		task(ctx, func() {
			// Decrement the task pool's in-flight count BEFORE waiting on the gather
			// channel. This makes it safe for gatherFunc to call `Scatter` with this
			// same `TaskPool` instance without deadlock, as there is guaranteed to be at
			// least one slot available.
			p.decrementInFlight()
		})
	})

	return true, nil
}

type backpressureFunc func(ctx context.Context, waiter waitq.Waiter, limitChangeCh <-chan struct{}) error

func (p *TaskPool) incrementInFlightIfUnder(limit int) bool {
	switch {
	case limit < 0:
		p.inFlight.Increment()
		return true
	case limit == 0:
		return false
	default:
		return p.inFlight.IncrementIfUnder(limit)
	}
}

func (p *TaskPool) decrementInFlight() {
	limit, _ := p.concurrencyLimit.Load()
	if p.inFlight.DecrementAndCheckIfUnder(limit) {
		// Signal any waiting tasks
		p.waiterQueue.Notify()
	}
}
