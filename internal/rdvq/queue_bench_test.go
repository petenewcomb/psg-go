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
// On a refusal a producer PARKS on the queue-level "outbox freed" listener and
// retries once per drain wakeup — the real caller's (workq.Post) postpone. This
// is what gives a miss a real opportunity cost: a free outbox behind a full front
// can be repeatedly missed by woken items (and an item starved) over wakeup
// cycles, instead of a tight spin that brute-forces every empty into use and
// erases the cost. What matters is per-item latency from first attempt to posted.
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

	// drainDurs[d] records the ACHIEVED consume duration per drain (time.Sleep is
	// a lower bound; under load a woken goroutine overshoots), so we can verify the
	// realized distribution and the actual load factor rather than trust the
	// nominal one. Each drainer owns its slice; the main goroutine reads after Wait.
	// A drain frees a slot; freed.Broadcast wakes parked producers to retry (the
	// real caller is re-driven by the "outbox freed" wakeup). A cond broadcast —
	// not the one-shot rdvq Listeners — because producers persistently park-retry:
	// a missed retry must re-park without depleting a token or leaking a listener.
	var mu sync.Mutex
	freed := sync.NewCond(&mu)

	drainDurs := make([][]time.Duration, nDrainers)
	var drainWg sync.WaitGroup
	for d := 0; d < nDrainers; d++ {
		drainWg.Add(1)
		go func(d int, seed uint64) {
			defer drainWg.Done()
			rng := newRNG(seed)
			for {
				if _, err := q.PopFront(ctx); err != nil {
					return // ctx cancelled
				}
				mu.Lock()
				freed.Signal() // a slot just freed — wake ONE parked producer (as outboxFreed does)
				mu.Unlock()
				if drainHeavy {
					t0 := time.Now()
					time.Sleep(paretoSleep(rng)) // heavy-tailed consume work
					drainDurs[d] = append(drainDurs[d], time.Since(t0))
				}
			}
		}(d, uint64(d)+1)
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
			// On a miss, park until a drain broadcasts "freed", then retry — the
			// real caller's postpone/re-drive. Items genuinely compete for each
			// freed slot (a missed retry re-parks), rather than a tight spin that
			// brute-forces every empty into use and erases the opportunity cost.
			for i := 0; i < count; i++ {
				if producerThink > 0 {
					time.Sleep(producerThink) // steady production load (not timed)
				}
				t0 := time.Now()
				for !q.TryPushBack(i, nil) {
					refusals.Add(1)
					mu.Lock()
					if q.TryPushBack(i, nil) { // retry under lock to close the race vs the signal
						mu.Unlock()
						break
					}
					freed.Wait() // park until a drain frees a slot
					mu.Unlock()
				}
				latencies[p] = append(latencies[p], time.Since(t0))
			}
		}(p, count)
	}
	prodWg.Wait()
	b.StopTimer()

	cancel()
	drainWg.Wait()

	b.ReportMetric(float64(refusals.Load())/float64(b.N), "refuse/op")
	reportEmitMetrics(b, latencies, drainDurs, nProducers, nDrainers, producerThink)
}

// runChanEmitBench is the head-to-head baseline for runEmitBench: the same
// producer/drainer/heavy-tail model driven against a plain buffered channel whose
// capacity equals the number of PRODUCERS — the closest naive equivalent of
// rdvq's "buffer length 1 per sender". Producers use a blocking send (the
// idiomatic channel backpressure); the measured emit latency is t0→send-accepted,
// the same "time to posted" runEmitBench measures, so the tails are directly
// comparable. There is no refuse/op (a blocking send never refuses); the cost of
// backpressure shows up entirely in the emit tail.
func runChanEmitBench(b *testing.B, nProducers, nDrainers int, producerThink time.Duration, drainHeavy bool) {
	b.Helper()
	ch := make(chan int, nProducers) // buffer = number of producers

	drainDurs := make([][]time.Duration, nDrainers)
	var drainWg sync.WaitGroup
	for d := 0; d < nDrainers; d++ {
		drainWg.Add(1)
		go func(d int, seed uint64) {
			defer drainWg.Done()
			rng := newRNG(seed)
			for range ch { // ranges until the channel is closed after producers finish
				if drainHeavy {
					t0 := time.Now()
					time.Sleep(paretoSleep(rng)) // heavy-tailed consume work
					drainDurs[d] = append(drainDurs[d], time.Since(t0))
				}
			}
		}(d, uint64(d)+1)
	}

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
				ch <- i // blocking send: blocks once the shared buffer fills (backpressure)
				latencies[p] = append(latencies[p], time.Since(t0))
			}
		}(p, count)
	}
	prodWg.Wait()
	b.StopTimer()

	close(ch)
	drainWg.Wait()

	reportEmitMetrics(b, latencies, drainDurs, nProducers, nDrainers, producerThink)
}

// runChanNBEmitBench is the APPLES-TO-APPLES baseline for runEmitBench: a plain
// buffered channel (capacity = #producers) driven with a NON-BLOCKING send
// (select/default) and the IDENTICAL cond-retry postpone harness as the rdvq arm
// — on a refused send the producer parks on `freed` and retries when a drain
// signals, exactly as runEmitBench does on a refused TryPushBack. Both arms are
// non-blocking and pay the same postpone-coordination cost (the shared mutex +
// cond, which models rdvq's outboxFreed re-drive), so the comparison isolates the
// data structure: a channel's non-blocking send vs rdvq's two-tier outbox. (The
// blocking runChanEmitBench bypasses this coordination via the runtime's direct
// handoff — a lower bound psg cannot use, since a producer must not park.)
func runChanNBEmitBench(b *testing.B, nProducers, nDrainers int, producerThink time.Duration, drainHeavy bool) {
	b.Helper()
	ch := make(chan int, nProducers) // buffer = number of producers
	done := make(chan struct{})

	var mu sync.Mutex
	freed := sync.NewCond(&mu)

	drainDurs := make([][]time.Duration, nDrainers)
	var drainWg sync.WaitGroup
	for d := 0; d < nDrainers; d++ {
		drainWg.Add(1)
		go func(d int, seed uint64) {
			defer drainWg.Done()
			rng := newRNG(seed)
			for {
				select {
				case <-ch:
				case <-done:
					return
				}
				mu.Lock()
				freed.Signal() // a slot just freed — wake ONE parked producer
				mu.Unlock()
				if drainHeavy {
					t0 := time.Now()
					time.Sleep(paretoSleep(rng))
					drainDurs[d] = append(drainDurs[d], time.Since(t0))
				}
			}
		}(d, uint64(d)+1)
	}

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
					time.Sleep(producerThink)
				}
				t0 := time.Now()
				sent := false
				for !sent {
					select {
					case ch <- i:
						sent = true
					default:
						refusals.Add(1)
						mu.Lock()
						select {
						case ch <- i: // retry under lock to close the race vs the signal
							sent = true
						default:
						}
						if !sent {
							freed.Wait() // park until a drain frees a slot
						}
						mu.Unlock()
					}
				}
				latencies[p] = append(latencies[p], time.Since(t0))
			}
		}(p, count)
	}
	prodWg.Wait()
	b.StopTimer()

	close(done)
	drainWg.Wait()

	b.ReportMetric(float64(refusals.Load())/float64(b.N), "refuse/op")
	reportEmitMetrics(b, latencies, drainDurs, nProducers, nDrainers, producerThink)
}

// reportEmitMetrics reports the emit-latency percentiles (tail is primary per
// [[feedback_bench_priorities]]) and the achieved drain-duration distribution +
// implied actual load, shared by the rdvq and buffered-channel harnesses. The
// drain distribution checks that time.Sleep overshoot has not moved the regime
// off its nominal load-factor label.
func reportEmitMetrics(
	b *testing.B,
	latencies, drainDurs [][]time.Duration,
	nProducers, nDrainers int,
	producerThink time.Duration,
) {
	b.Helper()
	var all []time.Duration
	for _, s := range latencies {
		all = append(all, s...)
	}
	slices.Sort(all)
	reportPercentile(b, all, 0.50, "emit-p50-us")
	reportPercentile(b, all, 0.99, "emit-p99-us")
	reportPercentile(b, all, 0.999, "emit-p99.9-us")
	if len(all) > 0 {
		b.ReportMetric(float64(all[len(all)-1].Microseconds()), "emit-max-us")
	}

	var drains []time.Duration
	for _, s := range drainDurs {
		drains = append(drains, s...)
	}
	slices.Sort(drains)
	if len(drains) > 0 {
		var sum time.Duration
		for _, d := range drains {
			sum += d
		}
		achievedMean := sum / time.Duration(len(drains))
		b.ReportMetric(float64(achievedMean.Microseconds()), "drainMean-us")
		reportPercentile(b, drains, 0.50, "drain-p50-us")
		reportPercentile(b, drains, 0.99, "drain-p99-us")
		if producerThink > 0 {
			// actual load = production rate / achieved drain capacity
			//             = (nProducers/producerThink) / (nDrainers/achievedMean)
			actualLoad := (float64(nProducers) * float64(achievedMean)) /
				(float64(nDrainers) * float64(producerThink))
			b.ReportMetric(actualLoad, "load-actual")
		}
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

// BenchmarkOutboxHintCycle drives the steady outbox+hint path single-threaded
// with no waiting receiver: every push buffers in an outbox and every pop drains
// it, so each iteration mints a hint (markEmpty → emptyOutboxes.PushBack) and
// claims one (TryPushBack hint-claim). It isolates the hint path from harness
// noise to confirm the generation-stamped outboxHint adds no per-op allocation —
// nbcq pools the stored value (valuePool), so steady state is 0 allocs/op.
func BenchmarkOutboxHintCycle(b *testing.B) {
	var q Queue[int]
	q.Init()
	q.TryPushBack(-1, nil) // warm the pools (outbox, node, value)
	if _, ok := q.TryPopFront(); !ok {
		b.Fatal("warmup drain failed")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !q.TryPushBack(i, nil) {
			b.Fatal("push refused")
		}
		if _, ok := q.TryPopFront(); !ok {
			b.Fatal("drain failed")
		}
	}
}

// BenchmarkChanCycle is the buffered-channel analog of BenchmarkOutboxHintCycle:
// the zero-contention, no-consumer base cost of a single push+drain. Comparing
// the two ns/op isolates rdvq's per-op machinery (two-tier inbox/outbox + hint
// mint/claim + reclaim probe) against a plain channel send/recv, with no
// goroutines, backpressure, or refuse dynamics in play.
func BenchmarkChanCycle(b *testing.B) {
	ch := make(chan int, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch <- i
		<-ch
	}
}

// BenchmarkQueueEmit sweeps the load factor against a heavy-tailed drain — the
// dimension that governs whether free outboxes ever sit behind a full front.
func BenchmarkQueueEmit(b *testing.B) {
	b.Logf("intended drain: Pareto(xm=200us, alpha=1.05, cap=2s); sampled mean=%v", meanDrain)
	procs := max(4, runtime.GOMAXPROCS(0))
	for _, lf := range []float64{0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 1.0, 1.5} {
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

// BenchmarkEmitVsChan runs rdvq head-to-head against a plain buffered channel
// (capacity = number of producers) under SATURATED production — no producer
// think time — across a concurrency sweep decoupled from GOMAXPROCS. The point
// is to contend the data structures: producers push as fast as they can while
// nDrainers consume, so the shared structure (rdvq's lock-free outbox pool vs the
// channel's mutex + wait queue) is the bottleneck, not an artificial pacing
// delay. The drain side sets the regime:
//
//   - fastdrain: drainers consume in a tight loop (no sleep). Production and
//     consumption both hammer the structure — raw contention / throughput, and
//     the direct-handoff path (a receiver is usually waiting). This is where
//     rdvq's lock-free design should pull ahead of the channel's mutex as the
//     producer count climbs past cores.
//   - heavydrain: drainers block heavy-tailed (Pareto), so producers saturate
//     into a slow consumer — sustained backpressure: emit latency is dominated by
//     waiting for a slot, and the structures' wait/wake fairness shows in the tail.
//
// Three arms per (conc, drain), interleaved so throughput (ns/op) and emit tails
// sit adjacent:
//
//   - rdvq:      TryPushBack + the cond-retry postpone harness.
//   - chan-nb:   the APPLES-TO-APPLES baseline — a non-blocking channel send
//     (select/default) with the SAME cond-retry postpone harness. Both arms are
//     non-blocking and pay the same postpone-coordination cost (shared mutex/cond
//     modelling rdvq's outboxFreed re-drive), so this isolates the data structure.
//   - chan-block: blocking send — a LOWER BOUND that bypasses postpone coordination
//     via the runtime's direct handoff. psg cannot use it (a producer must not
//     park), so it is a reference, not a drop-in alternative.
//
// (The think-time/load-factor miss regime — a free outbox behind a full one — is
// covered by BenchmarkQueueEmit, the recovery regression guard; saturation skips it.)
func BenchmarkEmitVsChan(b *testing.B) {
	b.Logf("intended drain: Pareto(xm=200us, alpha=1.05, cap=2s); sampled mean=%v", meanDrain)
	for _, conc := range []int{8, 64, 512} {
		conc := conc
		for _, drain := range []struct {
			name  string
			heavy bool
		}{{"fastdrain", false}, {"heavydrain", true}} {
			heavy := drain.heavy
			prefix := "conc-" + strconv.Itoa(conc) + "/" + drain.name + "/"
			b.Run(prefix+"rdvq", func(b *testing.B) {
				runEmitBench(b, conc, conc, 0, heavy) // think=0: saturate
			})
			b.Run(prefix+"chan-nb", func(b *testing.B) {
				runChanNBEmitBench(b, conc, conc, 0, heavy)
			})
			b.Run(prefix+"chan-block", func(b *testing.B) {
				runChanEmitBench(b, conc, conc, 0, heavy)
			})
		}
	}
}
