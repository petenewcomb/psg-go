// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package ctxmap provides utilities for associating values with contexts
// with caching and automatic cleanup.
package ctxmap

import (
	"context"
	"sync"
)

// entry stores a cached value along with the context that contains it
// and the cleanup function to cancel the AfterFunc.
type entry[T comparable] struct {
	value      T
	stampedCtx context.Context //nolint:containedctx // context stamped with the value
	stop       func() bool     // from context.AfterFunc
	cache      *sync.Map
}

func (e *entry[T]) remove() {
	e.cache.CompareAndDelete(e.stampedCtx, e)
}

// Map provides cached mapping of contexts to computed values with automatic cleanup.
type Map[K any, T comparable] struct {
	cache sync.Map // context.Context -> entry[T]
}

// Get retrieves or computes a value for the given context.
// Returns a Result containing the value and a context that has the value available.
// If the value was computed, it's cached and cleanup is automatically registered.
// Uses LoadOrStore pattern to handle race conditions.
func (m *Map[K, T]) WithValue(
	ctx context.Context,
	computeFn func(T, bool) (context.Context, T),
) (stampedCtx context.Context, value T) {
	// Check cache first to avoid expensive computation
	if cached, ok := m.cache.Load(ctx); ok {
		entry := cached.(*entry[T])
		return entry.stampedCtx, entry.value
	}

	if computeFn == nil {
		return nil, *new(T)
	}

	var key K // The key is the type, so the zero value will suffice

	// Check if the context already has the value via context.Value
	var sourceValue T
	haveSourceValue := false
	ctxValue := ctx.Value(key)
	if ctxValue != nil {
		sourceValue = ctxValue.(T)
		haveSourceValue = true
	}

	// Compute new value
	computedCtx, value := computeFn(sourceValue, haveSourceValue)

	newStampedCtx := computedCtx
	if computedCtx != ctx || !haveSourceValue || value != sourceValue {
		newStampedCtx = context.WithValue(computedCtx, key, value)
	}

	newEntry := &entry[T]{
		value:      value,
		stampedCtx: newStampedCtx,
		cache:      &m.cache,
	}

	// Use LoadOrStore to handle race condition where another goroutine
	// might have stored while we were computing
	if actual, loaded := m.cache.LoadOrStore(ctx, newEntry); loaded {
		actualEntry := actual.(*entry[T])
		return actualEntry.stampedCtx, actualEntry.value
	}

	// Set up cleanup. If the ctx is already canceled, this will immediately
	// remove it from the cache.
	newEntry.stop = context.AfterFunc(ctx, newEntry.remove)

	// Go ahead and add a cache entry for the stamped context to avoid a
	// slow-path lookup on first fetch. Use LoadOrStore to handle race condition
	// where another goroutine might have loaded the new entry and already
	// cached it under the stamped context before we got here. We must use a new
	// entry object because the stop function will be different.
	newStampedEntry := &entry[T]{
		value:      value,
		stampedCtx: newStampedCtx,
		cache:      &m.cache,
	}
	if _, loaded := m.cache.LoadOrStore(newStampedCtx, newStampedEntry); !loaded {
		// Set up cleanup. If the ctx is already canceled, this will immediately
		// remove it from the cache.
		newStampedEntry.stop = context.AfterFunc(newStampedCtx, newStampedEntry.remove)
	}

	// We successfully stored our result
	return newStampedCtx, value
}

// Clear cancels all AfterFunc cleanup functions and clears the cache.
// This should be called when the Map is no longer needed to prevent
// resource leaks.
func (m *Map[K, T]) Clear() {
	m.cache.Range(func(_, value any) bool {
		value.(*entry[T]).stop() // Cancel the AfterFunc that would call remove
		return true
	})
	m.cache.Clear()
}
