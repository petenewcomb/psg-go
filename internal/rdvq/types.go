// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// ProcessValueFunc is called to process a value retrieved from a queue.
type ProcessValueFunc[T any] = func(value T)

// Notification is the value-struct wake handed to a consumer (a listener's NotifyFunc
// or a parked waiter). A value type cannot be
// forgotten-and-leaked the way a pooled wrapper could, so conservation is
// total by construction.
//
// The zero value is the "no wake" sentinel ([Notification.Received] reports false): a
// consumer that was woken by ctx cancellation or an idle timeout rather than a
// notification observes it. A delivered Notification always carries a non-nil fallback
// (defaulting to noop), so Received distinguishes delivered from not.
//
// A consumer that takes a wake owns it and settles it exactly one of two ways:
//
//   - used it productively — do nothing; simply not calling Forward suppresses the
//     fallback (and where the notification was stored, zero the field so a later pass
//     does not Forward it). There is no explicit "consume" call: a value receiver could
//     not record one anyway, so it would enforce nothing.
//   - could not use it — [Notification.Forward] re-offers it. A listener-style
//     notification (n set) re-circulates through the Notifier so another listener or
//     waiter can use the still-live reserved resource; a waiter-style notification
//     (n nil) runs the fallback terminally.
type Notification struct {
	// n, when set, is the Notifier a listener-style Forward re-circulates through; nil
	// marks a waiter-style notification whose Forward is terminal (runs fallback).
	n *Notifier
	// fallback is the terminal conservation action — never nil once delivered (noop by
	// default). It is what a Forward ultimately runs when re-circulation finds no taker.
	fallback func()
	// chained marks a wake announcing capacity that may satisfy MORE consumers than
	// the one it wakes (a weighted release, a multi-permit drain, a capacity raise —
	// see [Notifier.NotifyChained]): a consumer that uses it productively owes the
	// chain exactly one fresh chained probe ([Notification.ProbeOrigin], or the
	// consumer's own probe at the pool-level notifier it knows), so consumers admit
	// one by one until the first miss ends the chain. Wake-one would otherwise
	// under-notify — k satisfiable waiters, one wake, k−1 stranded.
	chained bool
}

// NewNotification returns a terminal (waiter-style) Notification carrying fallback: a
// Forward it cannot be used for runs fallback rather than re-circulating. It is the way a
// producer that is not itself a [Notifier] hands a wake into the block/wait return path
// (e.g. a custom AddWorkFunc). A nil fallback defaults to noop, so the result always
// reports Received. Wakes routed through [Notifier.Notify]/[Waiters.Notify] do not need
// this — they build their own Notification.
func NewNotification(fallback func()) Notification {
	if fallback == nil {
		fallback = noop
	}
	return Notification{fallback: fallback}
}

// Received reports whether a notification was actually delivered to this consumer — true
// for a real wake, false for the zero-value "no wake" sentinel (the consumer woke via ctx
// cancellation, an idle timeout, or an aborted registration). A consumer forwards a wake
// it received but could not use; the sentinel is nothing to forward.
func (m Notification) Received() bool { return m.fallback != nil }

// Forward re-offers a wake the consumer could not use. A listener-style notification
// re-circulates through the Notifier (another listener or waiter may hold or want the
// still-live reserved resource); a waiter-style notification runs the fallback
// terminally, because a woken waiter that cannot use the wake means the resource is
// already gone and re-offering would cascade wasteful wakeups.
func (m Notification) Forward() {
	if m.n != nil {
		m.n.Notify(m.fallback)
	} else {
		m.fallback()
	}
}

// Chained reports whether this wake announces capacity that may satisfy more than
// its one recipient — see [Notifier.NotifyChained]. A consumer that uses a chained
// wake productively owes the chain one fresh probe.
func (m Notification) Chained() bool { return m.chained }

// ProbeOrigin pays a productive consumer's chain debt at the wake's origin Notifier:
// one fresh chained wake, so the next satisfiable consumer admits and the first miss
// ends the chain. A no-op for an unchained wake (no debt) or a waiter-style one (no
// origin recorded — those consumers emit their probe at the pool-level notifier they
// already know). Safe to call unconditionally after productive use.
func (m Notification) ProbeOrigin() {
	if m.chained && m.n != nil {
		m.n.NotifyChained(nil)
	}
}

// NotifyFunc delivers a [Notification] to a subscribed listener. It returns true if it
// took delivery — in which case it owns the Notification and must settle it (Consume or
// Forward), synchronously or asynchronously. It returns false if it declined
// synchronously without taking the wake, signaling the caller to try another listener
// (the most efficient outcome — no re-circulation needed).
type NotifyFunc = func(Notification) bool
