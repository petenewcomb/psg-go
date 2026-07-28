// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package wavestate

import (
	"context"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/trace"
)

type InFlightCounter struct {
	v atomic.Int64
}

//nolint:contextcheck // background context used only for tracing
func (c *InFlightCounter) Increment() bool {
	traceRegion := "InFlightCounter.Increment"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	newValue := c.v.Add(1)
	ok := newValue == 1

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "InFlightCounter=%p, newValue=%d; returning %v", c, newValue, ok)
	}
	return ok
}

//nolint:contextcheck // background context used only for tracing
func (c *InFlightCounter) IsUnder(limit int) bool {
	traceRegion := "InFlightCounter.IsUnder"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	value := c.v.Load()
	ok := value < int64(limit)

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion,
			"InFlightCounter=%p, value=%d, limit=%d; returning %v",
			c, value, limit, ok)
	}
	return ok
}

// AddIfUnder atomically adds n while the result stays within limit, reporting
// success — the weighted form of IncrementIfUnder (a weighted semaphore's
// TryAcquire(n) must admit all of n or none of it; n sequential increments would
// admit partially and strand the remainder).
func (c *InFlightCounter) AddIfUnder(n, limit int) bool {
	for {
		cur := c.v.Load()
		next := cur + int64(n)
		if next > int64(limit) {
			return false
		}
		if c.v.CompareAndSwap(cur, next) {
			return true
		}
	}
}

// AddUpTo atomically adds min(n, limit-current) and returns the amount added
// (0 when the counter is at or over limit) — the partial-draw form of
// AddIfUnder backing a resource's TryAcquireUpTo: one CAS commits a single
// min(free, n) draw, so one observed free state yields at most one partial
// grant and concurrent claimants resolve without double-counting.
func (c *InFlightCounter) AddUpTo(n, limit int) int {
	for {
		cur := c.v.Load()
		take := int64(limit) - cur
		if take > int64(n) {
			take = int64(n)
		}
		if take <= 0 {
			return 0
		}
		if c.v.CompareAndSwap(cur, cur+take) {
			return int(take)
		}
	}
}

//nolint:contextcheck // background context used only for tracing
func (c *InFlightCounter) IncrementIfUnder(limit int) bool {
	traceRegion := "InFlightCounter.IncrementIfUnder"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "InFlightCounter=%p", c)
	}

	// Tentatively increment the counter and check against limit. If over limit,
	// remove the tentative increment and try again if we notice that another
	// goroutine has made room between the increment and decrement.
	var newValue int64
	for {
		newValue = c.v.Add(1)
		if newValue <= int64(limit) {
			break
		}

		// Back out tentative increment and re-check.
		if trace.IsEnabled() {
			trace.Logf(context.Background(), traceRegion, "newValue=%d > limit=%d; backing out increment", newValue, limit)
		}
		newValue = c.v.Add(-1)
		if newValue < 0 {
			panic("unbalanced decrement detected")
		}
		if newValue >= int64(limit) {
			// Still at or over limit.
			if trace.IsEnabled() {
				trace.Logf(context.Background(), traceRegion,
					"newValue=%d >= limit=%d; still at or over limit, returning false",
					newValue, limit)
			}
			return false
		}
		// Room might have been made, try again.
	}

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "newValue=%d <= limit=%d; returning true", newValue, limit)
	}
	return true
}

// transitionClaim is the sentinel ClaimZero installs in place of a zero count while
// a zero-crossing transition commits. It sits far above any real count so a claimed
// counter is unmistakable to IncrementUnlessClaimed, and so a stray concurrent
// increment (a misuse-class late arrival) neither unclaims it nor is lost —
// ReleaseClaim subtracts the sentinel, preserving any such delta arithmetically.
const transitionClaim = int64(1) << 40

// ClaimZero atomically claims a zero count, reporting success. It is the commit
// point of a zero-crossing transition (Flushing→Done): claiming excludes
// IncrementUnlessClaimed pinners for the transition's duration, and failing means
// some reference (a pin or a fresh increment) landed first — the transition must
// abort, and that reference's own release will re-trigger it. Balance with
// [InFlightCounter.ReleaseClaim].
func (c *InFlightCounter) ClaimZero() bool {
	return c.v.CompareAndSwap(0, transitionClaim)
}

// ReleaseClaim ends a ClaimZero claim, restoring the count (plus any increments
// that arrived during the claim).
func (c *InFlightCounter) ReleaseClaim() {
	if c.v.Add(-transitionClaim) < 0 {
		panic("unbalanced claim release detected")
	}
}

// IncrementUnlessClaimed atomically increments the counter unless a zero-crossing
// transition has claimed it (see [InFlightCounter.ClaimZero]), reporting success.
// It is the counter-level tryPin: an unconditional increment cannot stop a claimed
// transition — the count would be resurrected under a teardown already committed on
// another goroutine. Incrementing from an UNCLAIMED zero is allowed and excludes
// any later claim, which is what lets a pinner hold open an idle state that has not
// committed to draining.
func (c *InFlightCounter) IncrementUnlessClaimed() bool {
	for {
		cur := c.v.Load()
		if cur >= transitionClaim {
			return false
		}
		if c.v.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

//nolint:contextcheck // background context used only for tracing
func (c *InFlightCounter) Decrement() bool {
	traceRegion := "InFlightCounter.Decrement"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	newValue := c.v.Add(-1)
	ok := newValue == 0
	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "InFlightCounter=%p, newValue=%d; returning %v", c, newValue, ok)
	}

	if newValue < 0 {
		panic("unbalanced decrement detected")
	}
	return ok
}

// DecrementAndCheckIfUnder decrements the counter and checks if the value was under the given limit after decrementing.
// Returns true if the value after decrementing was under the limit.
//
//nolint:contextcheck // background context used only for tracing
func (c *InFlightCounter) DecrementAndCheckIfUnder(limit int) bool {
	traceRegion := "InFlightCounter.DecrementAndCheckIfUnder"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	newValue := c.v.Add(-1)
	// Check if new value is under limit
	ok := limit < 0 || newValue < int64(limit)
	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion,
			"InFlightCounter=%p, newValue=%d, limit=%d; returning %v",
			c, newValue, limit, ok)
	}

	if newValue < 0 {
		panic("unbalanced decrement detected")
	}
	return ok
}

//nolint:contextcheck // background context used only for tracing
func (c *InFlightCounter) IsZero() bool {
	traceRegion := "InFlightCounter.IsZero"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	value := c.v.Load()
	ok := value == 0

	if trace.IsEnabled() {
		trace.Logf(context.Background(), traceRegion, "InFlightCounter=%p, value=%d; returning %v", c, value, ok)
	}
	return ok
}
