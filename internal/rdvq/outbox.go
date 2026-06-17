// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"sync/atomic"
)

// outbox is a single cap-1 buffered handoff slot, owned by the destination
// Queue rather than by any sender. A Queue keeps a pool of outboxes that
// self-sizes to the concurrency it actually experiences (see the "rdvq Sender
// redesign" notes): borrowers fill them, receivers drain them, and drained
// outboxes that are no longer needed are reclaimed to a sync.Pool so the live
// set tracks current concurrency.
//
// state folds a generation counter and a "reclaimable" bit into one atomic
// word: (gen << 1) | reclaimableBit. The generation closes the use-after-
// reclaim hazard. A receiver that drains an outbox at generation g marks it
// reclaimable with a CAS that sticks only if the generation is still g; any
// refill bumps the generation first (clearing reclaimable), so a refilled
// (full) outbox can never be left marked reclaimable and discarded out from
// under the value sitting in it.
type outbox[T any] struct {
	ch    chan T
	state atomic.Uint64
}

// reclaimable reports whether the outbox has been drained and not refilled,
// making it safe to discard to the sync.Pool.
func (ob *outbox[T]) reclaimable() bool { return ob.state.Load()&1 == 1 }

// genNow returns the outbox's current generation, captured by a draining
// receiver before it attempts to mark the outbox reclaimable.
func (ob *outbox[T]) genNow() uint64 { return ob.state.Load() >> 1 }

// bumpGen marks a fresh fill: it advances the generation and clears the
// reclaimable bit in one step, so a concurrent receiver's markReclaimable
// (which is gen-guarded) cannot mark this now-full outbox reclaimable.
func (ob *outbox[T]) bumpGen() {
	for {
		old := ob.state.Load()
		next := ((old >> 1) + 1) << 1 // next generation, reclaimable cleared
		if ob.state.CompareAndSwap(old, next) {
			return
		}
	}
}

// markReclaimable sets the reclaimable bit iff the generation is still g (no
// refill since the caller drained the outbox at generation g). It reports
// whether it marked, which is also the signal that a buffered slot truly
// opened up (a failed CAS means a producer refilled the slot, so nothing was
// freed).
func (ob *outbox[T]) markReclaimable(g uint64) bool {
	return ob.state.CompareAndSwap(g<<1, (g<<1)|1)
}

// ── Destination-owned outbox pool ────────────────────────────────────────────
//
// The pool is two queues plus a sync.Pool:
//
//   - outboxes:     the borrow source — every live outbox is here except while
//                   checked out by a borrower. A full outbox stays here too, so
//                   a borrower may grab one to block-fill (pacing).
//   - fullOutboxes: the drain source — an outbox is here exactly while it holds
//                   a value awaiting a receiver.
//   - outboxFree:   reclaimed drained outboxes, whose own GC-clearing is the
//                   scale-to-zero (cheap reuse hot, release cold).
//
// An outbox can be on both outboxes and fullOutboxes at once (filled, not yet
// drained); the two sides coordinate only through the cap-1 channel and the
// atomic state word, so the borrow and drain paths never share a lock.

// obtainOutbox returns an empty outbox ready to fill, recycling one from the
// sync.Pool or allocating a fresh one.
func (q *Queue[T]) obtainOutbox() *outbox[T] {
	if v := q.outboxFree.Get(); v != nil {
		return v.(*outbox[T])
	}
	return &outbox[T]{ch: make(chan T, 1)}
}

// reclaimOutbox discards a drained outbox to the sync.Pool, shrinking the live
// set toward current concurrency.
func (q *Queue[T]) reclaimOutbox(ob *outbox[T]) {
	q.outboxFree.Put(ob)
}

// borrowToFill pops an outbox for a blocking fill. It prefers a full outbox —
// filling it blocks on the cap-1 channel until a receiver drains it, which is
// the pacing that keeps work-in-flight minimal — reclaiming any drained empties
// it passes along the way and keeping at most one as a fallback. When the
// borrow pool holds no full outbox it returns an empty (the fallback, or a
// fresh/recycled one) ready for a non-blocking drop-and-go fill.
//
// The returned outbox is checked out (off the outboxes queue); the caller must
// either fill and publish it (publishFilled) or, if it declines to fill a full
// one, return it via outboxes.PushBack.
func (q *Queue[T]) borrowToFill() (ob *outbox[T], full bool) {
	var fallback *outbox[T]
	for {
		cand, ok := q.outboxes.TryPopFront()
		if !ok {
			if fallback != nil {
				return fallback, false
			}
			return q.obtainOutbox(), false
		}
		if cand.reclaimable() {
			// Drained slack. Keep one as a drop-and-go fallback to skip a pool
			// round-trip; reclaim any further empties.
			if fallback == nil {
				fallback = cand
			} else {
				q.reclaimOutbox(cand)
			}
			continue
		}
		// Full ⇒ prefer it (block-fill = pacing). The held empty fallback, if
		// any, is now surplus.
		if fallback != nil {
			q.reclaimOutbox(fallback)
		}
		return cand, true
	}
}

// tryBorrowEmpty pops at most one outbox for a non-blocking drop-and-go fill.
// It returns an empty outbox (a drained one, or a fresh/recycled one when the
// borrow pool is exhausted) ready for a send that will not block, or (nil,
// false) when the front of the pool is a full outbox — there is no slack, and
// the caller should treat the push as refused (backpressure).
//
// It does not scan past a full outbox to find an empty behind it: that false
// negative is safe (a refused push postpones, never deadlocks) and matches the
// clean borrow discipline (no per-outbox claim machinery). A postponed producer
// is re-driven by the queue-level "outbox freed" wakeup when a receiver drains.
func (q *Queue[T]) tryBorrowEmpty() (*outbox[T], bool) {
	cand, ok := q.outboxes.TryPopFront()
	if !ok {
		// Pool exhausted (all outboxes checked out, or none yet): a fresh empty
		// slot is not backlog, so admit it.
		return q.obtainOutbox(), true
	}
	if cand.reclaimable() {
		return cand, true
	}
	// Full ⇒ no slack. Return it to the borrow pool and refuse.
	q.outboxes.PushBack(cand)
	return nil, false
}

// publishFilled completes a fill: a value has just been sent into ob.ch by the
// caller. It bumps the generation (clearing reclaimable so a racing drain
// cannot discard this now-full outbox), runs bufferedFn before the value
// becomes observable, then publishes the outbox to the drain source and back to
// the borrow pool and notifies a waiting receiver.
func (q *Queue[T]) publishFilled(ob *outbox[T], bufferedFn BufferedFunc) {
	ob.bumpGen()
	// bufferedFn must complete before the value can be observed by any receiver,
	// so run it before publishing to fullOutboxes.
	if bufferedFn != nil {
		bufferedFn()
	}
	q.fullOutboxes.PushBack(ob)
	q.outboxes.PushBack(ob)
	q.outboxWaiters.Notify(nil)
}
