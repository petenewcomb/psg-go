// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"sync/atomic"
)

type InFlightCounter struct {
	atomic.Int64
}

func (c *InFlightCounter) Increment() {
	c.Add(1)
}

func (c *InFlightCounter) IncrementIfUnder(limit int) bool {
	// Tentatively increment the counter and check against limit. If over limit,
	// remove the tentative increment and try again if we notice that another
	// goroutine has made room between the increment and decrement.
	for c.Add(1) > int64(limit) {
		// Back out tentative increment and re-check.
		if c.Add(-1) >= int64(limit) {
			// Still at or over limit.
			return false
		}
	}
	// Incremented counter is within limit.
	return true
}

func (c *InFlightCounter) Decrement() bool {
	newValue := c.Add(-1)
	if newValue < 0 {
		panic("there were no tasks in flight")
	}
	return newValue == 0
}

func (c *InFlightCounter) GreaterThanZero() bool {
	return c.Load() > 0
}
