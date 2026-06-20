# Dispatch/Execution Split and the Global Permit Scheduler

**Status: design, not yet implemented.** This document records an architecture
arrived at through design discussion. It supersedes the goroutine-level
block-and-help and the *eager* limiter suspend/reclaim protocol of
`limiter-suspend-resume.md`, replacing both with (1) a clean separation of
dispatch from execution and (2) a permit model in which a unit holds its permits
through parks and gives them back only as a last resort. It deliberately keeps
the load-bearing constraints of the limiter design (intake/drain split,
skim-gather ban) and the limiter design's scheduler/resource layering, narrowing
the scheduler to a single global instance.

## The problem it solves

Every intermittent nested-drain deadlock we have chased traces to one conflation:
**a single goroutine does both the framework bookkeeping (admission, permits,
queue management) and runs the blocking user body.** A pool worker pulls a body,
the body re-enters the framework — `Launcher.Start`, `Submit`, a synchronous
`CloseAndSkimAll` on a sub-wave — and *blocks*. Because that goroutine was also
the thing draining queues and admitting work, blocking the body blocks the
dispatcher too, and the system can wedge with every worker parked inside a body
and nobody left to make progress.

The symptoms we kept patching — block-and-help and its hand-scoped re-entrancy
rule, the nil-demand skim queue's starvation, the spawn token pinned across a
blocking body, the vacate/replacement subtleties — are all downstream of that one
conflation.

## The split: managers and executors

Two pools, separated strictly by concern:

- **Executor pool** — runs user bodies and *nothing else*. An executor is
  *allowed* to block: that is its job. It scales on demand for ready bodies.
- **Manager pool / scheduler** — owns the work queues, admission, permits, and
  scheduling, and **never runs user code**, so it never blocks indefinitely.

Flow: a body submits new work to the managers; a manager, when it has work that
is *ready* (admitted — permits and governor satisfied), hands it to the executor
pool.

**The always-live-dispatcher invariant.** Because managers never run user code,
there is always a goroutine making progress admitting ready work, no matter how
many executors are parked inside blocking bodies. The old deadlock was only
possible because dispatch and execution shared goroutines; separating them makes
that class of deadlock impossible by construction. Demand-driven executor scaling
remains — "ready body, no free executor → spawn" — but its demand signal is now
produced by an always-live source, so it can never be lost the way it was when the
producers were themselves blockable.

block-and-help does not vanish so much as become *safe by construction*: a
goroutine draining wave W only ever skims W's own queue, while W's bodies run on
the executor pool. It can never wander into the global executor queue and
re-enter itself, because it never touches that queue.

## Submits and backpressure

Skimmers **must** run serially, and they run **on the draining goroutine** — the
one that called `Skim`/`SkimAll`/`CloseAndSkimAll` for that wave. Keeping skim
there preserves the ordering contract and a free backpressure channel: the
goroutine that submits top-level work also skims as it goes, so a producer
literally *is* its own consumer and cannot outrun it. There is exactly one serial
skim consumer per wave.

Three kinds of submit, differing in where they block:

- **Top-level submit** (from the draining goroutine): skims-as-it-goes and hits
  the governor/permit gate. Blocks the draining goroutine, which keeps skimming.
- **Nested same-wave submit** (a body scattering more work into its own wave):
  **queues** to the manager, non-blocking; the body races on, the manager admits
  it when the governor and permits allow. Backpressure on these is applied at
  *admission* (held un-admitted), not by blocking the executor.
- **Sub-wave drive** (a body creating and draining a sub-wave): a *top-level
  submit with respect to that sub-wave* (the executor becomes that sub-wave's
  serial skimmer) and merely a *blocked executor* to the parent wave, which the
  parent's executor pool scales to cover.

The **governor** survives unchanged in purpose: it shuts down top-level submits
when the pipeline is clogged — at skimming capacity, or blocked waiting for
permits. Its mechanism is the existing downstream backpressure counter; the only
change is that the relief path (consuming skim) is guaranteed by the always-live
managers feeding executors, not by the blocked producer helping.

## The permit model: held through parks, with deltas

The executor never participates in permit management — it requests its set, runs,
and parks. All permit logic lives in the scheduler. The model has exactly two
ideas: **base holds** and **deltas**.

### Base holds: held by the unit, period

A unit acquires its permits when admitted and **holds them for its entire
lifetime, period** — released only on completion. We do not try to detect when a
unit is "really using" a permit versus merely holding it idle, because in the
general case we cannot know.

Letting the unit's **sub-waves use those already-held permits** is just accepting
that same uncertainty one level down: the sub-waves are the unit's own work, so
they draw on the unit's hold. Nothing is reallocated and nothing leaves the unit's
subtree. (This is what the prior draft called "inheritance"; the key point is that
it is not a separate grant — it is the same hold, used by the unit's descendants.)

### Deltas: additional permission, scoped and self-releasing

The *only* acquisition beyond a unit's base is the **delta**: when a sub-wave unit
needs **more** permits than the enclosing unit holds, that extra permission is
acquired separately, scoped to that sub-wave unit, and **released as a natural
consequence of that unit completing** — ordinary lifecycle with a tighter scope
than the base, not a special preemptible class and not a forced suspend. Base
permits release when the unit finishes; deltas release when the sub-wave unit
finishes. One lifecycle, two scopes.

Deltas are the sole contention point, and they must be acquired deadlock-free —
which is the whole subtlety below.

### The cross-subtree cycle (the cost of holding through parks)

Because base holds are retained **through a park** (a unit driving a sub-wave
still holds its permits — that is what "period" means), a delta can need a permit
that is, right now, a base-hold of *another parked unit in a different subtree*:

> B holds P and is parked driving a sub-wave whose unit V needs Q; C holds Q and
> is parked driving a sub-wave whose unit W needs P. Neither base-hold releases,
> because each holder is parked driving the very sub-wave whose delta needs the
> other's permit. Nobody completes, so no delta releases. Deadlock.

This cycle is *created by* hold-through-park. The earlier eager-suspend design
avoided it precisely by releasing a permit when its holder parked — at the cost of
churn and re-acquisition on every park. Holding through parks is the minimum-WIP
choice; the cycle is the bill for it, paid by the breaker below.

### Breaking it: suspend-while-parked, as a last resort

The only hold in such a cycle that is **not backing running work** is a *parked
driver's base permit*: the driver is parked (not executing), so that permit is at
**zero leaf** — no descendant is currently running with it — and can be
reconsidered without preempting a running body. So the cycle-breaker is: the
scheduler **suspends a zero-leaf parked base permit**, satisfies the blocked delta
with it, and the original holder re-acquires it when it resumes.

This is a *last resort*, used only to break an actual deadlock, to minimize the
re-acquisition churn it costs (minimum-WIP). It is **not** a transfer between
reservations and **not** an eager release; it is the scheduler reconsidering one
non-running hold, exactly when waiting can no longer resolve.

**Detecting when to do it.** The wait-for graph has two edge kinds and only one is
dynamic:

- *drive edges* — a parked unit waits for its sub-wave's units. These **are** the
  wave-ancestry tree: static, acyclic on their own.
- *resource edges* — a blocked unit waits for the holder of the permit it needs.
  At most one per blocked unit, added on block, removed on grant.

Every cycle therefore routes through resource edges and is composed only of
*currently-blocked* units, so detection is a bounded walk over the small,
contention-bounded blocked set — never the whole workload — run only on the cold
path (a unit that could not get its permit). It shrinks further if each parked
driver carries the small set of permits its subtree is blocked on (propagated up
on block/unblock): the search graph is then just "parked holders ↔ contested
permits."

**Baseline first, detection as an optimization.** The cheap correct baseline needs
no graph at all: a block that has not cleared by natural completion, against a
holder that is *parked and zero-leaf*, gets that permit suspended — a local check
on the holder's state. It is always safe (never touches running work) and resolves
the block whether or not a true cycle existed; its only cost is extra re-acquisition
churn when it fires on a non-cycle that would have cleared on its own. Precise
cycle detection is purely a **churn-reduction** optimization over this baseline,
worth building only if the measured suspend/re-acquire rate proves material — and
under the structural rules below, genuine cycles should be rare enough that it may
never pay for itself. The one knob to decide with the baseline is the trigger for
"hasn't cleared": prefer event-based (fire when the contended permit's holders are
all parked and nothing in that holder-set is runnable) over a clock, consistent
with the rest of the design — never wait on a timer for something observable.

## The scheduler is global

Hold-through-park makes the cross-subtree cycle span **multiple limiters** (it
alternates "holds P, needs Q" with "holds Q, needs P"), so the wait-for graph that
contains it spans those limiters. Detecting and cleanly breaking it needs one
unified view over all the limiters the cycle can touch — and since a forest's
nesting is dynamic (any op can drive any sub-wave), you cannot statically prove two
limiters never co-nest into a cross-cycle. So everything reachable collapses to one
coordinator. Rather than compute per-forest scheduler boundaries (and solve the
"limiter created before its forest exists" binding puzzle), the scheduler is simply
**global, one per process**, like `defaultPool`. A global coordinator also "sees"
cycles that cannot form across genuinely independent forests, which costs nothing
but a marginally larger graph on the rare cycle check.

**Global must not mean a global lock per acquire.** The hot path stays cheap via
the existing resource/scheduler split:

- An **uncontended acquire/release** touches only *that limiter's own* per-limiter
  state — its count, a small bounded holder set, the per-wave governor counter, a
  push to the executor queue. This scales per-limiter and never serializes across
  limiters or across cores.
- Only **cross-limiter** work — a multi-limiter atomic-fit (the prioritized
  scheduler's whole-vector grant) and the cycle detect/break that by definition
  spans limiters — needs the coordinator. That path is rare by construction: it
  fires under saturation, which scales with permit pressure, not core count.

So the part that wants to be **single-writer** is the *cross-limiter coordinated
state* (the wait-for view, the cycle-break choice, the prioritized atomic-fit) —
because that is where every hard race lives, and a single owner turns
locks-and-races into plain sequential code. That is **not** the dispatch hot path.
Concretely: do not funnel common admission through one goroutine (that is what
would cap throughput when cores are plentiful); do let one writer own the
cross-limiter coordination, routing only blocked + multi-limiter admissions to it.
More cores then mean more parallel per-limiter acquires, not more coordination —
unless you are also more saturated, at which point you are permit-bound anyway and
serializing the contended decisions is the right thing. **One writer for the
coordination, not one dispatch worker.**

## Infeasible demand: never hang on it

A permit demand that can never be satisfied is just another silent deadlock, so it
gets the same treatment as everything else here — surface it, do not wait on it.
Two cases, opposite answers:

- **`W > total capacity`** (can never fit, any amount of waiting). Detected at
  admission (the scheduler knows capacity and computes the demand). If the weight
  is **static** (fixed per op), it is a misconfiguration — **panic**, ideally
  caught at op construction like the `WithLimits` checks. If the weight is
  **data-dependent** (e.g. bytes from the work item), it is a runtime condition —
  **fail the unit with a distinct error** so the caller can reject/split/route the
  oversized item; panicking on bad input would make the library brittle. Never
  "allow anyway" — overcommit silently breaks the guarantee the limiter exists to
  provide.
- **`W ≤ capacity` but unsatisfiable right now** (blocked by contention/cycle).
  *Not* a failure case — it is the scheduler's job to get there (withhold, order,
  and break the cycle per above). Failing or panicking here would turn solvable
  contention into a spurious error. Wait; rely on the deadlock-free machinery. The
  only thing forbidden is silently waiting forever.

(The "satisfiable only alone" request — `W ≤ C` but needing the whole pool — is not
an allow-over-capacity case; it is the prioritized scheduler's withholding/
anti-starvation path granting it once everything else clears.)

## Relationship to the limiter design, and what changes

This design **keeps**, unchanged and load-bearing:

- **Limiters gate intake, not drain.** Launcher tasks and funnel accumulates carry
  `WithLimits`; skim handlers and funnel flushes are limiter-free. This is what
  makes "queue drain never depends on a permit" true, which the always-live-managers
  story also leans on.
- **The skim-gather ban** (`vetNotNestedInSkim`): a skim handler — the sole serial
  drain driver — may not drive a blocking gather.
- **The scheduler/resource layering**: schedulers are the closed set (direct,
  ordered, prioritized) carrying the protocol; resources are the open extension
  point (semaphore, memory, rate, weighted), each a small accounting object.

This design **changes**:

- **Eager suspend/reclaim → hold-through-park + last-resort suspend-while-parked.**
  The two-class park rule and its bracket/reclaim machinery on the body path are
  retired; permits are held through parks, and the only give-back is the
  scheduler's zero-leaf cycle-breaker.
- **Per-op / per-domain scheduler → one global scheduler.** `WithLimits`'s "all
  limiters resolve to one scheduler" widens to "the global scheduler"; the
  `NewSemaphore(scheduler, n)` binding becomes binding to the global instance.
- **Terminology.** That doc's "reservation" is a *PriorityScheduler withholding
  policy* and is unrelated to anything here; this design speaks of a unit's
  *held set* and *deltas*, never a "reservation," to avoid the collision.

## What this deletes

- the goroutine-level **block-and-help drive loop** and its re-entrancy rule;
- the **eager suspend/reclaim** body-path protocol (bracket, re-entrancy no-op,
  help-shaped reclaim) — replaced by the scheduler's last-resort zero-leaf suspend;
- the **nil-demand skim queue's** starvation case (always-live managers feed
  executors, so the relief path is never unfed);
- the **spawn-token-through-body** hazard and the vacate/replacement subtleties —
  executors are *meant* to block, and the pool scales under an always-live producer;
- **per-forest scheduler bookkeeping** — there is one.

## Open / next

- Sketch the scheduler's permit core on its own — base-hold + delta acquisition,
  the per-limiter hot path, the single-writer cross-limiter coordination, and the
  baseline suspend-while-parked — and model-check the no-silent-wait and
  cycle-break invariants in isolation before any cutover.
- Decide the baseline's "hasn't cleared" trigger (event-based preferred).
- Defer precise cycle detection until a measured churn number justifies it.
- Map the manager/executor pools and the governor's per-wave gate around the core.
