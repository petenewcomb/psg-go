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
//     (no [Handle] / weak references). Cheaper: a plain signed a64 add, no atomic128, no CAS.
//   - [GenRefCounter] — a 128-bit (generation, refs) pair, REQUIRED by [Handle]. The
//     generation lets a referenceless holder discover, race-free, whether the object it
//     remembers has since been recycled and reused.
//
// A pooled type becomes reference-managed by exposing its counter through the single accessor
// [RefCounter.RefCount] / [GenRefCounter.RefCount] — embed the counter (which promotes the
// accessor) or hold it as a field and write the one-line accessor. Both accessors return a [Ref],
// so [RefCounted] (the interface the pool/[Handle] detect) needs only one method regardless of
// which counter a type holds. The pool drives the SEALED lifecycle (activate/release, unexported
// on Ref) through it, so a foreign type can opt in yet can never reimplement the race-critical
// protocol — it must hold one of omnipool's counters. When the generation is needed (only a
// [Handle] needs it), the Ref is type-asserted to *[GenRefCounter].
//
// Reaching AddRef: it is exported on Ref, so it is reached as obj.RefCount().AddRef(); an
// embedding type also promotes AddRef() directly, but omnipool never assumes that — it only ever
// requires RefCount().

const (
	refsWord = 0 // low-order element of the (gen, refs) pair
	genWord  = 1 // high-order element of the (gen, refs) pair
)

// Ref is the reference-counter handle a managed object exposes via RefCount(). AddRef is public
// (an additional strong clone); activate/release are unexported, so only omnipool's own counters
// satisfy Ref — a foreign type opts in by holding one of them, never by reimplementing the
// protocol. A Ref backing a generation-guarded object is type-asserted to *[GenRefCounter] when
// a [Handle] needs the generation.
type Ref interface {
	AddRef()       // take an additional strong reference (the infallible strong-to-strong clone)
	activate()     // Get: arm the owner reference
	release() bool // Release: drop one reference; true ⇒ recycle now
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
//
// The count is SIGNED so misuse is detectable with a bare atomic add: a strong-only counter has
// no weak upgraders and thus no resurrection race to guard against (unlike [GenRefCounter],
// whose packed gen+refs update forces a CAS), so AddRef/release each need only a single Add plus
// a sentinel check — a negative count means one release too many.
type RefCounter struct {
	refs atomic.Int64
}

// RefCount returns the counter as a [Ref] — the [RefCounted] accessor.
func (rc *RefCounter) RefCount() Ref { return rc }

func (rc *RefCounter) activate() { rc.refs.Store(1) }

// AddRef takes an additional strong reference — the infallible strong-to-strong clone (the
// caller's existing reference pins refs >= 1, so it cannot race a recycle). Reached as
// obj.RefCount().AddRef() (an embedding type also promotes it). A post-increment value of 1
// means the count was 0, so the call has no existing reference to clone from — a misuse, caught
// rather than silently resurrect.
func (rc *RefCounter) AddRef() {
	if rc.refs.Add(1) == 1 {
		panic("omnipool: AddRef on object with no outstanding reference")
	}
}

// TryAddRef takes an additional strong reference only if the object still has one (refs > 0),
// reporting success. It is the resurrection-refusing strong pin — the strong-side analog of
// [Handle.Get]'s weak upgrade, for a caller that holds a live pointer (typically found under an
// external lock) but must not revive an object whose last reference already dropped: such an
// object is committed to recycling, so an unconditional AddRef would resurrect it and cause a
// double-recycle. Unlike AddRef this needs a CAS: the load-then-increment must not straddle a
// concurrent drop to zero.
func (rc *RefCounter) TryAddRef() bool {
	for {
		n := rc.refs.Load()
		if n <= 0 {
			return false // committed to recycle — do not resurrect
		}
		if rc.refs.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

func (rc *RefCounter) release() (recycled bool) {
	n := rc.refs.Add(-1)
	if n < 0 {
		panic("omnipool: Release of object with no outstanding reference")
	}
	return n == 0
}

// RefExclusive reports whether this is the only reference (refs == 1) — the strong-side
// get_mut: a true result means the caller is the sole holder and may safely mutate the object
// in place rather than copy-on-write. Meaningful only to a caller that knows no other goroutine
// can concurrently AddRef (it has established sole custody); under concurrent mutation the
// answer is stale the instant it is read.
func (rc *RefCounter) RefExclusive() bool { return rc.refs.Load() == 1 }

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

// RefCount returns the counter as a [Ref] — the [RefCounted] accessor. A [Handle] recovers the
// generation-guarded operations by asserting the Ref back to *GenRefCounter.
func (g *GenRefCounter) RefCount() Ref { return g }

// loadGen returns the current generation (for [NewHandle] and [Handle.Valid]).
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
// generation across recycles: it loads the current generation and stores refs=1 alongside it.
// A freshly created object's generation is already 0, so the same load-and-store serves both
// the fresh and the reused case — no need to distinguish them. The object is single-owned at
// this point — a pooled-idle object at a bumped generation rejects every stale Handle.Get
// without touching the word — so a plain store is safe.
func (g *GenRefCounter) activate() {
	g.w.Store([2]uint64{1, g.w.Load()[genWord]})
}

// AddRef takes an additional strong reference (see [RefCounter.AddRef]); reached as
// obj.RefCount().AddRef().
func (g *GenRefCounter) AddRef() {
	for {
		w := g.w.Load()
		if w[refsWord] == 0 {
			panic("omnipool: AddRef on object with no outstanding reference")
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

// HandleP is the type-parameter constraint for the free [NewHandle]: a [RefCounted] type that is
// additionally comparable. NewHandle reaches the counter through the object's own RefCount()
// accessor and asserts it to a generation-guarded counter; a trait-managed object that lacks the
// accessor uses [NewCustomHandle], and when only a pool is at hand there is [Pool.NewHandle]. The
// [Handle] type itself needs only comparable — it holds a captured counter, not the accessor.
type HandleP interface {
	comparable
	RefCounted
}

// Handle is a copyable, referenceless ("weak") capture of a generation-guarded object: the
// object pointer, its counter, and the generation captured at mint. It outlives the object's
// recycling; [Handle.Get] reports whether the object it names is still the same incarnation.
// The counter is captured at mint so Get/Valid never re-locate it — the object need not expose
// an accessor, which is exactly what lets a trait-managed object back a Handle.
type Handle[P comparable] struct {
	p   P
	grc *GenRefCounter
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
	return h.p != zero && h.grc.loadGen() == h.gen
}

// Get upgrades a weak handle to a strong reference — the object pointer — if the object is
// still the incarnation the handle was minted against. On success the reference count is
// incremented and ok is true; on a generation mismatch nothing is touched and ok is false.
// Balance a successful Get with [Pool.Release].
func (h Handle[P]) Get() (obj P, ok bool) {
	if h.grc.upgrade(h.gen) {
		return h.p, true
	}
	var zero P
	return zero, false
}

// genRefCounterOf recovers the generation-guarded counter behind a Ref, or panics if the Ref
// belongs to an a64 (non-generation) counter, which cannot back a Handle. It is the single point
// where the runtime gen-check for handle minting lives.
func genRefCounterOf(r Ref) *GenRefCounter {
	grc, ok := r.(*GenRefCounter)
	if !ok {
		panic("omnipool: handle requires a generation-managed (a128) reference counter")
	}
	return grc
}

// NewHandle mints a weak handle to obj, capturing its counter through the object's own RefCount()
// accessor. It does not change the reference count. The caller should hold a live reference while
// minting (so the captured generation is meaningful), but a stale capture is harmless — a later
// [Handle.Get] fails. It panics if obj is not generation-managed (an a64 counter cannot back a
// handle). For a trait-managed object use [NewCustomHandle]; when only a pool is at hand,
// [Pool.NewHandle].
func NewHandle[P HandleP](obj P) Handle[P] {
	grc := genRefCounterOf(obj.RefCount())
	return Handle[P]{p: obj, grc: grc, gen: grc.loadGen()}
}
