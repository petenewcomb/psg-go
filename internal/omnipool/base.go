// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"sync"
)

// Global pools for all types
var pools sync.Map // map[reflect.Type]*Pool[T] (or *ChanPool[T] or *BufferedChanPool[C, E]; type-erased)

// basePool is the shared engine behind every omnipool front-end. It is parameterized over the
// OBJECT type O — the pointer a consumer holds (*T for the reflection [Pool], the trait's P for
// a [CustomPool]) — and drives a resolved set of operations: how to make a fresh object
// (maker), how to arm/drop its reference and how it recycles (lifecycler), and how to clear it
// before reuse (resetter). The resolution happens ONCE per type in the front-end's builder
// (For / ForCustom); the engine never re-detects capabilities, it just runs the ops.
type basePool[O comparable] struct {
	pool           sync.Pool
	newObject      maker[O]
	findRefCounter refCounterFinder[O]
	reset          resetter[O]
}

// maker constructs a fresh, fully-initialized object. Called only on a pool miss.
type maker[O any] func() O

// refCounterFinder locates an object's reference counter — the resolved-once strategy the
// engine drives. It returns nil for an unmanaged pool (so Get skips activation and Release
// always recycles); for a managed pool it returns the object's [Ref], reached either through the
// object's own RefCount() accessor or through a trait.
type refCounterFinder[O any] func(O) Ref

// unmanaged is the finder for a pool with no reference counting: every object maps to a nil
// counter, so Get skips activation and Release always recycles. (It must be a finder that
// RETURNS nil, not a nil finder — the engine calls it before the nil check.)
func unmanaged[O any]() refCounterFinder[O] {
	return func(O) Ref { return nil }
}

// resetter clears an object's payload before it returns to the pool. For a managed type it
// must clear field-wise and never touch the counter (a wholesale zero would destroy the
// generation); for an unmanaged type it may zero wholesale.
type resetter[O any] func(O)

// Get retrieves an object from the pool, creating one via the maker on a miss, and arms its
// owner reference (a no-op for an unmanaged type).
func (p *basePool[O]) Get() O {
	var obj O
	if pooled := p.pool.Get(); pooled != nil {
		obj = pooled.(O)
	} else {
		obj = p.newObject()
	}
	rc := p.findRefCounter(obj)
	if rc != nil {
		rc.activate()
	}
	return obj
}

// AddRef takes an additional strong reference to a live object. It panics for an unmanaged
// type. The pool-mediated form works even when the object exposes its counter only through a
// trait; a type that exposes the counter directly may equivalently call obj.RefCount().AddRef().
func (p *basePool[O]) AddRef(obj O) {
	rc := p.findRefCounter(obj)
	if rc == nil {
		panic("omnipool: AddRef on a non-reference-managed type")
	}
	rc.AddRef()
}

// NewHandle mints a weak [Handle] to obj by locating its counter through the pool. It works for
// any generation-managed pool — reflection [Pool] or trait [CustomPool] — and is the fallback
// when only the pool is at hand. It panics if the pool's objects are not generation-managed (an
// a64 or unmanaged pool cannot back a handle); the free [NewHandle] / [NewCustomHandle] mint the
// same handle.
func (p *basePool[O]) NewHandle(obj O) Handle[O] {
	grc := genRefCounterOf(p.findRefCounter(obj))
	return Handle[O]{p: obj, grc: grc, gen: grc.loadGen()}
}

// Release drops one reference to obj and recycles it — reset and returned to the pool — only
// when that was the last reference. For an unmanaged type every Release recycles it
// immediately. Release is safe to call with a nil object, which is a no-op.
func (p *basePool[O]) Release(obj O) {
	var zero O
	if obj == zero {
		return
	}
	rc := p.findRefCounter(obj)
	if rc == nil || rc.release() {
		p.reset(obj)
		// obj is always a pointer type (*T, or a pointer P), so this never boxes.
		p.pool.Put(obj) //nolint:staticcheck // SA6002: O is always pointer-like; see basePool doc
	}
}
