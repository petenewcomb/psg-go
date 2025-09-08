// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Receiver combines inbox and waiter functionality for receiving from queues.
// Each goroutine should have its own Receiver instance to avoid races.
// The Receiver automatically creates and manages inboxes as needed when
// receiving from different Queue instances.
type Receiver struct {
	inboxMap     map[any]any
	outboxWaiter Waiter
}

// Reset clears all inbox mappings, preparing the Receiver for reuse.
func (r *Receiver) Reset() {
	// Reset the map to allow reuse without reallocating
	clear(r.inboxMap)
}

// inboxFor returns the inbox for the given key, creating one if it doesn't exist.
func inboxFor[T any](r *Receiver, q *Queue[T]) *Inbox[T] {
	if r.inboxMap == nil {
		r.inboxMap = make(map[any]any)
	}

	inboxAny := r.inboxMap[q]
	if inboxAny == nil {
		inbox := &Inbox[T]{}
		r.inboxMap[q] = inbox
		return inbox
	}
	return inboxAny.(*Inbox[T])
}
