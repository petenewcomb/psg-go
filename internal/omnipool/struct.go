// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package omnipool provides a generic object pool implementation that automatically
// manages sync.Pool instances for different types using reflection.
//
// # Usage
//
// For best performance, get a pool reference once and reuse it:
//
//	nodePool := omnipool.For[node[T]]()
//	for i := 0; i < n; i++ {
//		obj := nodePool.Get()  // returns *node[T]
//		// ... use obj ...
//		nodePool.Put(obj)      // resets/zeros obj automatically
//	}
//
// # Object Lifecycle
//
// Objects retrieved from pools are guaranteed to be in a clean state:
// - If the type implements Resetter, Reset() is called on Put()
// - If the type implements Initer, Init() is called when creating new objects
// - Otherwise, objects are zeroed using *obj = *new(T) on Put()
//
// # Recommendations for Complex Types
//
// For channels, slices, maps, and other types with important behavioral
// characteristics (buffer sizes, capacities, etc.), wrap them in use-case
// specific types rather than pooling the raw types directly:
//
//	// Good: Predictable behavior, same buffer size
//	type WorkChannel struct {
//		ch chan Work  // always buffered to 100
//	}
//	func (w *WorkChannel) Reset() { /* drain and reset */ }
//
//	// Avoid: Unpredictable buffer sizes
//	omnipool.For[chan Work]()  // might get buffered or unbuffered channels
//
// This ensures pooled objects have consistent performance characteristics.
package omnipool

import (
	"reflect"
	"sync"
)

// Global pools for all types
var pools sync.Map // map[reflect.Type]*Pool[T] (or *ChanPool[T] or *BufferedChanPool[C, E]; type-erased)

// Initer defines the interface for objects that need initialization after creation.
type Initer interface {
	Init()
}

// Resetter defines the interface for objects that can reset themselves
// to a clean state for reuse.
type Resetter interface {
	Reset()
}

// Pool is a type-safe wrapper around sync.Pool that handles object creation,
// initialization, and resetting automatically.
type Pool[T any] struct {
	pool        sync.Pool
	hasReset    bool
	hasInit     bool
	hasRefCount bool
}

// For returns a shared pool instance for type T. Multiple calls with the same
// type will return the same pool instance, enabling efficient sharing across
// different parts of the application.
func For[T any]() *Pool[T] {
	typ := reflect.TypeFor[*T]()
	if p, ok := pools.Load(typ); ok {
		return p.(*Pool[T])
	}

	// Check which interfaces T implements
	initerType := reflect.TypeFor[Initer]()
	hasInit := typ.Implements(initerType)

	resetterType := reflect.TypeFor[Resetter]()
	hasReset := typ.Implements(resetterType)

	// A type that embeds RefCount and implements Resetter is reference-managed.
	// An embedder that omits Reset is simply not RefCounted and takes the
	// unmanaged path; that is harmless, since without a handle or AddRef its
	// reference count is never exercised, and using either fails to compile.
	hasRefCount := typ.Implements(reflect.TypeFor[RefCounted]())

	pool := &Pool[T]{
		hasReset:    hasReset,
		hasInit:     hasInit,
		hasRefCount: hasRefCount,
	}

	actual, _ := pools.LoadOrStore(typ, pool)
	return actual.(*Pool[T])
}

// Get retrieves a pointer to an object of type T from the pool. If the pool is empty,
// creates a new object using new(T) and calls [Initer.Init] if provided by T.
func (p *Pool[T]) Get() *T {
	pooled := p.pool.Get()
	if pooled != nil {
		obj := pooled.(*T)
		if p.hasRefCount {
			// Re-arm the recycled object with a single reference, preserving its
			// generation across the reuse.
			any(obj).(RefCounted).refCount().activate(false)
		}
		return obj
	}
	obj := new(T)
	if p.hasInit {
		any(obj).(Initer).Init()
	}
	if p.hasRefCount {
		any(obj).(RefCounted).refCount().activate(true)
	}
	return obj
}

// Clone gets an object from the pool and copies the provided value into it.
// This is a convenience method for the common pattern of [Get] + assignment.
func (p *Pool[T]) Clone(value T) *T {
	if p.hasRefCount {
		// Copying value over the object would overwrite (and copy) the embedded
		// RefCount, corrupting the generation and violating the non-copy rule.
		panic("omnipool: Clone is not supported for reference-managed types")
	}
	obj := p.Get()
	*obj = value
	return obj
}

// Release drops one reference to obj and returns it to the pool. For a
// reference-managed type (one embedding [RefCount]) the object is recycled only
// when the last reference is released; for an ordinary type every Release
// returns the object immediately. Release is safe to call with a nil pointer,
// which is a no-op.
func (p *Pool[T]) Release(obj *T) {
	if obj == nil {
		return
	}
	if p.hasRefCount {
		rc := any(obj).(RefCounted)
		if !rc.refCount().release() {
			// Not the last reference; the object stays live.
			return
		}
		// Last reference: clear payload field-wise (Resetter is mandatory for
		// managed types — never a wholesale zero, which would destroy the
		// embedded RefCount's generation) and recycle.
		rc.Reset()
		p.pool.Put(obj)
		return
	}
	if p.hasReset {
		any(obj).(Resetter).Reset()
	} else {
		// Zero the value if no Reset method
		*obj = *new(T)
	}
	p.pool.Put(obj)
}

// Get is a package-level convenience function that gets a pool and retrieves an object.
// For better performance, store and reuse a pool returned by [For].
func Get[T any]() *T {
	pool := For[T]()
	return pool.Get()
}

// Clone is a package-level convenience function that gets a pool and clones a value.
// For better performance, store and reuse a pool returned by [For].
func Clone[T any](value T) *T {
	pool := For[T]()
	return pool.Clone(value)
}

// Release is a package-level convenience function that gets a pool and releases an object.
// For better performance, store and reuse a pool returned by [For].
func Release[T any](obj *T) {
	pool := For[T]()
	pool.Release(obj)
}
