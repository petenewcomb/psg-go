// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/dynval"
	"github.com/petenewcomb/psg-go/internal/jobstate"
	"github.com/petenewcomb/psg-go/internal/opts"
	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/psgopt"
)

// A TaskPool defines a virtual set of task execution slots and optionally places a
// limit on its size. Use [Scatter] to launch tasks into a TaskPool.
//
// TaskPools are created using [NewTaskPool] with a job and concurrency limit.
type TaskPool struct {
	j              *Job
	maxConcurrency dynval.Value[int]
	inFlight       jobstate.InFlightCounter
	waiterQueue    rdvq.Waiters
}

// Creates a new [TaskPool] bound to the specified job with the given options.
// By default, the pool has unlimited concurrency (subject to other backpressure constraints).
// Use psgopt.WithMaxConcurrency() to set a specific limit.
//
// Panics if the job is nil or in the done state.
func NewTaskPool(job *Job, options ...psgopt.TaskPoolOption) *TaskPool {
	if job == nil {
		panic("job must be non-nil")
	}

	// Check if the job is done
	job.panicIfDone()

	p := &TaskPool{
		j: job,
	}
	p.waiterQueue.Init()

	// Set default unlimited concurrency
	p.maxConcurrency.Store(-1)

	// Apply user options
	p.SetOptions(options...)

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

// taskPoolConfigWrapper wraps a TaskPool to implement the taskPoolConfig interface for options
type taskPoolConfigWrapper struct {
	pool *TaskPool
}

func (w taskPoolConfigWrapper) SetMaxConcurrency(limit int) {
	w.pool.maxConcurrency.Store(limit)
}

// SetOptions applies the given configuration options to the pool.
// This method is safe to call at any time. Changes take effect immediately
// for subsequent task launches and may unblock existing blocked Scatter calls.
func (p *TaskPool) SetOptions(options ...psgopt.TaskPoolOption) {
	opts.ApplyToTaskPool(taskPoolConfigWrapper{pool: p}, options...)
}

func (p *TaskPool) launch(ctx context.Context, applyBackpressure backpressureFunc, task boundTask) (bool, error) {
	j := p.j

	// Try to add to the pool
	for {
		limit, limitChangeCh := p.maxConcurrency.Load()
		if p.incrementInFlightIfUnder(limit) {
			break
		}

		if applyBackpressure == nil {
			return false, nil
		}

		incrementSucceeded := false
		var err error
		waiter := p.waiterQueue.New(func() bool {
			// Check again after registering as a waiter, in case capacity
			// became available between the last check and this one. Note that
			// this overwrites the limitChangeCh at the top of the loop so that
			// the latest one is passed to applyBackpressure below.
			limit, limitChangeCh = p.maxConcurrency.Load()
			if p.incrementInFlightIfUnder(limit) {
				incrementSucceeded = true
				return false // waiter was not notified
			}
			return true
		})

		_, err = applyBackpressure(ctx, waiter, limitChangeCh)
		if err != nil {
			return false, err
		}

		if incrementSucceeded {
			break
		}

		// Even if the waiter was notified, we need to reattempt incrementing
		// the in-flight counter before proceding.
	}

	j.startTask(func(ctx context.Context, ctxWithBPFn func(backpressureProvider) context.Context, taskWorkerOutboxMap *outboxMap) {
		task(ctx, func() {
			// Decrement the task pool's in-flight count BEFORE waiting on the
			// gather channel. This makes it safe for gather functions to call
			// `Scatter` with this same `TaskPool` instance without deadlock, as
			// there is guaranteed to be at least one slot available.
			p.decrementInFlight()
		}, ctxWithBPFn, taskWorkerOutboxMap)
	})

	return true, nil
}

// Returns true if the waiter was notified, false otherwise.
type backpressureFunc func(ctx context.Context, waiter rdvq.Waiter, changeCh <-chan struct{}) (bool, error)

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
	limit, _ := p.maxConcurrency.Load()
	if p.inFlight.DecrementAndCheckIfUnder(limit) {
		// Signal any waiting tasks
		p.waiterQueue.Notify()
	}
}
