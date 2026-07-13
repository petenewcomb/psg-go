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
			d := NewDemand()
			defer d.Invalidate() // release the home a mid-loop miss created
			for range iters {
				pm, err := c.AcquireWait(ctx, d, 1)
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

// The weighted-release under-notify regression: ONE
// release of weight w frees w permits at once, and plain wake-one would admit a
// single waiter and strand the rest over borrowable capacity — no further wake would
// ever come. The chained wake walks them all: each admitted waiter probes once
// (rule 2), the first miss terminates the chain (rule 3). The admitted waiters HOLD
// their permits until everyone is in, so the weighted release is the only wake
// source — a dropped chain fails this test deterministically.
func TestWeightedReleaseChainAdmitsAllSatisfiable(t *testing.T) {
	const capacity, waiters = 3, 3
	tp := newTestPool(capacity)

	g := tp.NewCache()
	dg := NewDemand()
	pm, err := g.Acquire(dg, capacity) // whole-grant fast path holds ALL capacity
	require.NoError(t, err)
	require.True(t, pm.Held())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admitted := make(chan struct{}, waiters)
	holdRelease := make(chan struct{})
	var failed atomic.Int64
	var wg sync.WaitGroup
	for range waiters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := tp.NewCache()
			defer c.ReleaseRef()
			d := NewDemand()
			defer d.Invalidate()
			pmw, err := c.AcquireWait(ctx, d, 1)
			if err != nil {
				failed.Add(1)
				return
			}
			admitted <- struct{}{}
			<-holdRelease
			pmw.Release()
		}()
	}

	// Let the waiters park, then free all three permits with ONE weighted release.
	time.Sleep(50 * time.Millisecond)
	pm.Release()

	for i := range waiters {
		select {
		case <-admitted:
		case <-time.After(20 * time.Second):
			t.Fatalf("chain under-notified: only %d of %d waiters admitted", i, waiters)
		}
	}
	close(holdRelease)
	wg.Wait()
	require.Equal(t, int64(0), failed.Load())

	dg.Invalidate()
	g.ReleaseRef()
	require.Equal(t, 0, tp.totalHeld(), "no permit leaked")
	tp.check(t)
}

// A cancelled context unblocks a parked AcquireWait promptly with the ctx error,
// rather than wedging it.
func TestAcquireWaitCancel(t *testing.T) {
	tp := newTestPool(1)
	hog := tp.NewCache()
	dh := NewDemand()
	dw := NewDemand()
	hp, _ := hog.Acquire(dh, 1) // saturate the one permit and keep it in use
	defer hp.Release()

	waiter := tp.NewCache()
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := waiter.AcquireWait(ctx, dw, 1)
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
