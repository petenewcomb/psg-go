// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingAttendant records completion wakes for a registered demand.
type countingAttendant struct{ fired atomic.Int64 }

func (a *countingAttendant) Notify() { a.fired.Add(1) }

// newLedger builds a ledger over the test semaphore (export_test.go).
func newLedger(capacity int) (*ledger, *semaphore) {
	sem := &semaphore{capacity: capacity}
	return &ledger{resource: sem}, sem
}

// registerDemand is the test-side arrival that missed: draw what the pot
// offers, then register with a fresh account.
func registerDemand(l *ledger, w uint64) (*Demand, *countingAttendant) {
	d := NewDemand()
	att := &countingAttendant{}
	d.attendant = att
	drawn := l.tryReserve(w)
	l.register(d, accountPool.Get(), w, drawn)
	return d, att
}

// checkedOut is the conservation identity's left side: units the Resource
// considers out.
func checkedOut(sem *semaphore) uint64 {
	return uint64(sem.inFlight.Load()) //nolint:gosec // test capacities are small
}

// dumpLedger reports the full money state at a stall, from the stalled
// claimant's point of view.
func dumpLedger(t *testing.T, l *ledger, sem *semaphore, stuck *claimant) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	t.Errorf("claim stalled: weight=%d reserved=%d", stuck.weight, stuck.account.reserved.Load())
	t.Errorf("pool: checkedOut=%d capacity=%d inUse=%d waitingClaimants=%d anchor=%p",
		sem.inFlight.Load(), sem.capacity, l.inUse.Load(), l.waitingClaimants.Load(), l.anchor.Load())
	i := 0
	for c := l.claimants.Front(); c != nil; c = l.claimants.Next(c) {
		t.Errorf("claimant[%d]: weight=%d reserved=%d stuck=%v", i, c.weight, c.account.reserved.Load(), c == stuck)
		i++
	}
	i = 0
	for d := l.demands.Front(); d != nil; d = l.demands.Next(d) {
		t.Errorf("demand[%d]: w=%d reserved=%d", i, d.w, d.account.reserved.Load())
		i++
	}
}

// ledgerHeld is the identity's right side: every reservation plus in-use.
func ledgerHeld(l *ledger) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	sum := uint64(l.inUse.Load()) //nolint:gosec // test capacities are small
	for c := l.claimants.Front(); c != nil; c = l.claimants.Next(c) {
		sum += c.account.reserved.Load()
	}
	for d := l.demands.Front(); d != nil; d = l.demands.Next(d) {
		sum += d.account.reserved.Load()
	}
	return sum
}

func TestLedger_HappyPathStaysPrivate(t *testing.T) {
	l, sem := newLedger(4)

	drawn := l.tryReserve(3)
	require.Equal(t, uint64(3), drawn)
	l.claimPrivate(3)
	assert.Equal(t, int64(3), l.inUse.Load())

	l.release(3)
	assert.Equal(t, int64(0), l.inUse.Load())
	assert.Equal(t, uint64(0), checkedOut(sem))
	assert.Nil(t, l.anchor.Load())
}

func TestLedger_PartialDrawBecomesStartingBalance(t *testing.T) {
	l, sem := newLedger(5)

	// A consumes the pot.
	require.Equal(t, uint64(5), l.tryReserve(5))
	l.claimPrivate(5)

	// B arrives heavier than anything free: draws nothing, registers, and
	// becomes the anchor.
	d, att := registerDemand(l, 3)
	require.NotNil(t, l.anchor.Load())
	assert.Same(t, d, l.anchor.Load())

	// A's release delivers into B's reservation first; only the residue
	// reaches the pot. B's attendant fires exactly once, when the
	// reservation completes.
	l.release(5)
	assert.Equal(t, uint64(3), d.account.reserved.Load())
	assert.Equal(t, int64(1), att.fired.Load())
	assert.Nil(t, l.anchor.Load(), "satisfied demand no longer anchors")
	assert.Equal(t, uint64(3), checkedOut(sem), "pot got the residue")

	// Claim-only retirement: the satisfied entry leaves at its claim.
	require.Nil(t, l.claimRegistered(d))
	assert.Equal(t, int64(3), l.inUse.Load())
	assert.Equal(t, uint64(0), d.account.reserved.Load())
}

func TestLedger_SpillPastSatisfiedEntries(t *testing.T) {
	l, _ := newLedger(4)

	require.Equal(t, uint64(4), l.tryReserve(4))
	l.claimPrivate(4)

	b, attB := registerDemand(l, 2)
	c, attC := registerDemand(l, 3)

	// Two units satisfy B; spill continues into C on the next release even
	// though B (satisfied, unclaimed) still queues ahead of it.
	l.release(2)
	assert.Equal(t, int64(1), attB.fired.Load())
	assert.Equal(t, uint64(0), c.account.reserved.Load())
	assert.Same(t, c, l.anchor.Load(), "anchor advances past the satisfied front")

	l.release(2)
	assert.Equal(t, uint64(2), c.account.reserved.Load(), "spill fills the anchor first")
	assert.Equal(t, int64(0), attC.fired.Load(), "no wake before the reservation is whole")

	// B's protected units are untouched throughout.
	assert.Equal(t, uint64(2), b.account.reserved.Load())
}

func TestLedger_BarrierGatesArrivalsWhileShortfallStands(t *testing.T) {
	l, _ := newLedger(3)

	require.Equal(t, uint64(3), l.tryReserve(3))
	l.claimPrivate(3)
	registerDemand(l, 2)

	// Pot empty, anchor set: a fresh arrival draws nothing.
	assert.Equal(t, uint64(0), l.tryReserve(1))

	// Satisfy the queue; the pot holds the residue and the barrier lifts:
	// a fresh arrival takes from the pot and jumps nobody.
	l.release(3)
	assert.Nil(t, l.anchor.Load())
	assert.Equal(t, uint64(1), l.tryReserve(1))
}

func TestLedger_ClaimMissBecomesClaimantServedBeforeDemands(t *testing.T) {
	l, _ := newLedger(4)

	require.Equal(t, uint64(4), l.tryReserve(4))
	l.claimPrivate(4)

	// B registers and gets a partial fill, then claims before completion:
	// the miss retires the demand and registers the claimant in one section.
	b, _ := registerDemand(l, 3)
	l.release(2)
	require.Equal(t, uint64(2), b.account.reserved.Load())
	cl := l.claimRegistered(b)
	require.NotNil(t, cl)
	assert.Equal(t, int64(1), l.waitingClaimants.Load())
	assert.Equal(t, int64(2), l.inUse.Load(), "missed claim refunds its debit; the original holder's units remain")

	// C registers behind; the next release repays the claimant first even
	// though C's demand also stands short.
	c, attC := registerDemand(l, 2)

	claimDone := make(chan bool, 1)
	go func() { claimDone <- l.awaitClaim(cl, nil) }()

	l.release(1)
	assert.True(t, <-claimDone, "completion wake resolves the claim")
	assert.Equal(t, int64(4), l.inUse.Load(), "claimant's 3 plus the original holder's remaining 1")
	assert.Equal(t, int64(0), l.waitingClaimants.Load())
	assert.Equal(t, uint64(0), c.account.reserved.Load(), "claimant outranked the demand")
	assert.Equal(t, int64(0), attC.fired.Load())

	// The remaining release satisfies C normally.
	l.release(1)
	l.release(3) // the claimant's own weight coming home later
	assert.Equal(t, int64(1), attC.fired.Load())
	require.Nil(t, l.claimRegistered(c))
}

func TestLedger_AbortedClaimDeparts(t *testing.T) {
	l, sem := newLedger(2)

	require.Equal(t, uint64(2), l.tryReserve(2))
	l.claimPrivate(2)

	b, _ := registerDemand(l, 2)
	l.release(1)
	cl := l.claimRegistered(b)
	require.NotNil(t, cl)

	done := make(chan struct{})
	claimDone := make(chan bool, 1)
	go func() { claimDone <- l.awaitClaim(cl, done) }()
	close(done)
	assert.False(t, <-claimDone)

	// Departure homes the partial unit and clears the gate accounting.
	l.depart(b, cl)
	assert.Equal(t, int64(0), l.waitingClaimants.Load())
	assert.Equal(t, uint64(1), checkedOut(sem), "only the running unit stays out")
	l.release(1)
	assert.Equal(t, uint64(0), checkedOut(sem))
}

func TestLedger_DepartedDemandFundsSuccessors(t *testing.T) {
	l, _ := newLedger(2)

	require.Equal(t, uint64(2), l.tryReserve(2))
	l.claimPrivate(2)

	b, _ := registerDemand(l, 2)
	c, attC := registerDemand(l, 2)
	l.release(2)
	require.Equal(t, uint64(2), b.account.reserved.Load())

	// B walks away satisfied-but-unclaimed: its units flow to C by the
	// delivery order rather than stranding.
	l.depart(b, nil)
	assert.Equal(t, uint64(2), c.account.reserved.Load())
	assert.Equal(t, int64(1), attC.fired.Load())
}

func TestLedger_BorrowRepayLifecycle(t *testing.T) {
	l, sem := newLedger(3)

	// L draws the whole pot and parks holding it: reserved, in no queue —
	// the parked-body shape whose units are lendable.
	require.Equal(t, uint64(3), l.tryReserve(3))
	lender := accountPool.Get()
	lender.reserved.Add(3)

	// B registers (dry pool) and borrows two of L's parked units — the
	// in-subtree borrow, initiated by the beneficiary's own acquire path.
	b, attB := registerDemand(l, 2)
	l.mu.Lock()
	require.Equal(t, uint64(2), l.borrow(lender, b.account, 2))
	l.mu.Unlock()
	assert.Equal(t, uint64(1), lender.reserved.Load())
	assert.Equal(t, uint64(2), lender.lent)
	assert.Equal(t, int64(2), l.loansOutstanding.Load())
	assert.Equal(t, int64(0), attB.fired.Load(), "the borrower initiated; no wake owed")

	// B claims whole and runs.
	require.Nil(t, l.claimRegistered(b))
	assert.Equal(t, int64(2), l.inUse.Load())

	// L returns and claims: short by its lent units, it joins the claimant
	// queue carrying its unlent residue.
	cl := l.claimParked(lender, 3)
	require.NotNil(t, cl)
	assert.Equal(t, uint64(1), lender.reserved.Load())

	// B's completion repays: the release routes through delivery (the loans
	// guard forces the mutex path), fills L's shortfall, settles the
	// receivable, and the completion wake resolves L's claim.
	claimDone := make(chan bool, 1)
	go func() { claimDone <- l.awaitClaim(cl, nil) }()
	l.release(2)
	assert.True(t, <-claimDone)
	assert.Equal(t, uint64(0), lender.lent)
	assert.Equal(t, int64(0), l.loansOutstanding.Load())
	assert.Equal(t, int64(3), l.inUse.Load())

	l.release(3)
	assert.Equal(t, uint64(0), checkedOut(sem))
}

func TestLedger_LenderDepartureAbandonsReceivable(t *testing.T) {
	l, sem := newLedger(3)

	require.Equal(t, uint64(3), l.tryReserve(3))
	lender := accountPool.Get()
	lender.reserved.Add(3)

	b, _ := registerDemand(l, 2)
	l.mu.Lock()
	require.Equal(t, uint64(2), l.borrow(lender, b.account, 2))
	l.mu.Unlock()
	require.Nil(t, l.claimRegistered(b))

	// L departs without returning: the receivable is abandoned — lent zeroes,
	// the unlent residue homes, and B's later release routes past the
	// vanished lender emergently (to the pot; no shortfall stands).
	lenderDemand := NewDemand()
	lenderDemand.account = lender
	l.depart(lenderDemand, nil)
	assert.Equal(t, uint64(0), lender.lent)
	assert.Equal(t, int64(0), l.loansOutstanding.Load())
	assert.Equal(t, uint64(2), checkedOut(sem), "residue homed; only B's units stay out")

	l.release(2)
	assert.Equal(t, uint64(0), checkedOut(sem))
}

// TestLedger_ConservationUnderConcurrency hammers arrivals, claims, releases
// and departures from many goroutines and asserts the conservation identity
// (checked-out == Σ reserved + in-use) and the pot-empty invariant at
// quiescence.
func TestLedger_ConservationUnderConcurrency(t *testing.T) {
	l, sem := newLedger(8)

	const workers = 8
	const rounds = 300
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for r := range rounds {
				w := uint64(1 + (seed+r)%3) //nolint:gosec // G115: small positive test weights
				drawn := l.tryReserve(w)
				if drawn == w {
					l.claimPrivate(w)
					l.release(w)
					continue
				}
				// Short draw: register, carrying it as the starting balance.
				d := NewDemand()
				att := &countingAttendant{}
				d.attendant = att
				l.register(d, accountPool.Get(), w, drawn)
				if cl := l.claimRegistered(d); cl != nil {
					if r%7 == 0 {
						done := make(chan struct{})
						close(done)
						if !l.awaitClaim(cl, done) {
							l.depart(d, cl)
							continue
						}
					} else {
						// A stall here is the bug under investigation: dump
						// the ledger's terminal state instead of hanging.
						stall := make(chan struct{})
						tm := time.AfterFunc(20*time.Second, func() { close(stall) })
						ok := l.awaitClaim(cl, stall)
						tm.Stop()
						if !ok {
							dumpLedger(t, l, sem, cl)
							l.depart(d, cl)
							continue
						}
					}
				}
				l.release(w)
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, int64(0), l.inUse.Load())
	assert.Equal(t, int64(0), l.waitingClaimants.Load())
	assert.Equal(t, uint64(0), ledgerHeld(l))
	assert.Equal(t, uint64(0), checkedOut(sem), "all capacity home at quiescence")
	l.mu.Lock()
	assert.False(t, l.unservedShortfall())
	l.mu.Unlock()
}
