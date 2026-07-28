// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/streampool/internal/trace"
)

// Notifier is the notification domain of an unreserved resource (queue space,
// a governor): a Listeners set of one-shot queue relays plus a Waiters set of
// parked goroutines. Delivery is full and unconditional — see NotifyAll and
// docs/notification-conservation.md. (Reserved resources — permit pools — do
// not use a Notifier: their delivery is mode-directed through the demand
// queue's attendants, with a bare Listeners set in the fallback role.)
type Notifier struct {
	Listeners Listeners
	Waiters   Waiters
}

// Init initializes the Notifier for use. Must be called before any other
// operations.
func (n *Notifier) Init() {
	n.Listeners.Init()
	n.Waiters.Init()
}

// NotifyAll delivers a capacity event to the whole domain: every planted relay
// fires (one wake per interested queue), and every parked waiter wakes to
// re-check. No recipient's outcome narrows delivery.
//
//nolint:contextcheck // background context used only for tracing
func (n *Notifier) NotifyAll() {
	traceRegion := "rdvq.Notifier.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Notifier=%p", n)

	n.Listeners.NotifyAll()
	n.Waiters.NotifyAll()
}
