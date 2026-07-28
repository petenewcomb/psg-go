// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/nbcq"
	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/trace"
)

type inboxQueueQueue[T any] = inboxOnlyQueue[T, inboxQueue[T], inboxQueueTrait[T]]
type inboxStackQueue[T any] = inboxOnlyQueue[T, inboxStack[T], inboxStackTrait[T]]

// inboxOnlyQueue implements the base layer of rdvq's two-tier architecture,
// providing direct sender-receiver rendezvous without overflow handling.
// It serves as the foundation for [Queue] and [Waiters].
//
// inboxOnlyQueue uses lock-free operations and eliminates channel contention
// by giving each receiver a dedicated inbox channel. Senders attempt
// direct handoff to waiting receivers, failing immediately if none are available.
//
// The type parameter C represents the concrete collection type (either inboxQueue
// or inboxStack), while CT is the trait type that provides operations on C.
type inboxOnlyQueue[T any, C any, CT emptyInboxesTrait[T, C]] struct {
	emptyInboxes C
	// inboxPool recycles drained inboxes that are out of the emptyInboxes collection, so
	// the destination owns inbox storage (no per-receiver map) and a looping receiver
	// allocates nothing in steady state. It is the process-global omnipool.For[inbox[T]]
	// shared across every inboxOnlyQueue of the same T — cross-queue reuse is safe because
	// the emptyInboxes collection holds generation-stamped HINTS (see inboxHint): a sender
	// claims at the hint's captured generation, so a hint that outlived its incarnation
	// (the inbox abandoned and reused in this or any other queue via the shared pool) fails
	// its claim and is skipped.
	inboxPool *omnipool.Pool[inbox[T]]
}

// inboxHint is a generation-stamped reference to a registered inbox, published on the
// emptyInboxes collection by a receiver. The generation is the one the inbox held when
// the hint was minted; a sender claims at exactly that generation, so a hint that
// outlived its incarnation — the inbox abandoned (which bumps the generation) and then
// reused, in this queue or another via the shared inbox pool — fails its claimDeliver
// and is dropped. A bare *inbox pointer would not suffice: claiming at the inbox's
// CURRENT generation would let a stale hint deliver into a reused inbox now owned by a
// different receiver (cross-queue misdelivery). Mirrors outboxHint.
type inboxHint[T any] struct {
	ib  *inbox[T]
	gen uint64
}

// borrowInbox returns an inbox for a receiver to register and wait on, recycling a
// drained one from the shared pool or allocating (and Init-ing) a fresh one. A pooled
// inbox keeps its (empty) channel and its generation, so reuse re-allocates nothing.
func (q *inboxOnlyQueue[T, C, CT]) borrowInbox() *inbox[T] {
	return q.inboxPool.Get()
}

// reclaimInbox returns a drained, free inbox to the shared pool. PopFrontFunc always
// leaves the inbox free (received, abandoned, or orphan-drained), so the owning receiver
// always reclaims it; the generation stamp on outstanding hints keeps cross-queue reuse
// safe. omnipool's Put invokes inbox.Reset (clear the per-op flag, re-arm free at the
// current generation, keep the channel).
func (q *inboxOnlyQueue[T, C, CT]) reclaimInbox(ib *inbox[T]) {
	q.inboxPool.Release(ib)
}

// reapBudget bounds how many leading hints reapStale inspects per call — enough to
// outpace the one stale hint each abandon produces (so steady-state abort churn drains)
// without adding unbounded work to the abandon path.
const reapBudget = 8

// reapStale front-pops up to reapBudget leading hints, dropping STALE ones (whose inbox
// is no longer waiting at the hint's captured generation — abandoned and/or reused).
// TryPop already returns each dropped hint's node + value cell to the pool, so this
// recycles the storage that accumulated abandoned-but-never-notified registrations
// would otherwise pin live in the queue. The first LIVE hint (its inbox still
// waiting@gen, belonging to another receiver) is re-pushed and the scan stops — never
// dropping a live waiter (no lost wakeup); its re-push reuses a node+value the scan just
// recycled. Called from the abandon path, where new stale hints are produced.
func (q *inboxOnlyQueue[T, C, CT]) reapStale() {
	var ct CT
	for range reapBudget {
		h, ok := ct.TryPop(&q.emptyInboxes)
		if !ok {
			return
		}
		if gen, st := h.ib.loadState(); st == inboxWaiting && gen == h.gen {
			// Live registration of another receiver: restore it and stop.
			ct.Push(&q.emptyInboxes, h)
			return
		}
		// Stale: TryPop already recycled its node + value cell; drop it.
	}
}

// drainHints discards every hint in the collection, returning its storage to
// the pools. For quiescent single-owner resets only: with no registration
// live, every remaining hint is stale, so none may be restored (contrast
// reapStale, which must preserve the first live hint it meets).
func (q *inboxOnlyQueue[T, C, CT]) drainHints() {
	var ct CT
	for {
		if _, ok := ct.TryPop(&q.emptyInboxes); !ok {
			return
		}
	}
}

// emptyInboxesTrait is the internal interface for managing collections of
// waiting consumer inboxes. It provides a unified abstraction over FIFO and LIFO
// consumer selection - the queue itself always delivers items in FIFO order, but
// this trait controls which waiting consumer receives the next item.
type emptyInboxesTrait[T any, C any] interface {
	// Init initializes the collection. Must be called before first use.
	Init(c *C)

	// Push adds a generation-stamped inbox hint to the collection.
	Push(c *C, h inboxHint[T])

	// TryPop attempts to remove and return a hint from the collection.
	// Returns false if the collection is empty.
	TryPop(c *C) (inboxHint[T], bool)
}

// Init initializes the queue. Must be called before first use.
func (q *inboxOnlyQueue[T, C, CT]) Init() {
	traceRegion := "rdvq.inboxOnlyQueue.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "inboxOnlyQueue=%p, emptyInboxes=%p", q, &q.emptyInboxes)

	var ct CT
	ct.Init(&q.emptyInboxes)
	q.inboxPool = omnipool.For[inbox[T]]()
}

//nolint:contextcheck // background context used only for tracing
func (q *inboxOnlyQueue[T, C, CT]) TryPushBack(value T) bool {
	traceRegion := "rdvq.inboxOnlyQueue.TryPushBack"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "inboxOnlyQueue=%p", q)

	var ct CT

	// Pop hints and deliver to the first genuinely-waiting inbox. The hint collection
	// is stale-tolerant (it may hold inboxes that have since been abandoned, reused, or
	// taken by another sender), so each candidate is validated by a generation-guarded
	// claim. Senders NEVER reclaim — the owning receiver is the sole reclaimer.
	for {
		h, ok := ct.TryPop(&q.emptyInboxes)
		if !ok {
			trace.Logf(context.Background(), traceRegion, "no empty inboxes to try, returning false")
			return false
		}
		// Claim at the hint's CAPTURED generation, not the inbox's current one. A hint
		// minted at generation g succeeds only while the inbox is still waiting@g; if the
		// receiver abandoned it (generation bumped) and it was reused — here or in another
		// queue via the shared pool — the claim at g fails and the stale hint is skipped.
		if h.ib.claimDeliver(h.gen) {
			// Won exclusive delivery rights. The inbox was waiting (its channel empty),
			// so this send is uncontended and never blocks.
			h.ib.ch <- value
			trace.Logf(context.Background(), traceRegion, "delivered value to inbox=%p, returning true", h.ib)
			return true
		}
		// Stale or in-flight hint (abandoned/reused → generation advanced, already
		// delivering, or another sender took it): skip and try the next.
	}
}

// inboxOnlyPopSelectFunc handles the select operation for PopFrontFunc.
// It should select on the inbox channel. The callback MUST call ib.emptied()
// if a value is received from the inbox.
type inboxOnlyPopSelectFunc[T any] = func(ib *inbox[T])

func basicInboxOnlyPopSelect[T any](ctx context.Context, ib *inbox[T], processFn ProcessValueFunc[T]) error {
	traceRegion := "rdvq.basicInboxOnlyPopSelect"
	inboxCh := ib.channel()
	trace.Logf(ctx, traceRegion, "entering select: inbox=%p, inboxCh=%p", ib, inboxCh)
	select {
	case value := <-inboxCh:
		ib.emptied()
		trace.Logf(ctx, traceRegion, "received value from inbox=%p, inboxCh=%p", ib, inboxCh)
		processFn(value)
		return nil
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		return ctx.Err()
	}
}

// PopFrontFunc registers ib as a waiting inbox and runs selectFn to block on it.
// On return ib is always drained and free — reclaimable or directly reusable by the
// owning receiver (the sole reclaimer). selectFn must call ib.emptied() iff it
// received a value on ib.channel(); processOrphanFn receives a value a sender
// delivered just as the receiver gave up (the abandon-loses-to-a-claim race).
//
// Under the generation-stamped state machine (see inbox.go /
// docs/rdvq-inbox-reclamation.md), register publishes a hint
// unconditionally (duplicate/stale hints are inert by state/generation), a sender
// delivers only by winning claimDeliver, and abandon bumps the generation so a sender
// that observed the now-disowned registration can never deliver into it.
//
//nolint:contextcheck // background context used only for tracing
func (q *inboxOnlyQueue[T, C, CT]) PopFrontFunc(
	ib *inbox[T],
	processOrphanFn ProcessValueFunc[T],
	selectFn inboxOnlyPopSelectFunc[T],
) {
	traceRegion := "rdvq.inboxOnlyQueue.PopFrontFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	if processOrphanFn == nil {
		panic("processOrphanFn is nil")
	}

	var ct CT

	if ib.ch == nil {
		ib.ch = make(chan T, 1)
	}

	// Register free@g → waiting@g (uncontended: only the owning receiver touches a free
	// inbox), then publish a hint. The hint collection is stale-tolerant — a duplicate
	// hint from a prior registration of this same inbox is inert (it pops to a
	// non-waiting state or, after an abandon, an advanced generation).
	g, _ := ib.loadState()
	if !ib.register(g) {
		panic("rdvq: inbox passed to PopFrontFunc was not free")
	}
	// Publish a hint stamped with the registration generation g. A sender claims at this
	// captured g, so once this registration is abandoned (g bumped) and the inbox reused,
	// this hint is inert — even if the reuse is in another queue via the shared pool.
	ct.Push(&q.emptyInboxes, inboxHint[T]{ib: ib, gen: g})

	ib.emptyPending()
	selectFn(ib)

	if ib.wasEmptied {
		// The caller received a value a sender delivered (waiting@g → delivering@g → ch).
		ib.finishReceive(g)
		return
	}
	// The caller did not receive — try to abandon this registration.
	if ib.abandon(g) {
		// Won: no sender was delivering. The generation bump (→ g+1) inerts the lingering
		// hint, so the inbox is now free and safe to reclaim or reuse. This abandon just
		// orphaned our own hint in emptyInboxes; reap a bounded run of leading stale hints
		// to recycle the node+value storage that accumulated abandons would otherwise pin
		// out of the pool (the senders' TryPushBack reaps too, but only when a Notify
		// arrives — abort-heavy contention outruns Notifies). See reapStale.
		q.reapStale()
		trace.Logf(context.Background(), traceRegion, "abandoned inbox=%p", ib)
		return
	}
	// Lost: a sender claimed delivering@g between register and abandon, so a value is
	// inbound (e.g. the caller's select took ctx/wait just as a sender delivered). Drain
	// the orphan and hand it to processOrphanFn.
	orphan := <-ib.ch
	processOrphanFn(orphan)
	ib.finishReceive(g)
	trace.Logf(context.Background(), traceRegion, "drained orphan from inbox=%p", ib)
}

func (q *inboxOnlyQueue[T, C, CT]) PopFront(ctx context.Context, ib *inbox[T], processFn ProcessValueFunc[T]) error {
	var err error
	q.PopFrontFunc(ib, processFn, func(ib *inbox[T]) {
		err = basicInboxOnlyPopSelect(ctx, ib, processFn)
	})
	return err
}

// inboxQueue is a FIFO collection of waiting consumer inboxes, implemented
// using a lock-free queue. This provides fair consumer selection - the
// consumer that has been waiting longest gets the next item.
type inboxQueue[T any] = nbcq.Queue[inboxHint[T]]

type inboxQueueTrait[T any] struct{}

func (inboxQueueTrait[T]) Init(q *nbcq.Queue[inboxHint[T]]) {
	q.Init()
}

func (inboxQueueTrait[T]) Push(q *nbcq.Queue[inboxHint[T]], h inboxHint[T]) {
	q.PushBack(h)
}

func (inboxQueueTrait[T]) TryPop(q *nbcq.Queue[inboxHint[T]]) (inboxHint[T], bool) {
	return q.TryPopFront()
}

// inboxStack is a LIFO collection of waiting consumer inbox hints, implemented
// using a mutex-protected slice with an atomic empty flag. This provides
// "hot" consumer selection - the most recently active consumer gets the
// next item, enabling natural worker scaling through timeout.
type inboxStack[T any] struct {
	mu    sync.Mutex
	hints []inboxHint[T]
	empty atomic.Bool // Atomic flag for lock-free empty check
}

type inboxStackTrait[T any] struct{}

func (inboxStackTrait[T]) Init(*inboxStack[T]) {}

func (inboxStackTrait[T]) Push(s *inboxStack[T], h inboxHint[T]) {
	s.mu.Lock()
	s.hints = append(s.hints, h)
	s.empty.Store(false)
	s.mu.Unlock()
}

func (inboxStackTrait[T]) TryPop(s *inboxStack[T]) (inboxHint[T], bool) {
	// Fast path: check if empty without acquiring lock
	if s.empty.Load() {
		return inboxHint[T]{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	i := len(s.hints) - 1
	if i < 0 {
		return inboxHint[T]{}, false
	}

	h := s.hints[i]
	s.hints[i] = inboxHint[T]{} // clear reference
	s.hints = s.hints[:i]

	if i == 0 {
		// We just removed the last item
		s.empty.Store(true)
	}

	return h, true
}
