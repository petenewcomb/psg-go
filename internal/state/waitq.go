// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.
package state

type WaiterQueue struct {
	q SyncQueue[chan<- struct{}]
}

// Registers and returns a channel that will be closed when Notify is called and
// the caller is at the front of the queue.
func (wq *WaiterQueue) Add() chan struct{} {
	notifyCh := make(chan struct{})

	// Add to queue - never blocks
	wq.q.PushBack(notifyCh)
	return notifyCh
}

// Notify signals the waiter at the front of the queue (if any).
func (wq *WaiterQueue) Notify() {
	if notifyCh, ok := wq.q.PopFront(); ok {
		close(notifyCh)
	}
}
