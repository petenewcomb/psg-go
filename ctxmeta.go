// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"maps"
	"runtime"
	"sync"
	"time"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/workq"
)

type contextType int

//go:generate go run golang.org/x/tools/cmd/stringer@v0.35.0 -type=contextType -linecomment
const (
	topLevelContext contextType = iota // top-level
	taskContext                        // task
	skimContext                        // skim
	funnelContext                      // funnel
)

type ctxMeta struct {
	job  *Pool
	wave *Wave // set by NewWave; nil for ctxs not derived through a Wave
	// parent links to the ctxMeta this one was derived from along the
	// context value chain — the synchronous, same-goroutine derivations
	// (top-level→skim, body→NewWave→subwave contexts) that
	// currentHeldRequest walks to find a held limiter permit. Worker
	// contexts are fresh permit-roots (parent == nil), severed
	// explicitly at creation: the pool's base ctx may carry a foreign
	// pool's meta (a subjob created inside a body), and inheriting the
	// link there would let a worker find its dispatcher's permit across
	// the goroutine boundary. See docs/limiter-suspend-resume.md,
	// "Serialization and scoping".
	parent *ctxMeta
	// heldRequest is the limiter request handle stamped at body entry
	// (prevWave-style save/restore) and found via currentHeldRequest at
	// framework parking points. A stamped handle is only ever HELD or
	// SUSPENDED: POSTPONED is pre-body, DONE is post-unstamp.
	heldRequest request
	parentJobs  map[*Pool]struct{}
	ctxType     contextType
	executionEnvironment
}

// vetNotNestedInSkim panics if a blocking gather (Skim/SkimAll, hence
// CloseAndSkimAll) is being driven from inside a skim handler — i.e. an
// enclosing context on this goroutine is a skim context. Driving a
// subwave from a skim handler monopolizes the wave's sole serial skim
// driver while the handler is parked in the gather, which deadlocks
// under shared limiters / nested subwaves (see REVIEW_FINDINGS Finding
// 10). The fix is to keep skimming serial and drive subwork elsewhere:
// populate a [Funnel] from the handler (the map-reduce primitive), or
// launch a task that drives the subwave. Tasks and funnels are
// demand-driven, so they never monopolize a sole driver.
//
// Walks parent (excluding the gather's own skim context). Funnel/task/
// top-level enclosing contexts are fine — only an enclosing *skim*
// handler is disallowed.
func (cm *ctxMeta) vetNotNestedInSkim() {
	for m := cm.parent; m != nil; m = m.parent {
		if m.ctxType == skimContext {
			panic("psg: cannot drive a subwave (Skim/SkimAll/CloseAndSkimAll) from a skim handler; " +
				"populate a Funnel from the handler, or launch a task to drive the subwave")
		}
	}
}

// currentHeldRequest returns the limiter request handle held by the body
// this context is synchronously nested under, walking parent links and
// stopping at the first stamped handle. Structurally there is at most one
// per chain (every stamp site is a chain root — see the heldRequest field
// doc); under help-execution nesting the first handle found is the
// enclosing episode's, already SUSPENDED, so the suspend bracket's
// `r != nil && r.suspend()` contract needs no state checks here.
func (cm *ctxMeta) currentHeldRequest() request {
	for m := cm; m != nil; m = m.parent {
		if m.heldRequest != nil {
			return m.heldRequest
		}
	}
	return nil
}

func (cm *ctxMeta) String() string {
	return fmt.Sprintf("{%v Pool=%p exEnv=%p}", cm.ctxType, cm.job, cm.executionEnvironment)
}

func (cm *ctxMeta) IsTopLevel() bool {
	return cm.ctxType == topLevelContext
}

func (cm *ctxMeta) ShouldBlock() bool {
	switch cm.ctxType {
	case topLevelContext, taskContext:
		return true
	default:
		return false
	}
}

func (cm *ctxMeta) Lock() {
	if cm.IsTopLevel() {
		cm.executionEnvironment.Lock()
	}
}

func (cm *ctxMeta) Unlock() {
	if cm.IsTopLevel() {
		cm.executionEnvironment.Unlock()
	}
}

var waitMu sync.Mutex

// Wait for the scheduler to be mostly idle
func wait() {
	for {
		waitMu.Lock()
		start := time.Now()
		runtime.Gosched()
		elapsed := time.Since(start)
		waitMu.Unlock()
		if elapsed < 100*time.Microsecond {
			break
		}
	}
}

func (cm *ctxMeta) TryExecuteNow(
	ctx context.Context,
	deadline time.Time,
	work workq.Work,
) (bool, error) {
	traceRegion := "ctxMeta.TryExecuteNow"
	defer trace.StartRegion(ctx, traceRegion).End()

	executor := executorPool.Get()
	defer executorPool.Put(executor)
	ex := executor.BaseEx()

	if cm.IsTopLevel() {
		// Make sure existing work has a chance to run before we add more.
		wait()

		// Apply backpressure at top level by processing some outstanding work first
		err := cm.job.yield(ctx, deadline)
		if err != nil {
			return false, err
		}
	}

	// Deadline interpretation (as currently implemented):
	//   - past time → fail-fast (no attempt)
	//   - other     → attempt once
	// The "attempt once" semantic is enforced by ex.AddToListeners
	// being nil — the blocking layer treats nil AddToListeners as
	// "don't block." Forever and future deadlines do not currently
	// install genuine bounded-wait blocking at this level; that is
	// deferred Thread C work (see WORKING_NOTES). Naively enabling
	// AddToListeners here causes hangs because the timer/listener
	// plumbing through taskPostWork isn't fully wired (known open
	// issue: "Deadline propagation in taskPostWork").
	if !deadline.IsZero() && !isForever(deadline) && !time.Now().Before(deadline) {
		return false, nil
	}

	err := work.Execute(ctx, ex)
	if !ex.Started() {
		return false, err
	}
	work.Free()
	return true, err
}

func (cm *ctxMeta) ExecuteNowOrQueue(
	ctx context.Context,
	work workq.Work,
) error {
	traceRegion := "ctxMeta.ExecuteNowOrQueue"
	defer trace.StartRegion(ctx, traceRegion).End()

	executor := executorPool.Get()
	defer executorPool.Put(executor)
	ex := executor.BaseEx()

	if cm.ShouldBlock() {
		if cm.IsTopLevel() {
			// Suspend-class episode: the WHOLE blocking dispatch — the
			// backpressure yield, any governor/limiter block-and-help
			// waits, and the inner post — is one episode for an
			// enclosing body's held limiter permit (a subjob dispatch
			// runs on the body's goroutine). The reclaim must come
			// after the inner post: on self-acquisition (the dispatched
			// op shares the holder's limiter), reclaiming any earlier
			// waits on a task that hasn't been queued yet. Interior
			// brackets (Pool.block) no-op via re-entrancy.
			if r := suspendForEpisode(cm); r != nil {
				defer reclaimRequest(ctx, cm.job.blockFn, r)
			}

			// Make sure existing work has a chance to run before we add more.
			wait()

			// Apply backpressure at top level by processing some outstanding work first
			err := cm.job.yield(ctx, time.Time{})
			if err != nil {
				work.Free()
				return err
			}
		}

		// Signal the work that it should block by making AddToListeners non-nil
		ex.AddToListeners = func(*workq.Listeners) {
			panic("unexpected call to ctxMeta.TryExecuteOrQueue's ex.AddToListeners")
		}
	}

	return cm.executionEnvironment.ExecuteNowOrQueue(ctx, ex, work)
}

var executorPool = omnipool.For[workq.Executor]()

type executionEnvironment interface {
	Lock()
	Unlock()

	Group() workq.GroupID
	PushGroup(workq.GroupID)
	PopGroup()

	QueueFunc() workq.QueueWorkFunc
	PushQueueFunc(queueFn workq.QueueWorkFunc)
	PopQueueFunc()
	ExecuteNowOrQueue(context.Context, workq.Execution, workq.Work) error
}

type baseExEnv struct {
}

// Release returns the exEnv's pooled resources to their shared pools. Should be
// called via defer when the goroutine that owns this exEnv is exiting.
func (ee *baseExEnv) Release() {
}

type taskExEnv struct {
	baseExEnv
	// group is the GroupID of the task currently executing on the worker
	// that owns this exEnv. Set by the worker loop before invoking the
	// task body and cleared after; user-facing Submit calls from inside
	// the task pick it up via Group() so submissions ride on the task's
	// group rather than always allocating a fresh one.
	group workq.GroupID
}

// Lock/Unlock are no-ops in task context: the exEnv is per-worker and
// the worker runs one task body at a time, so cross-goroutine
// serialization isn't needed (matching integrationExEnv).
func (ee *taskExEnv) Lock()   {}
func (ee *taskExEnv) Unlock() {}

func (ee *taskExEnv) Group() workq.GroupID {
	return ee.group
}

func (ee *taskExEnv) PushGroup(workq.GroupID) {
	panic("PushGroup not supported in task context")
}

func (ee *taskExEnv) PopGroup() {
	panic("PopGroup not supported in task context")
}

func (ee *taskExEnv) QueueFunc() workq.QueueWorkFunc {
	return nil
}

func (ee *taskExEnv) PushQueueFunc(workq.QueueWorkFunc) {
	panic("PushQueueFunc not supported in task context")
}

func (ee *taskExEnv) PopQueueFunc() {
	panic("PopQueueFunc not supported in task context")
}

func (ee *taskExEnv) ExecuteNowOrQueue(ctx context.Context, ex workq.Execution, work workq.Work) error {
	err := work.Execute(ctx, ex)
	if err != nil {
		return err
	}
	if !ex.Started() {
		panic("work not started")
	}
	work.Free()
	return nil
}

type integrationExEnv struct {
	baseExEnv
	groupStack   []workq.GroupID
	queueFnStack []workq.QueueWorkFunc
}

func (ee *integrationExEnv) Group() workq.GroupID {
	if len(ee.groupStack) == 0 {
		return workq.InvalidGroupID
	}
	return ee.groupStack[len(ee.groupStack)-1]
}

func (ee *integrationExEnv) PushGroup(group workq.GroupID) {
	ee.groupStack = append(ee.groupStack, group)
}

func (ee *integrationExEnv) PopGroup() {
	if len(ee.groupStack) == 0 {
		panic("group stack underflow")
	}
	ee.groupStack = ee.groupStack[:len(ee.groupStack)-1]
}

func (ee *integrationExEnv) QueueFunc() workq.QueueWorkFunc {
	if len(ee.queueFnStack) == 0 {
		return nil
	}
	return ee.queueFnStack[len(ee.queueFnStack)-1]
}

func (ee *integrationExEnv) PushQueueFunc(queueFn workq.QueueWorkFunc) {
	if queueFn == nil {
		panic("queueFn is nil")
	}
	ee.queueFnStack = append(ee.queueFnStack, queueFn)
}

func (ee *integrationExEnv) PopQueueFunc() {
	if len(ee.queueFnStack) == 0 {
		panic("queue function stack underflow")
	}
	ee.queueFnStack = ee.queueFnStack[:len(ee.queueFnStack)-1]
}

type topLevelExEnv struct {
	integrationExEnv
	mu        sync.Mutex
	workQueue *workq.Accepted
}

func (ee *topLevelExEnv) Lock() {
	ee.mu.Lock()
}

func (ee *topLevelExEnv) Unlock() {
	ee.mu.Unlock()
}

func (ee *topLevelExEnv) ExecuteNowOrQueue(ctx context.Context, ex workq.Execution, work workq.Work) error {
	return ee.workQueue.ExecuteNowOrQueue(ctx, ex, work)
}

type ctxMetaValueKey struct{}

func (j *Pool) ctxMeta(ctx context.Context) (context.Context, *ctxMeta) {
	traceRegion := "Pool.ctxMeta"

	ctx, meta := j.ctxMetaMap.WithValue(ctx,
		func(sourceMeta *ctxMeta, _ bool) (context.Context, *ctxMeta) {
			if sourceMeta == nil {
				panic("Context not associated with a job")
			}
			if sourceMeta.job != j {
				if _, isParentJob := sourceMeta.parentJobs[j]; isParentJob {
					panic("Context belongs to a child job")
				} else {
					panic("Context belongs to a different job")
				}
			}
			return ctx, sourceMeta
		},
	)
	if meta != nil && meta.job != j {
		panic(fmt.Sprintf("Context metadata does not match job: expected %p, got %p", j, meta.job))
	}

	trace.Logf(ctx, traceRegion, "ctxMeta=%v", meta)

	return ctx, meta
}

func (j *Pool) ensureCtxMeta(
	ctx context.Context,
	updateFn func(context.Context, *ctxMeta) context.Context,
) (context.Context, *ctxMeta) {
	traceRegion := "Pool.ensureCtxMeta"

	ctx, meta := j.ctxMetaMap.WithValue(ctx,
		func(sourceMeta *ctxMeta, _ bool) (context.Context, *ctxMeta) {
			ctxType := topLevelContext
			var parentJobs map[*Pool]struct{}
			var exEnv executionEnvironment
			if sourceMeta != nil {
				if sourceMeta.job == j {
					parentJobs = sourceMeta.parentJobs
					ctxType = sourceMeta.ctxType
					exEnv = sourceMeta.executionEnvironment
				} else {
					if _, isParentJob := sourceMeta.parentJobs[j]; isParentJob {
						panic("Context belongs to a child job")
					}
					parentJobs = make(map[*Pool]struct{}, len(sourceMeta.parentJobs)+1)
					maps.Copy(parentJobs, sourceMeta.parentJobs)
					parentJobs[sourceMeta.job] = struct{}{}
				}
			}

			if sourceMeta == nil || sourceMeta.job != j {
				newCtx, cancel := context.WithCancel(ctx)
				stop := context.AfterFunc(j.ctx, cancel)
				context.AfterFunc(newCtx, func() {
					stop()
				})
				ctx = newCtx
			}

			meta := &ctxMeta{
				job:                  j,
				parent:               sourceMeta,
				parentJobs:           parentJobs,
				ctxType:              ctxType,
				executionEnvironment: exEnv,
			}
			// Preserve the wave across same-job ctx transitions
			// (e.g. top-level → skim) so nil-wave op dispatch from
			// inside a Skim/Accumulate body can still resolve.
			// Across-job transitions intentionally drop the wave —
			// the source wave is bound to a different Pool.
			if sourceMeta != nil && sourceMeta.job == j {
				meta.wave = sourceMeta.wave
			}

			if updateFn != nil {
				ctx = updateFn(ctx, meta)
			}

			return ctx, meta
		},
	)
	if meta != nil && meta.job != j {
		panic(fmt.Sprintf("context meta does not match job: expected %p, got %p", j, meta.job))
	}

	trace.Logf(ctx, traceRegion, "ctxMeta=%v", meta)

	return ctx, meta
}

type skimCtxMetaValueKey struct{}

// checkCtxType should panic if the type is not allowed
func (j *Pool) topLevelCtxMeta(
	ctx context.Context, checkCtxType func(ctxType contextType),
) (context.Context, *ctxMeta) {
	traceRegion := "Pool.topLevelCtxMeta"

	ctx, meta := j.ensureCtxMeta(ctx,
		func(ctx context.Context, meta *ctxMeta) context.Context {
			checkCtxType(meta.ctxType) // Avoid caching if invalid
			if meta.executionEnvironment == nil {
				exEnv := &topLevelExEnv{
					workQueue: &j.workQueue,
				}
				meta.executionEnvironment = exEnv
				trace.Logf(ctx, traceRegion, "created new topLevelExEnv=%p, ctxMeta=%v", exEnv, meta)
			}
			return ctx
		},
	)
	checkCtxType(meta.ctxType)
	return ctx, meta
}

func (j *Pool) skimCtxMeta(ctx context.Context) (context.Context, *ctxMeta) {
	traceRegion := "Pool.skimCtxMeta"

	ctx, meta := j.topLevelCtxMeta(ctx, func(ctxType contextType) {
		if ctxType != topLevelContext && ctxType != skimContext {
			panic(fmt.Sprintf("Skim called from %v context but allowed only by top-level or skim context", ctxType))
		}
	})
	if meta.ctxType == skimContext {
		return ctx, meta
	}

	ctx, _ = j.skimCtxMetaMap.WithValue(ctx,
		func(*Pool, bool) (context.Context, *Pool) {
			trace.Logf(ctx, traceRegion, "creating new skim context")
			return ctx, j
		},
	)

	ctx, meta = j.ensureCtxMeta(ctx,
		func(ctx context.Context, meta *ctxMeta) context.Context {
			if meta.ctxType != topLevelContext {
				panic(fmt.Sprintf("context type %v is not valid for skim, expected top-level context", meta.ctxType))
			}
			if meta.executionEnvironment == nil {
				panic("top-level context missing executionEnvironment; required for skim")
			}
			meta.ctxType = skimContext
			trace.Logf(ctx, traceRegion, "registering new skim context, ctxMeta=%v", meta)
			return ctx
		},
	)

	if meta.ctxType != skimContext {
		panic(fmt.Sprintf("context type %v is not valid for skim, expected skim context", meta.ctxType))
	}
	return ctx, meta
}
