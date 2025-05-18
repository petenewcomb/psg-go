// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package timerp

import (
	"sync"
	"time"
)

// This implementation relies on [Go 1.23+ behavior] and is therefore not much
// more than a type-safe wrapper over [sync.Pool].
//
// [Go 1.23+ behavior]: https://pkg.go.dev/time#NewTimer

var pool = sync.Pool{
	New: func() any {
		return time.NewTimer(0)
	},
}

func Get() *time.Timer {
	return pool.Get().(*time.Timer)
}

func Put(t *time.Timer) {
	pool.Put(t)
}
