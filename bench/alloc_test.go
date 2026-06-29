// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package bench

import (
	"context"
	"fmt"
	"testing"

	"github.com/petenewcomb/streampool"
)

// BenchmarkSPSubmitSkim isolates streampool's per-dispatch allocations on the
// single-threaded Submit+Skim path, with and without a limiter. It reuses one
// no-op body closure, so the only allocations measured are the framework's — not
// the per-task closure every system pays. Compare cap=0 (unlimited) against cap>0
// to attribute any allocation to the limiter gate.
func BenchmarkSPSubmitSkim(b *testing.B) {
	ctx := context.Background()
	noop := func() {}
	for _, capacity := range []int{0, 1, 8} {
		b.Run(fmt.Sprintf("cap=%d", capacity), func(b *testing.B) {
			var w streampool.Wave
			var opts []streampool.OpOption
			if capacity > 0 {
				opts = append(opts, streampool.WithLimits(streampool.NewSemaphore(capacity)))
			}
			l := streampool.NewFnLauncher(
				func(_ context.Context, t func(), _ error) error { t(); return nil },
				opts...).In(&w)
			for range 1000 { // warm the pools
				_ = l.Submit(ctx, noop)
				_ = w.Skim(ctx)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = l.Submit(ctx, noop)
				_ = w.Skim(ctx)
			}
		})
	}
}
