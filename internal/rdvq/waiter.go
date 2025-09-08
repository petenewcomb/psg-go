// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// WaitInbox is an inbox specialized for receiving RenotifyFunc notifications.
// It's used by Waiters to deliver notifications to waiting goroutines.
type WaitInbox = Inbox[RenotifyFunc]

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
