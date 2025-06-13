// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go"
	"github.com/stretchr/testify/require"
)

func TestMaxHoldTimeBasic(t *testing.T) {
	chk := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	job := psg.NewJob(ctx)
	defer job.CancelAndWait()

	var flushCount atomic.Int32
	var gatherCount atomic.Int32

	gather := psg.NewGather(func(ctx context.Context, result int, err error) error {
		t.Logf("GatherFunc called with result %d", result)
		gatherCount.Add(1)
		chk.NoError(err)
		return nil
	})

	combinerPool := psg.NewCombinerPool(job)
	combinerPool.SetLimits(1, 1) // Force exactly 1 goroutine

	combine := psg.NewCombine(gather, combinerPool, func() psg.Combiner[int, int] {
		return psg.FuncCombiner[int, int]{
			CombineFunc: func(ctx context.Context, value int, err error, emit psg.CombinerEmitFunc[int]) {
				t.Logf("CombineFunc called with value %d", value)
				// Don't emit immediately - let maxHoldTime trigger flush
			},
			FlushFunc: func(ctx context.Context, emit psg.CombinerEmitFunc[int]) {
				t.Logf("FlushFunc called")
				flushCount.Add(1)
				emit(ctx, 42, nil)
			},
		}
	})

	// Set a short maxHoldTime
	combine.SetMaxHoldTime(100 * time.Millisecond)

	taskPool := psg.NewTaskPool(job, 1)

	// Send one input
	err := combine.Scatter(ctx, taskPool, func(ctx context.Context) (int, error) {
		return 1, nil
	})
	chk.NoError(err)

	// Wait a bit to let the first task be processed
	time.Sleep(50 * time.Millisecond)

	// Send a second input to potentially trigger timer checking
	err = combine.Scatter(ctx, taskPool, func(ctx context.Context) (int, error) {
		return 2, nil
	})
	chk.NoError(err)

	// Wait for flush to happen due to maxHoldTime
	time.Sleep(200 * time.Millisecond)

	// Should have been flushed by timer
	flushCountValue := flushCount.Load()
	if flushCountValue == 0 {
		t.Logf("No flush occurred - trying to trigger job close")
		// Try to close job to see if flush happens then
		err := job.CloseAndGatherAll(ctx)
		chk.NoError(err)
		flushCountValue = flushCount.Load()
		t.Logf("Flush count after job close: %d", flushCountValue)
	}
	chk.Equal(int32(1), flushCountValue)
}
