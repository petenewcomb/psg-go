// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package jobstate

import (
	"context"
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/trace"
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

	trace.Logf(context.Background(), traceRegion, "InFlightCounter=%p, newValue=%d; returning %v", c, newValue, ok)
	return ok
}

//nolint:contextcheck // background context used only for tracing
func (c *InFlightCounter) IsUnder(limit int) bool {
	traceRegion := "InFlightCounter.IsUnder"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	value := c.v.Load()
	ok := value < int64(limit)

	trace.Logf(context.Background(), traceRegion, "InFlightCounter=%p, value=%d, limit=%d; returning %v", c, value, limit, ok)
	return ok
}

//nolint:contextcheck // background context used only for tracing
func (c *InFlightCounter) IncrementIfUnder(limit int) bool {
	traceRegion := "InFlightCounter.IncrementIfUnder"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "InFlightCounter=%p", c)

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
		trace.Logf(context.Background(), traceRegion, "newValue=%d > limit=%d; backing out increment", newValue, limit)
		newValue = c.v.Add(-1)
		if newValue >= int64(limit) {
			// Still at or over limit.
			trace.Logf(context.Background(), traceRegion, "newValue=%d >= limit=%d; still at or over limit, returning false", newValue, limit)
			return false
		}
		// Room might have been made, try again.
	}

	trace.Logf(context.Background(), traceRegion, "newValue=%d <= limit=%d; returning true", newValue, limit)
	return true
}

//nolint:contextcheck // background context used only for tracing
func (c *InFlightCounter) Decrement() bool {
	traceRegion := "InFlightCounter.Decrement"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	newValue := c.v.Add(-1)
	ok := newValue == 0
	trace.Logf(context.Background(), traceRegion, "InFlightCounter=%p, newValue=%d; returning %v", c, newValue, ok)

	if newValue < 0 {
		panic("there were no tasks in flight")
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
	trace.Logf(context.Background(), traceRegion, "InFlightCounter=%p, newValue=%d, limit=%d; returning %v", c, newValue, limit, ok)

	if newValue < 0 {
		panic("there were no tasks in flight")
	}
	return ok
}

//nolint:contextcheck // background context used only for tracing
func (c *InFlightCounter) IsZero() bool {
	traceRegion := "InFlightCounter.IsZero"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	value := c.v.Load()
	ok := value == 0

	trace.Logf(context.Background(), traceRegion, "InFlightCounter=%p, value=%d; returning %v", c, value, ok)
	return ok
}
