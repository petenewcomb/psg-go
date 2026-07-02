// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/streampool/internal/trace"
)

// A Notifier routes notifications to its embedded Listeners and Waiters. It
// notifies listeners before waiters, since listeners typically represent
// in-process work and waiters represent new work.
type Notifier struct {
	Listeners
	Waiters
}

// Init initializes the Notifier's embedded Listeners and Waiters.
// Must be called before any other operations.
//
//nolint:contextcheck // background context used only for tracing
func (n *Notifier) Init() {
	traceRegion := "rdvq.Notifier.Init"
	trace.Logf(context.Background(), traceRegion,
		"Notifier=%p, Listeners=%p, Waiters=%p",
		n, &n.Listeners, &n.Waiters)

	n.Listeners.Init()
	n.Waiters.Init()
}

// Notify delivers a wake to one listener or waiter, prioritizing listeners over waiters
// since listeners typically represent in-process work. It is total: if no consumer takes
// the wake, fallback runs synchronously — so callers need no `if !Notify {fallback()}`
// guard. A nil fallback defaults to noop.
//
// Listeners receive a listener-style Notification (a Forward they cannot use
// re-circulates through this Notifier); waiters receive a waiter-style Notification (a
// Forward runs fallback terminally). See [Notification].
//
//nolint:contextcheck // background context used only for tracing
func (n *Notifier) Notify(fallback func()) {
	traceRegion := "rdvq.Notifier.Notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Notifier=%p", n)

	if fallback == nil {
		fallback = noop
	}
	if n.Listeners.deliver(Notification{n: n, fallback: fallback}) {
		return
	}
	if n.Waiters.deliver(Notification{fallback: fallback}) {
		return
	}
	fallback()
}

// NotifyAll signals all listeners and waiters.
// This is typically used during shutdown or when conditions change globally.
//
//nolint:contextcheck // background context used only for tracing
func (n *Notifier) NotifyAll() {
	traceRegion := "rdvq.Notifier.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Notifier=%p", n)

	n.Listeners.NotifyAll()
	n.Waiters.NotifyAll()
}
