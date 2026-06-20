# streampool Programming Model

streampool is a Go library for running large batches of related work concurrently
and processing the results as they arrive. The model is small: **a pool of workers
serves waves of work, and each wave hosts flows of related processing.** You
describe work as a few composable *ops*, submit inputs, and drain results — the
library handles worker scaling, backpressure, concurrency limits, cancellation, and
cleanup.

This is the conceptual guide: the core types, the op model, the patterns, and the
structural rules that make concurrent workflows reliable. For the permit/concurrency
internals see `permit-core.md` and `dispatch-execution-split.md`; for how streampool
compares to other libraries see `ARCHITECTURE_COMPARISON.md`.

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
- **Failure isolation** — errors propagate through well-defined boundaries, and a
  body panic is recovered and surfaced as an error.

## The model: Pool, Wave, Flow

Three concerns, three concepts:

- **Pool** — the worker pool that executes work. It is **internal**: a process-wide
  default pool exists implicitly and is sized automatically (workers spin up on
  demand and retire when idle). You don't construct or tune it; user-facing
  concurrency control is expressed with **Limiters** (below), not pool knobs.
- **Wave** — *the primary user-facing type*: a batch of work to complete together.
  You construct ops against a wave, submit inputs, and call `Skim`/`SkimAll` to
  process results and await completion. Waves **nest** via `NewChild`: a parent
  wave's drain waits for its child waves. Multiple waves run concurrently.
- **Flow** *(optional)* — one logical thread of related work, carried in context,
  independent of any single wave. Reach for a Flow when you need to attach metadata
  (trace context, audit data) to work that may cross wave boundaries, or run a
  cleanup hook when that unit of work completes. Most programs never construct one.

### The three ops

Work is described with three op types, all constructed against a wave and fed with
`Submit`:

- **Launcher** — runs a body for each submitted input, in parallel on pool workers.
  The body does the work and submits results downstream. This is the fan-out.
- **Skimmer** — the terminal sink. Its body runs **serially on the draining
  goroutine** (the one that called `Skim`/`SkimAll`), processing results one at a
  time. This is the fan-in.
- **Funnel** — incremental aggregation. Each instance is an `Accumulator` that folds
  submitted values and flushes an aggregate downstream (on a threshold, a deadline,
  or at close). This is the map-reduce primitive.

`Handler[T]` is the universal body interface (`Handle(ctx, T, err) error`), and
`HandlerFunc[T]` adapts a closure. Implementing `Handler` on a struct lets the hot
path run allocation-free.

## Hello world

```go
ctx := context.Background()

wave := streampool.NewWave(ctx) // uses the implicit default pool
defer wave.Close()

// Terminal sink: runs serially as results arrive.
results := streampool.NewSkimmer(wave, streampool.HandlerFunc[*User](
    func(ctx context.Context, user *User, err error) error {
        if err != nil {
            return err
        }
        fmt.Println(user.Name)
        return nil
    },
))

// Fan-out: each id is fetched on a worker, then submitted to results.
fetch := streampool.NewLauncher(wave, streampool.HandlerFunc[UserID](
    func(ctx context.Context, id UserID, err error) error {
        user, err := userClient.Fetch(ctx, id)
        if err != nil {
            return err
        }
        return results.Submit(ctx, user)
    },
))

for _, id := range userIDs {
    fetch.Submit(ctx, id)
}

wave.SkimAll(ctx) // drain to completion; workers retire when the last wave drains
```

## Patterns

### Fan-out / fan-in

The basic shape above: a Launcher distributes work across workers; a Skimmer
processes results serially. Parallel where it helps, sequential where it is simplest
to reason about.

### Incremental aggregation (Funnel)

To aggregate related results before emitting them:

```go
totals := streampool.NewFunnel(wave, func() streampool.Accumulator[int] {
    var sum int
    return streampool.NewAccumulator(
        func(ctx context.Context, x int, err error) (time.Time, error) {
            if err != nil {
                return time.Time{}, err
            }
            sum += x
            return time.Time{}, nil // no flush deadline; flush on Close
        },
        func(ctx context.Context) error { return results.Submit(ctx, sum) }, // final flush
    )
})
```

Each accumulator instance owns its own state (here `sum`); the framework creates
instances on demand by concurrency, so partial-aggregate memory is bounded by
concurrency. Flushing is the instance's job — a downstream `Submit` from either the
accumulate step (incremental) or the close step (final).

### Nested workflows and reentrancy

Any body — a Launcher handler, a Funnel accumulate/flush, or a Skimmer handler — may
submit more work and may drive a **child** wave it owns (create one with `NewChild`,
submit to it, and `SkimAll` it). This lets a workflow branch dynamically on
intermediate results while keeping structured-concurrency guarantees.

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
slowAPI := streampool.NewSemaphore(nil, 5) // at most 5 concurrent

fetch := streampool.NewLauncher(wave, fetchFn, streampool.WithLimits(slowAPI))
```

A single limiter is self-scheduled (`nil` scheduler). The same `Limiter` shared
across ops expresses "these collectively cap at N concurrent." Limiters compose with
AND semantics; composing several on one op requires a shared scheduler to coordinate
their joint admission deadlock-free (a future feature). Limiters gate **intake**
(Launcher bodies, Funnel accumulates); skim bodies and funnel flushes are
limiter-free, which is what keeps a drain from ever waiting on a permit.
Worker-pool sizing is automatic — limiters bound *your* concurrency, not the pool.

## Errors and cancellation

- **Body errors** propagate to the wave; a Skimmer body can inspect the `err`
  argument alongside the value and decide whether to surface or absorb it.
- **Context cancellation** propagates through all in-flight work; cancel the ctx
  given to the wave to abort.
- **Panics** in a body are recovered and converted to errors.
- **Cleanup is guaranteed**: a wave's drain does not return until its work (and its
  child waves) complete, even under cancellation — no leaked goroutines.

## Configuration and observability

- **Concurrency** is controlled with Limiters; worker-pool sizing is automatic and
  adaptive — there is no pool to tune.
- **Funnel flushing** is configured per accumulator (threshold / deadline / close).
- **Instrumentation**: streampool emits structured events (work lifecycle, queue
  depth, throughput) for monitoring and bottleneck analysis, and result processing
  is deterministic within a drain, which aids reproducible testing.

## See also

- `permit-core.md` — the permit allocation model (the hierarchical cache).
- `dispatch-execution-split.md` — the dispatch/execution architecture.
- `../ARCHITECTURE_COMPARISON.md` — how streampool compares to other concurrency
  libraries and to its internal foundations.
