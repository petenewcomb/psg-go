// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// AcquireWait must never block permanently while progress is possible. Far more
// contenders than capacity each run AcquireWait+Release in a loop; the permits bounce
// between them by steal, and every parked waiter must be woken by a release and
// eventually succeed. A liveness bug surfaces as a ctx-deadline error from
// AcquireWait, not a hung test. Run under -race.
func TestAcquireWaitLiveness(t *testing.T) {
	const capacity, contenders, iters = 2, 8, 1000
	tp := newTestPool(capacity)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var failed atomic.Int64 // AcquireWait that returned an error (a hang)
	var done atomic.Int64   // successful acquire/release round-trips
	var wg sync.WaitGroup
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := tp.NewCache()
			defer c.ReleaseRef()
			for range iters {
				pm, err := c.AcquireWait(ctx)
				if err != nil {
					failed.Add(1)
					return
				}
				done.Add(1)
				pm.Release()
			}
		}()
	}
	wg.Wait()

	require.Equal(t, int64(0), failed.Load(), "no AcquireWait blocked permanently")
	require.Equal(t, int64(contenders*iters), done.Load(), "every contender completed all rounds")
	require.Equal(t, 0, tp.totalHeld(), "no permit leaked")
}

// A cancelled context unblocks a parked AcquireWait promptly with the ctx error,
// rather than wedging it.
func TestAcquireWaitCancel(t *testing.T) {
	tp := newTestPool(1)
	hog := tp.NewCache()
	hp, _ := hog.Acquire() // saturate the one permit and keep it in use
	defer hp.Release()

	waiter := tp.NewCache()
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := waiter.AcquireWait(ctx)
		errCh <- err
	}()

	// Give the waiter time to park, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("AcquireWait did not return after context cancellation")
	}
}
