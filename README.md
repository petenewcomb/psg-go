[![Go Reference][godev-badge]][godev]
[![Go Report Card][goreport-badge]][goreport]
[![CI][ci-badge]][ci]
[![Coverage][coverage-badge]][coverage]
[![License][license-badge]][license]

# streampool

**A Go worker pool whose tasks return values — and can submit more tasks
without deadlocking.** Results stream back to your code as they complete.

If you've reached for [`errgroup`][errgroup], [`conc`][conc],
[`ants`][ants], [`pond`][pond], or [`workerpool-go`][workerpool-go] and hit
one of these walls:

- "I want my tasks to return values, not just errors."
- "When my tasks try to submit child tasks, the pool deadlocks."
- "I want to handle results as they arrive, not wait for everything."
- "Rate-limited API calls and heavy local work share one concurrency limit
  and starve each other."

…this library is built for you.

## Hello world

``` go
ctx := context.Background()

wave := streampool.NewWave(ctx)   // a batch of work to await; the worker pool is internal

var results []string
collector := streampool.NewSkimmer(streampool.HandlerFunc[string](
    func(ctx context.Context, s string, err error) error {
        if err != nil { return err }
        results = append(results, s)
        return nil
    },
))

greeter := streampool.NewLauncher(streampool.HandlerFunc[string](
    func(ctx context.Context, s string, err error) error {
        if err != nil { return err }
        time.Sleep(1 * time.Millisecond)
        return collector.Submit(ctx, s)   // ambient: lands in this body's wave
    },
))

greeter.In(wave).Submit(ctx, "Hello")     // top level: route with In(wave)
greeter.In(wave).Submit(ctx, "world!")

wave.CloseAndSkimAll(ctx)                  // seal + drain to completion
fmt.Println(strings.Join(results, " "))
```

The worker pool is implicit and automatically sized; you don't construct
or tune it. For a richer walkthrough see the [Observable example][observable-source]
and the rest of the [reference documentation][godev], and `docs/programming-model.md`
for the full guide.

## The model

streampool enables frictionless execution of recursive streams of work
in hierarchical waves with granular visibility and control of individual
flows. Three types compose:

- **`Wave`** — a batch of work to be completed together; the user-primary
  type, a value handle. Ops route work into a wave (the ambient wave inside a
  body, or `op.In(wave)`); `wave.Skim` / `wave.SkimAll` / `wave.CloseAndSkimAll`
  drain it. Waves nest (`wave.NewChild`) and may overlap freely.
- **`Flow`** — optional, ctx-borne lifecycle entity for a single
  workflow instance. Refcounted, can span multiple Waves. Use a Flow
  to attach trace context, audit metadata, or cleanup hooks to a
  logical unit of work that may cross batch boundaries.
- **Pool** — the workers. Internal and fungible: a process-wide default
  is used implicitly, sized automatically (adaptive with demand from zero,
  retiring idle workers; lifecycle is refcount-driven). You don't construct
  or tune it — per-op concurrency is controlled with Limiters instead.

Ops are **wave-agnostic** — define them once (no wave at construction) and reuse
them; three op types compose pipelines:

- **`Launcher[T]`** — stateless dispatch. Each `Submit` runs the body
  (`Handler[T]` / `HandlerFunc[T]`) on the pool's workers, in parallel.
- **`Funnel[T]`** — stateful aggregation. A factory returns per-instance
  accumulators (each with private state) that fold inputs and emit on
  flush. Parallel by default; cap concurrency to 1 with a limiter for
  strict-serial ("reducer") behavior.
- **`Skimmer[T]`** — terminal sink. Its body runs on the draining
  goroutine when you `wave.Skim(ctx)` or `wave.SkimAll(ctx)`.

Function bodies route values explicitly by calling `sink.Submit(ctx, v)`
(or `sink.SubmitErr(ctx, v, err)` for value+error pairs) on any downstream op
in scope. No declarative wiring step; the dataflow lives in the code that
produces values. Inside a body, `Submit` targets the ambient wave; use
`op.In(wave)` to place work at top level or redirect into another wave —
routing never disturbs the ctx, so a Flow rides along across the hop.

## What you get

- **Tasks return values.** Provide a `Handler[T]` (or a `HandlerFunc[T]`
  closure) whose body calls `sink.Submit(ctx, value)` on whatever
  destinations matter. Multiple outputs, conditional routing, and
  dynamic destinations all work naturally — none of which fit through
  a `.To(sink)` wiring step.
- **Streaming results.** Your skimmer's `Handler` runs on each value
  as it arrives. You can validate, aggregate, or short-circuit
  incrementally instead of waiting for a full slice.
- **Recursive task submission.** Inside any body you can `Submit` to any
  sink you have, and drive child Waves you create. The one structural
  rule is that you can't skim a Wave you are part of (own or ancestor) —
  which is exactly the cycle that would deadlock. Useful for tree walks,
  crawlers, and multi-stage pipelines.
- **Composable concurrency limits.** Bind one or more `Limiter`s to an
  op via `WithLimits(...)`. Limiters compose: an op can be subject to a
  per-API semaphore *and* a global rate limit simultaneously. Multiple
  ops can share a Limiter to express a collective cap.
- **Adaptive pool sizing.** The worker pool scales with demand from zero and
  retires goroutines when idle — automatically, with no pool to construct or
  tune. GC-aware and load-aware backpressure are planned as `Limiter` types,
  letting users opt in per-op or share one across multiple ops for process-wide
  coordination.
- **End-to-end backpressure.** Workers don't just scale to local queue
  depth — the pool right-sizes to wherever the actual bottleneck is.
  Admission is paced by an always-live manager: top-level submission
  waits while a downstream stage is saturated, and funnel queues
  back-pressure their upstream Submits. Pipelines stay paced to the slowest
  downstream stage with no manual buffer-sizing.
- **Allocation-free hot path.** Op bindings, funnel state, queue nodes,
  and skim-callback wrappers are all recycled through pooled storage.
  With a typed argument (or a pooled argument struct), the user's
  dispatch loop runs near zero allocations per call — see the
  allocation-sensitive dispatch section in the reference docs.
- **Type-safe.** Generics throughout; no `interface{}` round trips.
- **Context-aware.** Cancellation propagates; bodies see the wave's
  context.
- **Panic-safe.** Panics in user code don't crash the process.

## How it compares

| | `errgroup` | `conc` | `ants` / `pond` | `workerpool-go` | streampool |
|---|:---:|:---:|:---:|:---:|:---:|
| Tasks return values | ❌ | partial[^1] | ❌ | ✅ | ✅ |
| Results stream as they arrive | ❌ | partial[^2] | ❌ | ✅[^3] | ✅[^4] |
| Workers can submit child tasks safely | ⚠️[^5] | ⚠️[^5] | ⚠️[^5] | ⚠️[^5] | ✅ |
| Composable concurrency limits | one[^6] | one | one | one[^7] | ✅[^8] |
| Adaptive pool sizing | ❌ | ❌ | ✅[^a] | ✅ | ✅[^b] |
| End-to-end backpressure | ❌ | ❌ | ❌[^c] | partial[^d] | ✅[^e] |
| Steady-state contention | low | medium | med-high / high[^f] | medium[^g] | low[^h] |
| Per-task allocations | high | low | low / med-high[^i] | low-medium | near-zero[^j] |
| Panic-safe | ❌ | ✅ | ✅ | ❌ | ✅ |
| Generics | ❌ | ✅ | partial | ✅ | ✅ |
| Actively maintained (as of 2026) | ✅ | ⚠️[^9] | ✅ | ✅ | ✅ |

[^1]: `conc.ResultPool` collects results into a slice; ordered streaming is
  a separate type (`stream`) that doesn't surface results.
[^2]: See [sourcegraph/conc#115](https://github.com/sourcegraph/conc/issues/115).
[^3]: Via a `Results` channel the caller must consume concurrently or
  deadlock.
[^4]: Via a gather callback the framework invokes; no consumer goroutine
  to manage.
[^5]: All bounded pools can deadlock if a task blocks waiting on a child
  task that can't be scheduled because the pool is full.
[^6]: errgroup added `SetLimit` after launch; remains a single limit per
  group.
[^7]: Pool-level concurrency cap only; per-task limits and shared limits
  across tasks aren't expressible.
[^8]: First-class `Limiter` type bound per-op via `WithLimits(...)`.
  Multiple limiters per op (AND semantics), shared limiters across ops
  (collective caps), and user-defined limiter types all compose.
[^9]: See [sourcegraph/conc#148](https://github.com/sourcegraph/conc/issues/148).
[^a]: Ceiling-only: workers spawn on demand up to a configurable maximum
  and retire when idle. No configurable floor (`pond` removed `MinWorkers`
  in v2). Ceiling is tunable at runtime (`ants.Tune`, `pond.Resize`).
[^b]: Pool-wide adaptive worker count scaling with demand from zero, internal
  and not user-tuned. No separate task/funnel pool types to manage; per-op
  concurrency control lives in composable Limiters instead.
[^c]: Local queue-depth scaling; no awareness of downstream consumption.
  Queues can fill while consumers fall behind.
[^d]: Detects when its own `Results` channel is blocked and reduces worker
  count to `0.9 × current`; pipelines back-pressure between pools via
  bounded blocking channels (`jobsNew` capacity 2). No coordination beyond
  sequential pipelines.
[^e]: The pool right-sizes to the workflow bottleneck. An always-live
  manager paces admission — top-level submission waits while a downstream
  stage is saturated; funnel queues back-pressure their upstream Submits.
[^f]: `ants` takes a spin lock on every submit *and* every completion;
  `pond` takes a `sync.Mutex` on every submit and every `readTask`. Both
  serialize dispatch through a single shared lock.
[^g]: All submissions, results, and scaling decisions flow through one
  central `loop` goroutine multiplexing `select`; `jobsNew` channel
  capacity is 2.
[^h]: Hot path uses a Michael-Scott lock-free queue with 128-bit atomic
  CAS (`nbcq`) over per-sender private outbox channels. No shared mutex
  on submit / pickup / result paths.
[^i]: `ants` pools worker structs via `sync.Pool` and passes tasks as raw
  `func()` through per-worker channels; `pond` allocates a `Future`, a
  `resolve` closure, a `wrapTask` closure, and a buffer slot per Submit.
[^j]: Per-task wrappers, queue nodes, and value-pointers are recycled
  through `omnipool` (`sync.Pool` + `Reset`). Bound Launcher+arg
  dispatch keeps user closures off the heap on the hot path too.

## When *not* to use streampool

- You just need to run N goroutines and wait. Use `errgroup`.
- All your tasks are the same shape, fire-and-forget, no results needed.
  Use `ants` or `pond`.
- Your task graph is fully known up front and result order matters more
  than streaming. Use `conc.ResultPool` or a fan-in pattern.

streampool pays off when results matter, when tasks aren't uniform, or
when one task's result determines whether you submit more.

## License

Copyright (c) Peter Newcomb. All rights reserved.

Licensed under the MIT License.

## Contributing

Contributions, including feedback, are welcome. Please feel free to start
or join a [discussion][discussions], create an [issue][issues], or submit a
[pull request][pull requests].

[godev-badge]: https://pkg.go.dev/badge/github.com/petenewcomb/streampool.svg
[godev]: https://pkg.go.dev/github.com/petenewcomb/streampool#section-documentation
[goreport-badge]: https://goreportcard.com/badge/github.com/petenewcomb/streampool
[goreport]: https://goreportcard.com/report/github.com/petenewcomb/streampool
[ci-badge]: https://github.com/petenewcomb/streampool/actions/workflows/ci.yml/badge.svg
[ci]: https://github.com/petenewcomb/streampool/actions/workflows/ci.yml
[coverage-badge]: https://github.com/petenewcomb/streampool/wiki/coverage.svg
[coverage]: https://raw.githack.com/wiki/petenewcomb/streampool/coverage.html
[license-badge]: https://img.shields.io/github/license/mashape/apistatus.svg
[license]: https://opensource.org/licenses/MIT
[observable-source]: ./example_observable_test.go
[errgroup]: https://pkg.go.dev/golang.org/x/sync/errgroup
[conc]: https://pkg.go.dev/github.com/sourcegraph/conc
[ants]: https://pkg.go.dev/github.com/panjf2000/ants
[pond]: https://pkg.go.dev/github.com/alitto/pond
[workerpool-go]: https://github.com/cmitsakis/workerpool-go
[discussions]: https://github.com/petenewcomb/streampool/discussions
[issues]: https://github.com/petenewcomb/streampool/issues
[pull requests]: https://github.com/petenewcomb/streampool/pulls
