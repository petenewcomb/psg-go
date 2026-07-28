// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Pool ranks are process-global and monotonic (creation order), so a limiter created
// earlier sorts before one created later. These tests assert RELATIVE order + identity,
// never absolute ranks (other tests mint pools in the same process).

func TestLimiterSet_CanonicalOrderDedupAndZeroDrop(t *testing.T) {
	chk := require.New(t)
	a := NewSemaphore(1) // ranks ascend in creation order: a < b < c
	b := NewSemaphore(2)
	c := NewSemaphore(3)

	s := NewLimiterSet(c, a, b) // built out of order
	chk.Len(s.pools, 3)
	chk.Same(a.pool, s.pools[0], "canonical order is ascending rank = creation order")
	chk.Same(b.pool, s.pools[1])
	chk.Same(c.pool, s.pools[2])

	chk.PanicsWithValue(
		"streampool: the same limiter appears more than once in a set",
		func() { NewLimiterSet(a, a) },
		"a duplicate limiter in a set is a user error",
	)

	chk.Len(NewLimiterSet(a, Limiter{}, b).pools, 2, "the zero (unlimited) Limiter is dropped")
}

func TestWeightLimiterSet_CanonicalOrder(t *testing.T) {
	chk := require.New(t)
	x := NewWeightedSemaphore(4) // x < y in rank
	y := NewWeightedSemaphore(5)
	wlx := NewWeightLimiter(x, func(int) int { return 1 })
	wly := NewWeightLimiter(y, func(int) int { return 2 })

	s := NewWeightLimiterSet(wly, wlx) // out of order
	chk.Len(s.bindings, 2)
	chk.Same(x.weightedPool(), s.bindings[0].pool, "ascending rank")
	chk.Same(y.weightedPool(), s.bindings[1].pool)
	chk.Equal(2, s.bindings[1].weigh(1), "the member's weigher is preserved")

	chk.PanicsWithValue(
		"streampool: the same limiter is bound more than once to an op",
		func() { NewWeightLimiterSet(wlx, wlx) },
	)
}

func TestWithLimiterSet_BindsInCanonicalOrder(t *testing.T) {
	chk := require.New(t)
	a := NewSemaphore(1) // a < b in rank
	b := NewSemaphore(2)
	base := NewFnLauncher(func(_ context.Context, _ int, _ error) error { return nil })

	// A multi-member set AND-composes: two bindings, canonically ordered, no panic.
	bound := base.WithLimiterSet(NewLimiterSet(b, a))
	chk.Len(bound.bindings, 2)
	chk.Same(a.pool, bound.bindings[0].pool, "ascending rank")
	chk.Same(b.pool, bound.bindings[1].pool)

	// Accumulating across binder calls merges into one canonical order; a duplicate panics.
	chk.PanicsWithValue(
		"streampool: the same limiter is bound more than once to an op",
		func() { base.WithLimits(a).WithLimits(a) },
	)
}
