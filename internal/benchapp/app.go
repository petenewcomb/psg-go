// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package benchapp

import (
	"context"
	"time"

	"github.com/petenewcomb/psg-go/psgfn"
	"github.com/petenewcomb/psg-go/psgwf"
)

type Context struct {
	WorkflowStartTime time.Time
}

type Workflow = psgwf.GenericWorkflow[Context]

type topLevelTask[T any] struct {
	duration       time.Duration
	simulateWorkFn func(time.Duration)
	value          T
}

func NewTopLevelTask[T any](duration time.Duration, simulateWorkFn func(time.Duration),
	value T) psgwf.GenericTaskFunc[TaskResult[T], Context] {
	t := &topLevelTask[T]{
		duration:       duration,
		simulateWorkFn: simulateWorkFn,
		value:          value,
	}
	return NewTask(t.execute)
}

func (t *topLevelTask[T]) execute(context.Context, *Workflow) (T, error) {
	t.simulateWorkFn(t.duration)
	return t.value, nil
}

type fanOutCombiner[T any] struct {
	combineDuration       time.Duration
	simulateCombineWorkFn func(time.Duration)
	subtaskCount          int
	scatterFn             func(context.Context, T, error) error
}

func NewFanOutCombiner[T any](
	combineDuration time.Duration,
	simulateCombineWorkFn func(time.Duration),
	subtaskCount int,
	scatterFn func(context.Context, T, error) error,
	fallbackFn func(res CombinerResult[struct{}]),
) psgwf.GenericCombiner[TaskResult[T], CombinerResult[struct{}], Context] {
	c := &fanOutCombiner[T]{
		combineDuration:       combineDuration,
		simulateCombineWorkFn: simulateCombineWorkFn,
		subtaskCount:          subtaskCount,
		scatterFn:             scatterFn,
	}
	return NewCombiner(nil, c, fallbackFn)
}

func (c *fanOutCombiner[T]) Combine(ctx context.Context, wf *Workflow,
	inputValue T, inputErr error) (time.Time, error) {
	c.simulateCombineWorkFn(c.combineDuration)
	for range c.subtaskCount {
		if err := c.scatterFn(ctx, inputValue, inputErr); err != nil {
			return time.Time{}, err
		}
	}
	return time.Time{}, nil
}

func (c *fanOutCombiner[T]) Flush(ctx context.Context) (wf *Workflow, value struct{}, err error) {
	return nil, struct{}{}, psgfn.ErrDoNotGather
}

type fanInCombiner[T any] struct {
	combineDuration       time.Duration
	simulateCombineWorkFn func(time.Duration)
	subtaskCount          int
	scatterFn             func(context.Context, T, error) error
}

func NewFanInCombiner[T any](
	combineDuration time.Duration,
	simulateCombineWorkFn func(time.Duration),
	subtaskCount int,
) psgwf.GenericCombiner[TaskResult[T], CombinerResult[struct{}], Context] {
	c := &fanInCombiner[T]{
		combineDuration:       combineDuration,
		simulateCombineWorkFn: simulateCombineWorkFn,
		subtaskCount:          subtaskCount,
	}
	return NewCombiner(nil, c, nil)
}

func (c *fanInCombiner[T]) Combine(ctx context.Context, wf *Workflow, inputValue T, inputErr error) (time.Time, error) {
	c.simulateCombineWorkFn(c.combineDuration)
	for range c.subtaskCount {
		if err := c.scatterFn(ctx, inputValue, inputErr); err != nil {
			return time.Time{}, err
		}
	}
	return time.Time{}, nil
}

func (c *fanInCombiner[T]) Flush(ctx context.Context) (wf *Workflow, value struct{}, err error) {
	return nil, struct{}{}, psgfn.ErrDoNotGather
}
