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

// Safe to call Put with a nil timer, it will be ignored. This simplifies
// conditional use cases with timer variables, as it's always safe to defer
// timerp.Put(t) even if the variable was never initialized.
func Put(t *time.Timer) {
	if t != nil {
		if !t.Stop() {
			// Given Go 1.23+, this is just to support asynctimerchan != 0
			select {
			case <-t.C:
			default:
			}
		}
		pool.Put(t)
	}
}
