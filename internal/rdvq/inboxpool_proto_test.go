// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Prototype for race-safe inbox RECLAMATION on the receiver-owned inbox pool.
// Today an ABANDONED inbox (a receiver registered it but took its value from
// elsewhere) is left on the hint collection with a zero-value channel marker and
// dropped to GC — so the inbox pool never recycles it and a borrow allocates per
// abandoning wait. The receiver still OWNS an abandoned inbox (Queue re-passes it
// across retries), so a sender cannot reclaim it (the reverted naive fix hung
// saturation_test).
//
// The fix (see docs/rdvq-inbox-reclamation.md): a generation-stamped 3-state inbox
// (free / waiting / delivering) in one atomic word. The receiver is the SOLE
// reclaimer — on the clean AND the abandon path; senders only deliver-or-skip. The
// ONE generation bump is on abandon, the single transition that disowns a waiting
// registration a sender may already have observed: a sender that loaded the inbox
// as waiting@g then does claimDeliver(g), which fails the instant abandon advanced
// it to free@(g+1), so the sender can never deliver into a period the receiver
// disowned. Every other transition leaves the generation alone.
//
// This file is a STANDALONE prototype of that protocol (its own simplified types),
// stress-tested so "every value received exactly once" under -race catches any
// lost/double delivery or use-after-reclaim — exactly as the outbox reclaim
// prototype (outboxpool_reclaim_proto_test.go) does for the other direction.

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/petenewcomb/streampool/internal/nbcq"
	"github.com/stretchr/testify/require"
)

// protoInbox state word: (gen << protoGenShift) | state.
type protoInboxStateT = uint64

const (
	protoFree       protoInboxStateT = iota // pooled / not registered / disowned-by-abandon
	protoWaiting                            // registered as a hint; receiver may take a value on ch
	protoDelivering                         // a sender won the claim and is sending into ch (transient)
	protoStateCount
)

const (
	protoGenShift = 2 // 2 low bits hold the 3 states
	protoStateMsk = (1 << protoGenShift) - 1
)

func protoPack(gen uint64, st protoInboxStateT) uint64 { return gen<<protoGenShift | st }

type protoInbox struct {
	ch    chan int
	state atomic.Uint64
}

func (ib *protoInbox) load() (gen uint64, st protoInboxStateT) {
	w := ib.state.Load()
	return w >> protoGenShift, w & protoStateMsk
}

// register: free@g → waiting@g (receiver, no gen bump).
func (ib *protoInbox) register(g uint64) bool {
	return ib.state.CompareAndSwap(protoPack(g, protoFree), protoPack(g, protoWaiting))
}

// claimDeliver: waiting@g → delivering@g (sender, no gen bump). Wins exclusive
// rights to send into ch; loses if the receiver abandoned (gen advanced) or
// another sender claimed.
func (ib *protoInbox) claimDeliver(g uint64) bool {
	return ib.state.CompareAndSwap(protoPack(g, protoWaiting), protoPack(g, protoDelivering))
}

// finishReceive: delivering@g → free@g (receiver, no gen bump). The value has been
// drained; the inbox is now reclaimable or re-registerable.
func (ib *protoInbox) finishReceive(g uint64) bool {
	return ib.state.CompareAndSwap(protoPack(g, protoDelivering), protoPack(g, protoFree))
}

// abandon: waiting@g → free@(g+1) (receiver, the SOLE gen bump). Wins iff no sender
// has claimed; the bump inerts any sender that observed waiting@g.
func (ib *protoInbox) abandon(g uint64) bool {
	return ib.state.CompareAndSwap(protoPack(g, protoWaiting), protoPack(g+1, protoFree))
}

type protoInboxPool struct {
	hints nbcq.Queue[*protoInbox] // stale-tolerant hint collection (receiver-registered)
	free  sync.Pool

	allocated   atomic.Int64 // inboxes ever newly made
	discarded   atomic.Int64 // reclaim() calls
	circulating atomic.Int64 // currently out of sync.Pool (the live set)
}

func newProtoInboxPool() *protoInboxPool {
	p := &protoInboxPool{}
	p.hints.Init()
	return p
}

func (p *protoInboxPool) obtain() *protoInbox {
	p.circulating.Add(1)
	if v := p.free.Get(); v != nil {
		return v.(*protoInbox)
	}
	p.allocated.Add(1)
	return &protoInbox{ch: make(chan int, 1)}
}

func (p *protoInboxPool) reclaim(ib *protoInbox) {
	p.discarded.Add(1)
	p.circulating.Add(-1)
	p.free.Put(ib)
}

// trySend is the sender side (the TryPushBack analogue): pop hints and deliver to
// the first genuinely-waiting inbox, skipping stale/in-flight ones. Senders NEVER
// reclaim. Returns false when no waiting receiver was found.
func (p *protoInboxPool) trySend(v int) bool {
	for {
		ib, ok := p.hints.TryPopFront()
		if !ok {
			return false
		}
		g, st := ib.load()
		if st == protoWaiting && ib.claimDeliver(g) {
			ib.ch <- v // cap-1, uncontended: the claim made us the exclusive sender
			return true
		}
		// free / delivering / claim lost (abandoned, gen-advanced, or another sender):
		// a stale or in-flight hint — skip and try the next.
	}
}

// TestInboxReclaim_Race stresses concurrent senders and churning receivers
// (registering, mostly abandoning to exercise the hazard, some re-passing like
// Queue) with reclamation active. "Every value received exactly once" under -race
// catches any lost/double delivery, use-after-reclaim, or stale-view delivery.
func TestInboxReclaim_Race(t *testing.T) {
	chk := require.New(t)
	const (
		nSenders   = 8
		nReceivers = 8
		perSender  = 20000
		total      = nSenders * perSender
	)
	p := newProtoInboxPool()
	received := make([]atomic.Int32, total)
	var count atomic.Int64
	done := make(chan struct{}) // closed once when the last value is recorded

	recordAndFinish := func(ib *protoInbox, v int, claimGen uint64) {
		if received[v].Add(1) != 1 {
			t.Errorf("value %d received more than once", v)
		}
		ib.finishReceive(claimGen) // delivering@claimGen → free@claimGen
		if count.Add(1) == int64(total) {
			close(done) // wake committed receivers blocked on <-ib.ch
		}
	}

	// Receivers: register an inbox, then take a delivered value or abandon. Most
	// iterations COMMIT — block on the inbox channel (or shutdown) so senders have
	// a real rendezvous window. A fraction CHURN — non-blocking probe then abandon
	// — to stress the hazard (abandon racing a sender's claim, reclaim/reuse). The
	// abandon's CAS failure means a sender claimed mid-register, so the receiver
	// drains the orphan. Some receivers re-pass (hold the inbox across iterations,
	// leaving a stale duplicate hint) — the stale-view stress.
	var receivers sync.WaitGroup
	for r := 0; r < nReceivers; r++ {
		receivers.Add(1)
		go func(seed int) {
			defer receivers.Done()
			var held *protoInbox // re-passed inbox (free at its current gen)
			iter := seed
			for count.Load() < total {
				iter++
				ib := held
				held = nil
				if ib == nil {
					ib = p.obtain()
				}
				g, _ := ib.load() // free@g
				if !ib.register(g) {
					p.reclaim(ib) // we own ib free@g, so this cannot fail; guard anyway
					continue
				}
				p.hints.PushBack(ib)

				// abandonOrDrain: give up the registration. abandon wins iff no sender
				// claimed (clean); a lost CAS means a value is inbound — drain the orphan.
				abandonOrDrain := func() {
					if ib.abandon(g) {
						return
					}
					v := <-ib.ch
					recordAndFinish(ib, v, g)
				}

				if iter%4 != 0 {
					// COMMIT: block until a sender delivers, or shutdown.
					select {
					case v := <-ib.ch:
						recordAndFinish(ib, v, g)
					case <-done:
						abandonOrDrain()
					}
				} else {
					// CHURN: probe non-blocking, else abandon (races senders).
					select {
					case v := <-ib.ch:
						recordAndFinish(ib, v, g)
					default:
						abandonOrDrain()
					}
				}

				if iter%3 == 0 {
					held = ib // re-pass: reuse next iteration (free@g or g+1)
				} else {
					p.reclaim(ib)
				}
			}
			if held != nil {
				p.reclaim(held)
			}
		}(r)
	}

	// Senders: deliver each value exactly once, retrying until a waiting receiver
	// takes it (mirrors saturation_test's refuse→re-drive producers).
	var senders sync.WaitGroup
	for s := 0; s < nSenders; s++ {
		senders.Add(1)
		go func(base int) {
			defer senders.Done()
			for j := 0; j < perSender; j++ {
				for !p.trySend(base*perSender + j) {
					runtime.Gosched()
				}
			}
		}(s)
	}
	senders.Wait()
	receivers.Wait()

	chk.Equal(int64(total), count.Load(), "every value received exactly once")
	for i := range received {
		chk.Equalf(int32(1), received[i].Load(), "value %d once", i)
	}
	chk.Positive(p.discarded.Load(), "reclamation must actually have happened")
	t.Logf("allocated=%d discarded=%d circulating(final)=%d (%d values, %d senders, %d receivers)",
		p.allocated.Load(), p.discarded.Load(), p.circulating.Load(), total, nSenders, nReceivers)
}
