// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"sync"
)

// SyncQueue is a thread-safe wrapper around Queue
type SyncQueue[T any] struct {
	mu sync.Mutex
	q  Queue[T]
}

// PushBack adds an item to the back of the queue
func (sq *SyncQueue[T]) PushBack(item T) {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	sq.q.PushBack(item)
}

// PopFront removes and returns an item from the front of the queue
// Returns the item and true if successful, zero value and false if empty
func (sq *SyncQueue[T]) PopFront() (T, bool) {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return sq.q.PopFront()
}

// Len returns the current queue length
func (sq *SyncQueue[T]) Len() int {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return sq.q.Len()
}
