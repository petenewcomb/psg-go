// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permitcore

import "fmt"

// This file holds test-only oracles, compiled only under `go test`: they verify the
// model-check targets but are not part of the package's runtime surface.

// CheckInvariants verifies the model-check targets across the whole store and
// returns the first violation found, or nil. (permit-core.md "Invariants".)
func (s *Store) CheckInvariants() error {
	sumHeld, sumInUse := 0, 0
	var walk func(p *Pool) error
	walk = func(p *Pool) error {
		if p.inUse < 0 || p.held < 0 || p.inUse > p.held {
			return fmt.Errorf("per-pool: 0 ≤ inUse ≤ held violated (held=%d inUse=%d)", p.held, p.inUse)
		}
		sumHeld += p.held
		sumInUse += p.inUse
		for _, c := range p.children {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range s.roots {
		if err := walk(r); err != nil {
			return err
		}
	}
	if sumHeld != s.checkedOut {
		return fmt.Errorf("conservation: Σheld=%d != checkedOut=%d", sumHeld, s.checkedOut)
	}
	if s.checkedOut > s.capacity {
		return fmt.Errorf("conservation: checkedOut=%d > capacity=%d", s.checkedOut, s.capacity)
	}
	if sumInUse > s.capacity {
		return fmt.Errorf("concurrency bound: ΣinUse=%d > capacity=%d", sumInUse, s.capacity)
	}
	return nil
}

// HasBorrowable reports whether ANY pool in the forest has an idle (borrowable)
// permit, by an independent exhaustive walk. It deliberately does NOT reuse the
// production steal search (findStealVictim): the liveness assertion — Acquire must
// not block while HasBorrowable is true — is meant to cross-check that the guided,
// early-terminating descent never misses a borrowable permit, so the oracle must be
// an independent witness, not the same code.
func (s *Store) HasBorrowable() bool {
	found := false
	var walk func(p *Pool)
	walk = func(p *Pool) {
		if found {
			return
		}
		if p.held > p.inUse {
			found = true
			return
		}
		for _, c := range p.children {
			walk(c)
		}
	}
	for _, r := range s.roots {
		walk(r)
	}
	return found
}
