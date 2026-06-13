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
  permit is held continuously from acquire to release. (Exception: a POSTPONED
  request — granted but never started — returns even a hold-through
  reservation; nothing has materialized. See "The POSTPONED state.")

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

type request interface { // limiter-owned; one small state machine (illegal transitions PANIC)
    tryAcquire() bool             // PENDING -> HELD (acquire) | POSTPONED -> HELD (re-grant); false: unchanged
    postpone()                 // HELD -> POSTPONED         (grant yielded: gated work couldn't start)
    suspend() bool             // HELD -> SUSPENDED (true)  | SUSPENDED: no-op, false (re-entrant brackets)
    tryResume() bool           // SUSPENDED -> HELD         (reclaim; false: still SUSPENDED)
    release()                  // any state -> DONE         (give back / abandon / discard, by state; idempotent)
    notifier() *workq.Notifier // wait/notify target for the current phase; limiter-chosen; never nil
}
```

The state machine:

```
PENDING ──tryAcquire──► HELD
HELD    ──suspend──► SUSPENDED ──tryResume──► HELD     (mid-body park)
HELD    ──postpone─► POSTPONED ──tryAcquire────► HELD     (pre-body yield)
any     ──release──► DONE                              (idempotent)
```

**Illegal transitions panic — framework bugs fail loud.** The one deliberate
silent case is `suspend()` on an already-SUSPENDED handle (returns false):
that is the re-entrancy mechanism for nested brackets, by design. `release()`
on DONE is a no-op by design too — a safety property of the by-state cleanup,
not bug-masking. Everything else off the table above (e.g. `postpone()` from
anything but HELD, `tryAcquire()` from HELD — a retry can legally find only
PENDING or POSTPONED, because a prior invocation either started the work, ending
in DONE + freed, or postponed it) panics at the mis-wired integration point.

**Notification discipline:** every transition that *returns capacity* triggers
the scheduler's availability-wakeup — `suspend()`, `postpone()`, and
`release()` from HELD. State-discarding transitions (`release()` from
SUSPENDED/POSTPONED — the give-back already happened) notify nothing and credit
nothing: double-notify is not a correctness bug (spurious wakeups re-check and
re-park) but it is renotify-storm fuel and a "no inflation" conservation soft
spot.

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
  SUSPENDED/POSTPONED` lives in the handle, not spread across the integration
  points as `permitToken`/`suspendToken` juggling. `tryAcquire()` is the one
  state-aware entry the routing needs — acquire or re-grant by state — so the
  framework never inspects state itself.
- **Cleanup is one idempotent call.** `release()` does the right thing by state —
  give the slot back (HELD), abandon a pending request (PENDING), discard a
  suspended or postponed one (SUSPENDED/POSTPONED — the give-back already
  happened at suspend/postpone time, so release credits and notifies nothing).
  So every integration point is one release call at its cleanup point (a defer,
  or the work's completion callback) and *can't* mis-sequence or forget the
  give-up/teardown signal, and there is no abandon-before-recycle ordering
  puzzle (the handle owns its own recycle). The limiter still distinguishes the
  cases internally (it switches on its own state); they're simply not separate
  methods the framework must choose correctly among.
- **The limiter owns notification routing.** `notifier()` returns the wait/notify
  target for the handle's *current phase* — acquire (PENDING), re-grant
  (POSTPONED), reclaim (SUSPENDED) — so the limiter, not the framework, decides
  whether those three waiter populations share a notifier or get separate ones,
  and in what order they wake. The direct scheduler returns one notifier for
  all three; a prioritized scheduler can wake reclaimers ahead of re-grants
  ahead of fresh acquires. Reclaim-prioritization and sizing become internal
  limiter concerns; there are no separate per-phase notifier methods on the
  contract, and `notifier()` is never nil (the unlimited semaphore returns its
  real notifier; callers stay branch-free).
- **`applicant` accessors box lazily.** The `applicant` is the already-allocated
  work item behind an interface; `Value()`/`Processor()` box `T->any` only when a
  limiter actually reads them, so the count-based semaphore allocates nothing.
  It's in the contract from the start so the gating layer must surface the work —
  see "Blocking," below — rather than leaving a one-way door.

### The POSTPONED state: granted, then yielded

A request can be granted before its body can start: on the postpone dispatch
path (skim/funnel contexts), the limiter gate acquires and only then discovers
the gated work cannot proceed — e.g. the downstream task queue is full — so
the work registers a listener and returns not-started, to be re-invoked later.
The grant must not sit idle on work parked in the postponed queue (that is
"held while not computing," the exact thing this design eliminates), and the
pre-handle code already released-and-reacquired here. POSTPONED preserves that
behavior *and* the request's stable identity across the retry:

- `postpone()` returns the held capacity; the retry's `tryAcquire()` re-grants
  without re-paying — see the per-resource rule below.
- **Per-resource divergence (why this is a distinct state, not SUSPENDED with
  a flag):** concurrency — both states give the slot back; hold-through memory
  — SUSPENDED *keeps* the reservation (the parked body has live allocations)
  but POSTPONED *releases* it (nothing materialized; the body never started);
  rate — never re-paid in either, mechanically: the scheduler re-takes exactly
  the dimensions it released for that state, and rate held nothing
  post-admission, so nothing is re-taken. No special case.
- **The pre-body/mid-body line:** POSTPONED yields a *pre-body* grant; the
  hold-through rule (see "Where suspend fires") keeps a *mid-body* grant
  across capacity stalls. The line in both is "has the body started."
- **Terminology caveat:** workq "postponed" is wider — a work item postpones
  whenever it can't start, including while its request is still PENDING
  (acquire failed, listener registered). *Work postponed* does NOT imply
  *request POSTPONED*; the state means specifically "granted, then yielded the
  grant because the gated work couldn't proceed."

### Resources: the open layer

The scheduler owns the protocol above; what it gates is one or more **resources**
— the open extension point. A resource is pure accounting over a capacity, with
no handle, lifecycle, or notification routing:

```go
type resource interface {
    demand(a applicant) amount               // sizing: how much this applicant needs (1 for a semaphore; bytes for memory)
    tryAcquire(amount) bool                  // deduct if it fits
    release(amount)                          // restore (a completion, suspend, or postpone give-back) — pure accounting, no notify
    suspendable() bool                       // relinquished while the holder is parked mid-body? (concurrency: yes; memory: no)
    setCapacityChangedFn(fn func(delta int)) // bind-time hook for out-of-band capacity *growth*; nil/ignored for fixed-size
}
```

The capacity signal is a **bind-time callback** — not a channel, and not the
internal `Notifier`. The scheduler installs the hook when the resource is
bound; the resource author's whole obligation is one line: after raising
capacity out-of-band (e.g. `SetMaxConcurrency`), call the hook, if non-nil,
with the delta. The hook is the scheduler's availability-wakeup entry — wake
per freed slot, wake-all on unlimited — routed to the scheduler's *full*
waiter set: parked (blocking) waiters AND postponed/registered listeners. It
must be safe to call from any goroutine (the framework's implementation is a
notifier poke); resource authors should avoid invoking it while holding their
own locks. Fixed-size resources never call it; the hot acquire/release path
never touches it. The scheduler's own waiter set keeps using the internal
`Notifier` (release-wakeup, phase routing) where the richer semantics earn
their keep; `Notifier` is not exposed.

(A `capacityIncreased() <-chan struct{}` channel was rejected: a ping with no
consumer parked sits unconsumed — postponed listeners would starve on a `0→n`
resize that happens while nobody is blocked; a unary ping cannot carry the
delta, which a `0→n` resize needs to wake n waiters; and multi-resource
schedulers would need forwarder goroutines just to consume it.)

The scheduler maps the handle lifecycle onto its resources: `tryAcquire` takes
each demand (per its discipline), `suspend` releases the suspendable ones,
`postpone` releases *everything currently held* (pre-body, nothing has
materialized — even a hold-through reservation returns; a rate resource holds
nothing post-admission, so it is naturally exempt), `tryResume`/re-grant
re-take exactly the dimensions released for that state (which is why rate is
never re-paid — no special case), and `release` restores whatever's held by
state. The handle carries the per-request demand amounts and the per-resource
held/released bookkeeping; resources are just shared, thread-safe counters.

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
- **`release` does not notify — the scheduler does, on exactly the
  capacity-returning transitions.** Availability-wakeup is the scheduler's job —
  it owns the waiter set and the routing. The rule (see "Notification
  discipline" above): `suspend`/`postpone`/HELD-`release` notify;
  SUSPENDED/POSTPONED-`release` (state-discarding) do not. The resource's
  capacity hook is only for *out-of-band* growth the scheduler can't observe
  through its own acquire/release flow (a `SetMaxConcurrency` raising the
  ceiling); fixed-size resources never call it.

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
// routing (this is what ExecuteOrWait becomes). tryAcquire is state-aware:
// a first invocation finds PENDING (acquire); a retry finds POSTPONED
// (re-grant). HELD at entry is impossible (a prior invocation either started
// the work — DONE + freed — or postponed it) and panics.
switch /* from ex.ShouldBlockOrPostpone() + ShouldBlock(ctx) */ {
case oneShot:    if !req.tryAcquire() { return notAcquired }                     // defer release() abandons
case postponing: if !req.tryAcquire() { ex.AddToListeners(&req.notifier().Listeners); return } // re-invoked later
case block:      if err := blockingAcquire(ctx, req, helpSelectFn); err != nil { return err }
}
err := workFn(...)
if !ex.Started() { req.postpone() } // grant yielded while the work waits elsewhere; see "The POSTPONED state"
return err

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

### Reclaim is help-shaped

Reclaim is the same loop with `tryResume`:
`for !req.tryResume() { helpSelectFn(req.notifier()) }` — and the help is
**load-bearing, not an optimization**. The principle: *any wait on a goroutine
that currently has a driving duty must help its driven domain* — including
reclaim waits. (The same deep rule that makes top-level acquire-blocking
help-shaped.) The help domain is **the pool whose skim context the goroutine
currently occupies** — during a subwave drive, the subjob — not the handle's
owning pool (a body goroutine has no `Receiver` for its parent pool and cannot
help it); the wake source is the request's notifier. This cross-pool
composition (help one pool, wake on another's limiter) is deliberate.

Plain-wait reclaim deadlocks. Witness: a parent body holds a shared `limit=1`
Limiter L (shared with an op inside the subwave it drives); a mid-drive `Skim`
suspends L; subjob body **B** acquires L and runs; the parent's `Skim` returns
and its reclaim plain-waits for L; B blocks posting its result into the
subjob's full skim queue — a hold-through capacity wait (see "Where suspend
fires"), so B keeps L; the only consumer of that skim queue is the parent
goroutine. Cycle: reclaim(L) ← B completes ← B's post drains ← parent skims ←
reclaim(L). The deadlock arises from the *interaction* of hold-through with
plain-wait reclaim — neither alone is wrong. Help-shaped reclaim drains B's
post, B completes, L frees.

`CloseAndSkimAll`'s reclaim is the same composition, vacuously plain: the
subjob is drained, so the help domain is empty. No special case.

## Where suspend fires: the two-class rule

The rule is principled, not a site list: **classify every framework park by
the kind of wait.** (Earlier analysis repeatedly mis-identified a "minimal
sufficient set" of dangerous sites; a structural criterion removes that
fragility. A blanket suspend-everywhere rule was also rejected — see
"Rejected alternatives" — because at capacity waits it is *anti*-backpressure.)

- **Suspend class — parks that wait on, or synchronously run, other
  framework-gated work.** The gathers (public skim methods: `Skim`/`SkimAll`,
  hence `CloseAndSkimAll`) and the block-and-help waits (today `Pool.block`;
  named here by episode class because the destination architecture migrates
  drain machinery from Pool to Wave). Resolution of these waits can depend on
  permit availability, so holding across them risks circular waits —
  suspension is **correctness-required**. Two witnesses:
  - the original livelock: a permit held across a subwave skim starves a
    sibling unit needing the same permit;
  - shared-limiter self-deadlock at the block-and-help wait: a parent op and
    an op inside the subwave it drives share a `limit=1` Limiter; the
    subjob-top-level dispatch enters `blockingAcquire` on a permit held by the
    *same goroutine's* parent body — an infinite block-and-help spin unless
    the bracket suspends the parent's handle. (This upgrades bracketing the
    block-and-help wait from "airtight bonus" to required.)
- **Hold-through class — pure capacity waits.** The task-context blocking
  posts (`taskPostWork`'s blocking push; the funnel submit's blocking branch):
  a mid-body Submit parked on a full downstream queue **keeps its permit**.
  Safe, by the invariant **"a permit wait never occupies bounded queue
  capacity"** — a consumer that can't acquire its permit *postpones*, vacating
  its queue slot, so queue drain never depends on permits (this holds even
  under adversarial limiter sharing between producer and consumer ops). And
  *desirable*: the held permit is the limiter's backpressure-propagation
  mechanism — suspending at capacity waits would admit sibling after sibling
  into the same full pipe, piling up unboundedly many half-done parked bodies.

(The scheduled-flush wait is a park with no holder — a worker between bodies —
so it belongs to neither class; a bracket there finds no handle.)

**Every future parking point must be classified into one of the two classes
when introduced; the classification, not a site list, is the contract.** Two
structural commitments keep the classification sound:

- **Skimmers (drain-side ops) never take `WithLimits`.** Not a missing
  feature — load-bearing: the hold-through class is safe because queue drain
  is permit-free, and skim handlers *are* the drain. Permit-gating them would
  make capacity waits permit-dependent, recreating the cycle class this design
  dissolves. Users who need to throttle expensive skim-handler work do it
  inside the handler with their own primitives.
- POSTPONED yields *pre-body* grants; hold-through keeps *mid-body* grants
  across capacity stalls (see "The POSTPONED state").

The suspend-class bracket:

```go
if r := meta.currentHeldRequest(); r != nil && r.suspend() {
    defer reclaim(ctx, r) // for !r.tryResume() { helpSelectFn(r.notifier()) } — help-shaped; see above
}
```

`suspend()` returns false on an already-SUSPENDED handle, so nested
same-episode brackets are no-ops: the slot is freed once per episode and
reclaimed once on return. In help-execution nesting, an inner bracket that
finds the *enclosing* body's already-SUSPENDED handle correctly installs no
reclaim — the reclaim belongs to the episode that suspended it. A reclaim
canceled mid-flight leaves the handle SUSPENDED, so the body's completion
`release` discards it correctly (no double give-back).

This covers only **framework-mediated** blocking. The framework cannot
intercept user code that blocks on its own channel, mutex, or syscall inside a
body; that remains the user's concern. The livelock being fixed is entirely
framework-mediated, so this is sufficient.

## Intake vs drain: limiter placement and the skim-gather prohibition

The two-class rule above keeps a *held* permit from deadlocking at a park. A
second, structural rule keeps the *drain* itself always able to make
progress — without which the hold-through class's "queue drain never depends on
permits" claim fails under limiter sharing.

**Limiters gate intake, not drain.** Map each op onto the intake/drain split:

- *Intake* — launcher tasks and funnel **accumulates**. Admitting work is
  exactly what a limiter bounds; these carry `WithLimits`.
- *Drain* — skim handlers and funnel **flushes**. These move work *out* (a skim
  consumes; a flush emits the aggregate downstream — often just a Submit to a
  skimmer). They are **limiter-free**, generalizing "skimmers never take
  `WithLimits`": gating the drain is the deadlock that commitment prevents.

So for a funnel, `WithLimits` means **accumulate (intake) concurrency**, and:

- **Flush is not gated, and must not be.** It runs in funnel context (it
  postpones, never blocking a worker) and never holds an accumulate permit —
  required for the flush/accumulate pipeline: at `limit==1`, the instant an
  instance flushes, demand must create a new instance and accumulate into it
  *concurrently with* the flush; a flush holding the permit would stall every
  flush boundary.
- **No `WithFlushLimits`.** Bounding flush-*triggered* work is done by flushing
  to a downstream *limited* launcher/funnel — the limit lives on the next
  intake hop. (As with a heavy skim handler, in-flush work a user wants bounded
  is bounded inside the body with their own primitive.)
- **No separate instance-count limiter.** Accumulator instances are created on
  demand by concurrency and pooled/reused (not per-value or per-key), so
  instance count — and thus partial-aggregate memory — is bounded by accumulate
  concurrency. Whatever caps concurrency (an explicit limiter, or the
  governor's downstream-blockage spawn-brake — see
  `backpressure-and-reentrancy.md`) caps instances for free; an unlimited
  funnel grows instances only as fast as the skimmer drains. (Keyed aggregation
  *would* decouple instance count from concurrency and could warrant its own
  resource — future, via the scheduler.)

**Skim handlers must not drive a blocking gather.** A skimmer's drain has a
single serial driver (its `Skim`/`SkimAll`/`CloseAndSkimAll` caller, plus
transient block-and-help helpers). If a skim handler itself drives a subwave
(`CloseAndSkimAll` from inside the handler), it monopolizes that sole driver
while parked in the sub-gather — and under limiter sharing / nested subjobs
that closes a driver-scarcity cycle: an outer wave can't drain to free a shared
permit the inner gather needs, because the one goroutine that would drain it is
stuck in the inner gather. So a blocking gather from skim context is
**disallowed** — `ctxMeta.vetNotNestedInSkim` walks the parent chain at gather
entry and panics if an enclosing context is a skim context (funnel/task/
top-level enclosing contexts are fine; the non-blocking `Try*` gathers are not
restricted). Subwork from a skim handler goes to a demand-driven consumer,
which has no sole driver to monopolize:

- *preferred* — populate a **funnel** from the handler. This is the right
  primitive, not a workaround: it *is* the map-reduce (serial populate stays in
  the handler; Accumulate = map; Flush = reduce-and-emit; Close + return
  replaces the gather), dropping only the deadlock-prone inline
  wait-for-results.
- *fallback* — launch a **task** that drives the subwave (a genuinely
  structured sub-wave; demand-spawned, so no monopolization).

The ban is narrower than "drain-side ops can't gather" — it is specifically
about the **sole serial driver**, not about being drain-side. A funnel **flush**
is also drain-side (and also limiter-free), yet it *may* drive a subwave: it
runs on a demand-spawned funnel-pool worker, so a flush parked in a sub-gather
is simply replaced by another spawned worker — no monopolization. Same for a
funnel accumulate or a launcher task. So two distinct properties are at work:
*drain-side* governs limiter placement (skim handlers and flushes are
limiter-free); *sole serial driver* governs the gather ban (only the skimmer
drain has one). They coincide for skim and diverge for flush — which is why the
check keys on skim context specifically, not on drain-side-ness.

This rule is what makes the hold-through class safe under limiter sharing: with
the drain always drivable, "queue drain never depends on a permit" holds, and
the committed suspend/reclaim brackets need no further mechanism (no
help-outward, no accept-and-defer, no metric). The cross-subjob deadlock that
motivated the rule was exactly a skim handler driving a subjob (`runSubjob`
from a Skimmer body); it is validated fixed with cross-subjob limiter sharing
enabled (`0 hangs / 30`, baseline `3 / 30`).

## Serialization and scoping

The handle needs no internal synchronization — not because it lives on one
goroutine (it doesn't), but because it is **externally serialized**: never
touched concurrently, with every cross-goroutine hand-off carrying a
happens-before edge through the queue or notifier it travels on. The lifecycle
touches up to four goroutine roles:

1. **acquire** — the dispatching goroutine, or whichever worker re-invokes
   postponed work (retries can hop workers);
2. **postpone/re-grant** — the goroutine whose Execute failed to start the
   inner work; then whatever goroutine the listener notification wakes;
3. **suspend/resume** — the body's worker goroutine;
4. **release** — completion (`completedFn`) or a shutdown-path `Free`.

The HB edges, as verification obligations (covered empirically by the
non-short `-race` sim loop): dispatch→queue→worker; postponed-set→retry;
listener→notify→waiter→re-grant; body→completion. Any future change that adds
a hand-off owes its edge to this list. (Rejected: a debug owner-assertion on
the handle — the owner legitimately changes across phases, so it would need
the full phase model to avoid false positives; `-race` covers the risk.)

Scoping — how a parking point finds the handle:

1. The handle is stamped on the body's per-worker `ctxMeta` at body entry,
   with the same save/restore stack discipline as the dispatching `wave` stamp
   (stamp on entry, restore the prior value on exit; same caveat about user
   code capturing ctx into goroutines that outlive the body). For the task
   path the handle is created at dispatch and travels across the queue
   hand-off to the body as one opaque value — still no token threading.

2. `ctxMeta` gains a `parent *ctxMeta` link along synchronous, same-goroutine
   derivations (top-level->skim, body->`NewWave`->subwave contexts), so a park
   inside a subwave walks `parent` up to the body's handle. **Worker contexts
   are fresh permit-roots (`parent == nil`)** — explicitly *severed* at
   task/funnel worker-context creation, because the body ctx derives from the
   dispatcher's ctx: fresh-rootness is enforced, not inherited — even though
   they still record the parent *wave* in `parentJobs`.

`currentHeldRequest` walks `parent` from the current context and **stops at
the first stamped handle**. Structurally there is at most one per chain: with
skimmers limiter-free (see the two-class rule's commitments), no limiter-gated
body is ever help-executed, so every stamp site is a chain root. This is
pinned by a free, always-on assertion at stamp time — `assert(meta.parent ==
nil)`, one pointer compare on the hot path — so any future non-root stamp site
(e.g. skimmer limiters) panics at first stamp, immediately and located. The
bracket-side walk runs only at parking points (cold; the goroutine is about to
park), with depth bounded by the synchronous-nesting depth.

Invariant: **a stamped handle is only ever HELD or SUSPENDED.** POSTPONED is
pre-body (the stamp happens at body entry, after the grant); DONE is
post-unstamp (body exit restores the stamp before completion releases). The
walk can never encounter PENDING/POSTPONED/DONE on a chain, so the bracket
contract stays exactly `r != nil && r.suspend()` — no state checks at call
sites: finding the *enclosing* episode's SUSPENDED handle no-ops; finding the
current body's HELD handle suspends it.

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

Implementation: the drop lives inside the sim's Func-walker at the `runSubjob`
step (entry: decrement; return: restore) — one point covers every body kind
that can drive a subwave (Launcher bodies, Accumulate, Flush, and skim
handlers executed via block-and-help), and the span subsumes the subjob's
internal block-and-help suspensions. **No drop around blocking submits** —
those are hold-through: the permit is genuinely held, siblings genuinely can't
enter. Both measurement edges skew toward under-counting (the sim drops before
the framework actually suspends, restores after the reclaim completes), so
`observed ≤ permits` stays sound — which also makes the measurement change
safe to land *before* the suspend brackets (implementation order: measurement
first, brackets after; see WORKING_NOTES).

The sim must also generate **limiter sharing across waves/subjobs** — its
fresh-limiter-per-(sub)job structure is a legacy holdover, and both deadlock
witnesses in this note live in exactly that blind spot. When one limiter is
shared across nesting depths, its concurrency counter must be shared along
with it: split counters would each assert `observed ≤ permits` on a subset and
could miss a joint violation.

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
**suspend/postpone/reclaim/release fan out per resource kind**: in a skim the
scheduler releases each `suspendable()` resource and re-takes them on reclaim
(a semaphore relinquishes; a memory resource — not suspendable — stays held);
on postpone it releases *all* held amounts (pre-body, nothing materialized);
`release` cleans up by state. A richer resource just reports a larger `demand`; no
cross-resource commit protocol is involved (atomicity, where needed, comes from
the prioritized scheduler's evaluate-lock). All of this lives between the
scheduler and its resources; the framework-facing handle is unchanged.

## Scope: shipped now vs designed-for-later

**Shipped.** The suspend-on-block [Semaphore] behind the handle (full state
machine including POSTPONED; illegal-transition panics), constructed
`NewSemaphore(nil, n)` — the scheduler argument lands now (the role made visible
at construction) but only `nil` (self-scheduled) is supported; `ExecuteOrWait`
split into routing + the shared `blockingAcquire`; the two-class park rule
(suspend-class brackets with help-shaped reclaim; hold-through at capacity
waits); the externally-serialized `ctxMeta` scoping with the root-stamp
assertion; the capacity-changed hook wired to `SetMaxConcurrency`; the sim's
active-concurrency measurement and cross-wave limiter sharing. No non-nil
scheduler, no prioritization — the semaphore's request is trivial and the
sibling-contention livelock's liveness already holds through the existing
renotify chain.

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
- **Uniform suspend at every framework park** (including capacity waits).
  Anti-backpressure: a body parked on a full downstream queue that gives up
  its permit lets siblings pile into the same full pipe — unboundedly many
  half-done parked bodies. Holding there is the limiter *propagating*
  backpressure; the two-class rule keeps suspension where it is
  correctness-required and nowhere else.
- **Plain-wait reclaim** (reclaim without block-and-help). Deadlocks under
  shared limiters — see the witness in "Reclaim is help-shaped": hold-through
  capacity waits plus a plain-waiting driver close a cycle through the
  subjob's skim queue. Any wait on a goroutine with driving duties must help
  its driven domain.
- **`capacityIncreased() <-chan struct{}` on the resource contract.** A ping
  with no consumer parked sits unconsumed (postponed listeners starve on a
  `0→n` resize), a unary ping can't carry the delta, and multi-resource
  schedulers would need forwarder goroutines just to consume it. Superseded by
  the bind-time `setCapacityChangedFn` hook.
