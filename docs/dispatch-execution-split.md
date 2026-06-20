# Dispatch/Execution Split

**Status: design, not yet implemented.** This document records the
dispatch/execution architecture arrived at through design discussion: a clean
separation of the goroutines that run blocking user bodies (**executors**) from the
goroutines that own admission, queues, and scheduling (**managers**). It supersedes
the goroutine-level block-and-help and the *eager* limiter suspend/reclaim protocol
of `limiter-suspend-resume.md`. The permit *allocation* model that rides on this
split — pools, the locality-ordered acquire, idle-stealing, cache-don't-return — is
specified separately in `permit-core.md`; this document covers only how the split
shapes, and is shaped by, that model. It deliberately keeps the load-bearing
constraints of the limiter design (intake/drain split, skim-gather ban) and its
scheduler/resource layering.

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

## Permits: managed off the executor, allocated per `permit-core.md`

The split's rule for permits is simple: **the executor never manages them.** It
requests the permits its body needs, runs, and parks; all allocation logic — the
pools, the locality-ordered acquire, idle-stealing, the cache-don't-return flow —
lives below that request interface and is specified in `permit-core.md`. That
document carries the model and its central result: the permit core is
**deadlock-free per-limiter, with no cycle graph and no global coordinator**, so
this split can lean on "a ready body's permits resolve without a cross-cutting
wedge" without itself reasoning about permit cycles. (It is what retired the
earlier draft's hold-through-park-plus-cycle-breaker machinery; that history lives
in `permit-core.md`.)

What *this* document owns is how the split **drives** that acquire, in two modes
that differ only in what they do on a miss:

- **Manager-side, at admission — non-blocking.** A manager makes a body *ready* by
  running the acquire without its waiting step (own pool → ancestor → free L →
  steal): on success the body is handed to an executor; on a miss the work is held
  un-admitted and retried when a permit frees. The manager never blocks, so the
  always-live-dispatcher invariant holds.
- **Executor-side, mid-body — blocking.** A driving executor that parked (lending
  its permits while blocked-waiting on a sub-wave) must **reacquire** to resume
  computing — to run a skim handler, or to return from the drive. That reacquire is
  the full acquire *including* the waiting step: the executor may block, which is
  exactly what executors are for, and the pool scales another executor to cover the
  parent wave. (See `permit-core.md`, "Driving is an alternation.")

So one primitive is *postponing* on the manager and *blocking* on the executor —
the routing the eager design tangled into `ExecuteOrWait`, now cleanly divided by
which pool runs it.

## The cross-limiter coordinator (deferred)

The permit core needs **no global coordinator for deadlock-freedom** — idle-stealing
is per-limiter and local (`permit-core.md`). A coordinator is needed for exactly one
thing, and it is **deferred**: **atomic joint acquisition** when an op's `WithLimits`
spans several limiters that must be taken all-or-nothing (the ordered and
prioritized scheduler disciplines, including starvation-avoidance by withholding).

When that feature is built, it must not become a global lock per acquire. The
resource/scheduler split keeps it cheap:

- An **uncontended acquire/release** touches only *that limiter's own* per-limiter
  state — its pool counters, the per-wave governor counter, a push to the executor
  queue. It scales per-limiter and never serializes across limiters or cores.
- Only **cross-limiter** work — the multi-limiter atomic-fit (the prioritized
  scheduler's whole-vector grant) — needs the coordinator, and only under
  saturation, which scales with permit pressure, not core count.

So the part that wants to be **single-writer** is the cross-limiter coordinated
state (the prioritized atomic-fit, the withholding policy): one owner turns
locks-and-races into plain sequential code, and it is **not** the dispatch hot path.
Route only blocked multi-limiter admissions to it; let per-limiter acquires run in
parallel. **One writer for the coordination, not one dispatch worker.** Whether that
owner is process-global or per-limiter-group is ergonomic — global sidesteps the
"limiter created before its forest exists" binding puzzle — not a deadlock
requirement.

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
- **`W ≤ capacity` but unsatisfiable right now** (blocked by contention).
  *Not* a failure case — the permit core gets there (free → steal idle → wait, per
  `permit-core.md`; withholding/ordering when the deferred multi-limiter coordinator
  is in play). Failing or panicking here would turn solvable contention into a
  spurious error. Wait; rely on the deadlock-free machinery. The only thing
  forbidden is silently waiting forever.

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

- **Eager suspend/reclaim → the hierarchical permit cache** (`permit-core.md`). The
  two-class park rule and its bracket/reclaim machinery on the body path are retired;
  permits are held through parks and cached idle rather than suspended, and the only
  give-back is an idle *steal* by a pool that genuinely needs the permit — no
  suspend, no eager release, no cycle-breaker.
- **Per-op / per-domain scheduler → a per-limiter permit core, no coordinator.** The
  core is deadlock-free per-limiter on its own; `WithLimits`'s "all limiters resolve
  to one scheduler" applies only to the **deferred** multi-limiter joint-acquisition
  feature, where a single coordinator (process-global or per-group) owns the
  atomic-fit.
- **Terminology.** That doc's "reservation" is a *PriorityScheduler withholding
  policy* and is unrelated to anything here; this design speaks of *pools*
  (`held`/`inUse`) and *deltas*, never a "reservation," to avoid the collision.

## What this deletes

- the goroutine-level **block-and-help drive loop** and its re-entrancy rule;
- the **eager suspend/reclaim** body-path protocol (bracket, re-entrancy no-op,
  help-shaped reclaim) — replaced by the permit core's cache-and-steal flow
  (`permit-core.md`), which never suspends;
- the **nil-demand skim queue's** starvation case (always-live managers feed
  executors, so the relief path is never unfed);
- the **spawn-token-through-body** hazard and the vacate/replacement subtleties —
  executors are *meant* to block, and the pool scales under an always-live producer;
- **cross-cutting permit coordination on the deadlock path** — there is none; the
  per-limiter core is deadlock-free, and the only coordinator (for the deferred
  joint-acquisition feature) is off the hot path entirely.

## Open / next

- The permit core (pools, acquire, idle-steal) is specified and has its own
  build/model-check plan in `permit-core.md`, "Open / next". This document's
  remaining work is the **dispatch side**:
- Map the **manager and executor pools** onto the existing `worker.Pool` +
  `workq.Queue`: who admits (manager, non-blocking acquire) versus who runs and
  reacquires (executor, blocking) — the two drive modes above — and how demand-driven
  executor scaling reads its signal from the always-live managers.
- Place the **governor's per-wave gate** around the manager admission path (it
  shuts down top-level submits when the pipeline is clogged at skim or permit
  capacity).
- Sequence the migration off the live eager code in `limiter.go`
  (`directRequest`/`reclaimRequest`/`suspendForEpisode`) without a flag day.
