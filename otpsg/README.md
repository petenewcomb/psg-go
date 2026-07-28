# OpenTelemetry integration for streampool (otpsg)

`otpsg` adds OpenTelemetry tracing, metrics, and structured logging to the
streampool scatter-gather library. Tracing is built on streampool **flows**:
a span's lifetime is the flow it belongs to, so it ends at the flow's *true*
end — after all work, including async work that outlives the handler that
spawned it and work that crosses funnel fan-ins — instead of at an op boundary.

## Tracing: one span per flow

`Traced` starts a span and returns the `FlowOption`s that bind its lifetime to
a flow. Open the flow with `streampool.WithFlow` and pass the options; the
framework ends the span for you.

```go
ctx, flow := otpsg.Traced(ctx, "process-request")

err := streampool.WithFlow(ctx, func(ctx context.Context) error {
    // Dispatch tasks / skims / funnels here. Every body — synchronous or
    // async — inherits the span for correlation. The span is NOT ended here;
    // it ends once, at the flow's true end.
    if err := loader.In(&wave).Start(ctx); err != nil {
        return err
    }
    return wave.CloseAndSkimAll(ctx)
}, flow...)
```

`Traced` returns two options with distinct scopes:

- **A path-scoped value** (`flowSpanKey.Value(span)`) rides every dispatch
  chain, so child-span parenting and log correlation work in every body,
  including async work. It **severs at a funnel fan-in** — merged items descend
  from independent parents, so no single span is the truthful value below the
  merge.
- **A DAG-scoped follow-up** (`FlowFollowUpFn(span.End)`) fires **exactly once**
  at the flow's true end, across funnels and covering async work. This is why
  the span is never ended at an op `defer`: that would end it while spawned work
  is still in flight.

### Correlating child spans and logs

In an **async** body (a task or skim handler running on a wave), OpenTelemetry's
active-span is *not* inherited — only the flow rider is. Use `Correlate` to
republish the flow span as active before creating a child span:

```go
_, child := otel.Tracer("otpsg").Start(otpsg.Correlate(ctx), "process-data")
defer child.End()
```

`FlowSpan(ctx) (trace.Span, bool)` is the read-only counterpart — reach for the
span's attributes or its `SpanContext` without making it active.

### Downstream of a funnel

Because the path-scoped value severs at a fan-in, `FlowSpan`/`Correlate` read
absent in work downstream of a funnel flush. If you need the span or its
`SpanContext` there, carry the `trace.SpanContext` yourself as part of the
accumulated data.

## Metrics and logging op decorators

`MetricsTask` / `MetricsSkim` / `MetricsFunnel` and `LoggedTask` / `LoggedSkim`
/ `LoggedFunnel` are flow-agnostic decorators over ordinary op bodies
(`func(ctx) (T, error)`, `streampool.HandlerFunc[T]`,
`streampool.AccumulatorFactory[T]`). Metrics use the global
`otel.GetMeterProvider()`; logging uses `zap.L()`.

```go
task := otpsg.MetricsTask("calculate-sum", myTaskFn) // records .count / .duration / .errors
skim := otpsg.LoggedSkim("handle-sum", mySkimFn)      // logs start / completion
```

## Combined instrumentation (metrics + logging)

`Instrumented*` stacks metrics over logging (metrics ∘ logging). It adds **no
tracing** — apply a span once at the flow level with `Traced` + `WithFlow`.

```go
task := otpsg.InstrumentedTask("calculate-sum", myTaskFn)     // func(ctx) (T, error)
skim := otpsg.InstrumentedSkim("handle-sum", mySkimFn)        // streampool.HandlerFunc[T]
funnel := otpsg.InstrumentedFunnel("acc", "flush", myFactory) // streampool.AccumulatorFactory[T]

skimmer := streampool.NewSkimmer(skim).In(&wave)
runner := streampool.NewTaskLauncher(func(ctx context.Context) error {
    result, err := task(ctx)
    return skimmer.SubmitResult(ctx, result, err)
})
err := runner.In(&wave).Start(ctx)
```

## API surface

| Function | Purpose |
| --- | --- |
| `Traced(ctx, name, opts...) (context.Context, []streampool.FlowOption)` | Start a flow span; returns ctx + the options that carry and end it. |
| `Correlate(ctx) context.Context` | Make the flow span active so `tracer.Start` creates a child (needed in async bodies). |
| `FlowSpan(ctx) (trace.Span, bool)` | Read the flow span (absent below a fan-in). |
| `MetricsTask` / `MetricsSkim` / `MetricsFunnel` | Metrics decorators. |
| `LoggedTask` / `LoggedSkim` / `LoggedFunnel` | Logging decorators. |
| `InstrumentedTask` / `InstrumentedSkim` / `InstrumentedFunnel` | Metrics ∘ logging (no tracing). |

See `example_tracing_test.go` for runnable end-to-end examples.
