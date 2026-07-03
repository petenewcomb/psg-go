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

// fifoLen reads the registered-demand count, for assertions.
func (p *Pool) fifoLen() int {
	p.fifoMu.Lock()
	defer p.fifoMu.Unlock()
	return len(p.fifo)
}

// Sticky head + FIFO succession: capacity freed while the barrier is armed goes to
// the HEAD, not to whichever registered demand retries first — and weight-1 traffic
// is gated without registering (Decision 3: it never joins the FIFO).
func TestBarrierFIFOOrderAndGating(t *testing.T) {
	tp := newTestPool(2)
	hog := tp.NewCache()
	var dh Demand
	hp, err := hog.Acquire(&dh, 2)
	require.NoError(t, err)
	require.True(t, hp.Held(), "w=2 whole-grant on a free Resource, no registration")
	require.Equal(t, 0, tp.fifoLen(), "a satisfied fast path never registers")

	a := tp.NewCache()
	b := tp.NewCache()
	var da, db Demand
	pa0, err := a.Acquire(&da, 2)
	require.NoError(t, err)
	require.False(t, pa0.Held(), "everything is in use; a registers and arms")
	require.Same(t, &da, tp.barrier.Load(), "a is the head")
	pb0, err := b.Acquire(&db, 2)
	require.NoError(t, err)
	require.False(t, pb0.Held(), "b queues behind a")
	require.Equal(t, 2, tp.fifoLen())

	w1 := tp.NewCache()
	var d1 Demand
	pw0, err := w1.Acquire(&d1, 1)
	require.NoError(t, err)
	require.False(t, pw0.Held(), "weight-1 is gated while armed")
	require.Equal(t, 2, tp.fifoLen(), "weight-1 never registers")

	hp.Release() // 2 permits go borrowable in hog's cache

	pb1, err := b.Acquire(&db, 2)
	require.NoError(t, err)
	require.False(t, pb1.Held(), "freed capacity must NOT satisfy the non-head, even on its retry")
	pa, err := a.Acquire(&da, 2)
	require.NoError(t, err)
	require.True(t, pa.Held(), "the sticky head takes the freed capacity")
	require.Same(t, da.cache.Load(), pa.backing)
	require.Same(t, &db, tp.barrier.Load(), "FIFO succession promoted b")

	pa.Release() // a parks: its 2 go borrowable in a's home
	pb, err := b.Acquire(&db, 2)
	require.NoError(t, err)
	require.True(t, pb.Held(), "the promoted head gathers from the parked predecessor's hoard")
	require.Nil(t, tp.barrier.Load(), "the emptied FIFO disarms")

	p1, err := w1.Acquire(&d1, 1)
	require.NoError(t, err)
	require.False(t, p1.Held(), "capacity is genuinely exhausted for weight-1 now")

	pb.Release()
	for _, d := range []*Demand{&dh, &da, &db, &d1} {
		d.Invalidate()
	}
	for _, c := range []*Cache{hog, a, b, w1} {
		require.True(t, c.ReleaseRef())
	}
	require.Equal(t, 0, tp.totalHeld(), "no permit leaked")
	tp.check(t)
}

// Invalidating the head passes the barrier to the next registered demand and drains
// the head's partial hoard back to the Resource, where the successor can gather it.
func TestBarrierHeadInvalidationPromotesSuccessor(t *testing.T) {
	tp := newTestPool(3)
	tp.tb = t
	v := makeIdle(tp, 2) // 2 idle, 1 free

	a := tp.NewCache()
	var da Demand
	pa0, err := a.Acquire(&da, 4)
	require.NoError(t, err, "the promise-mode Overdraft waits rather than granting or refusing")
	require.False(t, pa0.Held(), "w=4 on capacity 3 is infeasible; a hoards 2 and stays head")
	require.Equal(t, uint64(2), da.cache.Load().held())

	b := tp.NewCache()
	var db Demand
	pb0, err := b.Acquire(&db, 3)
	require.NoError(t, err)
	require.False(t, pb0.Held(), "b queues behind the armed head")

	da.Invalidate()
	require.Same(t, &db, tp.barrier.Load(), "b promoted")

	pb, err := b.Acquire(&db, 3)
	require.NoError(t, err)
	require.True(t, pb.Held(), "the promoted head assembles from the drained hoard + free capacity")
	require.Same(t, db.cache.Load(), pb.backing)

	pb.Release()
	db.Invalidate()
	for _, c := range []*Cache{v, a, b} {
		require.True(t, c.ReleaseRef())
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
	var dv Demand
	pv, err := v.Acquire(&dv, 1)
	require.NoError(t, err)
	require.True(t, pv.Held())
	pv.Release() // 1 idle in v; 2 free

	g := tp.NewCache()
	var d Demand
	pm, err := g.Acquire(&d, 3)
	require.NoError(t, err)
	require.True(t, pm.Held(), "3 = steal 1 + grant 2")
	home := pm.backing
	require.Same(t, d.cache.Load(), home)

	pm.Release() // park: the home keeps 3 borrowable
	pm2, err := g.Acquire(&d, 3)
	require.NoError(t, err)
	require.True(t, pm2.Held(), "resume reacquire")
	require.Same(t, home, pm2.backing, "step-0 own-home hit — stable backing, no re-registration")
	require.Equal(t, 0, tp.fifoLen(), "no registration on the home hit")

	pm2.Release()
	d.Invalidate()
	dv.Invalidate()
	require.True(t, g.ReleaseRef())
	require.True(t, v.ReleaseRef())
	require.Equal(t, 0, tp.totalHeld())
	tp.check(t)
}

// Over-subscribed weighted contention under -race — unrepresentable before the
// barrier (freelance gatherers could starve or livelock): mixed weights far beyond
// capacity churn registration, sticky-head gathering, FIFO succession, weight-1
// gating, and the AcquireWait wake-forwarding that keeps the head reachable. A
// conservation hole surfaces as a ctx-deadline error (or a hang under the harness
// timeout), not silent unfairness.
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
			var d Demand
			defer d.Invalidate()
			for range iters {
				pm, err := c.AcquireWait(ctx, &d, w)
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
	require.Nil(t, tp.barrier.Load(), "quiescence disarms")
}
