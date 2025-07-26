// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package omnipool

import (
	"testing"
)

// Test types
type simpleStruct struct {
	A int
	B string
}

type resetterStruct struct {
	A int
	B string
}

func (r *resetterStruct) Reset() {
	r.A = 0
	r.B = ""
}

func TestBasicPooling(t *testing.T) {
	// Get an object
	obj1 := Get[simpleStruct]()
	if obj1 == nil {
		t.Fatal("Get() returned nil")
	}

	// Modify it
	obj1.A = 42
	obj1.B = "basic test"

	// Put it back
	Put(obj1)

	// Get another object (might be the same one, zeroed)
	obj2 := Get[simpleStruct]()
	if obj2 == nil {
		t.Fatal("Second Get() returned nil")
	}

	// Should be zeroed
	if obj2.A != 0 || obj2.B != "" {
		t.Errorf("Object not properly zeroed: A=%d, B=%q", obj2.A, obj2.B)
	}
}

func TestResetterInterface(t *testing.T) {
	// Get an object
	obj1 := Get[resetterStruct]()
	if obj1 == nil {
		t.Fatal("Get() returned nil")
	}

	// Modify it
	obj1.A = 42
	obj1.B = "resetter test"

	// Put it back (should call Reset())
	Put(obj1)

	// Get another object
	obj2 := Get[resetterStruct]()
	if obj2 == nil {
		t.Fatal("Second Get() returned nil")
	}

	// Should be reset
	if obj2.A != 0 || obj2.B != "" {
		t.Errorf("Object not properly reset: A=%d, B=%q", obj2.A, obj2.B)
	}
}

func TestMultipleTypes(t *testing.T) {
	// Test that different types are pooled separately
	intPtr := Get[int]()
	stringPtr := Get[string]()
	structPtr := Get[simpleStruct]()

	if intPtr == nil || stringPtr == nil || structPtr == nil {
		t.Fatal("One of the Get() calls returned nil")
	}

	// Modify them
	*intPtr = 42
	*stringPtr = "multitype test"
	structPtr.A = 100

	// Put them back
	Put(intPtr)
	Put(stringPtr)
	Put(structPtr)

	// Get new ones
	intPtr2 := Get[int]()
	stringPtr2 := Get[string]()
	structPtr2 := Get[simpleStruct]()

	// Should be zeroed
	if *intPtr2 != 0 {
		t.Errorf("int not zeroed: %d", *intPtr2)
	}
	if *stringPtr2 != "" {
		t.Errorf("string not zeroed: %q", *stringPtr2)
	}
	if structPtr2.A != 0 || structPtr2.B != "" {
		t.Errorf("struct not zeroed: A=%d, B=%q", structPtr2.A, structPtr2.B)
	}
}

func TestPutNil(t *testing.T) {
	// Should not panic
	var nilPtr *simpleStruct
	Put(nilPtr)
}

func BenchmarkGetPut(b *testing.B) {
	b.Run("simpleStruct", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			obj := Get[simpleStruct]()
			obj.A = i
			obj.B = "benchmark"
			Put(obj)
		}
	})

	b.Run("resetterStruct", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			obj := Get[resetterStruct]()
			obj.A = i
			obj.B = "benchmark"
			Put(obj)
		}
	})
}
