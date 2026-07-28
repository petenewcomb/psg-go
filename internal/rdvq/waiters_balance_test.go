// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/petenewcomb/streampool/internal/rdvq"
	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

// waitOnce runs one wait that never blocks: the selectFn abandons immediately.
// The pair (received, parkAttempted) distinguishes the three outcomes: a
// consumed miss (true, false), a park attempt (false, true), and a confirmFn
// decline (false, false). A wake can never arrive during the call because the
// selectFn does not wait for one.
func waitOnce(w *rdvq.Waiters, confirm bool) (received, parkAttempted bool) {
	received = w.WaitFunc(
		func() bool { return confirm },
		func(<-chan struct{}) bool {
			parkAttempted = true
			return false
		},
	)
	return
}

func TestWaitersBalance_MissThenImmediateWait(t *testing.T) {
	var w rdvq.Waiters
	w.Init()

	// A wake with no parked waiter and the persist handler is recorded.
	w.Notify(rdvq.PersistMiss)

	// The recorded miss aborts the next wait before it parks.
	received, parkAttempted := waitOnce(&w, true)
	assert.True(t, received)
	assert.False(t, parkAttempted)

	// The balance is drained: the next wait parks.
	received, parkAttempted = waitOnce(&w, true)
	assert.False(t, received)
	assert.True(t, parkAttempted)
}

func TestWaitersBalance_PersistMissIsPureSentinel(t *testing.T) {
	var w rdvq.Waiters
	w.Init()

	// Persistence is reachable only through Notify's identity recognition;
	// the sentinel's own HandleMiss panics.
	assert.Panics(t, func() { rdvq.PersistMiss.HandleMiss(&w) })

	// The panic recorded nothing: the next wait parks.
	received, parkAttempted := waitOnce(&w, true)
	assert.False(t, received)
	assert.True(t, parkAttempted)
}

func TestWaitersBalance_NilHandlerDrops(t *testing.T) {
	var w rdvq.Waiters
	w.Init()

	// A wake with no parked waiter and a nil handler is dropped: nothing is
	// recorded, so the next wait parks.
	w.Notify(nil)

	received, parkAttempted := waitOnce(&w, true)
	assert.False(t, received)
	assert.True(t, parkAttempted)
}

func TestWaitersBalance_MultiplicityMatchesEvents(t *testing.T) {
	var w rdvq.Waiters
	w.Init()

	// N recorded misses must abort N park attempts — the balance is a counter,
	// not a sticky bit.
	const n = 3
	for range n {
		w.Notify(rdvq.PersistMiss)
	}
	for i := range n {
		received, parkAttempted := waitOnce(&w, true)
		assert.True(t, received, "wait %d", i)
		assert.False(t, parkAttempted, "wait %d", i)
	}
	received, parkAttempted := waitOnce(&w, true)
	assert.False(t, received)
	assert.True(t, parkAttempted)
}

func TestWaitersBalance_MissFuncBypassesBalance(t *testing.T) {
	var w rdvq.Waiters
	w.Init()

	// A miss with a MissFunc handler runs it instead of touching the balance.
	missed := 0
	w.Notify(rdvq.MissFunc(func() { missed++ }))
	assert.Equal(t, 1, missed)

	received, parkAttempted := waitOnce(&w, true)
	assert.False(t, received)
	assert.True(t, parkAttempted)
}

func TestWaitersBalance_DeclineLeavesBalance(t *testing.T) {
	var w rdvq.Waiters
	w.Init()

	w.Notify(rdvq.PersistMiss)

	// A confirmFn decline may not consume the miss: decline-implies-sweep is
	// not provable at this level.
	received, parkAttempted := waitOnce(&w, false)
	assert.False(t, received)
	assert.False(t, parkAttempted)

	// The miss is still there for the next approving wait.
	received, parkAttempted = waitOnce(&w, true)
	assert.True(t, received)
	assert.False(t, parkAttempted)
}

func TestWaitersBalance_NotifyAllNeutral(t *testing.T) {
	var w rdvq.Waiters
	w.Init()

	// NotifyAll with nobody parked records nothing: its broadcast is a
	// re-check prompt whose information the re-check re-derives.
	w.NotifyAll()
	received, parkAttempted := waitOnce(&w, true)
	assert.False(t, received)
	assert.True(t, parkAttempted)

	// NotifyAll does not clear recorded misses either: a mid-life NotifyAll
	// with nobody parked must not destroy live recorded misses.
	w.Notify(rdvq.PersistMiss)
	w.Notify(rdvq.PersistMiss)
	w.NotifyAll()
	for i := range 2 {
		received, parkAttempted := waitOnce(&w, true)
		assert.True(t, received, "wait %d", i)
		assert.False(t, parkAttempted, "wait %d", i)
	}
}

func TestWaitersBalance_Reset(t *testing.T) {
	var w rdvq.Waiters
	w.Init()

	w.Notify(rdvq.PersistMiss)
	w.Notify(rdvq.PersistMiss)
	w.Reset()

	// Reset returned the set to its rest state: the misses are gone and the
	// next wait parks.
	received, parkAttempted := waitOnce(&w, true)
	assert.False(t, received)
	assert.True(t, parkAttempted)

	// The set remains fully usable after Reset.
	w.Notify(rdvq.PersistMiss)
	received, parkAttempted = waitOnce(&w, true)
	assert.True(t, received)
	assert.False(t, parkAttempted)
}

// TestWaitersBalance_Model exercises the balance semantics against a
// reference counter through random operation sequences. All waits are
// non-blocking (see waitOnce), so no waiter is ever parked when a Notify
// runs: every Notify is a miss and its handler decides the fate. The core
// property: a wait never attempts to park while the model balance is
// positive.
func TestWaitersBalance_Model(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var w rdvq.Waiters
		w.Init()
		balance := 0

		t.Repeat(map[string]func(*rapid.T){
			"notifyPersist": func(t *rapid.T) {
				w.Notify(rdvq.PersistMiss)
				balance++
			},
			"notifyNil": func(t *rapid.T) {
				w.Notify(nil)
			},
			"notifyMissFunc": func(t *rapid.T) {
				missed := 0
				w.Notify(rdvq.MissFunc(func() { missed++ }))
				if missed != 1 {
					t.Fatalf("MissFunc run %d times, want 1", missed)
				}
			},
			"notifyAll": func(t *rapid.T) {
				w.NotifyAll()
			},
			"waitApprove": func(t *rapid.T) {
				received, parkAttempted := waitOnce(&w, true)
				if balance > 0 {
					if !received || parkAttempted {
						t.Fatalf("balance %d: wait got received=%v parkAttempted=%v, want consume without park",
							balance, received, parkAttempted)
					}
					balance--
				} else if received || !parkAttempted {
					t.Fatalf("balance 0: wait got received=%v parkAttempted=%v, want park attempt",
						received, parkAttempted)
				}
			},
			"waitDecline": func(t *rapid.T) {
				received, parkAttempted := waitOnce(&w, false)
				if received || parkAttempted {
					t.Fatalf("declined wait got received=%v parkAttempted=%v", received, parkAttempted)
				}
			},
			"reset": func(t *rapid.T) {
				w.Reset()
				balance = 0
			},
		})
	})
}

// TestWaitersBalance_ConcurrentLiveness storms Notify(PersistMiss) against
// parked blind waiters and then verifies the terminal state: every
// notification either woke a waiter, was recorded and later consumed, or
// collapsed into a waiter that was already returning (the abandon-race drain
// — its taker re-checks by contract). Liveness is the real assertion: the
// drain loop terminating proves no recorded miss is stranded and no waiter
// is parked beside a positive balance. Run under -race, this is the
// unit-level check of the record/re-offer serialization; the sim's
// quiet-wedge configurations are the system-level gate.
func TestWaitersBalance_ConcurrentLiveness(t *testing.T) {
	var w rdvq.Waiters
	w.Init()

	const notifiers = 4
	const perNotifier = 500
	const parkers = 4

	var received atomic.Int64
	stop := make(chan struct{})
	var parkerWG sync.WaitGroup
	for range parkers {
		parkerWG.Add(1)
		go func() {
			defer parkerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got := w.WaitFunc(
					func() bool { return true },
					func(waitCh <-chan struct{}) bool {
						select {
						case <-waitCh:
							return true
						case <-stop:
							return false
						}
					},
				)
				if got {
					received.Add(1)
				}
			}
		}()
	}

	var notifierWG sync.WaitGroup
	for range notifiers {
		notifierWG.Add(1)
		go func() {
			defer notifierWG.Done()
			for range perNotifier {
				w.Notify(rdvq.PersistMiss)
			}
		}()
	}
	notifierWG.Wait()
	close(stop)
	parkerWG.Wait()

	// Drain what remains in the balance. Termination bounds the loop: each
	// pass either consumes a recorded miss or proves the balance empty by
	// reaching a park attempt.
	drained := int64(0)
	for {
		got, parkAttempted := waitOnce(&w, true)
		if got {
			drained++
			continue
		}
		assert.True(t, parkAttempted)
		break
	}

	// Accounting bounds, not equality: a wake can collapse into a returning
	// waiter (undercount), and the record/re-offer race can add one bounded
	// extra wake per event (overcount). Total service stays within [1, 2N].
	total := received.Load() + drained
	n := int64(notifiers * perNotifier)
	assert.Positive(t, total)
	assert.LessOrEqual(t, total, 2*n)
}
