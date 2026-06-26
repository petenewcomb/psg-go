// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import "fmt"

// This file holds test-only fixtures and oracles, compiled only under `go test`.

// semaphore is the weight-1 test Resource: a fixed capacity and an in-flight count.
type semaphore struct {
	capacity int
	inFlight int
}

func (s *semaphore) TryAcquire(n int) bool {
	if s.inFlight+n > s.capacity {
		return false
	}
	s.inFlight += n
	return true
}

func (s *semaphore) Release(n int) { s.inFlight -= n }

// CheckInvariants verifies the model-check targets across the whole pool and returns
// the first violation found, or nil (permit-core.md "Invariants"). It cross-checks
// three independent views of Σheld — the caches, the Pool's mirror, and the
// Resource's in-flight count — which must all agree and stay within capacity.
func (p *Pool) CheckInvariants() error {
	sem := p.resource.(*semaphore)
	sumHeld, sumInUse := 0, 0
	var walk func(c *Cache) error
	walk = func(c *Cache) error {
		if c.inUse < 0 || c.held < 0 || c.inUse > c.held {
			return fmt.Errorf("per-cache: 0 ≤ inUse ≤ held violated (held=%d inUse=%d)", c.held, c.inUse)
		}
		sumHeld += c.held
		sumInUse += c.inUse
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
	if sumHeld != p.checkedOut {
		return fmt.Errorf("conservation: Σheld=%d != Pool.checkedOut=%d", sumHeld, p.checkedOut)
	}
	if p.checkedOut != sem.inFlight {
		return fmt.Errorf("resource mismatch: Pool.checkedOut=%d != Resource.inFlight=%d", p.checkedOut, sem.inFlight)
	}
	if sem.inFlight > sem.capacity {
		return fmt.Errorf("conservation: inFlight=%d > capacity=%d", sem.inFlight, sem.capacity)
	}
	if sumInUse > sem.capacity {
		return fmt.Errorf("concurrency bound: ΣinUse=%d > capacity=%d", sumInUse, sem.capacity)
	}
	return nil
}

// HasBorrowable reports whether ANY cache in the forest has an idle (borrowable)
// permit, by an independent exhaustive walk. It deliberately does NOT reuse the
// production steal search (findStealVictim): the liveness assertion — Acquire must
// not block while HasBorrowable is true — is meant to cross-check that the guided,
// early-terminating descent never misses a borrowable permit, so the oracle must be
// an independent witness, not the same code.
func (p *Pool) HasBorrowable() bool {
	found := false
	var walk func(c *Cache)
	walk = func(c *Cache) {
		if found {
			return
		}
		if c.held > c.inUse {
			found = true
			return
		}
		for _, ch := range c.children {
			walk(ch)
		}
	}
	for _, r := range p.roots {
		walk(r)
	}
	return found
}
