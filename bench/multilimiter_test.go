// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package bench

import (
	"context"
	"fmt"
	"testing"

	"github.com/petenewcomb/streampool"
)

// ── Multi-limiter matrix ──────────────────────────────────────────────────────
//
// BenchmarkMultiLimiter measures what the joint (multi-limiter) machinery costs
// relative to the single-limiter path, through two differential pairs run on the
// same harness as BenchmarkDispatch:
//
//   - limits1 vs limits2: identical dispatch and body; the launcher binds one
//     semaphore vs two, EVERY semaphore at the full capacity D, so the effective
//     concurrency bound is identical and the delta is pure joint-gate overhead
//     (canonical-order acquisition, two demand queues, two releases).
//   - limits1-subwave vs limits2-subwave: the body additionally drives a one-task
//     sub-wave to completion (CloseAndSkimAll), which lends the body's whole
//     permit set across the drive and reacquires it afterwards — the suspend/
//     reclaim brackets. The delta between the pairs isolates the joint reclaim
//     (per-hold suspend scoping, the lend/withdraw rule, the lowest-rank-first
//     fixpoint) against the single-hold reclaim; the delta between subwave and
//     non-subwave variants prices the bracket machinery itself.
//
// The interesting numbers are, as ever, the p99/p99.9 dispatch and e2e tails
// under the heavytail workload and the overload regimes: that is where misses,
// postpones, and reclaim churn actually happen (an uncontended joint gate is
// just two fast-path acquires). The withdraw rule's fairness cost — a reclaim
// re-registering at the back of a pool's queue — would surface here as an e2e
// tail gap between limits2-subwave and limits1-subwave that grows with the
// P:D overload ratio (see weighted-acquisition.md "The joint reclaim").
type multiLimiterDispatcher struct {
	wave     streampool.Wave
	launcher streampool.Launcher[func()]
	inner    streampool.Launcher[func()] // unbound; In(&sub) per body for subwave variants
	limiters int
	subwave  bool
}

func (d *multiLimiterDispatcher) name() string {
	sw := ""
	if d.subwave {
		sw = "-subwave"
	}
	return fmt.Sprintf("limits%d%s", d.limiters, sw)
}

func (d *multiLimiterDispatcher) start(capacity int) {
	body := func(ctx context.Context, task func(), _ error) error {
		if !d.subwave {
			task()
			return nil
		}
		// Drive the task through a one-shot sub-wave: the dispatch into it and the
		// CloseAndSkimAll drain both run under the enclosing body's held permit
		// set, engaging the suspend/reclaim brackets around each park.
		var sub streampool.Wave
		if err := d.inner.In(&sub).Submit(ctx, task); err != nil {
			return err
		}
		return sub.CloseAndSkimAll(ctx)
	}
	launcher := streampool.NewFnLauncher(body)
	limits := make([]streampool.Limiter, d.limiters)
	for i := range limits {
		limits[i] = streampool.NewSemaphore(capacity)
	}
	d.launcher = launcher.WithLimits(limits...).In(&d.wave)
	d.inner = streampool.NewFnLauncher(
		func(_ context.Context, task func(), _ error) error {
			task()
			return nil
		})
}

func (d *multiLimiterDispatcher) submit(ctx context.Context, task func()) {
	_ = d.launcher.Submit(ctx, task)
}

func (d *multiLimiterDispatcher) drain() {
	_ = d.wave.CloseAndSkimAll(context.Background())
}

func (d *multiLimiterDispatcher) stop() {}

// multiLimiterSUTs is the differential lineup (see the type comment).
var multiLimiterSUTs = []struct {
	label    string
	limiters int
	subwave  bool
}{
	{"limits1", 1, false},
	{"limits2", 2, false},
	{"limits1-subwave", 1, true},
	{"limits2-subwave", 2, true},
}

// BenchmarkMultiLimiter is the joint-machinery cost matrix: variant × workload ×
// P:D regime, all streampool, differential by construction.
func BenchmarkMultiLimiter(b *testing.B) {
	for _, wl := range workloads {
		for _, p := range pdSweep() {
			for _, sut := range multiLimiterSUTs {
				name := fmt.Sprintf("workload=%s/regime=%s/P=%d/D=%d/sut=%s",
					wl.name, p.regime, p.producers, p.capacity, sut.label)
				b.Run(name, func(b *testing.B) {
					runComparison(b, func() dispatcher {
						return &multiLimiterDispatcher{limiters: sut.limiters, subwave: sut.subwave}
					}, wl, p)
				})
			}
		}
	}
}
