// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/streampool/internal/omnipool"
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

// Notify attempts to signal a listener or waiter. It prioritizes listeners
// over waiters since listeners typically represent in-process work.
// Returns true if a notification was successfully delivered, false otherwise.
//
//nolint:contextcheck // background context used only for tracing
func (n *Notifier) Notify(renotifyFn RenotifyFunc) bool {
	traceRegion := "rdvq.Notifier.Notify"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Notifier=%p", n)

	if renotifyFn == nil {
		renotifyFn = NoopRenotify
	}

	r := wrappedRenotifyPool.Get()
	r.n = n
	r.wrappedFn = renotifyFn
	if n.Listeners.Notify(r.renotifyFn) {
		return true
	}
	wrappedRenotifyPool.Put(r)
	return n.Waiters.Notify(renotifyFn)
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

type wrappedRenotify struct {
	n         *Notifier
	wrappedFn RenotifyFunc

	renotifyFn RenotifyFunc // avoid reallocating closure
}

func (r *wrappedRenotify) Init() {
	r.renotifyFn = r.renotify
}

func (r *wrappedRenotify) Reset() {
	*r = wrappedRenotify{
		renotifyFn: r.renotifyFn,
	}
}

func (r *wrappedRenotify) renotify() {
	if !r.n.Notify(r.wrappedFn) {
		r.wrappedFn()
	}
	wrappedRenotifyPool.Put(r)
}

var wrappedRenotifyPool = omnipool.For[wrappedRenotify]()
