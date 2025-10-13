# leakguard

Safe, zero-allocation handle management for Go resources requiring explicit cleanup.

## Overview

`leakguard` provides POSIX-style handles that ensure resources are always cleaned up exactly once, with automatic leak detection during development and testing. Think of it as a lightweight, type-safe alternative to manual resource management with the safety of finalizers but without relying on them in production.

**Key features:**

- **Zero allocation** after warmup (proven via benchmarks)
- **Automatic leak detection** with stack traces pointing to where handles were created
- **Thread-safe** with atomic operations and CAS-based cleanup
- **Idempotent** Close() - safe to call multiple times
- **Handle duplication** for reference-counted or cloned resources
- **Configurable** via environment variables or programmatic API
- **Production-ready** - leak detection can be disabled to eliminate finalizer overhead

## Quick Start

### Basic Usage

```go
import "github.com/petenewcomb/psg-go/internal/leakguard"

// Define how to clean up your resource
type FileTrait struct{}

func (FileTrait) Close(f *os.File) {
    if err := f.Close(); err != nil {
        panic(err) // Cleanup failures should panic
    }
}

// Create and use a handle
func processFile(path string) error {
    f, err := os.Open(path)
    if err != nil {
        return err
    }

    h := leakguard.New[*os.File, FileTrait](f)
    defer h.Close()

    // Use the resource
    if resource := h.Get(); resource != nil {
        _, err = io.Copy(os.Stdout, resource)
        return err
    }
    return nil
}
```

### Reference-Counted Resources

```go
type RefCountedBuffer struct {
    data []byte
    refs atomic.Int32
}

type BufferTrait struct{}

func (BufferTrait) Close(b *RefCountedBuffer) {
    if b.refs.Add(-1) == 0 {
        // Last reference - actually free the buffer
        bufferPool.Put(b)
    }
}

func (BufferTrait) Dup(b *RefCountedBuffer) (*RefCountedBuffer, error) {
    b.refs.Add(1)
    return b, nil // Return same pointer with incremented refcount
}

// Now you can safely share the buffer across goroutines
h1 := leakguard.New[*RefCountedBuffer, BufferTrait](buf)
h2, _ := leakguard.Dup(h1)  // Independent handle, same buffer
// Each handle must be closed independently
```

## Configuration

### Environment Variables

- `PSG_LEAK_HANDLING` - Controls leak detection behavior:
  - `""` (default) - Panic during tests, log in production
  - `"log"` - Log leaks without crashing
  - `"panic"` - Always panic on leaks
  - `"off"` or `"ignore"` - Disable leak detection entirely

- `PSG_LEAK_STACK_DEPTH` - Number of stack frames to capture (default: 5)
  - Set to `0` to disable stack traces
  - Each frame costs 8 bytes per handle

### Programmatic Configuration

```go
// Set custom leak reporter
leakguard.SetLeakReporter(func(id leakguard.HandleID, resource string, pcs []uintptr) {
    // Format your own message using the structured data
    var msg strings.Builder
    fmt.Fprintf(&msg, "LEAK: %s handle %d", resource, id)
    if len(pcs) > 0 {
        fmt.Fprintf(&msg, "\nCreated at:")
        frames := runtime.CallersFrames(pcs)
        for {
            frame, more := frames.Next()
            fmt.Fprintf(&msg, "\n%s:%d %s", frame.File, frame.Line, frame.Function)
            if !more {
                break
            }
        }
    }
    log.Println(msg.String())
})

// Adjust stack trace depth
leakguard.SetStackDepth(10)

// Disable leak detection for maximum performance
leakguard.SetLeakReporter(nil)

// Use pre-defined functions
leakguard.SetLeakReporter(leakguard.PanicLeak)
leakguard.SetLeakReporter(leakguard.LogLeak)
```

## Leak Detection

When a handle is garbage collected without being closed, leakguard reports the leak with a stack trace:

```
*os.File handle 42 leaked (missing Close())

Created at:
main.loadConfig
    /home/user/app/config.go:123
main.main
    /home/user/app/main.go:45
```

This makes it trivial to find and fix resource leaks during development.

## Performance

Benchmarks on Intel i7-10510U @ 1.80GHz:

| Operation | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| Baseline (raw pool) | 66 | 0 | 0 |
| New/Close (no leak detection) | 109 | 0 | 0 |
| New/Close (with leak detection, depth=0) | 236 | 0 | 0 |
| New/Close (with leak detection, depth=5) | 600-1082 | 0 | 0 |
| Dup/Close | 121 | 0* | 0 |
| Get | 6 | 0 | 0 |

\* The B/op shown for Dup is from sync.Pool internal growth, not actual allocations.

**Overhead:**
- ~43 ns without leak detection (~65% overhead vs. raw pooling)
- ~170 ns with leak detection at depth=0 (2.5x overhead)
- ~534-1016 ns with full stack traces at depth=5 (8-15x overhead)

For most applications, the overhead is negligible compared to actual I/O or work being done.

## Design Principles

### Trait-Based Cleanup

Resources are cleaned up via the `Trait` interface, allowing zero-overhead generic implementations:

```go
type Trait[R any] interface {
    Close(*R)
}
```

The zero value of the trait is used, so implementations must not require initialization.

### Handle Semantics

Handles can be copied freely - all copies share the same close state:

```go
h1 := leakguard.New[*File, FileTrait](f)
h2 := h1  // Copy the handle
h1.Close()
// h2.Get() now returns nil - both see the closed state
h2.Close() // Safe, but does nothing
```

Use `Dup()` when you need independent close state:

```go
h1 := leakguard.New[*Buffer, BufferTrait](buf)
h2, _ := leakguard.Dup(h1)
h1.Close()
// h2.Get() still works - independent close state
h2.Close() // Must be called
```

### Production Deployment

1. **During Development**: Use default settings (panic on leaks with stack traces)
2. **In Tests**: Leaks cause test failures, catching bugs early
3. **In Production**: Either:
   - Keep leak detection enabled with `LogLeak` to monitor for issues
   - Disable entirely (`SetLeakReporter(nil)`) once confident no leaks exist

## Implementation Notes

- Handles are pooled via `omnipool` for zero allocation after warmup
- Close state is tracked with atomic CAS operations for thread safety
- Finalizers are registered only when leak detection is enabled
- Stack traces are captured at handle creation time, not at leak detection time
- The pool pointer is cached in each handle to avoid lookup overhead

## When to Use

**Good use cases:**
- File handles, network connections, database connections
- Reference-counted buffers or resources
- Resources that must be explicitly released (not GC'd)
- Ensuring cleanup in complex control flow with multiple error paths
- Preventing resource leaks during development

**Not needed for:**
- Pure memory allocations (let GC handle it)
- Resources with trivial cleanup (use defer directly)
- Hot paths where even 100ns matters (though measure first!)

## Examples

See `leakguard_test.go` for comprehensive examples including:
- Basic New/Close patterns
- Concurrent access and closing
- Dup for reference-counted resources
- Custom traits for different resource types
- Leak detection verification

## License

MIT License - Copyright (c) Peter Newcomb
