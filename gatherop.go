// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/workq"
	"github.com/petenewcomb/psg-go/psgfn"
)

// Gatherer is a terminal sink: values arrive via Submit and are delivered
// to the user-supplied gather function during Pool.Gather / Pool.GatherAll.
// Task dispatch lives separately on [TaskRunner] — a Gatherer never runs
// tasks of its own.
//
// Thread-safety and copying: a Gatherer value is designed to be copied.
// All copies share the same gather function binding, so they can be passed
// by value to goroutines or stored in structures and used concurrently.
type Gatherer[T any] struct {
	gatherFn psgfn.Gather[T]
	workPool *omnipool.Pool[gatherWork[T]]
}

func NewGatherer[T any](
	gatherFn psgfn.Gather[T],
) Gatherer[T] {
	if gatherFn == nil {
		panic("gather function must be non-nil")
	}
	return Gatherer[T]{
		gatherFn: gatherFn,
		workPool: omnipool.For[gatherWork[T]](),
	}
}

// Submit posts a value to the Gatherer's queue for later dispatch via
// Pool.Gather / Pool.GatherAll.
func (g Gatherer[T]) Submit(
	ctx context.Context,
	target *Pool,
	value T,
	err error,
) error {
	traceRegion := "Gatherer.Submit"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := target.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()

	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return g.submit(ctx, meta, target, group, value, err)
}

// TrySubmit attempts to post values to be gathered by the gather queue.
// Like Submit, but returns instead of blocking if queuing would be required.
func (g Gatherer[T]) TrySubmit(
	ctx context.Context,
	deadline time.Time,
	target *Pool,
	value T,
	err error,
) (bool, error) {
	traceRegion := "Gatherer.TrySubmit"
	defer trace.StartRegion(ctx, traceRegion).End()

	ctx, meta := target.ctxMeta(ctx)
	meta.Lock()
	defer meta.Unlock()

	group := meta.Group()
	if group == workq.InvalidGroupID {
		group = workq.NewGroupID()
	}

	return g.trySubmit(ctx, meta, target, group, value, err, deadline)
}

// boundGatherWork interface allows type erasure for gatherWork instances
type boundGatherWork interface {
	workq.Work
	Waiting(*workq.Governor)
}

type gatherWork[T any] struct {
	poolWork
	workq.DownstreamWork
	job      *Pool
	pool     *omnipool.Pool[gatherWork[T]]
	gatherFn psgfn.Gather[T]
	value    T
	err      error
}

// newGatherWork creates a new gather work item with the provided values
func (g Gatherer[T]) newGatherWork(group workq.GroupID, job *Pool, value T, err error) *gatherWork[T] {
	w := g.workPool.Get()
	w.Init(g.workPool, group, job, g.gatherFn, value, err)
	return w
}

func (w *gatherWork[T]) Init(
	pool *omnipool.Pool[gatherWork[T]],
	group workq.GroupID,
	job *Pool,
	gatherFn psgfn.Gather[T],
	value T,
	err error,
) {
	w.poolWork.Init(group, job)
	w.job = job
	w.pool = pool
	w.gatherFn = gatherFn
	w.value = value
	w.err = err
}

func (w *gatherWork[T]) Execute(ctx context.Context, ex workq.Execution) error {
	traceRegion := "gatherWork.Execute"
	defer trace.StartRegion(ctx, traceRegion).End()
	trace.Logf(ctx, traceRegion, "%v", w)

	ex.Starting()
	ctx, meta := w.job.ctxMeta(ctx)

	meta.PushGroup(w.Group())
	defer meta.PopGroup()

	return w.gatherFn(ctx, w.value, w.err)
}

//nolint:contextcheck // background context used only for tracing
func (w *gatherWork[T]) Free() {
	traceRegion := "gatherWork.Free"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "%v", w)

	w.DownstreamWork.Close()
	w.poolWork.Close(w.job)
	w.pool.Put(w)
}

// submit creates gather work and posts it to the gather queue
func (g Gatherer[T]) submit(
	ctx context.Context,
	meta *ctxMeta,
	job *Pool,
	group workq.GroupID,
	value T,
	err error,
) error {
	gatherWork := g.newGatherWork(group, job, value, err)
	postWork := job.newGatherPostWork(group, gatherWork)
	return meta.ExecuteNowOrQueue(ctx, postWork)
}

// submit creates gather work and posts it to the gather queue
func (g Gatherer[T]) trySubmit(
	ctx context.Context,
	meta *ctxMeta,
	job *Pool,
	group workq.GroupID,
	value T,
	err error,
	deadline time.Time,
) (bool, error) {
	gatherWork := g.newGatherWork(group, job, value, err)
	postWork := job.newGatherPostWork(group, gatherWork)
	ok, err := meta.TryExecuteNow(ctx, deadline, postWork)
	if !ok {
		postWork.Free()
	}
	return ok, err
}
