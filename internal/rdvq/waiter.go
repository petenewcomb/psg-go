// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Waiter manages waiting state across multiple Waiters instances for a single
// goroutine. Each goroutine should have its own Waiter instance.
type Waiter struct {
	inboxMap map[*Waiters]*inbox[RenotifyFunc]
}

// Reset clears all wait inbox mappings, preparing the Waiter for reuse.
func (s *Waiter) Reset() {
	clear(s.inboxMap)
}

// waitInboxFor returns the wait inbox for the given Waiters, creating one if it doesn't exist.
func waitInboxFor(s *Waiter, w *Waiters) *inbox[RenotifyFunc] {
	if s.inboxMap == nil {
		s.inboxMap = make(map[*Waiters]*inbox[RenotifyFunc])
	}

	ib := s.inboxMap[w]
	if ib == nil {
		ib = &inbox[RenotifyFunc]{}
		s.inboxMap[w] = ib
	}
	return ib
}
