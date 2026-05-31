// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import "sync"

// Receiver funnels inbox and waiter functionality for receiving from queues.
// Each goroutine should have its own Receiver instance to avoid races.
// The Receiver automatically creates and manages inboxes as needed when
// receiving from different Queue instances.
//
// To enable map pooling across goroutine lifecycles, callers should call
// [Receiver.Release] when done with a Receiver. Forgoing Release is safe but
// wastes the per-Receiver map allocation.
type Receiver struct {
	inboxMap     map[any]any
	outboxWaiter Waiter
}

// Release returns the Receiver's underlying inbox map to a shared pool for
// reuse by future Receivers. After Release the Receiver remains usable;
// lazily-initialized state will be re-acquired on the next receive.
func (r *Receiver) Release() {
	r.outboxWaiter.Release()
	if r.inboxMap == nil {
		return
	}
	clear(r.inboxMap)
	receiverMapPool.Put(r.inboxMap)
	r.inboxMap = nil
}

var receiverMapPool = sync.Pool{
	New: func() any { return make(map[any]any) },
}

// inboxFor returns the inbox for the given key, creating one if it doesn't exist.
func inboxFor[T any](r *Receiver, q *Queue[T]) *inbox[T] {
	if r.inboxMap == nil {
		r.inboxMap = receiverMapPool.Get().(map[any]any)
	}

	ibAny := r.inboxMap[q]
	if ibAny == nil {
		ib := &inbox[T]{}
		r.inboxMap[q] = ib
		return ib
	}
	return ibAny.(*inbox[T])
}
