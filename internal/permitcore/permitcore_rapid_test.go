// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permitcore

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// maxPools bounds the forest so the reachable state space stays finite under
// adversarial generation.
const maxPools = 10

// modelUnit tracks the usage state the real Pool does not: whether the owning unit's
// own reference is still held (it must be ReleaseRef'd exactly once), and the pool
// backing its body while it runs (nil while not running). A unit models one body: it
// runs (acquire), parks/completes (release), may resume (acquire again), and finally
// exits (ReleaseRef). Everything else — alive, inUse, children, refs — is read from
// the real pool, the source of truth (white-box test).
type modelUnit struct {
	pool        *Pool
	unitRefHeld bool
	body        *Pool // backing pool while the unit's body runs; nil otherwise
}

// TestPermitCoreModel model-checks the permit-core invariants (permit-core.md
// "Invariants") under adversarial nesting and cross-wave limiter sharing: random
// forests of pools on a shared store, random acquire/park/resume/exit. After every
// operation it asserts CheckInvariants; on every blocked Acquire it asserts the
// liveness property (nothing borrowable, free L exhausted); and after a full drain it
// asserts every permit returned to L (no leak).
func TestPermitCoreModel(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		capacity := rapid.IntRange(1, 4).Draw(t, "capacity")
		s := NewStore(capacity)

		var units []*modelUnit

		check := func() { require.NoError(t, s.CheckInvariants()) }

		// pick draws a random unit satisfying want, or nil if none qualify.
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
			// A new top-level wave/unit: a root pool drawing on free L.
			"newRoot": func(t *rapid.T) {
				if len(units) >= maxPools {
					return
				}
				units = append(units, &modelUnit{pool: s.NewRootPool(), unitRefHeld: true})
				check()
			},
			// A sub-wave drawing on an ancestor: a child of any alive pool.
			"newChild": func(t *rapid.T) {
				if len(units) >= maxPools {
					return
				}
				parent := pick(t, "child-parent", func(u *modelUnit) bool { return u.pool.alive })
				if parent == nil {
					return
				}
				units = append(units, &modelUnit{pool: parent.pool.NewChildPool(), unitRefHeld: true})
				check()
			},
			// A live, not-currently-running unit's body starts (or resumes): the
			// locality-ordered acquire.
			"acquire": func(t *rapid.T) {
				u := pick(t, "acquire-unit", func(u *modelUnit) bool {
					return u.unitRefHeld && u.pool.alive && u.body == nil
				})
				if u == nil {
					return
				}
				if b, ok := u.pool.Acquire(); ok {
					u.body = b
				} else {
					// Liveness: a block is legitimate ONLY when no permit is
					// borrowable anywhere and free L is exhausted. If either held,
					// the search should have succeeded — a deadlock-freedom bug.
					require.False(t, s.HasBorrowable(),
						"Acquire blocked while a permit was borrowable")
					require.Equal(t, s.capacity, s.checkedOut,
						"Acquire blocked while free L capacity remained")
				}
				check()
			},
			// A running body completes or parks-to-drive: release its backing permit
			// (it stays cached). Both are modeled uniformly; a later acquire is a
			// resume.
			"release": func(t *rapid.T) {
				u := pick(t, "release-unit", func(u *modelUnit) bool { return u.body != nil })
				if u == nil {
					return
				}
				u.body.Release()
				u.body = nil
				check()
			},
			// The unit exits, dropping its own reference. Valid only when its body is
			// not running (released). The pool may outlive this via live sub-waves
			// (refs stay > 0); the destroy cascade fires when the last drains.
			"exitUnit": func(t *rapid.T) {
				u := pick(t, "exit-unit", func(u *modelUnit) bool {
					return u.unitRefHeld && u.pool.alive && u.body == nil
				})
				if u == nil {
					return
				}
				u.unitRefHeld = false
				u.pool.ReleaseRef()
				check()
			},
		})

		// Teardown: drain everything and assert no permit leaked. Stop every running
		// body (inUse → 0 everywhere), then drop every remaining unit reference; the
		// destroy cascade returns all held to L.
		for _, u := range units {
			if u.body != nil {
				u.body.Release()
				u.body = nil
			}
		}
		for _, u := range units {
			if u.unitRefHeld {
				u.unitRefHeld = false
				u.pool.ReleaseRef()
			}
		}
		require.NoError(t, s.CheckInvariants())
		require.Equal(t, 0, s.checkedOut, "every permit returns to L after a full drain (no leak)")
		require.Empty(t, s.roots, "every pool is destroyed after a full drain")
	})
}
