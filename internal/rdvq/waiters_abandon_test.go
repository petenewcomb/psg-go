// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"testing"
	"time"
)

// BenchmarkWaitersAbandon models the blockAcquire abort pattern: a waiter registers
// (PushBack a hint to emptyInboxes) and then aborts via confirmFn (a permit became
// available) WITHOUT a Notify ever popping it. Without reaping, the abandoned hint
// lingers in emptyInboxes holding its nbcq node + value cell out of the pool, so each
// iteration allocates a fresh node + value. With the abandon-path reap (see
// inboxOnlyQueue.reapStale), the storage recycles and steady-state allocs are 0.
func BenchmarkWaitersAbandon(b *testing.B) {
	var w Waiters
	w.Init()
	abort := func() bool { return false } // confirmFn: a permit is available; abort the wait
	sel := func(<-chan Notification) Notification { return Notification{} }
	b.ReportAllocs()
	for b.Loop() {
		w.WaitFunc(abort, sel)
	}
}

// BenchmarkWaitersNotified is the control: every registration is matched by a Notify
// that pops it, so node+value recycle and steady-state allocs are ~0.
func BenchmarkWaitersNotified(b *testing.B) {
	var w Waiters
	w.Init()
	confirm := func() bool { return true } // proceed to block
	sel := func(ch <-chan Notification) Notification { return <-ch }
	b.ReportAllocs()
	for b.Loop() {
		done := make(chan struct{})
		go func() { w.WaitFunc(confirm, sel); close(done) }()
		for !w.Deliver(Notification{fallback: noop}) { // pop the registered waiter
		}
		<-done
	}
}

// TestWaitersReapPreservesLiveWaiter guards the reap's safety invariant: reapStale
// must re-push (never drop) a still-live waiter hint. A registers and parks (a live
// hint); then a run of register-and-abandon waiters triggers reapStale, which front-pops
// hints and will encounter A's. If the reap dropped it, A could never be woken — so we
// assert a subsequent Notify still reaches A.
func TestWaitersReapPreservesLiveWaiter(t *testing.T) {
	var w Waiters
	w.Init()

	aRegistered := make(chan struct{})
	gotA := make(chan struct{})
	go func() {
		w.WaitFunc(
			func() bool { close(aRegistered); return true }, // register, signal, then park
			func(ch <-chan Notification) Notification { m := <-ch; close(gotA); return m },
		)
	}()
	<-aRegistered // A's hint is now published in emptyInboxes

	// Abandon enough times to drive reapStale across A's live hint (and past its budget).
	for range reapBudget + 2 {
		w.WaitFunc(
			func() bool { return false }, // abort → abandon → reapStale
			func(<-chan Notification) Notification { return Notification{} },
		)
	}

	if !w.Deliver(Notification{fallback: noop}) {
		t.Fatal("Notify found no waiter: reap dropped the live waiter (lost wakeup)")
	}
	select {
	case <-gotA:
	case <-time.After(2 * time.Second):
		t.Fatal("live waiter A was not woken after reaps")
	}
}
