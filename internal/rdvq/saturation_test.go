// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTryPushBackSaturation hammers TryPushBack + PopFront with no producer
// pacing, the saturation regime that exposes reclamation races. Producers retry
// refusals with Gosched (the postpone re-drive). It asserts every value is
// received exactly once and must not hang.
//
// Regression guard for the stale-hint reclamation bug: emptyOutboxes hints must
// be generation-stamped and claimed at the minted generation. With a bare-pointer
// hint claimed at the current generation, a hint outliving its incarnation (the
// outbox reclaimed to the pool and reused) would claim and fill a pooled outbox,
// putting it in two places at once — surfacing here as a never-drained channel
// inside the "non-blocking" TryPushBack and an eventual deadlock. Run with -race.
func TestTryPushBackSaturation(t *testing.T) {
	chk := require.New(t)
	const (
		nProd   = 8
		nDrain  = 8
		perProd = 40000
		total   = nProd * perProd
	)
	var q Queue[int]
	q.Init()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make([]atomic.Int32, total)
	var count atomic.Int64

	var drainWg sync.WaitGroup
	for d := 0; d < nDrain; d++ {
		drainWg.Add(1)
		go func() {
			defer drainWg.Done()
			for {
				v, err := q.PopFront(ctx, nil)
				if err != nil {
					return
				}
				if received[v].Add(1) != 1 {
					t.Errorf("value %d received more than once", v)
				}
				count.Add(1)
			}
		}()
	}

	var prodWg sync.WaitGroup
	for p := 0; p < nProd; p++ {
		prodWg.Add(1)
		go func(base int) {
			defer prodWg.Done()
			for i := 0; i < perProd; i++ {
				for !q.TryPushBack(nil, base+i, nil) {
					runtime.Gosched() // refuse → re-drive
				}
			}
		}(p * perProd)
	}
	prodWg.Wait()
	for count.Load() < total {
		runtime.Gosched()
	}
	cancel()
	drainWg.Wait()

	chk.Equal(int64(total), count.Load(), "every value received exactly once")
}
