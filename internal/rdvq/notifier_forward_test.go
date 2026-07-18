// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// receiveWake parks a goroutine on wait and hands the notification it receives
// back to the test. It returns after the park's register-then-confirm has
// completed, so a single total notify is guaranteed to reach the waiter.
func receiveWake(t *testing.T, wait func(confirmFn func() bool) Notification) <-chan Notification {
	t.Helper()
	got := make(chan Notification, 1)
	parked := make(chan struct{})
	go func() {
		got <- wait(func() bool {
			close(parked)
			return true
		})
	}()
	<-parked
	return got
}

func awaitWake(t *testing.T, got <-chan Notification) Notification {
	t.Helper()
	select {
	case m := <-got:
		return m
	case <-time.After(10 * time.Second):
		t.Fatal("notification never reached the parked waiter")
		return Notification{}
	}
}

// A waiter-delivered notification forwards back through its origin Notifier, so
// a registrant that arrived after the waiter was woken — here a listener
// planted between delivery and the Forward — still receives the wake. Pins the
// conservation rule that no delivery outcome ends a token (invariant 4 of
// docs/notification-conservation.md): the fallback is reserved for exhaustion.
func TestWaiterForwardRecirculatesThroughNotifier(t *testing.T) {
	chk := require.New(t)
	var n Notifier
	n.Init()

	got := receiveWake(t, func(confirmFn func() bool) Notification {
		m, err := n.Waiters.Wait(context.Background(), confirmFn)
		chk.NoError(err)
		return m
	})

	fallbackRan := 0
	n.Notify(func() { fallbackRan++ })
	m := awaitWake(t, got)
	chk.True(m.Received())
	chk.Zero(fallbackRan, "delivered to the waiter, not the fallback")

	// The waiter could not use the wake. Under the old waiter-terminal
	// semantics its Forward would run the fallback; under conservation it
	// re-circulates through the origin Notifier — reaching this listener,
	// registered only after the waiter was woken.
	listenerFired := 0
	l := &Listener{Notify: func(Notification) bool {
		listenerFired++
		return true
	}}
	l.AddTo(&n.Listeners)

	m.Forward()
	chk.Equal(1, listenerFired, "the forward must re-circulate to the late-registered listener")
	chk.Zero(fallbackRan, "the forward must not run the fallback while a registrant existed")

	// With the domain now exhausted (the one-shot listener was consumed and no
	// waiter is parked), a fresh notification's walk ends in the fallback.
	n.Notify(func() { fallbackRan++ })
	chk.Equal(1, fallbackRan, "exhaustion runs the fallback")
}

// A standalone Waiters is its own whole notification domain: a wake it delivers
// carries no origin, so a Forward the waiter cannot use runs the fallback —
// that IS exhaustion there, not a shortcut.
func TestStandaloneWaiterForwardIsExhaustion(t *testing.T) {
	chk := require.New(t)
	var w Waiters
	w.Init()

	got := receiveWake(t, func(confirmFn func() bool) Notification {
		m, err := w.Wait(context.Background(), confirmFn)
		chk.NoError(err)
		return m
	})

	fallbackRan := 0
	w.Notify(func() { fallbackRan++ })
	m := awaitWake(t, got)
	chk.True(m.Received())
	chk.Zero(fallbackRan)

	m.Forward()
	chk.Equal(1, fallbackRan, "a standalone waiter's forward runs the fallback")
}
