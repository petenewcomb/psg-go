// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"sync/atomic"

	"github.com/petenewcomb/atomic128-go"
)

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
	p := c.w.Load()
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
		p := c.w.Load()
		held, inUse := p[0], p[1]
		if inUse != 0 {
			panic("permits: drain of a cache with a running body (inUse != 0)")
		}
		if c.w.CompareAndSwap(p, [2]uint64{0, 0}) {
			return held
		}
	}
}

// acquireLocal occupies w borrowable permits (inUse += w when inUse+w ≤ held) and
// reports success; false means this cache has too little idle to lend right now. The
// lock-free step-1 (own cache) / step-2 (ancestor) hit.
func (c *counts) acquireLocal(w uint64) bool {
	for {
		p := c.w.Load()
		held, inUse := p[0], p[1]
		if inUse+w > held {
			return false
		}
		if c.w.CompareAndSwap(p, [2]uint64{held, inUse + w}) {
			return true
		}
	}
}

// checkout adds w freshly-obtained permits (from the Resource at step 3, or stolen
// in at step 4) to held and immediately occupies them (held += w, inUse += w).
func (c *counts) checkout(w uint64) {
	for {
		p := c.w.Load()
		if c.w.CompareAndSwap(p, [2]uint64{p[0] + w, p[1] + w}) {
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
		p := c.w.Load()
		if c.w.CompareAndSwap(p, [2]uint64{p[0] + w, p[1]}) {
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
		p := c.w.Load()
		held, inUse := p[0]+n, p[1]
		if held >= inUse+w {
			if c.w.CompareAndSwap(p, [2]uint64{held, inUse + w}) {
				return true
			}
		} else if c.w.CompareAndSwap(p, [2]uint64{held, inUse}) {
			return false
		}
	}
}

// release lowers inUse by w — a body completed or parked. The permits stay in held
// (cache-don't-return), now borrowable. Returns the amount of overdraft excess this
// decrement returned to the pool allowance: the delta of max(inUse−held, 0) across
// the transition, computed inside the same CAS so no per-occupy tagging is needed
// (weighted-acquisition.md §Overdraft). Zero whenever inUse ≤ held — i.e. always,
// outside an overdraft episode.
func (c *counts) release(w uint64) (excessReturned uint64) {
	for {
		p := c.w.Load()
		held, inUse := p[0], p[1]
		if inUse < w {
			panic("permits: release underflow (no running body backed by this cache)")
		}
		if c.w.CompareAndSwap(p, [2]uint64{held, inUse - w}) {
			before := excessOver(held, inUse)
			after := excessOver(held, inUse-w)
			return before - after
		}
	}
}

// excessOver returns max(inUse−held, 0) — the overdraft excess a cache is running at.
func excessOver(held, inUse uint64) uint64 {
	if inUse > held {
		return inUse - held
	}
	return 0
}

// occupyTaking occupies w, drawing any shortfall past this cache's own borrowable
// from the pool allowance (weighted-acquisition.md §Overdraft: an occupy that cannot
// fit under held claims excess from the allowance and pushes inUse past held). The
// claim — min(w, inUse+w−held) — is debited from the allowance before the counts CAS
// and refunded if the CAS loses, so the joint (counts, allowance) state never
// double-spends; the transient debit is invisible to the episode invariant, which is
// only read at quiescent points. Reports false, leaving both untouched, when the
// remaining allowance cannot cover the shortfall. With inUse+w ≤ held it degenerates
// to acquireLocal (no allowance touched).
func (c *counts) occupyTaking(w uint64, allowance *atomic.Uint64) bool {
	for {
		p := c.w.Load()
		held, inUse := p[0], p[1]
		need := excessOver(held, inUse+w) - excessOver(held, inUse)
		if need > 0 && !takeAllowance(allowance, need) {
			return false
		}
		if c.w.CompareAndSwap(p, [2]uint64{held, inUse + w}) {
			return true
		}
		if need > 0 {
			allowance.Add(need) // counts moved underneath us — refund and retry
		}
	}
}

// takeAllowance debits n from the pool allowance if it covers n, reporting success.
func takeAllowance(a *atomic.Uint64, n uint64) bool {
	for {
		cur := a.Load()
		if cur < n {
			return false
		}
		if a.CompareAndSwap(cur, cur-n) {
			return true
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
// A cache running at overdraft excess (inUse > held, possible only inside an
// episode's exempt subtree) has nothing borrowable.
func (c *counts) stealOutUpTo(w uint64) uint64 {
	for {
		p := c.w.Load()
		held, inUse := p[0], p[1]
		if inUse >= held {
			return 0 // nothing borrowable (inUse > held is overdraft excess, not idle)
		}
		n := min(held-inUse, w)
		if n == 0 {
			return 0
		}
		if c.w.CompareAndSwap(p, [2]uint64{held - n, inUse}) {
			return n
		}
	}
}
