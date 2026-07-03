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
// backing its body while it runs. A unit models one body: it runs (acquire),
// parks/completes (release), may resume, and finally exits (ReleaseRef).
type modelUnit struct {
	cache       *Cache
	unitRefHeld bool
	pm          Permit
	running     bool
}

// TestPermitsModel model-checks the permit invariants (permit-core.md "Invariants")
// sequentially — the algorithm, not the concurrency (that is the -race stress tests):
// random forests of caches on a shared Pool, random acquire/park/resume/exit. After
// every operation it asserts the invariants; on every blocked Acquire the liveness
// property (nothing borrowable, Resource exhausted); and after a full drain, no leak.
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
				if pm, got := u.cache.Acquire(1); got {
					u.pm = pm
					u.running = true
				} else {
					// Liveness: a block is legitimate ONLY when no permit is borrowable
					// and the Resource is exhausted.
					require.False(t, tp.hasBorrowable(),
						"Acquire blocked while a permit was borrowable")
					require.Equal(t, int64(tp.sem.capacity), tp.sem.inFlight.Load(),
						"Acquire blocked while Resource capacity remained")
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
