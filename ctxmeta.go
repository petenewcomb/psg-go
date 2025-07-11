// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/workq"
)

type contextType int

const (
	topLevelContext contextType = iota
	taskContext
	gatherContext
	combineContext
)

func (ct contextType) String() string {
	switch ct {
	case topLevelContext:
		return "top-level"
	case taskContext:
		return "task"
	case gatherContext:
		return "gather"
	case combineContext:
		return "combine"
	default:
		return fmt.Sprintf("unknown(%d)", ct)
	}
}

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

func (cm *ctxMeta) ShouldBlock() workq.BlockFunc {
	if cm.IsTopLevel() {
		return cm.job.block
	}
	return nil
}

func (j *Job) withOutboxFor(ctx context.Context, key outboxKey[workq.WorkFunc], fn func(*workq.Outbox)) {
	_, meta := j.ctxMeta(ctx)
	meta.WithOutbox(key, fn)
}

func (j *Job) withGatherOutbox(ctx context.Context, fn func(*workq.Outbox)) {
	j.withOutboxFor(ctx, j.gatherOutboxKey(), fn)
}

func (j *Job) shouldBlock(ctx context.Context) workq.BlockFunc {
	_, meta := j.ctxMeta(ctx)
	return meta.ShouldBlock()
}

type executionEnvironment interface {
	WithOutbox(key outboxKey[workq.WorkFunc], fn func(*workq.Outbox))
	WithQueueFunc(queueFn workq.QueueWorkFunc, fn func())
	QueueWork(workFn workq.WorkFunc)
}

type topLevelExEnv struct {
	mu         sync.Mutex
	outboxMap  outboxMap
	queueFn    workq.QueueWorkFunc
	queueFnSet atomic.Bool // Can be read without holding mu
}

func (ee *topLevelExEnv) WithOutbox(key outboxKey[workq.WorkFunc], fn func(*workq.Outbox)) {
	traceRegion := "topLevelExEnv.WithOutbox"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "topLevelExEnv=%p", ee)

	ee.mu.Lock()
	defer ee.mu.Unlock()
	outbox := OutboxFor[workq.WorkFunc](&ee.outboxMap, key)
	trace.Logf(context.Background(), traceRegion, "outbox=%p", outbox)
	fn(outbox)
}

func (ee *topLevelExEnv) WithQueueFunc(queueFn workq.QueueWorkFunc, fn func()) {
	traceRegion := "topLevelExEnv.WithQueueFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "topLevelExEnv=%p", ee)

	if ee.queueFnSet.Load() {
		// Reentrant. Would be nice to assert that the queueFn is the same, but
		// function pointers are not comparable in Go.
		trace.WithRegion(context.Background(), traceRegion+".queueFnWasAlreadySet", fn)
		return
	}

	ee.mu.Lock()
	ee.queueFn = queueFn
	ee.queueFnSet.Store(true)
	defer func() {
		ee.queueFnSet.Store(false)
		ee.queueFn = nil
		ee.mu.Unlock()
	}()
	trace.WithRegion(context.Background(), traceRegion+".queueFnSet", fn)
}

func (ee *topLevelExEnv) QueueWork(workFn workq.WorkFunc) {
	traceRegion := "topLevelExEnv.QueueWork"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "topLevelExEnv=%p", ee)

	// Lock must already be held by WithQueueFunc
	ee.queueFn(workFn)
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
				stop := context.AfterFunc(j.ctx, func() {
					cancel()
				})
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
