// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// makeIdle returns a fresh root cache holding n idle (borrowable) permits: it acquires
// n bodies (each checks out of the Resource, which must have room) then releases them
// all — under cache-don't-return the permits stay in held, now borrowable.
func makeIdle(tp *testPool, n int) *Cache {
	c := tp.NewCache()
	pms := make([]Permit, n)
	var d Demand
	for i := range pms {
		pm, err := c.Acquire(&d, 1)
		require.NoError(tp.tb, err)
		require.True(tp.tb, pm.Held())
		pms[i] = pm
	}
	for _, pm := range pms {
		pm.Release()
	}
	//nolint:gosec // G115: n is a small non-negative test count
	require.Equal(tp.tb, uint64(n), c.held())
	return c
}

// The steal returns the first borrowable cache front-to-back (coldest by touch order)
// and leaves it in place, so a still-borrowable victim stays at the front and is
// re-picked — order-based camping, draining one stable source instead of scattering
// steals. Sequential, so deterministic.
func TestStealCampsOnFrontVictim(t *testing.T) {
	tp := newTestPool(5)
	tp.tb = t

	fat := makeIdle(tp, 3) // first → at the front of roots
	_ = makeIdle(tp, 1)
	_ = makeIdle(tp, 1)
	require.Equal(t, 5, tp.totalHeld())

	// Each search returns the same front victim; draining it (stealOutUpTo, which does
	// not touch) leaves it at the front while it stays borrowable, so it is re-picked.
	for range 3 {
		v := searchList(&tp.roots, 1, nil) // ref-pinned; release after
		require.Same(t, fat, v, "the steal camps on the coldest front victim")
		require.Equal(t, uint64(1), v.counts.stealOutUpTo(1))
		v.ReleaseRef()
	}
	// Exhausted now (held 0): the search abandons it for the next source.
	v := searchList(&tp.roots, 1, nil)
	require.NotSame(t, fat, v, "an exhausted victim is skipped")
	require.NotNil(t, v)
	v.ReleaseRef()
	// (No conservation check: bare stealOut without a destination checkout deliberately
	// unbalances Σheld; the rapid/concurrent tests cover conservation via Acquire.)
}

// touch (move-to-back) redirects the steal: a cache marked hot moves to the back of
// its sibling list, so the steal then prefers the now-coldest one at the front. This
// is the LRU telemetry the steal rests on.
func TestTouchRedirectsSteal(t *testing.T) {
	tp := newTestPool(4)
	tp.tb = t

	x := makeIdle(tp, 1) // roots front-to-back: [x, y]
	y := makeIdle(tp, 1)

	v := searchList(&tp.roots, 1, nil) // ref-pinned; release after
	require.Same(t, x, v, "x is the coldest (front) victim")
	v.ReleaseRef()

	x.touch() // x became active → moves to the back

	require.Equal(t, []*Cache{y, x}, listSlice(&tp.roots), "touch moved x to the back")
	v = searchList(&tp.roots, 1, nil)
	require.Same(t, y, v, "the steal now prefers y, the coldest")
	v.ReleaseRef()
}

// An unsatisfied acquire up-walk touches the ancestors it passes (move-to-back),
// driving the same redirection through the public API rather than touch() directly.
func TestAcquireUpWalkTouchesUnsatisfiedAncestors(t *testing.T) {
	tp := newTestPool(2)
	tp.tb = t

	// Two roots so roots has order we can observe; r0 will be driven hot.
	r0 := tp.NewCache()
	r1 := tp.NewCache()
	require.Equal(t, []*Cache{r0, r1}, listSlice(&tp.roots))

	// r0 runs a body and stays running (held=1, inUse=1 → no idle to lend).
	var d0, dc Demand
	p0, err := r0.Acquire(&d0, 1)
	require.NoError(t, err)
	require.True(t, p0.Held())

	// A child of r0 acquires: step-1 (own) misses (held 0), step-2 at r0 misses
	// (inUse==held), so the up-walk passes r0 unsatisfied → r0.touch() moves it to the
	// back of roots. (The acquire then checks out the Resource's last permit.)
	child := r0.NewChild()
	pc, err := child.Acquire(&dc, 1)
	require.NoError(t, err)
	require.True(t, pc.Held())

	require.Equal(t, []*Cache{r1, r0}, listSlice(&tp.roots),
		"the unsatisfied up-walk through r0 moved it to the back")

	pc.Release()
	p0.Release()
}
