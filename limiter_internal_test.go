// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import (
	"context"
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

	p1, ok := c.Acquire()
	chk.True(ok)
	p2, ok := c.Acquire()
	chk.True(ok, "second permit fits under limit 2")
	_, ok = c.Acquire()
	chk.False(ok, "third must miss at limit 2")

	p1.Release()
	p3, ok := c.Acquire()
	chk.True(ok, "a freed permit is reusable")

	p2.Release()
	p3.Release()
	c.ReleaseRef()
}

func TestSemaphoreResource_ZeroBlocksAll(t *testing.T) {
	chk := require.New(t)
	l := NewSemaphore(0)
	c := l.pool.NewCache()
	_, ok := c.Acquire()
	chk.False(ok, "limit 0 blocks every acquire")
	c.ReleaseRef()
}

func TestSemaphoreResource_Unlimited(t *testing.T) {
	chk := require.New(t)
	l := NewSemaphore(-1)
	c := l.pool.NewCache()
	perms := make([]permits.Permit, 0, 100)
	for range 100 {
		p, ok := c.Acquire()
		chk.True(ok, "unlimited never misses")
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
		p, err := c.AcquireWait(context.Background())
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
