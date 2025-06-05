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
func (q *Queue) Wait(fn func(Waiter) bool) {
	q.inner.PopFrontFunc(p,
		func(struct{}) {
			// There was an orphaned value in the channel, meaning that this
			// waiter was notified but didn't receive it. Call Notify to pass
			// the notification to another.
			q.Notify()
		},
		func(ch <-chan struct{}) bool {
			return fn(Waiter{ch: ch})
		},
	)
}

// Notify signals the waiter at the front of the queue (if any).
func (q *Queue) Notify() {
	q.inner.TryPushBack(p, struct{}{})
}

var p = &rdvq.Pool[struct{}]{}
