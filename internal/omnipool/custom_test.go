// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"testing"
)

// Test struct pooling with custom pool
type customStruct struct {
	Value int
	Slice []string
}

type customStructTrait struct{}

func (customStructTrait) Make() *customStruct {
	return &customStruct{
		Slice: make([]string, 0, 8), // Pre-allocate capacity
	}
}

func (customStructTrait) Reset(cs *customStruct) {
	cs.Value = 0
	cs.Slice = cs.Slice[:0] // Keep capacity
}

func TestCustomPoolStruct(t *testing.T) {
	pool := ForCustom(customStructTrait{})

	// Get a struct
	s1 := pool.Get()
	if s1 == nil {
		t.Fatal("Expected non-nil struct")
	}
	if cap(s1.Slice) != 8 {
		t.Errorf("Expected slice capacity 8, got %d", cap(s1.Slice))
	}

	// Modify it
	s1.Value = 42
	s1.Slice = append(s1.Slice, "test1", "test2")

	// Put it back
	pool.Put(s1)

	// Get another (should be the same instance, reset)
	s2 := pool.Get()
	if s2.Value != 0 {
		t.Errorf("Expected Value to be reset to 0, got %d", s2.Value)
	}
	if len(s2.Slice) != 0 {
		t.Errorf("Expected Slice length to be reset to 0, got %d", len(s2.Slice))
	}
	if cap(s2.Slice) != 8 {
		t.Errorf("Expected slice capacity to be preserved as 8, got %d", cap(s2.Slice))
	}
}

// Test channel pooling with custom pool
type customChanTrait struct{}

func (customChanTrait) Make() chan int {
	return make(chan int, 5)
}

func (customChanTrait) Reset(ch chan int) {
	// Drain the channel
	for len(ch) > 0 {
		<-ch
	}
}

func TestCustomPoolChannel(t *testing.T) {
	pool := ForCustom(customChanTrait{})

	// Get a channel
	ch1 := pool.Get()
	if ch1 == nil {
		t.Fatal("Expected non-nil channel")
	}
	if cap(ch1) != 5 {
		t.Errorf("Expected channel capacity 5, got %d", cap(ch1))
	}

	// Use the channel
	ch1 <- 100
	ch1 <- 200

	// Put it back
	pool.Put(ch1)

	// Get another (should be reset)
	ch2 := pool.Get()
	if len(ch2) != 0 {
		t.Errorf("Expected channel to be drained (length 0), got %d", len(ch2))
	}
	if cap(ch2) != 5 {
		t.Errorf("Expected channel capacity to be preserved as 5, got %d", cap(ch2))
	}
}

func TestCustomPoolPackageFunctions(t *testing.T) {
	// Test package-level convenience functions
	s := GetCustom(customStructTrait{})
	if s == nil {
		t.Fatal("GetCustom returned nil")
	}
	if cap(s.Slice) != 8 {
		t.Errorf("Expected slice capacity 8, got %d", cap(s.Slice))
	}

	s.Value = 99
	s.Slice = append(s.Slice, "package", "test")

	PutCustom(customStructTrait{}, s)

	// Get another to verify reset
	s2 := GetCustom(customStructTrait{})
	if s2.Value != 0 {
		t.Errorf("Expected Value to be reset to 0, got %d", s2.Value)
	}
	if len(s2.Slice) != 0 {
		t.Errorf("Expected Slice length to be reset to 0, got %d", len(s2.Slice))
	}
}

func BenchmarkCustomPool(b *testing.B) {
	b.Run("struct", func(b *testing.B) {
		pool := ForCustom(customStructTrait{})
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s := pool.Get()
			s.Value = i
			s.Slice = append(s.Slice, "benchmark")
			pool.Put(s)
		}
	})

	b.Run("channel", func(b *testing.B) {
		pool := ForCustom(customChanTrait{})
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ch := pool.Get()
			ch <- i
			pool.Put(ch)
		}
	})
}
