// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Overdraft-path tests (weighted-acquisition.md §Overdraft). The shared newTestPool
// carries a standing-promise Overdraft precisely so the W2b-era tests keep their
// blocking semantics; the pools here exercise the grant (a bare semaphore — the
// non-implementing default) and refuse behaviors, plus the episode machinery those
// unlock: the standing sentinel, allowance claims, extensions, the infeasibility
// proof, and the suspension-counter stranger check.

// newGrantTestPool draws on a bare semaphore — no Overdraft capability, so the pool
// defaults to GRANT at a proven-infeasible point.
func newGrantTestPool(capacity int) *testPool {
	sem := &semaphore{capacity: capacity}
	return &testPool{Pool: NewPool(sem), sem: sem}
}

// refuseResource refuses every overdraft with its own error.
type refuseResource struct {
	*semaphore
	err error
}

func (r refuseResource) Overdraft(int) (bool, error) { return false, r.err }

// countingGrantResource grants every overdraft, recording the asked amounts.
type countingGrantResource struct {
	*semaphore
	asks []int
}

func (r *countingGrantResource) Overdraft(n int) (bool, error) {
	r.asks = append(r.asks, n)
	return true, nil
}

// standingSentinel returns the standing episode's sentinel demand, or nil.
func (p *Pool) standingSentinel() *Demand {
	if od := p.od.Load(); od != nil {
		return &od.sentinel
	}
	return nil
}

// episodeAnchor returns the standing episode's anchor cache, or nil.
func (p *Pool) episodeAnchor() *Cache {
	if od := p.od.Load(); od != nil {
		return od.sentinel.cache.Load()
	}
	return nil
}

// allowanceRemaining returns the standing episode's un-claimed allowance (0 when
// no episode stands).
func (p *Pool) allowanceRemaining() uint64 {
	if od := p.od.Load(); od != nil {
		return od.allowance.Load()
	}
	return 0
}

// checkEpisode asserts the overdraft invariants at a quiescent point: conservation
// untouched by grants (Σheld == inFlight ≤ capacity) and the episode equation
// Σ max(inUse−held, 0) + allowance == the episode's grant total (all zero outside
// an episode).
func (tp *testPool) checkEpisode(t require.TestingT) {
	var sumHeld, excess uint64
	for _, c := range tp.snapshot() {
		h, u := c.counts.load()
		sumHeld += h
		excess += excessOver(h, u)
	}
	//nolint:gosec // G115: small non-negative test values
	inFlight, capacity := uint64(tp.sem.inFlight.Load()), uint64(tp.sem.capacity)
	require.Equal(t, inFlight, sumHeld, "conservation: Σheld == checkedOut, untouched by grants")
	require.LessOrEqual(t, inFlight, capacity)
	var total, allowance uint64
	if od := tp.od.Load(); od != nil {
		tp.fifoMu.Lock()
		total = od.total
		tp.fifoMu.Unlock()
		allowance = od.allowance.Load()
	}
	require.Equal(t, total, excess+allowance,
		"episode invariant: Σ excess + allowance == the episode's grant total")
}

// The full arc of a granted overdraft: proven-infeasible head → grant → standing
// sentinel (arrivals stay gated and queue behind it) → exempt descendant claims and
// extends → owner park/resume round-trips its excess through the allowance → the
// body cache's destroy ends the episode, promoting the queued successor.
func TestOverdraftGrantStandingEpisode(t *testing.T) {
	sem := &semaphore{capacity: 3}
	res := &countingGrantResource{semaphore: sem}
	tp := &testPool{Pool: NewPool(res), sem: sem}
	tp.tb = t
	v := makeIdle(tp, 3) // all capacity checked out and idle: gatherable, none free

	g := tp.NewCache()
	var d Demand
	d.Init()
	pm, err := g.Acquire(&d, 5)
	require.NoError(t, err)
	require.True(t, pm.Held(), "w=5 on capacity 3: gather 3, overdraft 2")
	require.Same(t, d.cache.Load(), pm.backing)
	require.Equal(t, []int{2}, res.asks, "the ask is the post-gather shortfall")
	require.Same(t, tp.standingSentinel(), tp.barrier.Load(), "the sentinel stands: barrier armed")
	require.Same(t, d.cache.Load(), tp.episodeAnchor())
	require.Equal(t, uint64(0), tp.allowanceRemaining(), "the head's occupy claimed the whole grant")
	require.Equal(t, uint64(2), excessOverCache(d.cache.Load()), "inUse runs past held by the grant")
	tp.checkEpisode(t)

	// Arrivals stay gated and queue BEHIND the standing episode — no successor
	// gathers into the over-committed window.
	w1 := tp.NewCache()
	var d1 Demand
	d1.Init()
	p1, err := w1.Acquire(&d1, 1)
	require.NoError(t, err)
	require.False(t, p1.Held(), "weight-1 is gated while the episode stands")
	b := tp.NewCache()
	var db Demand
	db.Init()
	pb, err := b.Acquire(&db, 2)
	require.NoError(t, err)
	require.False(t, pb.Held(), "a w≥2 arrival registers behind the sentinel")
	require.Equal(t, 2, tp.fifoLen(), "sentinel + the queued arrival")

	// The owner parks: its excess flows home to the allowance.
	pm.Release()
	require.Equal(t, uint64(2), tp.allowanceRemaining(), "park returned the excess")
	tp.checkEpisode(t)

	// An exempt descendant borrows the parked hoard, then a bigger one extends the
	// episode: the same capability call, for the shortfall only, added to the
	// aggregate.
	ch := d.cache.Load().NewChild()
	var dch Demand
	dch.Init()
	pch, err := ch.Acquire(&dch, 1)
	require.NoError(t, err)
	require.True(t, pch.Held(), "the exempt descendant inherits the parked hoard")
	require.Same(t, d.cache.Load(), pch.backing)
	pch.Release()

	var dbig Demand

	dbig.Init()
	pbig, err := ch.Acquire(&dbig, 6)
	require.NoError(t, err)
	require.True(t, pbig.Held(), "the descendant extends: 3 borrowable + 2 allowance + 1 fresh grant")
	require.Equal(t, []int{2, 1}, res.asks, "the extension asked only the shortfall")
	tp.checkEpisode(t)
	pbig.Release()
	dbig.Invalidate()

	// The owner resumes into its home, claiming its excess back from the allowance.
	pm2, err := g.Acquire(&d, 5)
	require.NoError(t, err)
	require.True(t, pm2.Held(), "resume reacquire: the episode owner is exempt at its own anchor")
	require.Same(t, d.cache.Load(), pm2.backing)
	tp.checkEpisode(t)
	pm2.Release()

	// Completion: the demand's ref drops and the drained subtree destroys the body
	// cache — the episode ends with the allowance necessarily home, and the queued
	// successor is promoted to a live (gathering) head.
	require.True(t, ch.ReleaseRef())
	d.Invalidate()
	require.Nil(t, tp.od.Load(), "episode end retired the pooled episode state")
	require.Same(t, &db, tp.barrier.Load(), "the queued arrival was promoted to head")
	tp.checkEpisode(t)

	pb2, err := b.Acquire(&db, 2)
	require.NoError(t, err)
	require.True(t, pb2.Held(), "the promoted head gathers the drained capacity")
	pb2.Release()
	db.Invalidate()
	d1.Invalidate()
	for _, c := range []*Cache{v, g, w1, b} {
		require.True(t, c.ReleaseRef())
	}
	require.Equal(t, 0, tp.totalHeld(), "no permit leaked")
	require.Nil(t, tp.barrier.Load())
	tp.checkEpisode(t)
}

// A refused overdraft fails the unit with the resource's own error, dequeues the
// demand (passing the barrier), and leaves the pool fully usable.
func TestOverdraftRefusalFailsUnitAndPassesBarrier(t *testing.T) {
	refuseErr := errors.New("memory wall: 4 exceeds any capacity I will reach")
	sem := &semaphore{capacity: 3}
	tp := &testPool{Pool: NewPool(refuseResource{sem, refuseErr}), sem: sem}
	tp.tb = t
	v := makeIdle(tp, 2) // 2 idle + 1 free < 4

	g := tp.NewCache()
	var d Demand
	d.Init()
	_, err := g.Acquire(&d, 4)
	require.ErrorIs(t, err, refuseErr, "the refusal error is the resource's own")
	require.Nil(t, tp.barrier.Load(), "the refused sole head disarmed the barrier")
	require.Nil(t, d.pool.Load(), "the refused demand was dequeued")
	d.Invalidate() // the caller's error path releases the home (and its hoard)
	require.Equal(t, 0, tp.totalHeld(), "the drained hoard returned to the Resource")

	// The pool is unharmed: a feasible acquire proceeds.
	var d2 Demand
	d2.Init()
	pm, err := g.Acquire(&d2, 3)
	require.NoError(t, err)
	require.True(t, pm.Held())
	pm.Release()
	d2.Invalidate()
	require.True(t, g.ReleaseRef())
	require.True(t, v.ReleaseRef())
	tp.check(t)
}

// AcquireWait surfaces a refusal as its error and invalidates the demand itself.
func TestOverdraftRefusalThroughAcquireWait(t *testing.T) {
	refuseErr := errors.New("refused")
	sem := &semaphore{capacity: 2}
	tp := &testPool{Pool: NewPool(refuseResource{sem, refuseErr}), sem: sem}

	g := tp.NewCache()
	var d Demand
	d.Init()
	_, err := g.AcquireWait(context.Background(), &d, 3)
	require.ErrorIs(t, err, refuseErr)
	require.Nil(t, d.pool.Load())
	require.Nil(t, d.cache.Load(), "AcquireWait's error path invalidated the demand")
	require.Nil(t, tp.barrier.Load())
	require.True(t, g.ReleaseRef())
	require.Equal(t, 0, tp.totalHeld())
}

// The infeasibility proof gates the grant: while anything runs (inUse > 0 anywhere),
// releases can still move the world, so even a granting resource is not consulted —
// the head waits.
func TestOverdraftWaitsWhileAnythingRuns(t *testing.T) {
	tp := newGrantTestPool(3)
	hog := tp.NewCache()
	var dh Demand
	dh.Init()
	ph, err := hog.Acquire(&dh, 1)
	require.NoError(t, err)
	require.True(t, ph.Held()) // a running body: inUse=1 somewhere

	g := tp.NewCache()
	var d Demand
	d.Init()
	pg, err := g.Acquire(&d, 4)
	require.NoError(t, err)
	require.False(t, pg.Held(), "no grant while a release could still change the answer")
	require.Same(t, &d, tp.barrier.Load(), "the head stays armed, waiting")

	ph.Release() // the last runner parks; now the proof can pass
	pg, err = g.Acquire(&d, 4)
	require.NoError(t, err)
	require.True(t, pg.Held(), "zero inUse everywhere: the gather takes the idle permit and the grant covers the rest")
	tp.checkEpisode(t)

	pg.Release()
	d.Invalidate()
	dh.Invalidate()
	require.True(t, g.ReleaseRef())
	require.True(t, hog.ReleaseRef())
	require.Equal(t, 0, tp.totalHeld())
}

// The ancestor-exempt trigger: a suspended holder OFF the head's driver chain is a
// stranger — its resume races the over-commitment — so the grant waits for the
// suspension to end; a suspension ON the chain (an ancestor drive the head runs
// causally inside) does not block.
func TestOverdraftStrangerSuspensionBlocksGrant(t *testing.T) {
	tp := newGrantTestPool(2)
	tp.tb = t
	v := makeIdle(tp, 2)

	stranger := tp.NewCache() // an unrelated wave's cache: off the head's chain
	stranger.SuspendDriver()

	g := tp.NewCache()
	var d Demand
	d.Init()
	pg, err := g.Acquire(&d, 3)
	require.NoError(t, err)
	require.False(t, pg.Held(), "a stranger's suspension blocks the grant")

	stranger.ResumeDriver() // ends the suspension (and nudges the armed pool)
	pg, err = g.Acquire(&d, 3)
	require.NoError(t, err)
	require.True(t, pg.Held(), "with the stranger visible again, the grant proceeds")
	tp.checkEpisode(t)
	pg.Release()
	d.Invalidate()

	// On-chain suspension: a driver parked INTO the registering cache's own wave.
	g2 := tp.NewCache()
	g2.SuspendDriver() // the demand registers under g2 → g2 is on the head's chain
	var d2 Demand
	d2.Init()
	pg2, err := g2.Acquire(&d2, 3)
	require.NoError(t, err)
	require.True(t, pg2.Held(), "an on-chain suspension is causally inside the head — no stranger")
	tp.checkEpisode(t)
	pg2.Release()
	d2.Invalidate()
	g2.ResumeDriver()

	for _, c := range []*Cache{v, stranger, g, g2} {
		require.True(t, c.ReleaseRef())
	}
	require.Equal(t, 0, tp.totalHeld())
	require.Nil(t, tp.barrier.Load())
}

// A parked exempt claimant is woken by a release routed through episodeNotify: while
// the episode stands, freed capacity must reach the subtree's waiters (the satisfied
// head consumes no wakes, and everyone else is gated).
func TestEpisodeReleaseWakesParkedExemptClaimant(t *testing.T) {
	tp := newGrantTestPool(1)
	g := tp.NewCache()
	var d Demand
	d.Init()
	pm, err := g.Acquire(&d, 2) // infeasible on capacity 1 → grant → episode
	require.NoError(t, err)
	require.True(t, pm.Held())
	require.Same(t, tp.standingSentinel(), tp.barrier.Load())

	// A descendant needs weight the busy episode cannot spare: it parks on
	// episodeNotify (exempt, unregistered).
	ch := d.cache.Load().NewChild()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got := make(chan error, 1)
	go func() {
		var dch Demand
		dch.Init()
		defer dch.Invalidate()
		pch, err := ch.AcquireWait(ctx, &dch, 1)
		if err == nil {
			pch.Release()
		}
		got <- err
	}()

	time.Sleep(50 * time.Millisecond) // let the claimant park
	pm.Release()                      // the owner parks: excess home → wake → episodeNotify

	select {
	case err := <-got:
		require.NoError(t, err, "the parked exempt claimant must be woken by the release")
	case <-time.After(20 * time.Second):
		t.Fatal("the release never reached the claimant parked on episodeNotify")
	}

	require.True(t, ch.ReleaseRef())
	d.Invalidate()
	require.Nil(t, tp.barrier.Load())
	require.True(t, g.ReleaseRef())
	require.Equal(t, 0, tp.totalHeld())
	tp.checkEpisode(t)
}

// Serialized episodes under -race: every contender's weight exceeds capacity, so
// every satisfaction is a full grant→episode→end cycle, with successors queued
// behind each standing sentinel and promoted at its end. Wake routing, the sentinel
// swap, allowance round-trips, and endEpisode all race each other here; a lost wake
// or a stuck sentinel surfaces as a ctx-deadline failure.
func TestConcurrentOverdraftEpisodes(t *testing.T) {
	const capacity, workers, iters = 2, 4, 300
	tp := newGrantTestPool(capacity)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var failed atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := tp.NewCache()
			defer c.ReleaseRef()
			for range iters {
				var d Demand
				d.Init()
				pm, err := c.AcquireWait(ctx, &d, capacity+1)
				if err != nil {
					failed.Add(1)
					return
				}
				pm.Release()
				d.Invalidate() // completes the episode; the successor promotes
			}
		}()
	}
	wg.Wait()

	require.Equal(t, int64(0), failed.Load(), "every over-capacity demand must complete via its episode")
	require.Nil(t, tp.barrier.Load(), "quiescence disarms")
	require.Nil(t, tp.od.Load(), "quiescence retires the episode state")
	require.Equal(t, 0, tp.totalHeld(), "no permit leaked")
	tp.checkEpisode(t)
}

// excessOverCache reads a cache's current overdraft excess, for tests.
func excessOverCache(c *Cache) uint64 {
	h, u := c.counts.load()
	return excessOver(h, u)
}
