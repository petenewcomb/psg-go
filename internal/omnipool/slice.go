// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"reflect"
	"sync"
)

// SlicePool is a type-safe wrapper around sync.Pool that handles slice types.
// It uses a two-pool design to efficiently manage slice recycling:
// - full: stores *T pointers to slices with preserved capacity
// - empty: recycles the *T pointer objects themselves
// This avoids pointer allocation overhead and preserves slice capacity.
type SlicePool[T ~[]E, E any] struct {
	full  sync.Pool // stores *T with slice content/capacity
	empty sync.Pool // recycles *T pointer objects
}

// ForSlice returns a shared pool instance for slice type T. Multiple calls with the same
// type will return the same pool instance, enabling efficient sharing across
// different parts of the application.
// The slice parameter is used only for type inference; its value is ignored.
func ForSlice[T ~[]E, E any](slice T) *SlicePool[T, E] {
	typ := reflect.TypeFor[T]()
	if p, ok := pools.Load(typ); ok {
		return p.(*SlicePool[T, E])
	}

	pool := &SlicePool[T, E]{}
	actual, _ := pools.LoadOrStore(typ, pool)
	return actual.(*SlicePool[T, E])
}

// Get retrieves a slice of type T from the pool. If the pool is empty,
// returns a nil slice. The returned slice has length 0 but may have capacity
// from previous use.
func (p *SlicePool[T, E]) Get() T {
	pooled := p.full.Get()
	if pooled != nil {
		ps := pooled.(*T)
		s := *ps
		*ps = nil
		p.empty.Put(ps)
		return s
	}
	return nil
}

// Put returns a slice to the pool after resetting its length to 0 while
// preserving capacity. Only slices with non-zero capacity are pooled.
// Put is safe to call with nil or zero-capacity slices, which are no-ops.
func (p *SlicePool[T, E]) Put(s T) {
	if cap(s) == 0 {
		return
	}
	pooled := p.empty.Get()
	var ps *T
	if pooled == nil {
		ps = new(T)
	} else {
		ps = pooled.(*T)
	}
	// Resize to full capacity and clear all elements to prevent memory leaks
	s = s[:cap(s)]
	clear(s)
	*ps = s[:0]
	p.full.Put(ps)
}

// GetSlice is a package-level convenience function that gets a slice pool and retrieves a slice.
// For better performance, store and reuse a pool returned by ForSlice.
// The slice parameter is used only for type inference; its value is ignored.
func GetSlice[T ~[]E, E any](slice T) T {
	pool := ForSlice(slice)
	return pool.Get()
}

// PutSlice is a package-level convenience function that gets a pool and puts a slice.
// For better performance, store and reuse a pool returned by ForSlice.
func PutSlice[T ~[]E, E any](slice T) {
	pool := ForSlice(slice)
	pool.Put(slice)
}
