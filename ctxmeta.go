// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"maps"
	"sync"

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

func (cm *ctxMeta) WouldBlock() bool {
	switch cm.ctxType {
	case topLevelContext, taskContext:
		return true
	default:
		return false
	}
}

type executionEnvironment interface {
	CurrentGroup() workq.GroupID
	LockOutbox(key outboxKey[workq.Work]) *workq.Outbox
	UnlockOutbox()
	LockAndSetQueueFunc(group workq.GroupID, queueFn workq.QueueWorkFunc, blockWaiters *workq.Waiters) (
		*workq.Receiver, *workq.Waiter, *workq.Waiter)
	UnlockAndResetQueueFunc()
	MayQueue() workq.QueueWorkFunc
}

type topLevelExEnv struct {
	mu             sync.Mutex
	outboxMap      outboxMap
	groupStack     []workq.GroupID
	queueFnStack   []workq.QueueWorkFunc
	workReceiver   workq.Receiver
	workWaiter     workq.Waiter
	blockWaiterMap map[*workq.Waiters]*workq.Waiter
}

func (ee *topLevelExEnv) CurrentGroup() workq.GroupID {
	if len(ee.groupStack) == 0 {
		return workq.InvalidGroupID
	}
	return ee.groupStack[len(ee.groupStack)-1]
}

func (ee *topLevelExEnv) LockOutbox(key outboxKey[workq.Work]) *workq.Outbox {
	traceRegion := "topLevelExEnv.LockOutbox"
	ee.mu.Lock()
	outbox := OutboxFor[workq.Work](&ee.outboxMap, key)
	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "topLevelExEnv=%p, outbox=%p", ee, outbox)
	}
	return outbox
}

func (ee *topLevelExEnv) UnlockOutbox() {
	ee.mu.Unlock()
}

func (ee *topLevelExEnv) LockAndSetQueueFunc(
	group workq.GroupID,
	queueFn workq.QueueWorkFunc,
	blockWaiters *workq.Waiters,
) (
	workReceiver *workq.Receiver,
	workWaiter *workq.Waiter,
	blockWaiter *workq.Waiter,
) {
	traceRegion := "topLevelExEnv.LockAndSetQueueFunc"

	if len(ee.queueFnStack) == 0 {
		ee.mu.Lock()
	}

	ee.queueFnStack = append(ee.queueFnStack, queueFn)
	trace.Logf(context.Background(), traceRegion, "topLevelExEnv=%p queueFnDepth=%d", ee, len(ee.queueFnStack))

	if blockWaiters != nil {
		blockWaiter = ee.blockWaiterMap[blockWaiters]
		if blockWaiter == nil {
			if ee.blockWaiterMap == nil {
				ee.blockWaiterMap = make(map[*workq.Waiters]*workq.Waiter)
			}
			blockWaiter = &workq.Waiter{}
			ee.blockWaiterMap[blockWaiters] = blockWaiter
		}
	}

	return &ee.workReceiver, &ee.workWaiter, blockWaiter
}

func (ee *topLevelExEnv) UnlockAndResetQueueFunc() {
	traceRegion := "topLevelExEnv.UnlockAndResetQueueFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	ee.queueFnStack = ee.queueFnStack[:len(ee.queueFnStack)-1]

	trace.Logf(context.Background(), traceRegion, "topLevelExEnv=%p queueFnDepth=%v", ee, len(ee.queueFnStack))

	if len(ee.queueFnStack) == 0 {
		ee.mu.Unlock()
	}
}

func (ee *topLevelExEnv) MayQueue() workq.QueueWorkFunc {
	// Lock must already be held by LockAndSetQueueFunc
	if len(ee.queueFnStack) == 0 {
		return nil
	}
	return ee.queueFnStack[len(ee.queueFnStack)-1]
}

// Simple execution environment for task workers that only needs outbox access
type taskWorkerExEnv struct {
	outboxMap *outboxMap
}

func (ee *taskWorkerExEnv) CurrentGroup() workq.GroupID {
	panic("CurrentGroup not supported in task worker context")
}

func (ee *taskWorkerExEnv) LockOutbox(key outboxKey[workq.Work]) *workq.Outbox {
	return OutboxFor[workq.Work](ee.outboxMap, key)
}

func (ee *taskWorkerExEnv) UnlockOutbox() {}

func (ee *taskWorkerExEnv) LockAndSetQueueFunc(
	group workq.GroupID,
	queueFn workq.QueueWorkFunc,
	blockWaiters *workq.Waiters,
) (
	workReceiver *workq.Receiver,
	workWaiter *workq.Waiter,
	blockWaiter *workq.Waiter,
) {
	panic("LockAndSetQueueFunc not supported in task worker context")
}

func (ee *taskWorkerExEnv) UnlockAndResetQueueFunc() {
	panic("UnlockAndResetQueueFunc not supported in task worker context")
}

func (ee *taskWorkerExEnv) MayQueue() workq.QueueWorkFunc {
	return nil // Task workers use blocking calls instead
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
				exEnv := &topLevelExEnv{}
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
