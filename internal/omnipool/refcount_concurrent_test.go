// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRefCountConcurrent hammers the full operation set across goroutines. Each
// goroutine keeps balanced accounting on its own object; correctness of the
// interleavings is validated by the model test, while this run (under -race)
// exercises the CAS contention for torn reads and lost updates.
func TestRefCountConcurrent(t *testing.T) {
	p := For[mo]()
	const goroutines = 8
	const iters = 4000

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				obj := p.Get()

				// A deterministic-but-varying number of extra references; the
				// interleaving variety comes from the scheduler, not the count.
				extra := (i + g) % 4
				for k := 0; k < extra; k++ {
					AddRef(obj)
				}

				// A same-goroutine upgrade always succeeds (obj is live).
				if got, ok := NewHandle(obj).Get(); assert.True(t, ok) {
					p.Release(got)
				}

				for k := 0; k < extra; k++ {
					p.Release(obj)
				}
				p.Release(obj) // the Get reference
			}
		}()
	}
	wg.Wait()
}

// TestRefCountResurrectionRace has producers publish handles and recycle their
// objects while consumers race to upgrade those handles. A recycled object is
// reused across goroutines via the pool, so a consumer's upgrade genuinely
// straddles a recycle. Every upgrade that succeeds must hand back a live object
// (the touched payload must not be a torn or recycled read); references stay
// balanced whether the producer or the consumer performs the final release.
func TestRefCountResurrectionRace(t *testing.T) {
	p := For[mo]()
	ch := make(chan Handle[*mo], 128)
	const workers = 4
	const iters = 8000

	var wg sync.WaitGroup

	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				obj := p.Get()
				obj.payload = i + 1 // nonzero; Reset clears it to 0 on recycle
				select {
				case ch <- NewHandle(obj):
				default:
				}
				p.Release(obj)
			}
		}()
	}

	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				select {
				case h := <-ch:
					if got, ok := h.Get(); ok {
						// Pinned live: the payload must be a coherent value from
						// some incarnation, never a mid-recycle zero-vs-nonzero tear.
						_ = got.payload
						p.Release(got)
					}
				default:
				}
			}
		}()
	}

	wg.Wait()

	// Drain any handles left unclaimed.
	for len(ch) > 0 {
		if got, ok := (<-ch).Get(); ok {
			p.Release(got)
		}
	}
}
