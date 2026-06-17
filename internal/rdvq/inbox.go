// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// inbox provides per-receiver buffering for direct handoff from senders.
// Each inbox is dedicated to a specific Receiver receiving items from
// a specific Queue.
type inbox[T any] struct {
	ch         chan T
	wasEmptied bool
}

// Init implements [omnipool.Initer]: it allocates the cap-1 buffered channel for
// a freshly created inbox. (Inboxes constructed directly, e.g. in tests, leave
// ch nil and allocate lazily in inboxOnlyQueue.PopFrontFunc instead.)
func (ib *inbox[T]) Init() { ib.ch = make(chan T, 1) }

// Reset implements [omnipool.Resetter]: on return to the pool it clears the
// emptied flag but KEEPS the (drained, empty) channel for reuse, since an inbox
// is only reclaimed once empty. Implementing Reset also prevents omnipool's
// default whole-struct zeroing, which would nil the channel.
func (ib *inbox[T]) Reset() { ib.wasEmptied = false }

// channel returns the inbox's channel for use in select statements. Returns
// nil if the inbox itself is nil. Panics if called when no channel has been
// allocated, which should only happen if it is called outside of a selectFn
// callback.
func (ib *inbox[T]) channel() <-chan T {
	if ib == nil {
		return nil
	}
	ch := ib.ch
	if ch == nil {
		panic("inbox channel is nil")
	}
	return ch
}

func (ib *inbox[T]) emptyPending() {
	ib.wasEmptied = false
}

// emptied marks the inbox as having been successfully emptied of a value.
// Must be called after successfully receiving from the inbox channel.
func (ib *inbox[T]) emptied() {
	ib.wasEmptied = true
}
