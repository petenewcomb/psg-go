// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/jobstate"
	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/opts"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgopt"
)

// A TaskPool defines a virtual set of task execution slots and optionally places a
// limit on its size. Use [Scatter] to launch tasks into a TaskPool.
//
// TaskPools are created using [NewTaskPool] with a job and concurrency limit.
type TaskPool struct {
	job            *Pool
	maxConcurrency atomic.Int32
	inFlight       jobstate.InFlightCounter
	notifier       workq.Notifier

	decrementInFlightFn func() // avoid closure reallocation
}

// Creates a new [TaskPool] bound to the specified job with the given options.
// By default, the pool has unlimited concurrency (subject to other backpressure constraints).
// Use psgopt.WithMaxConcurrency() to set a specific limit.
//
// Panics if the job is nil or in the done state.
//
//nolint:contextcheck // background context used only for tracing
func NewTaskPool(job *Pool, options ...psgopt.TaskPoolOption) *TaskPool {
	traceRegion := "NewTaskPool"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	p := &TaskPool{
		job: job,
	}

	trace.Logf(context.Background(), traceRegion,
		"TaskPool=%p, job=%p, inFlight=%p, notifier=%p",
		p, job, &p.inFlight, &p.notifier)

	if job == nil {
		panic("job must be non-nil")
	}

	// Check if the job is done
	job.panicIfDone()

	p.decrementInFlightFn = p.decrementInFlight

	p.notifier.Init()

	// Set default unlimited concurrency
	p.maxConcurrency.Store(-1)

	// Apply user options
	p.SetOptions(options...)

	return p
}

func (p *TaskPool) getJob() *Pool {
	if p.job == nil {
		panic("task pool not bound to a job")
	}
	return p.job
}

// taskPoolConfigWrapper wraps a TaskPool to implement the taskPoolConfig interface for options
type taskPoolConfigWrapper struct {
	pool *TaskPool
}

func (w taskPoolConfigWrapper) SetMaxConcurrency(limit int) {
	if limit < -1 {
		panic(fmt.Sprintf("max concurrency limit %d is less than minimum allowed value of -1", limit))
	}
	if limit > math.MaxInt32 {
		panic(fmt.Sprintf("max concurrency limit %d exceeds maximum allowed value of %d", limit, math.MaxInt32))
	}
	oldLimit := w.pool.maxConcurrency.Swap(int32(limit))
	switch {
	case limit == -1:
		w.pool.notifier.NotifyAll()
	case oldLimit != -1:
		for range max(0, limit-int(oldLimit)) {
			w.pool.notifier.Notify(nil)
		}
	}
}

// SetOptions applies the given configuration options to the pool.
// This method is safe to call at any time. Changes take effect immediately
// for subsequent task launches and may unblock existing blocked Scatter calls.
func (p *TaskPool) SetOptions(options ...psgopt.TaskPoolOption) {
	opts.ApplyToTaskPool(taskPoolConfigWrapper{pool: p}, options...)
}

func (p *TaskPool) newScatterWork(group workq.GroupID, deadline time.Time, task boundTask) workq.Work {
	taskWork := p.job.newTaskWork(group, task, p.decrementInFlightFn)
	taskPostWork := p.job.newTaskPostWork(group, deadline, taskWork)
	return newTaskPoolScatterWork(p, deadline, taskPostWork)
}

type taskPoolScatterWork struct {
	workq.Work
	pool                *TaskPool
	deadline            time.Time
	inFlightIncremented bool
}

func newTaskPoolScatterWork(pool *TaskPool, deadline time.Time, poolScatterWork workq.Work) *taskPoolScatterWork {
	w := taskPoolScatterWorkPool.Get()
	w.Work = poolScatterWork
	w.pool = pool
	w.deadline = deadline
	return w
}

func (w *taskPoolScatterWork) Execute(ctx context.Context, ex workq.Execution) error {
	wb := workq.WaitBehavior{
		BlockBehavior: w.pool.job.protoBB,
		ShouldWait: func() bool {
			return w.pool.scatterShouldWait(w)
		},
	}

	defer func() {
		// If we didn't start, we must release our slot
		if !ex.Started() && w.inFlightIncremented {
			w.pool.decrementInFlight()
			w.inFlightIncremented = false
		}
	}()

	return workq.ExecuteOrWait(ctx, ex, w.deadline, &w.pool.notifier, wb,
		func(ctx context.Context, ex workq.Execution) error {
			return w.Work.Execute(ctx, ex)
		})
}

func (w *taskPoolScatterWork) Free() {
	w.Work.Free()
	taskPoolScatterWorkPool.Put(w)
}

var taskPoolScatterWorkPool = omnipool.For[taskPoolScatterWork]()

//nolint:contextcheck // background context used only for tracing
func (p *TaskPool) scatterShouldWait(w *taskPoolScatterWork) bool {
	traceRegion := "TaskPool.scatterShouldWait"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	// Make sure we increment only once for this invocation of the scatter work
	// function, though ShouldWait may be called multiple times. This is ok
	// because once we have incremented, we have reserved a slot for this work
	// item.
	if w.inFlightIncremented {
		return false
	}

	w.inFlightIncremented = p.incrementInFlight()
	return !w.inFlightIncremented
}

//nolint:contextcheck // background context used only for tracing
func (p *TaskPool) incrementInFlight() bool {
	traceRegion := "TaskPool.incrementInFlight"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	limit := p.maxConcurrency.Load()
	switch {
	case limit < 0:
		p.inFlight.Increment()
		return true
	case limit == 0:
		trace.Logf(context.Background(), traceRegion, "limit is zero; returning false")
		return false
	default:
		return p.inFlight.IncrementIfUnder(int(limit))
	}
}

//nolint:contextcheck // background context used only for tracing
func (p *TaskPool) decrementInFlight() {
	traceRegion := "TaskPool.decrementInFlight"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "TaskPool=%p", p)

	limit := p.maxConcurrency.Load()
	if p.inFlight.DecrementAndCheckIfUnder(int(limit)) {
		// Signal any waiting task
		p.notifier.Notify(nil)
	}
}
