// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"reflect"
)

// A CustomPool is driven by a trait — an external value that supplies the object's lifecycle
// operations, keeping the pooled type's own API free of pool machinery. The trait's capabilities
// are à-la-carte, each its own interface, detected once at [ForCustom]:
//
//   - MakerTrait (required) — constructs a fresh object.
//   - ResetTrait (optional) — clears an object before reuse; absent ⇒ no reset.
//   - RefTrait (optional) — locates the object's counter (its [Ref]), making the pool
//     reference-managed. The trait only POINTS at the counter; the sealed lifecycle
//     (activate/release) is driven through it. If that Ref is generation-managed the object can
//     also back a [Handle] via [NewCustomHandle].

// MakerTrait constructs fresh pooled objects. It is the one required trait capability.
type MakerTrait[P any] interface {
	Make() P
}

// ResetTrait clears a pooled object's payload before reuse.
type ResetTrait[P any] interface {
	Reset(P)
}

// RefTrait maps a pooled object to its counter (a [Ref]), making the pool reference-managed. It
// works for either counter kind (a64 or a128); an a128-backed object additionally supports
// [NewCustomHandle], which asserts the Ref to a generation-guarded counter.
type RefTrait[P any] interface {
	RefCount(P) Ref
}

// CustomPool is a [basePool] whose lifecycle is resolved from a trait rather than by reflection
// over the object's own methods. It is the same engine as [Pool]; only the builder differs.
type CustomPool[T MakerTrait[P], P comparable] = basePool[P]

// ForCustom returns a shared pool instance for type P as managed by trait type T.
// Multiple calls with the same types T, P will return the same pool instance,
// enabling efficient sharing across different parts of the application.
// The trait parameter is used only for type inference; its value is ignored.
func ForCustom[T MakerTrait[P], P comparable](trait T) *CustomPool[T, P] {
	typ := reflect.TypeFor[T]()
	if p, ok := pools.Load(typ); ok {
		return p.(*CustomPool[T, P])
	}
	pool := &basePool[P]{
		newObject:      func() P { return trait.Make() },
		findRefCounter: resolveTraitRefCounterFinder[T, P](trait),
		reset:          resolveTraitResetter[T, P](trait),
	}
	actual, _ := pools.LoadOrStore(typ, pool)
	return actual.(*CustomPool[T, P])
}

func resolveTraitRefCounterFinder[T MakerTrait[P], P comparable](trait T) refCounterFinder[P] {
	if rt, ok := any(trait).(RefTrait[P]); ok {
		return func(o P) Ref { return rt.RefCount(o) }
	}
	return unmanaged[P]()
}

func resolveTraitResetter[T MakerTrait[P], P comparable](trait T) resetter[P] {
	if rt, ok := any(trait).(ResetTrait[P]); ok {
		return func(obj P) { rt.Reset(obj) }
	}
	return func(P) {}
}

// GetCustom is a package-level convenience that gets a custom pool and retrieves an object.
// For better performance, store and reuse a pool returned by [ForCustom].
// The trait parameter is used only for type inference; its value is ignored.
func GetCustom[T MakerTrait[P], P comparable](trait T) P {
	return ForCustom(trait).Get()
}

// ReleaseCustom is a package-level convenience that gets a pool and releases an object.
// For better performance, store and reuse a pool returned by [ForCustom].
// The trait parameter is used only for type inference; its value is ignored.
func ReleaseCustom[T MakerTrait[P], P comparable](trait T, obj P) {
	ForCustom(trait).Release(obj)
}

// NewCustomHandle mints a weak [Handle] to obj, locating its counter through a [RefTrait] — the
// trait-managed analog of [NewHandle], and the pool-free way to handle a clean object that
// exposes its counter only via the trait. It panics if the object's counter is not
// generation-managed (an a64 counter cannot back a handle). The trait value is used only to
// locate the counter; it carries no state.
func NewCustomHandle[T RefTrait[P], P comparable](trait T, obj P) Handle[P] {
	grc := genRefCounterOf(trait.RefCount(obj))
	return Handle[P]{p: obj, grc: grc, gen: grc.loadGen()}
}

// CustomAddRef takes an additional strong reference to obj through a [RefTrait] — the
// trait-managed analog of obj.RefCount().AddRef(), for a clean object that exposes its counter
// only via the trait. Works for either counter kind (a64 or a128).
func CustomAddRef[T RefTrait[P], P comparable](trait T, obj P) {
	trait.RefCount(obj).AddRef()
}
