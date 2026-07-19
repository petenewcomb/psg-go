// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"testing"
	"time"
)

// The Waiter protocol: Prepare/select/Finish on the owner side, gen-guarded
// Notify on the notifier side. Stale and spurious wakes are structurally
// harmless: a Notify against a finished wait is a no-op, and a wake that loses
// the abandon race is drained by Finish.
func TestWaiterProtocol(t *testing.T) {
	var w Waiter
	w.Init()

	// Notify with no wait in flight: no-op.
	w.Notify()

	// Prepare → Notify → receive → Finish(received).
	ch := w.Prepare()
	w.Notify()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("wake not delivered to prepared waiter")
	}
	w.Finish(true)

	// Prepare → no wake → Finish(!received) abandons; a later Notify is inert.
	abandonedCh := w.Prepare()
	w.Finish(false)
	w.Notify()
	select {
	case <-abandonedCh:
		t.Fatal("wake delivered against an abandoned wait")
	default:
	}

	// Lost-abandon race: Notify lands after the owner's select gave up but
	// before Finish — Finish drains the inbound wake and the waiter is reusable.
	_ = w.Prepare()
	w.Notify()
	w.Finish(false)
	ch = w.Prepare()
	w.Notify()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("waiter not reusable after a drained lost-abandon wake")
	}
	w.Finish(true)
}
