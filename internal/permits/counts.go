// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import "github.com/petenewcomb/atomic128-go"

// counts packs a cache's (held, inUse) into a single 128-bit atomic word so the
// joint invariant 0 ≤ inUse ≤ held is preserved by every transition as one atomic
// step — a CAS on inUse alone could not check held. Both halves are uint64 amounts
// (not 32-bit unit counts), so a weighted Resource (e.g. a memory limiter above
// 4 GiB) is representable; the operations below are weight-1 for now, and weights
// slot in later by parameterizing the deltas, not the layout.
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

// acquireLocal occupies one borrowable permit (inUse++ when inUse < held) and
// reports success; false means this cache has nothing idle to lend right now. The
// lock-free step-1 (own cache) / step-2 (ancestor) hit.
func (c *counts) acquireLocal() bool {
	for {
		p := atomic128.LoadUint128(&c.w)
		held, inUse := p[0], p[1]
		if inUse >= held {
			return false
		}
		if atomic128.CompareAndSwapUint128(&c.w, p, [2]uint64{held, inUse + 1}) {
			return true
		}
	}
}

// checkout adds one freshly-obtained permit (from the Resource at step 3, or stolen
// in at step 4) to held and immediately occupies it (held++, inUse++).
func (c *counts) checkout() {
	for {
		p := atomic128.LoadUint128(&c.w)
		if atomic128.CompareAndSwapUint128(&c.w, p, [2]uint64{p[0] + 1, p[1] + 1}) {
			return
		}
	}
}

// release lowers inUse by one — a body completed or parked. The permit stays in held
// (cache-don't-return), now borrowable. Reports whether the release raised borrowable
// from zero (held > inUse crossing), i.e. whether a waiter should be woken.
func (c *counts) release() (wokeBorrowable bool) {
	for {
		p := atomic128.LoadUint128(&c.w)
		held, inUse := p[0], p[1]
		if inUse == 0 {
			panic("permits: release underflow (no running body backed by this cache)")
		}
		if atomic128.CompareAndSwapUint128(&c.w, p, [2]uint64{held, inUse - 1}) {
			// borrowable went from (held-inUse) to (held-inUse+1); it crossed 0→1
			// exactly when it was 0 before, i.e. inUse == held.
			return inUse == held
		}
	}
}

// stealOut removes one borrowable permit from this cache (held--, gated inUse < held)
// for transfer into another. It reports false when nothing is borrowable now — a
// concurrent lock-free acquire may have consumed the idle permit since the steal walk
// observed it, so the caller must revalidate by this CAS and, on false, look
// elsewhere.
func (c *counts) stealOut() bool {
	for {
		p := atomic128.LoadUint128(&c.w)
		held, inUse := p[0], p[1]
		if inUse >= held {
			return false
		}
		if atomic128.CompareAndSwapUint128(&c.w, p, [2]uint64{held - 1, inUse}) {
			return true
		}
	}
}
