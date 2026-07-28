// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"sync"
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/dll"
)

// ledger is a pool's money model under directed delivery
// (docs/plan/directed-delivery.md): every unit not in use and not in the pot
// lives in some party's reservation, deliveries fill reservations under the
// ledger mutex in service order, and a party's wake is a completion signal —
// it fires exactly once, when its reservation is whole.
//
// The mutex owns all multi-word movement: registration, delivery, drains,
// claims that miss, departure. The lock-free surface is exactly: the
// debit-first in-use counter, the gated CAS on a reservation's balance (the
// happy claim), the anchor load (the barrier gate and the release guard),
// and the two guard counters (loans outstanding, waiting claimants).
type ledger struct {
	mu sync.Mutex

	// resource holds the pot: free capacity checked in with the backing
	// HoldableResource. Reservations and in-use units are checked OUT of it;
	// ledger transfers between reservations never touch it — a release
	// reaches it only for the residue no shortfall wanted.
	resource HoldableResource

	// inUse counts running bodies' units, pool-wide. Claims debit it first
	// and refund on a miss (never-invisible: a mid-claim unit over-counts,
	// so concurrent use never exceeds capacity).
	inUse atomic.Int64

	// loansOutstanding counts units currently drained from reservations for
	// other parties' benefit. Nonzero forces releases through the mutex so
	// repayment (delivery) can find the claimant or holder owed.
	loansOutstanding atomic.Int64

	// waitingClaimants counts parties blocked at the claim gate — the
	// release fast path's lock-avoidance guard: while one stands, every
	// release must deliver under the mutex (the front claimant is repaid by
	// the next release of anyone in the pool).
	waitingClaimants atomic.Int64

	// anchor publishes the demand queue's front-loaded boundary: the
	// head-most unsatisfied demand, or nil. It is the spill target, the
	// drain entry point, and the barrier gate — the acquisition fast path
	// stays lock-free on this one load, gated exactly when a shortfall
	// exists (a fresh arrival takes from the pot and jumps nobody when
	// every queued reservation is satisfied).
	anchor atomic.Pointer[Demand]

	// demands is the registered-admission queue in arrival order. At rest it
	// is front-loaded: a satisfied prefix, at most one partially-filled
	// entry, an empty suffix — transiently perturbed by in-subtree borrows,
	// restored by spill's front-first priority. An entry leaves exactly at
	// its claim (claim-only retirement). Guarded by mu.
	demands dll.List[*Demand]

	// claimants is the claim-gate queue, FIFO. Never front-loaded at rest:
	// entries arrive carrying their unlent residue, so junior claimants hold
	// real units. Guarded by mu.
	claimants dll.List[*claimant]
}

// potEmptyInvariant reports whether the pot may hold units: only when no
// shortfall stands in either queue. Callers assert it after every delivery
// (docs/plan/directed-delivery.md §Spill and the anchor).
//
// It reads queue state, so mu must be held.
func (l *ledger) unservedShortfall() bool {
	for c := l.claimants.Front(); c != nil; c = l.claimants.Next(c) {
		if c.account.shortfall(c.weight) > 0 {
			return true
		}
	}
	for d := l.demands.Front(); d != nil; d = l.demands.Next(d) {
		if d.account.shortfall(d.w) > 0 {
			return true
		}
	}
	return false
}

// refreshAnchor republishes the head-most unsatisfied demand (or nil). mu
// must be held; every structural or balance change to the demand queue ends
// by re-deriving it.
func (l *ledger) refreshAnchor() {
	for d := l.demands.Front(); d != nil; d = l.demands.Next(d) {
		if d.account.shortfall(d.w) > 0 {
			l.anchor.Store(d)
			return
		}
	}
	l.anchor.Store(nil)
}

// deliver places n released (or newly raised) units into reservations in
// service order — claimant queue front to tail, then demand queue front to
// tail — spilling past satisfied entries into successive shortfalls, and
// returns the residue for the pot. A reservation completed by delivery gets
// its party's one completion wake: the claimant's direct waiter, or the
// demand's attendant. mu must be held.
func (l *ledger) deliver(n uint64) (residue uint64) {
	for c := l.claimants.Front(); n > 0 && c != nil; c = l.claimants.Next(c) {
		n = l.fill(c.account, c.weight, n, func() { c.waiter.Notify() })
	}
	for d := l.demands.Front(); n > 0 && d != nil; d = l.demands.Next(d) {
		n = l.fill(d.account, d.w, n, func() {
			if a := d.attendant; a != nil {
				a.Notify()
			}
		})
	}
	l.refreshAnchor()
	return n
}

// fill moves up to n units into the account's reservation toward weight and
// returns what remains; when the fill completes the reservation it fires
// wake — the party's single completion signal. Units entering a reservation
// settle the account's receivable first (the fungible rule: a lender is
// repaid by whatever units make it whole, whoever released them). mu must
// be held.
func (l *ledger) fill(a *Account, weight, n uint64, wake func()) uint64 {
	short := a.shortfall(weight)
	if short == 0 {
		return n
	}
	take := min(short, n)
	a.reserved.Add(take)
	l.repay(a, take)
	if take == short {
		wake()
	}
	return n - take
}

// release returns a running body's w units to the pool: the in-use debit
// comes off, and the units flow by the delivery order. The fast path stays
// lock-free behind the guards: with no anchor, no loans outstanding, and no
// waiting claimants, the units go straight home to the pot; the post-release
// anchor re-check closes the racing-registrant window (a registration that
// lost the race re-derives capacity from the pot in its own mutex sweep).
func (l *ledger) release(w uint64) {
	l.inUse.Add(-int64(w)) //nolint:gosec // G115: w came from a validated int weight
	if !l.guarded() {
		l.resource.Release(int(w)) //nolint:gosec // G115: same bound
		if l.guarded() {
			// A shortfall registered between the guard loads and the pot
			// deposit: sweep the pot back through delivery so the deposit
			// cannot strand. Paired with the registering side's own
			// mutex-held sweep, one of the two always observes the other —
			// deposit-then-load here, register-then-sweep there.
			l.mu.Lock()
			l.sweepPot()
			l.mu.Unlock()
		}
		return
	}
	l.mu.Lock()
	if residue := l.deliver(w); residue > 0 {
		l.resource.Release(int(residue)) //nolint:gosec // G115: residue ≤ w
	}
	l.mu.Unlock()
}

// guarded reports whether a release may bypass delivery: only when no party
// anywhere could be owed the units — no anchored demand shortfall, no loans
// to repay, no claimant blocked at the gate.
func (l *ledger) guarded() bool {
	return l.anchor.Load() != nil || l.loansOutstanding.Load() != 0 || l.waitingClaimants.Load() != 0
}

// sweepPot draws whatever the pot can supply toward the outstanding
// shortfalls and delivers it — the registration-side arm that closes the
// draw/register race (docs/plan/directed-delivery.md §The Resource
// taxonomy), and the release-side compensation's shared body. mu must be
// held.
func (l *ledger) sweepPot() {
	need := l.totalShortfall()
	if need == 0 {
		return
	}
	got := l.resource.TryAcquireUpTo(int(need)) //nolint:gosec // G115: shortfall bounded by registered weights
	if got == 0 {
		return
	}
	if residue := l.deliver(uint64(got)); residue > 0 { //nolint:gosec // G115: got is a 0..need draw
		l.resource.Release(int(residue)) //nolint:gosec // G115: residue ≤ got
	}
}

// totalShortfall sums both queues' unfilled amounts. mu must be held.
func (l *ledger) totalShortfall() (sum uint64) {
	for c := l.claimants.Front(); c != nil; c = l.claimants.Next(c) {
		sum += c.account.shortfall(c.weight)
	}
	for d := l.demands.Front(); d != nil; d = l.demands.Next(d) {
		sum += d.account.shortfall(d.w)
	}
	return
}

// recallToFront runs the drain discipline for the beneficiary with nothing
// ahead of it — the front claimant's recall
// (docs/plan/directed-delivery.md §The drain discipline). Sources behind the
// beneficiary drain freely, deepest-first: demands' reservations tail-first,
// then junior claimants' reservations tail-first. Units behind the front
// cannot advance their own holders until the front completes — deliveries
// serve the front first — so moving them forward costs their holders
// nothing in service order and accelerates the completion the whole queue
// waits on. There are no exclusions and no pairwise debt: the drained
// holders keep their queue positions and are made whole by delivery order.
//
// It runs, mu held, at every point the pool's parked money changes shape
// with a front claimant standing: a party registered (its carried residue
// became drainable), the front advanced, a departure re-homed units. This
// is the liveness backstop the completion-signal model rests on — without
// it, capacity fragmented across partial reservations of parked parties
// deadlocks with nothing in use and nothing owed a wake.
func (l *ledger) recallToFront() {
	front := l.claimants.Front()
	if front == nil {
		return
	}
	need := front.account.shortfall(front.weight)
	if need == 0 {
		return
	}
	for d := l.demands.Back(); need > 0 && d != nil; d = l.demands.Prev(d) {
		need -= l.drain(d.account, front.account, need)
	}
	for c := l.claimants.Back(); need > 0 && c != nil && c != front; c = l.claimants.Prev(c) {
		need -= l.drain(c.account, front.account, need)
	}
	if need == 0 {
		front.waiter.Notify()
	}
	l.refreshAnchor()
}

// drain moves up to need units from one reservation to another — the ledger
// transfer at the heart of borrowing and recall. Arrival settles the
// receiving account's receivable (see fill). mu must be held.
func (l *ledger) drain(from, to *Account, need uint64) uint64 {
	take := min(from.reserved.Load(), need)
	if take > 0 {
		from.reserved.Add(-take)
		to.reserved.Add(take)
		l.repay(to, take)
	}
	return take
}

// borrow drains up to need units from a parked lender's reservation for a
// beneficiary, recording the receivable: the lender's ledger carries the
// loan (lent), and the pool-wide count forces releases through delivery
// until every receivable ends — by repayment or by the lender's departure
// abandonment. Permits are fungible and no pairwise debt is tracked
// (docs/plan/directed-delivery.md §The delivery order). mu must be held.
func (l *ledger) borrow(from, to *Account, need uint64) uint64 {
	take := l.drain(from, to, need)
	if take > 0 {
		from.lent += take
		l.loansOutstanding.Add(int64(take)) //nolint:gosec // G115: take bounded by validated weights
	}
	return take
}

// repay settles up to n units of an account's receivable — the delivery
// side of a loan coming home. Delivery calls it when filling a reservation
// whose account carries lent: the units arriving are, in the fungible
// ledger, the loan's return. mu must be held.
func (l *ledger) repay(a *Account, n uint64) {
	settle := min(a.lent, n)
	if settle > 0 {
		a.lent -= settle
		l.loansOutstanding.Add(-int64(settle)) //nolint:gosec // G115: settle bounded by validated weights
	}
}
