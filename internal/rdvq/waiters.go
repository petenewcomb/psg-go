// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/trace"
)

// WaitSelectFunc handles the select operation for a Waiters wait. It receives
// the wait channel and returns whether it received a wake from it (false if it
// took some other case, e.g. ctx.Done). The Waiters caller handles the inbox
// bookkeeping; the user does not need to.
type WaitSelectFunc = func(waitCh <-chan struct{}) bool

// BasicWaitSelect provides a standard implementation of [WaitSelectFunc] that
// selects on the wait channel and ctx.Done(). Returns whether a wake was
// received and a non-nil error if the context was cancelled instead.
func BasicWaitSelect(ctx context.Context, waitCh <-chan struct{}) (bool, error) {
	traceRegion := "rdvq.BasicWaitSelect"
	trace.Logf(ctx, traceRegion, "entering select: waitCh=%p", waitCh)
	select {
	case <-waitCh:
		trace.Logf(ctx, traceRegion, "received wake from waitCh=%p", waitCh)
		return true, nil
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		return false, ctx.Err()
	}
}

// Waiters provides a blocking wait and wake system for coordinating between
// goroutines. It can be used anywhere a multi-party blocking wait mechanism is
// needed.
//
// Waiters uses FIFO selection to ensure fairness - the waiter that has been
// waiting longest gets woken first.
//
// When goroutines register to wait, they provide a verification function that
// re-checks conditions after registration but before blocking, preventing
// missed wakes due to race conditions.
//
// A notification that finds no parked waiter is a miss, and the caller's
// missFn declares its fate. A wake announcing durable state that every
// parker's confirmFn re-derives after registration (a buffered value, a
// registered inbox) passes nil and the miss is dropped — the register-then-
// confirm discipline guarantees no parked party ever needed it. A wake
// carrying a fact that lives nowhere else passes [Waiters.RecordMiss] to
// persist it in the balance, or a spawn signal to mint its own consumer.
type Waiters struct {
	q inboxQueueQueue[struct{}]

	// balance counts missed notifications persisted by RecordMiss. A wait
	// consumes one after its confirmFn approves the wait, returning as a
	// received wake without parking. It is a counter rather than a sticky bit
	// because waiting may be concurrent: N recorded misses must be able to
	// abort N park attempts — service multiplicity matches event multiplicity,
	// so ready work is never serialized onto a single woken waiter while its
	// siblings park. Non-negative by construction (see tryConsumeMiss).
	balance atomic.Int64
}

// Init initializes the Waiters for use. Must be called before any other operations.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) Init() {
	traceRegion := "rdvq.Waiters.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	w.q.Init()
}

// dropWake is the orphan handler for the payloadless wake queue: a wake landing
// in an abandoned registration is simply dropped — the abandoner is running,
// and a running worker attempts everything before parking.
func dropWake(struct{}) {}

// WaitFunc registers a waiter and handles the blocking wait with custom
// select handling.
//
// Parameters:
//   - confirmFn: Function called to verify conditions after registration but
//     before blocking. The confirmFn prevents missed wakes by re-checking
//     conditions after the waiter is registered. If it returns false, the wait
//     is aborted (selectFn is not called).
//   - selectFn: Custom select function for handling the wait operation
//
// Returns whether a wake was received (false if the wait was aborted by
// confirmFn or selectFn picked some other case such as ctx.Done).
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) WaitFunc(confirmFn func() bool, selectFn WaitSelectFunc) bool {
	traceRegion := "rdvq.Waiters.WaitFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	if w == nil {
		// No waiters infrastructure to register against; let selectFn run
		// against a nil channel (its non-channel cases — ctx.Done, etc. —
		// can still fire).
		return selectFn(nil)
	}

	waitInbox := w.q.borrowInbox()
	// PopFrontFunc always leaves the inbox free (received, abandoned, or orphan-drained),
	// so the owning receiver always reclaims it — recycling abandoned inboxes too (the
	// generation-stamped protocol makes the lingering hint inert).
	defer w.q.reclaimInbox(waitInbox)
	received := false
	w.q.PopFrontFunc(
		waitInbox,
		dropWake,
		func(ib *inbox[struct{}]) {
			if !confirmFn() {
				// Declined: the balance is untouched. Consume-implies-retry is
				// provable only for callers that sweep after a received wake;
				// a decline makes no such promise, so it may not eat a miss.
				return
			}
			// Try to consume a recorded miss before blocking. Success returns
			// as a received wake without parking; the caller's post-wake sweep
			// then serves whatever the missed notification announced. The
			// consume deliberately follows registration (PopFrontFunc
			// registered ib above): paired with Notify's record-then-re-offer,
			// whichever side acted second observes the other — so "balance > 0
			// while a waiter is parked" is never a stable state.
			if w.tryConsumeMiss() {
				received = true
				return
			}
			received = selectFn(ib.channel())
			if received {
				ib.emptied()
			}
		},
	)
	return received
}

// Wait registers a waiter and blocks until woken or context cancelled.
func (w *Waiters) Wait(ctx context.Context, confirmFn func() bool) error {
	var err error
	w.WaitFunc(confirmFn, func(waitCh <-chan struct{}) bool {
		var received bool
		received, err = BasicWaitSelect(ctx, waitCh)
		return received
	})
	return err
}

// MissHandler is the fate of a missed notification — a wake that found no
// parked waiter of the set it was aimed at. [Waiters.Notify] consults it
// only on a miss: nil drops the miss, [PersistMiss] records it in the
// waiter set's balance, and any other handler — [MissFunc] or a custom
// implementation — runs its own action, such as a worker pool's spawn
// signal.
type MissHandler interface {
	// HandleMiss handles a miss on the given waiter set. Implementations
	// act through the set's exported API; persistence in the balance is
	// reserved to the [PersistMiss] sentinel, which Notify recognizes by
	// identity rather than by invoking this method.
	HandleMiss(w *Waiters)
}

// PersistMiss selects persistence as the miss fate: [Waiters.Notify]
// records the missed notification in the waiter set's balance, for a later
// wait to consume before it parks. It is the handler for wakes whose fact
// lives nowhere else (a one-shot capacity event already consumed at its
// source) aimed at a waiter set that mints no workers of its own.
//
// PersistMiss is a pure sentinel, recognized by Notify by identity:
// persistence runs the waiter set's internal recording protocol, which is
// deliberately unreachable through the exported API, so calling its
// HandleMiss directly panics.
var PersistMiss MissHandler = persistMissSentinel{}

type persistMissSentinel struct{}

func (persistMissSentinel) HandleMiss(*Waiters) {
	panic("rdvq: PersistMiss is a sentinel recognized by Notify; do not invoke its HandleMiss directly")
}

// persistMissImpl is the private implementation Notify swaps in for the
// [PersistMiss] sentinel: the one [MissHandler] with access to the waiter
// set's recording protocol.
var persistMissImpl MissHandler = persistMiss{}

type persistMiss struct{}

func (persistMiss) HandleMiss(w *Waiters) { w.recordMiss() }

// MissFunc adapts a plain function to a [MissHandler]; the function runs on
// each miss. Worker-minting pools pass their spawn signal this way, so
// their misses spawn a consumer instead of touching the balance.
type MissFunc func()

func (f MissFunc) HandleMiss(*Waiters) { f() }

// Notify wakes one waiting goroutine. On a miss — no parked waiter took the
// wake — it hands the miss to the given handler; a nil handler drops it.
// This is both the queue relay (a listener's wake-one-worker action) and
// the work-supply wake.
//
// The nil arm carries a proof obligation: it is sound only for wakes whose
// announced fact is durable queue state that every parker's confirmFn
// re-derives after registration, so a dropped miss can never strand a
// parked party. A wake whose fact lives nowhere else must pass a handler.
func (w *Waiters) Notify(miss MissHandler) {
	if !w.wakeOne() && miss != nil {
		// Safe against non-comparable handlers (MissFunc is a func type):
		// interface equality panics only when both operands hold the SAME
		// non-comparable dynamic type, and the sentinel's is a comparable
		// struct — any other type compares false.
		if miss == PersistMiss {
			miss = persistMissImpl
		}
		miss.HandleMiss(w)
	}
}

// recordMiss persists one missed notification in the balance ([PersistMiss]'s
// action).
func (w *Waiters) recordMiss() {
	// Record the miss, then re-offer. The re-offer closes the race against a
	// waiter that registered after the caller's failed wake but read the
	// balance before the record landed: a wait registers before it reads the
	// balance, and this record precedes the re-offer, so whichever side acted
	// second observes the other — the waiter consumes the miss, or the
	// re-offer finds its registration. "Balance > 0 while a waiter is parked"
	// is never a stable state. A delivered re-offer reclaims the recorded
	// miss; a failed reclaim means a third party already consumed it
	// (aborting a park attempt), leaving one extra wake whose taker re-checks
	// and re-parks — bounded, never a loop.
	w.balance.Add(1)
	if w.wakeOne() {
		w.tryConsumeMiss()
	}
}

// tryConsumeMiss consumes one recorded miss if the balance is positive,
// reporting whether it did. The conditional decrement is what keeps the
// balance non-negative by construction.
func (w *Waiters) tryConsumeMiss() bool {
	for {
		b := w.balance.Load()
		if b <= 0 {
			return false
		}
		if w.balance.CompareAndSwap(b, b-1) {
			return true
		}
	}
}

// Reset returns the waiter set to its rest state: the balance of missed
// notifications is zeroed and leftover registration hints are discarded. It
// is for single-owner lifecycle points that are quiescent by construction
// (a pooled owner's per-cycle reset): with no waiter registered, every
// remaining hint is stale — residue of abandoned registrations beyond
// reapStale's bounded budget — and a recorded miss is meaningful only
// within the cycle whose notification it carries; stale, it would abort a
// park in the next cycle for an event that cycle never saw.
func (w *Waiters) Reset() {
	w.q.drainHints()
	w.balance.Store(0)
}

// wakeOne pushes a wake to a parked waiter, returning whether one took it.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) wakeOne() bool {
	traceRegion := "rdvq.Waiters.wakeOne"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	return w.q.TryPushBack(struct{}{})
}

// NotifyAll wakes all waiting goroutines to re-check for work.
//
//nolint:contextcheck // background context used only for tracing
func (w *Waiters) NotifyAll() {
	traceRegion := "rdvq.Waiters.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Waiters=%p", w)

	for w.q.TryPushBack(struct{}{}) {
		// Keep waking until no parked waiter remains
	}
}
