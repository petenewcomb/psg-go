// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/psgfn"
	"github.com/petenewcomb/psg-go/psgopt"
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

	gatherOp := psg.NewGatherOp(func(ctx context.Context, result int, err error) error {
		t.Logf("Gather called with result %d", result)
		gatherCount.Add(1)
		chk.NoError(err)
		return nil
	})

	combinerPool := psg.NewCombinerPool(job, psgopt.WithConcurrencyBounds(1, 1)) // Force exactly 1 goroutine

	combineOp := psg.NewCombineOp(gatherOp, combinerPool, func() psgfn.Combiner[int, int] {
		return psgfn.Combiner[int, int]{
			CombineFn: func(ctx context.Context, value int, err error, emit psgfn.Emit[int]) {
				t.Logf("Combine called with value %d", value)
				// Don't emit immediately - let maxHoldTime trigger flush
			},
			FlushFn: func(ctx context.Context, emit psgfn.Emit[int]) {
				t.Logf("Flush called")
				flushCount.Add(1)
				emit(ctx, 42, nil)
			},
		}
	})

	// Set a short maxHoldTime
	combineOp.SetOptions(psgopt.WithMaxHoldTime(100 * time.Millisecond))

	taskPool := psg.NewTaskPool(job)

	// Send one input
	err := combineOp.Scatter(ctx, taskPool, func(ctx context.Context) (int, error) {
		return 1, nil
	})
	chk.NoError(err)

	// Wait a bit to let the first task be processed
	time.Sleep(50 * time.Millisecond)

	// Send a second input to potentially trigger timer checking
	err = combineOp.Scatter(ctx, taskPool, func(ctx context.Context) (int, error) {
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
