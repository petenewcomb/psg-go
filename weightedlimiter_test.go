// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool"
	"github.com/stretchr/testify/require"
)

// TestWeightedLauncher_WeightSerializesDispatch proves the weigher drives real admission
// end-to-end. With a weighted ceiling of 4 and every body weighing 3, at most one body can
// hold a permit at a time (3+3 = 6 > 4), so three concurrent dispatches serialize: the
// peak concurrency is exactly 1. If the weigher were ignored (each dispatch taking a plain
// weight-1 permit), all three would fit under 4 and overlap during the hold. The upper
// bound peak ≤ 1 is deterministic — the limiter forbids two weight-3 holders — while the
// deliberate hold window gives a mis-wired weight every chance to overlap and be caught.
func TestWeightedLauncher_WeightSerializesDispatch(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	var wave streampool.Wave

	wl := streampool.NewWeightedSemaphore(4)
	var live, peak atomic.Int32

	launcher := streampool.NewFnLauncher(
		func(_ context.Context, _ int, _ error) error {
			n := live.Add(1)
			for { // record the running peak
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond) // hold the permit, inviting overlap
			live.Add(-1)
			return nil
		},
	).WithWeightLimits(streampool.NewWeightLimiter(wl, func(int) int { return 3 })).In(&wave)

	// Submit blocks under backpressure (a full limiter), so the three weight-3 dispatches
	// must be launched concurrently for the second/third to queue behind the first. Submit
	// errors are collected and asserted on the test goroutine (require must not fire off it).
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- launcher.Submit(ctx, 0)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		chk.NoError(err)
	}
	chk.NoError(wave.CloseAndSkimAll(ctx))

	chk.Equal(int32(1), peak.Load(), "weight-3 bodies serialize under ceiling 4: peak concurrency 1")
}
