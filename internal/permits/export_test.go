// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"fmt"
	"sync/atomic"
)

// This file holds test-only fixtures and oracles, compiled only under `go test`.

// semaphore is the weight-1 test Resource: a fixed capacity and an atomic in-flight
// count (atomic so the -race concurrency harness can share it).
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

// CheckInvariants verifies the model-check targets across the whole pool and returns
// the first violation found, or nil. It takes the Pool lock and so observes a
// structurally stable forest; it is meant to be called at a quiescent point (no
// acquire mid-flight), where the three views of Σheld — the caches, and the
// Resource's in-flight count — must agree and stay within capacity.
func (p *Pool) CheckInvariants() error {
	sem := p.resource.(*semaphore)
	p.mu.Lock()
	defer p.mu.Unlock()

	var sumHeld, sumInUse uint64
	var walk func(c *Cache) error
	walk = func(c *Cache) error {
		h, u := c.counts.load()
		if u > h {
			return fmt.Errorf("per-cache: inUse > held (held=%d inUse=%d)", h, u)
		}
		sumHeld += h
		sumInUse += u
		for _, ch := range c.children {
			if err := walk(ch); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range p.roots {
		if err := walk(r); err != nil {
			return err
		}
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

// HasBorrowable reports whether ANY cache holds an idle (borrowable) permit, by an
// independent exhaustive walk — deliberately NOT the production findBorrowable — so
// the liveness assertion (Acquire must not block while HasBorrowable is true)
// cross-checks the production steal search rather than echoing it.
func (p *Pool) HasBorrowable() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return hasBorrowableWalk(p.roots)
}

func hasBorrowableWalk(caches []*Cache) bool {
	for _, c := range caches {
		if h, u := c.counts.load(); h > u {
			return true
		}
		if hasBorrowableWalk(c.children) {
			return true
		}
	}
	return false
}

// totalHeld returns Σheld across the forest — the count of permits checked out of the
// Resource (the test replacement for the dropped Pool.checkedOut mirror).
func (p *Pool) totalHeld() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	var sum uint64
	var walk func(c *Cache)
	walk = func(c *Cache) {
		h, _ := c.counts.load()
		sum += h
		for _, ch := range c.children {
			walk(ch)
		}
	}
	for _, r := range p.roots {
		walk(r)
	}
	return int(sum)
}

// held returns a cache's current held, for tests.
func (c *Cache) held() uint64 {
	h, _ := c.counts.load()
	return h
}
