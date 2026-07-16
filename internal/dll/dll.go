// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package dll provides a generic intrusive doubly-linked list. Items
// embed [Links] to become linkable; the list itself allocates nothing.
// An item may belong to at most one list at a time, and every list
// operation is caller-guarded: the caller must serialize all access to
// a list and its linked items.
package dll

// Item is the constraint for items stored in a [List]. Embedding
// [Links] satisfies it automatically.
type Item[T any] interface {
	comparable
	// ListLinks returns the item's embedded Links. Callers never call
	// it directly; it exists for the list's own use.
	ListLinks() *Links[T]
}

// Links makes an item linkable into a [List]. Embed a Links field in
// the item type; its zero value is ready to use.
type Links[T any] struct {
	prev, next T
	list       any // the containing *List[T], nil while unlinked
}

// ListLinks implements [Item] on behalf of types that embed Links.
func (l *Links[T]) ListLinks() *Links[T] {
	return l
}

// Linked reports whether the item is currently in a list.
func (l *Links[T]) Linked() bool {
	return l.list != nil
}

// List is an intrusive doubly-linked list of items that embed [Links].
// The zero value is an empty list ready to use.
type List[T Item[T]] struct {
	front, back T
}

// Front returns the first item in the list, or the zero value of T if
// the list is empty.
func (l *List[T]) Front() T {
	return l.front
}

// Next returns the item after item, or the zero value of T if item is
// the last. item must be in the list.
func (l *List[T]) Next(item T) T {
	return item.ListLinks().next
}

// PushBack links item at the back of the list. It panics if item is
// already in any list.
func (l *List[T]) PushBack(item T) {
	ln := l.adopt(item)
	var zero T
	ln.prev = l.back
	if l.back != zero {
		l.back.ListLinks().next = item
	} else {
		l.front = item
	}
	l.back = item
}

// PushFront links item at the front of the list. It panics if item is
// already in any list.
func (l *List[T]) PushFront(item T) {
	ln := l.adopt(item)
	var zero T
	ln.next = l.front
	if l.front != zero {
		l.front.ListLinks().prev = item
	} else {
		l.back = item
	}
	l.front = item
}

// adopt claims an unlinked item for this list and returns its Links.
func (l *List[T]) adopt(item T) *Links[T] {
	ln := item.ListLinks()
	if ln.list != nil {
		panic("dll: item is already in a list")
	}
	ln.list = l
	return ln
}

// Remove unlinks item from the list, returning false if item is not in
// any list. It panics if item is in a different list. The item's Links
// are reset, so it may be pushed again afterward.
func (l *List[T]) Remove(item T) bool {
	ln := item.ListLinks()
	if ln.list == nil {
		return false
	}
	if ln.list != l {
		panic("dll: item is in a different list")
	}
	var zero T
	if ln.prev != zero {
		ln.prev.ListLinks().next = ln.next
	} else {
		l.front = ln.next
	}
	if ln.next != zero {
		ln.next.ListLinks().prev = ln.prev
	} else {
		l.back = ln.prev
	}
	ln.prev, ln.next, ln.list = zero, zero, nil
	return true
}
