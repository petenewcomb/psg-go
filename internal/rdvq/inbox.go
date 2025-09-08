// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Inbox provides per-receiver buffering for direct handoff from senders.
// Each Inbox is dedicated to a specific Receiver receiving items from
// a specific Queue.
type Inbox[T any] struct {
	ch         chan T
	wasEmptied bool
}

// Ch returns the inbox's channel for use in select statements. Returns nil if
// the inbox itself is nil. Panics if called when no channel has been allocated,
// which should only happen if Ch() is called outside of a selectFn callback.
func (ib *Inbox[T]) Ch() <-chan T {
	if ib == nil {
		return nil
	}
	ch := ib.ch
	if ch == nil {
		panic("inbox channel is nil")
	}
	return ch
}

func (ib *Inbox[T]) emptyPending() {
	ib.wasEmptied = false
}

// Emptied marks the inbox as having been successfully emptied of a value.
// This must be called by the receiver after successfully receiving from the inbox channel.
func (ib *Inbox[T]) Emptied() {
	ib.wasEmptied = true
}
