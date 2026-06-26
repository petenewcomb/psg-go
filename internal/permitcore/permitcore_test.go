// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permitcore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ok runs CheckInvariants and fails the test on any violation.
func ok(t *testing.T, s *Store) {
	t.Helper()
	require.NoError(t, s.CheckInvariants())
}

// A top-level unit checks a permit out of free L, runs, releases (the permit stays
// cached), and on the unit's last reference returns it to L.
func TestRootUnitLifecycle(t *testing.T) {
	s := NewStore(1)
	u := s.NewRootPool()

	b, got := u.Acquire()
	require.True(t, got)
	require.Same(t, u, b, "a top-level acquire backs from the unit's own pool")
	assert.Equal(t, 1, s.checkedOut)
	ok(t, s)

	b.Release() // body completes; permit stays cached (held=1, inUse=0)
	assert.Equal(t, 1, s.checkedOut, "cache-don't-return: release does not give the permit back to L")
	ok(t, s)

	require.True(t, u.ReleaseRef(), "the unit's last reference destroys the pool")
	assert.Equal(t, 0, s.checkedOut, "destruction returns the cached permit to L")
	ok(t, s)
}

// THE canonical hang scenario, dissolved. Under a limit==1 limiter a parent body
// parks to drive a sub-wave; the sub-wave's body must run, but the only permit is
// the parent's. Eager-suspend gave the permit back; the cache lets the child
// INHERIT the parked parent's idle permit — no deadlock, no second checkout.
func TestParkedParentLendsToSubwave(t *testing.T) {
	s := NewStore(1)
	parent := s.NewRootPool()

	pb, _ := parent.Acquire() // parent runs: held=1, inUse=1
	ok(t, s)

	pb.Release() // parent parks to drive a sub-wave: inUse=0, borrowable=1
	sub := parent.NewChildPool()

	cb, got := sub.Acquire() // the old livelock — now a step-2 ancestor inherit
	require.True(t, got, "child must inherit the parked parent's idle permit, not deadlock")
	require.Same(t, parent, cb, "the child is backed by the parent's permit, unmoved")
	assert.Equal(t, 1, s.checkedOut, "no second permit is checked out")
	ok(t, s)

	cb.Release()                      // child body completes
	require.True(t, sub.ReleaseRef()) // sub-wave drains; its pool is destroyed
	assert.Equal(t, 1, s.checkedOut)  // parent still holds its cached permit
	ok(t, s)

	pb2, got := parent.Acquire() // parent resumes (reacquire): step-1 own-cache hit
	require.True(t, got)
	require.Same(t, parent, pb2)
	pb2.Release()
	require.True(t, parent.ReleaseRef())
	assert.Equal(t, 0, s.checkedOut)
	ok(t, s)
}

// Parallel sub-wave bodies contend for the parent's pool like a semaphore of size
// held: the first inherits the parent's idle permit; the second must take a delta
// (free L) into its own pool; a third blocks once capacity is exhausted — and
// blocking is legitimate only because nothing is borrowable.
func TestParallelChildrenTakeDeltaThenBlock(t *testing.T) {
	s := NewStore(2)
	parent := s.NewRootPool()
	pb, _ := parent.Acquire()
	pb.Release() // parent parks: borrowable=1
	sub := parent.NewChildPool()

	c1, ok1 := sub.Acquire() // step 2: inherit parent's permit
	require.True(t, ok1)
	require.Same(t, parent, c1)

	c2, ok2 := sub.Acquire() // ancestors exhausted → step 3: delta from free L into sub
	require.True(t, ok2)
	require.Same(t, sub, c2, "the second concurrent child takes a delta into its own pool")
	assert.Equal(t, 2, s.checkedOut)
	ok(t, s)

	_, ok3 := sub.Acquire() // capacity exhausted, both in use
	require.False(t, ok3, "a third concurrent child must block")
	assert.False(t, s.HasBorrowable(), "blocking is legitimate: nothing is borrowable")

	c1.Release()
	c2.Release()
	require.True(t, sub.ReleaseRef())
	require.True(t, parent.ReleaseRef())
	assert.Equal(t, 0, s.checkedOut)
	ok(t, s)
}

// Cross-wave steal with no coordinator: two independent top-level waves share one
// limiter. Wave A holds an idle (cached) permit; wave B, finding free L exhausted,
// steals it — a transfer, not a new checkout — purely by a local forest walk.
func TestCrossWaveSteal(t *testing.T) {
	s := NewStore(1)
	a := s.NewRootPool()
	ab, _ := a.Acquire()
	ab.Release() // wave A parked-idle: a.held=1, borrowable=1
	ok(t, s)

	b := s.NewRootPool()
	bb, got := b.Acquire() // own/ancestor miss, free L full → step 4 steals from A
	require.True(t, got, "B steals A's idle permit rather than deadlocking")
	require.Same(t, b, bb)
	assert.Equal(t, 1, s.checkedOut, "a steal is a transfer, not a new checkout")
	assert.Equal(t, 0, a.held, "the victim simply loses the cached permit")
	assert.Equal(t, 1, b.held)
	ok(t, s)

	bb.Release()
	require.True(t, b.ReleaseRef())
	require.True(t, a.ReleaseRef())
	assert.Equal(t, 0, s.checkedOut)
	ok(t, s)
}

// Nested driving: the base follows the computation locus down the nesting. A single
// limit==1 permit serves a three-level synchronous chain (parent → child → grand-
// child), because only one body computes at a time along it.
func TestNestedDriveSinglePermitChain(t *testing.T) {
	s := NewStore(1)
	parent := s.NewRootPool()
	pb, _ := parent.Acquire()
	pb.Release() // parent parks
	child := parent.NewChildPool()

	cb, ok1 := child.Acquire() // inherit parent's permit
	require.True(t, ok1)
	cb.Release() // child parks to drive its own sub-wave
	grand := child.NewChildPool()

	gb, ok2 := grand.Acquire() // walk grand → child(held=0) → parent(borrowable=1): inherit
	require.True(t, ok2)
	require.Same(t, parent, gb, "the grandchild reaches the parent's permit up the chain")
	assert.Equal(t, 1, s.checkedOut)
	ok(t, s)

	gb.Release()
	require.True(t, grand.ReleaseRef())
	require.True(t, child.ReleaseRef())
	require.True(t, parent.ReleaseRef())
	assert.Equal(t, 0, s.checkedOut)
	ok(t, s)
}
