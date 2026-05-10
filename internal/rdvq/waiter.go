// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import "sync"

// Waiter manages waiting state across multiple Waiters instances for a single
// goroutine. Each goroutine should have its own Waiter instance.
//
// To enable map pooling across goroutine lifecycles, callers should call
// [Waiter.Release] when done with a Waiter. Forgoing Release is safe but wastes
// the per-Waiter map allocation.
type Waiter struct {
	inboxMap map[*Waiters]*inbox[RenotifyFunc]
}

// Release returns the Waiter's underlying wait inbox map to a shared pool
// for reuse by future Waiters. After Release the Waiter remains usable;
// lazily-initialized state will be re-acquired on the next wait.
func (s *Waiter) Release() {
	if s.inboxMap == nil {
		return
	}
	clear(s.inboxMap)
	waiterMapPool.Put(s.inboxMap)
	s.inboxMap = nil
}

var waiterMapPool = sync.Pool{
	New: func() any { return make(map[*Waiters]*inbox[RenotifyFunc]) },
}

// waitInboxFor returns the wait inbox for the given Waiters, creating one if it doesn't exist.
func waitInboxFor(s *Waiter, w *Waiters) *inbox[RenotifyFunc] {
	if s.inboxMap == nil {
		s.inboxMap = waiterMapPool.Get().(map[*Waiters]*inbox[RenotifyFunc])
	}

	ib := s.inboxMap[w]
	if ib == nil {
		ib = &inbox[RenotifyFunc]{}
		s.inboxMap[w] = ib
	}
	return ib
}
