// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ok runs CheckInvariants and fails the test on any violation.
func ok(t *testing.T, p *Pool) {
	t.Helper()
	require.NoError(t, p.CheckInvariants())
}

func newPool(capacity int) *Pool { return NewPool(&semaphore{capacity: capacity}) }

// A top-level unit checks a permit out of the Resource, runs, releases (the permit
// stays cached), and on the unit's last reference returns it to the Resource.
func TestRootUnitLifecycle(t *testing.T) {
	p := newPool(1)
	c := p.NewCache()

	pm, got := c.Acquire()
	require.True(t, got)
	require.Same(t, c, pm.backing, "a top-level acquire backs from the unit's own cache")
	assert.Equal(t, 1, p.totalHeld())
	ok(t, p)

	pm.Release() // body completes; permit stays cached (held=1, inUse=0)
	assert.Equal(t, 1, p.totalHeld(), "cache-don't-return: release does not give the permit back")
	ok(t, p)

	require.True(t, c.ReleaseRef(), "the unit's last reference destroys the cache")
	assert.Equal(t, 0, p.totalHeld(), "destruction returns the permit to the Resource")
	ok(t, p)
}

// THE canonical hang scenario, dissolved. Under a capacity-1 Resource a parent body
// parks to drive a sub-wave; the sub-wave's body must run, but the only permit is the
// parent's. Eager-suspend gave the permit back; the cache lets the child INHERIT the
// parked parent's idle permit — no deadlock, no second checkout.
func TestParkedParentLendsToSubwave(t *testing.T) {
	p := newPool(1)
	parent := p.NewCache()

	pp, _ := parent.Acquire() // parent runs: held=1, inUse=1
	ok(t, p)

	pp.Release() // parent parks to drive a sub-wave: inUse=0, borrowable=1
	sub := parent.NewChild()

	cp, got := sub.Acquire() // the old livelock — now a step-2 ancestor inherit
	require.True(t, got, "child must inherit the parked parent's idle permit, not deadlock")
	require.Same(t, parent, cp.backing, "the child is backed by the parent's permit, unmoved")
	assert.Equal(t, 1, p.totalHeld(), "no second permit is checked out")
	ok(t, p)

	cp.Release()                      // child body completes
	require.True(t, sub.ReleaseRef()) // sub-wave drains; its cache is destroyed
	assert.Equal(t, 1, p.totalHeld()) // parent still holds its cached permit
	ok(t, p)

	pp2, got := parent.Acquire() // parent resumes (reacquire): step-1 own-cache hit
	require.True(t, got)
	require.Same(t, parent, pp2.backing)
	pp2.Release()
	require.True(t, parent.ReleaseRef())
	assert.Equal(t, 0, p.totalHeld())
	ok(t, p)
}

// Parallel sub-wave bodies contend for the parent's cache like a semaphore of size
// held: the first inherits the parent's idle permit; the second takes a delta from
// the Resource into its own cache; a third blocks once capacity is exhausted — and
// blocking is legitimate only because nothing is borrowable.
func TestParallelChildrenTakeDeltaThenBlock(t *testing.T) {
	p := newPool(2)
	parent := p.NewCache()
	pp, _ := parent.Acquire()
	pp.Release() // parent parks: borrowable=1
	sub := parent.NewChild()

	c1, ok1 := sub.Acquire() // step 2: inherit parent's permit
	require.True(t, ok1)
	require.Same(t, parent, c1.backing)

	c2, ok2 := sub.Acquire() // ancestors exhausted → step 3: delta from the Resource
	require.True(t, ok2)
	require.Same(t, sub, c2.backing, "the second concurrent child takes a delta into its own cache")
	assert.Equal(t, 2, p.totalHeld())
	ok(t, p)

	_, ok3 := sub.Acquire() // capacity exhausted, both in use
	require.False(t, ok3, "a third concurrent child must block")
	assert.False(t, p.HasBorrowable(), "blocking is legitimate: nothing is borrowable")

	c1.Release()
	c2.Release()
	require.True(t, sub.ReleaseRef())
	require.True(t, parent.ReleaseRef())
	assert.Equal(t, 0, p.totalHeld())
	ok(t, p)
}

// Cross-wave steal with no coordinator: two independent top-level waves share one
// Pool. Wave A holds an idle (cached) permit; wave B, finding the Resource exhausted,
// steals it — a transfer, not a new checkout — purely by a local forest walk.
func TestCrossWaveSteal(t *testing.T) {
	p := newPool(1)
	a := p.NewCache()
	ap, _ := a.Acquire()
	ap.Release() // wave A parked-idle: a.held=1, borrowable=1
	ok(t, p)

	b := p.NewCache()
	bp, got := b.Acquire() // own/ancestor miss, Resource full → step 4 steals from A
	require.True(t, got, "B steals A's idle permit rather than deadlocking")
	require.Same(t, b, bp.backing)
	assert.Equal(t, 1, p.totalHeld(), "a steal is a transfer, not a new checkout")
	assert.Equal(t, uint64(0), a.held(), "the victim simply loses the cached permit")
	assert.Equal(t, uint64(1), b.held())
	ok(t, p)

	bp.Release()
	require.True(t, b.ReleaseRef())
	require.True(t, a.ReleaseRef())
	assert.Equal(t, 0, p.totalHeld())
	ok(t, p)
}

// Nested driving: the base follows the computation locus down the nesting. A single
// capacity-1 permit serves a three-level synchronous chain (parent → child → grand-
// child), because only one body computes at a time along it.
func TestNestedDriveSinglePermitChain(t *testing.T) {
	p := newPool(1)
	parent := p.NewCache()
	pp, _ := parent.Acquire()
	pp.Release() // parent parks
	child := parent.NewChild()

	cp, ok1 := child.Acquire() // inherit parent's permit
	require.True(t, ok1)
	cp.Release() // child parks to drive its own sub-wave
	grand := child.NewChild()

	gp, ok2 := grand.Acquire() // walk grand → child(held=0) → parent(borrowable=1): inherit
	require.True(t, ok2)
	require.Same(t, parent, gp.backing, "the grandchild reaches the parent's permit up the chain")
	assert.Equal(t, 1, p.totalHeld())
	ok(t, p)

	gp.Release()
	require.True(t, grand.ReleaseRef())
	require.True(t, child.ReleaseRef())
	require.True(t, parent.ReleaseRef())
	assert.Equal(t, 0, p.totalHeld())
	ok(t, p)
}
