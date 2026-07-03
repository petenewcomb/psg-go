// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import "github.com/petenewcomb/atomic128-go"

// counts packs a cache's (held, inUse) into a single 128-bit atomic word so the
// joint invariant 0 ≤ inUse ≤ held is preserved by every transition as one atomic
// step — a CAS on inUse alone could not check held. Both halves are uint64 amounts
// (not 32-bit unit counts), so a weighted Resource (e.g. a memory limiter above
// 4 GiB) is representable; the transitions take the weight w as a parameter, and the
// multi-source gather assembles weights the fast path cannot cover through
// deposit/stealOutUpTo (docs/decisions/weighted-acquisition.md, Decision 1).
//
// It reuses atomic128-go — the same primitive nbcq uses — but directly, with no
// atomic.Pointer GC-shadow: both halves are scalars, not pointers, so nothing here
// is opaque to the garbage collector.
//
// The transitions are the gated CAS loops of the locality-ordered acquire: each
// returns false (rather than blocking) when its gate fails, so a caller retries the
// next locality step. They are lock-free; the per-Pool lock (steal walk / destroy /
// child-list mutation) is separate and off this hot path.
type counts struct {
	w atomic128.Uint128 // [0] = held, [1] = inUse
}

// load returns the current (held, inUse) snapshot.
func (c *counts) load() (held, inUse uint64) {
	p := atomic128.LoadUint128(&c.w)
	return p[0], p[1]
}

// drain atomically takes all of held (requiring inUse == 0) and returns the amount,
// for destroy to return to the Resource. It loops against a concurrent stealOut: if a
// steal lowers held between the load and the CAS, drain retries and takes only what
// remains — the stolen permit is now accounted on the thief, so conservation holds
// without a lock. A non-zero inUse is a bug (destroy runs only at refs==0, when the
// cache is quiescent).
func (c *counts) drain() uint64 {
	for {
		p := atomic128.LoadUint128(&c.w)
		held, inUse := p[0], p[1]
		if inUse != 0 {
			panic("permits: drain of a cache with a running body (inUse != 0)")
		}
		if atomic128.CompareAndSwapUint128(&c.w, p, [2]uint64{0, 0}) {
			return held
		}
	}
}

// acquireLocal occupies w borrowable permits (inUse += w when inUse+w ≤ held) and
// reports success; false means this cache has too little idle to lend right now. The
// lock-free step-1 (own cache) / step-2 (ancestor) hit.
func (c *counts) acquireLocal(w uint64) bool {
	for {
		p := atomic128.LoadUint128(&c.w)
		held, inUse := p[0], p[1]
		if inUse+w > held {
			return false
		}
		if atomic128.CompareAndSwapUint128(&c.w, p, [2]uint64{held, inUse + w}) {
			return true
		}
	}
}

// checkout adds w freshly-obtained permits (from the Resource at step 3, or stolen
// in at step 4) to held and immediately occupies them (held += w, inUse += w).
func (c *counts) checkout(w uint64) {
	for {
		p := atomic128.LoadUint128(&c.w)
		if atomic128.CompareAndSwapUint128(&c.w, p, [2]uint64{p[0] + w, p[1] + w}) {
			return
		}
	}
}

// deposit adds w borrowable permits to held without occupying them (held += w) — the
// gather's transfer-in. Capacity stolen from a victim (stealOutUpTo) or granted by
// the Resource lands here as hoard, borrowable by anyone, until the gatherer's
// occupying acquireLocal covers its full weight (weighted-acquisition.md Decision 1:
// a partial gather is not hold-and-wait precisely because the hoard stays in held).
func (c *counts) deposit(w uint64) {
	for {
		p := atomic128.LoadUint128(&c.w)
		if atomic128.CompareAndSwapUint128(&c.w, p, [2]uint64{p[0] + w, p[1]}) {
			return
		}
	}
}

// depositOccupy adds n gathered permits to held and, in the SAME atomic step,
// occupies the full weight w if the deposit makes it coverable (held+n ≥ inUse+w),
// reporting whether it occupied. The single CAS is load-bearing for liveness, not
// just economy: a take that completes the weight must never sit borrowable in a
// window between deposit and occupy, or a racing acquirer can lift it and two
// weight-1 acquirers can bounce one permit between their caches forever (the old
// stealOut→checkout pair kept the permit hidden mid-transfer; this preserves that
// no-exposure property exactly when the gather is completing). A take that does NOT
// complete the weight deposits borrowable-only — the partial hoard's contestability
// is Decision 1's design, not a window.
func (c *counts) depositOccupy(n, w uint64) bool {
	for {
		p := atomic128.LoadUint128(&c.w)
		held, inUse := p[0]+n, p[1]
		if held >= inUse+w {
			if atomic128.CompareAndSwapUint128(&c.w, p, [2]uint64{held, inUse + w}) {
				return true
			}
		} else if atomic128.CompareAndSwapUint128(&c.w, p, [2]uint64{held, inUse}) {
			return false
		}
	}
}

// release lowers inUse by w — a body completed or parked. The permits stay in held
// (cache-don't-return), now borrowable. Reports whether the release raised borrowable
// from zero (held > inUse crossing), i.e. whether a waiter should be woken.
func (c *counts) release(w uint64) (wokeBorrowable bool) {
	for {
		p := atomic128.LoadUint128(&c.w)
		held, inUse := p[0], p[1]
		if inUse < w {
			panic("permits: release underflow (no running body backed by this cache)")
		}
		if atomic128.CompareAndSwapUint128(&c.w, p, [2]uint64{held, inUse - w}) {
			// borrowable went from (held-inUse) to (held-inUse+w); it crossed 0→
			// exactly when it was 0 before, i.e. inUse == held.
			return inUse == held
		}
	}
}

// stealOutUpTo removes up to w borrowable permits from this cache (held -= n,
// n = min(borrowable, w)) for transfer into another, returning n — still a single
// CAS. The partial take is what lets a weighted gather harvest fragmented capacity
// victim by victim instead of demanding one source that covers the whole weight. A
// zero return means nothing was borrowable at the take — a concurrent lock-free
// acquire may have consumed the idle permits since the steal walk observed them, so
// the caller must treat the walk's candidate as a hint and, on zero, look elsewhere.
func (c *counts) stealOutUpTo(w uint64) uint64 {
	for {
		p := atomic128.LoadUint128(&c.w)
		held, inUse := p[0], p[1]
		n := min(held-inUse, w) // held ≥ inUse is the standing invariant
		if n == 0 {
			return 0
		}
		if atomic128.CompareAndSwapUint128(&c.w, p, [2]uint64{held - n, inUse}) {
			return n
		}
	}
}
