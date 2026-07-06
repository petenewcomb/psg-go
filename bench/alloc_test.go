// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package bench

import (
	"context"
	"fmt"
	"testing"

	"github.com/petenewcomb/streampool"
)

// BenchmarkSPDispatch isolates streampool's per-dispatch allocations on the
// single-threaded submit path used by the comparison harness's streampool arm: a
// Launcher whose body runs the submitted closure (on the executor), with and
// without a limiter. It reuses one no-op body closure, so the only allocations
// measured are the framework's — not the per-task closure every system pays.
//
// No explicit Skim: this launcher body returns nil (it routes nothing downstream),
// so there is no skim result to drain — a body completes and releases its limiter
// permit on its own. Submit self-paces on that permit via its backpressure skim;
// CloseAndSkimAll drains any in-flight tail at the end. Compare cap=0 (unlimited)
// against cap>0 to attribute allocations to the limiter + executor-handoff path.
func BenchmarkSPDispatch(b *testing.B) {
	ctx := context.Background()
	noop := func() {}
	for _, capacity := range []int{0, 8} {
		b.Run(fmt.Sprintf("cap=%d", capacity), func(b *testing.B) {
			var w streampool.Wave
			launcher := streampool.NewFnLauncher(
				func(_ context.Context, t func(), _ error) error { t(); return nil })
			if capacity > 0 {
				launcher = launcher.WithLimits(streampool.NewSemaphore(capacity))
			}
			l := launcher.In(&w)
			for range 1000 { // warm the pools
				_ = l.Submit(ctx, noop)
			}
			_ = w.CloseAndSkimAll(ctx)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = l.Submit(ctx, noop)
			}
			b.StopTimer()
			_ = w.CloseAndSkimAll(ctx)
		})
	}
}
