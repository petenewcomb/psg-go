// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import "sync"

// Sender manages outboxes across multiple queues for a single goroutine.
// Each goroutine should have its own Sender instance to avoid races.
// The Sender automatically creates and manages outboxes as needed when
// sending to different Queue instances.
//
// To enable map and outbox pooling across goroutine lifecycles, callers
// should call [Sender.Release] when done with a Sender. Forgoing Release is
// safe but wastes the per-Sender map allocation; it can also be a
// deliberate choice for an outlier goroutine that sends to a large number
// of distinct queues, to avoid leaving an oversized map in the shared pool.
type Sender struct {
	outboxMap map[any]any
}

// Release frees the Sender's per-queue outboxes and returns the underlying
// map to a shared pool for reuse by future Senders. After Release the
// Sender remains usable; lazily-initialized state will be re-acquired on
// the next send.
func (s *Sender) Release() {
	if s.outboxMap == nil {
		return
	}
	for _, v := range s.outboxMap {
		v.(interface{ free() }).free() // Free the outbox
	}
	clear(s.outboxMap)
	senderMapPool.Put(s.outboxMap)
	s.outboxMap = nil
}

var senderMapPool = sync.Pool{
	New: func() any { return make(map[any]any) },
}

// outboxFor returns the outbox for the given key, creating one if it doesn't exist.
func outboxFor[T any](s *Sender, q *Queue[T]) *outbox[T] {
	if s.outboxMap == nil {
		s.outboxMap = senderMapPool.Get().(map[any]any)
	}

	obAny := s.outboxMap[q]
	if obAny == nil {
		ob := newOutbox[T]()
		s.outboxMap[q] = ob
		return ob
	}
	return obAny.(*outbox[T])
}
