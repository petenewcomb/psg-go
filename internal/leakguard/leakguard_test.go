// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package leakguard_test provides comprehensive tests proving the claims in the
// leakguard package documentation:
//
//  1. (Net)zero-allocation: After pool warmup, New/Close/Dup have 0 allocs
//  2. Idempotent Close: Multiple Close() calls are safe and efficient
//  3. Thread-safe: Concurrent Close() from multiple goroutines is safe
//  4. Atomic CAS protection: Copied handles share state, only one closes
//  5. Leak detection: Finalizers detect and report unclosed handles
//  6. Stack traces: Creation location captured for leak reports
//  7. Dup independence: Dup'd handles have independent close state
//  8. Minimal overhead: ~500ns per New/Close cycle, ~140ns when concurrent
//
// Benchmark results demonstrate:
//   - BenchmarkNewClose: 1 alloc (pool overhead, amortizes to zero)
//   - BenchmarkDup: 0 allocs (zero-allocation duplication)
//   - BenchmarkGet: 0 allocs, <1ns (just an atomic load)
//   - BenchmarkClose: 0 allocs in fast path
//   - BenchmarkConcurrentClose: Good scaling under concurrent load
//
// IMPORTANT: These tests modify global package state (leakReporter, stackDepth, logLevel).
// DO NOT use t.Parallel() in any tests in this file, as it would cause races on the
// global state even with save/restore patterns.
package leakguard

import (
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/stretchr/testify/require"
)

// Test resource that tracks cleanup calls
type testResource struct {
	id         int
	closeCount atomic.Int32
	dupCount   atomic.Int32
}

func (r *testResource) String() string {
	return fmt.Sprintf("testResource(%d)", r.id)
}

func (r *testResource) Init() {}

func (r *testResource) Reset() {
	r.id = 0
	r.closeCount.Store(0)
	r.dupCount.Store(0)
}

// Trait for non-duplicable resources (used in tests, no pooling)
type testTrait struct{}

func (testTrait) Close(r *testResource) {
	r.closeCount.Add(1)
}

// DupTrait for duplicable resources (reference counted, used in tests, no pooling)
type testDupTrait struct{}

func (testDupTrait) Close(r *testResource) {
	r.closeCount.Add(1)
}

func (testDupTrait) Dup(r *testResource) (*testResource, error) {
	r.dupCount.Add(1)
	// Return same pointer (simulating refcount increment)
	return r, nil
}

var testResourcePool = omnipool.For[testResource]()

// Benchmark traits that pool resources to prove zero-allocation after warmup
type benchTrait struct{}

func (benchTrait) Close(r *testResource) {
	r.closeCount.Add(1)
	// Pool the resource
	testResourcePool.Put(r)
}

type benchDupTrait struct{}

func (benchDupTrait) Close(r *testResource) {
	r.closeCount.Add(1)
	// Pool the resource
	testResourcePool.Put(r)
}

func (benchDupTrait) Dup(r *testResource) (*testResource, error) {
	r.dupCount.Add(1)
	// Return same pointer (simulating refcount increment)
	return r, nil
}

func TestBasicNewClose(t *testing.T) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	SetLeakReporter(PanicLeak)
	SetStackDepth(5)
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	r := &testResource{id: 1}
	h := New[testResource, testTrait](r)

	require.NotNil(t, h.Get(), "Get() returned nil for new handle")
	require.Equal(t, r, h.Get(), "Get() returned wrong resource")

	h.Close()

	require.Nil(t, h.Get(), "Get() should return nil after Close()")
	require.Equal(t, int32(1), r.closeCount.Load(), "Close() should be called exactly once")
}

func TestIdempotentClose(t *testing.T) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	SetLeakReporter(PanicLeak)
	SetStackDepth(5)
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	r := &testResource{id: 1}
	h := New[testResource, testTrait](r)

	// Close multiple times - should be safe and idempotent
	h.Close()
	h.Close()
	h.Close()

	// Close() should only be called once on the resource
	require.Equal(t, int32(1), r.closeCount.Load(), "Close() should be idempotent")
}

func TestCopiedHandleClose(t *testing.T) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	SetLeakReporter(PanicLeak)
	SetStackDepth(5)
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	r := &testResource{id: 1}
	h1 := New[testResource, testTrait](r)
	h2 := h1 // Copy the handle

	// Both should access the same resource
	require.Equal(t, h1.Get(), h2.Get(), "Copied handles should share resource")

	// Close one
	h1.Close()

	// Both should now return nil
	require.Nil(t, h1.Get(), "h1.Get() should return nil after close")
	require.Nil(t, h2.Get(), "h2.Get() should return nil after h1 closed")

	// Closing the other should be a no-op
	h2.Close()

	// Close() should only be called once on the resource (copies share state)
	require.Equal(t, int32(1), r.closeCount.Load(), "Close() should be called once (copies share state)")
}

func TestConcurrentClose(t *testing.T) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	SetLeakReporter(PanicLeak)
	SetStackDepth(5)
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	r := &testResource{id: 1}
	h := New[testResource, testTrait](r)

	// Close from multiple goroutines concurrently
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Close()
		}()
	}
	wg.Wait()

	// Even with 100 concurrent Close() calls, resource should be closed exactly once
	require.Equal(t, int32(1), r.closeCount.Load(), "Close() should be called once (atomic CAS protection)")
}

func TestDup(t *testing.T) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	SetLeakReporter(PanicLeak)
	SetStackDepth(5)
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	r := &testResource{id: 1}
	h1 := New[testResource, testDupTrait](r)

	h2, err := Dup(h1)
	require.NoError(t, err, "Dup should not fail")

	// Dup should have been called once on the resource
	require.Equal(t, int32(1), r.dupCount.Load(), "Dup() should be called exactly once")

	// Both should access the same resource
	require.Equal(t, h1.Get(), h2.Get(), "Dup'd handles should share resource")

	// Close h1
	h1.Close()

	// h1 should return nil, but h2 should still work
	require.Nil(t, h1.Get(), "h1.Get() should return nil after close")
	require.NotNil(t, h2.Get(), "h2.Get() should still work after h1 is closed")

	// Close h2
	h2.Close()

	require.Nil(t, h2.Get(), "h2.Get() should return nil after close")

	// Both handles should independently call Close() on the resource
	require.Equal(t, int32(2), r.closeCount.Load(), "Close() should be called twice (independent handles)")
}

func TestDupAfterClose(t *testing.T) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	SetLeakReporter(PanicLeak)
	SetStackDepth(5)
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	r := &testResource{id: 1}
	h1 := New[testResource, testDupTrait](r)
	h1.Close()

	require.Panics(t, func() {
		_, _ = Dup(h1)
	}, "Dup should panic on closed handle")
}

func TestHandleID(t *testing.T) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	SetLeakReporter(PanicLeak)
	SetStackDepth(5)
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	r1 := &testResource{id: 1}
	r2 := &testResource{id: 2}

	h1 := New[testResource, testTrait](r1)
	h2 := New[testResource, testTrait](r2)

	require.NotEqual(t, h1.HandleID(), h2.HandleID(), "Different handles should have different IDs")

	// Copied handle should have same ID
	h1Copy := h1
	require.Equal(t, h1.HandleID(), h1Copy.HandleID(), "Copied handle should have same ID")

	h1.Close()
	h2.Close()
}

func TestLeakReporter(t *testing.T) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	// Enable reporting with custom reporter
	var leaked atomic.Bool
	SetLeakReporter(func(id HandleID, resource string, pcs []uintptr) {
		leaked.Store(true)
		require.Equal(t, "testResource(99)", resource)
		require.Len(t, pcs, 3)
	})
	SetStackDepth(3)

	// Create and leak a handle
	func() {
		r := &testResource{id: 99}
		_ = New[testResource, testTrait](r)
		// Handle leaked - finalizer should run eventually
	}()

	// Loop calling GC until we see the leak reported
	for {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
		if leaked.Load() {
			break
		}
	}
}

func TestLeakDetectionWorks(t *testing.T) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	// Create a sub-test that should fail due to leak
	t.Run("IntentionalLeak", func(t *testing.T) {
		// Use a test helper to catch the failure
		var leaked atomic.Bool
		SetLeakReporter(func(id HandleID, resource string, pcs []uintptr) {
			leaked.Store(true)
		})
		SetStackDepth(5)

		// Intentionally leak a handle
		func() {
			r := &testResource{id: 42}
			_ = New[testResource, testTrait](r)
			// Handle leaked - finalizer should detect it
		}()

		// Loop calling GC until we see the leak reported
		for {
			runtime.GC()
			time.Sleep(10 * time.Millisecond)
			if leaked.Load() {
				break
			}
		}
	})
}

func TestConfigureLogLevel(t *testing.T) {
	oldReporter := leakReporter
	defer SetLeakReporter(oldReporter)

	// Test SetLogLevel
	SetLogLevel(slog.LevelError)
	require.Equal(t, slog.LevelError, logLevel, "SetLogLevel should update logLevel")

	SetLogLevel(slog.LevelWarn) // Reset
}

func TestSetStackDepth(t *testing.T) {
	oldDepth := stackDepth
	defer SetStackDepth(oldDepth)

	SetStackDepth(10)
	require.Equal(t, 10, stackDepth, "SetStackDepth should update stackDepth")
}

// Benchmarks

func BenchmarkBaseline(b *testing.B) {
	// Baseline: resource pooling without leakguard
	b.ReportAllocs()
	for b.Loop() {
		r := testResourcePool.Get()
		r.closeCount.Add(1)
		testResourcePool.Put(r)
	}
}

func BenchmarkNewClose(b *testing.B) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	SetLeakReporter(nil)
	SetStackDepth(0)
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	b.ReportAllocs()
	for b.Loop() {
		r := testResourcePool.Get()
		h := New[testResource, benchTrait](r)
		h.Close()
	}
}

func BenchmarkNewCloseWithLeakDetection(b *testing.B) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	for depth := 0; depth <= 5; depth++ {
		b.Run(fmt.Sprintf("depth=%d", depth), func(b *testing.B) {
			SetLeakReporter(LogLeak)
			SetStackDepth(depth)

			b.ReportAllocs()
			for b.Loop() {
				r := testResourcePool.Get()
				h := New[testResource, benchTrait](r)
				h.Close()
			}
		})
	}
}

func BenchmarkNewCloseAutoMode(b *testing.B) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	// Set up auto-closing mode: no-op reporter with stack depth 0
	SetLeakReporter(func(HandleID, string, []uintptr) {}) // No-op reporter (not nil!)
	SetStackDepth(0)
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	b.ReportAllocs()
	for b.Loop() {
		r := testResourcePool.Get()
		h := New[testResource, benchTrait](r)
		h.Close()
	}
}

func BenchmarkDup(b *testing.B) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	SetLeakReporter(nil)
	SetStackDepth(0)
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	r := testResourcePool.Get()
	h := New[testResource, benchDupTrait](r)
	defer h.Close()

	b.ReportAllocs()
	for b.Loop() {
		h2, _ := Dup(h)
		h2.Close()
	}
}

func BenchmarkGet(b *testing.B) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	SetLeakReporter(nil)
	SetStackDepth(0)
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	r := testResourcePool.Get()
	h := New[testResource, benchTrait](r)
	defer h.Close()

	b.ReportAllocs()
	for b.Loop() {
		_ = h.Get()
	}
}

func BenchmarkConcurrentClose(b *testing.B) {
	oldReporter := leakReporter
	oldDepth := stackDepth
	SetLeakReporter(nil)
	SetStackDepth(0)
	defer func() {
		SetLeakReporter(oldReporter)
		SetStackDepth(oldDepth)
	}()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			r := testResourcePool.Get()
			h := New[testResource, benchTrait](r)
			h.Close()
		}
	})
}

func BenchmarkBaselineConcurrent(b *testing.B) {
	// Baseline: concurrent resource pooling without leakguard
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			r := testResourcePool.Get()
			r.closeCount.Add(1)
			testResourcePool.Put(r)
		}
	})
}
