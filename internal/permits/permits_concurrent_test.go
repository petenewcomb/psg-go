// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"sync"
	"sync/atomic"
	"testing"

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
	rp, _ := root.Acquire(1) // root runs, then parks → its base (held=1) is borrowable
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
			for range iters {
				if pm, ok := c.Acquire(1); ok {
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
	rp, _ := root.Acquire(1)
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
			for range 10000 {
				if pm, ok := c.Acquire(1); ok {
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
			for range 4000 {
				sub := tp.newChild(root)
				if pm, ok := sub.Acquire(1); ok {
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
