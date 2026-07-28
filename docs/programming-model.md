# streampool Programming Model

streampool is a Go library for running large batches of related work concurrently
and processing the results as they arrive. The model is small: **a pool of workers
serves waves of work, and flows of related processing run through them.** You
describe work as a few composable *ops*, submit inputs, and drain results — the
library handles worker scaling, backpressure, concurrency limits, cancellation, and
cleanup.

This is the conceptual guide: the core types, the op model, the patterns, and the
structural rules that make concurrent workflows reliable. For the permit/concurrency
internals see `permit-core.md` and `dispatch-execution-split.md`; for how streampool
compares to other libraries see `docs/decisions/ARCHITECTURE_COMPARISON.md`.

## The problem

Concurrent workflows are easy to get wrong: races on shared state, deadlocks from
circular waits, unbounded resource growth, lost backpressure, and shutdown that
leaks goroutines or drops work. streampool makes the common fan-out/fan-in shape —
distribute many work items, process their results, optionally aggregate — safe and
efficient without hand-rolled goroutine/channel/lock choreography, while keeping the
hot path allocation-free.

## Structured concurrency

streampool is built on structured concurrency: concurrent work has a clear,
hierarchical lifetime, like structured control flow. The properties that follow:

- **Clear boundaries** — parallel execution and sequential result processing are
  explicit phases.
- **Hierarchical composition** — waves nest; a parent's drain waits for its
  children.
- **Deterministic coordination** — result processing within a single drain is
  sequential and ordered.
- **Resource accountability** — all submitted work is tracked and must complete
  before a wave's drain returns; no goroutine leaks.
- **Failure isolation** — errors propagate through well-defined boundaries.

## The model: Wave, flows, and the internal Pool

One user-facing type — **Wave** — plus the ops (the verbs); the worker **Pool** is
internal. Cross-cutting concerns ride **flows**, a facility rather than a type.

- **Wave** — *the primary user-facing type*: a batch of work to complete together.
  A zero-value `var w streampool.Wave` is ready to use — there is no constructor, and
  a Wave owns no context. Ops route work into a wave (ambient inside a body, or
  `op.In(&w)`); `wave.Skim` / `SkimAll` / `CloseAndSkimAll` drain it, returning
  `ErrWaveDone` when complete. Waves **nest** (a sub-wave is just a zero-value Wave
  first used inside a body; the body's drain of it keeps the parent drain waiting,
  transitively) and run concurrently. The lifecycle is the drain — there is no
  `Cancel` and no `Dup`; cancellation rides the driving context (below).
- **Flows** — the causal DAG of related work the framework already maintains as
  dispatches submit further work. Flows always exist and are never constructed; most
  programs never call the API. To attach a cross-cutting rider — a value such as a
  request context, or a completion hook that fires once all related work is done —
  open a lexical scope with `streampool.WithFlow(ctx, body, opts...)` and user-minted
  flow keys/tags. (Designed, not yet implemented; the full design is
  `docs/decisions/flow-design.md`.)
- **Pool** *(internal)* — the worker goroutines. A process-wide default Pool serves
  all work, sized automatically (spin up on demand, retire when idle). Not
  constructed or tuned; per-op concurrency is expressed with **Limiters**, not pool
  knobs.

### The three ops

Work is described with three op types. Ops are **wave-agnostic** — constructed
without a wave and reusable; work is routed to a wave at dispatch (the ambient wave
inside a body, or `op.In(wave)`). All are fed with `Submit`:

- **Launcher** — runs a body for each submitted input, in parallel on pool workers.
  The body does the work and submits results downstream. This is the fan-out.
- **Skimmer** — the terminal sink. Its body runs **serially on the draining
  goroutine** (the one that called `Skim`/`SkimAll`), processing results one at a
  time. This is the fan-in.
- **Funnel** — incremental aggregation. Each instance is an `Accumulator` that folds
  submitted values and flushes an aggregate downstream (on a threshold, a deadline,
  or when the wave drains). This is the map-reduce primitive.

`Handler[T]` is the universal body interface (`Handle(ctx, T, err) error`), and
`HandlerFunc[T]` adapts a closure. Implementing `Handler` on a struct lets the hot
path run allocation-free. By convention, name an op for its role as a noun distinct
from its output — Launcher: `fetcher`; Funnel: `aggregator` (+ `totals`); Skimmer:
`collector` (+ `results`) — so `op.Submit(...)` reads naturally.

## Hello world

```go
ctx := context.Background()

var wave streampool.Wave // zero value is ready to use; the worker pool is internal

// Terminal sink: runs serially on the draining goroutine as results arrive.
printer := streampool.NewSkimmer(streampool.HandlerFunc[*User](
    func(ctx context.Context, user *User, err error) error {
        if err != nil {
            return err
        }
        fmt.Println(user.Name)
        return nil
    },
))

// Fan-out: each id is fetched on a worker, then submitted to the printer.
fetcher := streampool.NewLauncher(streampool.HandlerFunc[UserID](
    func(ctx context.Context, id UserID, err error) error {
        if err != nil {
            return err
        }
        user, ferr := userClient.Fetch(ctx, id)
        if ferr != nil {
            return ferr
        }
        return printer.Submit(ctx, user) // ambient: lands in this body's wave
    },
))

// Top level has no ambient wave, so route with In(&wave):
for _, id := range userIDs {
    fetcher.In(&wave).Submit(ctx, id)
}

wave.CloseAndSkimAll(ctx) // seal + drain to completion
```

## Patterns

### Fan-out / fan-in

The basic shape above: a Launcher distributes work across workers; a Skimmer
processes results serially. Parallel where it helps, sequential where it is simplest
to reason about.

### Incremental aggregation (Funnel)

To aggregate related results before emitting them:

```go
aggregator := streampool.NewFnFunnel(&wave,
    func() streampool.Accumulator[int] {
        var sum int
        return streampool.NewAccumulator(
            func(ctx context.Context, x int, err error) (time.Time, error) {
                if err != nil {
                    return time.Time{}, err
                }
                sum += x
                return time.Time{}, nil // no deadline; flushed when the wave drains
            },
            func(ctx context.Context) error { return results.Submit(ctx, sum) }, // final flush
        )
    },
)
```

`NewFnFunnel` takes the factory closure directly; `NewFunnel` takes the
`AccumulatorFactory` interface instead. Each
accumulator instance owns its own state (here `sum`); the framework creates instances
on demand by concurrency, so partial-aggregate memory is bounded by concurrency.
Flushing is the instance's job — a downstream `Submit` from the accumulate step
(incremental) or the final-flush step; the wave force-flushes any not-yet-flushed
instances at its drain. Finalize early or snapshot into another wave with
`aggregator.Flush(ctx)` / `aggregator.FlushTo(ctx, out)`.

### Nested workflows and reentrancy

Any body — a Launcher handler, a Funnel accumulate/flush, or a Skimmer handler — may
submit more work and may drive a **sub-wave** it owns (declare a zero-value
`var sub streampool.Wave`, route into it with `op.In(&sub)`, and `CloseAndSkimAll`
it). This lets a workflow branch dynamically on intermediate results while keeping
structured-concurrency guarantees.

## Key rules

### You cannot skim a wave you are part of

The one structural restriction on reentrancy: a body may not skim **its own wave or
any ancestor wave** — the only thing that would make a drain wait, transitively, on
itself. Driving an *independent child* wave you created is fine; submitting into any
wave is fine; the non-blocking `Try*` skims are never restricted. Why this single
rule is enough is covered in `dispatch-execution-split.md` and `permit-core.md`.

### Serial skim

A wave's results are processed serially on the draining goroutine — one at a time,
in completion order. That eliminates races on the skim body's state and keeps result
processing simple to reason about; keep skim bodies efficient, as they are the
sequential stage. (Multiple goroutines *may* drive the same wave concurrently when
you need it, but then your skim bodies must be concurrency-safe.)

### Deadlock freedom

streampool prevents the classic concurrency deadlocks structurally, not by asking
you to order locks. Dispatch is separated from execution — an always-live manager
keeps admitting work, so a blocking body never stalls the dispatcher; permits form a
deadlock-free-per-limiter cache; limiters gate *intake* only, never the drain; and
the skim rule above forecloses the only reentrant cycle. The mechanics live in
`permit-core.md` and `dispatch-execution-split.md`.

## Concurrency control: Limiters

User-facing concurrency is expressed with **Limiters**, attached to an op:

```go
slowAPI := streampool.NewSemaphore(5) // at most 5 concurrent

fetcher := streampool.NewLauncher(fetchFn, streampool.WithLimits(slowAPI))
```

A limiter is a standalone value. The same `Limiter` shared across ops expresses
"these collectively cap at N concurrent"; several on one op compose with AND
semantics, admitted jointly in a global order (deadlock-free, automatic — no
coordinator to construct). Limiters gate **intake** (Launcher bodies, Funnel
accumulates); skim bodies and funnel flushes are limiter-free, which is what keeps a
drain from ever waiting on a permit. Worker-pool sizing is automatic — limiters bound
*your* concurrency, not the pool.

## Errors and cancellation

- **Body errors** propagate to the wave; a Skimmer body can inspect the `err`
  argument alongside the value and decide whether to surface or absorb it.
- **Context cancellation** propagates through all in-flight work by context
  ancestry; cancel the ctx you dispatched the work with (usually the same ctx you
  drive the wave with) to abort. The Wave owns no context of its own.
- **Panics** in a body are not recovered — they unwind normally (framework
  cleanup along the unwind keeps accounting sound). A body that wants per-task
  isolation recovers for itself; the framework never masks a bug as an error.
- **Cleanup is guaranteed**: a wave's drain does not return until its work (and its
  child waves) complete, even under cancellation — no leaked goroutines.

## Configuration and observability

- **Concurrency** is controlled with Limiters; worker-pool sizing is automatic and
  adaptive — there is no pool to tune.
- **Funnel flushing** is per accumulator (threshold / deadline) plus a force-flush
  at the wave's drain; finalize early or snapshot with `Flush` / `FlushTo`.
- **Instrumentation**: streampool emits structured events (work lifecycle, queue
  depth, throughput) for monitoring and bottleneck analysis, and result processing
  is deterministic within a drain, which aids reproducible testing.

## See also

- `permit-core.md` — the permit allocation model (the hierarchical cache).
- `dispatch-execution-split.md` — the dispatch/execution architecture.
- `docs/decisions/ARCHITECTURE_COMPARISON.md` — how streampool compares to other concurrency
  libraries and to its internal foundations.
