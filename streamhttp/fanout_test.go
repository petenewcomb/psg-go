// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streamhttp_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool"
	"github.com/petenewcomb/streampool/streamhttp"
)

// TestFanOutLimiter shows the legitimate use of a Limiter: modeling a real
// external constraint on downstream work (e.g. "this backend allows ≤2
// concurrent calls"), NOT request admission. FanOut over many inputs respects
// the cap; the same Limiter value could be shared with other ops (the streamgrpc
// service, other handlers) to express a collective cap on that one dependency.
func TestFanOutLimiter(t *testing.T) {
	const limit = 2
	backend := streampool.NewSemaphore(limit)

	var cur, peak int64
	fetch := func(_ context.Context, in int) (int, error) {
		n := atomic.AddInt64(&cur, 1)
		for {
			m := atomic.LoadInt64(&peak)
			if n <= m || atomic.CompareAndSwapInt64(&peak, m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt64(&cur, -1)
		return in * in, nil
	}

	inputs := []int{1, 2, 3, 4, 5, 6, 7, 8}
	out, err := streamhttp.FanOut(context.Background(), backend, inputs, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(inputs) {
		t.Fatalf("got %d results, want %d", len(out), len(inputs))
	}
	if m := atomic.LoadInt64(&peak); m > limit {
		t.Fatalf("downstream concurrency = %d, exceeds external constraint %d", m, limit)
	} else if m < limit {
		t.Fatalf("downstream concurrency = %d; test did not exercise the limiter (want %d)", m, limit)
	}
}
