// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"math/bits"
	"sync/atomic"
)

// inboxState is the lifecycle state of an inbox, held in the low bits of the atomic
// state word alongside a monotonic generation. The generation is bumped on EXACTLY
// one transition — abandon — the single point at which a receiver disowns a waiting
// registration a sender may already have observed: a sender that loaded the inbox as
// waiting@g and then claimDelivers at g fails the instant abandon advanced it to
// free@(g+1). See docs/rdvq-inbox-reclamation.md.
type inboxState uint64

const (
	inboxFree       inboxState = iota // pooled / not registered / disowned-by-abandon
	inboxWaiting                      // registered as a hint; receiver may take a value on ch
	inboxDelivering                   // a sender claimed and is sending the value into ch (transient)
	inboxStateCount                   // trailing iota — number of states
)

// genShift reserves the low bits of the state word for inboxState; the generation
// occupies the rest. Derived from the state count, so adding a state widens it
// automatically (mirrors outbox.go). (var, not const: bits.Len is not constant.)
var (
	inboxGenShift  = uint(bits.Len(uint(inboxStateCount - 1)))
	inboxStateMask = uint64(1)<<inboxGenShift - 1
)

func inboxPack(gen uint64, s inboxState) uint64 { return gen<<inboxGenShift | uint64(s) }

// inbox provides per-receive-operation buffering for direct handoff from senders.
// Each inbox is dedicated to a specific receive operation receiving items from a
// specific Queue/Handoff/Waiters.
type inbox[T any] struct {
	ch         chan T
	state      atomic.Uint64 // (gen << inboxGenShift) | inboxState
	wasEmptied bool          // set by the caller's selectFn when it received a value on ch
}

// Init implements [omnipool.Initer]: it allocates the cap-1 buffered channel for a
// freshly created inbox (generation 0, state free). (Inboxes constructed directly,
// e.g. in tests, leave ch nil and allocate lazily in inboxOnlyQueue.PopFrontFunc.)
func (ib *inbox[T]) Init() { ib.ch = make(chan T, 1) }

// Reset implements [omnipool.Resetter]: on return to the pool it clears the per-op
// receive flag and leaves the inbox free at its CURRENT generation (NOT bumped — only
// abandon bumps), keeping the drained channel. Implementing Reset also prevents
// omnipool's default whole-struct zeroing, which would nil the channel and the gen.
func (ib *inbox[T]) Reset() {
	ib.wasEmptied = false
	g := ib.state.Load() >> inboxGenShift
	ib.state.Store(inboxPack(g, inboxFree))
}

// loadState reads the current generation and state.
func (ib *inbox[T]) loadState() (gen uint64, s inboxState) {
	w := ib.state.Load()
	return w >> inboxGenShift, inboxState(w & inboxStateMask)
}

// register transitions free@g → waiting@g (receiver). Uncontended: only the owning
// receiver ever touches a free inbox — senders skip any hint not in inboxWaiting.
func (ib *inbox[T]) register(g uint64) bool {
	return ib.state.CompareAndSwap(inboxPack(g, inboxFree), inboxPack(g, inboxWaiting))
}

// claimDeliver transitions waiting@g → delivering@g (sender), winning exclusive rights
// to send the value into ch. Fails if the receiver abandoned (generation advanced) or
// another sender already claimed.
func (ib *inbox[T]) claimDeliver(g uint64) bool {
	return ib.state.CompareAndSwap(inboxPack(g, inboxWaiting), inboxPack(g, inboxDelivering))
}

// finishReceive transitions delivering@g → free@g (receiver) once the delivered value
// has been drained from ch. No generation bump.
func (ib *inbox[T]) finishReceive(g uint64) bool {
	return ib.state.CompareAndSwap(inboxPack(g, inboxDelivering), inboxPack(g, inboxFree))
}

// abandon transitions waiting@g → free@(g+1) (receiver) — the SOLE generation bump.
// Wins iff no sender has claimed; the bump inerts any sender that observed waiting@g
// (its claimDeliver at g now fails), so the disowned registration's lingering hint can
// never produce a delivery.
func (ib *inbox[T]) abandon(g uint64) bool {
	return ib.state.CompareAndSwap(inboxPack(g, inboxWaiting), inboxPack(g+1, inboxFree))
}

// channel returns the inbox's channel for use in select statements. Returns nil if the
// inbox itself is nil. Panics if called when no channel has been allocated, which
// should only happen if it is called outside of a selectFn callback.
func (ib *inbox[T]) channel() <-chan T {
	if ib == nil {
		return nil
	}
	ch := ib.ch
	if ch == nil {
		panic("inbox channel is nil")
	}
	return ch
}

func (ib *inbox[T]) emptyPending() { ib.wasEmptied = false }

// emptied marks the inbox as having been successfully emptied of a value. Must be
// called by the caller's selectFn after it receives from the inbox channel.
func (ib *inbox[T]) emptied() { ib.wasEmptied = true }
