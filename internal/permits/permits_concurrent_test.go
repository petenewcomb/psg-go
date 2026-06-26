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
// Resource's atomic counter), and a cross-child steal (the locked down-walk vs other
// children's releases). Run under -race; the balance means inUse returns to zero, and
// the drain must leak nothing.
func TestConcurrentInheritDeltaSteal(t *testing.T) {
	const capacity, children, iters = 4, 8, 20000
	p := NewPool(&semaphore{capacity: capacity})

	root := p.NewCache()
	rp, _ := root.Acquire() // root runs, then parks → its base (held=1) is borrowable
	rp.Release()

	kids := make([]*Cache, children)
	for i := range kids {
		kids[i] = root.NewChild()
	}

	var invViolated atomic.Bool // set if inUse > held is ever observed (a CAS bug)
	var wg sync.WaitGroup
	for _, kid := range kids {
		wg.Add(1)
		go func(c *Cache) {
			defer wg.Done()
			for range iters {
				if pm, ok := c.Acquire(); ok {
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

	require.NoError(t, p.CheckInvariants(), "invariants hold once quiescent")

	// Drain: exit every child then the root; nothing should be running, so all held
	// returns to the Resource.
	for _, kid := range kids {
		require.True(t, kid.ReleaseRef())
	}
	require.True(t, root.ReleaseRef())
	require.NoError(t, p.CheckInvariants())
	require.Equal(t, 0, p.totalHeld(), "no permit leaked")
	require.Empty(t, p.roots)
}

// Concurrent structural churn vs steal: while one set of goroutines runs the
// acquire/steal hot path on a fixed subtree, another set repeatedly creates a
// sub-wave (NewChild), runs+releases a body in it, and drains it (ReleaseRef) — so
// destroy (which mutates children and returns capacity) runs concurrently with the
// steal down-walk and lock-free acquires. Exercises the per-Pool lock's
// serialization of structure against steal, and destroy's return-to-Resource.
func TestConcurrentChurnVsSteal(t *testing.T) {
	const capacity = 4
	p := NewPool(&semaphore{capacity: capacity})

	// A fixed subtree with a parked, lending root.
	root := p.NewCache()
	rp, _ := root.Acquire()
	rp.Release()
	fixed := make([]*Cache, 4)
	for i := range fixed {
		fixed[i] = root.NewChild()
	}

	var wg sync.WaitGroup

	// Hot-path runners on the fixed subtree.
	for _, c := range fixed {
		wg.Add(1)
		go func(c *Cache) {
			defer wg.Done()
			for range 10000 {
				if pm, ok := c.Acquire(); ok {
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
				sub := root.NewChild()
				if pm, ok := sub.Acquire(); ok {
					pm.Release()
				}
				sub.ReleaseRef() // drains the ephemeral sub-wave (destroy)
			}
		}()
	}

	wg.Wait()

	require.NoError(t, p.CheckInvariants())
	for _, c := range fixed {
		require.True(t, c.ReleaseRef())
	}
	require.True(t, root.ReleaseRef())
	require.Equal(t, 0, p.totalHeld(), "no permit leaked after churn")
	require.Empty(t, p.roots)
}
