// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"sync/atomic"
	"testing"
	"time"
)

type simpleStruct struct {
	A int
	B string
}

type resetterStruct struct {
	A          int
	B          string
	resetCount int
}

func (r *resetterStruct) Reset() {
	r.A = 0
	r.B = ""
	r.resetCount++
}

func TestBasicPooling(t *testing.T) {
	obj1 := Get[simpleStruct]()
	if obj1 == nil {
		t.Fatal("Get() returned nil")
	}

	obj1.A = 42
	obj1.B = "basic test"

	Release(obj1)

	// Try to verify pooling by attempting to get the same pointer back
	// Keep trying for up to 10ms to account for sync.Pool behavior
	deadline := time.Now().Add(10 * time.Millisecond)
	var gotPooled bool
	for time.Now().Before(deadline) {
		// Put the same object back to increase chances
		Release(obj1)

		obj2 := Get[simpleStruct]()
		if obj2 == obj1 {
			gotPooled = true
			// Should be zeroed
			if obj2.A != 0 || obj2.B != "" {
				t.Errorf("Object not properly zeroed: A=%d, B=%q", obj2.A, obj2.B)
			}
			Release(obj2) // Put it back for cleanup
			break
		} else {
			Release(obj2)
		}
	}

	if !gotPooled {
		t.Errorf("Failed to get a pooled object after 10ms - pooling may not be working")
	}
}

func TestResetterInterface(t *testing.T) {
	for i := 0; i < 10; i++ {
		obj := Get[resetterStruct]()
		obj.A = i
		obj.B = "test" //nolint:goconst // test string
		Release(obj)   // This should call Reset()
	}

	// Try to get a pooled object with non-zero reset count
	deadline := time.Now().Add(10 * time.Millisecond)
	var foundPooled bool

	for time.Now().Before(deadline) {
		obj := Get[resetterStruct]()

		// If resetCount > 0, this object was pooled and reset at least once
		if obj.resetCount > 0 {
			foundPooled = true
			if obj.A != 0 || obj.B != "" {
				t.Errorf("Pooled object not properly reset: A=%d, B=%q", obj.A, obj.B)
			}
			Release(obj)
			break
		}

		Release(obj)
	}

	if !foundPooled {
		t.Errorf("No pooled objects found after 10ms - pooling may not be working")
	}
}

// Types to test separate pooling - use structs with Init to track creation
var positiveCounter atomic.Int64
var negativeCounter atomic.Int64

type PositiveID struct {
	ID   int64 // Set during Init, preserved through Reset
	Data string
}

func (p *PositiveID) Init() {
	p.ID = positiveCounter.Add(1)
}

func (p *PositiveID) Reset() {
	// Preserve ID to track object identity
	p.Data = ""
}

type NegativeID struct {
	ID   int64 // Set during Init, preserved through Reset
	Data string
}

func (n *NegativeID) Init() {
	n.ID = -negativeCounter.Add(1)
}

func (n *NegativeID) Reset() {
	// Preserve ID to track object identity
	n.Data = ""
}

func TestMultipleTypes(t *testing.T) {
	// Test that different types are pooled separately even when similar
	positiveCounter.Store(0)
	negativeCounter.Store(0)

	for i := 0; i < 10; i++ {
		p := Get[PositiveID]()
		p.Data = "test" //nolint:goconst // test string
		Release(p)

		n := Get[NegativeID]()
		n.Data = "test" //nolint:goconst // test string
		Release(n)
	}

	// Now verify we only get appropriate IDs from each pool
	deadline := time.Now().Add(10 * time.Millisecond)
	var foundPositivePooled, foundNegativePooled bool

	for time.Now().Before(deadline) {
		p := Get[PositiveID]()
		if p.ID > 0 {
			if p.ID <= 10 {
				foundPositivePooled = true // This is a reused object
			}
		} else {
			t.Errorf("Got negative ID %d from PositiveID pool", p.ID)
		}
		Release(p)

		n := Get[NegativeID]()
		if n.ID < 0 {
			if n.ID >= -10 {
				foundNegativePooled = true // This is a reused object
			}
		} else {
			t.Errorf("Got positive ID %d from NegativeID pool", n.ID)
		}
		Release(n)

		if foundPositivePooled && foundNegativePooled {
			break
		}
	}

	if !foundPositivePooled {
		t.Errorf("Failed to get pooled objects from PositiveID pool")
	}
	if !foundNegativePooled {
		t.Errorf("Failed to get pooled objects from NegativeID pool")
	}
}

func TestPutNil(t *testing.T) {
	// Should not panic
	var nilPtr *simpleStruct
	Release(nilPtr)
}

func BenchmarkGetPut(b *testing.B) {
	b.Run("simpleStruct", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			obj := Get[simpleStruct]()
			obj.A = i
			obj.B = "benchmark"
			Release(obj)
		}
	})

	b.Run("resetterStruct", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			obj := Get[resetterStruct]()
			obj.A = i
			obj.B = "benchmark"
			Release(obj)
		}
	})
}
