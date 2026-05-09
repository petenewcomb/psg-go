// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// WaitInbox is an inbox specialized for receiving RenotifyFunc notifications.
// It is used by Waiters to deliver notifications to waiting goroutines and is
// passed to nested wait selectFn callbacks (see Waiters.WaitFunc).
type WaitInbox inbox[RenotifyFunc]

// Ch returns the WaitInbox's channel for use in select statements. Returns
// nil if the WaitInbox itself is nil. Panics if called when no channel has
// been allocated, which should only happen if it is called outside of a
// selectFn callback.
func (w *WaitInbox) Ch() <-chan RenotifyFunc {
	return (*inbox[RenotifyFunc])(w).channel()
}

// Emptied marks the WaitInbox as having been successfully emptied of a
// notification. Must be called after successfully receiving from the channel
// returned by Ch.
func (w *WaitInbox) Emptied() {
	(*inbox[RenotifyFunc])(w).emptied()
}

// Waiter manages waiting state across multiple Waiters instances for a single
// goroutine. Each goroutine should have its own Waiter instance.
type Waiter struct {
	inboxMap map[*Waiters]*WaitInbox
}

// Reset clears all wait inbox mappings, preparing the Waiter for reuse.
func (s *Waiter) Reset() {
	clear(s.inboxMap)
}

// waitInboxFor returns the wait inbox for the given Waiters, creating one if it doesn't exist.
func waitInboxFor(s *Waiter, w *Waiters) *WaitInbox {
	if s.inboxMap == nil {
		s.inboxMap = make(map[*Waiters]*WaitInbox)
	}

	waitInbox := s.inboxMap[w]
	if waitInbox == nil {
		waitInbox = &WaitInbox{}
		s.inboxMap[w] = waitInbox
	}
	return waitInbox
}
