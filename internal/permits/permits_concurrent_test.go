// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	dr := NewDemand()
	rp, _ := root.Acquire(dr, 1) // root runs, then parks → its base (held=1) is borrowable
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
			d := NewDemand()
			defer d.Invalidate() // a final miss leaves the demand queued (unified queue)
			for range iters {
				if pm, _ := c.Acquire(d, 1); pm.Held() {
					if h, u := pm.backing.counts.load(); u > h {
						invViolated.Store(true)
					}
					pm.Release()
				}
				// On a miss the demand queued; the next attempt re-presents it.
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
	dr := NewDemand()
	rp, _ := root.Acquire(dr, 1)
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
			d := NewDemand()
			defer d.Invalidate() // a final miss leaves the demand queued
			for range 10000 {
				if pm, _ := c.Acquire(d, 1); pm.Held() {
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
			d := NewDemand()
			for range 4000 {
				sub := tp.newChild(root)
				if pm, _ := sub.Acquire(d, 1); pm.Held() {
					pm.Release()
				}
				// A miss homed the demand under this ephemeral sub-cache;
				// withdraw it before the next iteration re-homes elsewhere.
				d.Invalidate()
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
			d := NewDemand()
			for range iters {
				pm, err := c.AcquireWait(ctx, d, w)
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

// TestConcurrentOverdraftSuspendChurn is the adversarial concurrent -race stress for
// the weighted core's contention paths: promoteScan (head enqueue / promote / retire
// churn), the suspension counters and their ResumeDriver nudge, cross-subtree steal
// vs forest mutation, and blocking waiters — all racing on ONE granting pool. Standing
// OVERDRAFT EPISODES rarely form here (a grant needs a quiescent, zero-inUse pool,
// which eight hammering workers almost never produce); the within-episode accounting
// is stressed concurrently by TestConcurrentEpisodeClaimants instead, which stands an
// episode deliberately. Four worker roles share the pool and a common root:
//
//   - waiter: blocking AcquireWait at w ≤ capacity. Such a demand never needs an
//     episode and, by FIFO succession, must eventually be satisfied as capacity
//     cycles — so a ctx timeout here is a real wedge (a missed wake / stuck head),
//     the property this role exists to catch.
//   - episode: non-blocking Acquire at w > capacity in a retry loop — forms an
//     overdraft episode opportunistically when the pool goes quiescent, completes
//     it fast (release + invalidate → anchor destroy → endEpisode), or withdraws
//     and retries. Heavy promoteScan / episode-transition churn.
//   - suspender: SuspendDriver/ResumeDriver brackets on a dedicated off-chain cache
//     — a stranger to every episode, so its suspensions gate grants and its resumes
//     nudge the waiting head; exercises the suspension counters and stranger check
//     concurrently with grants.
//   - churner: creates a child under the shared root, acquires/releases on it, and
//     destroys it — forest mutation racing the steal walk and the promotion scan.
//
// A wedge surfaces as a ctx-deadline failure; a race surfaces under -race; and at
// quiescence the accounting must be whole (no episode standing, no leak).
func TestConcurrentOverdraftSuspendChurn(t *testing.T) {
	const capacity, workers, iters = 2, 8, 800
	tp := newGrantTestPool(capacity)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	root := tp.NewCache()

	var wedged atomic.Int64
	var wg sync.WaitGroup
	for wi := range workers {
		wg.Add(1)
		go func(wi int) {
			defer wg.Done()
			switch wi % 4 {
			case 0: // waiter — must make progress (wedge detector)
				base := tp.newChild(root)
				defer base.ReleaseRef()
				var d Demand
				d.Init()
				defer d.Invalidate()
				for i := range iters {
					w := 1 + i%capacity // 1..capacity
					pm, err := base.AcquireWait(ctx, &d, w)
					if err != nil {
						wedged.Add(1)
						return
					}
					pm.Release()
				}
			case 1: // episode — opportunistic overdraft, complete or withdraw
				base := tp.newChild(root)
				defer base.ReleaseRef()
				var d Demand
				d.Init()
				defer d.Invalidate()
				for range iters {
					pm, err := base.Acquire(&d, capacity+1)
					if !assert.NoError(t, err, "grant-mode never refuses") {
						return
					}
					if pm.Held() {
						pm.Release()
					}
					d.Invalidate() // complete the episode (or withdraw a queued/head demand)
				}
			case 2: // suspender — stranger suspensions racing grants
				susp := tp.NewCache()
				defer susp.ReleaseRef()
				for range iters {
					susp.SuspendDriver()
					susp.ResumeDriver()
				}
			case 3: // churner — forest mutation vs steal/promote
				var d Demand
				d.Init()
				defer d.Invalidate()
				for range iters {
					child := tp.newChild(root)
					if pm, _ := child.Acquire(&d, 1); pm.Held() {
						pm.Release()
					}
					d.Invalidate() // a miss homed d under child; withdraw before destroy
					child.ReleaseRef()
				}
			}
		}(wi)
	}
	wg.Wait()

	require.Equal(t, int64(0), wedged.Load(),
		"a w ≤ capacity waiter must eventually be satisfied (no missed wake / stuck head)")
	require.True(t, root.ReleaseRef())
	require.Nil(t, tp.od.Load(), "no episode stands at quiescence")
	require.Nil(t, tp.head.Load(), "the head slot is open at quiescence")
	require.NoError(t, checkInvariants(tp.sem, tp.snapshot()))
	require.Equal(t, 0, tp.totalHeld(), "no permit leaked")
	require.Equal(t, int64(0), tp.sem.inFlight.Load(), "the Resource is fully released")
}

// TestConcurrentEpisodeClaimants stresses the overdraft episode ACCOUNTING under
// concurrency — the interleavings the sequential grant-mode model cannot reach.
// Standing an episode requires a quiescent pool, so each round forms one
// deliberately (single-threaded, quiescent), then unleashes concurrent activity
// WITHIN the standing episode: exempt claimants racing for the parked hoard and the
// allowance, the owner parking/resuming (excess round-tripping through the allowance
// while claims consume it), and a stranger suspender — so the allowance CAS, the
// release excess-return, the claimant wake routing, and the suspension counters all
// race. The oracle is endEpisode's own assert (the episode must balance —
// allowance == total — when the anchor destroys) plus a quiescent checkEpisode at
// each round boundary; a concurrent accounting leak (an occupy/release that skips
// the allowance) trips one of them, and any data race trips -race.
func TestConcurrentEpisodeClaimants(t *testing.T) {
	const capacity, rounds, claimers, inner = 2, 150, 4, 40
	tp := newGrantTestPool(capacity)

	for range rounds {
		// Form the episode while quiescent: gather `capacity`, overdraft `2`.
		host := tp.NewCache()
		var owner Demand
		owner.Init()
		pm, err := host.Acquire(&owner, capacity+2)
		require.NoError(t, err)
		require.True(t, pm.Held(), "a quiescent w>capacity acquire grants an episode")
		require.NotNil(t, tp.od.Load(), "the episode stands")
		anchor := owner.cache.Load()
		pm.Release() // park the owner so its hoard + allowance are up for grabs

		var wg sync.WaitGroup
		// Exempt claimants: fresh children of the anchor, claim/release/reset.
		for range claimers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				child := anchor.NewChild()
				defer child.ReleaseRef()
				var d Demand
				d.Init()
				defer d.Invalidate()
				for i := range inner {
					w := 1 + i%(capacity+2)
					p, err := child.Acquire(&d, w)
					if !assert.NoError(t, err, "grant-mode never refuses") {
						return
					}
					if p.Held() {
						p.Release()
					}
					d.Invalidate()
				}
			}()
		}
		// Owner park/resume churn: its excess round-trips through the allowance
		// racing the claimants' claims.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range inner {
				p, err := host.Acquire(&owner, capacity+2)
				if !assert.NoError(t, err) {
					return
				}
				if p.Held() {
					p.Release()
				}
			}
		}()
		// Stranger suspender: bumps the suspension counters (and nudges) while the
		// episode stands and claimants may trigger extension evaluations.
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := tp.NewCache()
			defer s.ReleaseRef()
			for range inner * 2 {
				s.SuspendDriver()
				s.ResumeDriver()
			}
		}()
		wg.Wait()

		// End the episode: the owner exits, the anchor's subtree has drained, so the
		// anchor destroys and endEpisode asserts the allowance is fully home.
		owner.Invalidate()
		require.True(t, host.ReleaseRef())
		require.Nil(t, tp.od.Load(), "the episode ended")
		require.Nil(t, tp.head.Load(), "the head slot is open")
		tp.checkEpisode(t)
		require.Equal(t, 0, tp.totalHeld(), "no permit leaked across the round")
	}
	require.Equal(t, int64(0), tp.sem.inFlight.Load(), "the Resource is fully released")
}
