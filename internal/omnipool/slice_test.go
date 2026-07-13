// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"testing"
	"time"
)

func TestSlicePool(t *testing.T) {
	// An isolated pool instance, so the empty-pool assertion is deterministic
	// and not polluted by other tests or by earlier -count iterations sharing
	// the process-global pool for this element type.
	pool := &SlicePool[[]int, int]{}

	s1 := pool.Get()
	if s1 != nil {
		t.Errorf("Expected nil slice from empty pool, got %v", s1)
	}

	original := make([]int, 2, 8)
	original[0] = 42
	original[1] = 24
	pool.Put(original)

	// Try to get a pooled slice - pool may return nil due to GC pressure
	// Keep trying for up to 10ms to increase chances of getting a pooled object
	deadline := time.Now().Add(10 * time.Millisecond)
	var s2 []int
	for time.Now().Before(deadline) {
		testSlice := make([]int, 0, 8)
		pool.Put(testSlice)

		s2 = pool.Get()
		if s2 != nil {
			if len(s2) != 0 {
				t.Errorf("Expected length 0, got %d", len(s2))
			}
			if cap(s2) != 8 {
				t.Errorf("Expected capacity 8, got %d", cap(s2))
			}
			break
		}
	}

	if s2 == nil {
		t.Fatal("sync.Pool failed to return any pooled objects after 10ms of attempts")
	}

	s2 = append(s2, 100, 200)
	if len(s2) != 2 || s2[0] != 100 || s2[1] != 200 {
		t.Errorf("Slice contents wrong: %v", s2)
	}

	pool.Put(s2)

	// Get again - should be reset but preserve capacity
	// Again, try for up to 10ms
	deadline = time.Now().Add(10 * time.Millisecond)
	var s3 []int
	for time.Now().Before(deadline) {
		// Put a slice to increase chances
		testSlice := make([]int, 0, 8)
		pool.Put(testSlice)

		s3 = pool.Get()
		if s3 != nil {
			if len(s3) != 0 {
				t.Errorf("Expected length 0, got %d", len(s3))
			}
			if cap(s3) != 8 {
				t.Errorf("Expected capacity 8, got %d", cap(s3))
			}
			break
		}
	}

	if s3 == nil {
		t.Fatal("sync.Pool failed to return any pooled objects after 10ms of attempts")
	}
}

func TestSlicePoolZeroCapacity(t *testing.T) {
	pool := ForSlice([]string(nil))

	// Put slice with zero capacity - should be ignored
	var zeroSlice []string
	pool.Put(zeroSlice)

	// Pool should still be empty
	s := pool.Get()
	if s != nil {
		t.Errorf("Expected nil slice after putting zero-capacity slice, got %v", s)
	}
}

func TestSlicePoolPackageFunctions(t *testing.T) {
	// The package-level functions route through the process-global pool, so
	// drain any residue an earlier -count iteration left before asserting the
	// empty-pool behavior.
	for GetSlice([]byte(nil)) != nil {
	}

	s1 := GetSlice([]byte(nil))
	if s1 != nil {
		t.Errorf("Expected nil slice from empty pool, got %v", s1)
	}

	original := make([]byte, 1, 4)
	original[0] = 0xFF
	PutSlice(original)

	deadline := time.Now().Add(10 * time.Millisecond)
	var s2 []byte
	for time.Now().Before(deadline) {
		// Put a slice to increase chances
		testSlice := make([]byte, 0, 4)
		PutSlice(testSlice)

		s2 = GetSlice([]byte(nil))
		if s2 != nil {
			if len(s2) != 0 {
				t.Errorf("Expected length 0, got %d", len(s2))
			}
			if cap(s2) != 4 {
				t.Errorf("Expected capacity 4, got %d", cap(s2))
			}
			break
		}
	}

	if s2 == nil {
		t.Fatal("sync.Pool failed to return any pooled objects after 10ms of attempts")
	}
}

func BenchmarkSlicePool(b *testing.B) {
	pool := ForSlice([]int(nil))

	b.Run("get_put", func(b *testing.B) {
		for i := 0; i < 10; i++ {
			s := make([]int, 0, 16)
			pool.Put(s)
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s := pool.Get()
			s = append(s, i, i+1, i+2)
			pool.Put(s)
		}
	})

	b.Run("baseline", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			s := make([]int, 0, 16)
			s = append(s, i, i+1, i+2)
			_ = s
		}
	})
}
