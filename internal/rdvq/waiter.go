// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Waiter is a caller-owned, single-consumer park point: one goroutine prepares
// a wait, composes the returned channel into its own select, and finishes the
// wait whether or not it was woken. A notifier holding a *Waiter (e.g. as a
// demand's attendant) wakes it with [Waiter.Notify], which is generation-guarded:
// a wake aimed at a wait the owner has already finished is a no-op, so spurious
// and stale wakes are safe by construction and every landed wake ends in the
// owner's confirm re-check.
//
// The zero value is not ready for use; call Init (or pool via omnipool, whose
// Init hook allocates the channel).
type Waiter struct {
	ib inbox[struct{}]
}

// Init implements [omnipool.Initer]: allocates the wait channel.
func (w *Waiter) Init() { w.ib.Init() }

// Prepare registers a fresh wait and returns the channel to compose into the
// owner's select. Per the park protocol, the owner must re-check its condition
// after Prepare and before blocking, and must pair every Prepare with a Finish.
func (w *Waiter) Prepare() <-chan struct{} {
	g, s := w.ib.loadState()
	if s != inboxFree || !w.ib.register(g) {
		panic("rdvq: Waiter.Prepare while a wait is already in flight")
	}
	return w.ib.ch
}

// Finish ends the wait begun by Prepare. received reports whether the owner's
// select took the wake from the channel. When it did not, Finish abandons the
// registration — or, if a wake was already inbound, drains it (payloadless, so
// draining loses nothing: the owner is running and will re-check on its own).
func (w *Waiter) Finish(received bool) {
	g, s := w.ib.loadState()
	switch {
	case received:
		if s != inboxDelivering || !w.ib.finishReceive(g) {
			panic("rdvq: Waiter.Finish(received) without a delivery in flight")
		}
	case s == inboxWaiting && w.ib.abandon(g):
		// Won the abandon: no wake was inbound; the generation bump inerts any
		// notifier still holding this wait's registration.
	default:
		// Lost the abandon: a notifier claimed between the owner's select and
		// this Finish, so a wake is inbound. Drain it and close out.
		<-w.ib.ch
		w.ib.finishReceive(g)
	}
}

// Notify wakes the waiter if a wait is in flight; otherwise it is a no-op. Safe
// to call from any goroutine, concurrently: the delivery claim is a CAS, and
// the winner's send has exclusive rights to the cap-1 channel.
func (w *Waiter) Notify() {
	g, s := w.ib.loadState()
	if s != inboxWaiting || !w.ib.claimDeliver(g) {
		return
	}
	w.ib.ch <- struct{}{}
}

// Reset implements [omnipool.Resetter]: a recycled Waiter keeps its channel and
// generation (a whole-struct zeroing would drop both, and the generation is
// what keeps a stale attendant's late Notify inert-or-harmless for the next
// borrower). Finish leaves the state free, so there is nothing to clear.
func (w *Waiter) Reset() {}
