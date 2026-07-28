// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Single-threaded: each gated transition lands the exact (held, inUse) it should and
// the gates (borrowable for acquireLocal/stealOut, underflow for release) behave.
func TestCountsTransitions(t *testing.T) {
	var c counts
	h, u := c.load()
	require.Equal(t, [2]uint64{0, 0}, [2]uint64{h, u})

	require.False(t, c.acquireLocal(1), "nothing borrowable on an empty cache")

	c.checkout(1) // held=1, inUse=1
	h, u = c.load()
	require.Equal(t, [2]uint64{1, 1}, [2]uint64{h, u})

	require.False(t, c.acquireLocal(1), "no borrowable while inUse == held")
	require.Equal(t, uint64(0), c.stealOutUpTo(1), "no borrowable to steal while inUse == held")

	require.Equal(t, uint64(0), c.release(1), "no excess to return while inUse ≤ held")
	h, u = c.load()
	require.Equal(t, [2]uint64{1, 0}, [2]uint64{h, u}, "cache-don't-return: held stays")

	require.True(t, c.acquireLocal(1), "the cached permit is now borrowable")
	h, u = c.load()
	require.Equal(t, [2]uint64{1, 1}, [2]uint64{h, u})

	require.Equal(t, uint64(0), c.release(1))
	h, u = c.load()
	require.Equal(t, [2]uint64{1, 0}, [2]uint64{h, u})
}

// A release returns exactly the overdraft excess the decrement retired — the delta of
// max(inUse−held, 0) across the transition — and zero when inUse never exceeded held.
func TestCountsReleaseExcessReturn(t *testing.T) {
	var c counts
	c.checkout(1) // 1,1
	c.checkout(1) // 2,2
	require.Equal(t, uint64(0), c.release(1), "2,2 → 2,1: no excess")
	require.Equal(t, uint64(0), c.release(1), "2,1 → 2,0: no excess")

	// Overdraft shape: an exempt occupy pushed inUse past held (held=2, inUse=5 —
	// 3 excess, claimed from a pool allowance by occupyTaking).
	c.acquireLocal(2)
	var allowance atomic.Uint64
	allowance.Store(3)
	require.True(t, c.occupyTaking(3, &allowance), "the allowance covers the shortfall exactly")
	require.Equal(t, uint64(0), allowance.Load(), "the claim debited the allowance")
	h, u := c.load()
	require.Equal(t, [2]uint64{2, 5}, [2]uint64{h, u})
	require.Equal(t, uint64(0), c.stealOutUpTo(1), "a cache in excess has nothing borrowable")

	require.Equal(t, uint64(1), c.release(1), "5→4 over held=2 retires 1 excess")
	require.Equal(t, uint64(2), c.release(2), "4→2 retires the remaining 2 excess")
	require.Equal(t, uint64(0), c.release(2), "2→0 is all under held — no excess")
	h, u = c.load()
	require.Equal(t, [2]uint64{2, 0}, [2]uint64{h, u})
}

// deposit adds borrowable without occupying; stealOutUpTo takes min(borrowable, w) —
// the partial-take pair the multi-source gather is built from.
func TestCountsDepositAndPartialSteal(t *testing.T) {
	var c counts
	c.deposit(4) // held=4, inUse=0: 4 borrowable, none occupied
	h, u := c.load()
	require.Equal(t, [2]uint64{4, 0}, [2]uint64{h, u})

	c.checkout(1) // an occupier arrives: held=5, inUse=1 — borrowable 4
	require.Equal(t, uint64(3), c.stealOutUpTo(3), "full take while enough is borrowable")
	require.Equal(t, uint64(1), c.stealOutUpTo(3), "partial take: only the remainder")
	require.Equal(t, uint64(0), c.stealOutUpTo(3), "nothing borrowable → zero, not a spin")
	h, u = c.load()
	require.Equal(t, [2]uint64{1, 1}, [2]uint64{h, u}, "the occupied permit was never stealable")
}

// Concurrent acquireLocal/release on shared borrowable capacity stays invariant
// (inUse ≤ held always) and returns to the exact starting state. Run under -race.
func TestCountsConcurrentAcquireRelease(t *testing.T) {
	var c counts
	const capacity = 8
	for range capacity {
		c.checkout(1)
	}
	for range capacity {
		c.release(1)
	}
	// held=cap, inUse=0 — cap permits borrowable.

	const workers, iters = 16, 5000
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iters {
				if c.acquireLocal(1) {
					h, u := c.load()
					assert.LessOrEqual(t, u, h, "inUse ≤ held must hold")
					c.release(1)
				}
			}
		}()
	}
	wg.Wait()

	h, u := c.load()
	assert.Equal(t, uint64(capacity), h, "held unchanged by balanced acquire/release")
	assert.Equal(t, uint64(0), u, "every acquire was released")
}

// depositOccupy is atomic take-and-occupy when the deposit completes the weight —
// the no-exposure guarantee the gather's completing tranche needs (a completing
// permit must never transit through a stealable state) — and a plain borrowable
// deposit otherwise.
func TestCountsDepositOccupy(t *testing.T) {
	var c counts
	require.False(t, c.depositOccupy(1, 2), "1 toward 2: deposits borrowable only")
	h, u := c.load()
	require.Equal(t, [2]uint64{1, 0}, [2]uint64{h, u})

	require.True(t, c.depositOccupy(1, 2), "the completing tranche occupies in the same atomic step")
	h, u = c.load()
	require.Equal(t, [2]uint64{2, 2}, [2]uint64{h, u})

	c.release(2)
	require.True(t, c.depositOccupy(1, 3), "own borrowable residue counts toward coverage")
	h, u = c.load()
	require.Equal(t, [2]uint64{3, 3}, [2]uint64{h, u})
}

// Concurrent stealOutUpTo hands out each borrowable permit at most once: with more
// stealers than permits, exactly `held` single-permit takes succeed and the cache
// drains to held=0.
func TestCountsConcurrentStealOnce(t *testing.T) {
	var c counts
	const capacity = 8
	for range capacity {
		c.checkout(1)
	}
	for range capacity {
		c.release(1)
	}
	// held=cap, inUse=0.

	const stealers = 64
	var won atomic.Int64
	var wg sync.WaitGroup
	for range stealers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			won.Add(int64(c.stealOutUpTo(1))) //nolint:gosec // G115: 0 or 1
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(capacity), won.Load(), "exactly `held` permits are stealable, each once")
	h, u := c.load()
	assert.Equal(t, uint64(0), h, "the cache drained")
	assert.Equal(t, uint64(0), u)
}
