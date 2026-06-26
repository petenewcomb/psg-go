// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/stretchr/testify/require"
)

// This file holds test-only fixtures and oracles, compiled only under `go test`.
//
// The forest's nbcq queues can't be walked non-destructively, so the oracles take an
// explicit slice of every cache the test created (the tests track that anyway). They
// read each cache's atomic counter, so they are meant to run at a quiescent point.

// semaphore is the weight-1 test Resource: a fixed capacity and an atomic in-flight
// count (atomic so the concurrency harness can share it).
type semaphore struct {
	capacity int
	inFlight atomic.Int64
}

func (s *semaphore) TryAcquire(n int) bool {
	for {
		cur := s.inFlight.Load()
		if cur+int64(n) > int64(s.capacity) {
			return false
		}
		if s.inFlight.CompareAndSwap(cur, cur+int64(n)) {
			return true
		}
	}
}

func (s *semaphore) Release(n int) { s.inFlight.Add(-int64(n)) }

// checkInvariants verifies the model-check targets over the given caches: per-cache
// inUse ≤ held; Σheld equals the Resource's in-flight count (cross-checking the two
// independent views); and both stay within capacity. (permit-core.md "Invariants".)
func checkInvariants(sem *semaphore, caches []*Cache) error {
	var sumHeld, sumInUse uint64
	for _, c := range caches {
		h, u := c.counts.load()
		if u > h {
			return fmt.Errorf("per-cache: inUse > held (held=%d inUse=%d)", h, u)
		}
		sumHeld += h
		sumInUse += u
	}
	//nolint:gosec // G115: in-flight and capacity are small non-negative test values
	inFlight, capacity := uint64(sem.inFlight.Load()), uint64(sem.capacity)
	if sumHeld != inFlight {
		return fmt.Errorf("conservation: Σheld=%d != Resource.inFlight=%d", sumHeld, inFlight)
	}
	if inFlight > capacity {
		return fmt.Errorf("conservation: inFlight=%d > capacity=%d", inFlight, capacity)
	}
	if sumInUse > capacity {
		return fmt.Errorf("concurrency bound: ΣinUse=%d > capacity=%d", sumInUse, capacity)
	}
	return nil
}

// hasBorrowable reports whether any of the given caches holds an idle permit.
func hasBorrowable(caches []*Cache) bool {
	for _, c := range caches {
		if h, u := c.counts.load(); h > u {
			return true
		}
	}
	return false
}

// totalHeld returns Σheld over the given caches — the permits checked out of the
// Resource.
func totalHeld(caches []*Cache) int {
	var sum uint64
	for _, c := range caches {
		h, _ := c.counts.load()
		sum += h
	}
	return int(sum)
}

// held returns a cache's current held, for tests.
func (c *Cache) held() uint64 {
	h, _ := c.counts.load()
	return h
}

// testPool wraps a Pool, tracking every cache created through it so the oracles have
// the full set to sum over. The tracking slice is guarded for the concurrency tests.
type testPool struct {
	*Pool
	sem *semaphore

	mu     sync.Mutex
	caches []*Cache
}

func newTestPool(capacity int) *testPool {
	sem := &semaphore{capacity: capacity}
	return &testPool{Pool: NewPool(sem), sem: sem}
}

func (tp *testPool) track(c *Cache) *Cache {
	tp.mu.Lock()
	tp.caches = append(tp.caches, c)
	tp.mu.Unlock()
	return c
}

func (tp *testPool) NewCache() *Cache { return tp.track(tp.Pool.NewCache()) }

func (tp *testPool) newChild(parent *Cache) *Cache { return tp.track(parent.NewChild()) }

// snapshot returns a copy of the tracked caches for the oracles to read.
func (tp *testPool) snapshot() []*Cache {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return append([]*Cache(nil), tp.caches...)
}

func (tp *testPool) check(t require.TestingT) {
	require.NoError(t, checkInvariants(tp.sem, tp.snapshot()))
}

func (tp *testPool) totalHeld() int      { return totalHeld(tp.snapshot()) }
func (tp *testPool) hasBorrowable() bool { return hasBorrowable(tp.snapshot()) }
