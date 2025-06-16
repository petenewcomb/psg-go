// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/gcok"
	"github.com/petenewcomb/psg-go/internal/jobstate"
	"github.com/petenewcomb/psg-go/internal/nbcq"
	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/petenewcomb/psg-go/internal/waitq"
)

// DefaultTaskWorkerIdleTimeout is the default duration a task worker will wait
// for new work before exiting. This controls how aggressively workers scale down
// when load decreases.
const DefaultTaskWorkerIdleTimeout = 100 * time.Millisecond

const DefaultMaxGCTimeRatioThreshold = 0.5 // 50% of total CPU time used for GC
const DefaultGCTimeUpdateInterval = 1 * time.Second

// Job represents a scatter-gather execution environment. It tracks tasks
// launched with [Scatter] across a set of [TaskPool] instances and provides methods
// for gathering their results. [Job.Cancel] and [Job.CancelAndWait] allow the
// caller to terminate the environment early and ensure cleanup when the
// environment is no longer needed.
//
// A Job must be created with [NewJob], see that function for caveats and
// important details.
type Job struct {
	ctx         context.Context
	cancelFn    context.CancelFunc
	gatherQueue rdvq.Required[boundGatherFunc]
	wg          sync.WaitGroup
	state       jobstate.JobState
	gcMonitor   gcok.Monitor

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
	workQueue   nbcq.Queue[workItem]
	workCounter atomic.Int64
	workWaiters waitq.Queue

	taskQueue             rdvq.Optional[preparedTaskFunc]
	taskWorkerIdleTimeout atomic.Int64 // stores time.Duration as nanoseconds

	vettedCtxCache sync.Map // context.Context -> vettedContext
	gatherCtxCache sync.Map // context.Context -> context.Context
}

// job returns the Job itself to satisfy the TaskPoolOrJob interface.
func (j *Job) job() *Job {
	return j
}

type vettedContext struct {
	ctx          context.Context
	hasTaskValue bool // Pre-computed includesJob(ctx, j, taskContextValueKey)
	inGather     bool
}

// withBackpressureProvider returns a context with the default backpressure provider for this Job
func (j *Job) vettedContext(ctx context.Context) vettedContext {
	// Check cache first to avoid expensive computation
	if cached, ok := j.vettedCtxCache.Load(ctx); ok {
		return cached.(vettedContext)
	}

	// Cache miss - compute expensive context checks
	hasTask := includesJob(ctx, j, taskContextValueKey)
	inGather := j.inGather(ctx)

	// Only try to add backpressure provider if context doesn't have task value
	// (to avoid the panic in hasBackpressureProviderForJob)
	var resultCtx context.Context
	if hasTask {
		// Task contexts cannot have backpressure providers added
		resultCtx = ctx
	} else if hasBackpressureProviderForJob(ctx, j) {
		resultCtx = ctx
	} else {
		resultCtx = withNewBackpressureProvider(ctx, j)
	}

	return j.cacheVettedContext(ctx, vettedContext{
		ctx:          resultCtx,
		hasTaskValue: hasTask,
		inGather:     inGather,
	})
}

func (j *Job) cacheVettedContext(ctx context.Context, vettedCtx vettedContext) vettedContext {
	// Use LoadOrStore to handle race condition where another goroutine
	// might have stored while we were computing
	if actual, loaded := j.vettedCtxCache.LoadOrStore(ctx, vettedCtx); loaded {
		// Another goroutine stored first, use their result
		return actual.(vettedContext)
	}

	// We successfully stored our result, set up cleanup
	// Clean up when the input context is cancelled, but not after the job is
	// cancelled
	stop := context.AfterFunc(ctx, func() {
		j.vettedCtxCache.Delete(ctx)
	})
	_ = context.AfterFunc(j.ctx, func() {
		_ = stop()
	})

	return vettedCtx
}

func (j *Job) withBackpressureProvider(ctx context.Context) context.Context {
	return j.vettedContext(ctx).ctx
}

type boundGatherFunc = func(ctx context.Context) error

type workFunc func(context.Context) error

type workItem struct {
	id     int64
	workFn workFunc
}

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
	ctx, cancelFn := context.WithCancel(ctx)

	// Reset job-specific context values that shouldn't be inherited from parent jobs
	// while preserving user-provided context values and jobContextValueKey for cycle detection
	if ctx.Value(taskContextValueKey) != nil {
		ctx = context.WithValue(ctx, taskContextValueKey, nil)
	}
	if ctx.Value(gatherContextValueKey) != nil {
		ctx = context.WithValue(ctx, gatherContextValueKey, nil)
	}
	if ctx.Value(backpressureProviderContextValueKey) != nil {
		ctx = context.WithValue(ctx, backpressureProviderContextValueKey, nil)
	}

	j := &Job{
		cancelFn: cancelFn,
	}
	j.ctx = withJob(ctx, j)
	j.state.Init()
	j.workQueue.Init(workQueuePool)
	j.workWaiters.Init()
	j.taskQueue.Init(taskQueuePool)
	j.taskWorkerIdleTimeout.Store(int64(DefaultTaskWorkerIdleTimeout))
	j.gatherQueue.Init(gatherQueuePool)
	j.gcMonitor.SetBusyThreshold(DefaultMaxGCTimeRatioThreshold)
	j.gcMonitor.SetUpdateInterval(DefaultGCTimeUpdateInterval)
	return j
}

var workQueuePool = &nbcq.Pool[workItem]{}
var taskQueuePool = &rdvq.Pool[preparedTaskFunc]{}
var gatherQueuePool = &rdvq.Pool[boundGatherFunc]{}

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
// Outstanding calls to [Scatter], [Job.Gather], [Job.TryGather],
// [Job.GatherAll], or [Job.TryGatherAll] using the job or any of its task pools will
// fail with [context.Canceled] or other error returned by a [GatherFunc].
//
// While Cancel always returns immediately, any running [TaskFunc] or
// [GatherFunc] will delay termination of their independent goroutine or caller
// until it returns. This method cancels the context passed to each [TaskFunc],
// but not the context passed to each [GatherFunc]. Gather functions instead
// receive the context passed to the calling [Scatter], [Job.Gather],
// [Job.TryGather], [Job.GatherAll], or [Job.TryGatherAll] function. If it is
// desirable to transmit a cancelation signal to a running [GatherFunc], one
// must also cancel any contexts being passed to those callers.
//
// Cancel is always thread-safe and calling it more than once has no additional
// effect.
func (j *Job) Cancel() {
	j.cancelFn()
	j.gcMonitor.Cancel()
}

// CancelAndWait cancels like [Job.Cancel], but then blocks until any
// outstanding task goroutines exit.
func (j *Job) CancelAndWait() {
	j.Cancel()
	j.wg.Wait()
	j.gcMonitor.Wait()
}

// SetTaskWorkerIdleTimeout sets the duration that idle task workers wait for
// new work before exiting. This controls how aggressively workers scale down
// when load decreases.
//
// A shorter timeout reduces resource usage during idle periods but may increase
// overhead when load patterns are bursty. A longer timeout keeps workers alive
// longer, reducing spawn/teardown overhead but potentially wasting resources.
//
// The default value is [DefaultTaskWorkerIdleTimeout].
//
// This method is safe to call at any time, but only affects workers that begin
// waiting after the call. Workers already in their idle timeout will use the
// previous value.
func (j *Job) SetTaskWorkerIdleTimeout(timeout time.Duration) {
	j.taskWorkerIdleTimeout.Store(int64(timeout))
}

// SetMaxGCTimeRatioThreshold sets the threshold for GC time ratio that triggers
// backpressure during scatter operations. When the ratio of GC CPU time to total
// CPU time exceeds this threshold, new scatter operations will be delayed until
// GC pressure decreases.
//
// The threshold must be in the range (0, 1], where 1.0 means 100% of CPU time
// spent on GC. The default value is [DefaultMaxGCTimeRatioThreshold].
//
// Setting this to 0 disables GC-based backpressure entirely.
func (j *Job) SetMaxGCTimeRatioThreshold(threshold float64) {
	j.gcMonitor.SetBusyThreshold(threshold)
}

// SetGCTimeUpdateInterval sets how frequently the Job monitors GC time ratios
// for backpressure decisions. More frequent updates provide more responsive
// backpressure but consume more CPU for monitoring.
//
// The default value is [DefaultGCTimeUpdateInterval].
//
// Setting this to 0 disables GC monitoring entirely, which also disables
// GC-based backpressure.
func (j *Job) SetGCTimeUpdateInterval(interval time.Duration) {
	j.gcMonitor.SetUpdateInterval(interval)
}

// Gather processes outstanding task results and then waits for the next
// task result from a task previously launched via [Scatter]. It will block until
// a completed task is available, the provided context or job is canceled, or
// another event causes a wake-up (e.g. a call to [TaskPool.SetLimit]).
// If the job is closed and no tasks remain in flight, it will return immediately.
// See [Job.TryGather] for a non-blocking alternative.
//
// Returns an error if one occurred:
//
//   - nil: a task completed and was successfully gathered
//   - ErrJobDone: the job is done and therefore nothing is left to gather
//   - other error: a task's gather function returned a non-nil error, or the
//     argument or job-internal context was canceled
//
// If a gather function returns an error, the job continues running and you can
// keep calling Gather to process more tasks (and errors, if any) until you
// receive ErrJobDone.
//
// If all gather functions are thread-safe, then Gather is thread-safe and
// may be called concurrently from multiple goroutines. Blocking and
// non-blocking calls may also be mixed, as can calls to any of the other gather
// methods.
//
// NOTE: If a task result is gathered, this method will call the task's
// [GatherFunc] and wait until it returns.
func (j *Job) Gather(ctx context.Context) error {
	vetted := j.vettedContext(ctx)
	_, err := j.processWorkAndGather(vetted)
	return err
}

func (j *Job) vetGather(vetted vettedContext) {
	if vetted.hasTaskValue {
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

func (j *Job) gatherContext(vettedCtx vettedContext) context.Context {

	// Check cache first to avoid context allocation
	if cached, ok := j.gatherCtxCache.Load(vettedCtx.ctx); ok {
		return cached.(context.Context)
	}

	// Create new gather context based on vettedCtx
	gatherCtx := context.WithValue(vettedCtx.ctx, gatherContextValueKey, j)

	// Use LoadOrStore to handle race condition where another goroutine
	// might have stored while we were computing
	if actual, loaded := j.gatherCtxCache.LoadOrStore(vettedCtx.ctx, gatherCtx); loaded {
		// Another goroutine stored first, use their result
		return actual.(context.Context)
	}

	// We successfully stored our result, set up cleanup
	// Clean up when the input context is cancelled, but not after the job is
	// cancelled
	stop := context.AfterFunc(vettedCtx.ctx, func() {
		j.gatherCtxCache.Delete(vettedCtx.ctx)
	})
	_ = context.AfterFunc(j.ctx, func() {
		_ = stop()
	})

	// Pre-populate a vetted context for the gather context
	vettedGatherCtx := vettedCtx
	vettedGatherCtx.ctx = gatherCtx
	vettedGatherCtx.inGather = true

	j.cacheVettedContext(gatherCtx, vettedGatherCtx)

	return gatherCtx
}

func (j *Job) queueWork(workFn workFunc) {
	j.workQueue.PushBack(workQueuePool, workItem{
		id:     j.workCounter.Add(1),
		workFn: workFn,
	})
	j.workWaiters.Notify()
}

func (j *Job) processOutstandingWork(ctx context.Context) error {
	lastIDToProcess := j.workCounter.Load()
	for {
		work, ok := j.workQueue.PopFront(workQueuePool)
		if !ok {
			break
		}
		if err := work.workFn(ctx); err != nil {
			return err
		}
		if work.id >= lastIDToProcess {
			break
		}
	}
	return nil
}

func (j *Job) processWorkAndGather(vettedCtx vettedContext) (bool, error) {

	j.vetGather(vettedCtx)

	ctx := vettedCtx.ctx
	var err error
	if !vettedCtx.inGather {
		ctx = j.gatherContext(vettedCtx)
		for {
			var waiterNotified bool
			workWaiter := j.workWaiters.NewWaiter(func() bool {
				// Process work queue inside the wait to avoid race conditions
				if err = j.processOutstandingWork(ctx); err != nil {
					return false
				}
				return true
			})
			waiterNotified, err = j.gather(ctx, workWaiter, nil)
			if !waiterNotified || err != nil {
				break
			}
		}
	} else {
		_, err = j.gather(ctx, waitq.Waiter{}, nil)
	}

	return err == nil, err
}

// postGather sends a gather operation to the gather queue.
func (j *Job) postGather(ctx context.Context, gather boundGatherFunc) {
	// Error can only be due to context cancellation, so safe to ignore here.
	_ = j.gatherQueue.PushBack(ctx, gatherQueuePool, gather)
}

func (j *Job) queueGather(gather boundGatherFunc) {
	j.queueWork(func(ctx context.Context) error {
		return j.executeGather(ctx, gather)
	})
}

// Returns true if the waiter was notified, false otherwise.  Returns errJobDone if the job is done.
func (j *Job) gather(ctx context.Context, waiter waitq.Waiter, limitCh <-chan struct{}) (bool, error) {
	waiterNotified := false
	var err error
	j.gatherQueue.PopFrontFunc(gatherQueuePool, j.queueGather,
		func(dedicatedCh, sharedCh <-chan boundGatherFunc) (sourceCh <-chan boundGatherFunc) {
			waiterNotified = waiter.Wait(func(waitCh <-chan struct{}) bool {
				select {
				case gather := <-dedicatedCh:
					j.queueGather(gather)
					sourceCh = dedicatedCh
				case gather := <-sharedCh:
					j.queueGather(gather)
				case <-waitCh:
					return true
				case <-limitCh:
				case <-j.state.Done():
					err = ErrJobDone
				case <-ctx.Done():
					err = ctx.Err()
				}
				return false
			})
			return sourceCh
		},
	)
	return waiterNotified, err
}

// TryGather processes outstanding task results and then attempts to process
// the next task result from a task previously launched via [Scatter]. Unlike
// [Job.Gather], it will not block if a completed task is not immediately available.
//
// Returns a boolean flag indicating whether there might be more task results
// immediately available to process and an error if one occurred.
//
// The error indicates:
//   - nil: no gather function returned an error
//   - ErrJobDone: the job is done and no more tasks will ever be available
//   - other error: a gather function returned an error or the context was canceled
//
// If a gather function returns an error, the job continues running and you can
// keep calling TryGather to process more tasks (and errors, if any) until you
// receive ErrJobDone.
//
// See Gather for additional details.
func (j *Job) TryGather(ctx context.Context) (bool, error) {
	vetted := j.vettedContext(ctx)
	return j.processWorkAndTryGather(vetted)
}

func (j *Job) processWorkAndTryGather(vettedCtx vettedContext) (bool, error) {

	j.vetGather(vettedCtx)

	if !vettedCtx.inGather {
		// Use cached gather context instead of creating new one
		ctx := j.gatherContext(vettedCtx)
		if err := j.processOutstandingWork(ctx); err != nil {
			return true, err
		}
	}

	return j.tryQueueGather(), nil
}

func (j *Job) tryQueueGather() bool {
	ok := false
	j.gatherQueue.TryPopFront(gatherQueuePool, func(gather boundGatherFunc) {
		ok = true
		j.queueGather(gather)
	})
	return ok
}

// GatherAll processes task results until the job completes or an error occurs.
// If the job has not been closed, GatherAll will block indefinitely, as new
// tasks might be added at any time. It will return an error if the provided context
// or job is canceled. After the job is closed, GatherAll will continue processing
// tasks until all work completes (including tasks spawned during result processing)
// and then return.
//
// Returns nil when the job is done, or an error if the context is canceled or a
// task's [GatherFunc] returns a non-nil error. If a gather function returns an
// error, you can call GatherAll again to continue processing more tasks (and
// errors, if any) until the job is done (i.e., GatherAll returns nil).
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
	vetted := j.vettedContext(ctx)
	err := j.gatherAll(vetted, j.processWorkAndGather)
	if err == ErrJobDone {
		return nil
	}
	return err
}

// TryGatherAll processes all currently available task results without blocking.
// Unlike [Job.GatherAll], TryGatherAll will return immediately if there are no
// completed tasks ready to process, regardless of whether the job is closed or
// whether there are still tasks in flight.
//
// Returns nil when all immediately available tasks have been processed, ErrJobDone
// when the job is done, or an error if the context is canceled or a task's
// [GatherFunc] returns a non-nil error. If a gather function returns an error,
// you can call TryGatherAll again to continue processing more tasks (and errors,
// if any) until you receive ErrJobDone.
//
// See GatherAll for information about thread safety.
//
// NOTE: If completed tasks are available, this method must still call each
// task's [GatherFunc] and wait until it finishes processing.
func (j *Job) TryGatherAll(ctx context.Context) error {
	vetted := j.vettedContext(ctx)
	return j.gatherAll(vetted, j.processWorkAndTryGather)
}

func (j *Job) gatherAll(vettedCtx vettedContext, gatherSomeFn func(vettedContext) (bool, error)) error {
	for {
		ok, err := gatherSomeFn(vettedCtx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
}

func (j *Job) startTask(taskFn preparedTaskFunc) {
	// Try to hand off to an idle worker
	if j.taskQueue.TryPushBack(taskQueuePool, taskFn) {
		return // Successfully handed off to idle worker
	}

	// No idle workers available, spawn a new one
	j.spawnTaskWorker(taskFn)
}

func (j *Job) spawnTaskWorker(taskFn preparedTaskFunc) {
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()

		ctx, cancel := context.WithCancel(j.ctx)
		defer cancel()

		ctx = context.WithValue(ctx, taskContextValueKey, j.ctx.Value(jobContextValueKey))

		// Cache for backpressure provider contexts
		bpContextCache := make(map[backpressureProviderKey]context.Context)
		ctxWithBP := func(bp backpressureProvider) context.Context {
			key := bp.Key()
			if cached, ok := bpContextCache[key]; ok {
				return cached
			}
			cached := withBackpressureProvider(ctx, bp)
			bpContextCache[key] = cached
			return cached
		}

		idleTimer := timerp.Get()
		defer timerp.Put(idleTimer)

		for taskFn != nil {
			// Execute the task
			taskFn(ctx, ctxWithBP)
			taskFn = nil

			// Wait for next task with timeout
			timerp.Reset(idleTimer, time.Duration(j.taskWorkerIdleTimeout.Load()))

			j.taskQueue.PopFrontFunc(taskQueuePool,
				func(orphanedTaskFn preparedTaskFunc) {
					if taskFn == nil {
						taskFn = orphanedTaskFn
					} else {
						// Hand this one off to a different or new goroutine
						j.startTask(orphanedTaskFn)
					}
				},
				func(ch <-chan preparedTaskFunc) bool {
					select {
					case taskFn = <-ch:
						return true
					case <-idleTimer.C:
					case <-ctx.Done():
					}
					return false
				},
			)
		}
	}()
}

type taskContextValueKeyType struct{}

var taskContextValueKey any = taskContextValueKeyType{}

// launch executes a task immediately without any concurrency constraints.
// Implements the TaskPoolOrJob interface.
func (j *Job) launch(ctx context.Context, backpressureFn backpressureFunc, taskFn boundTaskFunc) (launched bool, err error) {
	// Launch the task immediately without any pool tracking
	j.startTask(func(ctx context.Context, ctxWithBP func(backpressureProvider) context.Context) {
		taskFn(ctx, nil, ctxWithBP) // No completion callback needed for unlimited tasks
	})
	return true, nil
}

func (j *Job) executeGather(ctx context.Context, gather boundGatherFunc) error {
	// Decrement the environment-wide in-flight counter only AFTER calling the
	// gather function. This ensures that the in-flight count never drops to
	// zero before the gather function has had a chance to scatter new tasks.
	defer j.state.DecrementWork()
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
// ([Job.Gather], [Job.TryGather], [Job.GatherAll], [Job.TryGatherAll],
// [Job.CloseAndGatherAll]), [Gather.Scatter], or [Job.Close] if no tasks are in flight
// at the time of closing.
//
// If called multiple times, each call replaces any previously registered callback.
// Passing nil removes any existing callback.
func (j *Job) SetFlushListener(callback func()) {
	j.state.SetFlushListener(callback)
}
