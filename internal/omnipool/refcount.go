// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"github.com/petenewcomb/atomic128-go"
)

// RefCount makes a pooled type reference-managed. Embed it in a struct T and the
// pool for T (via [For]) hands out one reference at [Pool.Get] and recycles the
// object only when the last reference is released — deferring reuse past any
// straggler that still holds the object.
//
// It carries an atomic (gen, refs) pair: refs is the object-lifetime reference
// count, and gen is the incarnation identity, bumped every time the object is
// recycled. A [Handle] captures gen so a referenceless holder can later attempt
// to reacquire and discover — race-free, in a single CAS — whether the object it
// remembers has since been reused for something else.
//
// The pair lives in one 128-bit atomic word so that the last-reference recycle
// bumps gen and zeroes refs together, atomically: a concurrent upgrade
// ([Handle.Get]) either lands its increment before the recycle (which then fails
// and retries, sees refs > 0, and does not recycle) or after (it reloads, sees
// the new gen, and fails cleanly). There is no separate "retired" flag and no
// distinguished retirement step — the owner's own held reference keeps refs > 0
// until it is done, so recycle can never fire early.
//
// RefCount must not be copied (the embedded 128-bit atomic must not be copied),
// so a managed type is inherently non-copyable. Its [Resetter], if any, must
// clear payload field-wise and must NOT touch the embedded RefCount — the gen
// must survive recycling, so managed objects are never zeroed wholesale.
type RefCount struct {
	// w is the packed (gen, refs) pair: w[refsWord] is the reference count,
	// w[genWord] is the incarnation identity. See [atomic128.Uint128].
	w atomic128.Uint128
}

const (
	refsWord = 0 // low-order element of the pair
	genWord  = 1 // high-order element of the pair
)

// refCount is the unexported accessor that seals [RefCounted]: only a type
// embedding RefCount (in package omnipool) can satisfy the interface, so no
// foreign type can be wrapped in a [Handle] or passed to [AddRef] by accident.
func (rc *RefCount) refCount() *RefCount { return rc }

// RefCounted is satisfied by pointer types *T whose T embeds [RefCount] and
// implements [Resetter]. It is the constraint for [Handle], [NewHandle], and
// [AddRef]; because RefCount's accessor has a pointer receiver, only *T — never
// T — satisfies it, so the managed handle API is parameterized over the pointer
// type P.
//
// Resetter is mandatory because a managed object is recycled in place: on the
// last release its payload must be cleared field-wise (never wholesale, which
// would copy and zero the embedded RefCount and destroy the generation). A type
// that embeds RefCount without implementing Reset simply is not RefCounted, so
// it cannot be handed to [NewHandle] or [AddRef] — the compiler rejects it at
// the only sites where the reference count is actually used.
type RefCounted interface {
	Resetter
	refCount() *RefCount
}

// Handle is a copyable, referenceless ("weak") capture of a reference-managed
// object: the object pointer plus the generation it was captured at. It outlives
// the object's recycling; [Handle.Get] reports whether the object it names is
// still the same incarnation.
type Handle[P RefCounted] struct {
	p   P
	gen uint64
}

// NewHandle mints a weak handle to obj at its current generation. It does not
// change the reference count. The caller should hold a live reference while
// minting (so the captured generation is meaningful), but a stale capture is
// harmless — a later [Handle.Get] simply fails.
func NewHandle[P RefCounted](obj P) Handle[P] {
	w := atomic128.LoadUint128(&obj.refCount().w)
	return Handle[P]{p: obj, gen: w[genWord]}
}

// Get upgrades a weak handle to a strong reference — the object pointer — if the
// object is still the incarnation the handle was minted against. It is the
// fallible, weak-to-strong path: on success the reference count is incremented
// and ok is true; on a generation mismatch nothing is touched and ok is false.
// Balance a successful Get with [Pool.Release].
func (h Handle[P]) Get() (obj P, ok bool) {
	rc := h.p.refCount()
	for {
		w := atomic128.LoadUint128(&rc.w)
		if w[genWord] != h.gen {
			// The object has been recycled into a different incarnation (or is
			// idle in the pool at a bumped generation). Touch nothing.
			var zero P
			return zero, false
		}
		// A matching generation guarantees refs >= 1: recycling bumps gen and
		// zeroes refs in the same CAS, so refs can never be 0 while gen matches.
		next := [2]uint64{w[refsWord] + 1, w[genWord]}
		if atomic128.CompareAndSwapUint128(&rc.w, w, next) {
			return h.p, true
		}
		// refs changed under us but gen still matched at load; retry.
	}
}

// AddRef takes an additional strong reference to an object the caller already
// holds a strong reference to. It is the infallible, strong-to-strong path: the
// caller's existing reference pins refs >= 1 and freezes the generation for the
// duration of the call, so the increment cannot race a recycle and cannot fail.
// Balance each AddRef with a [Pool.Release].
//
// Its precondition is a live reference; calling it on an object whose count has
// already reached zero would resurrect a recycled object, so it panics rather
// than increment from zero.
func AddRef[P RefCounted](obj P) {
	rc := obj.refCount()
	for {
		w := atomic128.LoadUint128(&rc.w)
		if w[refsWord] == 0 {
			panic("omnipool: AddRef on object with no outstanding reference")
		}
		next := [2]uint64{w[refsWord] + 1, w[genWord]}
		if atomic128.CompareAndSwapUint128(&rc.w, w, next) {
			return
		}
	}
}

// activate initializes a managed object's word to hold a single reference (the
// caller's), preserving the generation across recycles. For a freshly created
// object (gen 0) it stores the initial word; for one drawn from the pool it sets
// refs from 0 to 1, keeping the generation the last recycle left behind. The
// object is single-owned at this point — a pooled-idle object at a bumped
// generation rejects every stale Handle.Get without touching the word — so a
// plain store is safe.
func (rc *RefCount) activate(fresh bool) {
	gen := uint64(0)
	if !fresh {
		gen = atomic128.LoadUint128(&rc.w)[genWord]
	}
	atomic128.StoreUint128(&rc.w, [2]uint64{1, gen})
}

// release drops one reference. On the last one it recycles: a single CAS bumps
// the generation and zeroes refs, after which the object is invisible to every
// stale handle and safe to reset and return to the pool. It reports whether this
// call performed the recycle.
func (rc *RefCount) release() (recycled bool) {
	for {
		w := atomic128.LoadUint128(&rc.w)
		refs := w[refsWord]
		if refs == 0 {
			panic("omnipool: Release of object with no outstanding reference")
		}
		if refs == 1 {
			next := [2]uint64{0, w[genWord] + 1}
			if atomic128.CompareAndSwapUint128(&rc.w, w, next) {
				return true
			}
		} else {
			next := [2]uint64{refs - 1, w[genWord]}
			if atomic128.CompareAndSwapUint128(&rc.w, w, next) {
				return false
			}
		}
	}
}
