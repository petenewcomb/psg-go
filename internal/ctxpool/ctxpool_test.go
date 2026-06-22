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
