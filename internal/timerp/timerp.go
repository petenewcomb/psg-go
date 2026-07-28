// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package timerp

import (
	"sync"
	"time"
)

var pool = sync.Pool{
	New: func() any {
		return time.NewTimer(0)
	},
}

func Get() *time.Timer {
	return pool.Get().(*time.Timer)
}

// Stop stops the timer and drains the channel if necessary.
// Returns true if the timer was stopped before expiring.
func Stop(t *time.Timer) bool {
	if !t.Stop() {
		// Given Go 1.23+, this is just to support asynctimerchan != 0
		select {
		case <-t.C:
		default:
		}
		return false
	}
	return true
}

// Safe to call Put with a nil timer, it will be ignored. This simplifies
// conditional use cases with timer variables, as it's always safe to defer
// timerp.Put(t) even if the variable was never initialized.
func Put(t *time.Timer) {
	if t != nil {
		Stop(t)
		pool.Put(t)
	}
}

// Reset stops the timer, drains the channel if necessary, and resets it to expire after duration d.
func Reset(t *time.Timer, d time.Duration) {
	Stop(t)
	t.Reset(d)
}
