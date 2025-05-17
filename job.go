// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"maps"
	"sync"

	"github.com/petenewcomb/psg-go/internal/nbcq"
	"github.com/petenewcomb/psg-go/internal/state"
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/petenewcomb/psg-go/internal/waitq"
)

// Job represents a scatter-gather execution environment. It tracks tasks
// launched with [Scatter] across a set of [TaskPool] instances and provides methods
// for gathering their results. [Job.Cancel] and [Job.CancelAndWait] allow the
// caller to terminate the environment early and ensure cleanup when the
// environment is no longer needed.
//
// A Job must be created with [NewJob], see that function for caveats and
// important details.
type Job struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	gatherChan chan boundGatherFunc
	wg         sync.WaitGroup
	state      state.JobState
	timerPool  timerp.Pool

	// workQueue must be thread-safe only to support multiple goroutines
	// potentially calling the job's gather methods concurrently, including
	// indirectly through Gather scatter methods. An alternative approach to
	// further leverage context values (see gatherContextValueKey) to hold
	// goroutine-specific work queues is not workable because the work queue
	// must persist across top-level calls to gather so that an error resulting
	// from processing one work item can be reported immediately while leaving
	// remaining work items in queue for future calls to gather methods to
	// process. These top-level calls are passed contexts not produced by other
	// psg functions, and to require that they be so would complicate the API in
	// ways that users should not need to understand. A true goroutine-local
	// storage capability would be a perfect fit here.
	workQueue nbcq.Queue[gatherWorkFunc]
}

type boundGatherFunc = func(ctx context.Context) error

type gatherWorkFunc func(context.Context) error

// NewJob creates an independent scatter-gather execution environment with the
// specified context. The context passed to NewJob is used as the root of the
// context that will be passed to all task functions. (See [TaskFunc] and
// [Job.Cancel] for more detail.)
//
// Use [NewTaskPool] to create task pools bound to this job.
//
// Each call to NewJob should typically be followed by a deferred call to
// [Job.CancelAndWait] to ensure that an early exit from the calling function
// does not leave any outstanding goroutines.
func NewJob(ctx context.Context) *Job {
	ctx, cancelFunc := context.WithCancel(ctx)
	j := &Job{
		cancelFunc: cancelFunc,
		gatherChan: make(chan boundGatherFunc),
	}
	j.ctx = withJob(ctx, j)
	j.state.Init()
	j.timerPool.Init()
	j.workQueue.Init()
	return j
}

type jobContextValueKeyType struct{}

var jobContextValueKey any = jobContextValueKeyType{}

func withJob(ctx context.Context, j *Job) context.Context {
	oldValue := ctx.Value(taskContextValueKey)
	if oldValue == nil {
		oldValue = ctx.Value(jobContextValueKey)
	}

	// Accumulate the jobs to which the context belongs but avoid creating a
	// collection unless it's needed.
	var newValue any
	switch oldValue := oldValue.(type) {
	case nil:
		newValue = j
	case *Job:
		if oldValue == j {
			return ctx
		}
		newValue = map[*Job]struct{}{
			oldValue: {},
			j:        {},
		}
	case map[*Job]struct{}:
		if _, ok := oldValue[j]; ok {
			return ctx
		}
		newValue := make(map[*Job]struct{}, len(oldValue)+1)
		maps.Copy(newValue, oldValue)
		newValue[j] = struct{}{}
	default:
		panic("unexpected job context value type")
	}
	return context.WithValue(ctx, jobContextValueKey, newValue)
}

func includesJob(ctx context.Context, j *Job, keys ...any) bool {
	for _, key := range keys {
		switch v := ctx.Value(key).(type) {
		case nil:
		case *Job:
			if v == j {
				return true
			}
		case map[*Job]struct{}:
			_, ok := v[j]
			if ok {
				return true
			}
		default:
			panic("unexpected job context value type")
		}
	}
	return false
}

// Cancel terminates any in-flight tasks and forfeits any ungathered results.
// Outstanding calls to [Scatter], [Job.GatherOne], [Job.TryGatherOne],
// [Job.GatherAll], or [Job.TryGatherAll] using the job or any of its task pools will
// fail with [context.Canceled] or other error returned by a [GatherFunc].
//
// While Cancel always returns immediately, any running [TaskFunc] or
// [GatherFunc] will delay termination of their independent goroutine or caller
// until it returns. This method cancels the context passed to each [TaskFunc],
// but not the context passed to each [GatherFunc]. Gather functions instead
// receive the context passed to the calling [Scatter], [Job.GatherOne],
// [Job.TryGatherOne], [Job.GatherAll], or [Job.TryGatherAll] function. If it is
// desirable to transmit a cancelation signal to a running [GatherFunc], one
// must also cancel any contexts being passed to those callers.
//
// Cancel is always thread-safe and calling it more than once has no additional
// effect.
func (j *Job) Cancel() {
	j.cancelFunc()
}

// CancelAndWait cancels like [Job.Cancel], but then blocks until any
// outstanding task goroutines exit.
func (j *Job) CancelAndWait() {
	j.Cancel()
	j.wg.Wait()
}

// GatherOne processes at most a single result from a task previously launched
// in one of the [Job]'s task pools via [Scatter]. It will block until a completed
// task is available, the provided context or job is canceled, or another event
// causes a wake-up (e.g. a call to [TaskPool.SetLimit]).
// If the job is closed and no tasks remain in flight, it will return immediately.
// See [Job.TryGatherOne] for a non-blocking alternative.
//
// Returns a boolean flag indicating whether a result was processed and an error
// if one occurred:
//
//   - true, nil: a task completed and was successfully gathered
//   - true, non-nil: a task completed but the gather function returned a
//     non-nil error
//   - false, nil: the job is done and therefore nothing is left to gather
//   - false, non-nil: the argument or job-internal context was canceled
//
// If all gather functions are thread-safe, then GatherOne is thread-safe and
// may be called concurrently from multiple goroutines. Blocking and
// non-blocking calls may also be mixed, as can calls to any of the other gather
// methods.
//
// NOTE: If a task result is gathered, this method will call the task's
// [GatherFunc] and wait until it returns.
func (j *Job) GatherOne(ctx context.Context) (bool, error) {
	ctx = withDefaultBackpressureProvider(ctx, j)
	return j.gatherOneAndDoTheWork(ctx)
}

func (j *Job) vetGather(ctx context.Context) {
	if includesJob(ctx, j, taskContextValueKey) {
		// Don't launch if the provided context is a task context within the
		// current job, since that may lead to deadlock.
		panic("Gather called from within TaskFunc of the same or a parent Job")
	}
}

type gatherContextValueKeyType struct{}

var gatherContextValueKey gatherContextValueKeyType

func (j *Job) inGather(ctx context.Context) bool {
	inGather := false
	switch v := ctx.Value(gatherContextValueKey).(type) {
	case *Job:
		inGather = v == j
	case nil:
	default:
		panic("unexpected gather context value type")
	}
	return inGather
}

func (j *Job) processOutstandingWork(ctx context.Context) error {
	for {
		work, ok := j.workQueue.PopFront()
		if !ok {
			return nil
		}
		if err := work(ctx); err != nil {
			return err
		}
	}
}

func (j *Job) gatherOneAndDoTheWork(ctx context.Context) (bool, error) {
	j.vetGather(ctx)

	inGatherAlready := j.inGather(ctx)

	if !inGatherAlready {
		// Modify ctx for the rest of the function, not just for this block
		ctx = context.WithValue(ctx, gatherContextValueKey, j)

		if err := j.processOutstandingWork(ctx); err != nil {
			return true, err
		}
	}

	_, err := j.gatherOne(ctx, waitq.Waiter{}, nil)

	jobDone := false
	if err == ErrJobDone {
		jobDone = true
		err = nil
	}

	if err == nil && !inGatherAlready {
		err = j.processOutstandingWork(ctx)
		if err != nil {
			return true, err
		}
	}

	return !jobDone, err
}

// Returns true if the waiter was notified, false otherwise.  Returns errJobDone if the job is done.
func (j *Job) gatherOne(ctx context.Context, waiter waitq.Waiter, limitCh <-chan struct{}) (bool, error) {
	select {
	case gather := <-j.gatherChan:
		j.workQueue.PushBack(func(ctx context.Context) error {
			return j.executeGather(ctx, gather)
		})
	case <-waiter.Done():
		return true, nil
	case <-limitCh:
	case <-j.state.Done():
		return false, ErrJobDone
	case <-ctx.Done():
		return false, ctx.Err()
	case <-j.ctx.Done():
		return false, j.ctx.Err()
	}
	return false, nil
}

// TryGatherOne processes at most a single result from a task previously
// launched in one of the [Job]'s task pools via [Scatter]. Unlike [Job.GatherOne], it
// will not block if a completed task is not immediately available.
//
// Return values are the same as GatherOne, except that false, nil means that
// there were no tasks ready to gather.
//
// See GatherOne for additional details.
func (j *Job) TryGatherOne(ctx context.Context) (bool, error) {
	ctx = withDefaultBackpressureProvider(ctx, j)
	return j.tryGatherOne(ctx)
}

func (j *Job) tryGatherOne(ctx context.Context) (bool, error) {
	j.vetGather(ctx)

	inGatherAlready := j.inGather(ctx)
	if !inGatherAlready {
		// Modify ctx for the rest of the function, not just for this block
		ctx = context.WithValue(ctx, gatherContextValueKey, j)

		if err := j.processOutstandingWork(ctx); err != nil {
			return true, err
		}
	}

	select {
	case gather := <-j.gatherChan:
		j.workQueue.PushBack(func(ctx context.Context) error {
			return j.executeGather(ctx, gather)
		})
	case <-j.state.Done():
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-j.ctx.Done():
		return false, j.ctx.Err()
	default:
		// There were no in-flight tasks ready to gather.
		return false, nil
	}

	var err error
	if !inGatherAlready {
		err = j.processOutstandingWork(ctx)
	}
	return true, err
}

// GatherAll processes task results until the job completes or an error occurs.
// If the job has not been closed, GatherAll will block indefinitely, as new
// tasks might be added at any time. It will return an error if the provided context
// or job is canceled. After the job is closed, GatherAll will continue processing
// tasks until all work completes (including tasks spawned during result processing)
// and then return.
//
// Returns nil unless the context is canceled or a task's [GatherFunc] returns a
// non-nil error.
//
// If all gather functions are thread-safe, then GatherAll is thread-safe and
// can be called concurrently from multiple goroutines. In this case they will
// collectively process all results, with each call handling a subset. Blocking
// and non-blocking calls may also be mixed, as can calls to any of the other
// gather methods.
//
// NOTE: This method will serially call each gathered task's [GatherFunc] and
// wait until it returns.
func (j *Job) GatherAll(ctx context.Context) error {
	ctx = withDefaultBackpressureProvider(ctx, j)
	return j.gatherAll(ctx, j.gatherOneAndDoTheWork)
}

// TryGatherAll processes all currently available task results without blocking.
// Unlike [Job.GatherAll], TryGatherAll will return immediately if there are no
// completed tasks ready to process, regardless of whether the job is closed or
// whether there are still tasks in flight. It will return an error if the
// provided context or job is canceled.
//
// See GatherAll for information about return values and thread safety.
//
// NOTE: If completed tasks are available, this method must still call each
// task's [GatherFunc] and wait until it finishes processing.
func (j *Job) TryGatherAll(ctx context.Context) error {
	ctx = withDefaultBackpressureProvider(ctx, j)
	return j.gatherAll(ctx, j.tryGatherOne)
}

func (j *Job) gatherAll(ctx context.Context, gatherOne func(ctx context.Context) (bool, error)) error {
	for {
		ok, err := gatherOne(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
}

func (j *Job) executeGather(ctx context.Context, gather boundGatherFunc) error {
	// Decrement the environment-wide in-flight counter only AFTER calling the
	// gather function. This ensures that the in-flight count never drops to
	// zero before the gather function has had a chance to scatter new tasks.
	defer j.state.DecrementTasks()
	return gather(ctx)
}

// panicIfDone panics if the job is in the done state
func (j *Job) panicIfDone() {
	j.state.PanicIfDone()
}

// Close changes the job's state from open to closed, which allows it to eventually
// progress to the done state once all tasks complete. When a job is closed,
// [Job.GatherAll] will return after processing all existing tasks and any tasks
// they spawn, rather than blocking indefinitely.
//
// After a job is closed and all tasks have completed, launching new tasks will panic.
// Gathering operations will continue to work normally but will always return
// immediately with no results.
//
// Note that tasks can still be added after Close is called but before all tasks
// have completed.
//
// Close may be called from any goroutine and may safely be called more than once.
func (j *Job) Close() {
	j.state.Close()
}

// CloseAndGatherAll closes the job via [Job.Close] and then waits for and
// gathers the results of all in-flight tasks via [Job.GatherAll].
func (j *Job) CloseAndGatherAll(ctx context.Context) error {
	j.Close()
	return j.GatherAll(ctx)
}

// SetFlushListener registers a callback function that will be called each time all
// tasks have completed and the job is waiting for combiners to emit their results.
// After the callback returns, the job signals any [CombinerFunc] that has received
// inputs but hasn't yet emitted its combined results to do so immediately. The callback
// may be invoked multiple times during a job's lifecycle if a [GatherFunc] directly or
// indirectly launches new tasks while processing the flushed results.
//
// The callback function is called synchronously from a goroutine calling a gather method
// ([Job.GatherOne], [Job.TryGatherOne], [Job.GatherAll], [Job.TryGatherAll],
// [Job.CloseAndGatherAll]), [Gather.Scatter], or [Job.Close] if no tasks are in flight
// at the time of closing.
//
// If called multiple times, each call replaces any previously registered callback.
// Passing nil removes any existing callback.
func (j *Job) SetFlushListener(callback func()) {
	j.state.SetFlushListener(callback)
}
