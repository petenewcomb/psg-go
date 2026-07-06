// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package benchapp

import (
	"context"
	"time"

	"github.com/petenewcomb/streampool/internal/omnipool"
)

type task[T any] struct {
	pool         *omnipool.Pool[task[T]]
	creationTime time.Time
	taskFn       func(context.Context) (T, error)
	wrappedFn    func(context.Context) (TaskResult[T], error)
}

type TaskResult[T any] struct {
	StartTime    time.Time
	StartLatency time.Duration
	Duration     time.Duration
	Value        T
}

func NewTask[T any](
	taskFn func(context.Context) (T, error),
) func(context.Context) (TaskResult[T], error) {
	pool := omnipool.For[task[T]]()
	task := pool.Get()
	task.pool = pool
	task.creationTime = time.Now()
	task.taskFn = taskFn
	return task.wrappedFn
}

func (t *task[T]) Init() {
	t.wrappedFn = t.execute
}

func (t *task[T]) Reset() {
	*t = task[T]{
		wrappedFn: t.wrappedFn,
	}
}

func (t *task[T]) execute(ctx context.Context) (TaskResult[T], error) {
	startTime := time.Now()
	value, err := t.taskFn(ctx)
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
