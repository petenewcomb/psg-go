// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"testing"
)

func TestSlicePool(t *testing.T) {
	pool := ForSlice([]int(nil))

	// Get a slice from empty pool (should be nil)
	s1 := pool.Get()
	if s1 != nil {
		t.Errorf("Expected nil slice from empty pool, got %v", s1)
	}

	// Create a slice with capacity and put it in pool
	original := make([]int, 2, 8)
	original[0] = 42
	original[1] = 24
	pool.Put(original)

	// Get it back - should have same capacity but zero length
	s2 := pool.Get()
	if len(s2) != 0 {
		t.Errorf("Expected length 0, got %d", len(s2))
	}
	if cap(s2) != 8 {
		t.Errorf("Expected capacity 8, got %d", cap(s2))
	}

	// Use the slice
	s2 = append(s2, 100, 200)
	if len(s2) != 2 || s2[0] != 100 || s2[1] != 200 {
		t.Errorf("Slice contents wrong: %v", s2)
	}

	// Put it back
	pool.Put(s2)

	// Get again - should be reset but preserve capacity
	s3 := pool.Get()
	if len(s3) != 0 {
		t.Errorf("Expected length 0, got %d", len(s3))
	}
	if cap(s3) != 8 {
		t.Errorf("Expected capacity 8, got %d", cap(s3))
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
	// Test package-level convenience functions
	s1 := GetSlice([]byte(nil))
	if s1 != nil {
		t.Errorf("Expected nil slice from empty pool, got %v", s1)
	}

	// Create and put a slice
	original := make([]byte, 1, 4)
	original[0] = 0xFF
	PutSlice(original)

	// Get it back
	s2 := GetSlice([]byte(nil))
	if len(s2) != 0 {
		t.Errorf("Expected length 0, got %d", len(s2))
	}
	if cap(s2) != 4 {
		t.Errorf("Expected capacity 4, got %d", cap(s2))
	}
}

func BenchmarkSlicePool(b *testing.B) {
	pool := ForSlice([]int(nil))

	b.Run("get_put", func(b *testing.B) {
		// Pre-populate with some slices
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
