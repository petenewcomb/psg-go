// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package leakguard_test

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/streampool/internal/leakguard"
	"github.com/petenewcomb/streampool/internal/omnipool"
)

// FileResource represents a file that needs cleanup.
type FileResource struct {
	Name string
	mu   sync.Mutex
}

// FileTrait defines cleanup for files.
type FileTrait struct{}

func (FileTrait) Close(r *FileResource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Printf("Closing file: %s\n", r.Name)
}

func (FileTrait) String(r *FileResource) string {
	return r.Name
}

// Example_basic demonstrates basic handle creation and cleanup.
func Example_basic() {
	// Create a resource that needs cleanup
	file := &FileResource{Name: "data.txt"}

	// Wrap it in a handle
	h := leakguard.New[FileResource, FileTrait](file)

	// Access the resource
	if f := h.Get(); f != nil {
		fmt.Printf("Using file: %s\n", f.Name)
	}

	// Close explicitly - cleanup happens here
	h.Close()

	// After Close, Get returns nil
	if f := h.Get(); f == nil {
		fmt.Println("Handle closed")
	}

	// Output:
	// Using file: data.txt
	// Closing file: data.txt
	// Handle closed
}

// pooledFilePool is a shared pool for the pooling example.
var pooledFilePool = omnipool.For[FileResource]()

// PooledFileTrait returns resources to pool on close.
type PooledFileTrait struct{}

func (PooledFileTrait) Close(r *FileResource) {
	fmt.Printf("Closing and pooling: %s\n", r.Name)
	pooledFilePool.Put(r)
}

// Example_pooling demonstrates zero-allocation patterns with pooling.
func Example_pooling() {
	// Get from pool, wrap in handle, use, close
	for i := 0; i < 3; i++ {
		file := pooledFilePool.Get()
		file.Name = fmt.Sprintf("file%d.txt", i)

		h := leakguard.New[FileResource, PooledFileTrait](file)
		fmt.Printf("Using: %s\n", h.Get().Name)
		h.Close()
	}

	// Output:
	// Using: file0.txt
	// Closing and pooling: file0.txt
	// Using: file1.txt
	// Closing and pooling: file1.txt
	// Using: file2.txt
	// Closing and pooling: file2.txt
}

// ConnResource represents a connection with reference counting.
type ConnResource struct {
	Name     string
	refCount int
	mu       sync.Mutex
}

// ConnTrait implements reference-counted duplication.
type ConnTrait struct{}

func (ConnTrait) Close(r *ConnResource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refCount--
	fmt.Printf("%s refcount: %d\n", r.Name, r.refCount)
	if r.refCount == 0 {
		fmt.Printf("Closing connection: %s\n", r.Name)
	}
}

func (ConnTrait) Dup(r *ConnResource) (*ConnResource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refCount++
	fmt.Printf("%s refcount: %d\n", r.Name, r.refCount)
	return r, nil
}

// Example_dup demonstrates reference-counted handles.
func Example_dup() {
	conn := &ConnResource{Name: "db-connection", refCount: 1}

	// Create original handle
	h1 := leakguard.New[ConnResource, ConnTrait](conn)
	fmt.Printf("Created handle 1\n")

	// Duplicate the handle - increments refcount
	h2, _ := leakguard.Dup(h1)
	fmt.Printf("Created handle 2\n")

	// Close h1 - decrements but doesn't close connection
	h1.Close()

	// h2 still works
	if c := h2.Get(); c != nil {
		fmt.Printf("Handle 2 still valid: %s\n", c.Name)
	}

	// Close h2 - now connection closes
	h2.Close()

	// Output:
	// Created handle 1
	// db-connection refcount: 2
	// Created handle 2
	// db-connection refcount: 1
	// Handle 2 still valid: db-connection
	// db-connection refcount: 0
	// Closing connection: db-connection
}

// Example_leakDetection demonstrates proper usage with no leaks.
func Example_leakDetection() {
	// Proper usage - no leak
	func() {
		file := &FileResource{Name: "proper.txt"}
		h := leakguard.New[FileResource, FileTrait](file)
		defer h.Close() // Ensures cleanup
		fmt.Printf("Using: %s\n", h.Get().Name)
	}()

	fmt.Println("No leaks detected")

	// Output:
	// Using: proper.txt
	// Closing file: proper.txt
	// No leaks detected
}

// Example_leakDetectionWithLeak demonstrates what happens when a handle is leaked.
// This example intentionally leaks a handle to show leak detection in action.
func Example_leakDetectionWithLeak() {
	// Note: In production, leakguard is configured once at startup.
	// This example saves/restores settings for demonstration purposes.

	// Set up a custom reporter that writes to stdout for this example
	var detected atomic.Bool
	leakguard.SetLeakReporter(func(id leakguard.HandleID, resource string, pcs []uintptr) {
		detected.Store(true)
		frames := runtime.CallersFrames(pcs)
		fmt.Printf("LEAK DETECTED: unclosed handle to %s created at:\n", resource)
		for {
			frame, more := frames.Next()
			fmt.Printf("\t%s:%d: %s\n", filepath.Base(frame.File), frame.Line, frame.Function)
			if !more {
				break
			}
		}
	})
	leakguard.SetStackDepth(3)

	// BAD: Create a handle and forget to close it
	func() {
		file := &FileResource{Name: "leaked.txt"}
		_ = leakguard.New[FileResource, FileTrait](file)
		// Oops! Forgot to close the handle - this will be detected
		fmt.Println("Created handle but forgot to close it")
	}()

	// Force garbage collection and wait for finalizer
	// In real code, this would happen naturally over time
	fmt.Println("Simulating GC and finalization...")

	// Loop calling GC until the leak is detected
	for i := 0; i < 50 && !detected.Load(); i++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}

	// Output:
	// Created handle but forgot to close it
	// Simulating GC and finalization...
	// LEAK DETECTED: unclosed handle to leaked.txt created at:
	// 	example_test.go:200: github.com/petenewcomb/streampool/internal/leakguard_test.Example_leakDetectionWithLeak.func2
	// 	example_test.go:203: github.com/petenewcomb/streampool/internal/leakguard_test.Example_leakDetectionWithLeak
	// 	run_example.go:63: testing.runExample
	// Closing file: leaked.txt
}

// Example_idempotentClose demonstrates that Close is safe to call multiple times.
func Example_idempotentClose() {
	file := &FileResource{Name: "data.txt"}
	h := leakguard.New[FileResource, FileTrait](file)

	// Close multiple times - safe and idempotent
	h.Close()
	h.Close()
	h.Close()

	fmt.Println("Multiple Close calls are safe")

	// Output:
	// Closing file: data.txt
	// Multiple Close calls are safe
}
