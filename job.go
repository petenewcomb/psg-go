// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/petenewcomb/psg-go/internal/state"
)

// Job represents a scatter-gather execution environment. It tracks tasks
// launched with [Scatter] across a set of [Pool] instances and provides methods
// for gathering their results. [Job.Cancel] and [Job.CancelAndWait] allow the
// caller to terminate the environment early and ensure cleanup when the
// environment is no longer needed.
//
// A Job must be created with [NewJob], see that function for caveats and
// important details.
type Job struct {
	ctx           context.Context
	cancelFunc    context.CancelFunc
	pools         []*Pool
	inFlight      state.InFlightCounter
	gatherChannel chan boundGatherFunc
	wg            sync.WaitGroup
}

type boundGatherFunc = func(ctx context.Context) error

// NewJob creates an independent scatter-gather execution environment with the
// specified context and set of pools. The context passed to NewJob is used as
// the root of the context that will be passed to all task functions. (See
// [TaskFunc] and [Job.Cancel] for more detail.)
//
// Pools may not be shared across jobs. NewJob panics if it detects such
// sharing, but such detection is not guaranteed to work if the same pool is
// passed to NewJob calls in different goroutines.
//
// Each call to NewJob should typically be followed by a deferred call to
// [Job.CancelAndWait] to ensure that an early exit from the calling function
// does not leave any outstanding goroutines.
func NewJob(
	ctx context.Context,
	pools ...*Pool,
) *Job {
	ctx, cancelFunc := context.WithCancel(ctx)
	j := &Job{
		cancelFunc:    cancelFunc,
		pools:         slices.Clone(pools),
		gatherChannel: make(chan boundGatherFunc),
	}
	j.ctx = j.makeTaskContext(ctx)
	for _, p := range j.pools {
		if p.job != nil {
			panic("pool was already registered")
		}
		p.job = j
	}
	return j
}

type taskContextMarkerType struct{}

var taskContextMarkerKey any = taskContextMarkerType{}

func (j *Job) makeTaskContext(ctx context.Context) context.Context {
	// Accumulate the jobs to which the context belongs but avoid creating a
	// collection unless it's needed.
	var newValue any
	switch oldValue := ctx.Value(taskContextMarkerKey).(type) {
	case nil:
		newValue = j
	case *Job:
		newValue = map[*Job]struct{}{
			oldValue: {},
			j:        {},
		}
	case map[*Job]struct{}:
		m := make(map[*Job]struct{}, len(oldValue)+1)
		maps.Copy(m, oldValue)
		m[j] = struct{}{}
		newValue = m
	default:
		panic("unexpected task context marker value type")
	}
	return context.WithValue(ctx, taskContextMarkerKey, newValue)
}

func (j *Job) isTaskContext(ctx context.Context) bool {
	switch v := ctx.Value(taskContextMarkerKey).(type) {
	case nil:
		return false
	case *Job:
		return v == j
	case map[*Job]struct{}:
		_, ok := v[j]
		return ok
	default:
		panic("unexpected task context marker value type")
	}
}

// Cancel terminates any in-flight tasks and forfeits any ungathered results.
// Outstanding calls to [Scatter], [Job.GatherOne], and [Job.GatherAll] using
// the job or any of its pools will fail with [context.Canceled] or other error
// returned by a [GatherFunc].
//
// While Cancel always returns immediately, any running [TaskFunc] or
// [GatherFunc] will delay termination of their independent goroutine or caller
// (i.e., [Scatter], [Job.GatherOne], or [Job.GatherAll]) until it returns. This
// method cancels the context passed to each [TaskFunc], but not the context
// passed to each [GatherFunc]. Gather functions instead receive the context
// passed to the calling [Scatter], [Job.GatherOne], or [Job.GatherAll]
// function. If it is desirable to transmit a cancelation signal to a running
// [GatherFunc], one must also cancel any contexts being passed to those
// callers.
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
// in one of the [Job]'s pools via [Scatter]. It will block until a completed
// task is available or the context has been canceled. See [Job.TryGatherOne] for a
// non-blocking alternative.
//
// Returns a boolean flag indicating whether a result was processed and an error
// if one occurred:
//
//   - true, nil: a task completed and was successfully gathered
//   - true, non-nil: a task completed but the gather function returned a
//     non-nil error
//   - false, nil: there were no tasks in flight
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
	return j.gatherOne(ctx, true)
}

// TryGatherOne processes at most a single result from a task previously
// launched in one of the [Job]'s pools via [Scatter]. Unlike [Job.GatherOne], it
// will not block if a completed task is not immediately available.
//
// Return values are the same as GatherOne, except that false, nil means that
// there were no tasks ready to gather.
//
// See GatherOne for additional details.
func (j *Job) TryGatherOne(ctx context.Context) (bool, error) {
	return j.gatherOne(ctx, false)
}

func (j *Job) gatherOne(ctx context.Context, block bool) (bool, error) {
	// If multiple goroutines are calling GatherOne concurrently, it's possible
	// that there are fewer in-flight tasks than there are calling goroutines.
	// All such goroutines running in parallel might pass the in-flight counter
	// check, but only some would receive bound gather functions from the
	// channel -- the rest would potentially block forever. To avoid this
	// problem, decrementInFlight sends nil values through gatherChannel to wake
	// waiters whenever the in-flight count reaches zero. The following code
	// must therefore detect such nil values, re-check the in-flight count, and
	// retry the receive if the count has become non-zero.
	for j.inFlight.GreaterThanZero() {
		// The following select statements are identical other than the absence
		// or presence of a default clause.
		if block {
			select {
			case gather := <-j.gatherChannel:
				if gather != nil {
					return true, j.executeGather(ctx, gather)
				}
			case <-ctx.Done():
				return false, ctx.Err()
			case <-j.ctx.Done():
				return false, j.ctx.Err()
			}
		} else {
			select {
			case gather := <-j.gatherChannel:
				if gather != nil {
					return true, j.executeGather(ctx, gather)
				}
			case <-ctx.Done():
				return false, ctx.Err()
			case <-j.ctx.Done():
				return false, j.ctx.Err()
			default:
				// There were no in-flight tasks ready to gather.
				return false, nil
			}
		}
	}
	// There were no in-flight tasks.
	return false, nil
}

// GatherAll processes all results from previously scattered tasks, continuing
// until there are no more in-flight tasks or an error occurs. It will block to
// wait for in-flight tasks that are not yet complete.
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
	return j.gatherAll(ctx, true)
}

// TryGatherAll processes all results from completed tasks, continuing until
// there are no more immediately available or an error occurs. Unlike
// [Job.GatherAll], TryGatherAll will not block to wait for in-flight tasks to
// complete.
//
// See GatherAll for information about return values and thread safety.
//
// NOTE: If completed tasks are available, this method must still call each
// task's [GatherFunc] and wait until it finishes processing.
func (j *Job) TryGatherAll(ctx context.Context) error {
	return j.gatherAll(ctx, false)
}

func (j *Job) gatherAll(ctx context.Context, block bool) error {
	for {
		ok, err := j.gatherOne(ctx, block)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
}

func (j *Job) executeGather(ctx context.Context, gather boundGatherFunc) error {
	// Decrement the environment-wide in-flight counter only after calling
	// the gather function. This ensures that the in-flight count never
	// drops to zero before the gather function has had a chance to scatter
	// new tasks.
	defer j.decrementInFlight()
	return gather(ctx)
}

func (j *Job) decrementInFlight() {
	if j.inFlight.Decrement() {
		// The in-flight counter reached zero. Wake up any goroutines waiting on
		// the gatherChannel, since there's nothing left to gather.
		j.wakeGatherers()
	}
}

// Wakes any goroutines that might be waiting on the gatherChannel.
func (j *Job) wakeGatherers() {
	for {
		select {
		case j.gatherChannel <- nil:
		default:
			// No more waiters
			return
		}
	}
}

// Job.MultiGatherAll executes [Job.GatherAll] in the specified number of new
// goroutines, continuing until there are no more tasks in flight or an error
// occurs. Only the first error detected will be returned, and an error found in
// one goroutine will be used to cancel all other goroutines. Regardless of
// error, Job.MultiGatherAll always waits for all launched goroutines to
// terminate before returning.
//
// See [Job.GatherAll] for more information.
func (j *Job) MultiGatherAll(ctx context.Context, parallelism int) error {
	return j.multiGatherAll(ctx, parallelism, true)
}

// MultiTryGatherAll executes [Job.TryGatherAll] in the specified number of new
// goroutines, continuing until there are no more task results immediately
// available or an error occurs. Only the first error detected will be returned,
// and an error found in one goroutine will be used to cancel all other
// goroutines. Regardless of error, MultiTryGatherAll always waits for all
// launched goroutines to terminate before returning.
//
// See [Job.TryGatherAll] for more information.
func (j *Job) MultiTryGatherAll(ctx context.Context, parallelism int) error {
	return j.multiGatherAll(ctx, parallelism, false)
}

func (j *Job) multiGatherAll(ctx context.Context, parallelism int, block bool) error {
	if parallelism < 1 {
		panic("parallelism is less than one")
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)

	var wg sync.WaitGroup
	wg.Add(parallelism)
	for range parallelism {
		go func() {
			defer wg.Done()
			err := j.gatherAll(ctx, block)
			if err != nil {
				cancel(err)
			}
		}()
	}
	wg.Wait()
	return ctx.Err()
}
