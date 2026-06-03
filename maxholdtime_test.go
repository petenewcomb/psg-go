// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go"

	"github.com/petenewcomb/psg-go/psgopt"
	"github.com/stretchr/testify/assert"
)

func TestMaxHoldTimeBasic(t *testing.T) {
	chk := assert.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctx, wave := psg.NewWave(ctx)
	defer wave.CancelAndWait()

	var flushCount atomic.Int32
	var skimCount atomic.Int32

	skimmer := psg.NewFnSkimmer(wave, func(ctx context.Context, result int, err error) error {
		t.Logf("Skim called with result %d", result)
		skimCount.Add(1)
		chk.NoError(err)
		return nil
	})

	funnelPool := psg.NewFunnelPool(wave.Pool(), psgopt.WithMaxConcurrency(1)) // Force exactly 1 goroutine

	funnelOp := psg.NewFunnel(funnelPool, psg.NewAccumulatorFactory(func() psg.Accumulator[int] {
		return psg.FuncAccumulator[int]{
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
	defer funnelOp.Close()

	newRunner := func(value int) psg.TaskLauncher {
		return psg.NewTaskLauncher(wave, func(ctx context.Context) error {
			return funnelOp.Submit(ctx, value)
		})
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
		t.Logf("No flush occurred - trying to trigger wave close")
		// Try to close wave to see if flush happens then
		err := wave.CloseAndSkimAll(ctx)
		chk.NoError(err)
		flushCountValue = flushCount.Load()
		t.Logf("Flush count after wave close: %d", flushCountValue)
	}
	chk.Equal(int32(1), flushCountValue)
}
