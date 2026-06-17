// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Hardened clean-vs-cheat comparison for the destination-owned outbox pool (see
// outboxpool_proto_test.go). "clean" keeps the single-borrow-queue invariant (a
// borrow is a single-owner nbcq pop — no claim, no flags). "cheat" pushes drained
// outboxes onto emptyOutboxes for a prioritized fast path, paid for with a
// per-outbox claim CAS + onEmpty/onMaybeFull membership flags.
//
// What we measure and why: the cheat's only lever is avoiding a borrow blocking
// on a still-full outbox when an empty one exists, so its payoff (if any) is in
// the TAIL of producer emit latency, not throughput. So we measure the latency of
// each push() call — p50/p99/p99.9/max — across several runs (so the tail is a
// distribution, not one noisy sample), under a HEAVY-TAILED consumer: most drains
// process fast, a few are slow, which transiently drops drain capacity, backs up
// outboxes, and makes producers block — exactly the regime the cheat targets.
//
// Still synthetic (dedicated pushers/drainers, no block-and-help), and dedicated
// roles UNDERSTATE the cheat (fungible workers would feed blocked-not-draining
// backlog into itself), so a cheat win here is a conservative lower bound.

import (
	"math"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/nbcq"
	"github.com/stretchr/testify/require"
)

type cmpOutbox struct {
	ch          chan int
	claimed     atomic.Bool
	onEmpty     atomic.Bool
	onMaybeFull atomic.Bool
}

type outboxPool interface {
	push(v int)
	drain() (int, bool)
	allocCount() int64
	blockCount() int64
}

// ── clean ────────────────────────────────────────────────────────────────────

type cleanPool struct {
	maybeFull nbcq.Queue[*cmpOutbox]
	full      nbcq.Queue[*cmpOutbox]
	allocated atomic.Int64
	blocks    atomic.Int64
}

func newCleanPool() *cleanPool {
	p := &cleanPool{}
	p.maybeFull.Init()
	p.full.Init()
	return p
}

func (p *cleanPool) push(v int) {
	ob, ok := p.maybeFull.TryPopFront()
	if !ok {
		p.allocated.Add(1)
		ob = &cmpOutbox{ch: make(chan int, 1)}
	}
	select {
	case ob.ch <- v:
	default:
		p.blocks.Add(1)
		ob.ch <- v
	}
	p.full.PushBack(ob)
	p.maybeFull.PushBack(ob)
}

func (p *cleanPool) drain() (int, bool) {
	for {
		ob, ok := p.full.TryPopFront()
		if !ok {
			return 0, false
		}
		select {
		case v := <-ob.ch:
			return v, true
		default:
		}
	}
}

func (p *cleanPool) allocCount() int64 { return p.allocated.Load() }
func (p *cleanPool) blockCount() int64 { return p.blocks.Load() }

// ── cheat ────────────────────────────────────────────────────────────────────

type cheatPool struct {
	empty     nbcq.Queue[*cmpOutbox]
	maybeFull nbcq.Queue[*cmpOutbox]
	full      nbcq.Queue[*cmpOutbox]
	allocated atomic.Int64
	blocks    atomic.Int64
}

func newCheatPool() *cheatPool {
	p := &cheatPool{}
	p.empty.Init()
	p.maybeFull.Init()
	p.full.Init()
	return p
}

func (p *cheatPool) borrow() *cmpOutbox {
	for {
		ob, ok := p.empty.TryPopFront()
		if !ok {
			break
		}
		ob.onEmpty.Store(false)
		if ob.claimed.CompareAndSwap(false, true) {
			return ob
		}
	}
	for {
		ob, ok := p.maybeFull.TryPopFront()
		if !ok {
			break
		}
		ob.onMaybeFull.Store(false)
		if ob.claimed.CompareAndSwap(false, true) {
			return ob
		}
	}
	p.allocated.Add(1)
	ob := &cmpOutbox{ch: make(chan int, 1)}
	ob.claimed.Store(true)
	return ob
}

func (p *cheatPool) push(v int) {
	ob := p.borrow()
	select {
	case ob.ch <- v:
	default:
		p.blocks.Add(1)
		ob.ch <- v
	}
	p.full.PushBack(ob)
	if ob.onMaybeFull.CompareAndSwap(false, true) {
		p.maybeFull.PushBack(ob)
	}
	ob.claimed.Store(false)
}

func (p *cheatPool) drain() (int, bool) {
	for {
		ob, ok := p.full.TryPopFront()
		if !ok {
			return 0, false
		}
		select {
		case v := <-ob.ch:
			if ob.onEmpty.CompareAndSwap(false, true) {
				p.empty.PushBack(ob)
			}
			return v, true
		default:
		}
	}
}

func (p *cheatPool) allocCount() int64 { return p.allocated.Load() }
func (p *cheatPool) blockCount() int64 { return p.blocks.Load() }

// ── harness ──────────────────────────────────────────────────────────────────

func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

// paretoWork is a deterministic heavy-tailed handler time per value: Pareto with
// ~1ms minimum and a fat tail (shape 1.3) capped at 1s. Roughly: median ~1.7ms,
// p99 ~37ms, p99.9 ~200ms, max 1s — i.e. ms of work with rare hundred-ms I/O
// stalls and continuous variation in between.
func paretoWork(v int) time.Duration {
	const minMs, shape, capMs = 1.0, 1.3, 1000.0
	u := float64(splitmix64(uint64(v))>>11) / float64(uint64(1)<<53) //nolint:gosec // v is a non-negative index → [0,1)
	ms := minMs / math.Pow(1-u, 1.0/shape)
	if ms > capMs {
		ms = capMs
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// runLatency returns the latency (ns) of every push() call. drainWork is run by a
// drainer after it receives each value (a stand-in for handler time); making it
// heavy-tailed drops drain capacity in bursts and backs up the pool.
func runLatency(
	t *testing.T, p outboxPool, nPushers, nDrainers, perPusher int,
	pushWork, drainWork func(v int),
) []int64 {
	t.Helper()
	total := nPushers * perPusher
	latNs := make([]int64, total)
	received := make([]atomic.Int32, total)
	var count atomic.Int64

	var drainers sync.WaitGroup
	for i := 0; i < nDrainers; i++ {
		drainers.Add(1)
		go func() {
			defer drainers.Done()
			for count.Load() < int64(total) {
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
				drainWork(v)
			}
		}()
	}

	var pushers sync.WaitGroup
	for i := 0; i < nPushers; i++ {
		pushers.Add(1)
		go func(base int) {
			defer pushers.Done()
			for j := 0; j < perPusher; j++ {
				idx := base*perPusher + j
				if pushWork != nil {
					pushWork(idx)
				}
				t0 := time.Now()
				p.push(idx)
				latNs[idx] = time.Since(t0).Nanoseconds()
			}
		}(i)
	}
	pushers.Wait()
	drainers.Wait()

	require.Equal(t, int64(total), count.Load(), "every value received")
	return latNs
}

func pctl(sorted []int64, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p / 100 * float64(len(sorted)))
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return time.Duration(sorted[i])
}

func TestOutboxPool_PushLatencyTail(t *testing.T) {
	if testing.Short() {
		t.Skip("latency comparison benchmark")
	}
	// Heavy-tailed blocking-I/O handler (time.Sleep yields), ms..~1s. decorr is a
	// second stream so producer and consumer work aren't correlated.
	drain := func(v int) { time.Sleep(paretoWork(v)) }
	decorr := func(v int) { time.Sleep(paretoWork(v ^ 0x5DEECE66D)) }

	cfgs := []struct {
		name          string
		nP, nD, total int
		pushWork      func(int)
		drainWork     func(int)
	}{
		// Few fast producers, many slow consumers — consumer-provisioned. Empties
		// should usually be available; the regime to re-check for the cheat.
		{"P<<D underload", 4, 16, 4000, nil, drain},
		// Fungible-balanced: equal counts, both sides do comparable I/O work, so
		// production and consumption are rate-matched and the buffer oscillates
		// (transient empties exist — the regime where the cheat could win).
		{"P==D balanced", 8, 8, 4000, decorr, drain},
		// Fan-in overload: many producers, few slow consumers — sustained backlog.
		{"P>>D overload", 16, 2, 2000, nil, drain},
	}
	for _, c := range cfgs {
		perPusher := c.total / c.nP
		t.Logf("=== %s: %d pushers / %d drainers / %d items, Pareto I/O handler ===", c.name, c.nP, c.nD, c.total)
		for run := 0; run < 4; run++ {
			clean := newCleanPool()
			cheat := newCheatPool()
			lc := runLatency(t, clean, c.nP, c.nD, perPusher, c.pushWork, c.drainWork)
			lh := runLatency(t, cheat, c.nP, c.nD, perPusher, c.pushWork, c.drainWork)
			slices.Sort(lc)
			slices.Sort(lh)
			t.Logf("  run%d clean push p50=%-9v p99=%-9v p99.9=%-9v max=%-10v blk=%-5d alloc=%d",
				run, pctl(lc, 50), pctl(lc, 99), pctl(lc, 99.9), pctl(lc, 100), clean.blockCount(), clean.allocCount())
			t.Logf("  run%d cheat push p50=%-9v p99=%-9v p99.9=%-9v max=%-10v blk=%-5d alloc=%d",
				run, pctl(lh, 50), pctl(lh, 99), pctl(lh, 99.9), pctl(lh, 100), cheat.blockCount(), cheat.allocCount())
		}
	}
}
