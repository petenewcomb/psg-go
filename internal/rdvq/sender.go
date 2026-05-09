// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Sender manages outboxes across multiple queues for a single goroutine.
// Each goroutine should have its own Sender instance to avoid races.
// The Sender automatically creates and manages outboxes as needed when
// sending to different Queue instances.
type Sender struct {
	outboxMap map[any]any
}

// Reset clears all outbox mappings and frees the outboxes, preparing the Sender for reuse.
func (s *Sender) Reset() {
	// Reset the map to allow reuse without reallocating
	for _, v := range s.outboxMap {
		v.(interface{ free() }).free() // Free the outbox
	}
	clear(s.outboxMap)
}

// outboxFor returns the outbox for the given key, creating one if it doesn't exist.
func outboxFor[T any](s *Sender, q *Queue[T]) *outbox[T] {
	if s.outboxMap == nil {
		s.outboxMap = make(map[any]any)
	}

	obAny := s.outboxMap[q]
	if obAny == nil {
		ob := newOutbox[T]()
		s.outboxMap[q] = ob
		return ob
	}
	return obAny.(*outbox[T])
}
