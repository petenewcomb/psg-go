// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttachments_BorrowOrderAndReopenedShortfall(t *testing.T) {
	l, _ := newLedger(4)
	var at Attachments

	// Two satisfied registered demands and one parked episode lender, all
	// attached to the same wave's anchor.
	require.Equal(t, uint64(4), l.tryReserve(4))
	lender := accountPool.Get()
	lender.reserved.Add(2)

	d1, att1 := registerDemand(l, 1)
	d2, att2 := registerDemand(l, 1)
	// The two units the lender does not hold run and release, funding the
	// demands through delivery.
	l.inUse.Add(2)
	l.release(1)
	l.release(1)
	require.Equal(t, int64(1), att1.fired.Load())
	require.Equal(t, int64(1), att2.fired.Load())

	e := func() *Episode {
		l.mu.Lock()
		defer l.mu.Unlock()
		ra1 := l.attachRegistration(&at, d1)
		ra2 := l.attachRegistration(&at, d2)
		_ = ra1
		_ = ra2
		return l.openEpisode(&at, lender)
	}()
	assert.False(t, at.Empty())

	// A beneficiary borrows three: the discipline drains registrations
	// first (tail-first: d2 then d1), then the episode lender.
	borrower := accountPool.Get()
	l.mu.Lock()
	got := l.borrowFromAnchor(&at, borrower, 3)
	l.mu.Unlock()
	assert.Equal(t, uint64(3), got)
	assert.Equal(t, uint64(0), d2.account.reserved.Load(), "tail registration drained first")
	assert.Equal(t, uint64(0), d1.account.reserved.Load())
	assert.Equal(t, uint64(1), lender.reserved.Load(), "episode drained last, only the remainder")
	assert.Equal(t, uint64(1), lender.lent)
	assert.Equal(t, uint64(1), d1.account.lent)
	assert.Equal(t, uint64(1), d2.account.lent)
	assert.Equal(t, int64(3), l.loansOutstanding.Load())

	// The drained demands' shortfalls reopened: the anchor republished.
	assert.NotNil(t, l.anchor.Load())

	// The borrower claims its borrowed reservation, runs, and completes; its
	// release repays in service order — the demands are made whole again
	// (front first), receivables settle.
	borrower.reserved.Store(0)
	l.inUse.Add(3)
	l.release(3)
	assert.Equal(t, uint64(0), d1.account.lent)
	assert.Equal(t, uint64(0), d2.account.lent)
	assert.Equal(t, int64(2), att1.fired.Load(), "re-completion fires the completion signal again")
	assert.Equal(t, uint64(1), lender.lent, "queue members outrank the parked lender; its receivable stands")

	l.mu.Lock()
	l.closeEpisode(&at, e)
	l.mu.Unlock()
	assert.False(t, at.Empty(), "registrations still attached")
}

func TestAttachments_ShiftDebt(t *testing.T) {
	l, _ := newLedger(6)

	// Inner lender L lent 2 away; ancestor U holds margin 3.
	require.Equal(t, uint64(6), l.tryReserve(6))
	inner := accountPool.Get()
	inner.reserved.Add(2)
	ancestor := accountPool.Get()
	ancestor.reserved.Add(3)
	borrower := accountPool.Get()
	l.mu.Lock()
	require.Equal(t, uint64(1), l.borrow(inner, borrower, 1))
	require.Equal(t, uint64(1), l.borrow(inner, borrower, 1)) // second unit: drains the last of inner's reserve
	l.mu.Unlock()
	require.Equal(t, uint64(2), inner.lent)
	require.Equal(t, int64(2), l.loansOutstanding.Load())

	// Normalization shifts the debt up: the ancestor's margin makes the
	// inner lender whole and the ancestor becomes lender-of-record; the
	// pool-wide loan count is unchanged (reassignment, never a fresh grant).
	l.mu.Lock()
	shifted := l.shiftDebt(inner, ancestor, 2)
	l.mu.Unlock()
	assert.Equal(t, uint64(2), shifted)
	assert.Equal(t, uint64(0), inner.lent)
	assert.Equal(t, uint64(2), inner.reserved.Load())
	assert.Equal(t, uint64(1), ancestor.reserved.Load())
	assert.Equal(t, uint64(2), ancestor.lent)
	assert.Equal(t, int64(2), l.loansOutstanding.Load())

	// The shift caps at available margin and standing debt.
	l.mu.Lock()
	assert.Equal(t, uint64(0), l.shiftDebt(inner, ancestor, 5), "no inner debt remains")
	l.mu.Unlock()
}
