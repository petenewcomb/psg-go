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
)

// Initer defines the interface for objects that need initialization after creation.
type Initer interface {
	Init()
}

// RefCounted is the single accessor interface a reference-managed type exposes: it returns a
// [Ref]. It is a PURE accessor — Reset is orthogonal (provided by the object for the reflection
// [Pool], or by the trait for a [CustomPool]), so it is not bundled here. Only *T — never T —
// satisfies it (the accessor has a pointer receiver). A foreign type satisfies it by embedding
// or holding an omnipool counter, never by reimplementing the sealed lifecycle. Whether the
// object is generation-managed (able to back a [Handle]) is discovered by asserting the returned
// Ref to *[GenRefCounter], not by a separate accessor.
type RefCounted interface {
	RefCount() Ref
}

type Copier[T any] interface {
	CopyFrom(T)
}

// Resetter defines the interface for objects that can reset themselves
// to a clean state for reuse.
type Resetter interface {
	Reset()
}

// Pool is the reflection front-end: a type-safe [basePool] over *T that detects T's
// capabilities (Initer, Resetter, RefCounted, Copier) by reflection. It adds the value-typed
// [Pool.Clone] convenience, which the object-typed engine cannot express.
type Pool[T any] struct {
	basePool[*T]
	// copier performs Clone's value copy. It is nil exactly for a reference-managed type
	// without a CopyFrom method — such a type cannot be byte-copied (that would clobber the
	// counter's generation), so Clone is unsupported and panics.
	copier copier[T]
}

// copier copies a value into a freshly-obtained object for [Pool.Clone].
type copier[T any] func(dst *T, src T)

// For returns a shared pool instance for type T. Multiple calls with the same
// type will return the same pool instance, enabling efficient sharing across
// different parts of the application.
func For[T any]() *Pool[T] {
	typ := reflect.TypeFor[*T]()
	if p, ok := pools.Load(typ); ok {
		return p.(*Pool[T])
	}
	pool := &Pool[T]{
		basePool: basePool[*T]{
			newObject:      resolveMaker[T](typ),
			findRefCounter: resolveRefCounterFinder[T](typ),
			reset:          resolveResetter[T](typ),
		},
		copier: resolveCopier[T](typ),
	}
	actual, _ := pools.LoadOrStore(typ, pool)
	return actual.(*Pool[T])
}

// Clone gets an object from the pool and copies value into it — the common Get + assignment
// pattern. It panics for a reference-managed type without a CopyFrom method, since byte-copying
// such a type would corrupt its counter.
func (p *Pool[T]) Clone(value T) *T {
	if p.copier == nil {
		panic("omnipool: Clone is not supported for a reference-managed type without CopyFrom")
	}
	obj := p.Get()
	p.copier(obj, value)
	return obj
}

func resolveMaker[T any](typ reflect.Type) maker[*T] {
	if typ.Implements(reflect.TypeFor[Initer]()) {
		return func() *T {
			obj := new(T)
			any(obj).(Initer).Init()
			return obj
		}
	}
	return func() *T {
		return new(T)
	}
}

func resolveRefCounterFinder[T any](typ reflect.Type) refCounterFinder[*T] {
	if typ.Implements(reflect.TypeFor[RefCounted]()) {
		return func(o *T) Ref { return any(o).(RefCounted).RefCount() }
	}
	return unmanaged[*T]()
}

func resolveCopier[T any](typ reflect.Type) copier[T] {
	if typ.Implements(reflect.TypeFor[Copier[T]]()) {
		return func(obj *T, value T) {
			any(obj).(Copier[T]).CopyFrom(value)
		}
	}
	// A reference-managed type must not be byte-copied — it would clobber the counter's
	// generation — so leave copier nil and let Clone panic with a clear message.
	if typ.Implements(reflect.TypeFor[RefCounted]()) {
		return nil
	}
	return func(obj *T, value T) {
		*obj = value
	}
}

func resolveResetter[T any](typ reflect.Type) resetter[*T] {
	if typ.Implements(reflect.TypeFor[Resetter]()) {
		return func(obj *T) {
			any(obj).(Resetter).Reset()
		}
	}
	// A reference-managed type must clear field-wise via Reset; without one, do nothing rather
	// than byte-zero (which would destroy the counter's generation). Real managed types always
	// implement Resetter — this is a defensive no-op.
	if typ.Implements(reflect.TypeFor[RefCounted]()) {
		panic("omnipool: reference-managed type does not provide Reset()")
	}
	return func(obj *T) {
		*obj = *new(T)
	}
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
func Release[T any](obj *T) (recycled bool) {
	pool := For[T]()
	return pool.Release(obj)
}
