// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package benchapp

import (
	"context"
	"time"

	"github.com/petenewcomb/streampool"
)

type topLevelTask[T any] struct {
	duration       time.Duration
	simulateWorkFn func(time.Duration)
	value          T
}

func NewTopLevelTask[T any](duration time.Duration, simulateWorkFn func(time.Duration),
	value T) func(context.Context) (TaskResult[T], error) {
	t := &topLevelTask[T]{
		duration:       duration,
		simulateWorkFn: simulateWorkFn,
		value:          value,
	}
	return NewTask(t.execute)
}

func (t *topLevelTask[T]) execute(context.Context) (T, error) {
	t.simulateWorkFn(t.duration)
	return t.value, nil
}

type fanOutFunnel[T any] struct {
	funnelDuration       time.Duration
	simulateFunnelWorkFn func(time.Duration)
	subtaskCount         int
	scatterFn            func(context.Context, T, error) error
}

func NewFanOutFunnel[T any](
	funnelDuration time.Duration,
	simulateFunnelWorkFn func(time.Duration),
	subtaskCount int,
	scatterFn func(context.Context, T, error) error,
	fallbackFn func(res FunnelResult[T]),
) streampool.Accumulator[TaskResult[T]] {
	c := &fanOutFunnel[T]{
		funnelDuration:       funnelDuration,
		simulateFunnelWorkFn: simulateFunnelWorkFn,
		subtaskCount:         subtaskCount,
		scatterFn:            scatterFn,
	}
	return NewFunnel[T](nil, c, fallbackFn)
}

func (c *fanOutFunnel[T]) Accumulate(ctx context.Context,
	inputValue T, inputErr error) (time.Time, error) {
	c.simulateFunnelWorkFn(c.funnelDuration)
	for range c.subtaskCount {
		if err := c.scatterFn(ctx, inputValue, inputErr); err != nil {
			return time.Time{}, err
		}
	}
	return time.Time{}, nil
}

func (c *fanOutFunnel[T]) Flush(ctx context.Context) error {
	// Wave 2: no aggregated output to emit. Per-call subtasks are
	// already scattered from Accumulate; nothing to do on Flush.
	return nil
}

type fanInFunnel[T any] struct {
	funnelDuration       time.Duration
	simulateFunnelWorkFn func(time.Duration)
	subtaskCount         int
	scatterFn            func(context.Context, T, error) error
}

func NewFanInFunnel[T any](
	funnelDuration time.Duration,
	simulateFunnelWorkFn func(time.Duration),
	subtaskCount int,
) streampool.Accumulator[TaskResult[T]] {
	c := &fanInFunnel[T]{
		funnelDuration:       funnelDuration,
		simulateFunnelWorkFn: simulateFunnelWorkFn,
		subtaskCount:         subtaskCount,
	}
	return NewFunnel[T](nil, c, nil)
}

func (c *fanInFunnel[T]) Accumulate(ctx context.Context,
	inputValue T, inputErr error) (time.Time, error) {
	c.simulateFunnelWorkFn(c.funnelDuration)
	for range c.subtaskCount {
		if err := c.scatterFn(ctx, inputValue, inputErr); err != nil {
			return time.Time{}, err
		}
	}
	return time.Time{}, nil
}

func (c *fanInFunnel[T]) Flush(ctx context.Context) error {
	return nil
}
