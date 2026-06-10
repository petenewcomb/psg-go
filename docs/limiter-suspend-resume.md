# Limiter Suspend/Resume

This document describes the limiter *suspend/resume* protocol: how a
concurrency permit is relinquished while its holder is parked and reclaimed
when the holder resumes work. It exists to dissolve a busy-spin livelock in
which a permit held across a blocking skim starves other work that needs the
same permit.

## The problem

A [Limiter] gates how much work an op runs at once. A permit is acquired before
the work body runs and released when it completes. The hazard is what happens
*between* those two points: a body can block — most importantly, it can drive a
**subwave** synchronously, parking in that subwave's skim
(`CloseAndSkimAll`/`SkimAll`) until the subwave drains.

While the body is parked there, it is still holding its permit but doing no
work. With a `limit==1` concurrency limiter, a *sibling* unit of the same op
that needs the same single permit can never acquire it — the holder is parked,
not releasing — so the framework's block-and-help machinery spins without
progress. This reproduces as an intermittent `TestBySimulation -race` hang: a
busy-spin livelock with zero mutex/semaphore waiters. It is pre-existing and
orthogonal to any particular op's logic; making all limiters unlimited makes it
vanish.

The fix follows from a single observation: **a concurrency permit should gate
*active computation*, not *blocked-waiting*.** A parked holder isn't computing,
so it should give its permit back for the duration of the park and reclaim it
on return.

## The principle, and the two kinds of limiter

Whether a permit *should* be relinquished while the holder is parked is a
property of what the permit protects — a semantic distinction, not a mechanical
one:

- **Suspend-on-block** (a concurrency [Semaphore]): the permit gates active
  computation. A parked holder isn't computing, so the slot is given back
  during the park and reclaimed on return. This is the kind that dissolves the
  livelock.
- **Hold-through** (a hypothetical memory/resource limiter): the resource stays
  occupied while the holder is parked — suspending it would let other work
  overcommit the real resource. For this kind, suspend/resume are no-ops; the
  permit is held continuously from acquire to release.

The same mechanism (a semaphore) could back either; the kind is intent. The
protocol below is uniform across both — the framework always drives the same
handle the same way, and the limiter decides whether suspend/resume do anything.

## The protocol: a request handle

A limiter is a sealed, internal interface. The shape that survived a long design
search is **not** a flat bag of primitives the framework sequences with threaded
tokens; it is a **request handle** the limiter hands back, which owns the
admission's whole lifecycle and state. The framework holds one opaque handle,
drives it, and threads nothing else.

```go
type applicant interface {
    Processor() any // op's Handler or Accumulator interface, type-assertable to a sizing interface
    Value() any
    Err() error
}

type limiterImpl interface {
    newRequest(a applicant) request // allocate the handle (state PENDING); poolable
}

type request interface { // limiter-owned; one small state machine PENDING -> HELD <-> SUSPENDED -> DONE
    tryAcquire() bool          // PENDING   -> HELD       (false: still PENDING)
    suspend() bool             // HELD      -> SUSPENDED   (true if it transitioned; false if already SUSPENDED)
    tryResume() bool           // SUSPENDED -> HELD        (false: still SUSPENDED)
    release()                  // any state -> DONE        (give back / abandon / discard, by state; idempotent)
    notifier() *workq.Notifier // wait/notify target for the current phase (acquire vs reclaim); limiter-chosen
}
```

`limiterImpl` is implemented by a **scheduler** — a *direct* scheduler over one
resource, or one coordinating a group of resources bound to it (see "Multiple
limiters"). The framework only ever drives the resulting handle.

The handle being a state machine is what makes everything else fall out:

- **Stable identity.** The handle is allocated once at `newRequest`, so it is the
  identity a sized/prioritized limiter needs to track a pending request across
  retries and to recognize its give-up. The `applicant` can't serve that role —
  it's a per-call value (a stack wrapper over the work's accessors), with no
  identity across calls.
- **No token threading, no framework-side state machine.** `PENDING/HELD/
  SUSPENDED` lives in the handle, not spread across the integration points as
  `permitToken`/`suspendToken` juggling.
- **Cleanup is one idempotent call.** `release()` does the right thing by state —
  give the slot back (HELD), abandon a pending request (PENDING), discard a
  suspended one (SUSPENDED). So every integration point is just
  `defer req.release()` and *can't* mis-sequence or forget the give-up/teardown
  signal, and there is no abandon-before-recycle ordering puzzle (the handle owns
  its own recycle). The limiter still distinguishes the three cases internally
  (it switches on its own state); they're simply not three methods the framework
  must choose correctly among.
- **The limiter owns notification routing.** `notifier()` returns the wait/notify
  target for the handle's *current phase*, so the limiter — not the framework —
  decides whether acquirers and reclaimers share a notifier or get separate
  ones, and in what order they wake. Reclaim-prioritization and sizing become
  internal limiter concerns; there are no separate acquire/resume notifier
  methods on the contract.
- **`applicant` accessors box lazily.** The `applicant` is the already-allocated
  work item behind an interface; `Value()`/`Processor()` box `T->any` only when a
  limiter actually reads them, so the count-based semaphore allocates nothing.
  It's in the contract from the start so the gating layer must surface the work —
  see "Blocking," below — rather than leaving a one-way door.

### Resources: the open layer

The scheduler owns the protocol above; what it gates is one or more **resources**
— the open extension point. A resource is pure accounting over a capacity, with
no handle, lifecycle, or notification routing:

```go
type resource interface {
    demand(a applicant) amount          // sizing: how much this applicant needs (1 for a semaphore; bytes for memory)
    tryAcquire(amount) bool             // deduct if it fits
    release(amount)                     // restore (a completion give-back or a suspend's) — pure accounting, no notify
    suspendable() bool                  // relinquished while the holder is parked? (concurrency: yes; memory: no)
    capacityIncreased() <-chan struct{} // signaled on out-of-band capacity *growth* (e.g. SetMaxConcurrency); nil if fixed-size
}
```

The capacity signal is a plain `<-chan struct{}` — not the internal `Notifier` —
because resources are user-implementable and this is the one place a resource
must hand the framework a wake source. A bare (coalescing/buffered-1) channel is
trivial to expose and pings only on out-of-band *growth*; it is consulted only in
the blocking path (to wake a parked waiter when a resize raises the ceiling), is
`nil` for a fixed-size resource, and never touches the hot acquire/release path.
The scheduler's *own* waiter set keeps using the internal `Notifier` (release-
wakeup, routing) where the richer semantics earn their keep; `Notifier` is not
exposed. A *direct* scheduler selects on one such channel; only the future
*prioritized* scheduler must wake on any of N, which it handles internally with a
small forwarder goroutine per growable resource (rare; cold path) — invisible to
the resource author.

The scheduler maps the handle lifecycle onto its resources: `tryAcquire` takes
each demand (per its discipline), `suspend` releases the suspendable ones,
`tryResume` re-takes them, `release` restores whatever's held by state. The handle
carries the per-request demand amounts; resources are just shared, thread-safe
counters.

This is where the sealing **inverts**: schedulers are the *closed* set
(framework-provided — direct, ordered, prioritized — carrying all the
concurrency-protocol subtlety), while **resources are open for extension** — a
semaphore is ~10 lines of accounting, and memory/rate/weighted/user-defined
resources are just as small. The two limiter "kinds" collapse to `suspendable()`;
sizing collapses to `demand`.

Two consequences worth stating:

- **No cross-resource transactions.** Nothing ever needs to deduct several
  resources atomically as a primitive. The ordered scheduler holds incrementally
  in canonical order (deadlock-free, no atomicity); the prioritized scheduler
  grants under the evaluate-lock it already needs for the priority decision, so
  the multi-resource deduction is atomic there for free; the direct scheduler is
  a single resource. Resources are strictly single-resource.
- **`release` does not notify.** Availability-wakeup is the scheduler's job — it
  called `release`, owns the waiter set, and knows the routing. The resource's
  `capacityIncreased()` channel is only for *out-of-band* growth the scheduler
  can't observe through its own acquire/release flow (a `SetMaxConcurrency`
  raising the ceiling), and is `nil` for a fixed-size resource.

The minimal case is a **direct scheduler over one semaphore resource**: the
resource is a count with `tryAcquire`/`release` over `IncrementIfUnder`/
`Decrement` and `suspendable() == true`; the direct scheduler supplies the handle,
its single waiter notifier, and the trivial discipline (`tryAcquire`/`tryResume`
both take the one slot).

## Blocking: routing plus one shared helper

The framework never blocks *inside* the limiter, and the limiter never owns the
block/postpone decision (that's an execution-context property, not the
limiter's). Instead `ExecuteOrWait` shrinks to **routing**, and the one genuinely
blocking case runs a shared helper:

```go
// routing (this is what ExecuteOrWait becomes)
switch /* from ex.ShouldBlockOrPostpone() + ShouldBlock(ctx) */ {
case oneShot:  if !req.tryAcquire() { return notAcquired }                     // defer release() abandons
case postpone: if !req.tryAcquire() { ex.AddToListeners(&req.notifier().Listeners); return } // re-invoked later
case block:    if err := blockingAcquire(ctx, req, helpSelectFn); err != nil { return err }
}
return workFn(...)

// shared helper: loop + automatic abandonment
func blockingAcquire(ctx, req, helpSelectFn) error {
    for !req.tryAcquire() {
        if err := helpSelectFn(req.notifier()); err != nil { return err } // give-up; defer release() abandons
    }
    return nil
}
```

- `helpSelectFn` is the existing block-and-help select (the `skimSelect`
  composition in `job.go`), parameterized by the request's notifier — so the
  holder drains the skim queue while waiting on the limiter's wake channel, ctx,
  and deadline, exactly as today.
- **Abandonment on the block path is automatic.** The give-up returns an error
  and the surrounding `defer req.release()` cleans up by state (PENDING ->
  abandon). Nothing to plumb separately.
- **Postpone and one-shot stay framework-driven** (`tryAcquire` + listener/
  return). A limiter-owned blocking loop *can't* express "postpone and be
  re-invoked," and the funnel/task contexts use exactly that, so those paths are
  not — and need not be — inverted.

Reclaim is the same shape with `tryResume`:
`for !req.tryResume() { helpSelectFn(req.notifier()) }`.

## Where suspend fires

The rule is uniform: **the framework suspends a held permit whenever it parks the
holder, and reclaims it on return** — rather than enumerating which parking
points are "the dangerous ones." Earlier analysis (and prior notes) repeatedly
mis-identified the minimal sufficient set; a uniform rule removes that fragility
and matches PSG's "make the unintended impossible" stance.

Concretely the framework brackets each blocking *episode* — the public skim
methods (`Skim`/`SkimAll`, hence `CloseAndSkimAll`), the block-and-help submit
point (`Pool.block`), and the scheduled-flush wait:

```go
if r := meta.currentHeldRequest(); r != nil && r.suspend() {
    defer reclaim(ctx, r) // for !r.tryResume() { helpSelectFn(r.notifier()) }
}
```

`suspend()` returns false on an already-SUSPENDED handle, so nested same-episode
skims are no-ops and the slot is freed once per episode and reclaimed once on
return. A reclaim canceled mid-flight leaves the handle SUSPENDED, so the body's
`defer req.release()` discards it correctly (no double give-back).

This covers only **framework-mediated** blocking. The framework cannot intercept
user code that blocks on its own channel, mutex, or syscall inside a body; that
remains the user's concern. The livelock being fixed is entirely
framework-mediated (subwave skims), so this is sufficient.

## Single-goroutine scoping

Each request handle's `tryAcquire`/`suspend`/`tryResume`/`release` happens on one
goroutine — the one running the body that holds it — so the handle needs no
internal synchronization. Two facts make this hold:

1. The handle is stamped on the body's per-worker `ctxMeta` at body entry (like
   the dispatching `wave` is stamped). A worker runs one body at a time, so it's
   goroutine-local. (For the task path the handle is created at dispatch and
   travels across the queue hand-off to the body as one opaque value — still no
   token threading.)

2. A skim running synchronously on the holder's goroutine must reach that handle
   even though the subwave runs on a *different* `Pool` with its own `ctxMeta`.
   `ctxMeta` gains a `parent *ctxMeta` link along synchronous, same-goroutine
   derivations (top-level->skim, body->`NewWave`->subwave-skim), so a skim walks
   `parent` up to the body's handle. **A subwave's worker contexts are fresh
   permit-roots (`parent == nil`)** — even though they still record the parent
   *wave* in `parentJobs`. That keeps the walk inside the calling goroutine: a
   worker only ever finds its *own* handle, never the spawning parent's. ("Other
   goroutines acquire their own permits anyway.")

So `currentHeldRequest` walks `parent` and finds at most one handle (one body per
goroutine).

## Measuring concurrency under suspension

Suspend-on-block changes what a concurrency limiter *guarantees*: from "at most N
bodies in flight" to "at most N bodies **actively computing**." A parked body
that has relinquished its slot doesn't count against the limit, so more than N
bodies can be mid-flight as long as at most N hold slots.

The property-based simulation asserts a per-limiter concurrency bound, so it must
measure *active* concurrency, not in-flight bodies: a body's contribution is
dropped while it drives a subwave (the span over which the framework suspends its
permit) and restored on return. Measuring in-flight bodies would spuriously
observe `> N` precisely in the scenario the fix enables. (A hold-through limiter,
when one exists, keeps the stricter in-flight bound — its permit is never
suspended.)

## Multiple limiters: the scheduler

From the framework's standpoint the only shape that matters is the thing that
implements `limiterImpl` — the **scheduler**. A bare limiter is its own trivial
scheduler (one dimension, direct acquire); a non-nil scheduler coordinates a
group of limiters as one. Either way the framework drives a single handle and
never learns how many limiters are behind it. (`WithLimits` is allowed by the API
today but unimplemented — `singleLimiter()` picks one; the scheduler is how it
becomes real.) The name names the *role* — deciding what is admitted when — which
is what both shapes below share; ordered acquisition is just its trivial
discipline.

**Binding is explicit at construction.** Each limiter takes its scheduler as the
*first* constructor argument — `NewSemaphore(scheduler, n)` — and the user passes
`nil` to mean self-scheduled (standalone). Putting the scheduler first puts its
fundamental role in front of the user at the moment a limiter is created, rather
than as an afterthought. A limiter is thus bound to at most one scheduler, fixed
at construction.

**`WithLimits` requires one scheduler.** All limiters passed to `WithLimits` must
resolve to the **same** scheduler — where a `nil`-scheduler limiter resolves to
its own self-scheduler, so a lone bare limiter is fine but two distinct bare
limiters (two distinct self-schedulers) are rejected. Validated at **op
construction** (fail-fast), not at dispatch.

**Why this is safe.** Joint acquisition only ever happens through one scheduler,
so every jointly-acquired set has exactly one coordinator owning their joint
capacity/order — exactly what deadlock-free all-or-nothing needs. The rule
rejects the unsafe shapes (limiters with different schedulers, or two with none)
because they'd have no common coordinator. And schedulers are **independent
domains**: no op can span two, so no op holds permits from two schedulers at
once, so there's no cross-scheduler deadlock. The case that looks like spanning —
an op holding scheduler-A permits whose body dispatches a nested op on
scheduler-B — isn't joint acquisition: the nested op is a separate request through
B on its own goroutine, and if the parent parks waiting on it the suspend
protocol has already released the parent's A permits. Cross-domain nesting is
handled by *suspend*, not by the scheduler.

**The scheduler's discipline is pluggable** — the payoff of it being the
integration boundary, and potentially a user choice per domain. Two are in view
(both deferred):

- *Ordered* — a lightweight scheduler: acquire members in a canonical order
  (registration order), holding the prefix and blocking on the next; release all
  on give-up. Deadlock-free by ordering; the prefix-hold across the wait is a
  *bounded* occupancy, not the recursive-skim livelock. Blocked on exactly one
  member at a time, so its `notifier()` is just that member's — no fan-in.
  Decentralized, no central serialization; per-resource/FIFO fairness only.
- *Prioritized* — a global capacity vector plus a queue of aggregate demand
  vectors, granting the highest-priority request whose whole vector currently
  fits, atomically (no partial holds → no hold-and-wait, and better utilization
  than ordered), withholding to avoid starving a large request. The home for
  cross-op prioritization (largest-first, reclaim-first, fairness), at the cost of
  a central serialization point — a reach-for-it-when-needed discipline.

A scheduler's members are **resources** (see "Resources: the open layer").
**suspend/reclaim/release fan out per resource kind**: in a skim the scheduler
releases each `suspendable()` resource, re-takes them on reclaim, and `release`
cleans up all by state — a semaphore relinquishes, a memory resource (not
suspendable) stays held. A richer resource just reports a larger `demand`; no
cross-resource commit protocol is involved (atomicity, where needed, comes from
the prioritized scheduler's evaluate-lock). All of this lives between the
scheduler and its resources; the framework-facing handle is unchanged.

## Scope: shipped now vs designed-for-later

**Shipped.** The suspend-on-block [Semaphore] behind the handle, constructed
`NewSemaphore(nil, n)` — the scheduler argument lands now (the role made visible
at construction) but only `nil` (self-scheduled) is supported; `ExecuteOrWait`
split into routing + the shared `blockingAcquire`; uniform episode-suspend at
framework parking points; the single-goroutine `ctxMeta` scoping; the sim's
active-concurrency measurement. No non-nil scheduler, no prioritization — the
semaphore's request is trivial and the sibling-contention livelock's liveness
already holds through the existing renotify chain.

**Deferred (reachable without changing the framework-facing handle):**

- **More resource kinds** (the open layer): a hold-through memory resource
  (`suspendable() == false`); a rate resource (admission paid once, `release` a
  no-op); weighted/cost resources. Each is a small accounting object reporting its
  `demand`; no protocol change.
- **Non-trivial schedulers** (the closed set): an `OrderedScheduler` (joint
  multi-resource admission, deadlock-free by canonical order — a fixed mechanism,
  no user knob) and a `PriorityScheduler` (global capacity vector +
  aggregate-demand queue with atomic-fit grants). The `PriorityScheduler` is the
  home for everything cross-request, and its policy is itself **pluggable** —
  `NewPriorityScheduler(prioritizer)`, where the user-supplied prioritizer ranks
  pending requests by their demands, ages, and `applicant` data. So largest-first
  / fairness / aging, reclaim-before-acquire (route reclaimers and new acquirers
  to different waiter sets, wake reclaimers first), and **starvation-avoidance by
  withholding** (declining freed capacity to smaller newcomers while accumulating
  for a larger pending request) are *configurations of one scheduler*, not
  separate types. (That last is the only real meaning of "reservation": not a
  distinct capacity state — a withheld unit is unavailable to others exactly as an
  acquired one is — but a scheduler policy, made possible by the pending set the
  handle's stable identity affords.) Both sit behind the same handle with no change
  at the integration points.

## Rejected alternatives

- **Fresh limiter per subwave "fixes" it.** Hides the livelock in the sim by
  removing same-limiter contention across the subwave boundary, but the bug is
  *within* a wave (siblings of one op), so this neither explains nor fixes it.
- **Demote subwave submits so they postpone instead of block-and-help.** Wrong
  lever — the cause is the *held permit across a wait*, not the top-level label;
  and it tangles with the dispatch contract (a nil `shouldBlock` panics the
  `ExecuteNowOrQueue` stub).
- **Counted suspend with an all-at-once resume barrier.** Considered when it
  seemed multiple subwave goroutines might suspend one parent permit
  concurrently. They don't: a subwave goroutine acquires its *own* permit rather
  than suspending the parent's, so suspension stays single-goroutine.
- **Flat primitives with threaded tokens** (`tryAcquire`/`suspend`/`release`/
  `abandon`/`discard` returning and consuming `permitToken`/`suspendToken`). Two
  fatal frictions: no stable identity for abandonment (the `applicant` is a
  per-call value), and a `PENDING/HELD/SUSPENDED` state machine replicated at each
  integration point. The handle dissolves both.
- **Full selectFn inversion** (the limiter owns *all* blocking, driving a
  framework selectFn). Can't express the postpone path — funnel/task contexts
  register-and-return rather than block — so it would be additive, not a
  replacement, and would split abandonment (automatic on block, still plumbed for
  postpone). The handle keeps blocking framework-driven and gets automatic
  block-path abandonment anyway via `defer release()`.
- **Two-phase reserve/commit/cancel surface for multi-limiter.** "Reservation"
  isn't a distinct capacity state (a reserved unit is unavailable to others just
  as an acquired one is), so multi-limiter all-or-nothing is just *ordered
  acquire*; the only genuine "reservation" is a single limiter's internal
  accumulation policy (above), which needs no cross-limiter commit protocol.
