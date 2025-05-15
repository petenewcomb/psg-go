// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

// Queue is a FIFO queue implemented as a ring buffer
// that grows as needed. The zero value is ready to use.
type Queue[T any] struct {
	items []T
	front int // index of the first element
	back  int // index of the next free slot
}

// PushBack adds an item to the back of the queue
func (q *Queue[T]) PushBack(item T) {
	capacity := q.capacity()
	if q.Len() == capacity {
		capacity = q.grow()
	}

	q.items[q.back%capacity] = item
	q.back++
}

// PopFront removes and returns an item from the front of the queue
// Returns the item and true if successful, zero value and false if empty
func (q *Queue[T]) PopFront() (T, bool) {
	if q.front == q.back {
		// Queue is empty
		var zero T
		return zero, false
	}

	item := q.items[q.front]
	var zero T
	q.items[q.front] = zero // help GC by clearing the reference
	q.front++

	// Reset counters when front reaches capacity to prevent integer overflow on long-running queues
	// This maintains the same logical state (back-front remains the same) but resets the counters
	capacity := q.capacity()
	if q.front == capacity {
		q.front = 0
		q.back -= capacity
	}

	return item, true
}

func (q *Queue[T]) capacity() int {
	return len(q.items)
}

// grow doubles the capacity of the queue.  Returns the new capacity.
func (q *Queue[T]) grow() int {
	oldCapacity := q.capacity()
	var newItems []T
	if oldCapacity == 0 {
		// Use default initial slice capacity
		var zero T
		newItems = append(q.items, zero)
		newItems = newItems[:cap(newItems)]
	} else {
		newItems = make([]T, oldCapacity*2)
		// Copy elements in order, starting from front
		size := q.Len()
		for i := 0; i < size; i++ {
			newItems[i] = q.items[(q.front+i)%oldCapacity]
		}
		q.front = 0
		q.back = size
	}
	q.items = newItems
	return q.capacity()
}

// Len returns the number of elements in the queue
func (q *Queue[T]) Len() int {
	return q.back - q.front
}
