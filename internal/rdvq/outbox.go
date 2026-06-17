// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"math/bits"
	"sync/atomic"
)

// outboxState is the lifecycle state of an outbox, held in the low bits of the
// atomic state word alongside a monotonic generation counter. Every transition
// is a generation-guarded CAS, so a stale reference (a hint for a slot that has
// since been refilled, or an outbox reused from the pool) can never act on the
// wrong incarnation.
type outboxState uint64

const (
	outboxEmpty      outboxState = iota // drained & available: on `outboxes` in place + a hint on `emptyOutboxes`
	outboxFilling                       // a push owns it mid-fill (transient)
	outboxFull                          // holds a value: on `outboxes` + `fullOutboxes`
	outboxStateCount                    // trailing iota — number of states
)

// genShift reserves the low bits of the state word for outboxState; the
// generation occupies the rest. Derived from the state count, so adding a state
// widens it automatically. (var, not const: bits.Len is not a constant function.)
var (
	genShift  = uint(bits.Len(uint(outboxStateCount - 1)))
	stateMask = uint64(1)<<genShift - 1
)

func packState(gen uint64, s outboxState) uint64 { return gen<<genShift | uint64(s) }

// outbox is a single cap-1 buffered handoff slot, owned by the destination Queue
// rather than by any sender. A Queue keeps a pool of outboxes that self-sizes to
// the concurrency it actually experiences (see the "rdvq Sender redesign" notes).
type outbox[T any] struct {
	ch    chan T
	state atomic.Uint64 // gen<<genShift | outboxState
}

// loadState reads the current generation and state.
func (ob *outbox[T]) loadState() (gen uint64, s outboxState) {
	w := ob.state.Load()
	return w >> genShift, outboxState(w & stateMask)
}

// claimEmpty transitions empty→filling iff the generation is still g (a hint's
// slot has not been refilled or reused since the hint was minted). Reports
// whether it claimed.
func (ob *outbox[T]) claimEmpty(g uint64) bool {
	return ob.state.CompareAndSwap(packState(g, outboxEmpty), packState(g, outboxFilling))
}

// claimFull transitions full→filling iff still at generation g — the blocking
// path taking ownership of a full outbox to block-fill (pace) it.
func (ob *outbox[T]) claimFull(g uint64) bool {
	return ob.state.CompareAndSwap(packState(g, outboxFull), packState(g, outboxFilling))
}

// finishFill publishes a completed fill: filling→full at the next generation.
// Only the owning push reaches this state, so a plain store is safe.
func (ob *outbox[T]) finishFill(g uint64) { ob.state.Store(packState(g+1, outboxFull)) }

// markEmpty transitions full→empty iff still at generation g (no refill since the
// caller drained at g). A failed CAS means a blocking producer is refilling this
// slot, so it must NOT be marked empty (the slot is taken). Reports whether it
// marked.
func (ob *outbox[T]) markEmpty(g uint64) bool {
	return ob.state.CompareAndSwap(packState(g, outboxFull), packState(g, outboxEmpty))
}

// Init implements [omnipool.Initer]: it allocates the cap-1 buffered channel for
// a freshly created outbox (generation 0, state empty).
func (ob *outbox[T]) Init() { ob.ch = make(chan T, 1) }

// Reset implements [omnipool.Resetter]: on return to the pool it ADVANCES the
// generation (never resets it) and leaves the state empty with the drained
// channel intact. Monotonic generations are what make a stale emptyOutboxes hint
// safe: a CAS at the hint's old generation can never match a reused outbox.
// Implementing Reset also prevents omnipool's default whole-struct zeroing, which
// would nil the channel.
func (ob *outbox[T]) Reset() {
	g, _ := ob.loadState()
	ob.state.Store(packState(g+1, outboxEmpty))
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
// shared pool or allocating (and Init-ing) a fresh one.
func (q *Queue[T]) obtainOutbox() *outbox[T] {
	return q.outboxPool.Get()
}

// reclaimOutbox returns a drained, owned outbox to the shared pool — the
// reciprocal of obtainOutbox and the scale-down primitive. The caller must hold
// it in a state no other path will act on (won via claimEmpty) and have already
// removed its outboxes entry, so no concurrent claimer or stale outboxes entry
// can resurrect it. Put → Reset bumps the generation, inerting any stale
// emptyOutboxes hint to this incarnation. See docs/rdvq-outbox-reclamation.md.
func (q *Queue[T]) reclaimOutbox(ob *outbox[T]) {
	q.outboxPool.Put(ob)
}

// reclaimProbe runs only after a successful emptyOutboxes hint-claim, so a free
// outbox was just confirmed available (slack, not backpressure). It reclaims at
// most one genuinely-idle outbox to track the live set to current concurrency,
// never the one just claimed (which is in use, not an idle reserve). Bounded to
// two pops of outboxes, O(1).
//
// The probe regulates itself from structure, holding no counter: the front of
// outboxes is usually full when utilization is high (push back, no reclaim) and
// usually empty when over-provisioned (reclaim), giving a negative-feedback
// equilibrium at the active working set. See docs/rdvq-outbox-reclamation.md for
// why no counter and no idle floor are kept (a floor trades primary-metric tail
// latency for secondary-metric throughput and was measured not worth it).
func (q *Queue[T]) reclaimProbe(claimed *outbox[T]) {
	ob, ok := q.outboxes.TryPopFront()
	if !ok {
		return
	}
	if ob == claimed {
		// The in-use outbox sat at the front. Hold it off-queue so the next pop
		// is guaranteed a different outbox, then restore it.
		next, nok := q.outboxes.TryPopFront()
		q.outboxes.PushBack(claimed)
		if !nok {
			return // only the in-use outbox was present — nothing idle to reclaim
		}
		ob = next
	}
	g, st := ob.loadState()
	if st == outboxEmpty && ob.claimEmpty(g) {
		q.reclaimOutbox(ob) // immediate reclaim; stale hint goes gen-safe on Reset
	} else {
		q.outboxes.PushBack(ob) // in use, or our claim lost a race
	}
}

// borrowScanCap bounds how far the blocking path scans `outboxes` for a full
// outbox to block-fill before giving up and allocating, so a deep backlog of
// empties/in-flight can't spin it.
const borrowScanCap = 32

// borrowToFill obtains an outbox for a BLOCKING fill (the PushBack path). It
// prefers a full outbox — claiming it (full→filling) and block-filling it paces
// the producer to drain rate (minimal work-in-flight) — and returns it with
// full=true. Empty/filling/claim-lost entries it passes are re-added to
// `outboxes` (empties are the non-blocking hint path's to claim; filling is
// in-flight). If it finds no full within the cap it allocates a fresh outbox and
// returns it claimed (filling) for a non-blocking drop-and-go fill (full=false).
//
// The returned outbox is in state `filling` at the returned generation; the
// caller fills its channel then calls publishFull(ob, gen, ...).
func (q *Queue[T]) borrowToFill() (ob *outbox[T], gen uint64, full bool) {
	for i := 0; i < borrowScanCap; i++ {
		cand, ok := q.outboxes.TryPopFront()
		if !ok {
			break
		}
		g, st := cand.loadState()
		if st == outboxFull && cand.claimFull(g) {
			return cand, g, true // block-fill this (paces)
		}
		// Empty (hint path's), in-flight, or our claim lost a race: re-add and
		// keep looking for a full to pace on.
		q.outboxes.PushBack(cand)
	}
	// No full to pace on: allocate a fresh outbox and drop-and-go fill it.
	fresh := q.obtainOutbox()
	g, _ := fresh.loadState()
	fresh.claimEmpty(g) // uncontended (just obtained)
	return fresh, g, false
}

// publishFull completes a fill: a value has just been sent into ob.ch and ob is
// in state `filling` at generation g. It transitions filling→full (next gen),
// runs bufferedFn before the value becomes observable, publishes ob to the drain
// source (and, when addToOutboxes, to the borrow pool — true for a freshly
// allocated or blocking-checked-out outbox; false for a non-blocking in-place
// hint claim, which never left `outboxes`), and notifies a waiting receiver.
func (q *Queue[T]) publishFull(ob *outbox[T], g uint64, bufferedFn BufferedFunc, addToOutboxes bool) {
	ob.finishFill(g)
	// bufferedFn must complete before the value can be observed by any receiver,
	// so run it before publishing to fullOutboxes.
	if bufferedFn != nil {
		bufferedFn()
	}
	q.fullOutboxes.PushBack(ob)
	if addToOutboxes {
		q.outboxes.PushBack(ob)
	}
	q.outboxWaiters.Notify(nil)
}
