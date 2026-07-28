// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package heap provides a generic wrapper around the standard library heap.
package heap

import (
	"container/heap"
)

// Item is the interface for items stored in the heap.
//
// Position is a small tri-state used both by the heap (to find an item
// for in-place update or removal) and by callers (to tell an item's
// history apart):
//   - zero: never added to the heap;
//   - positive: the item's current 1-based index in the heap;
//   - negative: previously in the heap, since removed (by Pop or Remove).
//
// Re-adding a removed item via [Heap.Push] is supported — Push treats any
// non-positive position as "not currently present" and inserts afresh.
type Item[T any] interface {
	// Less returns true if this item should be ordered before the other item.
	Less(other T) bool
	// SetPosition is called by the heap whenever the item's position
	// changes: a positive 1-based index while in the heap, or a negative
	// sentinel when removed. Callers never call it directly.
	SetPosition(index int)
	// Position returns the item's current position per the tri-state above.
	Position() int
}

// removedPosition is the sentinel [Heap.Pop] assigns to an item on
// removal so callers can distinguish "previously in the heap" (negative)
// from "never added" (zero).
const removedPosition = -1

// Heap is a generic min-heap that stores items that implement the Item interface.
// The zero value is an empty heap ready to use without initialization.
type Heap[T Item[T]] struct {
	impl heapImpl[T]
}

// heapImpl is the internal implementation that satisfies container/heap.Interface
type heapImpl[T Item[T]] struct {
	items []T
}

// Len returns the number of items in the heap.
func (h *Heap[T]) Len() int {
	return len(h.impl.items)
}

// Push adds item to the heap, or — if item is already in the heap
// (Position > 0) — replaces the existing entry at that position with
// item and Fixes the heap. Re-Push is the supported way to update an
// in-heap entry's ordering state: callers either mutate fields on a
// pointer entry and re-Push the same pointer, or build a new value
// entry and re-Push it; either way, the slice slot ends up holding
// the new value before Fix re-evaluates Less.
func (h *Heap[T]) Push(item T) {
	p := item.Position()
	if p <= 0 {
		// Not currently present (never added, or removed since): insert
		// afresh. A negative position from a prior removal is treated the
		// same as a never-added zero.
		heap.Push(&h.impl, item)
	} else {
		h.impl.items[p-1] = item
		heap.Fix(&h.impl, p-1)
	}
}

// Pop removes and returns the minimum item from the heap.
func (h *Heap[T]) Pop() T {
	return heap.Pop(&h.impl).(T)
}

// Peek returns the minimum item from the heap without removing it.
// Returns the zero value of T if the heap is empty.
func (h *Heap[T]) Peek() T {
	if len(h.impl.items) == 0 {
		return *new(T)
	}
	return h.impl.items[0]
}

// Remove removes an item from the heap. Returns true if the item was removed,
// false if it was not in the heap.
func (h *Heap[T]) Remove(item T) bool {
	p := item.Position()
	if p <= 0 {
		// Never added or already removed.
		return false
	}
	heap.Remove(&h.impl, p-1)
	return true
}

// Reset clears the heap of its entries but retains the allocated capacity
func (h *Heap[T]) Reset() {
	clear(h.impl.items)
	h.impl.items = h.impl.items[:0]
}

// Implementation of container/heap.Interface for heapImpl

func (h *heapImpl[T]) Len() int {
	return len(h.items)
}

func (h *heapImpl[T]) Less(i, j int) bool {
	return h.items[i].Less(h.items[j])
}

func (h *heapImpl[T]) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.items[i].SetPosition(i + 1)
	h.items[j].SetPosition(j + 1)
}

func (h *heapImpl[T]) Push(x interface{}) {
	item := x.(T)
	item.SetPosition(len(h.items) + 1)
	h.items = append(h.items, item)
}

func (h *heapImpl[T]) Pop() interface{} {
	old := h.items
	n := len(old)
	item := old[n-1]
	old[n-1] = *new(T) // avoid memory leak
	h.items = old[0 : n-1]
	item.SetPosition(removedPosition)
	return item
}
