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

// Concurrent hot-path stress: a parked root lends one borrowable base while N child
// goroutines hammer balanced acquire/release on their own caches. Every acquire
// contends for the same scarce capacity through three concurrent paths at once —
// inheritance (the shared root's idle base, a lock-free up-walk), a fresh delta (the
// Resource's atomic counter), and a cross-child steal (the lock-free nbcq walk vs
// other children's releases). Run under -race; balance means inUse returns to zero,
// and the drain must leak nothing.
func TestConcurrentInheritDeltaSteal(t *testing.T) {
	const capacity, children, iters = 4, 8, 20000
	tp := newTestPool(capacity)

	root := tp.NewCache()
	var dr Demand
	dr.Init()
	rp, _ := root.Acquire(&dr, 1) // root runs, then parks → its base (held=1) is borrowable
	rp.Release()

	kids := make([]*Cache, children)
	for i := range kids {
		kids[i] = tp.newChild(root)
	}

	var invViolated atomic.Bool // set if inUse > held is ever observed (a CAS bug)
	var wg sync.WaitGroup
	for _, kid := range kids {
		wg.Add(1)
		go func(c *Cache) {
			defer wg.Done()
			var d Demand
			d.Init()
			for range iters {
				if pm, _ := c.Acquire(&d, 1); pm.Held() {
					if h, u := pm.backing.counts.load(); u > h {
						invViolated.Store(true)
					}
					pm.Release()
				}
				// On a miss every permit was in use elsewhere; just retry next loop.
			}
		}(kid)
	}
	wg.Wait()
	require.False(t, invViolated.Load(), "inUse ≤ held held at every observed snapshot")

	require.NoError(t, checkInvariants(tp.sem, tp.snapshot()), "invariants hold once quiescent")

	// Drain: exit every child then the root; nothing running, so all held returns.
	for _, kid := range kids {
		require.True(t, kid.ReleaseRef())
	}
	require.True(t, root.ReleaseRef())
	require.Equal(t, 0, tp.totalHeld(), "no permit leaked")
}

// Concurrent structural churn vs steal: while one set of goroutines runs the
// acquire/steal hot path on a fixed subtree, another set repeatedly creates a
// sub-wave (NewChild), runs+releases a body in it, and drains it (ReleaseRef) — so
// destroy (which mutates the forest and returns capacity) runs concurrently with the
// steal walk and lock-free acquires. Exercises lazy reaping of dead caches by the
// steal, the CAS-drain return-to-Resource, and lock-free refcount teardown.
func TestConcurrentChurnVsSteal(t *testing.T) {
	const capacity = 4
	tp := newTestPool(capacity)

	root := tp.NewCache()
	var dr Demand
	dr.Init()
	rp, _ := root.Acquire(&dr, 1)
	rp.Release()
	fixed := make([]*Cache, 4)
	for i := range fixed {
		fixed[i] = tp.newChild(root)
	}

	var wg sync.WaitGroup

	// Hot-path runners on the fixed subtree.
	for _, c := range fixed {
		wg.Add(1)
		go func(c *Cache) {
			defer wg.Done()
			var d Demand
			d.Init()
			for range 10000 {
				if pm, _ := c.Acquire(&d, 1); pm.Held() {
					pm.Release()
				}
			}
		}(c)
	}

	// Churners: create / run / drain ephemeral sub-waves under the same root.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var d Demand
			d.Init()
			for range 4000 {
				sub := tp.newChild(root)
				if pm, _ := sub.Acquire(&d, 1); pm.Held() {
					pm.Release()
				}
				sub.ReleaseRef() // drains the ephemeral sub-wave (destroy)
			}
		}()
	}

	wg.Wait()

	require.NoError(t, checkInvariants(tp.sem, tp.snapshot()))
	for _, c := range fixed {
		require.True(t, c.ReleaseRef())
	}
	require.True(t, root.ReleaseRef())
	require.Equal(t, 0, tp.totalHeld(), "no permit leaked after churn")
}

// Weighted gathers racing each other and weight-1 traffic, in a configuration where
// every demand is always satisfiable (Σ peak concurrent weight ≤ capacity), so the
// pre-barrier fairness gaps cannot starve anyone: this exercises the gather's
// deposit / stealOutUpTo / occupy CAS interleavings under -race without asserting a
// fairness the design doesn't have yet. Over-subscribed weighted contention — where
// freelance gatherers can legitimately starve or contest each other's hoards — gets
// its liveness stress only once head-only gathering lands with the demand-FIFO
// barrier (weighted-acquisition.md, step-2 barrier checkpoint).
func TestConcurrentWeightedGatherSatisfiable(t *testing.T) {
	const capacity, workers, iters = 8, 4, 3000
	tp := newTestPool(capacity) // weights 1,2,1,2 → peak demand 6 ≤ 8

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var failed atomic.Int64
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c := tp.NewCache()
			defer c.ReleaseRef()
			var d Demand
			d.Init()
			for range iters {
				pm, err := c.AcquireWait(ctx, &d, w)
				if err != nil {
					failed.Add(1)
					return
				}
				pm.Release()
			}
		}(1 + i%2)
	}
	wg.Wait()

	require.Equal(t, int64(0), failed.Load(), "an always-satisfiable weighted mix must never wedge")
	require.NoError(t, checkInvariants(tp.sem, tp.snapshot()))
	require.Equal(t, 0, tp.totalHeld(), "no permit leaked")
}
