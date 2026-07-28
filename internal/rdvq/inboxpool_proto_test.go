// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Standalone model check of the race-safe inbox RECLAMATION protocol
// (docs/rdvq-inbox-reclamation.md, implemented by inbox.go): a generation-stamped
// 3-state inbox (free / waiting / delivering) in one atomic word, the receiver the
// SOLE reclaimer on the clean AND abandon path (a receiver still OWNS an abandoned
// inbox — the Queue re-passes it across retries — so senders only deliver-or-skip),
// and ONE generation bump on abandon. CRUCIALLY, the hint a receiver publishes carries the generation it
// was minted at, and a sender claims at THAT captured generation — not the inbox's
// current one. That is what makes a SHARED inbox pool safe: an inbox abandoned in one
// queue (generation bumped) and reused in ANOTHER via the shared pool leaves a stale
// hint in the first queue whose captured generation no longer matches, so its claim
// fails. Claiming at the current generation instead delivers into the reused inbox now
// owned by a different queue's receiver — cross-queue misdelivery.
//
// This is a STANDALONE prototype. It runs TWO queues sharing ONE inbox pool so the
// cross-queue hazard is actually exercised, and tags each value with its queue: "every
// value received exactly once, by a receiver of its OWN queue" under -race catches lost
// / double / cross-queue delivery and use-after-reclaim. (Switching trySend to claim at
// the current generation instead of the captured one makes this test fail — that is the
// regression it guards.) Mirrors outboxpool_reclaim_proto_test.go for the other direction.

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

func (ib *protoInbox) register(g uint64) bool {
	return ib.state.CompareAndSwap(protoPack(g, protoFree), protoPack(g, protoWaiting))
}
func (ib *protoInbox) claimDeliver(g uint64) bool {
	return ib.state.CompareAndSwap(protoPack(g, protoWaiting), protoPack(g, protoDelivering))
}
func (ib *protoInbox) finishReceive(g uint64) bool {
	return ib.state.CompareAndSwap(protoPack(g, protoDelivering), protoPack(g, protoFree))
}

// abandon: waiting@g → free@(g+1) (the SOLE gen bump).
func (ib *protoInbox) abandon(g uint64) bool {
	return ib.state.CompareAndSwap(protoPack(g, protoWaiting), protoPack(g+1, protoFree))
}

// protoHint is the generation-stamped reference a receiver publishes (the captured-gen
// hint that makes the shared pool cross-queue-safe).
type protoHint struct {
	ib  *protoInbox
	gen uint64
}

// protoQueue is one inboxOnlyQueue-like rendezvous: its own hint collection, but a
// SHARED inbox free pool (the cross-queue hazard surface).
type protoQueue struct {
	hints nbcq.Queue[protoHint]
	pool  *sync.Pool // SHARED across queues

	allocated   *atomic.Int64
	discarded   *atomic.Int64
	circulating *atomic.Int64
}

func (q *protoQueue) obtain() *protoInbox {
	q.circulating.Add(1)
	if v := q.pool.Get(); v != nil {
		return v.(*protoInbox)
	}
	q.allocated.Add(1)
	return &protoInbox{ch: make(chan int, 1)}
}

func (q *protoQueue) reclaim(ib *protoInbox) {
	q.discarded.Add(1)
	q.circulating.Add(-1)
	q.pool.Put(ib)
}

// trySend delivers to the first genuinely-waiting inbox, claiming at the hint's CAPTURED
// generation. Senders never reclaim.
func (q *protoQueue) trySend(v int) bool {
	for {
		h, ok := q.hints.TryPopFront()
		if !ok {
			return false
		}
		if h.ib.claimDeliver(h.gen) {
			h.ib.ch <- v
			return true
		}
		// stale (abandoned/reused → gen advanced) or in-flight: skip
	}
}

// TestInboxReclaim_Race stresses two queues sharing one inbox pool, each with concurrent
// senders and churning receivers (commit-block / churn-abandon / re-pass leaving stale
// duplicate hints). Each value is tagged with its queue; a receiver must only ever
// receive a value of its own queue. "Exactly once, own queue" under -race catches
// lost/double/cross-queue delivery and use-after-reclaim.
func TestInboxReclaim_Race(t *testing.T) {
	chk := require.New(t)
	const (
		nQueues    = 2
		nSenders   = 6
		nReceivers = 6
		perSender  = 20000
		perQueue   = nSenders * perSender
		total      = nQueues * perQueue
	)
	var pool sync.Pool
	var allocated, discarded, circulating atomic.Int64

	received := make([]atomic.Int32, total)
	var count atomic.Int64
	done := make(chan struct{})

	queues := make([]*protoQueue, nQueues)
	for k := range queues {
		q := &protoQueue{pool: &pool, allocated: &allocated, discarded: &discarded, circulating: &circulating}
		q.hints.Init()
		queues[k] = q
	}

	var all sync.WaitGroup

	for k := 0; k < nQueues; k++ {
		q := queues[k]
		base := k * perQueue // this queue owns value range [base, base+perQueue)

		// Receivers.
		for r := 0; r < nReceivers; r++ {
			all.Add(1)
			go func(seed int) {
				defer all.Done()
				var held *protoInbox
				iter := seed
				for count.Load() < total {
					iter++
					ib := held
					held = nil
					if ib == nil {
						ib = q.obtain()
					}
					g, _ := ib.load()
					if !ib.register(g) {
						q.reclaim(ib)
						continue
					}
					q.hints.PushBack(protoHint{ib: ib, gen: g})

					record := func(v int) {
						if v < base || v >= base+perQueue {
							t.Errorf("queue %d received value %d outside its range [%d,%d) — cross-queue misdelivery",
								k, v, base, base+perQueue)
							return
						}
						if received[v].Add(1) != 1 {
							t.Errorf("value %d received more than once", v)
						}
						ib.finishReceive(g)
						if count.Add(1) == int64(total) {
							close(done)
						}
					}

					commit := iter%4 != 0
					if commit {
						select {
						case v := <-ib.ch:
							record(v)
						case <-done:
							if !ib.abandon(g) {
								record(<-ib.ch)
							}
						}
					} else {
						select {
						case v := <-ib.ch:
							record(v)
						default:
							if !ib.abandon(g) {
								record(<-ib.ch)
							}
						}
					}

					if iter%3 == 0 {
						held = ib
					} else {
						q.reclaim(ib)
					}
				}
				if held != nil {
					q.reclaim(held)
				}
			}(r)
		}

		// Senders.
		for s := 0; s < nSenders; s++ {
			all.Add(1)
			go func(sbase int) {
				defer all.Done()
				for j := 0; j < perSender; j++ {
					for !q.trySend(sbase + j) {
						runtime.Gosched()
					}
				}
			}(base + s*perSender)
		}
	}

	all.Wait()

	chk.Equal(int64(total), count.Load(), "every value received exactly once")
	for i := range received {
		chk.Equalf(int32(1), received[i].Load(), "value %d once", i)
	}
	chk.Positive(discarded.Load(), "reclamation must actually have happened")
	t.Logf("allocated=%d discarded=%d circulating(final)=%d (%d values, %d queues sharing one pool)",
		allocated.Load(), discarded.Load(), circulating.Load(), total, nQueues)
}

// TestInboxCapturedGenStaleHintInert is the DETERMINISTIC cross-queue regression guard.
// The stress test above cannot reliably reproduce cross-queue reuse — sync.Pool's
// P-affinity usually returns a reclaimed inbox to the same goroutine that freed it — so
// this orchestrates the exact hazard by hand: an inbox abandoned in queue A and reused in
// queue B, with A's stale hint still pending. Captured-gen claiming makes A's stale hint
// inert; current-gen claiming would deliver A's value into B's inbox (cross-queue
// misdelivery). Flipping trySend to claim at the current generation fails this test.
func TestInboxCapturedGenStaleHintInert(t *testing.T) {
	chk := require.New(t)
	var pool sync.Pool
	var a64 atomic.Int64
	mk := func() *protoQueue {
		q := &protoQueue{pool: &pool, allocated: &a64, discarded: &a64, circulating: &a64}
		q.hints.Init()
		return q
	}
	qA, qB := mk(), mk()

	x := &protoInbox{ch: make(chan int, 1)}

	// Queue A registers X and publishes a hint at generation 0.
	gA, _ := x.load()
	chk.True(x.register(gA), "A registers X (free@0 → waiting@0)")
	qA.hints.PushBack(protoHint{ib: x, gen: gA})

	// A's receiver abandons X (waiting@0 → free@1, the sole gen bump); A's hint lingers.
	chk.True(x.abandon(gA), "A abandons X (waiting@0 → free@1)")

	// X is reclaimed to the shared pool and reused by queue B, which registers it at the
	// new generation (1) and publishes its own hint.
	gB, _ := x.load()
	chk.Equal(uint64(1), gB, "abandon bumped the generation")
	chk.True(x.register(gB), "B reuses X (free@1 → waiting@1)")
	qB.hints.PushBack(protoHint{ib: x, gen: gB})

	// A's sender drives its queue: it pops A's stale hint (captured gen 0). Claiming at
	// the captured generation FAILS (X is now waiting@1), so A delivers nothing into X —
	// no cross-queue misdelivery. (Current-gen claiming would succeed here and send 99.)
	chk.False(qA.trySend(99), "A's stale hint must NOT deliver into X (reused by B at gen 1)")

	// B's sender delivers correctly via its current-gen hint.
	chk.True(qB.trySend(42), "B's valid hint delivers")
	chk.Equal(42, <-x.ch, "X holds B's value (42), never A's (99)")
}
