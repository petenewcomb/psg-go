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

// borrowableTotal sums held−inUse over the given caches — everything a gather could
// steal, anywhere in the forest.
func borrowableTotal(caches []*Cache) int {
	var sum uint64
	for _, c := range caches {
		h, u := c.counts.load()
		sum += h - u
	}
	return int(sum) //nolint:gosec // G115: bounded by the Resource's small test capacity
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

// listSlice returns the members of l front-to-back (coldest-first). It takes l's lock,
// so it is safe to call concurrently, but reads a snapshot that may be stale the
// instant it returns.
func listSlice(l *cacheList) []*Cache {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*Cache
	for c := l.head; c != nil; c = c.next {
		out = append(out, c)
	}
	return out
}

// childrenLen reports how many caches are in c's children list.
func (c *Cache) childrenLen() int { return len(listSlice(&c.children)) }

// rootsLen reports how many caches are in the Pool's roots list.
func (p *Pool) rootsLen() int { return len(listSlice(&p.roots)) }

// childrenContains reports whether target is currently in c's children list.
func (c *Cache) childrenContains(target *Cache) bool {
	for _, e := range listSlice(&c.children) {
		if e == target {
			return true
		}
	}
	return false
}

// testPool wraps a Pool, tracking every cache created through it so the oracles have
// the full set to sum over. The tracking slice is guarded for the concurrency tests.
type testPool struct {
	*Pool
	sem *semaphore
	tb  require.TestingT // set by tests that use makeIdle

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

// snapshot returns the DISTINCT tracked caches for the oracles to read. Caches are
// pooled (recycled on destroy), so a destroyed cache's *Cache can be handed back out by
// a later NewCache/NewChild and tracked again — the same pointer then appears twice in
// the tracking slice. The oracles sum held/inUse per cache, so a duplicated pointer
// would double-count its current incarnation; dedup by pointer here. A pointer sitting
// idle in the pool contributes 0 (drained on destroy), and a reused one contributes its
// current incarnation's counts exactly once — so the deduped sum equals Σ over live
// caches = the Resource's in-flight count.
func (tp *testPool) snapshot() []*Cache {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	seen := make(map[*Cache]struct{}, len(tp.caches))
	out := make([]*Cache, 0, len(tp.caches))
	for _, c := range tp.caches {
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

func (tp *testPool) check(t require.TestingT) {
	require.NoError(t, checkInvariants(tp.sem, tp.snapshot()))
}

func (tp *testPool) totalHeld() int       { return totalHeld(tp.snapshot()) }
func (tp *testPool) hasBorrowable() bool  { return hasBorrowable(tp.snapshot()) }
func (tp *testPool) borrowableTotal() int { return borrowableTotal(tp.snapshot()) }

// free returns the Resource capacity not currently checked out.
func (tp *testPool) free() int {
	return tp.sem.capacity - int(tp.sem.inFlight.Load())
}
