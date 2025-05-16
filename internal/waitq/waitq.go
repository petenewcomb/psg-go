// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package waitq

import "github.com/petenewcomb/psg-go/internal/nbcq"

type Queue struct {
	inner nbcq.Queue[Waiter]
}

func (q *Queue) Init() {
	q.inner.Init()
}

// Add to unbounded queue - never blocks
func (q *Queue) Add() Waiter {
	w := Waiter{
		q:          q,
		notifyChan: make(chan struct{}, 1),
	}
	q.inner.PushBack(w)
	return w
}

// Notify signals the waiter at the front of the queue (if any).
func (q *Queue) Notify() {
	for {
		w, ok := q.inner.PopFront()
		if !ok {

			return
		}

		select {
		case w.notifyChan <- struct{}{}:
			// The notification was sent.
			return
		default:
			// The channel was full, meaning that the waiter was closed. Loop
			// and try the next one.
		}
	}
}
