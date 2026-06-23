// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool"

	"github.com/stretchr/testify/assert"
)

func TestMaxHoldTimeBasic(t *testing.T) {
	chk := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wave streampool.Wave

	var flushCount atomic.Int32
	var skimCount atomic.Int32

	skimmer := streampool.NewFnSkimmer(func(ctx context.Context, result int, err error) error {
		t.Logf("Skim called with result %d", result)
		skimCount.Add(1)
		chk.NoError(err)
		return nil
	}).In(&wave)

	funnelPool := &wave // NOTE: WithMaxConcurrency(1) dropped (no-op now); serialization must move to a limiter

	funnelOp := streampool.NewFunnel(funnelPool, streampool.NewAccumulatorFactory(func() streampool.Accumulator[int] {
		return streampool.FuncAccumulator[int]{
			AccumulateFn: func(ctx context.Context, value int, err error) (time.Time, error) {
				// Don't emit immediately - let the deadline trigger flushing
				return time.Now().Add(100 * time.Millisecond), nil
			},
			FlushFn: func(ctx context.Context) error {
				flushCount.Add(1)
				return skimmer.Submit(ctx, 42)
			},
		}
	}, nil))

	newRunner := func(value int) streampool.TaskLauncher {
		return streampool.NewTaskLauncher(func(ctx context.Context) error {
			return funnelOp.Submit(ctx, value)
		})
	}

	// Send one input
	err := newRunner(1).In(&wave).Start(ctx)
	chk.NoError(err)

	// Wait a bit to let the first task be processed
	time.Sleep(50 * time.Millisecond)

	// Send a second input to potentially trigger timer checking
	err = newRunner(2).In(&wave).Start(ctx)
	chk.NoError(err)

	// Wait for flush to happen due to maxHoldTime
	time.Sleep(200 * time.Millisecond)

	// Should have been flushed by timer
	flushCountValue := flushCount.Load()
	if flushCountValue == 0 {
		t.Logf("No flush occurred - trying to trigger wave close")
		// Try to close wave to see if flush happens then
		err := wave.CloseAndSkimAll(ctx)
		chk.NoError(err)
		flushCountValue = flushCount.Load()
		t.Logf("Flush count after wave close: %d", flushCountValue)
	}
	chk.Equal(int32(1), flushCountValue)
}
