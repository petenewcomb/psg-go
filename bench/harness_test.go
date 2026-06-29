// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package bench

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/influxdata/tdigest"
)

// workload describes the per-task blocking-I/O cost. Durations are drawn from a
// lognormal distribution (median * exp(sigma*N(0,1))), the standard model for a
// heavy right tail: most tasks are quick, a few are very slow. cap clamps the
// worst case so a single draw can't stall the whole window.
type workload struct {
	name   string
	median time.Duration
	sigma  float64 // tail heaviness: ~0.5 light, ~2.0 heavy
	cap    time.Duration
}

func (w workload) sample(rng *rand.Rand) time.Duration {
	d := float64(w.median) * math.Exp(w.sigma*rng.NormFloat64())
	if d > float64(w.cap) {
		d = float64(w.cap)
	}
	if d < 0 {
		d = 0
	}
	return time.Duration(d)
}

// dispatcher abstracts a system-under-test. The harness drives every framework
// (and baseline) through this one interface so the comparison is apples-to-apples:
// the producer stamps the enqueue time, submit hands the body off (blocking under
// backpressure — that wait IS part of the measured latency), the body runs the
// heavy-tailed sleep, and drain waits for everything in flight to finish.
type dispatcher interface {
	// name is the label reported in benchmark output.
	name() string
	// start brings the system up with the given concurrency capacity (the max
	// number of task bodies allowed to run at once; ignored by unbounded).
	start(capacity int)
	// submit schedules task for execution, blocking if the system applies
	// backpressure at capacity. task runs the body exactly once.
	submit(ctx context.Context, task func())
	// drain blocks until all submitted tasks have completed.
	drain()
	// stop tears the system down (after drain).
	stop()
}

// recorder collects per-task latencies into sharded t-digests (streaming
// quantiles, bounded memory). Tasks complete on many goroutines, so adds shard by
// an atomic counter; each shard's digest is mutex-guarded. The measured value
// (time.Since) is captured before the lock, so lock contention never inflates it.
type recorder struct {
	shards    []*recShard
	counter   atomic.Uint64
	completed atomic.Int64
}

type recShard struct {
	mu       sync.Mutex
	dispatch *tdigest.TDigest // enqueue -> body start (queue + backpressure wait)
	e2e      *tdigest.TDigest // enqueue -> body done (end-to-end)
}

func newRecorder() *recorder {
	n := runtime.GOMAXPROCS(-1) * 4
	if n < 4 {
		n = 4
	}
	r := &recorder{shards: make([]*recShard, n)}
	for i := range r.shards {
		r.shards[i] = &recShard{dispatch: tdigest.New(), e2e: tdigest.New()}
	}
	return r
}

func (r *recorder) add(dispatchLat, e2eLat time.Duration) {
	s := r.shards[r.counter.Add(1)%uint64(len(r.shards))]
	s.mu.Lock()
	s.dispatch.Add(dispatchLat.Seconds()*1e6, 1) // microseconds
	s.e2e.Add(e2eLat.Seconds()*1e6, 1)
	s.mu.Unlock()
	r.completed.Add(1)
}

// merge folds all shards into two digests for quantile extraction.
func (r *recorder) merge() (dispatch, e2e *tdigest.TDigest) {
	dispatch, e2e = tdigest.New(), tdigest.New()
	for _, s := range r.shards {
		s.mu.Lock()
		dispatch.Merge(s.dispatch)
		e2e.Merge(s.e2e)
		s.mu.Unlock()
	}
	return dispatch, e2e
}

// pd is one point in the P:D sweep: P producer goroutines offering load against a
// system whose body concurrency is capped at D.
type pd struct {
	regime    string
	producers int
	capacity  int
}

const (
	warmupDuration  = 300 * time.Millisecond
	measureDuration = 1500 * time.Millisecond
)

// runComparison runs one (dispatcher, workload, pd) point: warm up, then drive P
// producers for a fixed window, recording dispatch + end-to-end latency, and
// report the tail metrics plus throughput, allocations, and peak goroutines.
func runComparison(b *testing.B, makeDisp func() dispatcher, wl workload, p pd) {
	b.Helper()
	ctx := context.Background()

	run := func(d time.Duration, rec *recorder) {
		disp := makeDisp()
		disp.start(p.capacity)
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for i := 0; i < p.producers; i++ {
			wg.Add(1)
			go func(seed int64) {
				defer wg.Done()
				rng := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic, non-crypto
				for {
					select {
					case <-stop:
						return
					default:
					}
					enqueue := time.Now()
					sleep := wl.sample(rng)
					disp.submit(ctx, func() {
						start := time.Now()
						time.Sleep(sleep)
						done := time.Now()
						if rec != nil {
							rec.add(start.Sub(enqueue), done.Sub(enqueue))
						}
					})
				}
			}(int64(i) + 1)
		}
		time.Sleep(d)
		close(stop)
		wg.Wait()
		disp.drain()
		disp.stop()
	}

	// Warm up (pools, GC, allocator) without recording.
	run(warmupDuration, nil)

	// Measured window.
	rec := newRecorder()
	peak := newGoroutinePeak()
	var ms0, ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	b.ResetTimer()
	start := time.Now()
	run(measureDuration, rec)
	elapsed := time.Since(start)
	b.StopTimer()
	runtime.ReadMemStats(&ms1)
	peak.stop()

	dispatch, e2e := rec.merge()
	n := rec.completed.Load()
	if n == 0 {
		b.Fatal("no tasks completed")
	}
	tasks := float64(n)

	b.ReportMetric(tasks/elapsed.Seconds(), "tasks/sec")
	b.ReportMetric(dispatch.Quantile(0.50), "p50-dispatch-us")
	b.ReportMetric(dispatch.Quantile(0.99), "p99-dispatch-us")
	b.ReportMetric(dispatch.Quantile(0.999), "p99.9-dispatch-us")
	b.ReportMetric(e2e.Quantile(0.50), "p50-e2e-us")
	b.ReportMetric(e2e.Quantile(0.99), "p99-e2e-us")
	b.ReportMetric(e2e.Quantile(0.999), "p99.9-e2e-us")
	b.ReportMetric(float64(ms1.Mallocs-ms0.Mallocs)/tasks, "allocs/task")
	b.ReportMetric(float64(ms1.TotalAlloc-ms0.TotalAlloc)/tasks, "B/task")
	b.ReportMetric(float64(peak.max()), "peak-goroutines")
}

// goroutinePeak samples runtime.NumGoroutine() over the window and tracks the max,
// the scalability signal that separates bounded pools from unbounded goroutines.
type goroutinePeak struct {
	peak atomic.Int64
	done chan struct{}
	wg   sync.WaitGroup
}

func newGoroutinePeak() *goroutinePeak {
	g := &goroutinePeak{done: make(chan struct{})}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		t := time.NewTicker(5 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-g.done:
				return
			case <-t.C:
				if n := int64(runtime.NumGoroutine()); n > g.peak.Load() {
					g.peak.Store(n)
				}
			}
		}
	}()
	return g
}

func (g *goroutinePeak) stop()      { close(g.done); g.wg.Wait() }
func (g *goroutinePeak) max() int64 { return g.peak.Load() }

// workloads is the blocking-I/O matrix. Heavy tail is the headline; the light tail
// is a control showing the systems converge when there is no tail to absorb.
var workloads = []workload{
	{name: "heavytail", median: 100 * time.Microsecond, sigma: 2.0, cap: 50 * time.Millisecond},
	{name: "lighttail", median: 100 * time.Microsecond, sigma: 0.5, cap: 50 * time.Millisecond},
}

// pdSweep covers underload -> balanced -> overload relative to GOMAXPROCS.
func pdSweep() []pd {
	c := runtime.GOMAXPROCS(-1)
	return []pd{
		{regime: "underload", producers: max(1, c/2), capacity: c},
		{regime: "balanced", producers: c, capacity: c},
		{regime: "overload", producers: 4 * c, capacity: c},
		{regime: "heavy-overload", producers: 16 * c, capacity: c},
	}
}

// dispatchers is the lineup. Baselines first; external frameworks register here.
var dispatchers = []struct {
	label string
	make  func() dispatcher
}{
	{"unbounded", func() dispatcher { return &unboundedDispatcher{} }},
	{"chan-semaphore", func() dispatcher { return &semaphoreDispatcher{} }},
	{"naive-pool", func() dispatcher { return &naivePoolDispatcher{} }},
	{"streampool", func() dispatcher { return &streampoolDispatcher{} }},
}

// BenchmarkDispatch is the head-to-head matrix: dispatcher × workload × P:D regime.
func BenchmarkDispatch(b *testing.B) {
	for _, wl := range workloads {
		for _, p := range pdSweep() {
			for _, sut := range dispatchers {
				name := fmt.Sprintf("workload=%s/regime=%s/P=%d/D=%d/sut=%s",
					wl.name, p.regime, p.producers, p.capacity, sut.label)
				b.Run(name, func(b *testing.B) {
					runComparison(b, sut.make, wl, p)
				})
			}
		}
	}
}
