// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A top-level unit checks a permit out of the Resource, runs, releases (the permit
// stays cached), and on the unit's last reference returns it to the Resource.
func TestRootUnitLifecycle(t *testing.T) {
	tp := newTestPool(1)
	c := tp.NewCache()

	d := NewDemand()
	pm, err := c.Acquire(d, 1)
	require.NoError(t, err)
	require.True(t, pm.Held())
	require.Same(t, c, pm.backing, "a top-level acquire backs from the unit's own cache")
	assert.Equal(t, 1, tp.totalHeld())
	tp.check(t)

	pm.Release() // body completes; permit stays cached (held=1, inUse=0)
	assert.Equal(t, 1, tp.totalHeld(), "cache-don't-return: release does not give the permit back")
	tp.check(t)

	c.ReleaseRef()
	assert.Equal(t, 0, tp.totalHeld(), "destruction returns the permit to the Resource")
	tp.check(t)
}

// THE canonical hang scenario, dissolved. Under a capacity-1 Resource a parent body
// parks to drive a sub-wave; the sub-wave's body must run, but the only permit is the
// parent's. The cache lets the child INHERIT the parked parent's idle permit — no
// deadlock, no second checkout.
func TestParkedParentLendsToSubwave(t *testing.T) {
	tp := newTestPool(1)
	parent := tp.NewCache()

	dp := NewDemand()

	dc := NewDemand()
	pp, _ := parent.Acquire(dp, 1) // parent runs: held=1, inUse=1
	tp.check(t)

	pp.Release() // parent parks to drive a sub-wave: inUse=0, borrowable=1
	sub := tp.newChild(parent)

	cp, err := sub.Acquire(dc, 1) // step-2 ancestor inherit
	require.NoError(t, err)
	require.True(t, cp.Held(), "child must inherit the parked parent's idle permit, not deadlock")
	require.Same(t, parent, cp.backing, "the child is backed by the parent's permit, unmoved")
	assert.Equal(t, 1, tp.totalHeld(), "no second permit is checked out")
	tp.check(t)

	cp.Release()                       // child body completes
	sub.ReleaseRef()                   // sub-wave drains; its cache is destroyed
	assert.Equal(t, 1, tp.totalHeld()) // parent still holds its cached permit
	tp.check(t)

	pp2, err := parent.Acquire(dp, 1) // parent resumes (reacquire): step-1 own-cache hit
	require.NoError(t, err)
	require.True(t, pp2.Held())
	require.Same(t, parent, pp2.backing)
	pp2.Release()
	parent.ReleaseRef()
	assert.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}

// Parallel sub-wave bodies contend for the parent's cache like a semaphore of size
// held: the first inherits the parent's idle permit; the second takes a delta from
// the Resource; a third blocks once capacity is exhausted — legitimate only because
// nothing is borrowable.
func TestParallelChildrenTakeDeltaThenBlock(t *testing.T) {
	tp := newTestPool(2)
	parent := tp.NewCache()
	dp := NewDemand()
	d1 := NewDemand()
	d2 := NewDemand()
	d3 := NewDemand()
	pp, _ := parent.Acquire(dp, 1)
	pp.Release() // parent parks: borrowable=1
	sub := tp.newChild(parent)

	c1, err1 := sub.Acquire(d1, 1) // step 2: inherit parent's permit
	require.NoError(t, err1)
	require.True(t, c1.Held())
	require.Same(t, parent, c1.backing)

	c2, err2 := sub.Acquire(d2, 1) // ancestors exhausted → step 3: delta from the Resource
	require.NoError(t, err2)
	require.True(t, c2.Held())
	require.Same(t, sub, c2.backing, "the second concurrent child takes a delta into its own cache")
	assert.Equal(t, 2, tp.totalHeld())
	tp.check(t)

	c3, err3 := sub.Acquire(d3, 1) // capacity exhausted, both in use
	require.NoError(t, err3)
	require.False(t, c3.Held(), "a third concurrent child must block")
	assert.False(t, tp.hasBorrowable(), "blocking is legitimate: nothing is borrowable")

	c1.Release()
	c2.Release()
	d3.Invalidate() // the miss registered d3, homing it under sub — withdraw before drain
	sub.ReleaseRef()
	parent.ReleaseRef()
	assert.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}

// Cross-wave steal with no coordinator: two independent top-level waves share one
// Pool. Wave A holds an idle permit; wave B, finding the Resource exhausted, steals
// it — a transfer, not a new checkout — by a forest walk.
func TestCrossWaveSteal(t *testing.T) {
	tp := newTestPool(1)
	a := tp.NewCache()
	da := NewDemand()
	db := NewDemand()
	ap, _ := a.Acquire(da, 1)
	ap.Release() // wave A parked-idle: a.held=1, borrowable=1
	tp.check(t)

	b := tp.NewCache()
	bp, err := b.Acquire(db, 1) // own/ancestor miss, Resource full → step 4 steals from A
	require.NoError(t, err)
	require.True(t, bp.Held(), "B steals A's idle permit rather than deadlocking")
	require.Same(t, b, bp.backing)
	assert.Equal(t, 1, tp.totalHeld(), "a steal is a transfer, not a new checkout")
	assert.Equal(t, uint64(0), a.held(), "the victim simply loses the cached permit")
	assert.Equal(t, uint64(1), b.held())
	tp.check(t)

	bp.Release()
	b.ReleaseRef()
	a.ReleaseRef()
	assert.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}

// Nested driving: a single capacity-1 permit serves a three-level synchronous chain
// (parent → child → grandchild), because only one body computes at a time along it.
func TestNestedDriveSinglePermitChain(t *testing.T) {
	tp := newTestPool(1)
	parent := tp.NewCache()
	dp := NewDemand()
	dc := NewDemand()
	dg := NewDemand()
	pp, _ := parent.Acquire(dp, 1)
	pp.Release() // parent parks
	child := tp.newChild(parent)

	cp, err1 := child.Acquire(dc, 1) // inherit parent's permit
	require.NoError(t, err1)
	require.True(t, cp.Held())
	cp.Release() // child parks to drive its own sub-wave
	grand := tp.newChild(child)

	gp, err2 := grand.Acquire(dg, 1) // walk grand → child(held=0) → parent(borrowable=1): inherit
	require.NoError(t, err2)
	require.True(t, gp.Held())
	require.Same(t, parent, gp.backing, "the grandchild reaches the parent's permit up the chain")
	assert.Equal(t, 1, tp.totalHeld())
	tp.check(t)

	gp.Release()
	grand.ReleaseRef()
	child.ReleaseRef()
	parent.ReleaseRef()
	assert.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}

// A weighted acquire assembles its weight from fragmented sources — partial steals
// from several victims plus the Resource's remainder — into the demand's body cache
// (registration creates it; the gather hoards and occupies there,
// weighted-acquisition.md Decisions 1–2). No single source covers w=5, so the fast
// path misses, the demand registers, becomes the instant head, gathers, and the
// satisfaction retires the registration — all within one Acquire call.
func TestWeightedGatherAssemblesFromFragments(t *testing.T) {
	tp := newTestPool(5)
	tp.tb = t
	v1 := makeIdle(tp, 2) // fragmented idle: 2 + 2 cached, 1 free in the Resource
	v2 := makeIdle(tp, 2)

	g := tp.NewCache()
	d := NewDemand()
	pm, err := g.Acquire(d, 5)
	require.NoError(t, err)
	require.True(t, pm.Held(), "w=5 must assemble from 2+2 stolen plus 1 free")
	require.Same(t, d.cache.Load(), pm.backing, "a registered demand backs from its body cache")
	require.Same(t, g, pm.backing.parent, "the body cache is homed under the registering cache")
	assert.Equal(t, uint64(5), pm.backing.held(), "the whole weight landed in the body cache")
	assert.Equal(t, uint64(0), v1.held(), "victim 1 fully harvested")
	assert.Equal(t, uint64(0), v2.held(), "victim 2 fully harvested")
	assert.Nil(t, tp.head(), "satisfaction retired the head")
	assert.Equal(t, 5, tp.totalHeld(), "steals transfer and the delta checks out — no double-count")
	tp.check(t)

	pm.Release()
	d.Invalidate() // releases the demand's persistent home
	g.ReleaseRef()
	v1.ReleaseRef()
	v2.ReleaseRef()
	assert.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}

// A gather that comes up short keeps its partial hoard in the demand's body cache —
// cache-don't-return IS the rollback (no give-back protocol). The demand stands as
// the effective head, gating other acquirers off the hoard; invalidating it drains
// the hoard back to the Resource and unlinks the registration immediately.
func TestWeightedGatherMissRetainsHoardUntilInvalidated(t *testing.T) {
	tp := newTestPool(3)
	tp.tb = t
	v := makeIdle(tp, 2) // 2 cached idle + 1 free = 3 total < 4

	g := tp.NewCache()
	dg := NewDemand()
	gm, err := g.Acquire(dg, 4)
	require.NoError(t, err, "the 'not now' Overdraft waits rather than granting or refusing")
	require.False(t, gm.Held(), "w=4 cannot be covered by capacity 3")
	// The hoard holds the stolen 2 in the body cache. The 1 free permit stays in the
	// Resource: the Resource arm is all-or-nothing at the shortfall until
	// TryAcquireUpTo lands (weighted-acquisition.md sequencing step 3).
	require.NotNil(t, dg.cache.Load(), "the miss left the demand registered with its body cache")
	assert.Equal(t, uint64(2), dg.cache.Load().held(), "the failed gather keeps its partial hoard")
	assert.Equal(t, uint64(0), v.held(), "the victim was harvested before the miss")
	require.Same(t, dg, tp.head(), "the unsatisfied head stands")
	tp.check(t)

	// While a head stands, everyone else is gated — even off capacity the head
	// cannot use — and joins the queue in arrival order, weight-1 included.
	b := tp.NewCache()
	db1 := NewDemand()
	b1, err1 := b.Acquire(db1, 1)
	require.NoError(t, err1)
	require.False(t, b1.Held(), "a weight-1 acquire is gated while a head stands")
	require.True(t, db1.queued(), "and queues behind it")

	// Invalidate the queued weight-1 demand FIRST — an immediate unlink that
	// leaves the head standing — then the head, which empties the queue.
	db1.Invalidate()
	dg.Invalidate()
	require.Nil(t, tp.head(), "invalidation unlinked both registrations")
	assert.Equal(t, 0, tp.totalHeld(), "the drained hoard returned everything to the Resource")
	tp.check(t)

	db := NewDemand()
	bp, err2 := b.Acquire(db, 3)
	require.NoError(t, err2)
	require.True(t, bp.Held(), "the drained capacity is available again (whole-grant fast path)")
	tp.check(t)

	bp.Release()
	db.Invalidate()
	b.ReleaseRef()
	g.ReleaseRef()
	v.ReleaseRef()
	assert.Equal(t, 0, tp.totalHeld(), "hoards drain to the Resource like any cached permits")
	tp.check(t)
}

// NewDemand/Free round-trip: Free retires the identity through Reset →
// Invalidate — deregistering, releasing the home — so recycled objects come back
// clean.
func TestDemandPoolRoundTrip(t *testing.T) {
	tp := newTestPool(1)
	c := tp.NewCache()
	for range 3 { // recycle through the pool across iterations
		hogD := NewDemand()
		hog, err := c.Acquire(hogD, 1)
		require.NoError(t, err)
		require.True(t, hog.Held())

		d := NewDemand()
		p2, err := c.Acquire(d, 2) // over capacity: registers and takes the headship
		require.NoError(t, err)
		require.False(t, p2.Held())
		require.Same(t, d, tp.head(), "the pooled demand stands as head")

		d.Free() // Reset → Invalidate: unlinked, identity bumped
		require.Nil(t, tp.head())

		hog.Release()
		hogD.Free()
	}
	c.ReleaseRef()
	require.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}
