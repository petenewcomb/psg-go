// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/state"
)

// A TaskPool defines a virtual set of task execution slots and optionally places a
// limit on its size. Use [Scatter] to launch tasks into a TaskPool.
//
// TaskPools are created using [NewTaskPool] with a job and concurrency limit.
type TaskPool struct {
	concurrencyLimit state.DynamicValue[int]
	job              *Job
	inFlight         state.InFlightCounter
	waiterQueue      state.WaiterQueue
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
		job: job,
	}
	p.concurrencyLimit.Store(limit)
	return p
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
	j := p.job

	// Don't launch if the provided context has been canceled.
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// Don't launch if the job context has been canceled.
	if err := j.ctx.Err(); err != nil {
		return false, err
	}

	// Try to add to the pool
	limit, _ := p.concurrencyLimit.Load()
	if !p.incrementInFlightIfUnder(limit) {
		if applyBackpressure == nil {
			return false, nil
		}

		type waitResult struct {
			Proceed bool
			Work    workFunc
		}
		wait := func() (waitResult, error) {
			waiter := p.waiterQueue.Add()
			defer waiter.Close()

			// Check again after registering as a waiter, in case capacity
			// became available between the last check and this one.
			limit, limitChangeCh := p.concurrencyLimit.Load()
			if p.incrementInFlightIfUnder(limit) {
				return waitResult{Proceed: true}, nil
			}
			work, err := applyBackpressure(ctx, ctx, waiter, limitChangeCh)
			return waitResult{Work: work}, err
		}
		for {
			res, err := wait()
			if err != nil {
				return false, err
			}
			if res.Work != nil {
				if err := res.Work(ctx); err != nil {
					return false, err
				}
			}
			if res.Proceed {
				break
			}
		}
	}

	// Launch the task in a new goroutine.
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		task(j.ctx)
	}()

	return true, nil
}

type backpressureFunc func(ctx, ctx2 context.Context, waiter state.Waiter, limitChangeCh <-chan struct{}) (workFunc, error)

type boundTaskFunc func(ctx context.Context)

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
