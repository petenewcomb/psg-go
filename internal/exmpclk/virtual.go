// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package exmpclk

import (
	"sync"
	"time"
)

// VirtualClock makes time-annotated example output deterministic for examples whose
// timeline is driven entirely by sleeps (simulated work durations). Work still sleeps for
// real — so the framework genuinely runs it concurrently — but Elapsed reports VIRTUAL
// time that advances ONLY when a goroutine sleeps, by exactly the requested duration. The
// printed timeline is therefore the designed schedule, immune to real-sleep imprecision,
// GC pauses, and load from any co-running test.
//
// This is the deterministic counterpart to [ExampleClock], whose drift-corrected real
// clock is needed only by examples driven by real timeouts/cancellation (where no sleep
// marks the passage of time). For a pure sleep-driven schedule, prefer VirtualClock: it
// cannot drift.
//
// Virtual time is a single monotonic value advanced to max(now, sleepStart+duration) at
// each sleep. It is exact for the common example pattern where each unit of work begins
// at a prior unit's completion (so it reads the right virtual start) and durations are
// spaced enough that real execution order matches the virtual schedule.
type VirtualClock struct {
	mu  sync.Mutex
	now time.Duration // virtual time since Start
}

// Start resets virtual time to zero. Call once before use.
func (c *VirtualClock) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = 0
}

// Elapsed returns the virtual time since Start, floored to quantum.
func (c *VirtualClock) Elapsed(quantum time.Duration) time.Duration {
	c.mu.Lock()
	now := c.now
	c.mu.Unlock()
	return (now / quantum) * quantum
}

// Sleep sleeps for real (so the framework runs work concurrently) and advances virtual
// time by exactly duration from the virtual instant the sleep began.
func (c *VirtualClock) Sleep(duration time.Duration) {
	c.mu.Lock()
	start := c.now
	c.mu.Unlock()

	time.Sleep(duration)

	c.mu.Lock()
	if end := start + duration; end > c.now {
		c.now = end
	}
	c.mu.Unlock()
}
