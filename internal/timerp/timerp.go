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
type Pool struct {
	inner sync.Pool
}

func (p *Pool) Init() {
	p.inner.New = func() any {
		return time.NewTimer(0)
	}
}

func (p *Pool) Get() *time.Timer {
	return p.inner.Get().(*time.Timer)
}

func (p *Pool) Put(t *time.Timer) {
	p.inner.Put(t)
}
