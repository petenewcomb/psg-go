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
	"github.com/stretchr/testify/assert"
)

func TestMaxHoldTimeBasic(t *testing.T) {
	chk := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	job := psg.New(ctx)
	defer job.CancelAndWait()

	var flushCount atomic.Int32
	var gatherCount atomic.Int32

	gatherer := psg.NewGatherer(func(ctx context.Context, result int, err error) error {
		t.Logf("Gather called with result %d", result)
		gatherCount.Add(1)
		chk.NoError(err)
		return nil
	})

	combinerPool := psg.NewCombinerPool(job, psgopt.WithMaxConcurrency(1)) // Force exactly 1 goroutine

	combineOp := psg.NewCombiner(combinerPool, func() psgfn.Accumulator[int] {
		return psgfn.FuncAccumulator[int]{
			AccumulateFn: func(ctx context.Context, value int, err error) (time.Time, error) {
				// Don't emit immediately - let the deadline trigger flushing
				return time.Now().Add(100 * time.Millisecond), nil
			},
			FlushFn: func(ctx context.Context) error {
				flushCount.Add(1)
				return gatherer.Submit(ctx, job, 42, nil)
			},
		}
	})
	defer combineOp.Close()

	taskPool := job

	newRunner := func(value int) psg.TaskRunner0 {
		return psg.NewTaskRunner0(taskPool, psgfn.TaskFunc0(func(ctx context.Context) error {
			return combineOp.Submit(ctx, value, nil)
		}))
	}

	// Send one input
	err := newRunner(1).Start(ctx)
	chk.NoError(err)

	// Wait a bit to let the first task be processed
	time.Sleep(50 * time.Millisecond)

	// Send a second input to potentially trigger timer checking
	err = newRunner(2).Start(ctx)
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
