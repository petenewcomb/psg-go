// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package exmpclk

import (
	"sync"
	"time"
)

var epoch = time.Now()

// Imperfect but useful clock to help create deterministic example tests despite
// varability in time.Sleep() precision
type ExampleClock struct {
	mu         sync.Mutex
	origin     time.Duration // since epoch
	sleepStart time.Duration // since epoch
}

func (c *ExampleClock) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.origin = time.Since(epoch)
	if c.sleepStart >= 0 {
		c.sleepStart = c.origin
	}
}

func (c *ExampleClock) Elapsed(quantum time.Duration) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	elapsed := time.Since(epoch) - c.origin
	return (elapsed / quantum) * quantum
}

func (c *ExampleClock) Sleep(duration time.Duration) {
	ct := c.CalibrationTimer()
	defer ct.Stop(duration)
	time.Sleep(duration)
}

func (c *ExampleClock) CalibrationTimer() CalibrationTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CalibrationTimer{
		clock:  c,
		origin: c.origin,
		start:  time.Now(),
	}
}

type CalibrationTimer struct {
	clock  *ExampleClock
	origin time.Duration // since epoch
	start  time.Time
}

func (ct CalibrationTimer) Stop(refDuration time.Duration) {
	drift := time.Since(ct.start) - refDuration
	ct.clock.mu.Lock()
	defer ct.clock.mu.Unlock()
	ct.clock.origin = ct.origin + drift
}
