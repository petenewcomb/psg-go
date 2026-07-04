// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// maxCaches bounds the forest so the reachable state space stays finite under
// adversarial generation.
const maxCaches = 10

// modelUnit tracks the usage state the real Cache does not: whether the owning unit's
// own reference is still held (it must be ReleaseRef'd exactly once), and the Permit
// backing its body while it runs. A unit models one body: it runs (acquire, at a
// weight drawn per attempt), parks/completes (release), may resume, and finally exits
// (ReleaseRef). demand is the unit's caller-held identity, re-presented across its
// sequential acquire episodes (Decision 4's postpone-retry shape).
type modelUnit struct {
	cache       *Cache
	demand      Demand
	unitRefHeld bool
	pm          Permit
	running     bool
}

// TestPermitsModel model-checks the permit invariants (permit-core.md "Invariants")
// sequentially — the algorithm, not the concurrency (that is the -race stress tests):
// random forests of caches on a shared Pool, random weighted acquire/park/resume/exit.
// After every operation it asserts the invariants; on every blocked Acquire the
// weight-aware liveness property (everything gatherable — borrowable anywhere plus
// free Resource capacity — is less than w, so a sequential gather had to fail); and
// after a full drain, no leak. Weights range past capacity so the legitimately
// infeasible block is exercised too, and a failed gather's retained hoard is part of
// the modeled state (it stays borrowable for every later operation).
func TestPermitsModel(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		capacity := rapid.IntRange(1, 4).Draw(t, "capacity")
		tp := newTestPool(capacity)

		var units []*modelUnit

		check := func() { tp.check(t) }

		pick := func(t *rapid.T, label string, want func(*modelUnit) bool) *modelUnit {
			var cands []*modelUnit
			for _, u := range units {
				if want(u) {
					cands = append(cands, u)
				}
			}
			if len(cands) == 0 {
				return nil
			}
			return cands[rapid.IntRange(0, len(cands)-1).Draw(t, label)]
		}

		t.Repeat(map[string]func(*rapid.T){
			"newRoot": func(t *rapid.T) {
				if len(units) >= maxCaches {
					return
				}
				u := &modelUnit{cache: tp.NewCache(), unitRefHeld: true}
				u.demand.Init()
				units = append(units, u)
				check()
			},
			"newChild": func(t *rapid.T) {
				if len(units) >= maxCaches {
					return
				}
				parent := pick(t, "child-parent", func(u *modelUnit) bool { return u.cache.alive.Load() })
				if parent == nil {
					return
				}
				u := &modelUnit{cache: tp.newChild(parent.cache), unitRefHeld: true}
				u.demand.Init()
				units = append(units, u)
				check()
			},
			"acquire": func(t *rapid.T) {
				u := pick(t, "acquire-unit", func(u *modelUnit) bool {
					return u.unitRefHeld && u.cache.alive.Load() && !u.running
				})
				if u == nil {
					return
				}
				// A registered demand re-presents its STAMPED weight (weigh-once:
				// the registered weight is stable across retries — a mismatch
				// panics); a fresh attempt draws one.
				var w int
				if u.demand.pool.Load() != nil {
					w = int(u.demand.w) //nolint:gosec // G115: stamped from a small drawn int
				} else {
					w = rapid.IntRange(1, capacity+1).Draw(t, "weight")
				}
				pm, err := u.cache.Acquire(&u.demand, w)
				require.NoError(t, err, "the promise-mode Overdraft never grants or refuses")
				if pm.Held() {
					u.pm = pm
					u.running = true
				} else if b := tp.barrier.Load(); b == nil || b == &u.demand {
					// Unarmed miss, or the head's own gather exhausted: legitimate
					// ONLY when the gather could not assemble w — borrowable
					// everywhere plus free Resource capacity falls short.
					require.Less(t, tp.borrowableTotal()+tp.free(), w,
						"Acquire blocked while gatherable capacity covered w")
				}
				// else: gated by another head's armed barrier — legitimate
				// unconditionally (fairness over utilization, Decision 2).
				check()
			},
			"invalidate": func(t *rapid.T) {
				// Withdraw a demand (postpone dropped / deadline): deregisters a
				// waiting registration (promoting a successor head) and releases the
				// demand's persistent home; the unit may acquire again afterward
				// with a fresh registration.
				u := pick(t, "invalidate-unit", func(u *modelUnit) bool {
					return !u.running && (u.demand.pool.Load() != nil || u.demand.cache.Load() != nil)
				})
				if u == nil {
					return
				}
				u.demand.Invalidate()
				check()
			},
			"release": func(t *rapid.T) {
				u := pick(t, "release-unit", func(u *modelUnit) bool { return u.running })
				if u == nil {
					return
				}
				u.pm.Release()
				u.pm = Permit{}
				u.running = false
				check()
			},
			"exitUnit": func(t *rapid.T) {
				u := pick(t, "exit-unit", func(u *modelUnit) bool {
					return u.unitRefHeld && u.cache.alive.Load() && !u.running
				})
				if u == nil {
					return
				}
				u.demand.Invalidate() // unit exit withdraws its demand and home first
				u.unitRefHeld = false
				u.cache.ReleaseRef()
				check()
			},
		})

		// Teardown: stop every running body, withdraw every demand (registration and
		// persistent home — conservation: satisfied or invalidated, never dropped),
		// then drop every remaining unit reference; the destroy cascade returns all
		// held to the Resource.
		for _, u := range units {
			if u.running {
				u.pm.Release()
				u.pm = Permit{}
				u.running = false
			}
		}
		for _, u := range units {
			u.demand.Invalidate()
		}
		require.Nil(t, tp.barrier.Load(), "an emptied FIFO must disarm the barrier")
		for _, u := range units {
			if u.unitRefHeld {
				u.unitRefHeld = false
				u.cache.ReleaseRef()
			}
		}
		require.NoError(t, checkInvariants(tp.sem, tp.snapshot()))
		require.Equal(t, 0, tp.totalHeld(), "every permit returns to the Resource after a full drain")
		require.Equal(t, int64(0), tp.sem.inFlight.Load(), "the Resource is fully released")
	})
}
