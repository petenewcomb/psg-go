// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// queued reports whether the demand currently occupies the queue or the head
// slot, for assertions.
func (d *Demand) queued() bool { return d.pool.Load() != nil }

// Sticky head + strict arrival order: capacity freed while a head stands goes to
// the HEAD, not to whichever queued demand retries first — and weight-1 traffic
// joins the same queue in arrival order (Queue unification: every weight queues;
// weight-1's old exclusion is gone).
func TestBarrierFIFOOrderAndGating(t *testing.T) {
	tp := newTestPool(2)
	hog := tp.NewCache()
	dh := NewDemand()
	hp, err := hog.Acquire(dh, 2)
	require.NoError(t, err)
	require.True(t, hp.Held(), "w=2 whole-grant on a free Resource, no registration")
	require.Nil(t, tp.head.Load(), "a satisfied fast path never queues")
	require.False(t, dh.queued())

	a := tp.NewCache()
	b := tp.NewCache()
	da := NewDemand()
	db := NewDemand()
	pa0, err := a.Acquire(da, 2)
	require.NoError(t, err)
	require.False(t, pa0.Held(), "everything is in use; a queues and takes the head slot")
	require.Same(t, da, tp.head.Load(), "a is the head")
	pb0, err := b.Acquire(db, 2)
	require.NoError(t, err)
	require.False(t, pb0.Held(), "b queues behind a")
	require.True(t, db.queued())

	w1 := tp.NewCache()
	d1 := NewDemand()
	pw0, err := w1.Acquire(d1, 1)
	require.NoError(t, err)
	require.False(t, pw0.Held(), "weight-1 is gated while a head stands")
	require.True(t, d1.queued(), "weight-1 joins the queue in arrival order")

	hp.Release() // 2 permits go borrowable in hog's cache

	pb1, err := b.Acquire(db, 2)
	require.NoError(t, err)
	require.False(t, pb1.Held(), "freed capacity must NOT satisfy the non-head, even on its retry")
	pa, err := a.Acquire(da, 2)
	require.NoError(t, err)
	require.True(t, pa.Held(), "the sticky head takes the freed capacity")
	require.Same(t, da.cache.Load(), pa.backing)
	require.Same(t, db, tp.head.Load(), "arrival-order succession promoted b")

	pa.Release() // a parks: its 2 go borrowable in a's home
	pb, err := b.Acquire(db, 2)
	require.NoError(t, err)
	require.True(t, pb.Held(), "the promoted head gathers from the parked predecessor's hoard")
	require.Same(t, d1, tp.head.Load(), "the weight-1 demand is next in arrival order")

	p1, err := w1.Acquire(d1, 1)
	require.NoError(t, err)
	require.False(t, p1.Held(), "capacity is genuinely exhausted for the weight-1 head")

	pb.Release()
	for _, d := range []*Demand{dh, da, db, d1} {
		d.Invalidate()
	}
	require.Nil(t, tp.head.Load(), "invalidating the last waiter opens the slot")
	for _, c := range []*Cache{hog, a, b, w1} {
		c.ReleaseRef()
	}
	require.Equal(t, 0, tp.totalHeld(), "no permit leaked")
	tp.check(t)
}

// Invalidating the head hands the slot to the next queued demand and drains
// the head's partial hoard back to the Resource, where the successor can gather it.
func TestBarrierHeadInvalidationPromotesSuccessor(t *testing.T) {
	tp := newTestPool(3)
	tp.tb = t
	v := makeIdle(tp, 2) // 2 idle, 1 free

	a := tp.NewCache()
	da := NewDemand()
	pa0, err := a.Acquire(da, 4)
	require.NoError(t, err, "the 'not now' Overdraft waits rather than granting or refusing")
	require.False(t, pa0.Held(), "w=4 on capacity 3 is infeasible; a hoards 2 and stays head")
	require.Equal(t, uint64(2), da.cache.Load().held())

	b := tp.NewCache()
	db := NewDemand()
	pb0, err := b.Acquire(db, 3)
	require.NoError(t, err)
	require.False(t, pb0.Held(), "b queues behind the armed head")

	da.Invalidate()
	require.Same(t, db, tp.head.Load(), "b promoted")

	pb, err := b.Acquire(db, 3)
	require.NoError(t, err)
	require.True(t, pb.Held(), "the promoted head assembles from the drained hoard + free capacity")
	require.Same(t, db.cache.Load(), pb.backing)

	pb.Release()
	db.Invalidate()
	for _, c := range []*Cache{v, a, b} {
		c.ReleaseRef()
	}
	require.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}

// A satisfied demand's body cache persists as its home: after release, the same
// demand's next acquire is a step-0 own-home hit — no registration, no barrier, and
// the backing is stable across park/resume.
func TestDemandHomePersistsAcrossEpisodes(t *testing.T) {
	tp := newTestPool(3)
	v := tp.NewCache()
	dv := NewDemand()
	pv, err := v.Acquire(dv, 1)
	require.NoError(t, err)
	require.True(t, pv.Held())
	pv.Release() // 1 idle in v; 2 free

	g := tp.NewCache()
	d := NewDemand()
	pm, err := g.Acquire(d, 3)
	require.NoError(t, err)
	require.True(t, pm.Held(), "3 = steal 1 + grant 2")
	home := pm.backing
	require.Same(t, d.cache.Load(), home)

	pm.Release() // park: the home keeps 3 borrowable
	pm2, err := g.Acquire(d, 3)
	require.NoError(t, err)
	require.True(t, pm2.Held(), "resume reacquire")
	require.Same(t, home, pm2.backing, "step-0 own-home hit — stable backing, no re-queueing")
	require.Nil(t, tp.head.Load(), "no queueing on the home hit")

	pm2.Release()
	d.Invalidate()
	dv.Invalidate()
	g.ReleaseRef()
	v.ReleaseRef()
	require.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}

// Over-subscribed weighted contention under -race: mixed weights far beyond
// capacity churn enqueueing, promotion scans, sticky-head gathering, and the
// mailbox wake path — with weight-1 demands now queueing alongside the weighted
// ones (Queue unification). A conservation hole surfaces as a ctx-deadline error
// (or a hang under the harness timeout), not silent unfairness.
func TestConcurrentWeightedOverSubscribed(t *testing.T) {
	const capacity, workers, iters = 3, 6, 500
	tp := newTestPool(capacity)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var failed atomic.Int64
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c := tp.NewCache()
			defer c.ReleaseRef()
			d := NewDemand()
			defer d.Invalidate()
			for range iters {
				pm, err := c.AcquireWait(ctx, d, w)
				if err != nil {
					failed.Add(1)
					return
				}
				pm.Release()
			}
		}(1 + i%capacity)
	}
	wg.Wait()

	require.Equal(t, int64(0), failed.Load(),
		"every weighted contender must eventually be satisfied (FIFO no-starvation)")
	require.NoError(t, checkInvariants(tp.sem, tp.snapshot()))
	require.Equal(t, 0, tp.totalHeld(), "no permit leaked")
	require.Nil(t, tp.head.Load(), "quiescence opens the slot")
}
