// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/cerr"
	"github.com/petenewcomb/psg-go/internal/ctxmap"
	"github.com/petenewcomb/psg-go/internal/jobstate"
	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/opts"
	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/petenewcomb/psg-go/internal/timerp"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgopt"
)

//nolint:contextcheck // background context used only for tracing
type Pool struct {
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

	taskWorkersSpawning jobstate.InFlightCounter // count of workers in spawning state
	taskWorkerDemand    jobstate.InFlightCounter // count of tasks waiting for workers

	taskWorkerMu             sync.Mutex
	latestTaskWorkerIdleExit time.Time // protected by taskWorkerMu

	ctxMetaMap       ctxmap.Map[ctxMetaValueKey, *ctxMeta]
	gatherCtxMetaMap ctxmap.Map[gatherCtxMetaValueKey, *Pool]

	protoBB      workq.BlockBehavior  // avoid closure reallocation
	blockFn      workq.BlockFunc      // avoid closure reallocation
	tryAddWorkFn workq.TryAddWorkFunc // avoid closure reallocation
	addWorkFn    workq.AddWorkFunc    // avoid closure reallocation
}

//nolint:contextcheck // background context used only for tracing
func (j *Pool) newTaskWork(group workq.GroupID, task boundTask, completedFn func()) *taskWork {
	traceRegion := "Pool.newTaskWork"

	w := taskWorkPool.Get()
	w.Init(group, j)
	w.task = task
	w.completedFn = completedFn

	trace.Logf(context.Background(), traceRegion, "Pool=%p created %v", j, w)
	return w
}

type taskWork struct {
	poolWork
	task             boundTask
	completedFn      func()
	demandRegistered atomic.Bool
}

func (w *taskWork) Reset() {
	if trace.IsEnabled() {
		trace.Logf(context.Background(), "taskWork.Reset",
			"DEMAND_RESET task=%p demandRegistered=%v", w, w.demandRegistered.Load())
	}
	w.poolWork = poolWork{}
	w.task = nil
	w.completedFn = nil
	if w.demandRegistered.Load() {
		panic(fmt.Sprintf("taskWork.Reset: demandRegistered still true - unbalanced demand counter (task=%p)", w))
	}
}

func (w *taskWork) Execute(ctx context.Context, taskWorkerSender *rdvq.Sender) {
	traceRegion := "taskWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()

	w.task.Execute(ctx, w.Group(), w.completedFn, taskWorkerSender)
}

//nolint:contextcheck // background context used only for tracing
func (w *taskWork) Free(job *Pool) {
	traceRegion := "taskWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	w.task.Free()
	// If demand was registered but task never picked up, decrement the counter
	if w.demandRegistered.CompareAndSwap(true, false) {
		if trace.IsEnabled() {
			trace.Logf(context.Background(), traceRegion, "DEMAND_DEC_FREE task=%p counter=%p", w, &job.taskWorkerDemand)
		}
		job.taskWorkerDemand.Decrement()
	}
	w.Close(job)
	taskWorkPool.Put(w)
}

var taskWorkPool = omnipool.For[taskWork]()

func (j *Pool) getJob() *Pool {
	return j
}

// New creates an independent scatter-gather execution environment with the
// specified context. The context passed to New is used as the root of the
// context that will be passed to all task functions. (See [Task] and
// [Pool.Cancel] for more detail.)
//
// Use [NewTaskPool] to create task pools bound to this job.
//
// Each call to New should typically be followed by a deferred call to
// [Pool.CancelAndWait] to ensure that an early exit from the calling function
// does not leave any outstanding goroutines.
func New(ctx context.Context, options ...psgopt.PoolOption) *Pool {
	traceRegion := "New"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, cancelFn := context.WithCancel(ctx)
	j := &Pool{
		ctx:      ctx,
		cancelFn: cancelFn,
	}

	j.protoBB.ShouldBlock = j.shouldBlock
	j.blockFn = j.block
	j.tryAddWorkFn = j.tryAddWork
	j.addWorkFn = j.addWork

	trace.Logf(ctx, traceRegion,
		"Pool=%p, state=%p, gatherQueue=%p, governor=%p, workQueue=%p, taskQueue=%p",
		j, &j.state, &j.gatherQueue, &j.governor, &j.workQueue, &j.taskQueue)

	j.state.Init()
	j.gatherQueue.Init()
	j.governor.Init()
	j.workQueue.Init()
	j.taskQueue.Init()
	// taskWorkersSpawning zero-value ready, no init needed
	j.taskWorkerIdleTimeout.Store(int64(psgopt.DefaultTaskWorkerIdleTimeout))
	j.taskWorkerIdleJitter.Store(int64(psgopt.DefaultTaskWorkerIdleJitter))
	j.taskWorkerSpawnConcurrencyLimit.Store(int64(psgopt.DefaultTaskWorkerSpawnConcurrencyLimit))

	// Apply user options
	j.SetOptions(options...)

	return j
}

// Cancel terminates any in-flight tasks and forfeits any ungathered results.
// Outstanding calls to [Start], [Pool.Gather], [Pool.TryGather],
// [Pool.GatherAll], or [Pool.TryGatherAll] using the job or any of its task pools will
// fail with [context.Canceled] or other error returned by a [Gather].
//
// While Cancel always returns immediately, any running [Task] or
// [Gather] will delay termination of their independent goroutine or caller
// until it returns. This method cancels the context passed to each [Task],
// but not the context passed to each [Gather]. Gather functions instead
// receive the context passed to the calling [Start], [Pool.Gather],
// [Pool.TryGather], [Pool.GatherAll], or [Pool.TryGatherAll] function. If it is
// desirable to transmit a cancelation signal to a running [Gather], one
// must also cancel any contexts being passed to those callers.
//
// Cancel is always thread-safe and calling it more than once has no additional
// effect.
//
//nolint:contextcheck // background context used only for tracing
func (j *Pool) Cancel() {
	traceRegion := "Pool.Cancel"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Pool=%p", j)
	j.cancelFn()
}

// CancelAndWait cancels like [Pool.Cancel], but then blocks until any
// outstanding task goroutines exit.
//
//nolint:contextcheck // background context used only for tracing
func (j *Pool) CancelAndWait() {
	traceRegion := "Pool.CancelAndWait"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	j.Cancel()
	j.wg.Wait()
	j.gatherCtxMetaMap.Clear()
	j.ctxMetaMap.Clear()
}

// Gather processes outstanding task results and then waits for the next
// task result from a task previously launched via [Start]. It will block until
// a completed task is available, the provided context or job is canceled, or
// another event causes a wake-up (e.g. a call to [TaskPool.SetOptions]).
// If the job is closed and no tasks remain in flight, it will return immediately.
// See [Pool.TryGather] for a non-blocking alternative.
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
func (j *Pool) Gather(ctx context.Context) error {
	traceRegion := "Pool.Gather"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := j.vetGather(ctx)
	_, err := j.gather(ctx, meta)
	return err
}

func (j *Pool) tryAddWork(ctx context.Context, queueFn workq.QueueWorkFunc) error {
	traceRegion := "Pool.tryAddWork"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Pool=%p", j)
	if workFn, ok := j.gatherQueue.TryPopFront(); ok {
		queueFn(workFn)
	}
	return nil
}

func (j *Pool) vetGather(ctx context.Context) (context.Context, *ctxMeta) {
	return j.gatherCtxMeta(ctx)
}

func (j *Pool) tryGather(ctx context.Context, _ *ctxMeta) (bool, error) {
	return j.workQueue.TryExecuteOne(ctx, j.tryAddWorkFn)
}

func (j *Pool) gather(ctx context.Context, meta *ctxMeta) (bool, error) {
	return true, j.workQueue.ExecuteOne(ctx, j.addWorkFn, nil)
}

// This function is designed to be called before scattering a new task to
// preemptively gather or gather results from completed tasks. This smooths
// execution and adds backpressure that enables operation with unlimited task
// pools.
func (j *Pool) yield(ctx context.Context, deadline time.Time) error {
	traceRegion := "Pool.yield"
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

func (j *Pool) shouldBlock(ctx context.Context) workq.BlockFunc {
	_, meta := j.ctxMeta(ctx)
	if meta.IsTopLevel() {
		return j.blockFn
	}
	return nil
}

func (j *Pool) block(
	ctx context.Context,
	blockDeadline time.Time,
	blockWaiters *workq.Waiters,
	confirmBlockWaitFn func() bool,
) (workq.RenotifyFunc, error) {
	traceRegion := "Pool.block"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Pool=%p", j)
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
	job                 *Pool
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

func (j *Pool) addWork(
	ctx context.Context,
	queueFn workq.QueueWorkFunc,
	waiters *rdvq.Waiters,
	confirmWaitFn func() bool,
) (workq.RenotifyFunc, error) {
	traceRegion := "Pool.addWork"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "Pool=%p", j)
	ctx, meta := j.ctxMeta(ctx)
	workReadyRenotifyFn, _, err := j.addWorkWhileMaybeBlocking(ctx, meta, queueFn, waiters,
		confirmWaitFn, time.Time{}, nil, nil)
	return workReadyRenotifyFn, err
}

func (j *Pool) addWorkWhileMaybeBlocking(
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

	var workRf, blockRf rdvq.RenotifyFunc
	if workWaiters == nil {
		err = j.tryAddWork(ctx, queueFn)
	} else {
		var psResult rdvq.PopSelectResult[workq.Work]
		work, ok := j.gatherQueue.PopFrontFunc(
			meta.Receiver(),
			func(inboxCh <-chan workq.Work, outboxWaitCh <-chan rdvq.RenotifyFunc) rdvq.PopSelectResult[workq.Work] {
				workRf = workWaiters.WaitFunc(
					meta.Waiter(),
					confirmWorkWaitFn,
					func(workWaitCh <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
						var innerWorkRf rdvq.RenotifyFunc
						if blockWaiters == nil {
							psResult, innerWorkRf, _, err = j.gatherSelect(
								ctx, inboxCh, outboxWaitCh, workWaitCh, nil, nil,
							)
						} else {
							blockRf = blockWaiters.WaitFunc(
								meta.Waiter(),
								func() bool {
									shouldWait := confirmBlockWaitFn()
									if !shouldWait {
										err = errBlockWaitSignaled
									}
									return shouldWait
								},
								func(blockWaitCh <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
									var blockTimerCh <-chan time.Time
									if !blockDeadline.IsZero() {
										blockTimer := timerp.Get()
										defer timerp.Put(blockTimer)
										timerp.Reset(blockTimer, max(0, time.Until(blockDeadline)))
										blockTimerCh = blockTimer.C
									}
									var innerBlockRf rdvq.RenotifyFunc
									psResult, innerWorkRf, innerBlockRf, err = j.gatherSelect(
										ctx, inboxCh, outboxWaitCh, workWaitCh, blockTimerCh, blockWaitCh,
									)
									return innerBlockRf
								},
							)
						}
						return innerWorkRf
					},
				)
				return psResult
			},
		)
		if ok {
			queueFn(work)
		}
	}
	return workRf, blockRf, err
}

func (j *Pool) gatherSelect(
	ctx context.Context,
	inboxCh <-chan workq.Work,
	outboxWaitCh <-chan rdvq.RenotifyFunc,
	workWaitCh <-chan rdvq.RenotifyFunc,
	blockTimerCh <-chan time.Time,
	blockWaitCh <-chan rdvq.RenotifyFunc,
) (psResult rdvq.PopSelectResult[workq.Work], workRf, blockRf rdvq.RenotifyFunc, err error) {
	traceRegion := "Pool.gatherSelect"
	trace.Logf(ctx, traceRegion,
		"entering select: inboxCh=%p, outboxWaitCh=%p, workWaitCh=%p, blockWaitCh=%p",
		inboxCh, outboxWaitCh, workWaitCh, blockWaitCh)
	select {
	case work := <-inboxCh:
		trace.Logf(ctx, traceRegion, "received work from inboxCh=%p", inboxCh)
		psResult.InboxEmptied(work)
	case rf := <-outboxWaitCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from outboxWaitCh=%p", outboxWaitCh)
		psResult.OutboxReady(rf)
	case workRf = <-workWaitCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from workWaitCh=%p", workWaitCh)
	case <-blockTimerCh:
		trace.Logf(ctx, traceRegion, "received block deadline timer signal")
		err = errBlockWaitSignaled
	case blockRf = <-blockWaitCh:
		trace.Logf(ctx, traceRegion, "received renotifyFn from blockWaitCh=%p", blockWaitCh)
		err = errBlockWaitSignaled
	case <-j.state.Done():
		trace.Logf(ctx, traceRegion, "received job done signal")
		err = ErrJobDone
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		err = ctx.Err()
	}
	return
}

type gatherPostWork struct {
	poolWork
	job  *Pool
	work boundGatherWork
}

func (w *gatherPostWork) Init(group workq.GroupID, job *Pool, work boundGatherWork) {
	w.poolWork.Init(group, job)
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
			w.job.gatherQueue.PushBackFunc(meta.Sender(), w.work, nil, func(outboxCh chan<- workq.Work) bool {
				posted = false

				// Slow path, really going to block now
				ex.Blocking()

				waiting()

				var sent bool
				sent, err = rdvq.BasicPushSelect[workq.Work](ctx, outboxCh, w.work)
				if sent {
					posted = true
				}
				return sent
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
func (j *Pool) newGatherPostWork(group workq.GroupID, gatherWork boundGatherWork) *gatherPostWork {
	traceRegion := "Pool.newGatherPostWork"

	w := gatherPostWorkPool.Get()
	w.Init(group, j, gatherWork)

	trace.Logf(context.Background(), traceRegion, "Pool=%p created %v", j, w)
	return w
}

// TryGather processes outstanding task results and then attempts to process
// the next task result from a task previously launched via [Start]. Unlike
// [Pool.Gather], it will not block if a completed task is not immediately available.
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
func (j *Pool) TryGather(ctx context.Context) (bool, error) {
	traceRegion := "Pool.TryGather"
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
func (j *Pool) GatherAll(ctx context.Context) error {
	traceRegion := "Pool.GatherAll"
	defer trace.StartRegion(ctx, traceRegion).End()

	err := j.gatherAll(ctx, j.gather)
	if errors.Is(err, ErrJobDone) {
		return nil
	}
	return err
}

// TryGatherAll processes all currently available task results without blocking.
// Unlike [Pool.GatherAll], TryGatherAll will return immediately if there are no
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
func (j *Pool) TryGatherAll(ctx context.Context) error {
	traceRegion := "Pool.TryGatherAll"
	defer trace.StartRegion(ctx, traceRegion).End()

	return j.gatherAll(ctx, j.tryGather)
}

func (j *Pool) gatherAll(ctx context.Context, gatherFn func(context.Context, *ctxMeta) (bool, error)) error {
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
func (j *Pool) spawnTaskWorker() {
	traceRegion := "Pool.spawnTaskWorker"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	j.wg.Add(1)
	go j.runTasks()
}

// Attempts to spawn a worker if we're under the spawn concurrency limit.
//
//nolint:contextcheck // background context used only for tracing
func (j *Pool) trySpawnTaskWorker() bool {
	traceRegion := "Pool.trySpawnTaskWorker"
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

//nolint:contextcheck // task worker goroutine will use job context
func (j *Pool) runTasks() {
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
	trace.Logf(context.Background(), traceRegion, "Pool=%p, spawning=%v", j, spawning)

	goroutineCtx, cancelGoroutineCtx := context.WithCancel(j.ctx)
	defer func() {
		trace.Logf(context.Background(), traceRegion, "canceling goroutine context")
		cancelGoroutineCtx()
	}()

	var exEnv taskExEnv
	defer exEnv.Release()

	ctx, _ := j.ensureCtxMeta(goroutineCtx,
		func(ctx context.Context, meta *ctxMeta) context.Context {
			meta.ctxType = taskContext
			// Create execution environment with access to outboxes
			meta.executionEnvironment = &exEnv
			return ctx
		},
	)

	var receiver rdvq.Receiver
	defer receiver.Release()

	var idleTimer *time.Timer
	defer func() {
		if idleTimer != nil {
			timerp.Put(idleTimer)
		}
	}()

	for {
		// Fast-path: check the queue before expensive waiter registration
		if task == nil {
			if t, ok := j.taskQueue.TryPopFront(); ok {
				task = t
			}
		}

		if task != nil {
			// Decrement demand counter when worker receives task
			if task.demandRegistered.CompareAndSwap(true, false) {
				if trace.IsEnabled() {
					trace.Logf(ctx, traceRegion, "DEMAND_DEC_WORKER task=%p counter_before=%p", task, &j.taskWorkerDemand)
				}
				j.taskWorkerDemand.Decrement()
			}

			// Secured task - release spawn counter
			if spawning {
				spawning = false
				if j.taskWorkerDemand.IsZero() {
					j.taskWorkersSpawning.Decrement()
				} else {
					j.spawnTaskWorker()
				}
			}

			// Execute the task. Publish the task's group on the exEnv so
			// user-facing Submit calls from inside the task body inherit
			// that group instead of allocating a fresh one.
			func() {
				defer task.Free(j)
				exEnv.group = task.Group()
				defer func() { exEnv.group = workq.InvalidGroupID }()
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
		t, ok := j.taskQueue.PopFrontFunc(
			&receiver,
			func(inboxCh <-chan *taskWork, outboxWaitCh <-chan rdvq.RenotifyFunc) (result rdvq.PopSelectResult[*taskWork]) {
				trace.Logf(ctx, traceRegion,
					"entering select: inboxCh=%p, outboxWaitCh=%p", inboxCh, outboxWaitCh)
				select {
				case received := <-inboxCh:
					trace.Logf(ctx, traceRegion, "received task from inboxCh=%p", inboxCh)
					result.InboxEmptied(received)
				case rf := <-outboxWaitCh:
					trace.Logf(ctx, traceRegion, "received renotifyFn from outboxWaitCh=%p", outboxWaitCh)
					result.OutboxReady(rf)
				case <-idleTimerCh:
					trace.Logf(ctx, traceRegion, "received signal from idle timer")
					exit = j.tryTaskWorkerIdleExit()
				case <-ctx.Done():
					trace.Logf(ctx, traceRegion, "received context done signal")
					exit = true
				}
				return
			},
		)
		if ok {
			task = t
			exit = false // Got a task; ignore any concurrent exit signal — process it first
		}
		if exit {
			if task != nil {
				panic("exiting with non-nil task")
			}
			break
		}
	}
}

type taskPostWork struct {
	poolWork
	job  *Pool
	task *taskWork
}

func (w *taskPostWork) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "taskPostWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	if trace.IsEnabled() {
		trace.Logf(ctx, traceRegion, "%v", w)
	}

	registerDemand := func() {
		if w.task.demandRegistered.CompareAndSwap(false, true) {
			w.job.taskWorkerDemand.Increment()
			if trace.IsEnabled() {
				trace.Logf(ctx, traceRegion, "DEMAND_INC task=%p counter=%p", w.task, &w.job.taskWorkerDemand)
			}
		}
		w.job.trySpawnTaskWorker()
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
			w.job.taskQueue.PushBackFunc(meta.Sender(), w.task, bufferedFn, func(outboxCh chan<- *taskWork) bool {
				posted = false

				// Slow path, really going to block now
				ex.Blocking()

				var sent bool
				sent, err = rdvq.BasicPushSelect[*taskWork](ctx, outboxCh, w.task)
				if sent {
					posted = true
				}
				return sent
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

	// Free the nested task work if we still own it
	if w.task != nil {
		trace.Logf(context.Background(), traceRegion, "w.task.Free()")
		w.task.Free(w.job)
	}

	w.Close(w.job)
	taskPostWorkPool.Put(w)
}

var taskPostWorkPool = omnipool.For[taskPostWork]()

type poolWork struct {
	workq.WorkItem
}

func (w *poolWork) Init(group workq.GroupID, job *Pool) {
	w.WorkItem.Init(group)
	trace.Logf(context.Background(), "poolWork.Init", "%v", &w.WorkItem)
	job.state.IncrementWork()
}

//nolint:contextcheck // background context used only for tracing
func (w *poolWork) Close(job *Pool) {
	if w.ID() == 0 {
		// This check and panic is best-effort only as it may also be a race if
		// Close() is called from multiple goroutines -- which it should not be.
		panic("already closed")
	}
	trace.Logf(context.Background(), "poolWork.Close", "%v", &w.WorkItem)
	job.state.DecrementWork()
}

func (j *Pool) newScatterWork(group workq.GroupID, deadline time.Time, task boundTask) workq.Work {
	taskWork := j.newTaskWork(group, task, nil)
	return j.newTaskPostWork(group, deadline, taskWork)
}

func (j *Pool) newTaskPostWork(group workq.GroupID, deadline time.Time, task *taskWork) workq.Work {
	w := taskPostWorkPool.Get()
	w.Init(group, j)
	w.job = j
	w.task = task
	return w
}

// panicIfDone panics if the job is in the done state
func (j *Pool) panicIfDone() {
	j.state.PanicIfDone()
}

// Close changes the job's state from open to closed, which allows it to eventually
// progress to the done state once all tasks complete. When a job is closed,
// [Pool.GatherAll] will return after processing all existing tasks and any tasks
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
func (j *Pool) Close() {
	traceRegion := "Pool.Close"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	j.state.Close()
}

// CloseAndGatherAll closes the job via [Pool.Close] and then waits for and
// gathers the results of all in-flight tasks via [Pool.GatherAll].
func (j *Pool) CloseAndGatherAll(ctx context.Context) error {
	traceRegion := "Pool.CloseAndGatherAll"
	defer trace.StartRegion(ctx, traceRegion).End()

	j.Close()
	return j.GatherAll(ctx)
}

// poolConfigWrapper wraps a Pool to implement the poolConfig interface for options
type poolConfigWrapper struct {
	job *Pool
}

func (w poolConfigWrapper) Update(changes opts.PoolConfigChanges) {
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
func (j *Pool) tryTaskWorkerIdleExit() bool {
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
func (j *Pool) SetOptions(options ...psgopt.PoolOption) {
	traceRegion := "Pool.SetOptions"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	opts.ApplyToPool(poolConfigWrapper{job: j}, options...)
}
