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

## The three-type model

Three orthogonal concerns, three types. Conflating any two of them produces
the kind of API friction this design exists to remove.

1. **Pool** — the worker pool. Goroutines, idle policy, max-budget.
   Fungible. A package-level default Pool exists implicitly; users
   only construct a custom Pool when they need a non-default ctx
   (for cancellation) or non-default tuning. Pool's lifecycle is
   refcount-driven: workers spin up when the first referencing Wave
   begins work and exit synchronously when the last Wave referencing
   the Pool completes its drain. A Pool whose refcount has returned
   to zero is fully reusable — the next Wave that references it
   spins workers up again.

2. **Wave** — a batch of work to be completed together. The
   user-facing primary type. Ops are constructed against a Wave; the
   Wave's `Gather` / `GatherAll` await the completion of its work.
   Waves nest: a parent Wave's drain waits for its child Waves.
   Multiple Waves run concurrently on the same Pool routinely.

3. **Flow** — one logical thread of related work. Refcount-driven,
   carried in context, lives independently of any Wave. A Flow can
   submit values into multiple Waves over its lifetime; its
   lifecycle is bounded by when the work it spans completes, not by
   any single Wave's drain. Flow is optional — needed when you want
   to attach metadata (trace context, audit data) to a unit of work
   that may cross Wave boundaries, run a cleanup hook when its work
   completes, or have a cancellation domain narrower than the
   enclosing Pool ctx.

The streampool tagline lands literally: a **Pool** of workers serves
**Waves** of work; each Wave hosts **Flows** of related processing.

(The name "Stream" is reserved for a future observability concept — a
stream of Flow lifecycle events for monitoring/aggregation. It's not
used for any of the three types above precisely because "stream" in
common usage denotes a flow of multiple items, which would mismatch
the singular-instance semantics of Flow.)

## The final API surface

```go
package streampool

// ===== Pool: worker pool (often implicit) =====

// Pool hosts the goroutines that execute work for the Waves referencing
// it. Fungible — a package-level default Pool exists implicitly; users
// only call NewPool to get a custom ctx or non-default tuning.
//
// Lifecycle is refcount-driven: each referencing Wave bumps the count
// on construction and drops it when its drain completes. When the
// count returns to zero, the Pool's workers exit synchronously before
// the last Wave's drain method returns — so when GatherAll returns on
// the final Wave, the Pool has zero live goroutines. The Pool object
// itself is fully reusable; the next Wave that references it spins
// workers up again.
//
// During active use (refcount > 0), individual workers between work
// items exit via the configured IdleTimeout — that's the transient
// idle case, distinct from the refcount=0 termination above.
//
// Hard abort: cancel the ctx given to NewPool. Cascades to all
// referencing Waves, all in-flight work, all workers.
type Pool struct { /* ... */ }

func NewPool(ctx context.Context, opts ...PoolOption) *Pool

// (No Shutdown / Wait / Close method. Pool lifecycle is implicit.)

// Pool options
type PoolOption interface { /* ... */ }

func WithMaxGoroutines(n int) PoolOption
func WithIdleTimeout(d time.Duration) PoolOption
// Advanced (rarely user-relevant):
func WithIdleJitter(d time.Duration) PoolOption
func WithSpawnConcurrencyLimit(n int) PoolOption

// ===== Wave: batch of work the user awaits =====

// Wave is a collection of work to be completed together. Ops are
// constructed bound to a Wave (or with nil to defer binding — see Op
// constructors); the Wave's drain methods await its work. Multiple
// Waves run concurrently on the same Pool. Waves nest via NewChild.
type Wave struct { /* ... */ }

// NewWave creates a top-level Wave. Uses the package default Pool
// unless WithPool is supplied.
func NewWave(ctx context.Context, opts ...WaveOption) *Wave

// NewChild creates a Wave whose drain rolls up into the receiver's
// drain. Inherits the receiver's Pool unless WithPool is supplied —
// child Waves on a different Pool are valid (the library-isolation
// case) but uncommon. Parent's GatherAll waits for all child Waves
// to complete. Sub-work spawned inside the parent's task bodies
// belongs to the parent unless explicitly created as a child Wave.
func (*Wave) NewChild(ctx context.Context, opts ...WaveOption) *Wave

func (*Wave) Gather(ctx context.Context) error      // pull one ready result through Gatherers
func (*Wave) GatherAll(ctx context.Context) error   // drain to completion
func (*Wave) Close()                                 // signal no more top-level entries to this Wave
func (*Wave) CancelAndWait()                         // cancel Wave ctx, wait for drain

type WaveOption interface { /* ... */ }

// WithPool routes a Wave's work to a specific Pool instead of the
// inherited or default one.
func WithPool(p *Pool) WaveOption

// ===== Flow: one logical thread of work =====

// Flow is a refcounted, ctx-borne lifecycle entity representing a
// single workflow instance. Its lifetime is determined by reference
// counting across all work items that capture it — possibly spanning
// multiple Waves. Use a Flow to attach metadata (trace context,
// audit data) to related work, run a cleanup hook when all the
// work completes, or scope a cancellation domain narrower than the
// enclosing Pool ctx.
type Flow struct { /* ... */ }

// NewFlow creates a Flow rooted in parent. The returned ctx carries
// the Flow; pass that ctx into op dispatches (Start, Submit) so the
// framework can ref/unref the Flow across the work item's lifecycle.
// The Flow starts with refcount 1 (the caller's reference) — call
// Close to release it. afterFn fires when refcount reaches 0.
// Cancellation: cancel parent (or use context.WithCancel before
// calling NewFlow) to cancel the Flow's work.
func NewFlow(parent context.Context, opts ...FlowOption) (context.Context, *Flow)

// FlowFromContext retrieves the Flow attached to ctx, or nil if none.
// Useful inside task bodies that need to extend or inspect the Flow.
func FlowFromContext(ctx context.Context) *Flow

// Dup returns a new reference to the same Flow, incrementing the
// refcount. Use when handing the Flow to another goroutine that
// will manage its own lifecycle.
func (*Flow) Dup() *Flow

// Close releases the caller's reference. After all references
// (caller's plus framework's per-work-item) are released, afterFn
// fires.
func (*Flow) Close()

type FlowOption interface { /* ... */ }

func WithAfterFunc(fn func()) FlowOption  // fires when Flow refcount reaches 0

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
// Wave.Gather / Wave.GatherAll. Receives upstream values paired with
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
// All constructors bind ops to a *Wave at construction and take the
// user-supplied interface as canonical input. The *Wave parameter
// accepts nil as an explicit sentinel meaning "defer binding to
// dispatch time, use the wave attached to the dispatch ctx." See
// "Wave binding" below for the resolution rule.
//
// For raw closures, wrap in the corresponding *Func type (TaskFunc[T],
// HandlerFunc[T], FuncAccumulator[T]) at the call site.

// Per-op options (functional)
type OpOption interface { /* ... */ }

func WithLimits(limiters ...Limiter) OpOption
// ... future: WithPriority, WithDeadline, WithRetry, etc.

func NewTaskRunner0(wave *Wave, task Task0, opts ...OpOption) TaskRunner0
func NewTaskRunner[T any](wave *Wave, task Task[T], opts ...OpOption) TaskRunner[T]
func NewTaskRunner2[T1, T2 any](wave *Wave, task Task2[T1, T2], opts ...OpOption) TaskRunner2[T1, T2]
func NewCombiner[T any](wave *Wave, factory CombinerFactory[T], opts ...OpOption) Combiner[T]
func NewGatherer[T any](wave *Wave, handler Handler[T], opts ...OpOption) Gatherer[T]

// For "reducer" behavior — strictly serial accumulation, only one
// instance active at a time — construct a Combiner with a 1-permit
// limiter:
//
//   ordered := streampool.NewCombiner(wave, factory,
//       streampool.WithLimits(streampool.NewSemaphore(1)),
//   )

// ===== Wave binding =====
//
// Every op holds an optional *Wave bound at construction. At dispatch
// (Start / Submit / SubmitErr), the framework resolves the target
// wave as follows:
//
//   1. If the op was constructed with a non-nil *Wave, use it.
//   2. Otherwise, use the *Wave attached to the dispatch ctx (the wave
//      whose Task/Accumulator/Gather body the caller is running in).
//   3. If neither is available, the dispatch panics with a message
//      naming both options.
//
// The split lets top-level callers bind explicitly at construction
// (the common case) and lets ops constructed without a wave —
// typically by reusable library helpers, or inside a body that
// doesn't carry the wave in scope — resolve to the in-flight wave
// at dispatch time. The wave handle itself never leaks to body code;
// only the op does.

// ===== Op types =====
//
// TaskRunners use Start (active dispatch). Combiner and Gatherer use
// Submit / SubmitErr (sink reception). The verb split tracks the op's
// role: dispatchers start work; sinks accept submitted values. None
// of the dispatch methods take a *Wave — the wave was bound at
// construction (or deferred via nil; see "Wave binding").

type TaskRunner0 struct { /* ... */ }
func (TaskRunner0) Start(ctx context.Context) error
func (TaskRunner0) TryStart(ctx context.Context, deadline time.Time) (bool, error)

type TaskRunner[T any] struct { /* ... */ }
func (TaskRunner[T]) Start(ctx context.Context, arg T) error
func (TaskRunner[T]) TryStart(ctx context.Context, deadline time.Time, arg T) (bool, error)

type TaskRunner2[T1, T2 any] struct { /* ... */ }
func (TaskRunner2[T1, T2]) Start(ctx context.Context, arg1 T1, arg2 T2) error
func (TaskRunner2[T1, T2]) TryStart(ctx context.Context, deadline time.Time, arg1 T1, arg2 T2) (bool, error)

// Combiner — stateful aggregation via factory-created Accumulator
// instances. Parallel by default; cap parallelism via WithLimits.
type Combiner[T any] struct { /* ... */ }
func (Combiner[T]) Submit(ctx context.Context, value T) error                                  // sugar for SubmitErr(ctx, value, nil)
func (Combiner[T]) SubmitErr(ctx context.Context, value T, err error) error
func (Combiner[T]) TrySubmit(ctx context.Context, deadline time.Time, value T) (bool, error)
func (Combiner[T]) TrySubmitErr(ctx context.Context, deadline time.Time, value T, err error) (bool, error)
func (Combiner[T]) Close()                                                                     // signals no more input; triggers Flush on each instance
func (Combiner[T]) Dup() Combiner[T]                                                           // refcounted sharing across handlers

// Gatherer — terminal sink; Handler is dispatched on Wave.Gather pull.
type Gatherer[T any] struct { /* ... */ }
func (Gatherer[T]) Submit(ctx context.Context, value T) error                                  // sugar for SubmitErr(ctx, value, nil)
func (Gatherer[T]) SubmitErr(ctx context.Context, value T, err error) error
func (Gatherer[T]) TrySubmit(ctx context.Context, deadline time.Time, value T) (bool, error)
func (Gatherer[T]) TrySubmitErr(ctx context.Context, deadline time.Time, value T, err error) (bool, error)
func (Gatherer[T]) Close()                                                                     // signals no more input; GatherAll branch completes
func (Gatherer[T]) Dup() Gatherer[T]                                                           // refcounted sharing across handlers
```

## Hello world

```go
ctx := context.Background()

// Wave: the batch of work this function awaits. Uses the package
// default Pool implicitly; no Pool construction needed.
wave := streampool.NewWave(ctx)
defer wave.Close()

results := streampool.NewGatherer(wave, streampool.HandlerFunc[*User](
    func(ctx context.Context, user *User, err error) error {
        if err != nil { return err }
        fmt.Println(user.Name)
        return nil
    },
))

fetch := streampool.NewTaskRunner(wave, streampool.TaskFunc[UserID](
    func(ctx context.Context, id UserID) error {
        user, err := userClient.Fetch(ctx, id)
        return results.SubmitErr(ctx, user, err)
    },
))

for _, id := range userIDs {
    fetch.Start(ctx, id)
}

results.Close()
wave.GatherAll(ctx)
// When this Wave's drain returns, the default Pool's workers have
// exited synchronously (refcount → 0). If you then create another
// Wave, workers spin back up on demand.
```

## With a custom Pool

When you need a non-default ctx (cancellation domain narrower than
process-wide) or non-default tuning:

```go
pool := streampool.NewPool(ctx, streampool.WithMaxGoroutines(100))
// No defer needed — Pool's workers exit when refcount returns to 0.

wave := streampool.NewWave(ctx, streampool.WithPool(pool))
defer wave.Close()
// ... ops against wave ...
wave.GatherAll(ctx)
```

Flow is omitted from the hello world because it's optional. Add a Flow
when you want logical-thread metadata or cleanup hooks:

```go
ctx, flow := streampool.NewFlow(ctx, streampool.WithAfterFunc(func() {
    // fires when refcount reaches 0 — all work attributed to this Flow done
}))
defer flow.Close()  // releases the user's reference; framework refs come from work items
```

## With a combiner

```go
totals := streampool.NewCombiner(wave, func() streampool.Accumulator[int] {
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

score := streampool.NewTaskRunner(wave, streampool.TaskFunc[UserID](
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

fetch := streampool.NewTaskRunner(wave, &fetcher{db: db, sink: results})

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

fetch := streampool.NewTaskRunner(wave, fetchFn,
    streampool.WithLimits(slowAPI, apiRate),
)
```

Limiters compose with AND semantics: a dispatch proceeds only when all
attached limiters permit. The same `Limiter` instance can be shared
across multiple ops, expressing "these collectively cap at N concurrent
operations."

## Deferred wave binding (nil at construction)

Some ops want to be reusable across waves — a helper that returns a
TaskRunner without knowing which wave the caller will dispatch from,
for instance. Pass `nil` as the wave at construction; the op resolves
its target wave at each dispatch from the ctx the caller passes
(specifically, from the wave whose Task/Accumulator/Gather body the
caller is running in).

```go
// Library-style helper: returns a wave-agnostic Gatherer.
func NewLogGatherer(logger *slog.Logger) streampool.Gatherer[Event] {
    return streampool.NewGatherer(nil, streampool.HandlerFunc[Event](
        func(ctx context.Context, e Event, err error) error {
            logger.Info("event", "name", e.Name, "err", err)
            return nil
        },
    ))
}

// Caller binds the helper's gatherer to its own wave by dispatching
// from a body that's running in that wave. The Gatherer was
// constructed with nil, so each Submit resolves to the dispatching
// ctx's wave.
sink := NewLogGatherer(slog.Default())

runner := streampool.NewTaskRunner(wave, streampool.TaskFunc[ID](
    func(ctx context.Context, id ID) error {
        evt := process(id)
        return sink.Submit(ctx, evt)  // dispatches to `wave` via ctx
    },
))
```

Top-level callers calling `Submit`/`Start` directly on a nil-bound op
panic — there is no ctx-attached wave at top level. The panic message
names both options: pass a wave at construction, or dispatch from a
body running in a wave.

---

## Naming decisions

| Decision | Choice | Reasoning |
|---|---|---|
| Package name | `streampool` | Matches user-vocabulary search terms (worker pool / streaming results); distinct on pkg.go.dev; encodes the differentiator without being cute. The tagline lands literally: a Pool of workers runs Waves of work hosting Flows of related processing. |
| Three concerns, three types | `Pool` (workers), `Wave` (batch), `Flow` (workflow instance) | The conflated single-type model produced confusing "what does Pool actually do?" questions. Separating: Pool manages goroutines (singleton-ish, process-level); Wave is the user-facing batch primitive that hosts ops and exposes drain verbs; Flow is the ctx-borne refcounted lifecycle entity for a single workflow instance, independent of any Wave. |
| Role-3 name: `Wave` (not `Job` / `Group` / `Batch`) | "A wave of processing" carries the right metaphor: waves can overlap (multiple in flight in the same Pool), vary in size (small ripples to large processing bursts), and contain smaller waves (nested sub-batches). Coheres with the streampool nautical theme. `Job` is acceptable but reads as a discrete K8s/SLURM-style unit; `Group` clashes with errgroup. `Wave` is fresh and metaphorically apt. |
| Role-2 name: `Flow` (not `Stream` / `Workflow`) | `Stream` denotes "a flow of items" in standard usage — Java/Akka/Kafka/Node streams. Our role-2 type holds no payload; it's a refcounted lifecycle marker. Stream would mislead. `Flow` reads concretely (one specific flow of work) without the abstract-vs-concrete ambiguity of `Workflow`, has no Argo/BPM/Temporal baggage, is short, and coheres with the nautical theme. `Stream` is reserved for a future observability concept (a stream of Flow events). |
| Worker pool | `Pool` — fungible, often implicit | Pool's job is just goroutines, idle policy, max budget. No `Gather`, no `Shutdown`, no `Wait` — refcount-driven lifecycle handles termination implicitly. A package-level default Pool exists; users only call `NewPool` for a non-default ctx (cancellation domain) or non-default tuning. Matches `sync.Pool` convention as a fungible resource container. |
| Op constructor first arg | `wave *Wave` (nil OK) | Ops bind to a Wave at construction (their lifecycle owner). Pool comes via the Wave (which references a Pool through default or `WithPool`). Flow comes in dynamically via ctx, not at construction — because a Flow can span Waves but ops can't. Passing `nil` defers wave-binding to dispatch time, resolving from the dispatching ctx; the same op can then be reused inside any wave's body. The dispatch methods never take a *Wave — the wave is locked in at construction (explicitly or as the deferred-to-ctx sentinel). |
| Dispatch methods take no *Wave | `Start(ctx, ...)` / `Submit(ctx, v)` / `SubmitErr(ctx, v, err)` | Considered three alternatives and rejected each: (a) explicit *Wave on every dispatch — verbose in the common case where one wave handles many dispatches; (b) `Submit` / `SubmitIn` method split — doubles surface for every dispatch verb; (c) `op.In(wave)` bind-chain — breaks the lifecycle model for ops with Close (Combiner, Gatherer), since the unassigned intermediate handle has no way to be closed. Wave-at-construction with a nil sentinel preserves single-verb dispatch, single lifecycle, and explicit binding when desired. |
| Pool lifecycle | Refcount-driven; workers exit synchronously on last Wave drain | No `Shutdown` / `Wait` API. Each referencing Wave bumps refcount; drain completion drops it. When count → 0, workers terminate synchronously before the last Wave's drain returns — strong guarantee that no Pool goroutines outlive the user's drain calls. Pool reuse after this is automatic; the next Wave that references the Pool spins workers up again. |
| Op constructor verb | `NewTaskRunner`, `NewCombiner`, `NewGatherer` | Agent nouns (`-er` suffix). The type names describe roles, not the function-call verb. Matches `http.Handler`, `io.Reader`, `sync.Mutex`. |
| TaskRunner dispatch verb | `Start` | Active async dispatch ("start a task with this arg"). Matches `os/exec.Cmd.Start()` precedent — fire-it-off-async-don't-wait. Works across arities including the no-arg case (`runner0.Start(ctx)`). |
| Sink dispatch verb | `Submit` / `SubmitErr` | Committed-delivery semantics: "submit this value to the sink." Avoids the Java `BlockingQueue.offer` baggage that would mislead users to expect try-semantics from `Offer`. No collision with TaskRunner verb since TaskRunner uses Start. |
| Interface method verbs | `Run` (Task), `Accumulate`/`Flush` (Accumulator), `Handle` (Handler) | Sync execution verbs on user-implemented interfaces, mirroring the http.Handler.ServeHTTP / exec.Cmd inner-process pattern: async dispatch on the op (Start, Submit), sync invocation on the implementation (Run, Accumulate, Handle). |
| User inputs as interfaces | `Task[T]`, `Task0`, `Task2[T1,T2]`, `Accumulator[T]`, `Handler[T]` | Function signatures forced closure allocations for any stateful task. Interfaces let users implement on structs with state as fields (alloc-free hot path). Function-type wrappers (`TaskFunc[T]`, `HandlerFunc[T]`, `FuncAccumulator[T]`) provide the closure-based convenience for simple cases. Same pattern as http.Handler / http.HandlerFunc. |
| No `.To(sink)` wiring | Function bodies call `sink.Submit(ctx, value)` directly | Enables multi-output ops, conditional routing, zero-output paths. Cost: wiring is no longer visible at construction; users read function bodies to trace dataflow. Worth it for the flexibility and the elimination of the output type parameter on Combiner. |
| No `Sink[T]` in public API | Not exported | Nothing in the framework's own API consumes a Sink type. User code that wants polymorphism over "things you can Submit to" defines a one-method interface locally; Go's structural typing makes that work without a published contract. |
| Combiner state | Via `CombinerFactory[T]` returning `Accumulator[T]` | Factory creates per-instance Accumulators (each with closure state); framework calls each instance's `Combine` per input and `Flush` on Close. Same shape as the existing `psgfn.Combiner` interface. |
| Serial accumulation ("reducer") | Combiner with `WithLimits(NewSemaphore(1))` | No separate Reducer type. The "only one instance active at a time" property is enforced by a 1-permit limiter, reusing the Limiter abstraction. Same factory, same Accumulator interface — only the concurrency cap differs. |
| TaskRunner.Close | Not present | The framework can't deduce what sinks a task body will Submit to, so closing the TaskRunner tells the framework nothing useful. Resources release via leakguard finalizer when the value falls out of scope. |
| Combiner.Close / Gatherer.Close | Present, type-specific behavior | Combiner.Close triggers final accumulator flush. Gatherer.Close signals end-of-input to GatherAll. Both are semantically meaningful because the op has its own state to finalize. |
| Dup() on Combiner / Gatherer | Present | Refcounted-handle pattern earns its place in psgwf-style scenarios where ops cross handler boundaries with independent lifecycles. Single-scope usage doesn't require Dup; advanced shared usage does. |
| Per-op configuration | Functional options on constructor (`opts ...OpOption`) | Standard Go idiom for multiple optional parameters. Composable, extensible without breaking existing call sites. |
| Limiters | First-class entity, not a pool property | Limiters compose; pools don't. Multiple Tasks can share a Limiter; an op can have multiple Limiters; new Limiter types extend the system without core-API changes. |
| Type parameter convention | `T` (or `T1`, `T2`) | Default to T per user preference. Use T1/T2 for binary variants like `TaskRunner2`, not `T, U`, for explicit naming under refactor. |

---

## Mapping from psg-go

| Old (psg-go) | New (streampool) | Notes |
|---|---|---|
| `psg.NewJob(ctx)` (the post-rename `psg.New(ctx)`) | **Splits** into `streampool.NewWave(ctx)` (user-primary; uses default Pool) + optionally `streampool.NewPool(ctx, opts...)` (only for non-default ctx or tuning) | The conflated Pool-as-bounded-context becomes two types. Wave is the user-facing handle for a batch of work; Pool is the fungible worker container, mostly implicit. |
| `*Job` / `*Pool` (conflated) | `*Pool` (workers, fungible) + `*Wave` (batch, user-primary) | See three-type model above. |
| `psg.NewPool` / `psg.NewTaskPool` | (removed) | Per-op concurrency limits move to Limiters; workers are managed by the Pool. |
| `psg.NewCombinerPool` | (removed) | Same — combiner workloads run in the Pool's goroutine pool, bounded by Limiters. |
| `psg.NewGatherOp(handler)` | `streampool.NewGatherer(wave, handler)` | Wave bound at construction (or nil to defer to dispatch-ctx). Handler signature unchanged. |
| `psg.NewCombineOp(gather, pool, factory)` | `streampool.NewCombiner(wave, factory)` | Output type parameter gone; downstream sink wired via factory's closure. Wave bindable like Gatherer. |
| `gatherOp.Scatter(ctx, job, taskFn)` | `streampool.NewTaskRunner(wave, taskFn)` + `runner.Start(ctx, arg)` | Two-step: construct once (with wave or nil), dispatch with arg. The dispatch takes no *Wave; the wave was locked in at construction. |
| `gatherer.Submit(ctx, job, value)` | `gatherer.Submit(ctx, value)` / `gatherer.SubmitErr(ctx, value, err)` | Job/Wave arg drops out of dispatch — was bound at construction. SubmitErr is the explicit form; Submit is the sugar for nil-err. |
| `runner.Start(ctx)` (post-Wave-3 shape with positional pool) | `runner.Start(ctx, arg...)` | Pool/Wave arg drops out of dispatch. |
| `Pool.CloseAndGatherAll(ctx)` | `wave.GatherAll(ctx)` | Single call. Pool worker termination is automatic (refcount → 0 → synchronous worker exit before drain returns). |
| `*GatherOp[T]` | `Gatherer[T]` | Op-suffix dropped; agent noun. |
| `*CombineOp[I, O]` | `Combiner[T]` | Output type parameter eliminated. |
| (n/a) | `TaskRunner[T]` / `TaskRunner2[T1, T2]` | Stateless dispatch op surfaced as a first-class type. |
| `psgfn.Task[T]` | `TaskFunc[T]` | Naming convention: function-signature types end in `Func`. |
| `psgfn.CombinerFactory[I, O]` | `CombinerFactory[T]` | Output type removed. |
| `psgwf.Workflow` | `streampool.Flow` | Renamed and folded into main package. Same refcounted-ctx-borne lifecycle semantics. |
| `psgwf` package | (folded into main package; Flow type) | Workflow consolidates into Flow. No separate sub-package. |
| `otpsg` package | (deleted; replaced by doc page) | OpenTelemetry integration becomes a doc page demonstrating the `Flow` + `trace.ContextWithSpan` pattern. No separate package. |
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
out, blocks conditional routing. The explicit-Submit model gives all of
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
underlying behavior is API noise. The Pool itself is the single
worker pool; per-op concurrency control is expressed via Limiters.

### `Job` as the role-3 (batch-of-work) name

**Rejected**: naming the batch primitive `Job`.

**Reason**: `Job` reads as a discrete K8s/SLURM-style unit with a
clear single start and end. The streampool model has many of these
running concurrently in the same Pool, overlapping and nesting —
`Job` resists that mental model. `Wave` carries the metaphor better:
waves overlap (multiple in flight), vary in size (small ripples to
large bursts), and contain smaller waves (nested sub-batches).
`Job` also fights the nautical theme that Pool / Wave / Flow / future
Stream all share. (`Group` was a runner-up; rejected due to errgroup
clash and lighter-than-warranted feel for what's actually a
substantial batch primitive.)

### `Workflow` as the role-2 (workflow-instance) name

**Rejected**: naming the ctx-borne refcounted lifecycle entity
`Workflow` (the psgwf legacy name).

**Reason**: `Workflow` is used in both abstract ("the user onboarding
workflow" — the process/template) and concrete ("this workflow
instance is in progress") senses. As a Go type name, that ambiguity
forces readers to spend a beat figuring out which sense is meant.
`Flow` reads concretely by default — "a flow of work" is almost
always a specific thing. `Flow` is also shorter, has no
Argo/BPM/Temporal baggage, and coheres with the nautical theme.

### `Stream` as the role-2 name

**Rejected**: naming the workflow-instance type `Stream`.

**Reason**: in standard usage — Java Stream\<T\>, Akka Streams,
Kafka, Node.js streams, RxJS — "stream" denotes a flow of multiple
items over time. Our role-2 type carries no payload; it's a
refcounted lifecycle marker. Calling it Stream would prime users to
expect methods like `.send()` / `.write()` / `.next()` that don't
exist. `Stream` is instead **reserved** for a future observability
concept: a stream of Flow lifecycle events for monitoring and
aggregation. That meaning matches "stream" semantically.

### TaskRunner.Close

**Rejected**: a public Close method on TaskRunner.

**Reason**: closing the TaskRunner can't help the framework with
completion tracking, because the framework doesn't know what sinks the
task body will Submit to (the wiring lives in the closure). Without
semantic value, Close is API surface that does nothing useful.

### Public Dup on TaskRunner

**Rejected**: refcounted-handle Dup() / Close() on TaskRunner.

**Reason**: TaskRunner has no semantic Close, so refcounted lifecycle is
unmotivated. Sharing a TaskRunner across goroutines via plain Go value
semantics is sufficient; leakguard finalizers handle cleanup of unowned
handles.

### Explicit `*Wave` on every dispatch (`Start(ctx, wave, arg)`)

**Rejected**: making the wave a required arg on Start / Submit /
SubmitErr at every dispatch site.

**Reason**: top-level callers typically construct a handful of ops and
dispatch each many times to the same wave. Threading the wave through
every dispatch site is verbose for the common case, with no
information not already captured at construction. Wave-at-construction
collapses that repetition to one place. Inside a body, the framework
already knows which wave the body is running in; a body that needs to
dispatch via a no-wave-bound op (constructed with `nil`) gets the
right wave automatically via ctx.

### `op.In(wave)` bind-chain

**Rejected**: leaving constructors wave-free and providing a value-typed
`runner.In(wave).Start(ctx, arg)` bind step.

**Reason**: Combiner and Gatherer carry refcounted lifecycles (Close,
Dup). The chain `NewCombiner(...).In(wave).Submit(ctx, v)` produces an
intermediate Combiner handle that no variable holds, so there is no
way to Close it; either both handles share the same underlying state
(closing one closes the other, surprising) or `In` Dups (every chain
leaks a handle). Wave-at-construction sidesteps both: each op has one
wave, one handle, one Close. The bind-chain pattern only worked
cleanly for stateless TaskRunner — making it the universal model
forces lifecycle-having ops into shape they can't accommodate.

### `Submit` / `SubmitIn` (and `Start` / `StartIn`) method split

**Rejected**: separate verbs for ctx-default dispatch (`Submit(ctx,
v)`) and explicit-wave dispatch (`SubmitIn(ctx, wave, v)`).

**Reason**: doubles the dispatch surface for every verb in the
family (Submit/SubmitIn, SubmitErr/SubmitErrIn, TrySubmit/TrySubmitIn,
TrySubmitErr/TrySubmitErrIn, and the matching Start variants per
arity). Each new dispatch verb in the future would need an `In`
partner. Wave-at-construction collapses both behaviors into a single
verb whose binding was decided at construction (explicitly, or
explicitly-deferred via nil).

### `CurrentWave(ctx)` package-level helper

**Rejected**: a `CurrentWave(ctx) *Wave` (or `WaveFromContext(ctx)
(*Wave, bool)`) helper to look up the in-flight wave from inside a
body.

**Reason**: returning a full `*Wave` from inside the body the wave is
running gives user code methods it shouldn't call. `CurrentWave(ctx).
Close()` and `CurrentWave(ctx).Gather(ctx)` from a task body are
either no-ops at best or destructive at worst (canceling the wave
that's executing the calling body). Narrowing the return to a
dispatch-only interface ran into either interface-allocation cost or
extra type surface. Wave-at-construction with nil-binding gives the
same ergonomic outcome (body code dispatches without seeing the wave)
without exposing the handle.

### Generic `Work` unit (build-then-execute pattern)

**Rejected**: `work := runner.With(arg); work.Start(ctx, wave)` —
exposing the work item as a separable, dispatchable unit.

**Reason**: lifecycle hazard (build-without-dispatch leaks unless a
finalizer pays the cost we're trying to avoid) and allocation cost
for the generic case (`Work` as an interface boxes the underlying
concrete struct on every dispatch, defeating the alloc-free hot
path). Concrete per-op Work types preserve alloc-free but lose the
generic composition story that justified the shape. The compositional
use cases (deferred dispatch, batched enqueue, cross-wave routing)
are real but rare; when needed, the user can build the equivalent in
their own code without exposing it in the framework surface.

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

3. ~~**Sub-package naming and psgwf/otpsg consolidation.**~~
   **Resolved (2026-05-25)** after the three-type-model design session:
   - `psgfn` → folded into main package. ✓
   - `psgopt` → folded into main package. ✓
   - `psgwf.Workflow` → `streampool.Flow`, folded into main package. The
     Workflow concept (one logical thread of related work, refcounted,
     ctx-borne) becomes the Flow type. Named "Flow" rather than
     "Workflow" because Flow reads concretely as one specific instance,
     where Workflow tilts abstract (a process/template). Named "Flow"
     rather than "Stream" because Stream in standard usage denotes a
     flow of multiple items, mismatching the singular-instance
     semantics. Stream is reserved for a future observability concept
     (a stream of Flow lifecycle events).
   - `otpsg` → deleted; replaced by a doc page demonstrating
     `streampool.Flow` + `trace.ContextWithSpan`. No result-type
     wrapping needed; trace context rides on Flow's ctx through the
     standard `ctx.Value` / `trace.SpanFromContext` idioms.
   - Flow is **optional** — users who don't need lifecycle tracking
     just don't create one. Users who do create one via
     `streampool.NewFlow(parent)` (returns updated ctx); framework
     auto-refs/unrefs around work items dispatched with that ctx.

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
   `GatherFunc`, `Accumulator.Accumulate`, `SubmitErr`. This matches the
   existing psg-go semantics: errors flow alongside their associated
   values through the pipeline. `Submit(ctx, value)` is sugar for
   `SubmitErr(ctx, value, nil)`; callers reach for `SubmitErr` when
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

9. ~~**Submit ctx propagation.**~~ **Resolved (2026-05-24, updated
   2026-05-25 for Flow naming and Wave-boundary model).**

   For Flow lifecycle and OpenTelemetry trace context to ride `ctx`
   (replacing the result-type wrapping in psgwf/otpsg), the framework
   treats Submit ctx as load-bearing. Three boundary cases:

   - **Task body invocation.** Framework passes the Start ctx
     directly. Submit ctx originates here. No change from current
     behavior.

   - **Combiner.Accumulate invocation.** Framework captures the
     Submit ctx with each work item and uses it directly for the
     Accumulate call (layered via `ensureCtxMeta` to add combine-meta).
     Submit ctx is rooted in the user's Flow ctx (if any), which is
     rooted in the Wave ctx, which is rooted in the Pool ctx — so
     cancellation cascade works through the standard `context`
     hierarchy. Submit ctx values (trace span, Flow handle, audit
     metadata) are accessible via standard `ctx.Value` /
     `trace.SpanFromContext` idioms inside Accumulate.

   - **Gatherer Handler invocation.** Handler's ctx is rooted in the
     Submit ctx so that Flow, Wave, and Pool cancellation propagate
     via the standard parent chain. The puller's ctx (from
     `wave.Gather(pullCtx)`) is an independent cancellation source
     wired in as an additional source. Conceptually equivalent to:

     ```go
     ctx, cancel := context.WithCancel(submitCtx)      // rooted in submit
     stop := context.AfterFunc(pullCtx, cancel)         // pull also cancels
     defer func() { stop(); cancel() }()
     return handler.Handle(ctx, value, err)
     ```

     Result: Handler's `ctx.Done()` fires on Flow cancel, Wave
     cancel, Pool cancel, or puller cancel; `ctx.Value(key)` walks
     from Submit ctx upward. Implementation may use the literal
     stdlib pattern above OR a pooled-goroutine `mergedCtx` (with
     idle eviction and done-channel reuse to avoid per-call
     allocations on the hot path). Choice is a profiling decision;
     user contract is the same.

   **Flow lifecycle**: framework inspects Submit ctx for a Flow
   (via `streampool.FlowFromContext(ctx)`) at Submit time; `Dup()`s
   the Flow (incrementing refcount) before queueing the work item;
   `Close()`s it when the work item is `Free()`'d. Flow's `afterFn`
   fires when refcount hits zero. Flows are user-created via
   `streampool.NewFlow(parent, ...)` and may span multiple Waves —
   their lifecycle is determined by refcount across all work items
   that captured them, not by any one Wave's drain.

   **What this lets us delete**:
   - `psgwf` entirely (Flow replaces Workflow in main package)
   - `otpsg` entirely (replaced by a doc page on the standard
     `streampool.Flow` + `trace.ContextWithSpan` pattern)
   - All result-type wrapping plumbing in both

   **Machinery additions**:
   - Submit ctx stored as a field on each work item (one interface
     value).
   - Gather-boundary ctx construction: stdlib `WithCancel` + `AfterFunc`,
     or pooled-goroutine `mergedCtx`. Implementation choice deferred
     to profiling.
   - Flow Dup/Close bracketing around work-item lifecycle.

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

10. ~~**The three-type model (Pool / Wave / Flow).**~~
    **Resolved (2026-05-25).** The earlier "Pool as bounded context"
    framing conflated three distinct concerns: a process-level worker
    pool, a user-facing batch of work to await, and a single workflow
    instance. Splitting into Pool (workers, singleton-ish), Wave
    (batch, nestable, hosts ops), and Flow (workflow instance,
    ctx-borne, refcounted, can span Waves) gives each role a clean
    home. Multi-Pool usage shrinks to library-boundary resource
    isolation; multi-Wave is the typical pattern; Flow remains
    optional for cross-cutting metadata + lifecycle hooks. See the
    three-type model section at the top of this doc for details.

11. ~~**Pool lifecycle semantics.**~~ **Resolved (2026-05-25, refined
    2026-05-25 to refcount-driven model).** Pool has no `Shutdown` or
    `Wait` methods. Lifecycle is implicit:

    - **Construction**: `NewPool(ctx, opts...)` creates a custom Pool.
      A package-level default Pool exists implicitly for users who
      don't need a custom ctx or tuning.
    - **Refcount tracking**: each referencing Wave bumps the Pool's
      refcount on construction and drops it when its drain
      (`GatherAll`, terminal `Close+Gather`, or `CancelAndWait`)
      completes.
    - **Worker spin-up**: workers are created on demand as the first
      Wave starts dispatching work to them.
    - **Idle worker exit during active use** (`refcount > 0`):
      individual workers between work items exit per `IdleTimeout`.
      The transient-idle case in a busy Pool.
    - **Refcount → 0**: synchronous worker termination. The last
      Wave's drain method signals all workers to exit, blocks until
      they have, then returns. Guarantee: when the final drain
      returns, the Pool has zero live goroutines.
    - **Reuse**: a new Wave referencing the Pool after the refcount=0
      transition spins workers up again from scratch. Pool object
      itself unchanged; only the worker set cycles.
    - **Hard abort**: cancel the ctx given to `NewPool` (or, for the
      default Pool, the program's parent ctx if the user wired one
      in). Cascades to all referencing Waves, all in-flight work,
      all workers ASAP.

    Misbehaved tasks that ignore ctx degrade shutdown semantics
    (workers can't exit until they return from user code). Documented
    constraint, not framework-enforceable.

12. ~~**Cross-Wave Submit.**~~ **Resolved (2026-05-25).** Submit
    routinely crosses Wave boundaries — a worker running in Wave A
    can call `gatherer.Submit(ctx, value)` on an op constructed
    against Wave B. The submitted work item belongs to Wave B
    (counted in Wave B's drain). Flow rides on ctx and tracks the
    cross-Wave hop naturally; Flow's refcount holds across the
    transition.

    Sub-Wave Submit (parent ↔ child Waves) is the typical pattern
    for nested workflows. Cross-Pool Submit (between Waves in
    different Pools) is available but exotic — the library-isolation
    case.

    Stale-handle errors (Submit to a closed/cancelled Wave's op)
    return defined error sentinels: `ErrWaveClosed`, ctx error from
    Pool/Wave cancellation. Use-after-final-Unref is a programming
    error and may panic.

---

## Impact on the rest of the project

### README

`README-proposed.md` needs updates reflecting:
- The three-type model (Pool / Wave / Flow) — Pool typically
  constructed once at startup; Waves per batch of work; Flows
  optional for cross-cutting metadata.
- The Limiter-based concurrency model (replacing per-pool limits).
- The Submit-from-body model (replacing `.To(sink)` wiring).
- Updated hello world using `NewWave` + `NewTaskRunner(wave, ...)`
  + `Submit`.
- Candidate tagline: "streampool enables frictionless execution of
  recursive streams of work in hierarchical waves with granular
  visibility and control of individual flows." Captures all four
  pillars of the metaphor (Pool implicit in the package name; Waves;
  Flows; future Streams) in one sentence. "Frictionless" earns its
  place as a one-word claim covering three concrete technical
  properties: low developer ceremony (bound-op pattern,
  Submit-from-body), allocation-free hot path (interface-on-struct,
  pooled state), and zero hot-path contention (nbcq/rdvq/omnipool
  infrastructure). The metaphor is carried by the nouns
  (streams/waves/flows), so the adjective slot doesn't need to do
  fluid-themed work — it's freed up for a technical claim instead.

The comparison table rows mostly stand, but some footnotes need
adjustment (the "per-pool concurrency limits" row becomes "limiters
compose across pools"; the "end-to-end backpressure" footnote already
matches the new model; the "adaptive pool sizing" row gets simpler
since the user doesn't construct pools per batch).

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
- **Split today's conflated `Pool` into three types: Pool (workers),
  Wave (batch), Flow (workflow instance).** Pool's unit-of-work
  verbs migrate to Wave; psgwf.Workflow consolidates into Flow.
- Remove `.To`-style wiring from Combine/Task; route values via
  user-explicit `Submit` calls in op bodies.
- Eliminate the output type parameter from Combiner.
- Add `Limiter` interface and implementations; refactor pool-internal
  concurrency limits to accept Limiters per-op.
- Merge `TaskPool` and `CombinerPool` machinery into one internal
  worker pool with Limiter-based per-op control.
- Rename and re-shape examples, tests, sub-packages.
- Replace Pool's CloseAndGatherAll with Wave's GatherAll. Pool
  lifecycle becomes refcount-driven with synchronous worker
  termination on the last referencing Wave's drain. Establish a
  package-level default Pool for the common case.

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
