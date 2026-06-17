// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// Long-term benchmark for the destination-owned outbox pool's producer-emit
// path. It measures two things across changes to the borrow/claim machinery:
//
//   - OVERHEAD: per-op cost of a push with no backpressure (the "overhead" case)
//     — catches regressions from added state-machine work.
//   - EMIT TAIL: producer emit-latency percentiles under a realistic load — the
//     tail (p99/p99.9/max) is primary per [[feedback_bench_priorities]].
//
// The regime that matters (and the one a "find the free outbox" fast path could
// help) is NOT raw overload. It is: average drain capacity EXCEEDS the steady
// production rate — so we keep up on average and empty outboxes exist — WHILE a
// heavy tail of drain durations transiently eats the headroom, backs the buffer
// up, and during recovery leaves a mix of full and empty outboxes. Only then can
// a producer refuse on a full front while a free outbox sits behind it
// ([[feedback_bench_methodology]]).
//
// So the control knob is the LOAD FACTOR = steady production rate / average drain
// capacity. Drain durations are heavy-tailed (Pareto); producers emit at a steady
// interval sized from the load factor. Deep headroom (low load factor) absorbs the
// tail and never backs up; thin headroom (load factor near 1) is the miss-prone
// regime; load factor > 1 is sustained overload (no empties — a regression/tie
// check that refuse stays O(1)).
//
// refuse/op (TryPushBack failures per emit) is the direct probe: with capacity to
// spare, a refusal IS the empty-behind-full miss the fast path targets. On a
// refusal a producer polls until an outbox frees (the real caller, workq.Post,
// parks on the queue-level "outbox freed" listener; polling is deadlock-free and a
// fixed poll granularity cancels in a relative before/after comparison).
//
// Run with a fixed sample budget for stable tail percentiles, e.g.:
//
//	go test -run '^$' -bench BenchmarkQueueEmit -benchtime 20000x ./internal/rdvq/

import (
	"context"
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pollInterval is how long a backpressured producer waits between TryPushBack
// retries. Much finer than the ms-scale heavy-tailed drain it waits on, so it
// resolves the tail without busy-spinning (which would starve drainers).
const pollInterval = 25 * time.Microsecond

// paretoSleep returns a heavy-tailed drain duration: most short, a few very long.
// alpha near 1 makes the tail extreme — rare multi-hundred-ms-to-second stalls
// that briefly drop drain capacity and build large transient backlogs, the
// condition under which a free outbox can end up behind a full front.
func paretoSleep(rng *rand.Rand) time.Duration {
	const (
		xm    = 200 * time.Microsecond // minimum
		alpha = 1.05                   // very heavy tail (closer to 1 = heavier)
		ceil  = 2 * time.Second        // truncate the worst case
	)
	u := rng.Float64() //nolint:gosec // non-cryptographic use case
	if u < 1e-12 {
		u = 1e-12
	}
	d := time.Duration(float64(xm) / math.Pow(u, 1/alpha))
	if d > ceil {
		d = ceil
	}
	return d
}

// meanDrain is the empirical mean of paretoSleep, used to size the steady producer
// interval from a load factor. Estimated once from a large fixed-seed sample so it
// stays accurate when the distribution changes (the truncated heavy-tail mean is
// not worth deriving in closed form).
var meanDrain = func() time.Duration {
	rng := newRNG(42)
	const n = 1 << 21
	var sum time.Duration
	for i := 0; i < n; i++ {
		sum += paretoSleep(rng)
	}
	return sum / n
}()

//nolint:gosec // non-cryptographic use case
func newRNG(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15)) }

// runEmitBench drives nProducers producers and nDrainers drainers against one
// Queue. Producers emit at a steady interval producerThink (0 = as fast as
// possible, for the overhead case); drainers block heavy-tailed when drainHeavy.
// b.N is the total number of emits; it reports refuse/op and emit-latency
// percentiles.
func runEmitBench(b *testing.B, nProducers, nDrainers int, producerThink time.Duration, drainHeavy bool) {
	b.Helper()
	var q Queue[int]
	q.Init()

	ctx, cancel := context.WithCancel(context.Background())

	var drainWg sync.WaitGroup
	for d := 0; d < nDrainers; d++ {
		drainWg.Add(1)
		go func(seed uint64) {
			defer drainWg.Done()
			rng := newRNG(seed)
			for {
				if _, err := q.PopFront(ctx, nil); err != nil {
					return // ctx cancelled
				}
				if drainHeavy {
					time.Sleep(paretoSleep(rng)) // heavy-tailed consume work
				}
			}
		}(uint64(d) + 1)
	}

	// refusals counts TryPushBack failures. With capacity to spare (load factor
	// < 1), a refusal IS the empty-behind-full miss the fast path targets; under
	// overload it is correct backpressure. So refuse/op below load factor 1 is the
	// direct measure of whether the optimization has anything to do.
	var refusals atomic.Int64

	latencies := make([][]time.Duration, nProducers)
	base, rem := b.N/nProducers, b.N%nProducers

	b.ResetTimer()
	var prodWg sync.WaitGroup
	for p := 0; p < nProducers; p++ {
		count := base
		if p < rem {
			count++
		}
		latencies[p] = make([]time.Duration, 0, count)
		prodWg.Add(1)
		go func(p, count int) {
			defer prodWg.Done()
			for i := 0; i < count; i++ {
				if producerThink > 0 {
					time.Sleep(producerThink) // steady production load (not timed)
				}
				t0 := time.Now()
				for !q.TryPushBack(nil, i, nil) {
					refusals.Add(1)
					time.Sleep(pollInterval) // backpressure: poll until an outbox frees
				}
				latencies[p] = append(latencies[p], time.Since(t0))
			}
		}(p, count)
	}
	prodWg.Wait()
	b.StopTimer()

	cancel()
	drainWg.Wait()

	var all []time.Duration
	for _, s := range latencies {
		all = append(all, s...)
	}
	slices.Sort(all)
	b.ReportMetric(float64(refusals.Load())/float64(b.N), "refuse/op")
	b.ReportMetric(float64(q.missRefusals.Load())/float64(b.N), "miss/op")
	reportPercentile(b, all, 0.50, "p50-us")
	reportPercentile(b, all, 0.99, "p99-us")
	reportPercentile(b, all, 0.999, "p99.9-us")
	if len(all) > 0 {
		b.ReportMetric(float64(all[len(all)-1].Microseconds()), "max-us")
	}
}

func reportPercentile(b *testing.B, sorted []time.Duration, p float64, name string) {
	b.Helper()
	if len(sorted) == 0 {
		return
	}
	idx := int(p * float64(len(sorted)-1))
	b.ReportMetric(float64(sorted[idx].Microseconds()), name)
}

// thinkForLoad sizes the steady producer interval so that nProducers emitting at
// 1/think roughly equals loadFactor × (nDrainers / meanDrain) — the average drain
// capacity. loadFactor < 1 leaves headroom; > 1 overloads.
func thinkForLoad(loadFactor float64, nProducers, nDrainers int) time.Duration {
	capacity := float64(nDrainers) / float64(meanDrain) // drains per ns
	rate := loadFactor * capacity                       // target emits per ns
	return time.Duration(float64(nProducers) / rate)
}

// BenchmarkQueueEmit sweeps the load factor against a heavy-tailed drain — the
// dimension that governs whether free outboxes ever sit behind a full front.
func BenchmarkQueueEmit(b *testing.B) {
	procs := max(4, runtime.GOMAXPROCS(0))
	for _, lf := range []float64{0.5, 0.8, 0.95, 0.99, 1.0, 1.01, 1.05, 1.5} {
		name := "heavytail/load-" + strconv.FormatFloat(lf, 'g', -1, 64)
		think := thinkForLoad(lf, procs, procs)
		b.Run(name, func(b *testing.B) {
			runEmitBench(b, procs, procs, think, true)
		})
	}
	b.Run("overhead", func(b *testing.B) {
		runEmitBench(b, procs, procs, 0, false)
	})
}
