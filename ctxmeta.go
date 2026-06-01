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
	"github.com/petenewcomb/psg-go/internal/rdvq"
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
	job        *Pool
	wave       *Wave // set by NewWave; nil for ctxs not derived through a Wave
	parentJobs map[*Pool]struct{}
	ctxType    contextType
	executionEnvironment
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

	if !deadline.IsZero() && !time.Now().Before(deadline) {
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

	Receiver() *rdvq.Receiver
	Sender() *rdvq.Sender
	Waiter() *rdvq.Waiter
}

type baseExEnv struct {
	sender rdvq.Sender
	waiter rdvq.Waiter
}

func (ee *baseExEnv) Sender() *rdvq.Sender {
	return &ee.sender
}

//nolint:contextcheck // background context used only for tracing
func (ee *baseExEnv) Waiter() *rdvq.Waiter {
	return &ee.waiter
}

// Release returns the exEnv's pooled rdvq resources (sender, waiter) to
// their shared pools. Should be called via defer when the goroutine that
// owns this exEnv is exiting.
func (ee *baseExEnv) Release() {
	ee.sender.Release()
	ee.waiter.Release()
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

func (ee *taskExEnv) Receiver() *rdvq.Receiver {
	panic("Receiver not supported in task context")
}

type integrationExEnv struct {
	baseExEnv
	groupStack   []workq.GroupID
	queueFnStack []workq.QueueWorkFunc
	receiver     rdvq.Receiver
}

// Release returns the integrationExEnv's pooled rdvq resources (sender,
// waiter, receiver) to their shared pools. Should be called via defer when
// the goroutine that owns this exEnv is exiting.
func (ee *integrationExEnv) Release() {
	ee.receiver.Release()
	ee.baseExEnv.Release()
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

func (ee *integrationExEnv) Receiver() *rdvq.Receiver {
	return &ee.receiver
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
