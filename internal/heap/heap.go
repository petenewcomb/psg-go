// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package heap provides a generic wrapper around the standard library heap.
package heap

import (
	"container/heap"
)

// Item is the interface for items stored in the heap.
type Item[T any] interface {
	// Less returns true if this item should be ordered before the other item.
	Less(other T) bool
	// SetPosition is called when the item's position in the heap changes. Valid
	// positions are greater than zero; zero means not in the heap.
	SetPosition(index int)
	// Position returns the item's current index in the heap.
	Position() int
}

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

// Push adds an item to the heap.
func (h *Heap[T]) Push(item T) {
	p := item.Position()
	if p < 0 {
		panic("item reports invalid position")
	}
	if p == 0 {
		heap.Push(&h.impl, item)
	} else {
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
		var zero T
		return zero
	}
	return h.impl.items[0]
}

// Remove removes an item from the heap. Returns true if the item was removed,
// false if it was not in the heap.
func (h *Heap[T]) Remove(item T) bool {
	p := item.Position()
	if p < 0 {
		panic("item reports invalid position")
	}
	if p == 0 {
		return false
	}
	heap.Remove(&h.impl, p-1)
	return true
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
	item.SetPosition(0)
	return item
}
