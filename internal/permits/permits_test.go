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

	var d Demand
	pm, got := c.Acquire(&d, 1)
	require.True(t, got)
	require.Same(t, c, pm.backing, "a top-level acquire backs from the unit's own cache")
	assert.Equal(t, 1, tp.totalHeld())
	tp.check(t)

	pm.Release() // body completes; permit stays cached (held=1, inUse=0)
	assert.Equal(t, 1, tp.totalHeld(), "cache-don't-return: release does not give the permit back")
	tp.check(t)

	require.True(t, c.ReleaseRef(), "the unit's last reference destroys the cache")
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

	var dp, dc Demand
	pp, _ := parent.Acquire(&dp, 1) // parent runs: held=1, inUse=1
	tp.check(t)

	pp.Release() // parent parks to drive a sub-wave: inUse=0, borrowable=1
	sub := tp.newChild(parent)

	cp, got := sub.Acquire(&dc, 1) // the old livelock — now a step-2 ancestor inherit
	require.True(t, got, "child must inherit the parked parent's idle permit, not deadlock")
	require.Same(t, parent, cp.backing, "the child is backed by the parent's permit, unmoved")
	assert.Equal(t, 1, tp.totalHeld(), "no second permit is checked out")
	tp.check(t)

	cp.Release()                       // child body completes
	require.True(t, sub.ReleaseRef())  // sub-wave drains; its cache is destroyed
	assert.Equal(t, 1, tp.totalHeld()) // parent still holds its cached permit
	tp.check(t)

	pp2, got := parent.Acquire(&dp, 1) // parent resumes (reacquire): step-1 own-cache hit
	require.True(t, got)
	require.Same(t, parent, pp2.backing)
	pp2.Release()
	require.True(t, parent.ReleaseRef())
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
	var dp, d1, d2, d3 Demand
	pp, _ := parent.Acquire(&dp, 1)
	pp.Release() // parent parks: borrowable=1
	sub := tp.newChild(parent)

	c1, ok1 := sub.Acquire(&d1, 1) // step 2: inherit parent's permit
	require.True(t, ok1)
	require.Same(t, parent, c1.backing)

	c2, ok2 := sub.Acquire(&d2, 1) // ancestors exhausted → step 3: delta from the Resource
	require.True(t, ok2)
	require.Same(t, sub, c2.backing, "the second concurrent child takes a delta into its own cache")
	assert.Equal(t, 2, tp.totalHeld())
	tp.check(t)

	_, ok3 := sub.Acquire(&d3, 1) // capacity exhausted, both in use
	require.False(t, ok3, "a third concurrent child must block")
	assert.False(t, tp.hasBorrowable(), "blocking is legitimate: nothing is borrowable")

	c1.Release()
	c2.Release()
	require.True(t, sub.ReleaseRef())
	require.True(t, parent.ReleaseRef())
	assert.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}

// Cross-wave steal with no coordinator: two independent top-level waves share one
// Pool. Wave A holds an idle permit; wave B, finding the Resource exhausted, steals
// it — a transfer, not a new checkout — by a forest walk.
func TestCrossWaveSteal(t *testing.T) {
	tp := newTestPool(1)
	a := tp.NewCache()
	var da, db Demand
	ap, _ := a.Acquire(&da, 1)
	ap.Release() // wave A parked-idle: a.held=1, borrowable=1
	tp.check(t)

	b := tp.NewCache()
	bp, got := b.Acquire(&db, 1) // own/ancestor miss, Resource full → step 4 steals from A
	require.True(t, got, "B steals A's idle permit rather than deadlocking")
	require.Same(t, b, bp.backing)
	assert.Equal(t, 1, tp.totalHeld(), "a steal is a transfer, not a new checkout")
	assert.Equal(t, uint64(0), a.held(), "the victim simply loses the cached permit")
	assert.Equal(t, uint64(1), b.held())
	tp.check(t)

	bp.Release()
	require.True(t, b.ReleaseRef())
	require.True(t, a.ReleaseRef())
	assert.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}

// Nested driving: a single capacity-1 permit serves a three-level synchronous chain
// (parent → child → grandchild), because only one body computes at a time along it.
func TestNestedDriveSinglePermitChain(t *testing.T) {
	tp := newTestPool(1)
	parent := tp.NewCache()
	var dp, dc, dg Demand
	pp, _ := parent.Acquire(&dp, 1)
	pp.Release() // parent parks
	child := tp.newChild(parent)

	cp, ok1 := child.Acquire(&dc, 1) // inherit parent's permit
	require.True(t, ok1)
	cp.Release() // child parks to drive its own sub-wave
	grand := tp.newChild(child)

	gp, ok2 := grand.Acquire(&dg, 1) // walk grand → child(held=0) → parent(borrowable=1): inherit
	require.True(t, ok2)
	require.Same(t, parent, gp.backing, "the grandchild reaches the parent's permit up the chain")
	assert.Equal(t, 1, tp.totalHeld())
	tp.check(t)

	gp.Release()
	require.True(t, grand.ReleaseRef())
	require.True(t, child.ReleaseRef())
	require.True(t, parent.ReleaseRef())
	assert.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}

// A weighted acquire assembles its weight from fragmented sources — partial steals
// from several victims plus the Resource's remainder — into the acquirer's own held,
// then occupies atomically (weighted-acquisition.md Decision 1: gather into your own
// held). No single source covers w=5, so the pre-gather single-source path would
// have blocked here.
func TestWeightedGatherAssemblesFromFragments(t *testing.T) {
	tp := newTestPool(5)
	tp.tb = t
	v1 := makeIdle(tp, 2) // fragmented idle: 2 + 2 cached, 1 free in the Resource
	v2 := makeIdle(tp, 2)

	g := tp.NewCache()
	var d Demand
	pm, ok := g.Acquire(&d, 5)
	require.True(t, ok, "w=5 must assemble from 2+2 stolen plus 1 free")
	require.Same(t, g, pm.backing, "the gather lands the whole weight in the acquirer's own cache")
	assert.Equal(t, uint64(0), v1.held(), "victim 1 fully harvested")
	assert.Equal(t, uint64(0), v2.held(), "victim 2 fully harvested")
	assert.Equal(t, 5, tp.totalHeld(), "steals transfer and the delta checks out — no double-count")
	tp.check(t)

	pm.Release()
	require.True(t, g.ReleaseRef())
	require.True(t, v1.ReleaseRef())
	require.True(t, v2.ReleaseRef())
	assert.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}

// A gather that comes up short keeps its partial hoard cached and borrowable —
// cache-don't-return IS the rollback (no give-back protocol) — and the hoard is
// ordinary steal fodder for anyone else meanwhile.
func TestWeightedGatherMissRetainsBorrowableHoard(t *testing.T) {
	tp := newTestPool(3)
	tp.tb = t
	v := makeIdle(tp, 2) // 2 cached idle + 1 free = 3 total < 4

	g := tp.NewCache()
	var dg Demand
	_, ok := g.Acquire(&dg, 4)
	require.False(t, ok, "w=4 cannot be covered by capacity 3")
	// The hoard holds the stolen 2. The 1 free permit stays in the Resource: the
	// Resource arm is all-or-nothing at the shortfall until TryAcquireUpTo lands
	// (weighted-acquisition.md sequencing step 3).
	assert.Equal(t, uint64(2), g.held(), "the failed gather keeps its partial hoard")
	assert.Equal(t, uint64(0), v.held(), "the victim was harvested before the miss")
	tp.check(t)

	// The hoard is ordinary borrowable capacity: an unrelated wave assembles its own
	// weight from it (plus the free permit) — the abandoned gather blocks no one.
	b := tp.NewCache()
	var db Demand
	bp, ok := b.Acquire(&db, 3)
	require.True(t, ok, "the abandoned hoard is steal-recoverable by others")
	assert.Equal(t, 3, tp.totalHeld())
	tp.check(t)

	bp.Release()
	require.True(t, b.ReleaseRef())
	require.True(t, g.ReleaseRef())
	require.True(t, v.ReleaseRef())
	assert.Equal(t, 0, tp.totalHeld(), "hoards drain to the Resource like any cached permits")
	tp.check(t)
}
