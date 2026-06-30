// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Head-to-head benchmark for [Handoff] — the unbuffered rendezvous (the
// scheduler→executor handoff in the dispatch/execution split) — against its true
// analog, an UNBUFFERED Go channel. Handoff has no outbox buffering: a push blocks
// until a receiver takes it directly, exactly like `make(chan T)` (capacity 0). So
// the apples-to-apples baseline is the unbuffered channel, not the cap-1 channel
// used in the Queue benchmarks (Queue buffers one item per sender; Handoff buffers
// none).
//
// The structures contended: Handoff's lock-free inbox stack + sender-park waiters,
// vs the channel's runtime mutex + send/recv wait queues. Producers push as fast as
// they can while an equal number of consumers receive — saturated, so the shared
// structure is the bottleneck. As concurrency climbs past GOMAXPROCS, the lock-free
// inbox handoff should hold up where the channel's single mutex serializes.

import (
	"context"
	"strconv"
	"sync"
	"testing"
)

// benchHandoffArm pushes exactly b.N values through `conc` producers to `conc`
// consumers, over a Handoff (handoff=true) or an unbuffered channel. ns/op is the
// per-rendezvous cost under that concurrency.
func benchHandoffArm(b *testing.B, conc int, handoff bool) {
	b.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	var h Handoff[int]
	var ch chan int
	if handoff {
		h.Init()
	} else {
		ch = make(chan int) // unbuffered: the Handoff analog
	}

	// Consumers: drain until the context is cancelled (after all pushes complete).
	var consumers sync.WaitGroup
	for i := 0; i < conc; i++ {
		consumers.Add(1)
		go func() {
			defer consumers.Done()
			for {
				if handoff {
					if _, err := h.PopFront(ctx); err != nil {
						return
					}
				} else {
					select {
					case <-ch:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	// Producers: push exactly b.N values total, split evenly across `conc`.
	per := b.N / conc
	rem := b.N % conc
	b.ResetTimer()
	var producers sync.WaitGroup
	for i := 0; i < conc; i++ {
		n := per
		if i < rem {
			n++
		}
		producers.Add(1)
		go func(n int) {
			defer producers.Done()
			for j := 0; j < n; j++ {
				if handoff {
					_ = h.PushBack(ctx, j)
				} else {
					ch <- j
				}
			}
		}(n)
	}
	producers.Wait()
	b.StopTimer()

	cancel()
	consumers.Wait()
}

// BenchmarkHandoffVsChan sweeps producer/consumer concurrency, interleaving the
// Handoff and unbuffered-channel arms so their ns/op sit adjacent.
func BenchmarkHandoffVsChan(b *testing.B) {
	for _, conc := range []int{1, 8, 64, 512} {
		prefix := "conc-" + strconv.Itoa(conc) + "/"
		b.Run(prefix+"handoff", func(b *testing.B) { benchHandoffArm(b, conc, true) })
		b.Run(prefix+"chan-unbuffered", func(b *testing.B) { benchHandoffArm(b, conc, false) })
	}
}
