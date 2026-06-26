// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package ctxpool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The whole point of the pool is to reuse the child context.Context object: a
// Free'd child must come back out of WithValue rather than being re-minted. This
// guards against the regression where an embedded zero-value omnipool.Pool (no
// Reset) zeroed the child and forced a fresh context.WithValue every borrow.
func TestWithValue_ReusesChildCtxAfterFree(t *testing.T) {
	ctx := context.Background()
	c1 := WithValue(ctx, 1)
	got, ok := GetValue[int](c1)
	require.True(t, ok)
	require.Equal(t, 1, got)

	Free(c1)
	c2 := WithValue(ctx, 2)
	require.Same(t, c1, c2, "a Free'd child ctx must be reused, not re-minted")

	got, ok = GetValue[int](c2)
	require.True(t, ok)
	require.Equal(t, 2, got, "the reused child must carry the freshly stamped value")
}

// Free clears the stamped value so a not-yet-reused child never pins it.
func TestFree_ClearsValue(t *testing.T) {
	c := WithValue(context.Background(), "live")
	Free(c)
	_, ok := GetValue[string](c)
	assert.False(t, ok, "Free must clear the stamped value")
}

// Distinct parent contexts get distinct child pools and therefore distinct children.
func TestWithValue_DistinctParentsDistinctChildren(t *testing.T) {
	a := context.Background()
	b := context.WithValue(context.Background(), struct{ k int }{}, 1)
	ca := WithValue(a, 1)
	cb := WithValue(b, 2)
	assert.NotSame(t, ca, cb, "distinct parent ctxs must yield distinct child ctxs")
}

// GetValue on a context with no stamped child returns not-ok rather than panicking.
func TestGetValue_Missing(t *testing.T) {
	_, ok := GetValue[int](context.Background())
	assert.False(t, ok)
}

// Clear drops every cached child pool so its child contexts (and stamped values) can
// be GC'd without waiting for each parent ctx's AfterFunc — the eager reclaim that
// streampool.Wait performs after joining the workers. The pool map ends empty, and
// the pool stays immediately reusable (a later borrow repopulates a fresh map; a
// child cached before Clear is not handed back out).
func TestClear_DropsAllPoolsAndStaysReusable(t *testing.T) {
	countPools := func() int {
		n := 0
		childPools.Load().Range(func(_, _ any) bool { n++; return true })
		return n
	}

	a := context.Background()
	b := context.WithValue(context.Background(), struct{ k int }{}, 1)

	// Populate: two distinct parents → two child pools, with a Free'd child cached
	// under a so its (non-)reuse across Clear is observable.
	ca := WithValue(a, 1)
	Free(ca)
	_ = WithValue(b, 2)
	require.NotZero(t, countPools(), "precondition: child pools are cached")

	Clear()
	assert.Zero(t, countPools(), "Clear must drop every cached child pool")

	// Reusable: a fresh borrow works and repopulates the swapped-in map. The child is
	// newly minted, not the pre-Clear cached one (that pool was dropped, not reused).
	ca2 := WithValue(a, 3)
	assert.NotSame(t, ca, ca2, "a child cached before Clear must not survive it")
	got, ok := GetValue[int](ca2)
	require.True(t, ok)
	assert.Equal(t, 3, got)
	assert.NotZero(t, countPools(), "the pool is reusable after Clear")
}

// A child of a cancelled parent is not re-pooled (its ctx is done); Free is a no-op
// rather than recycling a doomed child. This is the in-flight-vs-eviction contract
// that keeps a cancelled child from being handed to a later borrower.
func TestFree_SkipsCancelledChild(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	c := WithValue(parent, 7)
	cancel()
	<-c.Done()
	Free(c) // must not re-pool a cancelled child

	// A fresh borrow from a live parent is unaffected and reuses normally.
	live := WithValue(context.Background(), 8)
	Free(live)
	again := WithValue(context.Background(), 9)
	assert.Same(t, live, again)
}
