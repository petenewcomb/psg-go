// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"sync/atomic"
)

type InFlightCounter struct {
	v atomic.Int64
}

func (c *InFlightCounter) Increment() bool {
	return c.v.Add(1) == 1
}

func (c *InFlightCounter) IsUnder(limit int) bool {
	return c.v.Load() < int64(limit)
}

func (c *InFlightCounter) IncrementIfUnder(limit int) bool {
	// Tentatively increment the counter and check against limit. If over limit,
	// remove the tentative increment and try again if we notice that another
	// goroutine has made room between the increment and decrement.
	for c.v.Add(1) > int64(limit) {
		// Back out tentative increment and re-check.
		if c.v.Add(-1) >= int64(limit) {
			// Still at or over limit.
			return false
		}
	}
	// Incremented counter is within limit.
	return true
}

func (c *InFlightCounter) Decrement() bool {
	newValue := c.v.Add(-1)
	if newValue < 0 {
		panic("there were no tasks in flight")
	}
	return newValue == 0
}

// DecrementAndCheckIfUnder decrements the counter and checks if the value was under the given limit after decrementing.
// Returns true if the value after decrementing was under the limit.
func (c *InFlightCounter) DecrementAndCheckIfUnder(limit int) bool {
	newValue := c.v.Add(-1)
	if newValue < 0 {
		panic("there were no tasks in flight")
	}
	// Check if new value is under limit
	return limit < 0 || newValue < int64(limit)
}

func (c *InFlightCounter) IsZero() bool {
	return c.v.Load() == 0
}
