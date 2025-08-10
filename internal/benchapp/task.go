// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package benchapp

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/psgwf"
)

type task[T, C any] struct {
	pool         *omnipool.Pool[task[T, C]]
	creationTime time.Time
	taskFn       psgwf.GenericTaskFunc[T, C]
	wrappedFn    psgwf.GenericTaskFunc[TaskResult[T], C]
}

type TaskResult[T any] struct {
	StartTime    time.Time
	StartLatency time.Duration
	Duration     time.Duration
	Value        T
}

func NewTask[T, C any](
	taskFn func(context.Context, *psgwf.GenericWorkflow[C]) (T, error),
) psgwf.GenericTaskFunc[TaskResult[T], C] {
	pool := omnipool.For[task[T, C]]()
	task := pool.Get()
	task.pool = pool
	task.creationTime = time.Now()
	task.taskFn = taskFn
	return task.wrappedFn
}

func (t *task[T, C]) Init() {
	t.wrappedFn = t.execute
}

func (t *task[T, C]) Reset() {
	*t = task[T, C]{
		wrappedFn: t.wrappedFn,
	}
}

func (t *task[T, C]) execute(ctx context.Context, wf *psgwf.GenericWorkflow[C]) (TaskResult[T], error) {
	startTime := time.Now()
	value, err := t.taskFn(ctx, wf)
	duration := time.Since(startTime)
	res := TaskResult[T]{
		StartTime:    startTime,
		StartLatency: startTime.Sub(t.creationTime),
		Duration:     duration,
		Value:        value,
	}
	t.pool.Put(t)
	return res, err
}
