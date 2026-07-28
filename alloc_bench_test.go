// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/petenewcomb/streampool"
)

// BenchmarkLauncherSkim is the synchronous dispatch+skim hot path, for -benchmem /
// -memprofile analysis of per-dispatch allocations.
func BenchmarkLauncherSkim(b *testing.B) {
	ctx := context.Background()
	w := streampool.NewWave()
	var sum int64
	collector := streampool.NewSkimmer[int](allocAddHandler{&sum})
	fetcher := streampool.NewLauncher[int](allocForwardHandler{collector}).In(w)
	// warm the pools
	for i := 0; i < 1000; i++ {
		_ = fetcher.Submit(ctx, 1)
		_ = w.Skim(ctx)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := fetcher.Submit(ctx, 1); err != nil {
			b.Fatal(err)
		}
		if err := w.Skim(ctx); err != nil {
			b.Fatal(err)
		}
	}
	_ = atomic.LoadInt64(&sum)
}
