// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"testing"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/stretchr/testify/require"
)

// newTestSemaphore returns the limiter plus its scheduler for direct
// request-handle access.
func newTestSemaphore(t *testing.T, n int) (Limiter, *directScheduler) {
	t.Helper()
	l := NewSemaphore(nil, n)
	s, ok := l.impl.(*directScheduler)
	require.True(t, ok, "NewSemaphore impl is not a directScheduler")
	return l, s
}

// wakeCounter registers a counting listener on the scheduler's notifier.
// Each registration is one-shot (consumed by one wake), so register as
// many as the wakes the test intends to observe.
type wakeCounter struct {
	count     int
	listeners []*rdvq.Listener
}

func (w *wakeCounter) register(s *directScheduler, n int) {
	for range n {
		l := &rdvq.Listener{}
		l.Notify = func(rdvq.RenotifyFunc) bool {
			w.count++
			return true
		}
		l.AddTo(&s.notify.Listeners)
		w.listeners = append(w.listeners, l)
	}
}

func state(t *testing.T, r request) requestState {
	t.Helper()
	dr, ok := r.(*directRequest)
	require.True(t, ok)
	return dr.state
}

func TestRequestLifecycle_SuspendResume(t *testing.T) {
	_, s := newTestSemaphore(t, 1)

	r := s.newRequest(nil)
	require.Equal(t, requestPending, state(t, r))
	require.NotNil(t, r.notifier())

	require.True(t, r.tryAcquire())
	require.Equal(t, requestHeld, state(t, r))

	require.True(t, r.suspend())
	require.Equal(t, requestSuspended, state(t, r))

	// Re-entrant no-op: the ONE silent case.
	require.False(t, r.suspend())
	require.Equal(t, requestSuspended, state(t, r))

	require.True(t, r.tryResume())
	require.Equal(t, requestHeld, state(t, r))

	r.release()
	require.Equal(t, requestDone, state(t, r))
	r.release() // idempotent
	require.Equal(t, requestDone, state(t, r))
}

func TestRequestLifecycle_PostponeRegrant(t *testing.T) {
	_, s := newTestSemaphore(t, 1)

	a := s.newRequest(nil)
	require.True(t, a.tryAcquire())
	a.postpone()
	require.Equal(t, requestPostponed, state(t, a))

	// The yielded slot is genuinely available to a sibling...
	b := s.newRequest(nil)
	require.True(t, b.tryAcquire())
	// ...and the postponed request cannot re-grant while it's taken.
	require.False(t, a.tryAcquire())
	require.Equal(t, requestPostponed, state(t, a))

	b.release()
	require.True(t, a.tryAcquire(), "re-grant after the slot frees")
	require.Equal(t, requestHeld, state(t, a))
	a.release()
}

func TestRequestSuspendFreesSlotForSibling(t *testing.T) {
	_, s := newTestSemaphore(t, 1)

	a := s.newRequest(nil)
	require.True(t, a.tryAcquire())

	b := s.newRequest(nil)
	require.False(t, b.tryAcquire(), "limit=1: sibling must not acquire while held")

	require.True(t, a.suspend())
	require.True(t, b.tryAcquire(), "suspend must free the slot — this IS the livelock fix")

	require.False(t, a.tryResume(), "reclaim must fail while the sibling holds the slot")
	b.release()
	require.True(t, a.tryResume())
	a.release()
}

// parkTransitions are the two capacity-returning park transitions, shared
// by the no-double-credit and notify-discipline tests.
var parkTransitions = []struct {
	name string
	park func(t *testing.T, r request)
}{
	{"suspend", func(t *testing.T, r request) {
		t.Helper()
		require.True(t, r.suspend())
	}},
	{"postpone", func(t *testing.T, r request) {
		t.Helper()
		r.postpone()
	}},
}

// TestRequestNoDoubleCredit pins that release() from SUSPENDED/POSTPONED
// does not credit capacity a second time: acquire→suspend→release must
// leave the semaphore exactly where acquire→release does.
func TestRequestNoDoubleCredit(t *testing.T) {
	for _, tc := range parkTransitions {
		t.Run(tc.name, func(t *testing.T) {
			_, s := newTestSemaphore(t, 1)

			a := s.newRequest(nil)
			require.True(t, a.tryAcquire())
			tc.park(t, a)

			b := s.newRequest(nil)
			require.True(t, b.tryAcquire(), "parked give-back frees the slot")

			a.release() // discard — must NOT credit again

			c := s.newRequest(nil)
			require.False(t, c.tryAcquire(),
				"capacity must still be 1, not 2, after discarding the parked request")

			b.release()
			require.True(t, c.tryAcquire())
			c.release()
		})
	}
}

// TestRequestNotifyDiscipline pins "capacity-returning transitions notify;
// state-discarding ones do not" — and that each notifies exactly once.
func TestRequestNotifyDiscipline(t *testing.T) {
	t.Run("held-release notifies once", func(t *testing.T) {
		_, s := newTestSemaphore(t, 1)
		a := s.newRequest(nil)
		require.True(t, a.tryAcquire())

		var w wakeCounter
		w.register(s, 2)
		a.release()
		require.Equal(t, 1, w.count, "HELD-release wakes exactly one waiter per slot")
	})

	for _, tc := range parkTransitions {
		t.Run(tc.name+" notifies once, discard release does not", func(t *testing.T) {
			_, s := newTestSemaphore(t, 1)
			a := s.newRequest(nil)
			require.True(t, a.tryAcquire())

			var w wakeCounter
			w.register(s, 2)

			tc.park(t, a)
			require.Equal(t, 1, w.count, "park give-back wakes exactly once")

			a.release() // discard
			require.Equal(t, 1, w.count, "discard release must not re-notify")

			// Prove the second listener was still live (the silence above
			// wasn't just an empty listener set): a real capacity return
			// reaches it.
			b := s.newRequest(nil)
			require.True(t, b.tryAcquire())
			b.release()
			require.Equal(t, 2, w.count)
		})
	}
}

// TestRequestLegalityPanics pins the method/state legality table: illegal
// transitions are framework bugs and fail loud.
func TestRequestLegalityPanics(t *testing.T) {
	mk := func(t *testing.T, s *directScheduler, st requestState) request {
		t.Helper()
		r := s.newRequest(nil)
		switch st {
		case requestPending:
		case requestHeld:
			require.True(t, r.tryAcquire())
		case requestSuspended:
			require.True(t, r.tryAcquire())
			require.True(t, r.suspend())
		case requestPostponed:
			require.True(t, r.tryAcquire())
			r.postpone()
		case requestDone:
			r.release()
		}
		require.Equal(t, st, state(t, r))
		return r
	}

	for _, tc := range []struct {
		op     string
		call   func(r request)
		states []requestState
	}{
		{"tryAcquire", func(r request) { r.tryAcquire() },
			[]requestState{requestHeld, requestSuspended, requestDone}},
		{"postpone", func(r request) { r.postpone() },
			[]requestState{requestPending, requestSuspended, requestPostponed, requestDone}},
		{"suspend", func(r request) { r.suspend() },
			[]requestState{requestPending, requestPostponed, requestDone}},
		{"tryResume", func(r request) { r.tryResume() },
			[]requestState{requestPending, requestHeld, requestPostponed, requestDone}},
	} {
		for _, st := range tc.states {
			t.Run(tc.op+" in "+st.String(), func(t *testing.T) {
				_, s := newTestSemaphore(t, 2)
				r := mk(t, s, st)
				require.Panics(t, func() { tc.call(r) })
			})
		}
	}
}

// TestSetMaxConcurrencyWakesPostponedListeners pins the capacity-changed
// hook: growth must reach listeners registered while nobody is parked in a
// blocking wait (the hole that motivated replacing the channel with the
// bind-time callback).
func TestSetMaxConcurrencyWakesPostponedListeners(t *testing.T) {
	t.Run("finite growth", func(t *testing.T) {
		l, s := newTestSemaphore(t, 0)
		r := s.newRequest(nil)
		require.False(t, r.tryAcquire(), "limit=0 blocks all")

		var w wakeCounter
		w.register(s, 1)

		SetMaxConcurrency(l, 2)
		require.Equal(t, 1, w.count, "raised ceiling must wake the registered listener")
		require.True(t, r.tryAcquire())
		r.release()
	})

	t.Run("growth to unlimited", func(t *testing.T) {
		l, s := newTestSemaphore(t, 0)

		var w wakeCounter
		w.register(s, 1)

		SetMaxConcurrency(l, -1)
		require.Equal(t, 1, w.count, "unlimited resize must wake all listeners")

		r := s.newRequest(nil)
		require.True(t, r.tryAcquire())
		r.release()
	})

	t.Run("shrink does not wake", func(t *testing.T) {
		l, s := newTestSemaphore(t, 5)

		var w wakeCounter
		w.register(s, 1)

		SetMaxConcurrency(l, 1)
		require.Equal(t, 0, w.count, "lowering the ceiling must not wake anyone")
	})
}
