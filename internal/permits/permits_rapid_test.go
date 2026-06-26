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
// backing its body while it runs (zero while not running). A unit models one body: it
// runs (acquire), parks/completes (release), may resume (acquire again), and finally
// exits (ReleaseRef). Everything else is read from the real cache, the source of
// truth (white-box test).
type modelUnit struct {
	cache       *Cache
	unitRefHeld bool
	pm          Permit
	running     bool
}

// TestPermitsModel model-checks the permit invariants (permit-core.md "Invariants")
// under adversarial nesting and cross-wave Resource sharing: random forests of caches
// on a shared Pool, random acquire/park/resume/exit. After every operation it asserts
// CheckInvariants; on every blocked Acquire it asserts the liveness property (nothing
// borrowable, Resource exhausted); and after a full drain it asserts every permit
// returned to the Resource (no leak).
func TestPermitsModel(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		capacity := rapid.IntRange(1, 4).Draw(t, "capacity")
		sem := &semaphore{capacity: capacity}
		p := NewPool(sem)

		var units []*modelUnit

		check := func() { require.NoError(t, p.CheckInvariants()) }

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
			// A new top-level wave/unit: a root cache drawing on the Pool.
			"newRoot": func(t *rapid.T) {
				if len(units) >= maxCaches {
					return
				}
				units = append(units, &modelUnit{cache: p.NewCache(), unitRefHeld: true})
				check()
			},
			// A sub-wave drawing on an ancestor: a child of any alive cache.
			"newChild": func(t *rapid.T) {
				if len(units) >= maxCaches {
					return
				}
				parent := pick(t, "child-parent", func(u *modelUnit) bool { return u.cache.alive })
				if parent == nil {
					return
				}
				units = append(units, &modelUnit{cache: parent.cache.NewChild(), unitRefHeld: true})
				check()
			},
			// A live, not-currently-running unit's body starts (or resumes).
			"acquire": func(t *rapid.T) {
				u := pick(t, "acquire-unit", func(u *modelUnit) bool {
					return u.unitRefHeld && u.cache.alive && !u.running
				})
				if u == nil {
					return
				}
				if pm, got := u.cache.Acquire(); got {
					u.pm = pm
					u.running = true
				} else {
					// Liveness: a block is legitimate ONLY when no permit is borrowable
					// anywhere and the Resource is exhausted. If either held, the search
					// should have succeeded — a deadlock-freedom bug.
					require.False(t, p.HasBorrowable(),
						"Acquire blocked while a permit was borrowable")
					require.Equal(t, int64(sem.capacity), sem.inFlight.Load(),
						"Acquire blocked while Resource capacity remained")
				}
				check()
			},
			// A running body completes or parks-to-drive: release its permit (it stays
			// cached). Both are modeled uniformly; a later acquire is a resume.
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
			// The unit exits, dropping its own reference. Valid only when its body is
			// not running. The cache may outlive this via live sub-waves (refs stay
			// > 0); the destroy cascade fires when the last drains.
			"exitUnit": func(t *rapid.T) {
				u := pick(t, "exit-unit", func(u *modelUnit) bool {
					return u.unitRefHeld && u.cache.alive && !u.running
				})
				if u == nil {
					return
				}
				u.unitRefHeld = false
				u.cache.ReleaseRef()
				check()
			},
		})

		// Teardown: drain everything and assert no permit leaked. Stop every running
		// body (inUse → 0 everywhere), then drop every remaining unit reference; the
		// destroy cascade returns all held to the Resource.
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
		require.NoError(t, p.CheckInvariants())
		require.Equal(t, 0, p.totalHeld(), "every permit returns to the Resource after a full drain (no leak)")
		require.Equal(t, int64(0), sem.inFlight.Load(), "the Resource is fully released after a full drain")
		require.Empty(t, p.roots, "every cache is destroyed after a full drain")
	})
}
