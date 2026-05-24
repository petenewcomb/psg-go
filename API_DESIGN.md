# API Design

The proposed API for the repositioned library (working name `streampool`,
formerly `psg-go`). Captures naming decisions, the final user-facing
surface, and the reasoning behind each choice.

This is a design artifact, not an implementation plan. The decisions here
are still revisable, but they form a coherent set — pulling one thread
often unravels several.

Companion docs:
- `POSITIONING_RESEARCH.md` — outward-facing market/audience research.
- `ARCHITECTURE_COMPARISON.md` — source-level contention/allocation
  analysis against competitors.

---

## Design principles

These guided every naming and shape decision below.

1. **User vocabulary first.** Pool, worker, task, submit, results,
   limiter — words developers actually type into search engines and use
   in issues. Avoid academic vocabulary (`scatter-gather`, `pipelined`,
   `monoid`) in the API surface.

2. **End-to-end backpressure is the differentiator.** Make the
   pool's adaptive sizing and gather-driven backpressure visible in the
   docs; let the API stay simple.

3. **No silent failures.** Forgetting a method call shouldn't cause data
   loss. If a method is structurally required for correctness, make it
   either positional or compile-time enforced.

4. **Match Go convention.** Functional options over builders for
   per-op configuration. Concrete return types, structural interfaces
   defined by consumers. `package.New` as primary constructor.

5. **Asymmetry tracks role.** Where roles genuinely differ (Task is
   stateless; Combiner has accumulator state; Gatherer is terminal), the
   API reflects that. Don't force uniformity that hides real
   differences.

6. **Allocations are a feature.** The bound-op pattern, combiner-factory
   closures, and pool of internal state are all designed so user code
   can run allocation-free on the hot path. This is psg-go's
   defensible technical differentiator.

---

## The final API surface

```go
package streampool

// ===== Top-level container =====

// Pool is the streampool — a bounded context that adaptively manages a
// set of worker goroutines for the ops constructed against it. Every
// op-construction call takes a *Pool as its first arg.
type Pool struct { /* ... */ }

func New(ctx context.Context, opts ...PoolOption) *Pool

func (*Pool) Gather(ctx context.Context) error
func (*Pool) GatherAll(ctx context.Context) error
func (*Pool) Close()
func (*Pool) CancelAndWait()

// Pool options
type PoolOption interface { /* ... */ }

func WithMaxGoroutines(n int) PoolOption
func WithIdleTimeout(d time.Duration) PoolOption
// ... additional adaptive-tuning options as they're stabilized

// ===== Limiters =====

// Limiter is the user-facing concurrency-control primitive. Limiters
// compose: an op can bind multiple Limiters, all of which must permit a
// dispatch before it proceeds. Users obtain Limiter values from
// framework constructors (NewSemaphore, NewRateLimit, etc.) and pass
// them to WithLimits.
//
// In v0.x the internal contract is closed — users cannot implement
// their own Limiter types. This keeps the framework free to evolve the
// internal acquire/notify machinery without breaking users. Custom
// concurrency logic that doesn't fit the built-ins should live inside
// the user's TaskFunc / GatherFunc / Accumulate body, calling whatever
// blocking primitive (rate.Limiter.Wait, semaphore.Weighted.Acquire,
// custom) is appropriate.
type Limiter struct {
    impl limiterImpl  // unexported; sealed against external implementations
}

func NewSemaphore(n int) Limiter           // generalization of TaskPool's mechanism
func NewRateLimit(n int, d time.Duration) Limiter   // wraps x/time/rate; signals replenishment via timer

// Future: NewAdaptive(...) for GC/load-aware backpressure, when ready.
// Future: opening the interface for external implementations once the
// contract is stable.

// ===== User-supplied interfaces =====
//
// All user inputs are defined as interfaces, not function signatures.
// This lets users implement on structs (with state as fields) to avoid
// closure allocations on the hot path. Function-type wrappers are
// provided for the simple closure case, matching the http.Handler /
// http.HandlerFunc pattern.

// Task interfaces — implemented by user-supplied work. The framework
// calls Run synchronously on a worker goroutine when dispatching.
// (The user-facing async dispatch verb is TaskRunner.Start; the
// interface uses Run because at this level it is synchronous code on
// a worker.)
type Task0 interface {
    Run(ctx context.Context) error
}
type Task[T any] interface {
    Run(ctx context.Context, arg T) error
}
type Task2[T1, T2 any] interface {
    Run(ctx context.Context, arg1 T1, arg2 T2) error
}

// Function-type wrappers (satisfy the corresponding interfaces).
type TaskFunc0           func(context.Context) error
type TaskFunc[T any]     func(context.Context, T) error
type TaskFunc2[T1, T2 any] func(context.Context, T1, T2) error

func (f TaskFunc0) Run(ctx context.Context) error                   { return f(ctx) }
func (f TaskFunc[T]) Run(ctx context.Context, arg T) error          { return f(ctx, arg) }
func (f TaskFunc2[T1, T2]) Run(ctx context.Context, a1 T1, a2 T2) error { return f(ctx, a1, a2) }

// Accumulator — the per-instance interface a CombinerFactory returns.
// Mirrors the existing psgfn.Combiner shape: Accumulate per input,
// Flush when the framework needs the instance to finalize.
type Accumulator[T any] interface {
    Accumulate(ctx context.Context, value T, err error) (deadline time.Time, returnErr error)
    Flush(ctx context.Context) error
}

type CombinerFactory[T any] = func() Accumulator[T]

// FuncAccumulator builds an Accumulator from function fields; FlushFn
// is optional. Convenience for the common closure-over-state case.
type FuncAccumulator[T any] struct {
    AccumulateFn func(ctx context.Context, value T, err error) (time.Time, error)
    FlushFn      func(ctx context.Context) error
}

func (f FuncAccumulator[T]) Accumulate(ctx context.Context, value T, err error) (time.Time, error) {
    return f.AccumulateFn(ctx, value, err)
}
func (f FuncAccumulator[T]) Flush(ctx context.Context) error {
    if f.FlushFn == nil { return nil }
    return f.FlushFn(ctx)
}

// Handler — the interface a Gatherer dispatches to when the user calls
// Pool.Gather / Pool.GatherAll. Receives upstream values paired with
// any upstream errors.
type Handler[T any] interface {
    Handle(ctx context.Context, value T, err error) error
}

type HandlerFunc[T any] func(context.Context, T, error) error

func (f HandlerFunc[T]) Handle(ctx context.Context, value T, err error) error {
    return f(ctx, value, err)
}

// ===== Op constructors =====
//
// All constructors take the user-supplied interface as canonical input.
// For raw closures, wrap in the corresponding *Func type (TaskFunc[T],
// HandlerFunc[T], FuncAccumulator[T]) at the call site.

// Per-op options (functional)
type OpOption interface { /* ... */ }

func WithLimits(limiters ...Limiter) OpOption
// ... future: WithPriority, WithDeadline, WithRetry, etc.

func NewTaskRunner0(pool *Pool, task Task0, opts ...OpOption) TaskRunner0
func NewTaskRunner[T any](pool *Pool, task Task[T], opts ...OpOption) TaskRunner[T]
func NewTaskRunner2[T1, T2 any](pool *Pool, task Task2[T1, T2], opts ...OpOption) TaskRunner2[T1, T2]
func NewCombiner[T any](pool *Pool, factory CombinerFactory[T], opts ...OpOption) Combiner[T]
func NewGatherer[T any](pool *Pool, handler Handler[T], opts ...OpOption) Gatherer[T]

// For "reducer" behavior — strictly serial accumulation, only one
// instance active at a time — construct a Combiner with a 1-permit
// limiter:
//
//   ordered := streampool.NewCombiner(pool, factory,
//       streampool.WithLimits(streampool.NewSemaphore(1)),
//   )

// ===== Op types =====
//
// TaskRunners use Start (active dispatch). Combiner and Gatherer use
// Offer/OfferErr (passive sink reception). The verb split tracks the
// op's role: dispatchers run; sinks accept offered values.

type TaskRunner0 struct { /* ... */ }
func (TaskRunner0) Start(ctx context.Context) error

type TaskRunner[T any] struct { /* ... */ }
func (TaskRunner[T]) Start(ctx context.Context, arg T) error

type TaskRunner2[T1, T2 any] struct { /* ... */ }
func (TaskRunner2[T1, T2]) Start(ctx context.Context, arg1 T1, arg2 T2) error

// Combiner — stateful aggregation via factory-created Accumulator
// instances. Parallel by default; cap parallelism via WithLimits.
type Combiner[T any] struct { /* ... */ }
func (Combiner[T]) Submit(ctx context.Context, value T) error                 // sugar for SubmitErr(ctx, value, nil)
func (Combiner[T]) SubmitErr(ctx context.Context, value T, err error) error
func (Combiner[T]) Close()                                                    // signals no more input; triggers Flush on each instance
func (Combiner[T]) Dup() Combiner[T]                                          // refcounted sharing across handlers

// Gatherer — terminal sink; Handler is dispatched on Pool.Gather pull.
type Gatherer[T any] struct { /* ... */ }
func (Gatherer[T]) Submit(ctx context.Context, value T) error                 // sugar for SubmitErr(ctx, value, nil)
func (Gatherer[T]) SubmitErr(ctx context.Context, value T, err error) error
func (Gatherer[T]) Close()                                                    // signals no more input; GatherAll branch completes
func (Gatherer[T]) Dup() Gatherer[T]                                          // refcounted sharing across handlers
```

## Hello world

```go
ctx := context.Background()
pool := streampool.New(ctx)
defer pool.CancelAndWait()

results := streampool.NewGatherer(pool, streampool.HandlerFunc[*User](
    func(ctx context.Context, user *User, err error) error {
        if err != nil { return err }
        fmt.Println(user.Name)
        return nil
    },
))

fetch := streampool.NewTaskRunner(pool, streampool.TaskFunc[UserID](
    func(ctx context.Context, id UserID) error {
        user, err := userClient.Fetch(ctx, id)
        return results.SubmitErr(ctx, user, err)
    },
))

for _, id := range userIDs {
    fetch.Start(ctx, id)
}

results.Close()
pool.GatherAll(ctx)
```

## With a combiner

```go
totals := streampool.NewCombiner(pool, func() streampool.Accumulator[int] {
    var sum int
    return streampool.FuncAccumulator[int]{
        AccumulateFn: func(ctx context.Context, x int, err error) (time.Time, error) {
            if err != nil { return time.Time{}, err }
            sum += x
            if sum >= flushThreshold {
                if err := results.Submit(ctx, sum); err != nil {
                    return time.Time{}, err
                }
                sum = 0
            }
            return time.Time{}, nil  // no flush deadline; flush only on Close
        },
        FlushFn: func(ctx context.Context) error {
            if sum != 0 {
                return results.Submit(ctx, sum)
            }
            return nil
        },
    }
})

score := streampool.NewTaskRunner(pool, streampool.TaskFunc[UserID](
    func(ctx context.Context, id UserID) error {
        user, err := userClient.Fetch(ctx, id)
        if err != nil { return err }
        return totals.Submit(ctx, user.Score)
    },
))
```

The combiner factory creates a fresh `FuncAccumulator` (with its own
closure-captured `sum`) each time the framework needs a new instance.
Flushing is the instance's responsibility, expressed as a downstream
`Submit` in either AccumulateFn (incremental flushes) or FlushFn (final
flush on Close).

## Allocation-free dispatch

To avoid per-call closure allocations on the hot path, implement the
interface directly on a struct rather than wrapping a closure:

```go
type fetcher struct {
    db *Database
    sink streampool.Gatherer[*User]
}

func (f *fetcher) Run(ctx context.Context, id UserID) error {
    user, err := f.db.Fetch(ctx, id)
    return f.sink.SubmitErr(ctx, user, err)
}

fetch := streampool.NewTaskRunner(pool, &fetcher{db: db, sink: results})

for _, id := range userIDs {
    fetch.Start(ctx, id)  // no closure allocation per call
}
```

Combined with a pooled argument type, the dispatch loop can run with
zero allocations per call.

## With limiters

```go
slowAPI := streampool.NewSemaphore(5)
apiRate := streampool.NewRateLimit(100, time.Second)

fetch := streampool.NewTaskRunner(pool, fetchFn,
    streampool.WithLimits(slowAPI, apiRate),
)
```

Limiters compose with AND semantics: a dispatch proceeds only when all
attached limiters permit. The same `Limiter` instance can be shared
across multiple ops, expressing "these collectively cap at N concurrent
operations."

---

## Naming decisions

| Decision | Choice | Reasoning |
|---|---|---|
| Package name | `streampool` | Matches user-vocabulary search terms (worker pool / streaming results); distinct on pkg.go.dev; encodes the differentiator without being cute. |
| Top-level type | `Pool` (in package `streampool`) | Stdlib convention allows package=type pairing (`container/list.List`, `sync.Pool`). The `streampool.New` idiom means the type name is rarely uttered. |
| Top-level type **not** Job | The streams positioning makes the spool a "pool of streams" bounded context; Job is task-centric vocabulary that fights the framing. |
| Worker pool | Implicit; no public `WorkerPool` type | The spool *is* the worker pool. Adaptive goroutine management is a property of the spool, configurable via `PoolOption`. No second pool type to construct. |
| Op constructor verb | `NewTaskRunner`, `NewCombiner`, `NewGatherer` | Agent nouns (`-er` suffix). The type names describe roles, not the function-call verb. Matches `http.Handler`, `io.Reader`, `sync.Mutex`. |
| Op constructor first arg | `pool *Pool` | Positional and required across all three op constructors. No `.In(pool)` chain. Mirrors how `ctx` is conventionally first; `pool` is the next-most-central reference. |
| TaskRunner dispatch verb | `Start` | Active async dispatch ("start a task with this arg"). Matches `os/exec.Cmd.Start()` precedent — fire-it-off-async-don't-wait. Works across arities including the no-arg case (`runner0.Start(ctx)`). |
| Sink dispatch verb | `Submit` / `SubmitErr` | Committed-delivery semantics: "submit this value to the sink." Avoids the Java `BlockingQueue.offer` baggage that would mislead users to expect try-semantics from `Offer`. No collision with TaskRunner verb since TaskRunner uses Start. |
| Interface method verbs | `Run` (Task), `Accumulate`/`Flush` (Accumulator), `Handle` (Handler) | Sync execution verbs on user-implemented interfaces, mirroring the http.Handler.ServeHTTP / exec.Cmd inner-process pattern: async dispatch on the op (Start, Submit), sync invocation on the implementation (Run, Accumulate, Handle). |
| User inputs as interfaces | `Task[T]`, `Task0`, `Task2[T1,T2]`, `Accumulator[T]`, `Handler[T]` | Function signatures forced closure allocations for any stateful task. Interfaces let users implement on structs with state as fields (alloc-free hot path). Function-type wrappers (`TaskFunc[T]`, `HandlerFunc[T]`, `FuncAccumulator[T]`) provide the closure-based convenience for simple cases. Same pattern as http.Handler / http.HandlerFunc. |
| No `.To(sink)` wiring | Function bodies call `sink.Offer(ctx, value)` directly | Enables multi-output ops, conditional routing, zero-output paths. Cost: wiring is no longer visible at construction; users read function bodies to trace dataflow. Worth it for the flexibility and the elimination of the output type parameter on Combiner. |
| No `Sink[T]` in public API | Not exported | Nothing in the framework's own API consumes a Sink type. User code that wants polymorphism over "things you can Offer to" defines a one-method interface locally; Go's structural typing makes that work without a published contract. |
| Combiner state | Via `CombinerFactory[T]` returning `Accumulator[T]` | Factory creates per-instance Accumulators (each with closure state); framework calls each instance's `Combine` per input and `Flush` on Close. Same shape as the existing `psgfn.Combiner` interface. |
| Serial accumulation ("reducer") | Combiner with `WithLimits(NewSemaphore(1))` | No separate Reducer type. The "only one instance active at a time" property is enforced by a 1-permit limiter, reusing the Limiter abstraction. Same factory, same Accumulator interface — only the concurrency cap differs. |
| TaskRunner.Close | Not present | The framework can't deduce what sinks a task body will Offer to, so closing the TaskRunner tells the framework nothing useful. Resources release via leakguard finalizer when the value falls out of scope. |
| Combiner.Close / Gatherer.Close | Present, type-specific behavior | Combiner.Close triggers final accumulator flush. Gatherer.Close signals end-of-input to GatherAll. Both are semantically meaningful because the op has its own state to finalize. |
| Dup() on Combiner / Gatherer | Present | Refcounted-handle pattern earns its place in psgwf-style scenarios where ops cross handler boundaries with independent lifecycles. Single-scope usage doesn't require Dup; advanced shared usage does. |
| Per-op configuration | Functional options on constructor (`opts ...OpOption`) | Standard Go idiom for multiple optional parameters. Composable, extensible without breaking existing call sites. |
| Limiters | First-class entity, not a pool property | Limiters compose; pools don't. Multiple Tasks can share a Limiter; an op can have multiple Limiters; new Limiter types extend the system without core-API changes. |
| Type parameter convention | `T` (or `T1`, `T2`) | Default to T per user preference. Use T1/T2 for binary variants like `TaskRunner2`, not `T, U`, for explicit naming under refactor. |

---

## Mapping from psg-go

| Old (psg-go) | New (streampool) | Notes |
|---|---|---|
| `psg.NewJob(ctx)` | `streampool.New(ctx, opts...)` | Top-level constructor unchanged in spirit. |
| `*Job` | `*Pool` | The bounded context. |
| `psg.NewPool` / `psg.NewTaskPool` | (removed) | Spool IS the worker pool. Adaptive management built in. |
| `psg.NewCombinerPool` | (removed) | Same — combiner workloads run in the spool's goroutine pool. |
| `psg.NewGatherOp(handler)` | `streampool.NewGatherer(pool, handler)` | Spool now required at construction; handler signature simplified. |
| `psg.NewCombineOp(gather, pool, factory)` | `streampool.NewCombiner(pool, factory)` | Output type parameter gone; downstream sink wired via factory's closure. |
| `gatherOp.Scatter(ctx, job, taskFn)` | `streampool.NewTaskRunner(pool, taskFn)` + `task.Offer(ctx, arg)` | Two-step: construct the runner once; dispatch with arg. |
| `*GatherOp[T]` | `Gatherer[T]` | Op-suffix dropped; agent noun. |
| `*CombineOp[I, O]` | `Combiner[T]` | Output type parameter eliminated. |
| (n/a) | `TaskRunner[T]` / `TaskRunner2[T1, T2]` | Stateless dispatch op surfaced as a first-class type. |
| `psgfn.Task[T]` | `TaskFunc[T]` | Naming convention: function-signature types end in `Func`. |
| `psgfn.CombinerFactory[I, O]` | `CombinerFactory[T]` | Output type removed. |
| `psgwf` package | TBD | Workflow utilities adapt to new API; ops still refcounted via Dup/Close. |
| `otpsg` package | `otstreampool`? | OpenTelemetry integration; rename TBD. |
| `psgopt` package | (folded into main package) | Option types live with the package they configure. |

---

## What we chose not to do

Captured here so future readers (including future you) can see what was
considered and rejected, and re-examine if circumstances change.

### `.To(sink)` for declarative wiring

**Rejected**: keeping `.To(sink)` as the auto-routing wiring step
(builder-pattern with typestate, where `.To` consumes the typed-output of
a Task/Combine and routes its return value to the destination).

**Reason**: limits ops to a single output type, prevents multi-sink fan-
out, blocks conditional routing. The explicit-Offer model gives all of
those capabilities at the cost of losing the at-a-glance declarative
wiring. Worth the trade.

### Fluent typestate builder (`NewTaskRunner(fn).In(pool).To(sink)`)

**Rejected**: phantom-typed builder phases enforcing `.In` then `.To` at
compile time.

**Reason**: with `.To` gone and `pool` positional, there's nothing left
to enforce in a builder chain. The constructor signature already
captures everything.

### `Sink[T]` interface in public API

**Rejected**: exporting a `Sink[T]` interface that Combiner and Gatherer
satisfy.

**Reason**: no public API consumes it. Users who want polymorphism over
"offerable things" can define a one-method interface in their own
package; Go's structural typing makes the framework's concrete types
satisfy it automatically. Smaller public surface.

### Verb alternatives

**Rejected**: `Run`, `Submit`, `Push`, `Send`, `Feed`, `Apply`,
`Integrate`, `Accept`, `Yield` for the dispatch verb.

**Reasons** (briefly): `Run` only fits TaskRunner, not the passive
receivers; `Submit` is pool-vocabulary that double-encodes "to";
`Push` is stack-coded and slightly imperative; `Send` carries channel
direction baggage; `Feed` reads cute; `Apply` is function-application
(works for handler-like Gatherer, breaks for accumulator-like
Combiner); `Integrate` is too academic; `Accept` is too passive
(from sink's perspective, the caller is the one acting); `Yield` is
generator-coded.

### Renaming "Combiner" to "Stage" / "Aggregator" / "Reducer"

**Rejected**: replacing the Combine vocabulary with stream-processing
terms.

**Reason**: `Stage` carries Apache Beam / Flink baggage with subtly
different semantics. `Reducer` implies N→1 collapse that combiners don't
strictly do. `Aggregator` is verbose. `Combiner`'s only flaw was
proximity to `Combine` (the verb), and that's resolved by adopting the
agent-noun pattern across all three op types.

### Keeping `TaskPool` and `CombinerPool` as distinct types

**Rejected**: maintaining separate user-facing pool types for task
workers vs combiner workers.

**Reason**: the historical reason was state-per-goroutine coupling in
combiners. That coupling no longer exists — state is pooled separately
via omnipool, goroutines are fungible. Two pool types for one
underlying behavior is API noise. The spool itself is the single
worker pool; per-op concurrency control is expressed via Limiters.

### TaskRunner.Close

**Rejected**: a public Close method on TaskRunner.

**Reason**: closing the TaskRunner can't help the framework with
completion tracking, because the framework doesn't know what sinks the
task body will Offer to (the wiring lives in the closure). Without
semantic value, Close is API surface that does nothing useful.

### Public Dup on TaskRunner

**Rejected**: refcounted-handle Dup() / Close() on TaskRunner.

**Reason**: TaskRunner has no semantic Close, so refcounted lifecycle is
unmotivated. Sharing a TaskRunner across goroutines via plain Go value
semantics is sufficient; leakguard finalizers handle cleanup of unowned
handles.

---

## Open questions / things to verify

These are real and need answers before implementation locks in.

1. ~~**`TaskRunner` collision check.**~~ **Resolved (2026-05-24):
   doesn't matter at the API level.** Type names are scoped by package;
   `streampool.TaskRunner` is uniquely identified by its import path.
   Conversational/SEO collisions with other Go libraries that use
   "TaskRunner" are possible but low-impact — users will reach for
   "streampool's TaskRunner" naturally, and search discoverability
   doesn't depend on this term (the research showed "worker pool" /
   "errgroup with results" are the actual queries). If a really
   popular `TaskRunner` shows up later and complicates marketing copy,
   adjust the README — but the type name itself is fine.

2. ~~**Exact `Limiter` interface.**~~ **Resolved (2026-05-24).** Limiter
   is an opaque struct in v0.x; the internal contract (TryAcquire +
   Notifier or similar) is unexported and sealed against external
   implementations. Users obtain Limiter values from framework
   constructors and pass them to `WithLimits`; custom concurrency
   logic that doesn't fit the built-ins (`Semaphore`, `RateLimit`,
   future `Adaptive`) lives inside the user's task/accumulate/gather
   body. Opening the interface later is non-breaking; closing it
   later would be — conservative now, expansive later.

3. **Sub-package naming — partially resolved, partially needs deeper
   work.** Updated 2026-05-24 after auditing psgwf and otpsg:
   - `psgfn` → folded into main package. **Resolved.**
   - `psgopt` → folded into main package. **Resolved.**
   - `psgwf` and `otpsg` turn out to be **alternate complete API
     surfaces**, not sub-domains. psgwf injects a `Workflow` parameter
     across task/combine/gather signatures via result-type wrapping;
     otpsg does the same for OpenTelemetry trace context via
     `PropagatedResult[T]`. Users import one or the other, not both
     alongside plain psg. And per the user, otpsg was already slated
     to refactor on top of psgwf — meaning they consolidate into one
     concept.

   The new API design enables consolidation:

   - **No result-type wrapping needed.** Explicit `Submit(ctx, value)`
     from function bodies replaces the `.To(sink)` auto-routing
     mechanism that required result-type tricks. Side-band data rides
     `ctx` instead.

   - **Rename Workflow → Stream.** Aligns with the streampool
     positioning ("pool of streams"). A Stream is one logical thread
     of related work — refcounted lifecycle, hierarchical (parent-child),
     optional typed context via generics, default cancellation domain.
     Avoids the "Workflow" baggage (Argo, BPM, Temporal-style state
     machines).

   - **Stream is a first-class concept in main `streampool` package.**
     Not a sub-package. With the rename and the streampool framing,
     it's load-bearing for the metaphor, not optional add-on.

   - **OpenTelemetry integration becomes a doc page.** Trace context
     rides on Stream's ctx. otpsg as a separate package is unnecessary;
     a 1–2 page doc shows the `streampool.Stream` + `trace.ContextWithSpan`
     pattern with optional thin helpers. **Provisional: drop the otpsg
     package entirely.**

   - **Stream optional, not required.** Users who don't need lifecycle
     tracking just don't create one. Users who do create one and put
     it in ctx (e.g., `streampool.WithStream(ctx, stream)`); framework
     auto-refs/unrefs around work it dispatches.

   **Decision substantially settled, with one substantive design
   requirement still open** — see open question 9 (Submit ctx
   propagation) below.

4. ~~**Combiner factory invocation strategy.**~~ **Resolved (2026-05-24).**
   Documented contract for users:
   - The framework maintains a per-op queue of idle Accumulator
     instances. When a worker has work and the queue is empty, the
     factory is called to create a new instance.
   - Instances are reused across many `Accumulate` calls — pushed
     back onto the idle queue between calls. The factory is *not*
     invoked per input.
   - Instances are not pinned to goroutines; any worker can pick any
     free instance. The framework serializes access to a single
     instance while it's held.
   - Multiple instances exist concurrently under load, bounded by
     demand and configured limits.
   - On `Flush` (driven by user-supplied deadlines from `Accumulate`
     or by op Close), the instance's lifecycle ends — its closure
     becomes garbage.
   - Subsequent work with no free instance triggers a fresh factory
     call with fresh state.

   Implementation reference: `combineop.go:712-755` (`combineWork.Combine`)
   and `combineop.go:433-457` (`halfBoundCombiner.allocate`).

5. ~~**GatherFunc error handling.**~~ **Resolved (2026-05-24):** All
   downstream-facing signatures take `(value T, err error)` —
   `GatherFunc`, `Accumulator.Accumulate`, `OfferErr`. This matches the
   existing psg-go semantics: errors flow alongside their associated
   values through the pipeline. `Offer(ctx, value)` is sugar for
   `OfferErr(ctx, value, nil)`; callers reach for `OfferErr` when
   forwarding an upstream error.

6. ~~**Adaptive-pool-sizing surface.**~~ **Resolved (2026-05-24).** Audit
   of the existing option set against actual consumers (`cpstate.Config.Update`
   and friends) found that about half of the surfaced options are dead
   code — they're accepted into config-changes structs but never read by
   the framework. The adaptive-tuning options (`MinConcurrency`,
   `ConcurrencyBounds`, `MeasurementTimeConstant`, `HighUtilizationThreshold`,
   `HistoryRetentionPeriod`, `MinThroughputROI`, growth factors) were
   designed for an earlier adaptive-sizing implementation that has since
   been supplanted by a simpler one that doesn't read these knobs. The
   `HoldTime` options on `CombineOp` are likewise dead — `ApplyToCombineOp`
   is never invoked from production code.

   **Resolution:** prune the dead options as part of the rename. The
   `PoolOption` surface for v1 is just:

   ```go
   WithMaxGoroutines(n int)              // hard ceiling, -1 unlimited
   WithIdleTimeout(d time.Duration)      // worker idle exit
   WithFlushListener(callback func())    // fires when all tasks complete
   ```

   Two more (`WithIdleJitter`, `WithSpawnConcurrencyLimit`) are wired
   but rarely user-relevant — keep them available, document them as
   advanced/rarely-needed.

   When/if the adaptive logic gains tuning inputs, add the knobs then.
   Shipping dead options is the worst state for users (they think they
   can tune something they can't).

7. ~~**TaskRunner2 vs higher-arity.**~~ **Resolved (2026-05-24).** Ship
   `TaskRunner0`, `TaskRunner[T]`, `TaskRunner2[T1, T2]` — 0, 1, and 2
   arities. Stop there. The TaskRunner0 covers closure-captured-args
   one-offs and long-running source tasks; TaskRunner[T] is the
   workhorse; TaskRunner2 mirrors `iter.Seq2`. Higher arities are
   handled via struct-typed arguments or closure capture — same advice
   as for HTTP handlers and similar Go APIs.

8. ~~**Migration story for v0.x users.**~~ **Resolved (2026-05-24): no
   migration needed.** psg-go has no users outside this codebase, so
   the rename is a clean rewrite — no compatibility shims, no
   deprecation cycle, no migration guide required. Tag the existing
   `psg-go` for archival and develop the new `streampool` module
   freely.

9. ~~**Submit ctx propagation.**~~ **Resolved (2026-05-24).**

   For Stream lifecycle and OpenTelemetry trace context to ride `ctx`
   (replacing the result-type wrapping in psgwf/otpsg), the framework
   treats Submit ctx as load-bearing. Three boundary cases:

   - **Task body invocation.** Framework passes the Start ctx
     directly. Submit ctx originates here. No change from current
     behavior.

   - **Combiner.Accumulate invocation.** Framework captures the
     Submit ctx with each work item and uses it directly for the
     Accumulate call (layered via `ensureCtxMeta` to add combine-meta).
     Submit ctx is rooted in the user's Stream ctx, which is rooted
     in the pool ctx, so cancellation cascade works through the
     standard `context` hierarchy. Submit ctx values (trace span,
     Stream handle, audit metadata) are accessible via standard
     `ctx.Value` / `trace.SpanFromContext` idioms inside Accumulate.

   - **Gatherer Handler invocation.** Handler's ctx is rooted in the
     Submit ctx so that stream and pool cancellation propagate via
     the standard parent chain. The puller's ctx (from
     `pool.Gather(pullCtx)`) is an independent cancellation source
     that needs to be wired in as an additional source. Conceptually
     equivalent to:

     ```go
     ctx, cancel := context.WithCancel(submitCtx)      // rooted in submit
     stop := context.AfterFunc(pullCtx, cancel)         // pull also cancels
     defer func() { stop(); cancel() }()
     return handler.Handle(ctx, value, err)
     ```

     Result: Handler's `ctx.Done()` fires on stream cancel, pool
     cancel, or puller cancel; `ctx.Value(key)` walks from Submit ctx
     upward. Implementation may use the literal stdlib pattern above
     OR a pooled-goroutine `mergedCtx` (with idle eviction and
     done-channel reuse to avoid per-call allocations on the hot
     path). Choice is a profiling decision; user contract is the
     same.

   **Stream lifecycle**: framework inspects Submit ctx for a Stream
   (via `streampool.StreamFromContext(ctx)`) at Submit time;
   `Ref()`s the Stream before queueing the work item; `Unref()`s
   when the work item is `Free()`'d. Stream's `afterFn` fires when
   refcount hits zero. Stream ctxs are user-created via
   `streampool.NewStream(parent, ...)` where parent must be rooted
   in the pool ctx; framework verifies at NewStream time and panics
   on misuse.

   **What this lets us delete**:
   - `psgwf` entirely (Stream replaces it in main package)
   - `otpsg` entirely (replaced by a doc page on the standard
     `streampool.Stream` + `trace.ContextWithSpan` pattern)
   - All result-type wrapping plumbing in both

   **Machinery additions**:
   - Submit ctx stored as a field on each work item (one interface
     value).
   - Gather-boundary ctx construction: stdlib `WithCancel` + `AfterFunc`,
     or pooled-goroutine `mergedCtx`. Implementation choice deferred
     to profiling.
   - Stream Ref/Unref bracketing around work-item lifecycle.

   **Open implementation sub-choice (deferred to implementation
   phase)**: how to implement the Gather-boundary ctx. Two options:
   - (a) `context.WithCancel` + `AfterFunc` per Handler invocation.
     Simple; uses stdlib; per-call allocations for the cancelCtx
     internals.
   - (b) Pool of `mergedCtx` structs each with a long-running
     goroutine watching configured cancellation sources. Idle
     eviction prevents goroutine accumulation. Done-channel reuse
     when the ctx didn't actually cancel (the common case). No
     per-call stdlib allocations; one goroutine per concurrent
     Handler invocation up to the steady-state cap. Choose if
     profiling shows the stdlib path is a bottleneck.

---

## Impact on the rest of the project

### README

`README-proposed.md` needs updates reflecting:
- The simplified "no pool to construct" framing.
- The Limiter-based concurrency model (replacing per-pool limits).
- The Offer-from-body model (replacing `.To(sink)` wiring).
- Updated hello world using `NewTaskRunner` + `Offer`.

The comparison table rows mostly stand, but some footnotes need
adjustment (the "per-pool concurrency limits" row becomes "limiters
compose across pools"; the "end-to-end backpressure" footnote already
matches the new model; the "adaptive pool sizing" row gets simpler
since the user doesn't construct pools).

### ARCHITECTURE_COMPARISON.md

The contention/allocation footnotes still hold — the underlying
machinery (nbcq, rdvq, omnipool, leakguard) is unchanged by the rename.
The new bound-op pattern strengthens the "near-zero allocations" claim
for user code, which the doc could mention in its caveats section.

### POSITIONING_RESEARCH.md

No changes needed — research is timeless. The findings still inform
naming and tagline choices; this design doc applies them.

### Implementation cost

Bigger than just a rename. Notable refactors:
- Remove `.To`-style wiring from Combine/Task; route values via
  user-explicit `Offer` calls in op bodies.
- Eliminate the output type parameter from Combiner.
- Add `Limiter` interface and implementations; refactor pool-internal
  concurrency limits to accept Limiters per-op.
- Merge `TaskPool` and `CombinerPool` machinery into one internal
  worker pool with Limiter-based per-op control.
- Rename and re-shape examples, tests, sub-packages.

Estimated as a major-version rewrite. Worth doing as a single coherent
landing rather than piecemeal — incremental migration would force
maintaining two coexisting models in the codebase.

### Dead-code pruning (alongside the rename)

The audit for open question #6 surfaced significant dead code in the
options system. Items to remove as part of the rename:

- `psgopt.WithConcurrencyBounds` + `internal/opts.ConcurrencyBounds`
- `psgopt.WithMinConcurrency` + `internal/opts.MinConcurrency`
- `internal/opts.MeasurementTimeConstant` (not exposed via psgopt)
- `internal/opts.HighUtilizationThreshold`
- `internal/opts.HistoryRetentionPeriod`
- `internal/opts.MinThroughputROI`
- `internal/opts.GrowthFactors`, `AggressiveGrowthFactor`, `ConservativeGrowthFactor`
- `internal/opts.MinHoldTime`, `MaxHoldTime`, `HoldTimes`
- `internal/opts.CombineOpOption`, `CombineOpConfigChanges`,
  `combineOpConfig`, `ApplyToCombineOp` (entire CombineOp options
  plumbing — `ApplyToCombineOp` has no production caller)
- The corresponding fields on `CombinerPoolConfigChanges` that no
  consumer reads

These were designed for an earlier adaptive-sizing implementation that
has been supplanted by a simpler one not reading these knobs. If/when
the adaptive logic gains tuning inputs, surface them then.
