// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Prototype for the destination-owned outbox pool (the rdvq Sender redesign):
// outboxes are owned by the Queue, not cached in a per-goroutine Sender, and
// pacing + the "≤ #goroutines outboxes per destination" bound come from an
// empty / maybe-full / full triple of queues instead of a per-outbox refcount.
//
// Invariant under test: every outbox is a member of exactly one of empty /
// maybeFull when not checked out by a borrow; full is an independent membership
// for "holds a value, awaiting a receiver". A borrow prefers empty, falls back
// to maybeFull, and only allocates when both are empty — which can only happen
// when every existing outbox is checked out, capping the pool at the number of
// concurrent borrowers (≤ #goroutines).
//
// This isolates the novel sender-side mechanism; the inbox/direct-handoff path
// (which feeds the empty queue) is unchanged and orthogonal, so the prototype
// exercises the maybeFull/full cycle that does the pacing + bounding.

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/petenewcomb/psg-go/internal/nbcq"
	"github.com/stretchr/testify/require"
)

type protoOutbox struct {
	ch chan int
}

type protoPool struct {
	empty     nbcq.Queue[*protoOutbox]
	maybeFull nbcq.Queue[*protoOutbox]
	full      nbcq.Queue[*protoOutbox]
	allocated atomic.Int64 // outboxes ever created (the pool's high-water mark)
}

func newProtoPool() *protoPool {
	p := &protoPool{}
	p.empty.Init()
	p.maybeFull.Init()
	p.full.Init()
	return p
}

// borrow returns an outbox to fill: prefer a known-empty one, fall back to a
// maybe-full one (which may still be full → the fill will block = pacing), and
// only allocate when both are exhausted.
func (p *protoPool) borrow() *protoOutbox {
	if ob, ok := p.empty.TryPopFront(); ok {
		return ob
	}
	if ob, ok := p.maybeFull.TryPopFront(); ok {
		return ob
	}
	p.allocated.Add(1)
	return &protoOutbox{ch: make(chan int, 1)}
}

// push borrows an outbox and fills it. If the borrowed outbox is still full
// (a prior value not yet drained), the cap-1 channel send blocks until a
// receiver drains it — that is the backpressure. After filling, the outbox is
// published to full (for a receiver) and maybeFull (for reuse).
func (p *protoPool) push(v int) {
	ob := p.borrow()
	ob.ch <- v // blocks iff the borrowed outbox is still full → paced
	p.full.PushBack(ob)
	p.maybeFull.PushBack(ob)
}

// drain pops a full outbox and receives its value. It deliberately does NOT
// re-file the outbox: it remains on maybeFull (now drained), where a future
// borrow finds it empty and reuses it without blocking. This is what keeps the
// receiver off the borrow-side bookkeeping entirely.
func (p *protoPool) drain() (int, bool) {
	for {
		ob, ok := p.full.TryPopFront()
		if !ok {
			return 0, false
		}
		select {
		case v := <-ob.ch:
			return v, true
		default:
			// Defensive: a full entry whose channel is already empty. With
			// single-owner full-pop and one full entry per fill this should
			// not occur; skip to the next.
		}
	}
}

// TestProtoOutboxPool_Race stress-tests the pool under many concurrent
// borrowers and a few drainers (so pacing is actually exercised), asserting:
// every value is received exactly once, and the pool never exceeds the number
// of concurrent pushers (the bound). Run with -race.
func TestProtoOutboxPool_Race(t *testing.T) {
	chk := require.New(t)

	const (
		nPushers   = 8
		nDrainers  = 3 // fewer than pushers, so borrowers routinely hit the full path
		perPusher  = 20000
		totalCount = nPushers * perPusher
	)

	p := newProtoPool()
	received := make([]atomic.Int32, totalCount)
	var count atomic.Int64

	var pushers sync.WaitGroup
	for i := 0; i < nPushers; i++ {
		pushers.Add(1)
		go func(base int) {
			defer pushers.Done()
			for j := 0; j < perPusher; j++ {
				p.push(base*perPusher + j)
			}
		}(i)
	}

	var drainers sync.WaitGroup
	for i := 0; i < nDrainers; i++ {
		drainers.Add(1)
		go func() {
			defer drainers.Done()
			for count.Load() < totalCount {
				v, ok := p.drain()
				if !ok {
					runtime.Gosched()
					continue
				}
				if received[v].Add(1) != 1 {
					t.Errorf("value %d received more than once", v)
					return
				}
				count.Add(1)
			}
		}()
	}

	pushers.Wait()
	drainers.Wait()

	chk.Equal(int64(totalCount), count.Load(), "every pushed value must be received")
	for i := range received {
		chk.Equal(int32(1), received[i].Load(), "value %d received exactly once", i)
	}
	chk.LessOrEqualf(p.allocated.Load(), int64(nPushers),
		"pool must not exceed #concurrent pushers; allocated=%d", p.allocated.Load())
	t.Logf("allocated=%d outboxes for %d pushers / %d values", p.allocated.Load(), nPushers, totalCount)
}
