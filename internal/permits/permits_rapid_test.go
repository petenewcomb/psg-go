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
				units = append(units, &modelUnit{cache: tp.NewCache(), unitRefHeld: true})
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
				units = append(units, &modelUnit{cache: tp.newChild(parent.cache), unitRefHeld: true})
				check()
			},
			"acquire": func(t *rapid.T) {
				u := pick(t, "acquire-unit", func(u *modelUnit) bool {
					return u.unitRefHeld && u.cache.alive.Load() && !u.running
				})
				if u == nil {
					return
				}
				w := rapid.IntRange(1, capacity+1).Draw(t, "weight")
				if pm, got := u.cache.Acquire(&u.demand, w); got {
					u.pm = pm
					u.running = true
				} else {
					// Liveness: a block is legitimate ONLY when the gather could not
					// assemble w — borrowable everywhere plus free Resource capacity
					// falls short. (Weight-1 special case: nothing borrowable and the
					// Resource exhausted, as before.)
					require.Less(t, tp.borrowableTotal()+tp.free(), w,
						"Acquire blocked while gatherable capacity covered w")
				}
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
				u.unitRefHeld = false
				u.cache.ReleaseRef()
				check()
			},
		})

		// Teardown: stop every running body, then drop every remaining unit reference;
		// the destroy cascade returns all held to the Resource.
		for _, u := range units {
			if u.running {
				u.pm.Release()
				u.pm = Permit{}
				u.running = false
			}
		}
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
