# OpenTelemetry Integration for PSG (otpsg)

This package provides OpenTelemetry integration for the PSG scatter-gather library. It enables transparent propagation of trace context, metrics collection, and structured logging for PSG tasks, gathers, and combiners.

## Features

- **Context Propagation**: Transparently propagate OpenTelemetry trace context through PSG tasks, gathers, and combiners
- **Tracing**: Add spans with meaningful operation names to PSG operations
- **Metrics**: Collect metrics on task execution, duration, and errors
- **Logging**: Add structured logging for PSG operations
- **Combined Instrumentation**: Convenient wrappers that apply all instrumentation at once

## Usage

### Basic Context Propagation

```go
import (
    "github.com/petenewcomb/psg-go"
    "github.com/petenewcomb/psg-go/otpsg"
)

// Create a task that propagates trace context
propagatedTask := otpsg.PropagateTask(func(ctx context.Context) (string, error) {
    // Normal task logic
    return "result", nil
})

// Create a gather function that handles propagated context
propagatedGather := otpsg.PropagateGather(func(ctx context.Context, result string, err error) error {
    // Context here will have trace context from the task
    return nil
})

// Use as normal with PSG
propagatedGather.Scatter(ctx, pool, propagatedTask)
```

### Adding Tracing Spans

```go
// Create a task with a named span
tracedTask := otpsg.TracedTask("process-data", func(ctx context.Context) (string, error) {
    // Normal task logic - will have a span named "process-data"
    return "result", nil
})

// Create a gather function with a named span
tracedGather := otpsg.TracedGather("handle-result", func(ctx context.Context, result string, err error) error {
    // Will have a span named "handle-result" that's connected to the task's span
    return nil
})

// Use as normal with PSG
tracedGather.Scatter(ctx, pool, tracedTask)
```

### Adding Metrics

```go
// Create a task with metrics
metricsTask := otpsg.MetricsTask("data_processing", func(ctx context.Context) (string, error) {
    // Normal task logic
    return "result", nil
})

// This will record:
// - data_processing.count
// - data_processing.duration (as a histogram)
// - data_processing.errors (if errors occur)
```

### Adding Logging

```go
// Create a task with logging
loggedTask := otpsg.LoggedTask("process-data", func(ctx context.Context) (string, error) {
    // Normal task logic
    return "result", nil
})

// This will log:
// - "Starting task" at DEBUG level
// - "Task completed" at DEBUG level (or "Task failed" at ERROR level)
```

### Full Instrumentation

```go
// Create a fully instrumented task (logging + metrics + tracing)
instrumentedTask := otpsg.InstrumentedTask("process-data", func(ctx context.Context) (string, error) {
    // Normal task logic
    return "result", nil
})

// Create a fully instrumented gather
instrumentedGather := otpsg.InstrumentedGather("handle-result", func(ctx context.Context, result string, err error) error {
    // Normal gather logic
    return nil
})

// Use the convenience function to scatter
err := otpsg.InstrumentedScatter(ctx, pool, instrumentedTask, instrumentedGather)
```

## Implementation Details

The instrumentation is implemented in layers:

1. **Propagation Layer**: Handles the core context propagation mechanism
2. **Tracing Layer**: Adds spans with meaningful names
3. **Metrics Layer**: Collects metrics on operation counts, durations, and errors
4. **Logging Layer**: Provides structured logging for operations
5. **Combined Layer**: Integrates all layers into convenient wrappers

The propagation mechanism wraps task results with trace context, which is then extracted and used in subsequent operations. This allows trace context to flow through the PSG pipeline without requiring users to manually handle it.