// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import "github.com/petenewcomb/streampool/internal/permits"

// binding pairs a permit Pool with an op's optional per-value weigher (nil ⟹ plain,
// weight 1). An op stores its bindings in CANONICAL GLOBAL ACQUISITION ORDER — ascending
// pool rank — so that when joint multi-limiter admission lands, a joint acquirer blocked
// at one limiter holds only limiters ordered before it: every wait-for edge points up the
// order and no cycle can close (weighted-acquisition.md §"Multi-limiter: the FIFO under
// joint admission").
type binding[T any] struct {
	pool  *permits.Pool
	weigh func(T) int
}

// addBinding returns bindings with (pool, weigh) inserted in canonical order (ascending
// pool.Rank()). It COPIES the input rather than mutating it, so the ops' builder methods
// keep their copy-with-modification semantics (a derived op must not disturb its parent's
// slice). Panics if pool is already bound — binding the same limiter twice to one op is a
// user error, not a silent no-op.
func addBinding[T any](bindings []binding[T], pool *permits.Pool, weigh func(T) int) []binding[T] {
	rank := pool.Rank()
	out := make([]binding[T], 0, len(bindings)+1)
	inserted := false
	for _, b := range bindings {
		switch br := b.pool.Rank(); {
		case br == rank:
			panic("streampool: the same limiter is bound more than once to an op")
		case !inserted && br > rank:
			out = append(out, binding[T]{pool: pool, weigh: weigh})
			inserted = true
		}
		out = append(out, b)
	}
	if !inserted {
		out = append(out, binding[T]{pool: pool, weigh: weigh})
	}
	return out
}

// insertPool is addBinding for the weigher-free (plain) case, over a bare pool slice.
func insertPool(pools []*permits.Pool, pool *permits.Pool) []*permits.Pool {
	rank := pool.Rank()
	out := make([]*permits.Pool, 0, len(pools)+1)
	inserted := false
	for _, p := range pools {
		switch pr := p.Rank(); {
		case pr == rank:
			panic("streampool: the same limiter appears more than once in a set")
		case !inserted && pr > rank:
			out = append(out, pool)
			inserted = true
		}
		out = append(out, p)
	}
	if !inserted {
		out = append(out, pool)
	}
	return out
}

// LimiterSet is a reusable, canonicalized bundle of plain [Limiter]s — built once (sorted
// into canonical acquisition order, duplicate-scanned, frozen) and bound to many ops of
// EVERY value type via [Launcher.WithLimiterSet]. Each dispatch acquires a weight-1 permit
// from every member.
//
// The zero-value (unlimited) Limiter is dropped on construction (it gates nothing).
type LimiterSet struct {
	pools []*permits.Pool // canonical order, deduped, immutable after construction
}

// NewLimiterSet canonicalizes ls into a reusable [LimiterSet]. Duplicate limiters panic.
func NewLimiterSet(ls ...Limiter) LimiterSet {
	var pools []*permits.Pool
	for _, l := range ls {
		if l.pool == nil {
			continue // the zero (unlimited) Limiter contributes nothing
		}
		pools = insertPool(pools, l.pool)
	}
	return LimiterSet{pools: pools}
}

// WeightLimiterSet is the typed analogue of [LimiterSet]: a canonicalized bundle of
// [WeightLimiter] bindings, reusable across same-T ops via [Launcher.WithWeightLimiterSet].
// Each dispatch acquires a weight-weigh(value) permit from every member.
type WeightLimiterSet[T any] struct {
	bindings []binding[T] // canonical order, deduped, immutable after construction
}

// NewWeightLimiterSet canonicalizes wls into a reusable [WeightLimiterSet]. Duplicate
// weighted limiters panic.
func NewWeightLimiterSet[T any](wls ...WeightLimiter[T]) WeightLimiterSet[T] {
	var bindings []binding[T]
	for _, wl := range wls {
		bindings = addBinding(bindings, wl.limiter.weightedPool(), wl.weigh)
	}
	return WeightLimiterSet[T]{bindings: bindings}
}
