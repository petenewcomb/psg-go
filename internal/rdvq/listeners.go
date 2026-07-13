// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/petenewcomb/streampool/internal/nbcq"
)

// noop is the default fallback for a Notification with no explicit conservation
// action: a never-nil terminal so Forward/Empty need no nil check on the fallback.
func noop() {}

// Listeners manages a queue of notification functions waiting to be signaled.
// It provides a subscription mechanism for goroutines to register for notifications
// when work becomes available.
type Listeners struct {
	q nbcq.Queue[NotifyFunc]
}

// Init initializes the Listeners for use. Must be called before any other operations.
func (c *Listeners) Init() {
	traceRegion := "rdvq.Listeners.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listeners=%p, nbcq.Queue=%p", c, &c.q)

	c.q.Init()
}

//nolint:contextcheck // background context used only for tracing
func (c *Listeners) add(notifyFn NotifyFunc) {
	traceRegion := "rdvq.Listeners.add"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listeners=%p", c)

	if notifyFn == nil {
		panic("notifyFn is nil")
	}
	c.q.PushBack(notifyFn)
}

// Notify delivers a wake to one waiting listener, running fallback if no listener takes
// it (total conservation). A nil fallback defaults to noop. The listener receives a
// waiter-style (terminal) Notification: a standalone Listeners has no enclosing Notifier
// to re-circulate through. See [Notifier.Notify] for the re-circulating listener-style
// delivery.
func (c *Listeners) Notify(fallback func()) {
	if fallback == nil {
		fallback = noop
	}
	if !c.deliver(Notification{fallback: fallback}) {
		fallback()
	}
}

// deliver offers m to waiting listeners in FIFO order, returning true once one takes it
// (its NotifyFunc returned true) and false if none did. Listeners that decline
// synchronously (return false) are dropped and the next is tried.
//
//nolint:contextcheck // background context used only for tracing
func (c *Listeners) deliver(m Notification) bool {
	traceRegion := "rdvq.Listeners.deliver"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listeners=%p", c)

	for {
		notifyFn, ok := c.q.TryPopFront()
		if !ok {
			return false
		}

		if notifyFn(m) {
			return true
		}
	}
}

// NotifyAll signals all waiting listeners.
// This is typically used during shutdown or when conditions change globally.
//
//nolint:contextcheck // background context used only for tracing
func (c *Listeners) NotifyAll() {
	traceRegion := "rdvq.Listeners.NotifyAll"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listeners=%p", c)

	for {
		notifyFn, ok := c.q.TryPopFront()
		if !ok {
			break
		}
		notifyFn(Notification{fallback: noop})
	}
}

// Reset prepares the Listeners for reuse.
// Panics if called when there are still pending listeners.
func (c *Listeners) Reset() {
	if _, ok := c.q.TryPopFront(); ok {
		panic("resetting non-empty Listeners")
	}
}
