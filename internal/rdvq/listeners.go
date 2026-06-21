// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/petenewcomb/streampool/internal/nbcq"
)

// NoopRenotify is a no-op RenotifyFunc that can be used when no re-notification
// action is needed. It serves as a placeholder in notification systems.
func NoopRenotify() {}

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

// Notify attempts to signal one waiting listener.
// Returns true if a listener was successfully notified, false if no listeners were available.
// The renotifyFn will be passed to the listener's NotifyFunc.
//
//nolint:contextcheck // background context used only for tracing
func (c *Listeners) Notify(renotifyFn RenotifyFunc) bool {
	traceRegion := "rdvq.Listeners.notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Listeners=%p", c)

	for {
		notifyFn, ok := c.q.TryPopFront()
		if !ok {
			return false
		}

		if notifyFn(renotifyFn) {
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
		notifyFn(NoopRenotify)
	}
}

// Reset prepares the Listeners for reuse.
// Panics if called when there are still pending listeners.
func (c *Listeners) Reset() {
	if _, ok := c.q.TryPopFront(); ok {
		panic("resetting non-empty Listeners")
	}
}
