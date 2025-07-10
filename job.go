// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/cerr"
	"github.com/petenewcomb/psg-go/internal/ctxmap"
	"github.com/petenewcomb/psg-go/internal/gcok"
	"github.com/petenewcomb/psg-go/internal/jobstate"
	"github.com/petenewcomb/psg-go/internal/opts"
	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgopt"
)

// Job represents a scatter-gather execution environment. It tracks tasks
// launched with [Scatter] across a set of [TaskPool] instances and provides methods
// for gathering their results. [Job.Cancel] and [Job.CancelAndWait] allow the
// caller to terminate the environment early and ensure cleanup when the
// environment is no longer needed.
//
// A Job must be created with [NewJob], see that function for caveats and
// important details.
//
//nolint:contextcheck // background context used only for tracing
type Job struct {
	ctx       context.Context //nolint:containedctx // used as parent for contexts in job-owned goroutines
	cancelFn  context.CancelFunc
	wg        sync.WaitGroup
	state     jobstate.JobState
	gcMonitor gcok.Monitor
	gcWaiters workq.Waiters

	gatherQueue workq.Offers

	workQueue workq.Accepted

	taskQueue             rdvq.Optional[pendingTask]
	taskWorkerIdleTimeout atomic.Int64 // stores time.Duration as nanoseconds

	ctxMetaMap       ctxmap.Map[ctxMetaValueKey, *ctxMeta]
	gatherCtxMetaMap ctxmap.Map[gatherCtxMetaValueKey, *Job]
}

type pendingTask func(ctx context.Context, taskWorkerOutboxMap *outboxMap)

// job returns the Job itself to satisfy the TaskPoolOrJob interface.
func (j *Job) job() *Job {
	return j
}

// gatherOutboxKey returns a unique identifier for this Job for gather outbox mapping.
func (j *Job) gatherOutboxKey() outboxKey[workq.WorkFunc] {
	return j
}

type boundGatherFunc = func(ctx context.Context) error

// NewJob creates an independent scatter-gather execution environment with the
// specified context. The context passed to NewJob is used as the root of the
// context that will be passed to all task functions. (See [Task] and
// [Job.Cancel] for more detail.)
//
// Use [NewTaskPool] to create task pools bound to this job.
//
// Each call to NewJob should typically be followed by a deferred call to
// [Job.CancelAndWait] to ensure that an early exit from the calling function
// does not leave any outstanding goroutines.
func NewJob(ctx context.Context, options ...psgopt.JobOption) *Job {
	ctx, cancelFn := context.WithCancel(ctx)
	j := &Job{
		ctx:      ctx,
		cancelFn: cancelFn,
	}
	j.state.Init()
	j.gatherQueue.Init()
	j.workQueue.Init()
	j.taskQueue.Init(taskQueuePool)
	j.taskWorkerIdleTimeout.Store(int64(psgopt.DefaultTaskWorkerIdleTimeout))

	// Initialize GC monitor with defaults
	defaultGCThreshold := psgopt.DefaultMaxGCTimeRatioThreshold
	defaultGCInterval := psgopt.DefaultGCTimeUpdateInterval
	onGCBusyChange := func(busy bool) {
		if !busy {
			j.gcWaiters.NotifyAll()
		}
	}
	j.gcMonitor.Update(gcok.ConfigChanges{
		BusyThreshold:  &defaultGCThreshold,
		UpdateInterval: &defaultGCInterval,
		OnChange:       &onGCBusyChange,
	})
	j.gcWaiters.Init()

	// Apply user options
	j.SetOptions(options...)

	trace.Logf(ctx, "psg.NewJob", "job=%p, state=%p, gatherQueue=%p, workQueue=%p, taskQueue=%p, gcMonitor=%p, gcWaiters=%p", j, &j.state, &j.gatherQueue, &j.workQueue, &j.taskQueue, &j.gcMonitor, &j.gcWaiters)

	return j
}

var taskQueuePool = &rdvq.Pool[pendingTask]{}

// Cancel terminates any in-flight tasks and forfeits any ungathered results.
// Outstanding calls to [Scatter], [Job.Gather], [Job.TryGather],
// [Job.GatherAll], or [Job.TryGatherAll] using the job or any of its task pools will
// fail with [context.Canceled] or other error returned by a [Gather].
//
// While Cancel always returns immediately, any running [Task] or
// [Gather] will delay termination of their independent goroutine or caller
// until it returns. This method cancels the context passed to each [Task],
// but not the context passed to each [Gather]. Gather functions instead
// receive the context passed to the calling [Scatter], [Job.Gather],
// [Job.TryGather], [Job.GatherAll], or [Job.TryGatherAll] function. If it is
// desirable to transmit a cancelation signal to a running [Gather], one
// must also cancel any contexts being passed to those callers.
//
// Cancel is always thread-safe and calling it more than once has no additional
// effect.
//
//nolint:contextcheck // background context used only for tracing
func (j *Job) Cancel() {
	defer trace.StartRegion(context.Background(), "job.Cancel").End()
	trace.Logf(context.Background(), "job.Cancel", "canceling job context")
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

// Gather processes outstanding task results and then waits for the next
// task result from a task previously launched via [Scatter]. It will block until
// a completed task is available, the provided context or job is canceled, or
// another event causes a wake-up (e.g. a call to [TaskPool.SetOptions]).
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
// [Gather] and wait until it returns.
func (j *Job) Gather(ctx context.Context) error {
	ctx, meta := j.vetGather(ctx)
	_, err := j.gather(ctx, meta)
	return err
}

func (j *Job) tryAddWork(ctx context.Context, queueFn workq.QueueWorkFunc) error {
	defer trace.StartRegion(ctx, "job.tryAddWork").End()
	if workFn, ok := j.gatherQueue.TryPopFront(); ok {
		trace.Logf(ctx, "job.tryAddWork", "found work in gather queue, queueing it")
		queueFn(workFn)
	} else {
		trace.Logf(ctx, "job.tryAddWork", "no work found in gather queue")
	}
	return nil
}

func (j *Job) vetGather(ctx context.Context) (context.Context, *ctxMeta) {
	return j.gatherCtxMeta(ctx)
}

func (j *Job) tryGather(ctx context.Context, _ *ctxMeta) (bool, error) {
	return j.workQueue.TryExecuteOne(ctx, j.tryAddWork)
}

func (j *Job) gather(ctx context.Context, meta *ctxMeta) (bool, error) {
	return true, j.workQueue.ExecuteOne(ctx,
		func(ctx context.Context, workReadyCh <-chan workq.RenotifyFunc, queueFn workq.QueueWorkFunc) (workq.RenotifyFunc, error) {
			workReadyRenotifyFn, _, err := j.addWork(ctx, meta, workReadyCh, queueFn, nil)
			return workReadyRenotifyFn, err
		},
	)
}

// This function is designed to be called before scattering a new task to
// preemptively gather or gather results from completed tasks. This smooths
// execution and adds backpressure that enables operation with unlimited task
// pools.
func (j *Job) yield(ctx context.Context, meta *ctxMeta) error {
	for {
		if ok, err := j.tryGather(ctx, meta); !ok || err != nil {
			return err
		}
	}
}

const errBlockWaitSignaled = cerr.Error("block wait signaled")

func (j *Job) block(ctx context.Context, blockWaitCh <-chan workq.RenotifyFunc) (workq.RenotifyFunc, error) {
	defer trace.StartRegion(ctx, "job.block").End()
	trace.Logf(ctx, "job.block", "starting, blockWaitCh=%p", blockWaitCh)
	ctx, meta := j.vetGather(ctx)
	trace.Logf(ctx, "job.block", "meta=%v", meta)
	var blockWaitRenotifyFn workq.RenotifyFunc
	err := j.workQueue.ExecuteOne(ctx,
		func(ctx context.Context, workReadyCh <-chan workq.RenotifyFunc, queueFn workq.QueueWorkFunc) (workq.RenotifyFunc, error) {
			var workReadyRenotifyFn workq.RenotifyFunc
			var err error
			workReadyRenotifyFn, blockWaitRenotifyFn, err = j.addWork(ctx, meta, workReadyCh, queueFn, blockWaitCh)
			return workReadyRenotifyFn, err
		},
	)
	if errors.Is(err, errBlockWaitSignaled) {
		err = nil
	}
	trace.Logf(ctx, "job.block", "ExecuteOne returned blockWaitRenotifyFn=%v, err=%v", blockWaitRenotifyFn, err)
	return blockWaitRenotifyFn, err
}

func (j *Job) addWork(
	ctx context.Context,
	meta *ctxMeta,
	workReadyCh <-chan workq.RenotifyFunc,
	queueFn workq.QueueWorkFunc,
	blockWaitCh <-chan workq.RenotifyFunc,
) (workReadyRenotifyFn, blockWaitRenotifyFn workq.RenotifyFunc, err error) {
	traceRegion := "Job.addWork"
	meta.WithQueueFunc(queueFn, func() {
		if workReadyCh == nil {
			err = j.tryAddWork(ctx, queueFn)
		} else {
			j.gatherQueue.PopFrontFunc(queueFn,
				func(inboxCh <-chan workq.WorkFunc, outboxFilledCh <-chan rdvq.RenotifyFunc) (rdvq.SelectResult, rdvq.RenotifyFunc) {
					trace.Logf(ctx, traceRegion, "entering select: inboxCh=%p, outboxFilledCh=%p, workReadyCh=%p, blockWaitCh=%p",
						inboxCh, outboxFilledCh, workReadyCh, blockWaitCh)
					select {
					case workFn := <-inboxCh:
						trace.Logf(ctx, "job.addWork", "received workFn from inboxCh=%p", inboxCh)
						queueFn(workFn)
						return rdvq.SelectInboxEmptied, nil
					case renotifyFn := <-outboxFilledCh:
						trace.Logf(ctx, "job.addWork", "received renotifyFn from outboxFilledCh=%p", outboxFilledCh)
						return rdvq.SelectOutboxFilled, renotifyFn
					case renotifyFn := <-workReadyCh:
						trace.Logf(ctx, "job.addWork", "received renotifyFn from workReadyCh=%p", workReadyCh)
						workReadyRenotifyFn = renotifyFn
					case renotifyFn := <-blockWaitCh:
						trace.Logf(ctx, "job.addWork", "received renotifyFn from blockWaitCh=%p", blockWaitCh)
						blockWaitRenotifyFn = renotifyFn
						err = errBlockWaitSignaled
					case <-j.state.Done():
						trace.Logf(ctx, "job.addWork", "woke from job done signal")
						err = ErrJobDone
					case <-ctx.Done():
						trace.Logf(ctx, "job.addWork", "woke from context done signal")
						err = ctx.Err()
					}
					return rdvq.SelectAborted, nil
				},
			)
		}
	})
	return
}

// postGather sends a gather operation to the gather queue.
func (j *Job) postGather(ctx context.Context, outbox *rdvq.Outbox[workq.WorkFunc], gatherFn boundGatherFunc) {
	// Error can only be due to context cancellation, so safe to ignore here.
	workFn := j.newGatherWork(gatherFn)
	_ = j.gatherQueue.PushBack(ctx, outbox, workFn)
}

var workIDCounter atomic.Int64

//nolint:contextcheck // background context used only for tracing
func (j *Job) newGatherWork(gatherFn boundGatherFunc) workq.WorkFunc {

	workID := workIDCounter.Add(1)
	trace.Logf(context.Background(), "job.newGatherWork", "workID=%d", workID)

	return func(ctx context.Context, ex workq.Execution) error {
		defer trace.StartRegion(ctx, "job.gatherWork").End()
		trace.Logf(ctx, "job.gatherWork", "starting, workID=%d", workID)
		ex.Starting()
		trace.Logf(ctx, "job.gatherWork", "started, workID=%d", workID)
		ctx, meta := j.ctxMeta(ctx)
		var err error
		meta.WithQueueFunc(ex.Queue, func() {
			err = gatherFn(ctx)
		})
		trace.Logf(ctx, "job.gatherWork", "ended, workID=%d, err=%v", workID, err)
		return err
	}
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
	ctx, meta := j.vetGather(ctx)
	return j.tryGather(ctx, meta)
}

// GatherAll processes task results until the job completes or an error occurs.
// If the job has not been closed, GatherAll will block indefinitely, as new
// tasks might be added at any time. It will return an error if the provided context
// or job is canceled. After the job is closed, GatherAll will continue processing
// tasks until all work completes (including tasks spawned during result processing)
// and then return.
//
// Returns nil when the job is done, or an error if the context is canceled or a
// task's [Gather] returns a non-nil error. If a gather function returns an
// error, you can call GatherAll again to continue processing more tasks (and
// errors, if any) until the job is done (i.e., GatherAll returns nil).
//
// If all gather functions are thread-safe, then GatherAll is thread-safe and
// can be called concurrently from multiple goroutines. In this case they will
// collectively process all results, with each call handling a subset. Blocking
// and non-blocking calls may also be mixed, as can calls to any of the other
// gather methods.
//
// NOTE: This method will serially call each gathered task's [Gather] and
// wait until it returns.
func (j *Job) GatherAll(ctx context.Context) error {
	err := j.gatherAll(ctx, j.gather)
	if errors.Is(err, ErrJobDone) {
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
// [Gather] returns a non-nil error. If a gather function returns an error,
// you can call TryGatherAll again to continue processing more tasks (and errors,
// if any) until you receive ErrJobDone.
//
// See GatherAll for information about thread safety.
//
// NOTE: If completed tasks are available, this method must still call each
// task's [Gather] and wait until it finishes processing.
func (j *Job) TryGatherAll(ctx context.Context) error {
	return j.gatherAll(ctx, j.tryGather)
}

func (j *Job) gatherAll(ctx context.Context, gatherFn func(context.Context, *ctxMeta) (bool, error)) error {
	ctx, meta := j.vetGather(ctx)
	for {
		ok, err := gatherFn(ctx, meta)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
}

func (j *Job) startTask(ctx context.Context, taskFn pendingTask) {
	// Try to hand off to an idle worker
	if j.taskQueue.TryPushBack(taskQueuePool, taskFn) {
		return // Successfully handed off to idle worker
	}

	// No idle workers available, spawn a new one
	j.spawnTaskWorker(ctx, taskFn)
}

func (j *Job) spawnTaskWorker(_ context.Context, taskFn pendingTask) {
	j.wg.Add(1)
	go func() { //nolint:contextcheck // task worker goroutine will use job context
		defer j.wg.Done()

		traceRegion := "Job.taskGoroutine"
		defer trace.StartRegion(context.Background(), traceRegion).End()
		trace.Logf(context.Background(), traceRegion, "Job=%p", j)

		goroutineCtx, cancelGoroutineCtx := context.WithCancel(j.ctx)
		defer func() {
			trace.Logf(context.Background(), traceRegion, "canceling goroutine context")
			cancelGoroutineCtx()
		}()

		ctx, _ := j.ensureCtxMeta(goroutineCtx,
			func(ctx context.Context, meta *ctxMeta) context.Context {
				meta.ctxType = taskContext
				// No executionEnvironment needed
				return ctx
			},
		)

		// Create the outbox map for this task worker goroutine
		var taskWorkerOutboxMap outboxMap

		idleTimer := timerp.Get()
		defer timerp.Put(idleTimer)

		for taskFn != nil {
			// Execute the task
			taskFn(ctx, &taskWorkerOutboxMap)
			taskFn = nil

			// Wait for next task with timeout
			timerp.Reset(idleTimer, time.Duration(j.taskWorkerIdleTimeout.Load()))

			j.taskQueue.PopFrontFunc(taskQueuePool,
				func(orphanedTaskFn pendingTask) {
					if taskFn == nil {
						taskFn = orphanedTaskFn
					} else {
						// Hand this one off to a different or new goroutine
						j.startTask(ctx, orphanedTaskFn)
					}
				},
				func(inboxCh <-chan pendingTask) rdvq.SelectResult {
					trace.Logf(ctx, traceRegion, "entering select: inboxCh=%p", inboxCh)
					select {
					case taskFn = <-inboxCh:
						trace.Logf(ctx, traceRegion, "received taskFn from inboxCh=%p", inboxCh)
						return rdvq.SelectInboxEmptied
					case <-idleTimer.C:
						trace.Logf(ctx, traceRegion, "received signal from idle timer")
					case <-ctx.Done():
						trace.Logf(ctx, traceRegion, "received context done signal")
					}
					return rdvq.SelectAborted
				},
			)
		}
	}()
}

// launch executes a task immediately without any concurrency constraints.
// Implements the TaskPoolOrJob interface.
func (j *Job) newScatterWork(taskFn boundTaskFunc) workq.WorkFunc {
	// No completion callback needed for unlimited tasks
	return j.newScatterWorkWithCompletedFn(taskFn, nil)
}

// launch executes a task immediately without any concurrency constraints.
// Implements the TaskPoolOrJob interface.
func (j *Job) newScatterWorkWithCompletedFn(taskFn boundTaskFunc, completedFn func()) workq.WorkFunc {

	baseWorkFn := func(ctx context.Context, ex workq.Execution) error {
		// Launch the task immediately without any pool tracking
		ex.Starting()
		j.startTask(ctx, func(ctx context.Context, taskWorkerOutboxMap *outboxMap) {
			taskFn(ctx, completedFn, taskWorkerOutboxMap)
		})
		return nil
	}

	return j.gcWaiters.Wrap(baseWorkFn,
		func(ctx context.Context) workq.WaitBehavior {
			_, meta := j.ctxMeta(ctx)
			return meta.WaitBehavior(j.gcMonitor.Busy)
		},
	)
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

// jobConfigWrapper wraps a Job to implement the jobConfig interface for options
type jobConfigWrapper struct {
	job *Job
}

func (w jobConfigWrapper) Update(changes opts.JobConfigChanges) {
	if changes.TaskWorkerIdleTimeout != nil {
		w.job.taskWorkerIdleTimeout.Store(int64(*changes.TaskWorkerIdleTimeout))
	}
	if changes.FlushListener != nil {
		w.job.state.SetFlushListener(*changes.FlushListener)
	}

	// Apply GC changes atomically using the embedded GCConfig
	w.job.gcMonitor.Update(changes.GCConfig)
}

// SetOptions applies the given configuration options to the job.
// This method is safe to call at any time and changes take effect immediately.
func (j *Job) SetOptions(options ...psgopt.JobOption) {
	opts.ApplyToJob(jobConfigWrapper{job: j}, options...)
}
