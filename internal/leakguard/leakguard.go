// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package leakguard provides safe, (net)zero-allocation handles for resources
// that require explicit cleanup. The interface design is inspired by that of
// [POSIX File Descriptors].
//
// Cleanup operations are defined via [Trait.Close]. [Trait] is passed to
// [Handle] as a type parameter. [Handle.Close] ensures that cleanup is executed
// only once and therefore ensures idempotentcy. By default [Handle] ensures
// that cleanup is also executed _exactly_ once, upon finalization by the
// garbage collector if not before. It also by default reports such
// finalization-based cleanup (leaks) as warning log messages or as panics if
// being run under `go test`. These reports by default include a stack trace
// from the point at which the unclosed handle was created.
//
// [Dup] additionally provides a way to clone a handle as long as the handle's
// trait type supports it via [DupTrait.Dup]. After a call to Dup, the original
// and cloned handles each have their own close state and therefore must be
// independently closed. The semantics of the underlying resource (copying,
// reference counting, degree of independence) as accessed through the separate
// handles are left to the implementation of the trait to define and guarantee.
//
// All defaults can be overridden via environment variables or programmatic
// setting of package variables. The reporting mechanism is also customizable.
//
// While it is possible to configure leakguard to provide "automatic" closure of
// resources upon garbage collection, this practice is not recommended. Such
// automatic closure places a heavy reliance on the garbage collector and
// finalization - a reliance this package is specifically designed to help users
// avoid by ensuring that they explicitly close handles when no longer needed.
// For instance, a primary use case for the package is to ensure that closed
// resources can be pooled for reuse via [sync.Pool] and thus avoid creating
// garbage collection overhead. If the garbage collector is relied upon for
// closure-via-finalization, then much of that overhead has already been
// incurred.
//
// Once a user is confident that an application always properly closes all
// resources (which leakguard helps to verify), they can disable leakguard's
// detection mechanism to eliminate even the registration and cancellation of
// finalizers. Since leakguard handles themselves are pooled, this reduces the
// overhead incurred by their use to an atomic operation or two, an additional
// layer of pointer indirection, and perhaps a non-inlined function call.
//
// [POSIX File Descriptors]: https://en.wikipedia.org/wiki/File_descriptor
package leakguard

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/petenewcomb/psg-go/internal/omnipool"
)

// Trait provides cleanup operations for a resource type R.
// The zero value of the trait type is used to call methods, so implementations
// must not require initialization.
//
// Close is called when a handle is closed (explicitly or via finalizer).
// Implementations should panic if cleanup fails.
type Trait[R any] interface {
	Close(*R)
}

// ReportTrait extends Trait with custom string representation for leak reports.
// If not implemented, the resource will be formatted using fmt.Sprintf("%v", resource).
type ReportTrait[R any] interface {
	Trait[R]
	String(*R) string
}

// DupTrait extends Trait with duplication capability for resources that
// support shared ownership or cloning.
//
// Dup creates a new ownership claim on the resource. The returned pointer
// may be the same (reference-counted resources) or different (copied resources).
// The trait implementation defines the sharing semantics.
// Returns an error for runtime resource failures (e.g., file descriptor limits).
type DupTrait[R any] interface {
	Trait[R]
	Dup(*R) (*R, error)
}

// ReportLeakFunc is called when a handle leak is detected.
// It receives:
//   - id: The HandleID that was leaked
//   - resource: String representation of the resource (via ReportTrait.String or fmt %v)
//   - pcs: Program counters for the stack trace where the handle was created
type ReportLeakFunc func(HandleID, string, []uintptr)

var (
	stackDepth   int
	leakReporter ReportLeakFunc
	logLevel     = slog.LevelWarn
)

// LogLeak logs leak messages using the default slog logger with structured fields.
// The log level can be configured via SetLogLevel (default: LevelWarn).
// Use this for production to detect leaks without crashing.
func LogLeak(id HandleID, resource string, pcs []uintptr) {
	// Build structured stack trace
	var stackAttrs []slog.Attr
	if len(pcs) > 0 {
		frames := runtime.CallersFrames(pcs)
		idx := 0
		for {
			frame, more := frames.Next()
			offset := frame.PC - frame.Entry
			stackAttrs = append(stackAttrs, slog.GroupAttrs(fmt.Sprintf("%d", idx),
				slog.String("function", frame.Function),
				slog.String("file", frame.File),
				slog.String("line", strconv.Itoa(frame.Line)),
				slog.String("offset", fmt.Sprintf("+0x%x", offset)),
			))
			idx++
			if !more {
				break
			}
		}
	}

	// Log with structured fields
	slog.LogAttrs(context.Background(), logLevel,
		"Close() was not called on handle before finalization",
		slog.String("handle_id", strconv.FormatInt(int64(id), 10)),
		slog.String("resource", resource),
		slog.GroupAttrs("creation_stack", stackAttrs...),
	)
}

// PanicLeak calls panic with the leak message and stack trace.
// Use this during development and testing to catch bugs immediately.
func PanicLeak(id HandleID, resource string, pcs []uintptr) {
	var msg strings.Builder
	fmt.Fprintf(&msg, "Close() was not called on %s handle %d before finalization", resource, id)
	if len(pcs) > 0 {
		fmt.Fprintf(&msg, "\nHandle %d created at:", id)
		frames := runtime.CallersFrames(pcs)
		for {
			frame, more := frames.Next()
			offset := frame.PC - frame.Entry
			fmt.Fprintf(&msg, "\n%s\n\t%s:%d +0x%x", frame.Function, frame.File, frame.Line, offset)
			if !more {
				break
			}
		}
	}
	panic(msg.String())
}

// SetLogLevel sets the slog level used by LogLeak.
// Default is LevelWarn. Changes take effect for subsequent leak reports.
func SetLogLevel(level slog.Level) {
	logLevel = level
}

// SetStackDepth sets the maximum number of stack frames to capture when
// creating new handles (0 disables stack traces). Stack traces are included
// in leak reports to help identify where unclosed handles were created.
//
// Changes only affect newly created handles. The depth can also be set via
// the PSG_LEAK_STACK_DEPTH environment variable. The default is 5 frames
// (sufficient to identify the call site plus context), or 0 when reporter is nil.
//
// Note: Each frame requires 8 bytes of memory per handle.
func SetStackDepth(depth int) {
	stackDepth = depth
}

// SetLeakReporter sets the function to call when a handle leak is detected.
// The function receives:
//   - id: The HandleID that was leaked
//   - resource: String representation of the resource (via ReportTrait.String or fmt %v)
//   - pcs: Program counters for the stack trace where the handle was created
//
// The function is called asynchronously in a separate goroutine to avoid
// blocking the finalizer thread. Setting nil disables leak detection entirely
// (no finalizers registered, no cleanup overhead).
//
// Pre-defined functions: LogLeak (uses slog at LogLevel) and PanicLeak.
// The default is PanicLeak when running under 'go test' and LogLeak otherwise.
//
// Custom implementations can format stack traces using runtime.CallersFrames(pcs).
func SetLeakReporter(fn ReportLeakFunc) {
	leakReporter = fn
}

func init() {
	// Parse PSG_LEAK_REPORTING
	reportingStr := os.Getenv("PSG_LEAK_REPORTING")
	var reporter ReportLeakFunc
	switch reportingStr {
	case "log":
		reporter = LogLeak
	case "panic":
		reporter = PanicLeak
	case "off":
		reporter = nil
	default:
		// Default: Panic during tests (catch bugs), Log in production (don't crash)
		var defaultStr string
		if testing.Testing() {
			defaultStr = "panic"
			reporter = PanicLeak
		} else {
			defaultStr = "log"
			reporter = LogLeak
		}
		if reportingStr != "" {
			slog.Warn("Invalid PSG_LEAK_REPORTING value, using default", "value", reportingStr, "default", defaultStr)
		}
	}
	SetLeakReporter(reporter)

	// Parse PSG_LEAK_STACK_DEPTH with smart defaults
	depth := 5 // Default: call site + 1-2 layers of context
	if reporter == nil {
		depth = 0 // No stacks needed when leak detection is off
	}

	if depthStr := os.Getenv("PSG_LEAK_STACK_DEPTH"); depthStr != "" {
		if d, err := strconv.Atoi(depthStr); err == nil && d >= 0 {
			depth = d
		} else {
			slog.Warn("Invalid PSG_LEAK_STACK_DEPTH value, using default", "value", depthStr, "default", depth)
		}
	}
	SetStackDepth(depth)
}

// HandleID uniquely identifies a handle for tracing and debugging.
type HandleID int64

var handleCounter atomic.Int64

// handle is the internal pooled handle structure (not exported).
type handle[R any, T Trait[R]] struct {
	id          atomic.Int64                 // Current valid ID, 0 = closed
	pcs         []uintptr                    // Program counters for stack trace (len = captured depth)
	resource    atomic.Pointer[R]            // Pointer to the resource
	pool        *omnipool.Pool[handle[R, T]] // Cached pool pointer (set once in Init)
	finalizerFn func(*handle[R, T])          // Finalizer function (set once in Init)
}

// Handle is a value type that wraps a pooled internal handle.
// It can be copied; each copy shares the same internal handle.
// Close() uses atomic CAS to ensure only one copy successfully closes.
type Handle[R any, T Trait[R]] struct {
	id HandleID      // Captured at creation, immutable
	h  *handle[R, T] // Pointer to pooled internal handle
}

// Init initializes an internal handle when first allocated from the pool.
func (h *handle[R, T]) Init() {
	// Create finalizer once - reused across pool cycles
	h.finalizerFn = func(h *handle[R, T]) {
		// Check if already closed (id = 0)
		currentID := h.id.Load()
		if currentID == 0 {
			// Already closed - nothing to do
			h.pool.Put(h)
			return
		}

		// Try to mark as closed
		if !h.id.CompareAndSwap(currentID, 0) {
			// Someone else closed it
			h.pool.Put(h)
			return
		}

		// Capture resource and clear it atomically
		resource := h.resource.Swap(nil)
		if resource == nil {
			panic("finalizer called with nil resource")
		}

		// Capture leak reporter once
		reporter := leakReporter

		// Handle was leaked - report asynchronously to avoid blocking finalizer
		if reporter == nil {
			var trait T
			trait.Close(resource)
		} else {
			// Close after reporting.

			// Capture what we need for reporting
			handleID := HandleID(currentID)
			var pcs []uintptr
			if len(h.pcs) > 0 {
				pcs = make([]uintptr, len(h.pcs))
				copy(pcs, h.pcs)
			}

			// Do expensive formatting and reporting in separate goroutine
			go func() {
				// Get resource string representation
				var resourceStr string
				var trait T
				if reportTrait, ok := any(trait).(ReportTrait[R]); ok {
					resourceStr = reportTrait.String(resource)
				} else {
					resourceStr = fmt.Sprintf("%v", resource)
				}

				reporter(handleID, resourceStr, pcs)

				trait.Close(resource)
			}()
		}

		// Return handle to pool
		h.pool.Put(h)
	}
}

// Reset prepares an internal handle for reuse from the pool.
func (h *handle[R, T]) Reset() {
	// Assert clean state - both should already be cleared
	if h.id.Load() != 0 {
		panic("Reset() called with non-zero id")
	}
	if h.resource.Load() != nil {
		panic("Reset() called with non-nil resource")
	}
	// Don't clear finalizerFn or pcs - they're reused
}

// capture records handle creation information for leak detection.
func (h *handle[R, T]) capture(id HandleID) {
	h.id.Store(int64(id))

	// Read global once
	depth := stackDepth

	// Allocate/reallocate/free pcs to match current settings
	if leakReporter != nil && depth > 0 {
		// Ensure capacity matches current depth
		if cap(h.pcs) != depth {
			h.pcs = make([]uintptr, depth)
		} else {
			// Reuse existing buffer, set length to capacity
			h.pcs = h.pcs[:cap(h.pcs)]
		}

		//nolint:mnd // Skip: runtime.Callers, capture, and New/Dup
		n := runtime.Callers(3, h.pcs)
		// Set length to actual number captured
		h.pcs = h.pcs[:n]
	} else {
		// Free the slice when not needed
		h.pcs = nil
	}
}

// New creates a new handle for the given resource.
// The handle tracks ownership and ensures cleanup is performed exactly once,
// either via explicit Close() or automatic finalization (if leak detection
// is enabled).
//
// If a leak reporter is configured (via SetLeakReporter), a finalizer is
// registered to detect and report handles that are garbage collected without
// being closed.
func New[R any, T Trait[R]](resource *R) Handle[R, T] {
	pool := omnipool.For[handle[R, T]]()
	h := pool.Get()
	h.pool = pool // Cache pool pointer in handle

	id := HandleID(handleCounter.Add(1))
	h.capture(id)
	h.resource.Store(resource)

	if leakReporter != nil {
		runtime.SetFinalizer(h, h.finalizerFn)
	}

	return Handle[R, T]{id: id, h: h}
}

// Get returns a pointer to the resource, or nil if the handle has
// been closed or if the handle ID no longer matches (indicating use after close
// of a copied handle).
//
// This method is safe to call concurrently from multiple goroutines.
func (h Handle[R, T]) Get() *R {
	// Check if handle is still valid (id matches and not closed)
	if h.h.id.Load() != int64(h.id) {
		return nil
	}
	return h.h.resource.Load()
}

// HandleID returns the handle ID for tracing and debugging.
func (h Handle[R, T]) HandleID() HandleID {
	return h.id
}

// Close closes the handle and performs cleanup on the underlying resource.
// This method is idempotent and thread-safe: the first call performs cleanup,
// and subsequent calls (including from copies of the handle) are no-ops.
//
// After Close returns, Get() will return nil for all copies of this handle.
// Close uses atomic compare-and-swap to ensure exactly one caller performs cleanup.
func (h Handle[R, T]) Close() {
	// Try to mark as closed using CAS - only succeeds if id still matches
	if !h.h.id.CompareAndSwap(int64(h.id), 0) {
		return // Already closed or wrong ID
	}

	// Cancel finalizer
	if leakReporter != nil {
		runtime.SetFinalizer(h.h, nil)
	}

	// Capture resource and clear it atomically
	resource := h.h.resource.Swap(nil)
	if resource == nil {
		panic("Close() called with nil resource")
	}

	// Perform cleanup
	var trait T
	trait.Close(resource)

	// Return internal handle to pool (using cached pointer)
	h.h.pool.Put(h.h)
}

// Dup creates a new handle with independent close state for the same resource.
// Panics if called on a closed handle (programming error).
// Returns an error if the trait's Dup fails (e.g., resource limit exceeded).
//
// The new handle has its own HandleID and must be closed independently.
// The trait's Dup method determines whether the resource is shared (via reference
// counting) or copied. Both handles must be closed regardless of the sharing model.
//
// Only available when T implements DupTrait.
func Dup[R any, T DupTrait[R]](h Handle[R, T]) (Handle[R, T], error) {
	resource := h.Get()
	if resource == nil {
		panic(fmt.Sprintf("Dup() on closed handle %d", h.HandleID()))
	}

	// Duplicate via trait (may return same pointer or different one)
	var trait T
	dupResource, err := trait.Dup(resource)
	if err != nil {
		return Handle[R, T]{}, err
	}

	// Create new internal handle
	pool := omnipool.For[handle[R, T]]()
	newInternal := pool.Get()
	newInternal.pool = pool // Cache pool pointer in handle

	newID := HandleID(handleCounter.Add(1))
	newInternal.capture(newID)
	newInternal.resource.Store(dupResource)

	if leakReporter != nil {
		runtime.SetFinalizer(newInternal, newInternal.finalizerFn)
	}

	return Handle[R, T]{id: newID, h: newInternal}, nil
}
