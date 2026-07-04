// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/petenewcomb/streampool/internal/permits"
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

	var d permits.Demand

	d.Init()
	p1, err := c.Acquire(&d, 1)
	chk.NoError(err)
	chk.True(p1.Held())
	p2, err := c.Acquire(&d, 1)
	chk.NoError(err)
	chk.True(p2.Held(), "second permit fits under limit 2")
	px, err := c.Acquire(&d, 1)
	chk.NoError(err)
	chk.False(px.Held(), "third must miss at limit 2")

	p1.Release()
	p3, err := c.Acquire(&d, 1)
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
	var d permits.Demand
	d.Init()
	p, err := c.Acquire(&d, 1)
	chk.NoError(err)
	chk.False(p.Held(), "limit 0 blocks every acquire")
	c.ReleaseRef()
}

func TestSemaphoreResource_Unlimited(t *testing.T) {
	chk := require.New(t)
	l := NewSemaphore(-1)
	c := l.pool.NewCache()
	perms := make([]permits.Permit, 0, 100)
	var d permits.Demand
	d.Init()
	for range 100 {
		p, err := c.Acquire(&d, 1)
		chk.NoError(err)
		chk.True(p.Held(), "unlimited never misses")
		perms = append(perms, p)
	}
	for _, p := range perms {
		p.Release()
	}
	c.ReleaseRef()
}

func TestSetMaxConcurrency_RaiseWakesParkedWaiter(t *testing.T) {
	chk := require.New(t)
	l := NewSemaphore(0) // start blocked
	c := l.pool.NewCache()

	acquired := make(chan struct{})
	var acqErr error
	go func() {
		// Parks on the Pool until SetMaxConcurrency raises the ceiling and wakes it.
		var d permits.Demand
		d.Init()
		p, err := c.AcquireWait(context.Background(), &d, 1)
		acqErr = err
		if err == nil {
			p.Release()
		}
		close(acquired) // the close happens-before the main goroutine's read of acqErr
	}()

	// Let the goroutine reach AcquireWait's park before raising.
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

// A capacity raise frees MULTIPLE slots at once with nothing stealable anywhere —
// the multi-permit event class. The raise seeds the wake chain (one chained wake;
// each admitted waiter probes the next), so every newly satisfiable parked waiter
// admits; the replaced broadcast is gone and wake-one alone would strand all but
// the first. Waiters hold their permits until everyone is in, so the raise is the
// only wake source.
func TestSetMaxConcurrency_RaiseChainAdmitsAllParkedWaiters(t *testing.T) {
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
			var d permits.Demand
			d.Init()
			defer d.Invalidate() // after the release below — a held permit may back from the demand's home
			p, err := c.AcquireWait(ctx, &d, 1)
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
