// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Prototype for race-safe outbox RECLAMATION on the destination-owned pool. The
// clean pool (outboxes + fullOutboxes) never shrinks: a concurrency spike pins
// peak-concurrency outboxes on `outboxes` forever, and for a long-lived
// destination that is unbounded by cores (a goroutine parked on a full outbox
// costs no core). Reclamation returns drained-and-unneeded outboxes to a
// sync.Pool so the live set tracks current concurrency.
//
// The hazard is a use-after-reclaim: the receiver must never mark an outbox
// reclaimable if a producer has refilled it (it would then be discarded while a
// value sits on fullOutboxes). The fix folds a generation counter and the
// reclaimable bit into ONE atomic word: fill bumps gen + clears reclaimable; the
// receiver marks reclaimable with a CAS that only sticks if gen is unchanged
// since it drained. So a refill (gen bump) always wins, and a full outbox can
// never be left reclaimable.

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/petenewcomb/streampool/internal/nbcq"
	"github.com/stretchr/testify/require"
)

type rclOutbox struct {
	ch    chan int
	state atomic.Uint64 // (gen << 1) | reclaimableBit
}

func (o *rclOutbox) reclaimable() bool { return o.state.Load()&1 == 1 }
func (o *rclOutbox) genNow() uint64    { return o.state.Load() >> 1 }

// bumpGen marks a fresh fill: gen+1, reclaimable cleared.
func (o *rclOutbox) bumpGen() {
	for {
		old := o.state.Load()
		next := ((old >> 1) + 1) << 1 // next generation, reclaimable cleared
		if o.state.CompareAndSwap(old, next) {
			return
		}
	}
}

// markReclaimable sets the reclaimable bit iff the gen is still g (no refill
// since the caller drained at gen g). Returns whether it marked.
func (o *rclOutbox) markReclaimable(g uint64) bool {
	return o.state.CompareAndSwap(g<<1, (g<<1)|1)
}

type rclPool struct {
	outboxes nbcq.Queue[*rclOutbox]
	full     nbcq.Queue[*rclOutbox]
	free     sync.Pool

	allocated   atomic.Int64 // outboxes ever newly made
	discarded   atomic.Int64 // reclaim() calls
	circulating atomic.Int64 // currently out of sync.Pool (the live set)
}

func newRclPool() *rclPool {
	p := &rclPool{}
	p.outboxes.Init()
	p.full.Init()
	return p
}

func (p *rclPool) obtain() *rclOutbox {
	p.circulating.Add(1)
	if v := p.free.Get(); v != nil {
		return v.(*rclOutbox)
	}
	p.allocated.Add(1)
	return &rclOutbox{ch: make(chan int, 1)}
}

func (p *rclPool) reclaim(o *rclOutbox) {
	p.discarded.Add(1)
	p.circulating.Add(-1)
	p.free.Put(o)
}

func (p *rclPool) fill(ob *rclOutbox, v int) {
	ob.ch <- v // blocks if the borrowed outbox is still full → pacing
	ob.bumpGen()
	p.full.PushBack(ob)
	p.outboxes.PushBack(ob)
}

func (p *rclPool) push(v int) {
	var fallback *rclOutbox // a reclaimable one kept to reuse instead of pooling
	for {
		ob, ok := p.outboxes.TryPopFront()
		if !ok {
			if fallback != nil {
				p.fill(fallback, v)
			} else {
				p.fill(p.obtain(), v)
			}
			return
		}
		if ob.reclaimable() {
			// Empty slack. Keep one as a reuse fallback to skip a pool round-trip
			// when the queue exhausts; reclaim the rest to sync.Pool (whose own
			// GC-clearing is the scale-to-zero — cheap reuse hot, release cold).
			if fallback == nil {
				fallback = ob
			} else {
				p.reclaim(ob)
			}
			continue
		}
		// Not reclaimable ⇒ full ⇒ prefer it (block-fill = pace).
		if fallback != nil {
			p.reclaim(fallback)
		}
		p.fill(ob, v)
		return
	}
}

func (p *rclPool) drain() (int, bool) {
	for {
		ob, ok := p.full.TryPopFront()
		if !ok {
			return 0, false
		}
		g := ob.genNow()
		select {
		case v := <-ob.ch:
			ob.markReclaimable(g) // sticks only if no refill since gen g
			return v, true
		default:
		}
	}
}

// TestReclaimPool_Race stresses concurrent push/drain with reclamation active;
// "every value received exactly once" under -race catches any use-after-reclaim.
func TestReclaimPool_Race(t *testing.T) {
	chk := require.New(t)
	const (
		nPushers  = 8
		nDrainers = 8
		perPusher = 20000
		total     = nPushers * perPusher
	)
	p := newRclPool()
	received := make([]atomic.Int32, total)
	var count atomic.Int64

	var drainers sync.WaitGroup
	for i := 0; i < nDrainers; i++ {
		drainers.Add(1)
		go func() {
			defer drainers.Done()
			for count.Load() < total {
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
	pushers.Wait()
	drainers.Wait()

	chk.Equal(int64(total), count.Load(), "every value received exactly once")
	for i := range received {
		chk.Equalf(int32(1), received[i].Load(), "value %d once", i)
	}
	chk.Positive(p.discarded.Load(), "reclamation must actually have happened")
	t.Logf("allocated=%d discarded=%d circulating(final)=%d (%d values, %d pushers)",
		p.allocated.Load(), p.discarded.Load(), p.circulating.Load(), total, nPushers)
}
