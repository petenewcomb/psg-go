// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package waitq

import "github.com/petenewcomb/psg-go/internal/rdvq"

type Queue struct {
	inner rdvq.Optional[struct{}]
}

func (q *Queue) Init() {
	q.inner.Init(p)
}

// Add to unbounded queue - never blocks
func (q *Queue) NewWaiter(verifyFn func() bool) Waiter {
	return Waiter{
		q:        q,
		verifyFn: verifyFn,
	}
}

// Notify signals the waiter at the front of the queue (if any).
func (q *Queue) Notify() {
	q.inner.TryPushBack(p, struct{}{})
}

func (q *Queue) NotifyAll() {
	for q.inner.TryPushBack(p, struct{}{}) {
		// Keep notifying until we can't anymore
	}
}

var p = &rdvq.Pool[struct{}]{}
