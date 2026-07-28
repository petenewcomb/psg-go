# API Design

The proposed API for the repositioned library (working name `streampool`,
formerly `psg-go`). Captures naming decisions, the final user-facing
surface, and the reasoning behind each choice.

This is a design artifact, not an implementation plan. The decisions here
are still revisable, but they form a coherent set — pulling one thread
often unravels several.

> **RECONCILING TO THE LOCKED SURFACE (2026-06-21).** A design pass converged the
> user-facing surface; this doc is being updated to match (full record:
> `WORKING_NOTES.md`, "SURFACE REDESIGN"). The load-bearing changes vs. the original
> (2026-05-25) text:
> - **Pool is internal** — no `NewPool` / `WithPool` / `PoolOption`; sizing is
>   automatic. Users construct **Wave** and optionally **Flow**.
> - **Ops are wave-agnostic** — constructors take no wave. The target is the body's
>   ambient wave, or `op.In(wave)` to place/redirect. No bind-at-construction, no
>   sentinel.
> - **Wave: no constructor (zero-value, lazy-init).** SUPERSEDED LATER (2026-06-21b):
>   the value-handle / `NewWave` / `Cancel`-`CancelAndWait` shape this doc still
>   describes in places gave way to a zero-value `var w streampool.Wave` (`*Wave`),
>   lazy `ensureInit`, drain-only lifecycle (no Cancel), bound via `op.In(&w)`. Full
>   evolution + rationale: `surface-lineage.md`. Flow stays a refcounted, ctx-borne
>   value handle (keeps `Dup`/`Close`) — the lone ctx-borne type. (That last sentence
>   is itself superseded — see the Flow bullet below.)
> - **Funnel is wave-scoped** — `Flush` / `FlushTo`, no `Close` / `Dup`;
>   finalization is wave-driven.
> - **Limiters are standalone values** — `WithLimits` jointly admits in a global
>   canonical order; **no user-facing Coordinator/Scheduler** (prioritization is a
>   deferred, internal-arbiter feature; no API named yet).
> - **Reentrancy: the only rule is "you cannot skim a wave you are part of."**
>   Principle 7's task-to-task prohibition / skim-queued-not-recursive are retired.
> - **The Flow type is GONE (2026-07-03).** The refcounted, ctx-borne `Flow` value
>   handle this doc still describes (`NewFlow` / `FlowFromContext` / `Dup` / `Close`
>   / `WithAfterFunc`) is superseded *entirely* by the flow facility —
>   `WithFlow(ctx, body, opts...)` plus user-minted flow keys/tags; there is NO Flow
>   type anymore. See `docs/decisions/flow-design.md`.

Companion docs:
- `docs/permit-core.md` — the permit allocation model (the hierarchical cache).
- `docs/dispatch-execution-split.md` — the dispatch/execution architecture.
- `docs/decisions/POSITIONING_RESEARCH.md` — outward-facing market/audience research.
- `docs/decisions/ARCHITECTURE_COMPARISON.md` — source-level contention/allocation
  analysis against competitors.

---

## Design principles

These guided every naming and shape decision below.

1. **User vocabulary first.** Pool, worker, task, submit, results,
   limiter — words developers actually type into search engines and use
   in issues. Avoid academic vocabulary (`scatter-gather`, `pipelined`,
   `monoid`) in the API surface.

2. **End-to-end backpressure is the differentiator.** Make the
   pool's adaptive sizing and skim-driven backpressure visible in the
   docs; let the API stay simple.

3. **No silent failures.** Forgetting a method call shouldn't cause data
   loss. If a method is structurally required for correctness, make it
   either positional or compile-time enforced.

4. **Match Go convention.** Functional options over builders for
   per-op configuration. Concrete return types, structural interfaces
   defined by consumers. `package.New` as primary constructor.

5. **Asymmetry tracks role.** Where roles genuinely differ (Task is
   stateless; Funnel has accumulator state; Skimmer is terminal), the
   API reflects that. Don't force uniformity that hides real
   differences.

6. **Allocations are a feature.** The bound-op pattern, funnel-factory
   closures, and pool of internal state are all designed so user code
   can run allocation-free on the hot path. This is psg-go's
   defensible technical differentiator.

7. **The trust boundary is the callback, not the package.** The
   foundational packages (rdvq, workq, delayq) maintain a notification
   conservation invariant that is *inductive over the set of
   participating nodes*: it holds only if every node discharges its
   "if I don't consume a wakeup, I re-propagate it" obligation. That
   induction closes only because the node set is finite, known, and
   single-authored — which is why these packages are `internal` and
   must never be exposed for extension. But hiding the packages is
   necessary, not sufficient: every user callback (Task, Handler,
   Skimmer, Funnel/accumulator, Recycler, and any lifecycle/metrics
   hook) runs *on a node's goroutine while that node's invariant is
   mid-flight*. So the rule for the API surface is: **every user
   callback must be a bracketed leaf** — it carries *zero* propagation
   duty (a leaf, never asked to re-notify), and the surrounding node
   discharges its conservation obligation *regardless of what the
   callback does* (blocks, panics, re-enters, spawns). The
   dispatch/execution split is exactly this defense: managers, which never
   run user code, discharge each node's conservation obligation no matter
   what an executor body does — block, panic, re-enter, spawn — and the one
   reentrancy rule (a body may not skim a wave it is part of) closes the only
   cycle. (The earlier task-to-task scatter prohibition / skim-queued-not-
   recursive constraints are retired — see the banner above.) Any future
   plug-in point — especially blocking-capable hooks — must pass the
   bracketed-leaf test or it punches a hole in conservation, no matter
   that the foundational types stay hidden. (This entry is a summary;
   the full treatment is a TODO — see TODO.md "Formal verification &
   foundations".)

---

## The model: Wave, Flow, and the internal Pool

Two user-facing types — **Wave** and **Flow** — plus the ops (the verbs). The
worker **Pool** is an internal implementation detail, surfaced only to explain
sizing. Keeping these concerns separate is what removes the API friction this
design exists to remove.

1. **Wave** — *the* user-facing primary type: a batch of work to complete
   together. A zero-value `var w streampool.Wave` (`*Wave`) is ready to use — no
   constructor — and owns no context. Ops route work into a wave (ambient inside a
   body, or `op.In(&w)` to place/redirect); `wave.Skim` / `SkimAll` /
   `CloseAndSkimAll` drain it, returning `ErrWaveDone` when complete. Waves nest (a
   sub-wave is just a zero-value Wave first used inside a body; its body's drain
   keeps the parent drain waiting, transitively) and run concurrently. The lifecycle
   is the drain — no `Cancel`, no `Dup`; cancellation rides the driving context.

2. **Flow** *(optional)* — **SUPERSEDED (2026-07-03): there is no Flow type; see
   `flow-design.md`.** As originally designed: one logical thread of related work:
   a refcounted, ctx-borne **value handle** that can span multiple Waves. Use a
   Flow to attach
   metadata (trace context, audit data) or a cleanup hook to work that crosses
   batch boundaries. It is the one type that **rides the ctx** (propagation) and
   the one that keeps **`Dup`/`Close`** (a cross-wave lifetime no single wave
   bounds). Most programs never construct one.

3. **Pool** *(internal)* — the worker goroutines. A process-wide default Pool
   serves all work, sized automatically (adaptive under demand, retiring idle
   workers; refcount-driven lifecycle). Not user-constructed or tuned; per-op
   concurrency is expressed with **Limiters** instead.

(The name "Stream" is reserved for a future observability concept — a stream of
Flow lifecycle events for monitoring/aggregation — so it is not used for any type
above, whose semantics are singular-instance.)

## The final API surface

```go
package streampool

// ===== Pool: internal worker pool (not user-facing) =====
//
// The worker Pool is NOT part of the surface. A process-wide default Pool serves
// all work, sized automatically (adaptive under demand; idle workers retire;
// refcount-driven lifecycle tied to active Waves). There is no NewPool / WithPool
// / PoolOption. Per-op concurrency is expressed with Limiters (below); a narrower
// cancellation domain comes from the ctx you dispatch/drive a Wave with.

// ===== Wave: the batch of work the user awaits =====

// Wave is per-batch state with an internal lifecycle, used as *Wave. There is NO
// constructor: a zero-value Wave (var w streampool.Wave) is ready to use and owns no
// context — it self-inits on first ctx-bearing use (op dispatch into it, or a drain),
// capturing the DRIVING ancestry. Ops are wave-AGNOSTIC (constructed without a wave);
// work is routed to a wave at dispatch — the body's ambient wave by default, or
// op.In(&w) to place/redirect (see "Routing"). A sub-wave is just a zero-value Wave
// first used inside a body; the body's drain of it keeps the parent drain waiting,
// transitively. No Dup.
//
// (Superseded forms — NewWave (two-return, then value-handle), NewChild, and
// Cancel/CancelAndWait — and why they were dropped: surface-lineage.md.)
type Wave struct { /* ... */ }

func (*Wave) Skim(ctx context.Context) error            // process one ready result through its Skimmer
func (*Wave) SkimAll(ctx context.Context) error         // drain without sealing (for a pure drainer)
func (*Wave) CloseAndSkimAll(ctx context.Context) error // seal + drain to completion — the terminal call
func (*Wave) Close()                                    // seal: no more top-level entries to this Wave

// All drains return ErrWaveDone once in-flight==0 and the wave is sealed. There is no
// Cancel and no framework force-abort: a Wave owns no context. Cancellation rides the
// driving ctx — cancel the ctx you drive/dispatch with; a drain then returns that
// ctx's error, and in-flight bodies stop via their own submit ctxs.

// ===== Flow: one logical thread of work (optional) =====
//
// SUPERSEDED (2026-07-03): this entire Flow surface is dead. There is no Flow type;
// the flow facility (streampool.WithFlow + NewFlowKey[V]/NewFlowTag riders) replaces
// it. Kept here as the historical record; see docs/decisions/flow-design.md.

// Flow is a refcounted, ctx-borne value handle for a single workflow instance —
// the ONE type that rides the ctx (propagation) and the ONE that keeps Dup/Close
// (a cross-wave lifetime no single Wave bounds). Use it to attach metadata (trace,
// audit) or a cleanup hook to work that may cross Wave boundaries.
type Flow struct { /* ... */ }

// NewFlow creates a Flow rooted in parent and returns a ctx that CARRIES the Flow.
// This is the deliberate exception to "constructors don't return a ctx" — a Flow's
// whole purpose is to propagate on the ctx. Pass that ctx into dispatches so the
// framework can ref/unref the Flow across each work item's lifecycle. Refcount
// starts at 1 (the caller's reference); afterFn fires when it reaches 0.
func NewFlow(parent context.Context, opts ...FlowOption) (context.Context, Flow)

// FlowFromContext returns the Flow attached to ctx (a non-counting view), or the
// zero Flow if none. Use to inspect or extend the Flow inside a body.
func FlowFromContext(ctx context.Context) Flow

// Dup returns an independent reference to the same Flow (refcount++). Use when
// handing the Flow to another goroutine that manages its own lifecycle.
func (Flow) Dup() Flow

// Close releases the caller's reference (refcount--). afterFn fires once all
// references (caller's + the framework's per-work-item refs) are released.
func (Flow) Close()

type FlowOption interface { /* ... */ }

func WithAfterFunc(fn func()) FlowOption  // fires when the Flow's refcount reaches 0

// ===== Limiters =====
//
// Permit model: docs/permit-core.md (a hierarchical permit cache, deadlock-free
// per-limiter) and docs/dispatch-execution-split.md.
//
// A Limiter is a standalone, composable concurrency-control value. Share one
// Limiter across ops for a collective cap; list several in WithLimits on one op
// (AND semantics — a dispatch proceeds only when all permit it). A multi-limiter
// set is admitted JOINTLY in a global canonical order, which is deadlock-free by
// construction (lock-ordering) and fully automatic — there is NO user-facing
// coordinator/scheduler and no grouping to declare.
//
// A Semaphore caps ACTIVE concurrency, not in-flight: a body parked driving a
// sub-wave lends its permit to that sub-wave's work (the cache's inheritance) and
// reacquires it to resume computing — which dissolves the held-across-a-skim
// livelock. A permit demand exceeding total capacity FAILS FAST — panic for a
// static (misconfigured) weight, a distinct error for a data-dependent oversized
// input — never blocking forever.
type Limiter struct { /* ... */ }  // opaque; obtained from the constructors below

func NewSemaphore(n int) Limiter                    // cap active concurrency at n
func NewRateLimit(n int, d time.Duration) Limiter   // (future) wraps x/time/rate

// Future (additive): more limiter constructors — NewMemoryLimiter, weighted /
// cost limiters, NewAdaptive(...); optionally an exported Resource interface +
// NewLimiter(resource) for user-defined resources. Prioritized admission
// (cross-op priority + anti-starvation) is a separate deferred feature backed by
// an internal global arbiter — no user-facing coordinator type; its API is left
// to its own design effort and not specified here.

// ===== User-supplied interfaces =====
//
// All user inputs are defined as interfaces, not function signatures.
// This lets users implement on structs (with state as fields) to avoid
// closure allocations on the hot path. Function-type adapters are
// provided for the closure case, matching the http.Handler /
// http.HandlerFunc pattern.

// Handler is the universal interface for op bodies — implemented by
// user-supplied work that Launcher runs on a worker or Skimmer
// invokes during drain. The method is named Handle; sync execution
// verb on the interface, parallel to http.Handler.ServeHTTP. The
// user-facing async dispatch verbs (Submit / SubmitErr /
// SubmitResult; Start is void sugar) live on the op types.
type Handler[T any] interface {
    Handle(ctx context.Context, value T, err error) error
}

// HandlerFunc[T] is the canonical adapter — matches Handler[T].Handle
// exactly. Use this when you want a closure-based handler with both
// a value and an upstream err.
type HandlerFunc[T any] func(context.Context, T, error) error
func (f HandlerFunc[T]) Handle(ctx context.Context, value T, err error) error {
    return f(ctx, value, err)
}

// Task is the named func adapter for the no-input case: a piece of
// work that runs without a value or upstream err. Satisfies
// Handler[struct{}], so it plugs into NewLauncher (the common case)
// and into NewSkimmer (rare; a value-less sink).
//
// Short-circuit semantics: Handle returns the upstream err
// immediately when it is non-nil, without invoking the wrapped
// closure. This matches the convenience-adapter contract: a Task
// closure that declined to accept an err arg almost certainly didn't
// plan to run when one was already in flight. The two escape
// hatches:
//
//   - To run on err and handle it, use [ErrHandler].
//   - To run regardless of err (cleanup, always-fire side effects),
//     write a [HandlerFunc][struct{}] that ignores err, or implement
//     Handler[struct{}] directly on a struct.
//
// No paired Task interface — no-arg bodies almost always close over
// state from the surrounding scope, so the struct-implementation
// pattern that justifies exposing Handler[T] as an interface doesn't
// pay off strongly for T = struct{}. Users who do want to implement
// the no-arg case on a struct write Handler[struct{}] directly with
// Handle(ctx, _ struct{}, err error) error and choose their own
// err-handling policy.
type Task func(context.Context) error
func (f Task) Handle(ctx context.Context, _ struct{}, err error) error {
    if err != nil {
        return err
    }
    return f(ctx)
}

// ErrHandler is Task's err-receiving sibling — a no-value handler
// that receives the upstream err and decides what to do with it
// (rewrite, suppress, log, etc.). Named descriptively (handling an
// err) rather than tied to either op type's vocabulary, since it
// reads naturally in both Launcher and Skimmer contexts.
//
// Unlike [Task], ErrHandler does not short-circuit — it always
// invokes the wrapped closure, passing err through. That's the whole
// point: the user opted into the err-receiving signature precisely
// because they want the err to reach their code.
type ErrHandler func(context.Context, error) error
func (f ErrHandler) Handle(ctx context.Context, _ struct{}, err error) error { return f(ctx, err) }

// Accumulator — the per-instance interface an AccumulatorFactory
// returns. Mirrors the existing combine-style shape: Accumulate per
// input, Flush when the framework needs the instance to finalize.
// Distinct from Handler because the two-method shape (Accumulate +
// Flush) and the (deadline, error) return on Accumulate make it
// genuinely different from a single-dispatch handler.
type Accumulator[T any] interface {
    Accumulate(ctx context.Context, value T, err error) (deadline time.Time, returnErr error)
    Flush(ctx context.Context) error
}

// AccumulatorFactory creates per-instance Accumulators for a Funnel.
// The framework calls NewAccumulator whenever fresh accumulator
// state is needed. Close fires once when the bound Funnel's
// refcount hits zero — gives the factory a hook to release
// factory-level state. Errors from Close surface through SkimAll
// (same path as Accumulator errors).
type AccumulatorFactory[T any] interface {
    NewAccumulator() Accumulator[T]
    Close() error
}

// FuncAccumulator builds an Accumulator from function fields; FlushFn
// is optional. Convenience for the common closure-over-state case.
type FuncAccumulator[T any] struct {
    AccumulateFn func(ctx context.Context, value T, err error) (time.Time, error)
    FlushFn      func(ctx context.Context) error
}

func NewAccumulator[T any](
    accumulate func(ctx context.Context, value T, err error) (time.Time, error),
    flush func(ctx context.Context) error,
) FuncAccumulator[T]

// FuncErrAccumulator: err-only Accumulator[struct{}] adapter — the
// user's Accumulate body receives (ctx, err) without the void value
// parameter. Saves a framework-added signature-adapter closure when
// the user's body doesn't care about the void value.
type FuncErrAccumulator struct {
    AccumulateFn func(ctx context.Context, err error) (time.Time, error)
    FlushFn      func(ctx context.Context) error
}

func NewErrAccumulator(
    accumulate func(ctx context.Context, err error) (time.Time, error),
    flush func(ctx context.Context) error,
) FuncErrAccumulator

// FuncAccumulatorFactory: factory-closure adapter for
// AccumulatorFactory[T]. Use when each Accumulator needs per-instance
// state via closure capture.
type FuncAccumulatorFactory[T any] struct {
    NewAccumulatorFn func() Accumulator[T]
    CloseFn          func() error
}

func NewAccumulatorFactory[T any](
    newAccumulator func() Accumulator[T],
    closeFn func() error,
) FuncAccumulatorFactory[T]

// AccumulatorFactoryFunc[T]: bare-func adapter — equivalent to a
// FuncAccumulatorFactory with no CloseFn. Compact form for the
// no-cleanup case.
type AccumulatorFactoryFunc[T any] func() Accumulator[T]

// FuncErrAccumulatorFactory: err-only direct-fn-storage adapter for
// AccumulatorFactory[struct{}]. Stores the AccumulateFn / FlushFn /
// CloseFn as struct fields directly (no factory closure). Each
// NewAccumulator() call returns a fresh FuncErrAccumulator with the
// stored fns copied in. Zero framework-added closures. Suitable for
// stateless err-aggregation; for per-instance state, use
// FuncAccumulatorFactory[struct{}] with a NewErrAccumulator inside
// the factory closure.
type FuncErrAccumulatorFactory struct {
    AccumulateFn func(ctx context.Context, err error) (time.Time, error)
    FlushFn      func(ctx context.Context) error
    CloseFn      func() error
}

func NewErrAccumulatorFactory(
    accumulate func(ctx context.Context, err error) (time.Time, error),
    flush func(ctx context.Context) error,
    closeFn func() error,
) FuncErrAccumulatorFactory

// ===== Op constructors =====
//
// Ops are WAVE-AGNOSTIC: constructors take no wave, only the user-supplied
// interface. An op is a reusable spec — define it once (even before any wave
// exists) and route its work to a wave at dispatch (see "Routing"). By
// convention, name an op for its role as a noun distinct from its output —
// Launcher: fetcher/crawler; Funnel: aggregator (+ totals); Skimmer: collector
// (+ results) — so `op.Submit(...)` reads naturally.
//
// For raw closures, use HandlerFunc[T] (parameterized), Task
// (no-arg, no-err), or ErrHandler (no-arg, with err); for Funnel,
// use FuncAccumulator[T].

// Per-op options (functional)
type OpOption interface { /* ... */ }

func WithLimits(limiters ...Limiter) OpOption
// ... future: WithPriority, WithDeadline, WithRetry, etc.

// ===== Constructor progression =====
//
// Each op type exposes a progression of constructors from most
// general (interface-accepting, alloc-free hot path) to most
// specialized. Users pick the shortest one that fits their case.
//
// Launcher:
//   NewLauncher(handler Handler[T], opts...)         // interface; struct or HandlerFunc
//   NewFnLauncher(fn func(ctx, T, error) error, ...) // closure form, T inferred
//   NewTaskLauncher(fn func(ctx) error, opts...)     // no-arg (T=struct{}); wraps in TaskFunc
//   NewErrLauncher(fn func(ctx, err) error, opts...) // err-only (T=struct{}); wraps in ErrHandlerFunc
//
// Skimmer (same pattern, no TaskSkimmer):
//   NewSkimmer(handler Handler[T])
//   NewFnSkimmer(fn func(ctx, T, error) error)
//   NewErrSkimmer(fn func(ctx, err) error)
//
// Funnel (interface accepts AccumulatorFactory[T]; closure form
// takes factory + close fns; err-only form takes accumulate/flush/
// close fns directly via FuncErrAccumulatorFactory — zero framework-
// added closures):
//   NewFunnel(factory AccumulatorFactory[T], opts...)
//   NewFnFunnel(newAccFn, closeFn, opts...)
//   NewErrFunnel(accumulate, flush, closeFn, opts...)

func NewLauncher[T any](handler Handler[T], opts ...OpOption) Launcher[T]
func NewFnLauncher[T any](handle func(ctx context.Context, value T, err error) error, opts ...OpOption) Launcher[T]
func NewTaskLauncher(task func(ctx context.Context) error, opts ...OpOption) TaskLauncher
func NewErrLauncher(handle func(ctx context.Context, err error) error, opts ...OpOption) ErrLauncher

func NewSkimmer[T any](handler Handler[T]) Skimmer[T]
func NewFnSkimmer[T any](handle func(ctx context.Context, value T, err error) error) Skimmer[T]
func NewErrSkimmer(handle func(ctx context.Context, err error) error) ErrSkimmer

func NewFunnel[T any](factory AccumulatorFactory[T], opts ...OpOption) Funnel[T]
func NewFnFunnel[T any](newAccumulator func() Accumulator[T], closeFn func() error, opts ...OpOption) Funnel[T]
func NewErrFunnel(accumulate func(ctx, err error) (time.Time, error), flush func(ctx) error, closeFn func() error, opts ...OpOption) ErrFunnel

// ===== Void-T type aliases (named for intent) =====

type Task                  = Handler[struct{}]              // no-arg or void-value handler
type ErrHandler            = Handler[struct{}]              // err-only handler (alias for Task; distinct intent name)
type ErrAccumulator        = Accumulator[struct{}]
type ErrAccumulatorFactory = AccumulatorFactory[struct{}]
type TaskLauncher          = Launcher[struct{}]
type ErrLauncher           = Launcher[struct{}]
type ErrSkimmer            = Skimmer[struct{}]
type ErrFunnel             = Funnel[struct{}]

// For "reducer" behavior — strictly serial accumulation, only one
// instance active at a time — construct a Funnel with a 1-permit
// limiter:
//
//   ordered := streampool.NewFunnel(factory,
//       streampool.WithLimits(streampool.NewSemaphore(1)),
//   )

// ===== Routing: ambient wave + op.In(wave) =====
//
// An op has no wave of its own; each dispatch targets a wave, resolved as:
//
//   1. op.In(wave).Submit(ctx, v)  — explicit: place this op's work in `wave`.
//      Used at top level (no ambient wave) and to redirect into a child/other
//      wave. In(wave) returns a cheap wave-bound handle (a value: bind once and
//      reuse, or chain inline). It is membership, not a value destination — the
//      value still goes to the op; `wave` is the batch the work is accounted to.
//   2. op.Submit(ctx, v)           — ambient: inside a body the framework stamps
//      the running body's wave on ctx, so an un-routed Submit lands in that wave.
//      The common in-body case.
//   3. An un-routed Submit from a goroutine with no ambient wave (e.g. top level)
//      panics, naming the fix: route with In(wave).
//
// Routing is handle-level, so it never perturbs ctx — Flow / trace / cancellation
// ride ctx untouched across a redirect: op.In(other).Submit(bodyCtx, v) keeps
// bodyCtx's propagation while targeting `other`.

// ===== Op types =====
//
// All three ops share the same dispatch family, layered as sugars
// over a single primitive (TrySubmitResult taking a deadline
// parameter). Launcher additionally has Start / TryStart as sugar
// for the void case (T = struct{}). Dispatch methods take no wave — route with
// op.In(wave) when not using the ambient wave (see "Routing").
//
// Naming pattern: each method's name describes exactly what's being
// submitted. Submit takes a value. SubmitErr takes an err.
// SubmitResult takes the full (value, err) result tuple. The
// frequency ordering is value-only > err-only > both — callers in
// Go-idiomatic code reflexively decompose (v, err) at the dispatch
// site, branching to a value sink on success and an err sink on
// failure. The both-case exists for sinks that genuinely want the
// pair (logged outcomes, status-aware accumulators) but is the
// least common shape — hence the longest name.
//
// Six methods per sink, all layered sugars over the single
// primitive TrySubmitResult(ctx, deadline, v, err) (bool, error):
//   - Submit(ctx, v)              = TrySubmitResult(ctx, Forever, v, nil),   bool dropped
//   - SubmitErr(ctx, err)         = TrySubmitResult(ctx, Forever, zero, err), bool dropped
//   - SubmitResult(ctx, v, err)   = TrySubmitResult(ctx, Forever, v, err),    bool dropped
//   - TrySubmit(ctx, t, v)        = TrySubmitResult(ctx, t, v, nil)
//   - TrySubmitErr(ctx, t, err)   = TrySubmitResult(ctx, t, zero, err)
//   - TrySubmitResult(ctx, t, v, err) = primitive
//
// The deadline parameter is a time.Time interpreted as:
//   - time.Time{} (zero) → attempt once (safer default than block-forever)
//   - Forever (sentinel) → block until success
//   - past time          → fail fast; no attempt
//   - future time        → bounded wait
//
// Submit / SubmitErr / SubmitResult are convenience names for the
// "block forever" case; they drop the bool return because the
// deadline is suppressed. See "Forever sentinel and dispatch model"
// below for the full deadline value semantics.

// Forever is the deadline-sentinel value for "block until success."
// Pass to TrySubmit / TrySubmitErr / TrySubmitResult to express the
// same behavior as Submit / SubmitErr / SubmitResult (without the
// bool drop).
var Forever time.Time = /* implementation-chosen specific instant; opaque */

type Launcher[T any] struct { /* ... */ }
func (Launcher[T]) In(wave Wave) Launcher[T]   // route work to wave (top-level / redirect); ambient otherwise
// Dispatch surface. All sinks share this shape (TrySubmitResult is the primitive).
func (Launcher[T]) Submit(ctx context.Context, v T) error                                                       // sugar — value only, block forever
func (Launcher[T]) SubmitErr(ctx context.Context, err error) error                                              // sugar — err only, block forever
func (Launcher[T]) SubmitResult(ctx context.Context, v T, err error) error                                      // sugar — both, block forever
func (Launcher[T]) TrySubmit(ctx context.Context, deadline time.Time, v T) (bool, error)                        // sugar — value only
func (Launcher[T]) TrySubmitErr(ctx context.Context, deadline time.Time, err error) (bool, error)               // sugar — err only
func (Launcher[T]) TrySubmitResult(ctx context.Context, deadline time.Time, v T, err error) (bool, error)       // primitive
// Start sugars for the void case (T = struct{}).
func (Launcher[T]) Start(ctx context.Context) error                                                             // sugar for Submit(ctx, *new(T))
func (Launcher[T]) TryStart(ctx context.Context, deadline time.Time) (bool, error)                              // sugar for TrySubmit(ctx, deadline, *new(T))

// Funnel — stateful aggregation via factory-created Accumulator instances,
// wave-scoped (per-(funnel,wave) instances owned by the wave, force-flushed at
// the wave's drain). Parallel by default; cap parallelism via WithLimits.
type Funnel[T any] struct { /* ... */ }
func (Funnel[T]) In(wave Wave) Funnel[T]   // route accumulate/flush to wave; ambient otherwise
func (Funnel[T]) Submit(ctx context.Context, v T) error                                                         // sugar — value only, block forever
func (Funnel[T]) SubmitErr(ctx context.Context, err error) error                                                // sugar — err only, block forever
func (Funnel[T]) SubmitResult(ctx context.Context, v T, err error) error                                        // sugar — both, block forever
func (Funnel[T]) TrySubmit(ctx context.Context, deadline time.Time, v T) (bool, error)                          // sugar — value only
func (Funnel[T]) TrySubmitErr(ctx context.Context, deadline time.Time, err error) (bool, error)                 // sugar — err only
func (Funnel[T]) TrySubmitResult(ctx context.Context, deadline time.Time, v T, err error) (bool, error)         // primitive
func (Funnel[T]) Flush(ctx context.Context) error                                                              // finalize this funnel's instances; aggregates emit into the ambient wave
func (Funnel[T]) FlushTo(ctx context.Context, wave Wave) error                                                 // one-shot finalize; aggregates emit into `wave` (snapshot / staged capture)

// Skimmer — terminal sink; the Handler runs on the draining goroutine at Wave.Skim.
type Skimmer[T any] struct { /* ... */ }
func (Skimmer[T]) In(wave Wave) Skimmer[T]   // route work to wave (top-level / redirect); ambient otherwise
func (Skimmer[T]) Submit(ctx context.Context, v T) error                                                        // sugar — value only, block forever
func (Skimmer[T]) SubmitErr(ctx context.Context, err error) error                                               // sugar — err only, block forever
func (Skimmer[T]) SubmitResult(ctx context.Context, v T, err error) error                                       // sugar — both, block forever
func (Skimmer[T]) TrySubmit(ctx context.Context, deadline time.Time, v T) (bool, error)                         // sugar — value only
func (Skimmer[T]) TrySubmitErr(ctx context.Context, deadline time.Time, err error) (bool, error)                // sugar — err only
func (Skimmer[T]) TrySubmitResult(ctx context.Context, deadline time.Time, v T, err error) (bool, error)        // primitive
// (No Flush / Close on Skimmer — it is terminal; SkimAll completion is driven by
// the wave's in-flight tracking, not by an explicit end-of-input signal.)
```

### Forever sentinel and dispatch model

All dispatch verbs reduce to one primitive — `TrySubmitResult(ctx,
deadline, v, err) (bool, error)` — with the deadline value
controlling the wait behavior. Submit / SubmitErr / SubmitResult /
TrySubmit / TrySubmitErr are layered sugars over it.

**Deadline value semantics:**

| Value | Behavior |
|---|---|
| `time.Time{}` (zero) | attempt once; return immediately |
| `streampool.Forever` | block until success |
| past time | already-expired; fail fast without attempting |
| future time | bounded wait until deadline |

**Why zero = "attempt once":**

`Try` already implies "attempt without committing to wait" (per the
Go `TryLock` precedent). The deadline parameter on TrySubmit is "how
long am I willing to wait if it doesn't dispatch immediately?" — a
zero value naturally answers "not at all." That gives the safest
default for programmers who don't have a specific deadline to set
and pass the zero value: one attempt that fails fast, rather than a
silent indefinite block. Block-forever stays available via the
named `Submit` / `SubmitErr` / `SubmitResult` sugars or via the
explicit `Forever` sentinel.

**Implementation notes:**

- The blocking sugars (`Submit` / `SubmitErr` / `SubmitResult`) call
  the primitive with `Forever` and drop the returned bool. The bool
  is meaningless when the deadline is suppressed (you either
  dispatch or you error).
- An already-expired deadline (`time.Now().After(deadline)`) returns
  false-with-nil-error without attempting. This matches the
  ecosystem convention for operations that take a deadline —
  `context.WithDeadline` with a past time produces an immediately-
  canceled ctx; `semaphore.Weighted.Acquire` on a canceled ctx
  returns the ctx error without attempting acquisition. A deadline
  parameter says "I will not wait past this time," so already-past
  means no attempt.
- Generic dispatch over an arbitrary deadline value composes
  cleanly: pass any time value through TrySubmit / TrySubmitErr /
  TrySubmitResult and the deadline value alone determines behavior.
  No method branching required at the call site.

The `For(duration)` variant was considered and dropped (see What we
chose not to do): aside from the naming reading ambiguously next to
a time-typed value, the bigger problem is that `For` silently picks
"now" as the base time. In real code the relevant base often isn't
the dispatch instant — it's a request's `receivedAt`, a task's
`enqueuedAt`, a retry's `firstAttemptAt`, or some other domain time.
Forcing the user to write `baseTime.Add(d)` explicitly makes them
confront the base-time choice; the `For` sugar would actively hide it.

## Hello world

```go
ctx := context.Background()

// Wave: the batch of work this function awaits. Zero value is ready to use; the
// worker pool is internal.
var wave streampool.Wave

// Skimmer (terminal sink): runs on the draining goroutine as results arrive.
printer := streampool.NewSkimmer(streampool.HandlerFunc[*User](
    func(ctx context.Context, user *User, err error) error {
        if err != nil { return err }
        fmt.Println(user.Name)
        return nil
    },
))

// Launcher: fetches each id on a worker, then submits the user to the printer.
fetcher := streampool.NewLauncher(streampool.HandlerFunc[UserID](
    func(ctx context.Context, id UserID, err error) error {
        if err != nil { return err }
        user, ferr := userClient.Fetch(ctx, id)
        if ferr != nil { return ferr }
        return printer.Submit(ctx, user) // ambient: lands in this body's wave
    },
))

// Top level has no ambient wave, so route explicitly with In(&wave):
for _, id := range userIDs {
    fetcher.In(&wave).Submit(ctx, id)
}

wave.CloseAndSkimAll(ctx) // seal + drain to completion
```

## With a Flow

> **SUPERSEDED (2026-07-03).** The Flow object below no longer exists; the equivalent
> under the flow facility is a `WithFlow` scope with a key/tag `FollowUp` option. See
> `docs/decisions/flow-design.md`.

Flow is optional — add one to attach logical-thread metadata (trace, audit) or a
cleanup hook to work that may cross Wave boundaries:

```go
ctx, flow := streampool.NewFlow(ctx, streampool.WithAfterFunc(func() {
    // fires when refcount reaches 0 — all work attributed to this Flow is done
}))
defer flow.Close() // releases the caller's reference; framework refs come from work items

// Dispatch with this ctx; the Flow rides it onto every work item and across waves.
fetcher.In(&wave).Submit(ctx, id)
```

## With a funnel

```go
// NewFnFunnel takes the factory closure directly (and a factory-level closeFn,
// nil here). NewFunnel takes the AccumulatorFactory interface instead.
aggregator := streampool.NewFnFunnel(
    func() streampool.Accumulator[int] {
        var sum int
        return streampool.NewAccumulator(
            func(ctx context.Context, x int, err error) (time.Time, error) {
                if err != nil { return time.Time{}, err }
                sum += x
                if sum >= flushThreshold {
                    if serr := totals.Submit(ctx, sum); serr != nil {
                        return time.Time{}, serr
                    }
                    sum = 0
                }
                return time.Time{}, nil // no deadline; otherwise flushed at wave drain
            },
            func(ctx context.Context) error { // final flush
                if sum != 0 { return totals.Submit(ctx, sum) }
                return nil
            },
        )
    },
    nil, // no factory-level Close
)

scorer := streampool.NewLauncher(streampool.HandlerFunc[UserID](
    func(ctx context.Context, id UserID, err error) error {
        if err != nil { return err }
        user, ferr := userClient.Fetch(ctx, id)
        if ferr != nil { return ferr }
        return aggregator.Submit(ctx, user.Score) // ambient wave
    },
))
```

The factory creates a fresh accumulator (with its own closure-captured `sum`) on
demand by concurrency. Flushing is the instance's job — a downstream `Submit` from
the accumulate step (incremental) or the final-flush step; the wave force-flushes
any not-yet-flushed instances at its drain. To finalize early or capture a snapshot
into another wave, call `aggregator.Flush(ctx)` / `aggregator.FlushTo(ctx, out)`.

## Allocation-free dispatch

To avoid per-call closure allocations on the hot path, implement the
`Handler[T]` interface directly on a struct rather than wrapping a
closure:

```go
type userFetcher struct {
    db   *Database
    sink streampool.Skimmer[*User]
}

func (f *userFetcher) Handle(ctx context.Context, id UserID, err error) error {
    if err != nil { return err }
    user, ferr := f.db.Fetch(ctx, id)
    if ferr != nil { return ferr }
    return f.sink.Submit(ctx, user)
}

fetcher := streampool.NewLauncher(&userFetcher{db: db, sink: printer})

in := fetcher.In(wave) // bind once; reuse across the loop
for _, id := range userIDs {
    in.Submit(ctx, id)  // no closure allocation per call
}
```

Combined with a pooled argument type, the dispatch loop can run with
zero allocations per call. The same struct-implementation pattern
also works for `T = struct{}` if you want an alloc-free no-arg task
— pay the cosmetic cost of two unused params in the `Handle` method
signature (`Handle(ctx, _ struct{}, _ error) error`).

## With limiters

A limiter is a standalone value:

```go
slowAPI := streampool.NewSemaphore(5)

fetcher := streampool.NewLauncher(fetchFn,
    streampool.WithLimits(slowAPI),
)
```

Compose several limiters on one op — they are admitted jointly in a global order,
deadlock-free and automatic; there is no coordinator to construct:

```go
apiRate := streampool.NewRateLimit(100, time.Second) // future

fetcher := streampool.NewLauncher(fetchFn,
    streampool.WithLimits(slowAPI, apiRate),
)
```

Limiters compose with AND semantics: a dispatch proceeds only when all attached
limiters permit. The same `Limiter` shared across ops expresses "these collectively
cap at N concurrent."

## Reusable ops

Because ops are wave-agnostic, a library can build and return one without knowing
which wave the caller will use; the caller routes it with `In(wave)`, or it resolves
to the ambient wave inside a body.

```go
// Library helper: a wave-agnostic Skimmer.
func NewLogSkimmer(logger *slog.Logger) streampool.Skimmer[Event] {
    return streampool.NewSkimmer(streampool.HandlerFunc[Event](
        func(ctx context.Context, e Event, err error) error {
            logger.Info("event", "name", e.Name, "err", err)
            return nil
        },
    ))
}

events := NewLogSkimmer(slog.Default())

runner := streampool.NewLauncher(streampool.HandlerFunc[ID](
    func(ctx context.Context, id ID, _ error) error {
        return events.Submit(ctx, process(id)) // ambient: this body's wave
    },
))
```

Inside a body, `events.Submit(ctx, …)` lands in the body's ambient wave. At top
level there is no ambient wave, so route explicitly: `events.In(wave).Submit(…)`;
an un-routed top-level `Submit` panics, naming the fix.

---

## Naming decisions

> **Rationale below predates the 2026-06-21 lock.** These tables, the psg-go
> mapping, "What we chose not to do," and the resolved open questions record how the
> design got here and still hold for the names that survived (Wave, Flow,
> Launcher/Funnel/Skimmer, Submit, Handler, the deadline model). They have NOT all
> been re-edited to the locked surface, so where a row conflicts with the surface
> above, **the surface wins.** Known reversals: **Pool is internal** (no
> NewPool/WithPool); **ops are wave-agnostic** with `op.In(wave)` routing — note the
> "`op.In(wave)` bind-chain" listed below as *rejected* is in fact what was adopted,
> its objection having dissolved once Funnel lost Close/Dup; **Funnel has no
> Close/Dup** (→ `Flush`/`FlushTo`, wave-driven finalization); **no user-facing
> Coordinator/Scheduler** (global-order joint admission). Full record:
> `WORKING_NOTES.md`, "SURFACE REDESIGN."

| Decision | Choice | Reasoning |
|---|---|---|
| Package name | `streampool` | Matches user-vocabulary search terms (worker pool / streaming results); distinct on pkg.go.dev; encodes the differentiator without being cute. The tagline lands literally: a Pool of workers runs Waves of work hosting Flows of related processing. |
| Three concerns, three types | `Pool` (workers), `Wave` (batch), `Flow` (workflow instance) | The conflated single-type model produced confusing "what does Pool actually do?" questions. Separating: Pool manages goroutines (singleton-ish, process-level); Wave is the user-facing batch primitive that hosts ops and exposes drain verbs; Flow is the ctx-borne refcounted lifecycle entity for a single workflow instance, independent of any Wave. |
| Role-3 name: `Wave` (not `Job` / `Group` / `Batch`) | "A wave of processing" carries the right metaphor: waves can overlap (multiple in flight in the same Pool), vary in size (small ripples to large processing bursts), and contain smaller waves (nested sub-batches). Coheres with the streampool nautical theme. `Job` is acceptable but reads as a discrete K8s/SLURM-style unit; `Group` clashes with errgroup. `Wave` is fresh and metaphorically apt. |
| Role-2 name: `Flow` (not `Stream` / `Workflow`) | `Stream` denotes "a flow of items" in standard usage — Java/Akka/Kafka/Node streams. Our role-2 type holds no payload; it's a refcounted lifecycle marker. Stream would mislead. `Flow` reads concretely (one specific flow of work) without the abstract-vs-concrete ambiguity of `Workflow`, has no Argo/BPM/Temporal baggage, is short, and coheres with the nautical theme. `Stream` is reserved for a future observability concept (a stream of Flow events). |
| Worker pool | `Pool` — fungible, often implicit | Pool's job is just goroutines, idle policy, max budget. No `Skim`, no `Shutdown`, no `Wait` — refcount-driven lifecycle handles termination implicitly. A package-level default Pool exists; users only call `NewPool` for a non-default ctx (cancellation domain) or non-default tuning. Matches `sync.Pool` convention as a fungible resource container. |
| Op constructor first arg | `wave Wave` (AtDispatch to defer) | Ops bind to a Wave at construction (their lifecycle owner). Pool comes via the Wave (which references a Pool through default or `WithPool`). Flow comes in dynamically via ctx, not at construction — because a Flow can span Waves but ops can't. Passing AtDispatch defers wave-binding to dispatch time, resolving from the dispatching ctx; the same op can then be reused inside any wave's body. The dispatch methods never take a Wave — the wave is locked in at construction (explicitly or as the deferred-to-ctx sentinel). |
| Dispatch methods take no Wave | `Submit(ctx, v)` / `SubmitErr(ctx, v, err)` / `Start(ctx, ...)` | Considered three alternatives and rejected each: (a) explicit Wave on every dispatch — verbose in the common case where one wave handles many dispatches; (b) `Submit` / `SubmitIn` method split — doubles surface for every dispatch verb; (c) `op.In(wave)` bind-chain — breaks the lifecycle model for ops with Close (Funnel, Skimmer), since the unassigned intermediate handle has no way to be closed. Wave-at-construction with the AtDispatch sentinel preserves single-verb dispatch, single lifecycle, and explicit binding when desired. |
| Pool lifecycle | Refcount-driven; workers exit synchronously on last Wave drain | No `Shutdown` / `Wait` API. Each referencing Wave bumps refcount; drain completion drops it. When count → 0, workers terminate synchronously before the last Wave's drain returns — strong guarantee that no Pool goroutines outlive the user's drain calls. Pool reuse after this is automatic; the next Wave that references the Pool spins workers up again. |
| Op trio names: `Launcher`, `Funnel`, `Skimmer` | Three fluid-handling concretes that reinforce the streampool nautical theme | **Launcher**: a launch is a small motorboat (nautical noun); also the verb sense ("launch a task," "launch a ship"). Universally understood; pairs cleanly with Start/Submit dispatch verbs. **Funnel**: the device that channels many inputs into a narrower output — exact metaphor for what a Combiner does (many submitted values → aggregated downstream output). Used as noun directly (no `-er` suffix needed); no awkward agent-noun derivation. **Skimmer**: pool skimmers are literal devices that pull debris from a pool's surface as it arrives — exact metaphor for pulling completed results from a wave's queue. Also a wading bird (nautical), and skimboarding is a form of wave-riding (themantic). All three are concrete physical referents, casual vocabulary (per Principle #1), and free of the academic baggage of `Gather`/`Combine`. The Wave's drain verbs match: `Wave.Skim` / `Wave.SkimAll` / `Wave.TrySkim`. |
| Op constructor verb | `NewLauncher`, `NewFunnel`, `NewSkimmer` | Agent nouns (`-er` suffix where it pays; Funnel is a noun directly). The type names describe roles, not the function-call verb. Matches `http.Handler`, `io.Reader`, `sync.Mutex`. |
| Sink dispatch verb | `Submit` / `SubmitErr` | Committed-delivery semantics: "submit this value to the sink." Avoids the Java `BlockingQueue.offer` baggage that would mislead users to expect try-semantics from `Offer`. Used uniformly across Launcher, Funnel, and Skimmer — submitting a value to a Launcher dispatches a task with that value as its arg, exactly mirroring how Submit works for Funnel and Skimmer. |
| `Start` as sugar for void Launcher dispatch | `Start(ctx)` == `Submit(ctx, *new(T))` | When `T = struct{}` (the no-input task case), `Submit(ctx, struct{}{})` is the explicit form and reads awkwardly. `Start(ctx)` is the sugar — matches the conventional "start a fire-and-forget task" intent and the `os/exec.Cmd.Start()` precedent. Available on all `Launcher[T]` instantiations; meaningful primarily when T's zero value is conventional (`struct{}` or similar). |
| Layered sugar over one TrySubmitResult primitive | `TrySubmitResult(ctx, deadline time.Time, v, err) (bool, error)` is the only primitive. Six methods per sink: `Submit(v)`, `SubmitErr(err)`, `SubmitResult(v, err)` use `Forever` and drop the bool; the three Try variants take an explicit deadline and return the bool. | Each method's name describes exactly what's being submitted — value only, err only, or the full (v, err) pair. Generic dispatch over a deadline value works through a single funnel; no method branching needed at any call site. |
| Three submission shapes per sink: Submit / SubmitErr / SubmitResult | Named by what they take, not by err presence/absence | The dispatch frequency in practice is value-only > err-only > both. Go programmers reflexively decompose `(v, err)` at the call site, branching to a value sink on success and an err sink on failure — both-case sinks (logged outcomes, status-aware accumulators) exist but are the least common. The naming matches the frequency: shortest name on the most common case (`Submit(v)`), short marked name on the second (`SubmitErr(err)`), longest on the rare both-case (`SubmitResult(v, err)`). The pattern "each name describes its args literally" beats the alternative "Submit is the primitive, others are sugars" — readers don't need to learn which method is canonical vs sugared; the args list and the name agree. |
| Zero deadline = "attempt once" (defensive default) | `time.Time{}` (zero) → one immediate attempt; `streampool.Forever` → block until success | The deadline parameter on TrySubmit answers "how long am I willing to wait if it doesn't dispatch immediately?" — a zero value naturally answers "not at all." That gives the safest default for programmers who don't have a deadline to set and pass the zero value: one attempt that fails fast, rather than a silent indefinite block. Block-forever is available via the `Forever` sentinel or via the `Submit` / `SubmitErr` named sugars. |
| No `For(duration)` family | Dropped | Two reasons: `For` reads ambiguously next to a time-typed value, and it silently picks `time.Now()` as the base time — but the base often isn't dispatch-instant in real code (request `receivedAt`, retry `firstAttemptAt`, etc.). Forcing `baseTime.Add(d)` at the call site is trivial and makes the base-time choice explicit. |
| Interface method verb | `Handle` (Handler), `Accumulate`/`Flush` (Accumulator) | Sync execution verb on the user-implemented interface, parallel to `http.Handler.ServeHTTP`. The user-facing async dispatch verbs (Submit, Start) live on the op types; the body invokes the synchronous method. |
| Single `Handler[T]` interface across Launcher + Skimmer | Both take `Handler[T]` | The two op types' bodies have identical signatures — `(ctx, T, err) error`. Defining separate `Task[T]` and `Handler[T]` interfaces with the same shape and different method names (`Run` vs `Handle`) would force users to write the same closure twice if they want the same body in both contexts. Unifying under `Handler[T]` lets adapters and struct implementations work for both ops without rewrap; the runtime context (worker vs drain goroutine) is the op type's job, not the interface's. |
| `Task` as named func adapter, not a separate interface | `type Task func(context.Context) error`, satisfies `Handler[struct{}]` | No-arg task bodies almost always close over state from the surrounding scope (they're closures by necessity), so the struct-implementation pattern that justifies exposing `Handler[T]` as an interface doesn't earn its keep for `T = struct{}`. Users who do want alloc-free no-arg work implement `Handler[struct{}]` directly with `Handle(ctx, _ struct{}, err error)` and choose their own err policy. The named `Task` adapter provides the closure shorthand; no paired `Task` interface, hence no `Func` suffix. |
| `Task.Handle` short-circuits on non-nil err | Returns `err` immediately without invoking the wrapped closure | A `Task` closure has signature `func(ctx) error` — the user explicitly opted out of receiving an err arg. Running the closure anyway when an upstream err is already in flight would either silently swallow the err (lost diagnostic) or require the user to re-handle one they declined to receive. Short-circuiting matches the convenience-adapter contract: "I didn't ask for err, so don't run me on err." Escape hatches: `ErrHandler` for run-on-err with the err visible; `HandlerFunc[struct{}]` that ignores err for always-run side effects. The behavior is asymmetric with `HandlerFunc[T]` and `ErrHandler`, both of which always invoke the closure — and that's correct, because those signatures put err in the user's hands. |
| `ErrHandler` as the no-value with-err adapter | `type ErrHandler func(ctx, error) error`, satisfies `Handler[struct{}]` | Sibling of `Task`. Named descriptively (handles an err) rather than `ErrTask` because "handle" reads naturally in both Launcher and Skimmer contexts, while "task" carries Launcher-specific vocabulary. The asymmetric pair `Task` / `ErrHandler` lives with this: each name fits its primary use case. Does *not* short-circuit on non-nil err (unlike Task) — the err-receiving signature was the whole point of choosing this adapter. |
| Single `Launcher[T]` type (no `Launcher0` / `Launcher2`) | One parameterized type | Per-arity types proliferate without earning their keep: zero-arg uses `T = struct{}` (with `Start` sugar), two-arg packs into a struct (named-field call sites). The type-parameter inference makes the single-type signature concise at use sites. |
| User inputs as interfaces | `Handler[T]`, `Accumulator[T]` | Function signatures forced closure allocations for any stateful body. Interfaces let users implement on structs with state as fields (alloc-free hot path). Function-type adapters — canonical `HandlerFunc[T]` plus named `Task` / `ErrHandler` for the void cases, and `FuncAccumulator[T]` — provide closure convenience. Same pattern as `http.Handler` / `http.HandlerFunc`. |
| No `.To(sink)` wiring | Function bodies call `sink.Submit(ctx, value)` directly | Enables multi-output ops, conditional routing, zero-output paths. Cost: wiring is no longer visible at construction; users read function bodies to trace dataflow. Worth it for the flexibility and the elimination of the output type parameter on Funnel. |
| No `Sink[T]` in public API | Not exported | Nothing in the framework's own API consumes a Sink type. User code that wants polymorphism over "things you can Submit to" defines a one-method interface locally; Go's structural typing makes that work without a published contract. |
| Funnel state | Via `AccumulatorFactory[T]` interface (`NewAccumulator` only) returning `Accumulator[T]` instances | Factory creates per-instance Accumulators (each with closure state); framework calls each instance's `Accumulate` per input and `Flush` to finalize it. There is no factory `Close`: the framework guarantees a well-defined instance lifetime (an instance is never touched after its `Flush`; every outstanding instance is flushed before the wave drains), so the owner releases any factory-level state (connections, registries) after the drain returns. Closure factories use `AccumulatorFactoryFunc[T]` (bare-func adapter) or `NewAccumulatorFactory[T](fn)`. |
| Serial accumulation ("reducer") | Funnel with `WithLimits(NewSemaphore(1))` | No separate Reducer type. The "only one instance active at a time" property is enforced by a 1-permit limiter, reusing the Limiter abstraction. Same factory, same Accumulator interface — only the concurrency cap differs. |
| Launcher.Close | Not present | The framework can't deduce what sinks a task body will Submit to, so closing the Launcher tells the framework nothing useful. Resources release via leakguard finalizer when the value falls out of scope. |
| Funnel.Close / .Dup | Removed | Funnels are wave-scoped; finalization is wave-driven (force-flush at the wave's drain), which subsumes the old refcount and counts *all* feeders. Early/snapshot finalize is `Flush(ctx)` / `FlushTo(ctx, wave)`. |
| Skimmer.Close / .Dup | Absent | Skimmer's Handler is stateless from the framework's perspective; SkimAll completion is driven by in-flight tracking, not by an explicit end-of-input signal. No Dup either — sharing across handlers needs no lifecycle ceremony. |
| Wave.Dup | Absent | The cross-producer "all done" signal it would provide is the wave's own in-flight tracking; concurrent producers are wave-internal work. The one surviving refcounted handle is `Flow`. |
| Per-op configuration | Functional options on constructor (`opts ...OpOption`) | Standard Go idiom for multiple optional parameters. Composable, extensible without breaking existing call sites. |
| Limiters | First-class entity, not a pool property | Limiters compose; pools don't. Multiple Tasks can share a Limiter; an op can have multiple Limiters; new Limiter types extend the system without core-API changes. |
| Type parameter convention | `T` | Default to T per user preference. (Earlier drafts considered T1/T2 for binary variants; with the unified Launcher[T] there are no per-arity types, and binary use cases pack into a struct.) |

---

## Mapping from psg-go

| Old (psg-go) | New (streampool) | Notes |
|---|---|---|
| `psg.NewJob(ctx)` (the post-rename `psg.New(ctx)`) | a zero-value `var w streampool.Wave` (no constructor; the worker Pool is internal) | The conflated Pool-as-bounded-context dissolves: Wave is the user-facing handle for a batch of work; the Pool is internal and auto-sized (no `NewPool`). |
| `*Job` / `*Pool` (conflated) | `*Wave` (batch, user-primary) + internal `*Pool` (workers, not user-facing) | See three-type model above. |
| `psg.NewPool` / `psg.NewTaskPool` | (removed) | Per-op concurrency limits move to Limiters; workers are managed by the Pool. |
| `psg.NewCombinerPool` | (removed) | Same — funnel workloads run in the Pool's goroutine pool, bounded by Limiters. |
| `psg.NewGatherOp(handler)` | `streampool.NewSkimmer(wave, handler)` | Wave bound at construction (or AtDispatch to defer to dispatch-ctx). Handler is `Handler[T]` (renamed from psgfn.Gather). |
| `psg.NewCombineOp(gather, pool, factory)` | `streampool.NewFunnel(wave, factory)` | Output type parameter gone; downstream sink wired via factory's closure. Wave bindable like Skimmer. |
| `gatherOp.Scatter(ctx, job, taskFn)` | `streampool.NewLauncher(wave, handler)` + `runner.Submit(ctx, arg)` | Two-step: construct once (with wave or AtDispatch), dispatch with arg. Dispatch verb is `Submit` (matches Funnel / Skimmer); `Start` is sugar for `Submit(*new(T))`. The dispatch takes no Wave; the wave was locked in at construction. |
| `gatherer.Submit(ctx, job, value, err)` | `skimmer.Submit(ctx, v)` / `skimmer.SubmitErr(ctx, err)` / `skimmer.SubmitResult(ctx, v, err)` | Job/Wave arg drops out of dispatch — was bound at construction. The three submission shapes name what's being submitted: value, err, or the full (v, err) result tuple. Callers typically branch — `if err != nil { errSink.SubmitErr(...) } else { sink.Submit(...) }` — so the value-only and err-only forms cover the dominant patterns; SubmitResult covers the rare both-case where one sink wants the pair. |
| `runner.Start(ctx)` (post-Wave-3 shape with positional pool) | `runner.Submit(ctx, arg)` / `runner.Start(ctx)` | Pool/Wave arg drops out of dispatch. Start exists as sugar for void Submit (`T = struct{}`). |
| `psg.TaskRunner0`, `psg.TaskRunner[T]`, `psg.TaskRunner2[T1, T2]` | `Launcher[T]` (single) | Per-arity types collapse: void = `T = struct{}`, two-arg packs into a struct with named fields. |
| `Pool.CloseAndGatherAll(ctx)` | `wave.SkimAll(ctx)` | Single call. Pool worker termination is automatic (refcount → 0 → synchronous worker exit before drain returns). |
| `*GatherOp[T]` | `Skimmer[T]` | Op-suffix dropped; agent noun; Skim/Skimmer rename per Op trio decision. |
| `*CombineOp[I, O]` | `Funnel[T]` | Output type parameter eliminated; Combine→Funnel rename per Op trio decision. |
| `psgfn.Task[T]` interface (`Run` method) | `Handler[T]` interface (`Handle` method) | Task interface unified with the Skimmer Handler interface — same shape, single name. Launcher now takes `Handler[T]`. |
| `psgfn.TaskFunc[T]` | `HandlerFunc[T]` (parameterized), `Task` (void, no-err), `ErrHandler` (void, with err) | Adapter set reshaped: HandlerFunc[T] is the canonical Func adapter; Task and ErrHandler are named func adapters for the no-input cases (no paired interface, hence no `Func` suffix). |
| `psgfn.Gather[T]` (function type) | `Handler[T]` (interface) | Was a function-type alias; promoted to interface for the struct-implementation alloc-free path. |
| `psgfn.FunnelFactory[I, O]` | `AccumulatorFactory[T]` interface (`NewAccumulator` only) | Output type removed; func-type alias replaced with a single-method interface. No `Close` hook — instance lifetime is contract-defined (flushed before the wave drains), so factory-level cleanup is the owner's, done after the drain. Closure factories use `AccumulatorFactoryFunc[T]` or `NewAccumulatorFactory(fn)`. |
| `psgfn.FuncAccumulator[T]` struct-literal construction | `streampool.NewAccumulator(accumulate, flush)` (or struct literal still works) | Constructor enables T inference; struct literal stays for named-field clarity. |
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

### Fluent typestate builder (`NewLauncher(fn).In(pool).To(sink)`)

**Rejected**: phantom-typed builder phases enforcing `.In` then `.To` at
compile time.

**Reason**: with `.To` gone and `pool` positional, there's nothing left
to enforce in a builder chain. The constructor signature already
captures everything.

### `Sink[T]` interface in public API

**Rejected**: exporting a `Sink[T]` interface that Funnel and Skimmer
satisfy.

**Reason**: no public API consumes it. Users who want polymorphism over
"offerable things" can define a one-method interface in their own
package; Go's structural typing makes the framework's concrete types
satisfy it automatically. Smaller public surface.

### Op trio name alternatives

**Rejected**: keeping the original `TaskRunner` / `Combiner` / `Gatherer`
naming, and various proposed substitutes.

**Reasons** (per slot):

- **Launcher** (was `TaskRunner`). `TaskRunner` was working fine but
  generic and not theme-aligned. `Caster` (fishing) was thematic but
  required a mapping click and felt fantasy-coded. `Pitcher` carried
  baseball baggage. `Sluice` is obscure. `Launcher` won on the dual
  reading: it's the verb-derived agent noun ("thing that launches
  tasks") *and* references a launch (the small motorboat) — direct
  nautical fit. Universally understood; matches the `os/exec.Cmd.Start`
  precedent for fire-and-forget dispatch.
- **Funnel** (was `Combiner`). `Mix`/`Mixer` was close — vivid and
  fluid-themed, but the "music mixer" / "concrete mixer" connotations
  competed with the API meaning. `Blend`/`Blender` was narrower
  (smoothies). `Distill` implied single-pass concentration not stream
  aggregation. `Funnel` is exact: many inputs at the top, narrower
  output at the bottom — the literal channeling-to-aggregation
  metaphor that matches what an Accumulator does. Used as a noun
  directly (no `-er` derivation needed), with `sync.WaitGroup` and
  `io.Pipe` as precedent for no-suffix op types in Go.
- **Skimmer** (was `Gatherer`). `Gather` was Design-Principle-1
  academic vocabulary (the "scatter-gather" pattern name). The
  decisive alternative was `Drain`/`Drainer` — accurate but Drainer
  carries depletion/fatigue connotation. `Reaper`, `Pumper`,
  `Collector` all had worse trade-offs. `Skimmer` won on three-layer
  theme integration: pool skimmers are literal devices that pull
  debris from a pool surface as it arrives (exact metaphor for the
  op's role); skimmer birds; and skimboarding is a form of
  wave-riding (matches `Wave.Skim` verb naming honestly).

The renames also pulled the Wave's drain methods along: `Gather` /
`GatherAll` / `TryGather` → `Skim` / `SkimAll` / `TrySkim`. The
verb-on-Wave and the Skimmer op-type name are intentionally related
because the caller's goroutine runs Skimmer handlers during the
drain — that property of the design is what makes the verb/agent
pair coupling honest.

### Verb alternatives

**Rejected**: `Run`, `Submit`, `Push`, `Send`, `Feed`, `Apply`,
`Integrate`, `Accept`, `Yield` for the dispatch verb.

**Reasons** (briefly): `Run` only fits Launcher, not the passive
receivers; `Submit` is pool-vocabulary that double-encodes "to";
`Push` is stack-coded and slightly imperative; `Send` carries channel
direction baggage; `Feed` reads cute; `Apply` is function-application
(works for handler-like Skimmer, breaks for accumulator-like
Funnel); `Integrate` is too academic; `Accept` is too passive
(from sink's perspective, the caller is the one acting); `Yield` is
generator-coded.

### Renaming "Funnel" to "Stage" / "Aggregator" / "Reducer"

**Rejected**: replacing the Combine vocabulary with stream-processing
terms.

**Reason**: `Stage` carries Apache Beam / Flink baggage with subtly
different semantics. `Reducer` implies N→1 collapse that funnels don't
strictly do. `Aggregator` is verbose. `Funnel`'s only flaw was
proximity to `Combine` (the verb), and that's resolved by adopting the
agent-noun pattern across all three op types.

### Keeping `TaskPool` and `FunnelPool` as distinct types

**Rejected**: maintaining separate user-facing pool types for task
workers vs funnel workers.

**Reason**: the historical reason was state-per-goroutine coupling in
funnels. That coupling no longer exists — state is pooled separately
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

### Launcher.Close

**Rejected**: a public Close method on Launcher.

**Reason**: closing the Launcher can't help the framework with
completion tracking, because the framework doesn't know what sinks the
task body will Submit to (the wiring lives in the closure). Without
semantic value, Close is API surface that does nothing useful.

### Public Dup on Launcher

**Rejected**: refcounted-handle Dup() / Close() on Launcher.

**Reason**: Launcher has no semantic Close, so refcounted lifecycle is
unmotivated. Sharing a Launcher across goroutines via plain Go value
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

**Reason**: Funnel carries a refcounted lifecycle (Close, Dup). The
chain `NewFunnel(...).In(wave).Submit(ctx, v)` produces an
intermediate Funnel handle that no variable holds, so there is no
way to Close it; either both handles share the same underlying state
(closing one closes the other, surprising) or `In` Dups (every chain
leaks a handle). Wave-at-construction sidesteps both: each op has one
wave, one handle, one Close. The bind-chain pattern only worked
cleanly for stateless ops (Launcher, Skimmer) — making it the
universal model would force Funnel into a shape it can't
accommodate.

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
Close()` and `CurrentWave(ctx).Skim(ctx)` from a task body are
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

### Separate `Task[T]` interface alongside `Handler[T]`

**Rejected**: defining `Task[T]` as a separate interface with `Run(ctx,
T, err) error` for Launcher bodies, parallel to `Handler[T]` with
`Handle(ctx, T, err) error` for Skimmer bodies.

**Reason**: identical signatures, different method names. A user with
a closure or struct satisfying `Handler[T]` would have to rewrap to
make it a `Task[T]` (different method name) — pure friction with no
type-system benefit. Unifying under `Handler[T]` lets the same handler
plug into Launcher or Skimmer; the runtime context (worker vs
drain goroutine) is the op type's job, not the interface's.

### `Task` as a separate interface with simplified `Run()`

**Rejected**: `type Task interface { Run(ctx) error }` as the
preferred interface for void task bodies (no `struct{}` value
parameter visible in the signature).

**Reason**: would force a separate `NewLauncherVoid` constructor (or
a call-site adapter wrapping a Task into Handler[struct{}]), breaking
the unified `NewLauncher[T any](wave, Handler[T], ...)` story. The
ergonomic win (a prettier method signature for struct
implementations of void tasks) doesn't earn its keep — no-arg task
bodies almost always close over state from the surrounding scope, so
the struct-implementation case is rare. The named `Task` func adapter
gives the ergonomic shorthand at construction sites without paying
the interface-doubling cost.

### Per-arity Launcher types (`Launcher0` / `Launcher2[T1, T2]`)

**Rejected**: separate types for zero-arg and two-arg task bodies.

**Reason**: arity is just T's type. `Launcher[struct{}]` covers the
void case (with `Start` sugar); `Launcher[fetchInput]` covers a
two-arg case with named-field clarity at call sites. Per-arity types
proliferate without earning their keep. Single `Launcher[T]` reads
identically thanks to type inference at use sites.

### `TrySubmitFor(ctx, duration, v)` duration-sugar variant

**Rejected**: a `For` family of dispatch methods taking a
`time.Duration` rather than a `time.Time`.

**Reason**: two problems compound. (1) **Naming ambiguity**: `For`
next to a time-typed value reads ambiguously — "submit for this
duration" vs. "submit for (on behalf of) this thing." `Until` is
unambiguously temporal. (2) **Silent base-time choice**, which is
the larger issue: `For(d)` would compute its deadline as `time.Now()
+ d`, but in real code the relevant base time is often something
else — a request's `receivedAt`, a task's `enqueuedAt`, a retry's
`firstAttemptAt`. Forcing the caller to write `baseTime.Add(d)`
explicitly makes them confront that choice; the `For` sugar would
hide it behind an implicit `time.Now()` assumption. Trivial savings
at the call site, real cost in obscured intent.

### `VoidHandler` / `ErrTask` / `TaskFunc0` naming

**Rejected** (in turn): `VoidHandler` for the no-input adapter,
`ErrTask` for the with-err sibling, `TaskFunc0` for the zero-arg
shape.

**Reasons**: `VoidHandler` is technically accurate but reads as
"handler that produces no output" to readers trained on void-return
languages; `Task` reads more directly. `ErrTask` reads as "task
related to errors" (ambiguous between propagating, processing,
chaining); `ErrHandler` reads cleanly as "handler that takes an
err." `TaskFunc0` keeps a `Func` suffix that paired with a now-gone
`Task0` interface — the suffix is dead weight without an interface
partner. The accepted pair is `Task` / `ErrHandler` as named func
adapters with no `Func` suffix and no paired interfaces.

---

## Open questions / things to verify

These are real and need answers before implementation locks in.

1. ~~**`Launcher` collision check.**~~ **Resolved (2026-05-24):
   doesn't matter at the API level.** Type names are scoped by package;
   `streampool.Launcher` is uniquely identified by its import path.
   Conversational/SEO collisions with other Go libraries that use
   "Launcher" are possible but low-impact — users will reach for
   "streampool's Launcher" naturally, and search discoverability
   doesn't depend on this term (the research showed "worker pool" /
   "errgroup with results" are the actual queries). If a really
   popular `Launcher` shows up later and complicates marketing copy,
   adjust the README — but the type name itself is fine.

2. ~~**Exact `Limiter` interface.**~~ **Resolved (2026-05-24).** Limiter
   is an opaque struct in v0.x; the internal contract (TryAcquire +
   Notifier or similar) is unexported and sealed against external
   implementations. Users obtain Limiter values from framework
   constructors and pass them to `WithLimits`; custom concurrency
   logic that doesn't fit the built-ins (`Semaphore`, `RateLimit`,
   future `Adaptive`) lives inside the user's task/accumulate/skim
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

4. ~~**Funnel factory invocation strategy.**~~ **Resolved (2026-05-24).**
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

   Implementation reference: `funnelop.go` (`funnelWork.Funnel`)
   and `funnelop.go` (`funnelInstance.allocate`).

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

7. ~~**Launcher2 vs higher-arity.**~~ **Resolved (revised
   2026-05-31): collapse to single `Launcher[T]`.** Earlier resolution
   shipped per-arity `Launcher0` / `Launcher[T]` / `Launcher2[T1,
   T2]`. Subsequent reshape unified the Task and Handler interfaces
   (single `Handler[T]` shared across Launcher and Skimmer), which
   eliminated the per-arity rationale: zero-arg is `T = struct{}` with
   `Start` sugar, two-arg packs into a struct with named fields, higher
   arities the same. Single-type Launcher[T] reads identically at use
   sites thanks to type inference. See "Per-arity Launcher types"
   under What we chose not to do.

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

   - **Funnel.Accumulate invocation.** Framework captures the
     Submit ctx with each work item and uses it directly for the
     Accumulate call (layered via `ensureCtxMeta` to add combine-meta).
     Submit ctx is rooted in the user's Flow ctx (if any), which is
     rooted in the Wave ctx, which is rooted in the Pool ctx — so
     cancellation cascade works through the standard `context`
     hierarchy. Submit ctx values (trace span, Flow handle, audit
     metadata) are accessible via standard `ctx.Value` /
     `trace.SpanFromContext` idioms inside Accumulate.

   - **Skimmer Handler invocation.** Handler's ctx is rooted in the
     Submit ctx so that Flow, Wave, and Pool cancellation propagate
     via the standard parent chain. The puller's ctx (from
     `wave.Skim(pullCtx)`) is an independent cancellation source
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
   - Skim-boundary ctx construction: stdlib `WithCancel` + `AfterFunc`,
     or pooled-goroutine `mergedCtx`. Implementation choice deferred
     to profiling.
   - Flow Dup/Close bracketing around work-item lifecycle.

   **Open implementation sub-choice (deferred to implementation
   phase)**: how to implement the Skim-boundary ctx. Two options:
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
    2026-05-25 to refcount-driven model).** *(Superseded surface: there is no
    user-facing `NewPool` or `CancelAndWait`, and a Wave is constructor-less and
    owns no ctx — see `surface-lineage.md`. The refcount-driven, drain-completes-
    lifecycle principle below still holds; the named entry points do not.)* Pool has
    no `Shutdown` or `Wait` methods. Lifecycle is implicit:

    - **Construction**: `NewPool(ctx, opts...)` creates a custom Pool.
      A package-level default Pool exists implicitly for users who
      don't need a custom ctx or tuning.
    - **Refcount tracking**: each referencing Wave bumps the Pool's
      refcount on construction and drops it when its drain
      (`SkimAll`, terminal `Close+Skim`, or `CancelAndWait`)
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
    can call `skimmer.Submit(ctx, value)` on an op constructed
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
- Updated hello world using `NewWave` + `NewLauncher(wave, ...)`
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
machinery (nbcq, rdvq, omnipool) is unchanged by the rename.
(leakguard has since been retired with the wave-driven funnel finalization.)
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
- Eliminate the output type parameter from Funnel.
- Add `Limiter` interface and implementations; refactor pool-internal
  concurrency limits to accept Limiters per-op.
- Merge `TaskPool` and `FunnelPool` machinery into one internal
  worker pool with Limiter-based per-op control.
- Rename and re-shape examples, tests, sub-packages.
- Replace Pool's CloseAndSkimAll with Wave's SkimAll. Pool
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
- The corresponding fields on `FunnelPoolConfigChanges` that no
  consumer reads

These were designed for an earlier adaptive-sizing implementation that
has been supplanted by a simpler one not reading these knobs. If/when
the adaptive logic gains tuning inputs, surface them then.
