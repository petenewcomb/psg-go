// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"sync/atomic"

	"github.com/petenewcomb/atomic128-go"
)

// Reference counting comes in two counter structs, differing only in whether they carry a
// generation:
//
//   - [RefCounter] — a single 64-bit counter, for pooled types with ONLY strong holders
//     (no [Handle] / weak references). Cheaper: an a64 CAS, no atomic128.
//   - [GenRefCounter] — a 128-bit (generation, refs) pair, REQUIRED by [Handle]. The
//     generation lets a referenceless holder discover, race-free, whether the object it
//     remembers has since been recycled and reused.
//
// A pooled type becomes reference-managed by exposing its counter through an accessor
// ([RefCounter.RefCount] / [GenRefCounter.GenRefCount]) — embed the counter (which promotes
// the accessor) or hold it as a field and write the one-line accessor. The pool then drives
// the counter's SEALED lifecycle (activate/release, unexported) through that accessor, so a
// foreign type can opt in yet can never reimplement the race-critical protocol — it must
// hold one of omnipool's counters. The trait interfaces the pool/[Handle] type-assert are
// [RefCounted] and [GenRefCounted].
//
// Naming: the counter STRUCTs are [RefCounter]/[GenRefCounter]; their ACCESSORS are
// RefCount()/GenRefCount(); the trait INTERFACES are [RefCounted]/[GenRefCounted]. (The
// distinct struct and accessor names are also what let a type embed the counter without the
// embedded field shadowing the promoted accessor method.)

const (
	refsWord = 0 // low-order element of the (gen, refs) pair
	genWord  = 1 // high-order element of the (gen, refs) pair
)

// RefCounted is the trait interface the pool type-asserts to drive a reference-managed type:
// it returns the type's [RefCounter]. A [Resetter] is also required (the pool clears payload
// field-wise on recycle). Only *T — never T — satisfies it (the accessor has a pointer
// receiver). A foreign type satisfies it by embedding or holding an omnipool counter, never
// by reimplementing the sealed lifecycle.
type RefCounted interface {
	Resetter
	RefCount() *RefCounter
}

// GenRefCounted is the trait for a generation-guarded type — it returns a [GenRefCounter],
// required to back a [Handle]. (It is not a subtype of [RefCounted]: a Gen type exposes
// GenRefCount(), and the pool/Handle drive it through that.)
type GenRefCounted interface {
	Resetter
	GenRefCount() *GenRefCounter
}

// ─────────────────────────────────────────────────────────────────────────────
// RefCounter — the lightweight a64 counter (no generation).
// ─────────────────────────────────────────────────────────────────────────────

// RefCounter is the a64 reference counter: a single 64-bit count, for pooled types with ONLY
// strong holders (no [Handle]s). Expose it via the [RefCounter.RefCount] accessor — embed it
// (which promotes RefCount()) or hold it and return &field. Without a generation there is no
// way to detect a recycle-and-reuse across a stale reference, which is why RefCounter cannot
// back a [Handle]; a type needing handles uses [GenRefCounter].
//
// The embedded atomic must not be copied, so a managed type is inherently non-copyable. Its
// [Resetter] must clear payload field-wise and must NOT touch the RefCounter.
type RefCounter struct {
	refs atomic.Uint64
}

// RefCount returns the counter itself — the [RefCounted] accessor.
func (rc *RefCounter) RefCount() *RefCounter { return rc }

func (rc *RefCounter) activate(bool) { rc.refs.Store(1) } // no gen to preserve; fresh is irrelevant

// Inc takes an additional strong reference — the infallible strong-to-strong clone (the
// caller's existing reference pins refs >= 1, so it cannot race a recycle). Reached via the
// accessor: obj.RefCount().Inc(). Calling it on an object whose count has reached zero panics
// rather than resurrect it.
func (rc *RefCounter) Inc() {
	for {
		r := rc.refs.Load()
		if r == 0 {
			panic("omnipool: Inc on object with no outstanding reference")
		}
		if rc.refs.CompareAndSwap(r, r+1) {
			return
		}
	}
}

func (rc *RefCounter) release() (recycled bool) {
	for {
		r := rc.refs.Load()
		if r == 0 {
			panic("omnipool: Release of object with no outstanding reference")
		}
		if rc.refs.CompareAndSwap(r, r-1) {
			return r == 1
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GenRefCounter — the generation-guarded a128 (gen, refs) pair, required by Handle.
// ─────────────────────────────────────────────────────────────────────────────

// GenRefCounter is [RefCounter] plus a generation, required to back a [Handle]. It carries an
// atomic (gen, refs) pair: refs is the object-lifetime reference count, and gen is the
// incarnation identity, bumped every time the object is recycled. A [Handle] captures gen so
// a referenceless holder can later attempt to reacquire and discover — race-free, in a single
// CAS — whether the object it remembers has since been reused for something else.
//
// The pair lives in one 128-bit atomic word so that the last-reference recycle bumps gen and
// zeroes refs together, atomically: a concurrent upgrade ([Handle.Get]) either lands its
// increment before the recycle (which then fails and retries, sees refs > 0, and does not
// recycle) or after (it reloads, sees the new gen, and fails cleanly). There is no separate
// "retired" flag — the owner's own held reference keeps refs > 0 until it is done, so recycle
// can never fire early.
//
// The embedded 128-bit atomic must not be copied, so a managed type is inherently
// non-copyable. Its [Resetter] must clear payload field-wise and must NOT touch the
// GenRefCounter — the gen must survive recycling, so managed objects are never zeroed
// wholesale.
type GenRefCounter struct {
	// w is the packed (gen, refs) pair: w[refsWord] is the reference count,
	// w[genWord] is the incarnation identity. See [atomic128.Uint128].
	w atomic128.Uint128
}

// GenRefCount returns the counter itself — the [GenRefCounted] accessor.
func (g *GenRefCounter) GenRefCount() *GenRefCounter { return g }

// loadGen returns the current generation (for [NewHandle] and [Handle.Valid]); unexported,
// reached only inside omnipool via the accessor.
func (g *GenRefCounter) loadGen() uint64 { return g.w.Load()[genWord] }

// upgrade is the fallible weak→strong path behind [Handle.Get]: if the object is still the
// captured incarnation (gen matches) it increments refs and returns true; on a generation
// mismatch it touches nothing and returns false.
func (g *GenRefCounter) upgrade(gen uint64) (ok bool) {
	for {
		w := g.w.Load()
		if w[genWord] != gen {
			// Recycled into a different incarnation (or idle in the pool at a bumped
			// generation). Touch nothing.
			return false
		}
		// A matching generation guarantees refs >= 1: recycling bumps gen and zeroes refs
		// in the same CAS, so refs can never be 0 while gen matches.
		if g.w.CompareAndSwap(w, [2]uint64{w[refsWord] + 1, w[genWord]}) {
			return true
		}
		// refs changed under us but gen still matched at load; retry.
	}
}

// activate initializes the word to hold a single reference (the caller's), preserving the
// generation across recycles. For a freshly created object (gen 0) it stores the initial
// word; for one drawn from the pool it sets refs from 0 to 1, keeping the generation the last
// recycle left behind. The object is single-owned at this point — a pooled-idle object at a
// bumped generation rejects every stale Handle.Get without touching the word — so a plain
// store is safe.
func (g *GenRefCounter) activate(fresh bool) {
	gen := uint64(0)
	if !fresh {
		gen = g.w.Load()[genWord]
	}
	g.w.Store([2]uint64{1, gen})
}

// Inc takes an additional strong reference (see [RefCounter.Inc]); reached via
// obj.GenRefCount().Inc().
func (g *GenRefCounter) Inc() {
	for {
		w := g.w.Load()
		if w[refsWord] == 0 {
			panic("omnipool: Inc on object with no outstanding reference")
		}
		if g.w.CompareAndSwap(w, [2]uint64{w[refsWord] + 1, w[genWord]}) {
			return
		}
	}
}

// release drops one reference. On the last one it recycles: a single CAS bumps the generation
// and zeroes refs, after which the object is invisible to every stale handle and safe to
// reset and return to the pool. It reports whether this call performed the recycle.
func (g *GenRefCounter) release() (recycled bool) {
	for {
		w := g.w.Load()
		refs := w[refsWord]
		if refs == 0 {
			panic("omnipool: Release of object with no outstanding reference")
		}
		if refs == 1 {
			if g.w.CompareAndSwap(w, [2]uint64{0, w[genWord] + 1}) {
				return true
			}
		} else {
			if g.w.CompareAndSwap(w, [2]uint64{refs - 1, w[genWord]}) {
				return false
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Handle — a weak, generation-guarded capture (requires a GenRefCounter).
// ─────────────────────────────────────────────────────────────────────────────

// HandleP is the type-parameter constraint for [Handle] and [NewHandle]: a [GenRefCounted]
// type that is additionally comparable. A managed type is always a pointer (*T), so this is
// free; requiring it lets a Handle compare by identity ([Handle.Is]) and serve as a map key.
// comparable is a separate constraint (not embedded into GenRefCounted) because that
// interface is also used as an ordinary interface value (the pool's type assertions), which a
// comparable-embedding interface — being constraint-only — may not be.
type HandleP interface {
	comparable
	GenRefCounted
}

// Handle is a copyable, referenceless ("weak") capture of a generation-guarded object: the
// object pointer plus the generation it was captured at. It outlives the object's recycling;
// [Handle.Get] reports whether the object it names is still the same incarnation.
type Handle[P HandleP] struct {
	p   P
	gen uint64
}

// Is reports whether the handle names obj — a pointer-identity comparison that does NOT
// consult the generation. It is the "synchronous / live" identity test: the caller holds obj
// live, so the handle either names that same live incarnation or a different object entirely;
// the gen guard is harmless. For a referenceless cross-lifetime test, mint a fresh handle and
// compare (h == NewHandle(live)) so the generation participates.
func (h Handle[P]) Is(obj P) bool { return h.p == obj }

// Empty reports whether the handle names no object (the zero Handle). It says nothing about
// liveness of a bound object; use [Handle.Valid] for that.
func (h Handle[P]) Empty() bool {
	var zero P
	return h.p == zero
}

// Valid reports whether the handle names a bound object that is still the incarnation it was
// captured against (referenceless: it takes no reference, unlike [Handle.Get]).
func (h Handle[P]) Valid() bool {
	var zero P
	return h.p != zero && h.p.GenRefCount().loadGen() == h.gen
}

// NewHandle mints a weak handle to obj at its current generation. It does not change the
// reference count. The caller should hold a live reference while minting (so the captured
// generation is meaningful), but a stale capture is harmless — a later [Handle.Get] fails.
func NewHandle[P HandleP](obj P) Handle[P] {
	return Handle[P]{p: obj, gen: obj.GenRefCount().loadGen()}
}

// Get upgrades a weak handle to a strong reference — the object pointer — if the object is
// still the incarnation the handle was minted against. On success the reference count is
// incremented and ok is true; on a generation mismatch nothing is touched and ok is false.
// Balance a successful Get with [Pool.Release].
func (h Handle[P]) Get() (obj P, ok bool) {
	if h.p.GenRefCount().upgrade(h.gen) {
		return h.p, true
	}
	var zero P
	return zero, false
}
