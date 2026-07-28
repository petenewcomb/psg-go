// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/petenewcomb/streampool/internal/permits"
	"github.com/petenewcomb/streampool/internal/rdvq"
	"github.com/stretchr/testify/require"
)

// These tests cover the streampool-specific limiter pieces: the semaphoreResource as a
// permits.Resource and the SetMaxConcurrency capacity-grow wake. The permit-core
// allocation semantics (acquire / inherit / steal / suspend / wait) are exercised by the
// internal/permits package's own tests.

func TestSemaphoreResource_Accounting(t *testing.T) {
	chk := require.New(t)
	l := NewSemaphore(2)
	c := l.pool.NewCache()

	d := permits.NewDemand()
	p1, err := c.Acquire(d, 1)
	chk.NoError(err)
	chk.True(p1.Held())
	p2, err := c.Acquire(d, 1)
	chk.NoError(err)
	chk.True(p2.Held(), "second permit fits under limit 2")
	px, err := c.Acquire(d, 1)
	chk.NoError(err)
	chk.False(px.Held(), "third must miss at limit 2")

	p1.Release()
	p3, err := c.Acquire(d, 1)
	chk.NoError(err)
	chk.True(p3.Held(), "a freed permit is reusable")

	p2.Release()
	p3.Release()
	c.ReleaseRef()
}

func TestSemaphoreResource_ZeroBlocksAll(t *testing.T) {
	chk := require.New(t)
	l := NewSemaphore(0)
	c := l.pool.NewCache()
	d := permits.NewDemand()
	p, err := c.Acquire(d, 1)
	chk.NoError(err)
	chk.False(p.Held(), "limit 0 blocks every acquire")
	c.ReleaseRef()
}

func TestSemaphoreResource_Unlimited(t *testing.T) {
	chk := require.New(t)
	l := NewSemaphore(-1)
	c := l.pool.NewCache()
	perms := make([]permits.Permit, 0, 100)
	d := permits.NewDemand()
	for range 100 {
		p, err := c.Acquire(d, 1)
		chk.NoError(err)
		chk.True(p.Held(), "unlimited never misses")
		perms = append(perms, p)
	}
	for _, p := range perms {
		p.Release()
	}
	c.ReleaseRef()
}

// blockingAcquire is the tests' blocking acquire against a Limiter's pool: the
// production park protocol — Acquire, and on a miss prepare a Waiter, arm it as
// the demand's attendant, and re-check before blocking.
func blockingAcquire(ctx context.Context, c *permits.Cache, d *permits.Demand, w int) (permits.Permit, error) {
	var waiter rdvq.Waiter
	waiter.Init()
	for {
		pm, err := c.Acquire(d, w)
		if err != nil {
			d.Invalidate()
			return permits.Permit{}, err
		}
		if pm.Held() {
			return pm, nil
		}
		ch := waiter.Prepare()
		d.SetAttendant(&waiter)
		pm, err = c.Acquire(d, w)
		if err != nil || pm.Held() {
			waiter.Finish(false)
			if err != nil {
				d.Invalidate()
				return permits.Permit{}, err
			}
			return pm, nil
		}
		select {
		case <-ch:
			waiter.Finish(true)
		case <-ctx.Done():
			waiter.Finish(false)
			d.Invalidate()
			return permits.Permit{}, ctx.Err()
		}
	}
}

func TestSetMaxConcurrency_RaiseWakesParkedWaiter(t *testing.T) {
	chk := require.New(t)
	l := NewSemaphore(0) // start blocked
	c := l.pool.NewCache()

	acquired := make(chan struct{})
	var acqErr error
	go func() {
		// Parks on the Pool until SetMaxConcurrency raises the ceiling and wakes it.
		d := permits.NewDemand()
		p, err := blockingAcquire(context.Background(), c, d, 1)
		acqErr = err
		if err == nil {
			p.Release()
		}
		close(acquired) // the close happens-before the main goroutine's read of acqErr
	}()

	// Let the goroutine reach the park before raising.
	select {
	case <-acquired:
		t.Fatal("acquired before capacity was raised")
	case <-time.After(20 * time.Millisecond):
	}

	SetMaxConcurrency(l, 1)

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("raising the ceiling did not wake the parked waiter")
	}
	chk.NoError(acqErr)
	c.ReleaseRef()
}

func TestSetMaxConcurrency_PanicsOnUnlimitedLimiter(t *testing.T) {
	require.Panics(t, func() { SetMaxConcurrency(Limiter{}, 1) },
		"SetMaxConcurrency on the zero (unlimited) Limiter must panic")
}

// A capacity raise frees MULTIPLE units at once with nothing stealable anywhere —
// the multi-unit event class. The raise mints one token; each satisfied head's
// retirement re-probes (conservation rule 5), so every newly satisfiable parked
// waiter admits one by one. Waiters hold their permits until everyone is in, so
// the raise is the only mint source.
func TestSetMaxConcurrency_RaiseReprobeAdmitsAllParkedWaiters(t *testing.T) {
	chk := require.New(t)
	const raised = 3
	l := NewSemaphore(0) // start fully blocked; nothing checked out, nothing stealable
	c := l.pool.NewCache()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admitted := make(chan struct{}, raised)
	holdRelease := make(chan struct{})
	errs := make(chan error, raised)
	var wg sync.WaitGroup
	for range raised {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := permits.NewDemand()
			defer d.Invalidate() // after the release below — a held permit may back from the demand's home
			p, err := blockingAcquire(ctx, c, d, 1)
			if err != nil {
				errs <- err
				return
			}
			admitted <- struct{}{}
			<-holdRelease
			p.Release()
		}()
	}

	// Let the waiters park, then open all three slots in one raise.
	time.Sleep(50 * time.Millisecond)
	SetMaxConcurrency(l, raised)

	for i := range raised {
		select {
		case <-admitted:
		case err := <-errs:
			chk.NoError(err, "waiter %d failed instead of admitting", i)
		case <-time.After(20 * time.Second):
			chk.FailNowf("chain under-notified", "only %d of %d waiters admitted after the raise", i, raised)
		}
	}
	close(holdRelease)
	wg.Wait()
	c.ReleaseRef()
}

// The semaphore's overdraft answer is "not now" for every weight — the
// OverdraftResource middle outcome (granted=false, err=nil): the head keeps
// waiting, no grant, no failure, and no commitment (a later call could refuse).
// It stays uniformly "not now" until weighted-acquisition step 4 wires the
// exempt-subtree meta-redirect: pre-step-4 an episode owner's own downstream
// dispatches would be gated behind its episode and wedge, so granting is unsafe
// regardless of weight. Step 4 decides the real policy; update this test with it.
func TestSemaphoreOverdraftPolicy(t *testing.T) {
	chk := require.New(t)

	paused := NewSemaphore(0)
	pc := paused.pool.NewCache()
	dp := permits.NewDemand()
	pm, err := pc.Acquire(dp, 3)
	chk.NoError(err, "paused: not now, not a refusal")
	chk.False(pm.Held(), "limit 0 blocks every weight until raised")
	dp.Free()
	pc.ReleaseRef()

	sem := NewSemaphore(2)
	c := sem.pool.NewCache()
	d := permits.NewDemand()
	pm, err = c.Acquire(d, 3)
	chk.NoError(err, "over the ceiling: not now too, until step 4 makes episodes safe")
	chk.False(pm.Held(), "no grant before the exempt subtree is representable")
	d.Free()
	c.ReleaseRef()
}
