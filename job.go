// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/cerr"
	"github.com/petenewcomb/psg-go/internal/ctxmap"
	"github.com/petenewcomb/psg-go/internal/jobstate"
	"github.com/petenewcomb/psg-go/internal/nbcq"
	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/opts"
	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgopt"
)

//nolint:contextcheck // background context used only for tracing
type Job struct {
	ctx      context.Context //nolint:containedctx // used as parent for contexts in job-owned goroutines
	cancelFn context.CancelFunc
	wg       sync.WaitGroup
	state    jobstate.JobState

	gatherQueue workq.Pending

	// If there are tasks waiting to post work to gatherQueue, the governor
	// will block new top-level scatters, thereby applying backpressure to
	// regulate the system.
	governor workq.Governor

	workQueue workq.Accepted

	taskQueue                       rdvq.Queue[*taskWork]
	taskWorkerIdleTimeout           atomic.Int64 // stores time.Duration as nanoseconds
	taskWorkerIdleJitter            atomic.Int64 // stores time.Duration as nanoseconds
	taskWorkerSpawnConcurrencyLimit atomic.Int64 // maximum concurrent task worker spawns

	// Orphan task handling
	orphanedTasks         nbcq.Queue[*orphanedTaskWork] // buffer for orphaned tasks
	orphanWaiters         rdvq.Waiters                  // notification infrastructure
	taskWorkersSpawning   jobstate.InFlightCounter      // count of workers in spawning state
	unmetTaskWorkerDemand nbcq.Queue[*unmetDemandToken] // tracks tasks waiting for workers

	taskWorkerMu             sync.Mutex
	latestTaskWorkerIdleExit time.Time // protected by taskWorkerMu

	ctxMetaMap       ctxmap.Map[ctxMetaValueKey, *ctxMeta]
	gatherCtxMetaMap ctxmap.Map[gatherCtxMetaValueKey, *Job]

	protoBB      workq.BlockBehavior  // avoid closure reallocation
	blockFn      workq.BlockFunc      // avoid closure reallocation
	tryAddWorkFn workq.TryAddWorkFunc // avoid closure reallocation
	addWorkFn    workq.AddWorkFunc    // avoid closure reallocation
}

//nolint:contextcheck // background context used only for tracing
func (j *Job) newTaskWork(group workq.GroupID, task boundTask, completedFn func()) *taskWork {
	traceRegion := "Job.newTaskWork"

	w := taskWorkPool.Get()
	w.Init(group, j)
	w.task = task
	w.completedFn = completedFn

	trace.Logf(context.Background(), traceRegion, "Job=%p created %v", j, w)
	return w
}

type taskWork struct {
	jobWork
	task        boundTask
	completedFn func()
}

func (w *taskWork) Execute(ctx context.Context, taskWorkerSender *rdvq.Sender) {
	traceRegion := "taskWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()

	defer w.task.Free()
	w.task.Execute(ctx, w.Group(), w.completedFn, taskWorkerSender)
}

//nolint:contextcheck // background context used only for tracing
func (w *taskWork) Free(job *Job) {
	traceRegion := "taskWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	w.Close(job)
	taskWorkPool.Put(w)
}

var taskWorkPool = omnipool.For[taskWork]()

func (j *Job) getJob() *Job {
	return j
}

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
	traceRegion := "NewJob"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, cancelFn := context.WithCancel(ctx)
	j := &Job{
		ctx:      ctx,
		cancelFn: cancelFn,
	}

	j.protoBB.ShouldBlock = j.shouldBlock
	j.blockFn = j.block
	j.tryAddWorkFn = j.tryAddWork
	j.addWorkFn = j.addWork

	trace.Logf(ctx, traceRegion,
		"Job=%p, state=%p, gatherQueue=%p, governor=%p, workQueue=%p, taskQueue=%p",
		j, &j.state, &j.gatherQueue, &j.governor, &j.workQueue, &j.taskQueue)

	j.state.Init()
	j.gatherQueue.Init()
	j.governor.Init()
	j.workQueue.Init()
	j.taskQueue.Init()
	j.orphanedTasks.Init()
	j.orphanWaiters.Init()
	j.unmetTaskWorkerDemand.Init()
	// taskWorkersSpawning zero-value ready, no init needed
	j.taskWorkerIdleTimeout.Store(int64(psgopt.DefaultTaskWorkerIdleTimeout))
	j.taskWorkerIdleJitter.Store(int64(psgopt.DefaultTaskWorkerIdleJitter))
	j.taskWorkerSpawnConcurrencyLimit.Store(int64(psgopt.DefaultTaskWorkerSpawnConcurrencyLimit))

	// Apply user options
	j.SetOptions(options...)

	return j
}

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
	traceRegion := "Job.Cancel"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Job=%p", j)
	j.cancelFn()
}

// CancelAndWait cancels like [Job.Cancel], but then blocks until any
// outstanding task goroutines exit.
//
//nolint:contextcheck // background context used only for tracing
func (j *Job) CancelAndWait() {
	traceRegion := "Job.CancelAndWait"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	j.Cancel()
	j.wg.Wait()
	j.gatherCtxMetaMap.Clear()
	j.ctxMetaMap.Clear()
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
	traceRegion := "Job.Gather"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := j.vetGather(ctx)
	_, err := j.gather(ctx, meta)
	return err
}

func (j *Job) tryAddWork(ctx context.Context, queueFn workq.QueueWorkFunc) error {
	traceRegion := "Job.tryAddWork"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Job=%p", j)
	if workFn, ok := j.gatherQueue.TryPopFront(); ok {
		queueFn(workFn)
	}
	return nil
}

func (j *Job) vetGather(ctx context.Context) (context.Context, *ctxMeta) {
	return j.gatherCtxMeta(ctx)
}

func (j *Job) tryGather(ctx context.Context, _ *ctxMeta) (bool, error) {
	return j.workQueue.TryExecuteOne(ctx, j.tryAddWorkFn)
}

func (j *Job) gather(ctx context.Context, meta *ctxMeta) (bool, error) {
	return true, j.workQueue.ExecuteOne(ctx, j.addWorkFn, nil)
}

// This function is designed to be called before scattering a new task to
// preemptively gather or gather results from completed tasks. This smooths
// execution and adds backpressure that enables operation with unlimited task
// pools.
func (j *Job) yield(ctx context.Context, deadline time.Time) error {
	traceRegion := "Job.yield"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := j.vetGather(ctx)
	for {
		ok, err := j.tryGather(ctx, meta)
		if err != nil {
			return err
		}
		// Test for deadline passing only after trying at least one gather
		if !ok || (!deadline.IsZero() && !time.Now().Before(deadline)) {
			break
		}
	}
	return nil
}

const errBlockWaitSignaled = cerr.Error("block wait signaled")

func (j *Job) shouldBlock(ctx context.Context) workq.BlockFunc {
	_, meta := j.ctxMeta(ctx)
	if meta.IsTopLevel() {
		return j.blockFn
	}
	return nil
}

func (j *Job) block(
	ctx context.Context,
	blockDeadline time.Time,
	blockWaiters *workq.Waiters,
	confirmBlockWaitFn func() bool,
) (workq.RenotifyFunc, error) {
	traceRegion := "Job.block"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Job=%p", j)
	ctx, meta := j.vetGather(ctx)
	adder := blockingWorkAdderPool.Get()
	defer blockingWorkAdderPool.Put(adder)
	adder.job = j
	adder.meta = meta
	adder.blockDeadline = blockDeadline
	adder.blockWaiters = blockWaiters
	adder.confirmBlockWaitFn = confirmBlockWaitFn

	err := j.workQueue.ExecuteOne(ctx, adder.addWorkFn, nil)
	if errors.Is(err, errBlockWaitSignaled) {
		err = nil
	}
	return adder.blockWaitRenotifyFn, err
}

var blockingWorkAdderPool = omnipool.For[blockingWorkAdder]()

type blockingWorkAdder struct {
	job                 *Job
	meta                *ctxMeta
	blockDeadline       time.Time
	blockWaiters        *workq.Waiters
	confirmBlockWaitFn  func() bool
	blockWaitRenotifyFn workq.RenotifyFunc

	addWorkFn workq.AddWorkFunc
}

func (a *blockingWorkAdder) Init() {
	a.addWorkFn = a.addWork
}

func (a *blockingWorkAdder) Reset() {
	*a = blockingWorkAdder{
		addWorkFn: a.addWorkFn,
	}
}

func (a *blockingWorkAdder) addWork(
	ctx context.Context,
	queueFn workq.QueueWorkFunc,
	workWaiters *rdvq.Waiters,
	confirmWorkWaitFn func() bool,
) (workq.RenotifyFunc, error) {
	var workReadyRenotifyFn workq.RenotifyFunc
	var err error
	workReadyRenotifyFn, a.blockWaitRenotifyFn, err = a.job.addWorkWhileMaybeBlocking(
		ctx, a.meta, queueFn, workWaiters, confirmWorkWaitFn, a.blockDeadline, a.blockWaiters, a.confirmBlockWaitFn)
	return workReadyRenotifyFn, err
}

func (j *Job) addWork(
	ctx context.Context,
	queueFn workq.QueueWorkFunc,
	waiters *rdvq.Waiters,
	confirmWaitFn func() bool,
) (workq.RenotifyFunc, error) {
	traceRegion := "Job.addWork"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Job=%p", j)
	ctx, meta := j.ctxMeta(ctx)
	workReadyRenotifyFn, _, err := j.addWorkWhileMaybeBlocking(ctx, meta, queueFn, waiters,
		confirmWaitFn, time.Time{}, nil, nil)
	return workReadyRenotifyFn, err
}

func (j *Job) addWorkWhileMaybeBlocking(
	ctx context.Context,
	meta *ctxMeta,
	queueFn workq.QueueWorkFunc,
	workWaiters *rdvq.Waiters,
	confirmWorkWaitFn func() bool,
	blockDeadline time.Time,
	blockWaiters *workq.Waiters,
	confirmBlockWaitFn func() bool,
) (workReadyRenotifyFn, blockWaitRenotifyFn workq.RenotifyFunc, err error) {
	meta.PushQueueFunc(queueFn)
	defer meta.PopQueueFunc()

	var renotifyFns gatherRenotifyFuncs
	if workWaiters == nil {
		err = j.tryAddWork(ctx, queueFn)
	} else {
		j.gatherQueue.PopFrontFunc(
			meta.Receiver(),
			queueFn,
			func(inbox *rdvq.Inbox[workq.Work], outboxWaitInbox *rdvq.WaitInbox) rdvq.RenotifyFunc {
				workWaiters.WaitFunc(
					meta.Waiter(),
					confirmWorkWaitFn,
					func(workWaitInbox *rdvq.WaitInbox) {
						if blockWaiters == nil {
							renotifyFns, err = j.gatherSelect(
								ctx,
								queueFn,
								inbox,
								outboxWaitInbox,
								workWaitInbox,
								nil,
								nil,
							)
						} else {
							blockWaiters.WaitFunc(
								meta.Waiter(),
								func() bool {
									shouldWait := confirmBlockWaitFn()
									if !shouldWait {
										err = errBlockWaitSignaled
									}
									return shouldWait
								},
								func(blockWaitInbox *rdvq.WaitInbox) {
									var blockTimerCh <-chan time.Time
									if !blockDeadline.IsZero() {
										blockTimer := timerp.Get()
										defer timerp.Put(blockTimer)
										timerp.Reset(blockTimer, max(0, time.Until(blockDeadline)))
										blockTimerCh = blockTimer.C
									}
									renotifyFns, err = j.gatherSelect(
										ctx,
										queueFn,
										inbox,
										outboxWaitInbox,
										workWaitInbox,
										blockTimerCh,
										blockWaitInbox,
									)
								},
							)
						}
					},
				)
				return renotifyFns.Outbox
			},
		)
	}
	return renotifyFns.Work, renotifyFns.Block, err
}

type gatherRenotifyFuncs struct {
	Outbox rdvq.RenotifyFunc
	Work   rdvq.RenotifyFunc
	Block  rdvq.RenotifyFunc
}

func (j *Job) gatherSelect(
	ctx context.Context,
	queueFn workq.QueueWorkFunc,
	inbox *rdvq.Inbox[workq.Work],
	outboxWaitInbox *rdvq.WaitInbox,
	workWaitInbox *rdvq.WaitInbox,
	blockTimerCh <-chan time.Time,
	blockWaitInbox *rdvq.WaitInbox,
) (gatherRenotifyFuncs, error) {
	traceRegion := "Job.gatherSelect"

	inboxCh := inbox.Ch()
	outboxWaitCh := outboxWaitInbox.Ch()
	workWaitCh := workWaitInbox.Ch()
	blockWaitCh := blockWaitInbox.Ch()

	trace.Logf(ctx, traceRegion,
		"entering select: inbox=%p, inboxCh=%p, outboxWaitInbox=%p, outboxWaitCh=%p, workWaitInbox=%p, workWaitCh=%p, "+
			"blockWaitInbox=%p, blockWaitCh=%p",
		inbox, inboxCh, outboxWaitInbox, outboxWaitCh, workWaitInbox, workWaitCh, blockWaitInbox, blockWaitCh)
	var renotifyFns gatherRenotifyFuncs
	var err error
	select {
	case work := <-inboxCh:
		inbox.Emptied()
		trace.Logf(ctx, traceRegion, "received work from inbox=%p, inboxCh=%p", inbox, inboxCh)
		queueFn(work)
	case renotifyFns.Outbox = <-outboxWaitCh:
		outboxWaitInbox.Emptied()
		trace.Logf(ctx, traceRegion, "received renotifyFn from outboxWaitInbox=%p, outboxWaitCh=%p",
			outboxWaitInbox, outboxWaitCh)
	case renotifyFns.Work = <-workWaitCh:
		workWaitInbox.Emptied()
		trace.Logf(ctx, traceRegion, "received renotifyFn from workWaitInbox=%p, workWaitCh=%p", workWaitInbox, workWaitCh)
	case <-blockTimerCh:
		trace.Logf(ctx, traceRegion, "received block deadline timer signal")
		err = errBlockWaitSignaled
	case renotifyFns.Block = <-blockWaitCh:
		blockWaitInbox.Emptied()
		trace.Logf(ctx, traceRegion,
			"received renotifyFn from blockWaitInbox=%p, blockWaitCh=%p", blockWaitInbox, blockWaitCh)
		err = errBlockWaitSignaled
	case <-j.state.Done():
		trace.Logf(ctx, traceRegion, "received job done signal")
		err = ErrJobDone
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		err = ctx.Err()
	}
	return renotifyFns, err
}

type gatherPostWork struct {
	jobWork
	job  *Job
	work boundGatherWork
}

func (w *gatherPostWork) Init(group workq.GroupID, job *Job, work boundGatherWork) {
	w.jobWork.Init(group, job)
	w.job = job
	w.work = work
}

func (w *gatherPostWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "gatherPostWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", w)

	posted, err := func() (bool, error) {
		// Discover outbox from execution environment
		ctx, meta := w.job.ctxMeta(ctx)

		waiting := func() {
			// Call Waiting on the nested gatherWork to notify the governor
			w.work.Waiting(&w.job.governor)
		}

		tryPost := func() bool {
			// Try non-blocking post - can be retried if it fails
			return w.job.gatherQueue.TryPushBack(meta.Sender(), w.work, nil)
		}

		for {
			if tryPost() {
				return true, nil
			}

			if !ex.ShouldBlockOrPostpone() {
				return false, nil
			}

			if !meta.ShouldBlock() {
				// We expect to be queued and called again, so listen and don't block
				ex.AddToListeners(w.job.gatherQueue.ListenersFor(meta.Sender()))

				// Check again after registering for notification, but return
				// and expect to be called again if needed
				posted := tryPost()
				if !posted {
					waiting()
				}
				trace.Logf(ctx, traceRegion, "meta.QueueFunc() != nil, posted=%v", posted)
				return posted, nil
			}

			// Use blocking post
			posted := true
			var err error
			w.job.gatherQueue.PushBackFunc(meta.Sender(), w.work, nil, func(outbox *rdvq.Outbox[workq.Work]) {
				posted = false

				// Slow path, really going to block now
				ex.Blocking()

				waiting()

				err = rdvq.BasicPushSelect[workq.Work](ctx, outbox, w.work)
				if err == nil {
					posted = true
				}
			})
			trace.Logf(ctx, traceRegion, "meta.ShouldBlock(), posted=%v, err=%v", posted, err)
			if posted || err != nil {
				return posted, err
			}
		}
	}()

	if posted {
		ex.Starting() // Signal success only if we actually posted
		w.work = nil  // Clear the work reference since it's now owned by the queue
	}
	return err
}

//nolint:contextcheck // background context used only for tracing
func (w *gatherPostWork) Free() {
	traceRegion := "gatherPostWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	// Free the nested work item if we still own it
	if w.work != nil {
		trace.Logf(context.Background(), traceRegion, "w.work.Free()")
		w.work.Free()
		w.work = nil
	}

	w.Close(w.job)
	gatherPostWorkPool.Put(w)
}

var gatherPostWorkPool = omnipool.For[gatherPostWork]()

//nolint:contextcheck // background context used only for tracing
func (j *Job) newGatherPostWork(group workq.GroupID, gatherWork boundGatherWork) *gatherPostWork {
	traceRegion := "Job.newGatherPostWork"

	w := gatherPostWorkPool.Get()
	w.Init(group, j, gatherWork)

	trace.Logf(context.Background(), traceRegion, "Job=%p created %v", j, w)
	return w
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
	traceRegion := "Job.TryGather"
	defer trace.StartRegion(ctx, traceRegion).End()

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
	traceRegion := "Job.GatherAll"
	defer trace.StartRegion(ctx, traceRegion).End()

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
	traceRegion := "Job.TryGatherAll"
	defer trace.StartRegion(ctx, traceRegion).End()

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

// spawnTaskWorkerGoroutine spawns a new task worker goroutine.
// Caller must have already incremented taskWorkersSpawning.
//
//nolint:contextcheck // goroutine will use job context
func (j *Job) spawnTaskWorker() {
	traceRegion := "Job.spawnTaskWorker"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	j.wg.Add(1)
	go j.runTasks()
}

// Attempts to spawn a worker if we're under the spawn concurrency limit.
//
//nolint:contextcheck // background context used only for tracing
func (j *Job) trySpawnTaskWorker() bool {
	traceRegion := "Job.trySpawnTaskWorker"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	// Try to spawn within concurrency limit
	limit := int(j.taskWorkerSpawnConcurrencyLimit.Load())
	if limit == -1 {
		// Unlimited - always spawn
		j.taskWorkersSpawning.Increment()
	} else if !j.taskWorkersSpawning.IncrementIfUnder(limit) {
		// Limited - spawn only if under limit
		return false
	}

	j.spawnTaskWorker()
	return true
}

// registerTaskWorkerDemand increments the demand counter and attempts to spawn
// a worker if we're under the spawn concurrency limit. This should be called
// once per task that needs a worker. Returns zero value if the demand was
// immediately satisfied by spawning a new worker, or an unmetDemand handle if
// the demand could not be immediately met.
//
//nolint:contextcheck // background context used only for tracing
func (j *Job) registerTaskWorkerDemand() unmetDemand {
	traceRegion := "Job.registerTaskWorkerDemand"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	if j.trySpawnTaskWorker() {
		return unmetDemand{} // zero value = no demand token
	}

	token := unmetDemandTokenPool.Get()
	token.mu.Lock()
	token.id = unmetDemandID(unmetDemandTokenCounter.Add(1))
	token.stillNeeded = true
	id := token.id
	token.mu.Unlock()
	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "Job=%p, at spawn limit, unmetDemandID=%d", j, id)
	}
	j.unmetTaskWorkerDemand.PushBack(token)
	return unmetDemand{token: token, id: id}
}

type unmetDemandID int64

type unmetDemandToken struct {
	mu          sync.Mutex
	id          unmetDemandID
	stillNeeded bool
}

// Don't reinitialize the mutex
func (t *unmetDemandToken) Reset() {}

var unmetDemandTokenCounter atomic.Int64
var unmetDemandTokenPool = omnipool.For[unmetDemandToken]()

// unmetDemand represents registered demand for a task worker.
// The zero value is valid and represents no demand.
type unmetDemand struct {
	token *unmetDemandToken
	id    unmetDemandID
}

func (d *unmetDemand) RegisterDemand(j *Job) {
	token := d.token
	if token == nil {
		*d = j.registerTaskWorkerDemand()
	} else {
		trySpawn := false
		func() {
			token.mu.Lock()
			defer token.mu.Unlock()
			if token.id == d.id {
				trySpawn = token.stillNeeded
			}
		}()
		if trySpawn && j.trySpawnTaskWorker() {
			token.mu.Lock()
			defer token.mu.Unlock()
			token.stillNeeded = false
		}
	}
}

// Cancel cancels this demand if it hasn't already been satisfied.
// Safe to call on zero value or multiple times.
func (d *unmetDemand) Cancel() {
	token := d.token
	if token == nil {
		return
	}
	id := d.id
	d.id = 0
	d.token = nil

	token.mu.Lock()
	defer token.mu.Unlock()
	if token.id == id {
		token.stillNeeded = false
	}
}

type orphanedTaskWork struct {
	mu     sync.Mutex
	id     orphanedTaskID
	job    *Job
	work   *taskWork
	demand unmetDemand
}

func newOrphanedTaskWork(j *Job, tw *taskWork) *orphanedTaskWork {
	w := orphanedTaskWorkPool.Get()
	w.id = orphanedTaskID(orphanedTaskIDCounter.Add(1))
	w.job = j
	w.work = tw
	return w
}

func (w *orphanedTaskWork) Reset() {
	// No-op. All is done while the mutex is still held in Unwrap()
}

func (w *orphanedTaskWork) registerDemand(id orphanedTaskID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.id == id {
		w.demand.RegisterDemand(w.job)
	}
}

func (w *orphanedTaskWork) Unwrap() *taskWork {
	var work *taskWork
	func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		work = w.work
		w.id = 0
		w.job = nil
		w.work = nil
		w.demand.Cancel()
	}()
	orphanedTaskWorkPool.Put(w)
	return work
}

var orphanedTaskWorkPool = omnipool.For[orphanedTaskWork]()

type orphanedTaskID int64

var orphanedTaskIDCounter atomic.Int64

type orphanedTaskRenotify struct {
	id         orphanedTaskID
	work       *orphanedTaskWork
	RenotifyFn rdvq.RenotifyFunc
}

func newOrphanedTaskRenotify(work *orphanedTaskWork) *orphanedTaskRenotify {
	r := orphanedTaskRenotifyPool.Get()
	r.id = work.id
	r.work = work
	return r
}

func (r *orphanedTaskRenotify) Init() {
	r.RenotifyFn = r.renotify
}

func (r *orphanedTaskRenotify) Reset() {
	r.work = nil
	r.id = 0
}

func (r *orphanedTaskRenotify) renotify() {
	r.work.registerDemand(r.id)
	r.Free()
}

func (r *orphanedTaskRenotify) Free() {
	orphanedTaskRenotifyPool.Put(r)
}

var orphanedTaskRenotifyPool = omnipool.For[orphanedTaskRenotify]()

func (j *Job) tryGetOrphanedTask() *taskWork {
	if orphan, ok := j.orphanedTasks.TryPopFront(); ok {
		return orphan.Unwrap()
	}
	return nil
}

//nolint:contextcheck // task worker goroutine will use job context
func (j *Job) runTasks() {
	defer j.wg.Done()

	var task *taskWork

	spawning := true
	defer func() {
		if spawning {
			j.taskWorkersSpawning.Decrement() // Safety: always release if still spawning
		}
	}()

	traceRegion := "taskWorker.Run"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Job=%p, spawning=%v", j, spawning)

	goroutineCtx, cancelGoroutineCtx := context.WithCancel(j.ctx)
	defer func() {
		trace.Logf(context.Background(), traceRegion, "canceling goroutine context")
		cancelGoroutineCtx()
	}()

	var exEnv taskExEnv

	ctx, _ := j.ensureCtxMeta(goroutineCtx,
		func(ctx context.Context, meta *ctxMeta) context.Context {
			meta.ctxType = taskContext
			// Create execution environment with access to outboxes
			meta.executionEnvironment = &exEnv
			return ctx
		},
	)

	var receiver rdvq.Receiver
	var waiter rdvq.Waiter

	var idleTimer *time.Timer
	defer func() {
		if idleTimer != nil {
			timerp.Put(idleTimer)
		}
	}()

	for {
		// Fast-path: check both queues before expensive waiter registration
		if task == nil {
			task = j.tryGetOrphanedTask()
		}
		if task == nil {
			if t, ok := j.taskQueue.TryPopFront(); ok {
				task = t
			}
		}

		if task != nil {
			// Secured task - release spawn counter
			if spawning {
				spawning = false
				decrementSpawning := true
				for {
					token, ok := j.unmetTaskWorkerDemand.TryPopFront()
					if !ok {
						break
					}
					spawnAnother := func() bool {
						token.mu.Lock()
						defer token.mu.Unlock()
						stillNeeded := token.stillNeeded
						token.id = 0
						token.stillNeeded = false
						return stillNeeded
					}()
					unmetDemandTokenPool.Put(token)
					if spawnAnother {
						j.spawnTaskWorker()
						decrementSpawning = false
						break
					}
				}
				if decrementSpawning {
					j.taskWorkersSpawning.Decrement()
				}
			}

			// Execute the task
			func() {
				defer task.Free(j)
				task.Execute(ctx, exEnv.Sender())
			}()
			task = nil
			continue
		}

		// No immediately available work - now worth paying waiter registration cost
		// Wait for next task with timeout
		idleTimeout := time.Duration(j.taskWorkerIdleTimeout.Load())
		var idleTimerCh <-chan time.Time
		if idleTimeout != -1 {
			// Timeout enabled - ensure we have a timer and set it
			if idleTimer == nil {
				idleTimer = timerp.Get()
			}
			maxJitter := time.Duration(j.taskWorkerIdleJitter.Load())
			jitter := time.Duration(rand.Int64N(int64(maxJitter))) //nolint:gosec // jitter doesn't need crypto/rand
			timerp.Reset(idleTimer, idleTimeout+jitter)
			idleTimerCh = idleTimer.C
		} else if idleTimer != nil {
			// Timeout disabled - return timer to pool
			timerp.Put(idleTimer)
			idleTimer = nil
		}

		exit := false
		j.taskQueue.PopFrontFunc(
			&receiver,
			func(orphanedTask *taskWork) {
				if task == nil {
					task = orphanedTask
					exit = false // Orphans are detected after selectFn returns
				} else {
					orphan := newOrphanedTaskWork(j, orphanedTask)
					orphanRenotify := newOrphanedTaskRenotify(orphan)
					j.orphanedTasks.PushBack(orphan)
					j.orphanWaiters.Notify(orphanRenotify.RenotifyFn)
				}
			},
			func(inbox *rdvq.Inbox[*taskWork], outboxWaitInbox *rdvq.WaitInbox) rdvq.RenotifyFunc {
				var renotifyFn rdvq.RenotifyFunc
				j.orphanWaiters.WaitFunc(&waiter,
					func() bool {
						// Check orphan queue after registration to avoid race
						task = j.tryGetOrphanedTask()
						if task != nil {
							return false // Don't wait, we found an orphan
						}
						return true // Wait for notification
					},
					func(orphanWaitInbox *rdvq.WaitInbox) {
						inboxCh := inbox.Ch()
						outboxWaitCh := outboxWaitInbox.Ch()
						orphanWaitCh := orphanWaitInbox.Ch()
						//nolint:lll // trace message readability
						trace.Logf(ctx, traceRegion,
							"entering select: inbox=%p, inboxCh=%p, outboxWaitInbox=%p, outboxWaitCh=%p, orphanWaitInbox=%p, orphanWaitCh=%p",
							inbox, inboxCh, outboxWaitInbox, outboxWaitCh, orphanWaitInbox, orphanWaitCh)
						select {
						case task = <-inboxCh:
							inbox.Emptied()
							trace.Logf(ctx, traceRegion, "received task from inbox=%p, inboxCh=%p", inbox, inboxCh)
						case renotifyFn = <-outboxWaitCh:
							outboxWaitInbox.Emptied()
						case <-orphanWaitCh:
							orphanWaitInbox.Emptied()
							trace.Logf(ctx, traceRegion, "received orphan notification from orphanWaitInbox=%p, orphanWaitCh=%p",
								orphanWaitInbox, orphanWaitCh)
							// Check orphan queue again after notification
							task = j.tryGetOrphanedTask()
						case <-idleTimerCh:
							trace.Logf(ctx, traceRegion, "received signal from idle timer")
							exit = j.tryTaskWorkerIdleExit()
						case <-ctx.Done():
							trace.Logf(ctx, traceRegion, "received context done signal")
							exit = true
						}
					})
				return renotifyFn
			},
		)
		if exit {
			if task != nil {
				panic("exiting with non-nil task")
			}
			break
		}
	}
}

type taskPostWork struct {
	jobWork
	job    *Job
	task   *taskWork
	demand unmetDemand
}

func (w *taskPostWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "taskPostWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "%v", w)
	}

	registerDemand := func() {
		w.demand.RegisterDemand(w.job)
	}

	posted, err := func() (bool, error) {
		ctx, meta := w.job.ctxMeta(ctx)

		bufferedFn := func() {
			// Work was buffered in the sender's outbox because a task worker wasn't
			// immediately available -- go ahead and start one if we can.
			registerDemand()
		}

		tryPost := func() bool {
			// Try non-blocking post - can be retried if it fails
			return w.job.taskQueue.TryPushBack(meta.Sender(), w.task, bufferedFn)
		}

		for {
			if tryPost() {
				return true, nil
			}

			if !ex.ShouldBlockOrPostpone() {
				return false, nil
			}

			// Post attempt failed, need more task workers
			registerDemand()

			if !meta.ShouldBlock() {
				// We expect to be queued and called again, so listen and don't block
				ex.AddToListeners(w.job.taskQueue.ListenersFor(meta.Sender()))

				// Check again after registering for notification, but return
				// and expect to be called again if needed
				posted := tryPost()
				if trace.IsEnabled() {
					trace.Logf(ctx, traceRegion, "meta.ShouldBlock() == false, posted=%v", posted)
				}

				return posted, nil
			}

			// Use blocking post
			posted := true
			var err error
			w.job.taskQueue.PushBackFunc(meta.Sender(), w.task, bufferedFn, func(outbox *rdvq.Outbox[*taskWork]) {
				posted = false

				// Slow path, really going to block now
				ex.Blocking()

				err = rdvq.BasicPushSelect[*taskWork](ctx, outbox, w.task)
				if err == nil {
					posted = true
				}
			})
			if trace.IsEnabled() {
				trace.Logf(ctx, traceRegion, "meta.ShouldBlock() == true, posted=%v, err=%v", posted, err)
			}
			if posted || err != nil {
				return posted, err
			}
		}
	}()

	if posted {
		ex.Starting() // Signal success only if we actually posted
		w.task = nil  // Clear the work reference since it's now owned by the queue
	}
	return err
}

//nolint:contextcheck // background context used only for tracing
func (w *taskPostWork) Free() {
	traceRegion := "taskPostWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	// Cancel demand before returning to pool
	w.demand.Cancel()

	// Free the nested task work if we still own it
	if w.task != nil {
		trace.Logf(context.Background(), traceRegion, "w.task.Free()")
		w.task.Free(w.job)
	}

	w.Close(w.job)
	taskPostWorkPool.Put(w)
}

var taskPostWorkPool = omnipool.For[taskPostWork]()

type jobWork struct {
	workq.WorkItem
}

func (w *jobWork) Init(group workq.GroupID, job *Job) {
	w.WorkItem.Init(group)
	trace.Logf(context.Background(), "jobWork.Init", "%v", &w.WorkItem)
	job.state.IncrementWork()
}

//nolint:contextcheck // background context used only for tracing
func (w *jobWork) Close(job *Job) {
	if w.ID() == 0 {
		// This check and panic is best-effort only as it may also be a race if
		// Close() is called from multiple goroutines -- which it should not be.
		panic("already closed")
	}
	trace.Logf(context.Background(), "jobWork.Close", "%v", &w.WorkItem)
	job.state.DecrementWork()
}

func (j *Job) newScatterWork(group workq.GroupID, deadline time.Time, task boundTask) workq.Work {
	taskWork := j.newTaskWork(group, task, nil)
	return j.newTaskPostWork(group, deadline, taskWork)
}

func (j *Job) newTaskPostWork(group workq.GroupID, deadline time.Time, task *taskWork) workq.Work {
	w := taskPostWorkPool.Get()
	w.Init(group, j)
	w.job = j
	w.task = task
	return w
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
//
//nolint:contextcheck // background context used only for tracing
func (j *Job) Close() {
	traceRegion := "Job.Close"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	j.state.Close()
}

// CloseAndGatherAll closes the job via [Job.Close] and then waits for and
// gathers the results of all in-flight tasks via [Job.GatherAll].
func (j *Job) CloseAndGatherAll(ctx context.Context) error {
	traceRegion := "Job.CloseAndGatherAll"
	defer trace.StartRegion(ctx, traceRegion).End()

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
	if changes.TaskWorkerIdleJitter != nil {
		w.job.taskWorkerIdleJitter.Store(int64(*changes.TaskWorkerIdleJitter))
	}
	if changes.TaskWorkerSpawnConcurrencyLimit != nil {
		w.job.taskWorkerSpawnConcurrencyLimit.Store(int64(*changes.TaskWorkerSpawnConcurrencyLimit))
	}
	if changes.FlushListener != nil {
		w.job.state.SetFlushListener(*changes.FlushListener)
	}
}

// tryTaskWorkerIdleExit attempts to record an idle task worker exit. Returns true if this worker
// is allowed to exit (enough time has passed since latest exit), false if
// another worker exited too recently and this worker should retry later.
func (j *Job) tryTaskWorkerIdleExit() bool {
	j.taskWorkerMu.Lock()
	defer j.taskWorkerMu.Unlock()

	now := time.Now()
	idleTimeout := time.Duration(j.taskWorkerIdleTimeout.Load())
	if now.Sub(j.latestTaskWorkerIdleExit) >= idleTimeout {
		j.latestTaskWorkerIdleExit = now
		return true
	}
	return false
}

// SetOptions applies the given configuration options to the job.
// This method is safe to call at any time and changes take effect immediately.
//
//nolint:contextcheck // background context used only for tracing
func (j *Job) SetOptions(options ...psgopt.JobOption) {
	traceRegion := "Job.SetOptions"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	opts.ApplyToJob(jobConfigWrapper{job: j}, options...)
}
