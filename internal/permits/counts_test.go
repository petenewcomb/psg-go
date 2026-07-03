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
	require.False(t, c.stealOut(1), "no borrowable to steal while inUse == held")

	require.True(t, c.release(1), "release of the last in-use permit raises borrowable 0→1")
	h, u = c.load()
	require.Equal(t, [2]uint64{1, 0}, [2]uint64{h, u}, "cache-don't-return: held stays")

	require.True(t, c.acquireLocal(1), "the cached permit is now borrowable")
	h, u = c.load()
	require.Equal(t, [2]uint64{1, 1}, [2]uint64{h, u})

	require.True(t, c.release(1), "1,1 → 1,0 crosses borrowable 0→1 again")
	h, u = c.load()
	require.Equal(t, [2]uint64{1, 0}, [2]uint64{h, u})
}

// A release raises borrowable only when it crosses 0→1 (inUse was == held). With
// held > inUse already, a release does not newly free capacity for a waiter.
func TestCountsReleaseWakeSignal(t *testing.T) {
	var c counts
	c.checkout(1) // 1,1
	c.checkout(1) // 2,2
	require.True(t, c.release(1), "2,2 → 2,1 crosses borrowable 0→1")
	require.False(t, c.release(1), "2,1 → 2,0 was already borrowable, no new crossing")
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

// Concurrent stealOut hands out each borrowable permit at most once: with more
// stealers than permits, exactly `held` succeed and the cache drains to held=0.
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
			if c.stealOut(1) {
				won.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(capacity), won.Load(), "exactly `held` permits are stealable, each once")
	h, u := c.load()
	assert.Equal(t, uint64(0), h, "the cache drained")
	assert.Equal(t, uint64(0), u)
}
