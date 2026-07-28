// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

// This file is the admission lifecycle over the ledger: reserve at arrival,
// claim at the run boundary, depart on abandonment. The claim retires a
// registered demand — always and only — which makes the ledger's structures
// an exact partition: demand queue = registered admissions not yet running,
// claimant queue = parties blocked at the run boundary, in-use = running
// bodies (docs/plan/directed-delivery.md §Queue membership and retirement).

// tryReserve attempts an arrival's happy-path reservation: draw up to w
// from the pot in one atomic step. It returns the amount drawn — w means
// the reservation is whole and stays private to the caller's handle (no
// account, no queue, no mutex); anything less is the caller's cue to
// register, carrying the partial draw as its reservation's starting
// balance.
func (l *ledger) tryReserve(w uint64) uint64 {
	// While any reservation ahead stands short, the pot's contents are the
	// queue's, not a newcomer's — first come once the queue is clear, no
	// jumping while it isn't. The pot-empty invariant makes this nearly
	// vacuous at rest (a standing shortfall implies an empty pot); the one
	// anchor load closes the transient delivery windows where units sit in
	// the pot en route to the queue. A claimant shortfall needs no separate
	// check: it too implies an empty pot.
	if l.anchor.Load() != nil {
		return 0
	}
	return uint64(l.resource.TryAcquireUpTo(int(w))) //nolint:gosec // G115: w is a registered weight; the draw is 0..w
}

// register turns a short arrival into a registered demand: its account takes
// the partial draw as the reservation's starting balance, the demand links
// at the queue's tail, and the mutex-side pot sweep closes the window
// between the lock-free draw and the registration (a release that raced the
// arrival re-derives here). The demand stays queued until its claim.
func (l *ledger) register(d *Demand, a *Account, w, drawn uint64) {
	d.account = a
	d.w = w
	if drawn > 0 {
		a.reserved.Add(drawn)
	}
	l.mu.Lock()
	l.demands.PushBack(d)
	l.sweepPot()
	l.recallToFront()
	l.refreshAnchor()
	l.mu.Unlock()
}

// claimPrivate converts a whole private reservation to in-use: pure
// accounting, atomics only — the units were drawn from the pot at reserve
// time and no other party can see them.
func (l *ledger) claimPrivate(w uint64) {
	l.inUse.Add(int64(w)) //nolint:gosec // G115: w came from a validated int weight
}

// claimRegistered runs a registered admission's claim at the run boundary:
// one mutex section in which success retires the demand, and a miss retires
// it and registers the claimant in its place. It returns the claimant to
// block on, or nil when the claim went through. A drain racing the claim
// just blocks the claim here, where it outranks every demand.
func (l *ledger) claimRegistered(d *Demand) *claimant {
	// Debit-first: a mid-claim unit over-counts, never under.
	l.inUse.Add(int64(d.w)) //nolint:gosec // G115: registered weights come from validated int weights
	l.mu.Lock()
	defer l.mu.Unlock()
	l.demands.Remove(d)
	return l.gateClaim(d.account, d.w)
}

// claimParked runs a returning parked body's claim: its reservation converts
// whole, or the party joins the claimant queue at the tail — behind every
// queued claimant, per the drain discipline's service order — to wait for
// its lent units to come home.
func (l *ledger) claimParked(a *Account, w uint64) *claimant {
	l.inUse.Add(int64(w)) //nolint:gosec // G115: w came from a validated int weight
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gateClaim(a, w)
}

// gateClaim is the run boundary's shared body: convert a whole reservation
// to in-use, or refund the debit and register the party as a claimant. mu
// must be held, the debit already taken.
func (l *ledger) gateClaim(a *Account, w uint64) *claimant {
	if a.shortfall(w) == 0 {
		a.reserved.Add(-w)
		l.refreshAnchor()
		return nil
	}
	// Refund the debit; the claimant re-debits when whole.
	l.inUse.Add(-int64(w)) //nolint:gosec // G115: validated int weight
	c := claimantPool.Get()
	c.account = a
	c.weight = w
	l.claimants.PushBack(c)
	l.waitingClaimants.Add(1)
	// The registering side's arm of the race-closing pair (see release): a
	// pot deposit that loaded the guards before this registration is
	// re-derived here, under the same mutex section that published it — then
	// the recall, because this registration just parked its carried residue.
	l.sweepPot()
	l.recallToFront()
	l.refreshAnchor()
	return c
}

// awaitClaim blocks a claimant until its reservation is whole, then converts
// it to in-use. The wake is a completion signal, so the woken retry misses
// only if a drain intervened between the wake and the retry — it then
// re-parks, still at its place in the claimant queue. A non-nil done aborts
// the wait; the caller owns the subsequent departure. Returns whether the
// claim succeeded.
func (l *ledger) awaitClaim(c *claimant, done <-chan struct{}) bool {
	for {
		ch := c.waiter.Prepare()
		if c.account.shortfall(c.weight) == 0 {
			c.waiter.Finish(false)
			if l.finishClaim(c) {
				return true
			}
			continue // a drain reopened the shortfall before the conversion
		}
		select {
		case <-ch:
			c.waiter.Finish(true)
		case <-done:
			c.waiter.Finish(false)
			return false
		}
	}
}

// finishClaim retires a satisfied claimant: the reservation converts to
// in-use and the frame leaves the queue. The wholeness re-check runs under
// the mutex — a drain that reopened the shortfall after the caller's
// lock-free check sends the claimant back to its park, still at its place
// in the queue.
func (l *ledger) finishClaim(c *claimant) bool {
	l.mu.Lock()
	if c.account.shortfall(c.weight) > 0 {
		l.mu.Unlock()
		return false
	}
	c.account.reserved.Add(-c.weight)
	l.inUse.Add(int64(c.weight)) //nolint:gosec // G115: validated int weight
	l.claimants.Remove(c)
	l.waitingClaimants.Add(-1)
	l.recallToFront() // the front advanced; serve its successor
	l.mu.Unlock()
	claimantPool.Release(c)
	return true
}

// depart settles a party that will never run: its unlent reserved units flow
// home by the delivery order, its receivables are abandoned (a borrower's
// later release routes past the vanished lender emergently), and its account
// closes — all in one mutex section, so no transiently-discoverable departed
// lender exists. Departure is the one unified owner-departure rule; every
// abandonment path (cancelled claim, freed-without-executing, walk-away) is
// this settlement seen from a different call site
// (WORKING_NOTES teardown settlement, items 1-4).
func (l *ledger) depart(d *Demand, c *claimant) {
	l.mu.Lock()
	if d != nil && l.demands.Owns(d) {
		l.demands.Remove(d)
	}
	if c != nil {
		l.claimants.Remove(c)
		l.waitingClaimants.Add(-1)
	}
	a := accountOf(d, c)
	if a != nil {
		if home := a.reserved.Swap(0); home > 0 {
			if residue := l.deliver(home); residue > 0 {
				l.resource.Release(int(residue)) //nolint:gosec // G115: bounded by the reservation balance
			}
		}
		l.loansOutstanding.Add(-int64(a.lent)) //nolint:gosec // G115: lent is bounded by validated weights
		a.lent = 0                             // abandoned: never recalled, repayment routes emergently
		a.closed.Store(true)
	}
	l.recallToFront()
	l.refreshAnchor()
	l.mu.Unlock()
	if d != nil {
		d.account = nil
	}
	if c != nil {
		claimantPool.Release(c)
	}
}

// accountOf returns the departing party's account from whichever frame
// carries it.
func accountOf(d *Demand, c *claimant) *Account {
	if c != nil {
		return c.account
	}
	if d != nil {
		return d.account
	}
	return nil
}
