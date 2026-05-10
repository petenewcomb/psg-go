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
	gatherContext                      // gather
	combineContext                     // combine
)

type ctxMeta struct {
	job        *Job
	parentJobs map[*Job]struct{}
	ctxType    contextType
	executionEnvironment
}

func (cm *ctxMeta) String() string {
	return fmt.Sprintf("{%v Job=%p exEnv=%p}", cm.ctxType, cm.job, cm.executionEnvironment)
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
		runtime.Gosched()

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
			runtime.Gosched()

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
}

func (ee *taskExEnv) Lock() {
	panic("Lock not supported in task context")
}

func (ee *taskExEnv) Unlock() {
	panic("Unlock not supported in task context")
}

func (ee *taskExEnv) Group() workq.GroupID {
	panic("Group not supported in task context")
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

func (j *Job) ctxMeta(ctx context.Context) (context.Context, *ctxMeta) {
	traceRegion := "Job.ctxMeta"

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

func (j *Job) ensureCtxMeta(
	ctx context.Context,
	updateFn func(context.Context, *ctxMeta) context.Context,
) (context.Context, *ctxMeta) {
	traceRegion := "Job.ensureCtxMeta"

	ctx, meta := j.ctxMetaMap.WithValue(ctx,
		func(sourceMeta *ctxMeta, _ bool) (context.Context, *ctxMeta) {
			ctxType := topLevelContext
			var parentJobs map[*Job]struct{}
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
					parentJobs = make(map[*Job]struct{}, len(sourceMeta.parentJobs)+1)
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

type gatherCtxMetaValueKey struct{}

// checkCtxType should panic if the type is not allowed
func (j *Job) topLevelCtxMeta(ctx context.Context, checkCtxType func(ctxType contextType)) (context.Context, *ctxMeta) {
	traceRegion := "Job.topLevelCtxMeta"

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

func (j *Job) gatherCtxMeta(ctx context.Context) (context.Context, *ctxMeta) {
	traceRegion := "Job.gatherCtxMeta"

	ctx, meta := j.topLevelCtxMeta(ctx, func(ctxType contextType) {
		if ctxType != topLevelContext && ctxType != gatherContext {
			panic(fmt.Sprintf("Gather called from %v context but allowed only by top-level or gather context", ctxType))
		}
	})
	if meta.ctxType == gatherContext {
		return ctx, meta
	}

	ctx, _ = j.gatherCtxMetaMap.WithValue(ctx,
		func(*Job, bool) (context.Context, *Job) {
			trace.Logf(ctx, traceRegion, "creating new gather context")
			return ctx, j
		},
	)

	ctx, meta = j.ensureCtxMeta(ctx,
		func(ctx context.Context, meta *ctxMeta) context.Context {
			if meta.ctxType != topLevelContext {
				panic(fmt.Sprintf("context type %v is not valid for gather, expected top-level context", meta.ctxType))
			}
			if meta.executionEnvironment == nil {
				panic("top-level context missing executionEnvironment; required for gather")
			}
			meta.ctxType = gatherContext
			trace.Logf(ctx, traceRegion, "registering new gather context, ctxMeta=%v", meta)
			return ctx
		},
	)

	if meta.ctxType != gatherContext {
		panic(fmt.Sprintf("context type %v is not valid for gather, expected gather context", meta.ctxType))
	}
	return ctx, meta
}
