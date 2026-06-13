# Limiter Suspend/Resume — Design Review Findings

Review of `docs/limiter-suspend-resume.md` against the current tree (branch
`combiner`), 2026-06-10. Each finding has a Status line to track resolution as
we work through them; resolutions should flow back into the design note (and
the implementation plan in WORKING_NOTES) before task #1 starts.

Overall verdict: the design holds up against the code. The handle/scheduler/
resource layering, the telescoping suspend argument, and the fresh-root scoping
rule all check out against the actual dispatch paths. The findings below are
contract amendments, one decision to record, and plan hygiene — not a
rethink.

---

## Finding 1 — Missing state transition: HELD after a postponed inner post

**Severity: contract gap (must resolve before task #1).**
**Status: RESOLVED (2026-06-10) — new POSTPONED state; see resolution below.**

Today `limiterScatterWork.Execute` acquires the permit, then the inner
`taskPostWork` tries to post to the task queue. When the dispatch comes from a
skim or funnel context (postpone discipline), the inner post can fail-to-start
*after* the permit was acquired. The current defer (`limiter.go:178-184`)
releases the permit when `!ex.Started() && w.acquired`, and the retry
re-acquires — so today a permit is never held by work parked in the postponed
queue.

The design's routing sketch doesn't cover this case: after `tryAcquire()`
succeeds, if `workFn` returns not-started, the handle is HELD with no legal way
back. `release()` goes to DONE, and the retry would need a fresh `newRequest` —
destroying exactly the stable-identity-across-retries property the handle
exists to provide. The state machine has no HELD→PENDING.

**Resolution (PN, 2026-06-10): a distinct POSTPONED state**, not a reuse of
SUSPENDED. Same give-back accounting for the concurrency dimension, but a
separate state because the scheduler genuinely branches differently on it:

- The state machine becomes `PENDING → HELD ⇄ SUSPENDED → DONE` with
  `HELD ⇄ POSTPONED` alongside; `postpone()` mirrors `suspend()`.
- **Per-resource divergence (the behavioral case for the split):**
  concurrency — both states give the slot back; memory (hold-through) —
  SUSPENDED *keeps* the reservation (the parked body has live allocations) but
  POSTPONED can *release* it (nothing materialized; the body never started);
  rate — neither re-pays, falling out of the handle's per-resource bookkeeping
  (on resume, re-take exactly the dimensions released for that state; rate was
  never released, so it is never re-paid — no special case).
- **Retry entry is state-aware** and sees only PENDING or POSTPONED (SUSPENDED
  cannot appear pre-body; HELD cannot survive a not-started return).
- **`notifier()` becomes phase-routed three ways:** acquire (PENDING),
  re-grant (POSTPONED), reclaim (SUSPENDED). The direct scheduler returns the
  same notifier for all; a future prioritized scheduler can route reclaimers
  ahead of re-grants ahead of fresh acquires. This also dissolves the
  phase-routing ambiguity for a granted-then-yielded request.
- **Terminology caveat to state in the design note:** "postponed" is already a
  workq term with wider meaning — a work item postpones whenever it can't
  start, including while its request is still PENDING. So *work postponed* does
  NOT imply *request POSTPONED*; the state means specifically "granted, then
  yielded the grant because the gated work couldn't proceed."

Design-note amendment required before task #1.

---

## Finding 2 — The concrete bracket list is incomplete relative to its own uniform rule

**Severity: design decision to record (affects task #4 and the sim).**
**Status: RESOLVED (2026-06-10) — two-class rule; see resolution below.**

The note enumerates three parking points: the public skim methods,
`Pool.block`, and the scheduled-flush wait. But `Pool.block` is reachable only
from top-level contexts (`j.shouldBlock` returns the blockFn only for
`IsTopLevel()`, `job.go:305-311`). A **task body** holding its permit parks
somewhere else entirely: the task-context blocking posts —
`taskPostWork`'s `BasicPushSelect`, and the funnel submit's blocking branch
(`combinerpool.go:294ff`) — when it Submits to a sink against a full queue.
That is a framework-mediated park with a held permit, and it is not on the
list. The note explicitly disavows "minimal sufficient set" reasoning, then
ships one.

**Resolution (PN, 2026-06-10): a principled two-class rule replaces site
enumeration.** Classify every framework park by the *kind of wait*, not by
listing sites:

- **Suspend class — parks that wait on, or synchronously run, other
  framework-gated work:** the gathers (public skim methods) and the
  block-and-help episodes (today `Pool.block`). Resolution of these waits can
  depend on permit availability, so holding across them risks circular waits.
  Suspension is correctness-required.
- **Hold-through class — pure capacity waits:** the task-context blocking
  posts (`taskPostWork`'s `BasicPushSelect`, the funnel submit's blocking
  branch). Safe by the invariant **"a permit wait never occupies bounded queue
  capacity"** — a consumer that can't acquire its permit *postpones*, vacating
  its queue slot, so queue space never depends on permits (verified even under
  adversarial limiter sharing between producer and consumer ops). And holding
  is *desirable*: the held permit is the limiter's backpressure-propagation
  mechanism — suspending at capacity waits would admit sibling after sibling
  into a full pipe, piling up unboundedly many half-done parked bodies.

**New supporting evidence (must go into the design note): shared-limiter
self-deadlock at the block-and-help point.** If a parent op and an op inside a
subwave driven from the parent's body share a `limit=1` Limiter, the
subjob-top-level dispatch enters `blockingAcquire` on a permit held by the
*same goroutine's* parent body — without the bracket, an infinite
block-and-help spin (same signature as the original livelock). The bracket
dissolves it (parent handle suspends, subjob acquires). This upgrades
block-and-help bracketing from "airtight bonus" (its prior justification in
WORKING_NOTES) to correctness-required.

**Coherence with Finding 1:** POSTPONED yields a *pre-body* grant (nothing
started, nothing materialized); hold-through keeps a *mid-body* grant across
capacity stalls (work in progress; its hold is backpressure). The line in both
cases is "has the body started" plus the wait classification.

**Naming note:** the design note should name the suspend-class site by its
episode class — "the block-and-help wait" — not by the method name
`Pool.block`. The episode is semantically wave-aligned (the destination
architecture migrates drain machinery from Pool to Wave per REFACTOR_PLAN
Wave 5), but today it mechanically parks on the pool's `workQueue` and helps
cross-wave. Naming the class keeps the rule stable across the Pool/Wave
consolidation; any rename/relocation belongs to that pass.

**Sim consequence (simplifies Finding 4):** task #5 stays as scoped — drop
spans around `runSubjob` only (which subsume their internal block-and-help
episodes). No drop around blocking `submitTo` calls: the permit is genuinely
held there, siblings genuinely can't enter, `observed ≤ permits` is preserved.

**Design-note amendment:** replace the three-site enumeration with the
two-class criterion + the queue-capacity invariant; classify each existing
park; require every future parking point to be classified into one of the two
classes.

---

## Finding 3 — `SetMaxConcurrency` growth-wake hole in the resource contract

**Severity: contract gap (must resolve before task #1); no test coverage.**
**Status: RESOLVED (2026-06-10) — bind-time callback replaces the channel.**

`capacityIncreased()` is specified as "consulted only in the blocking path."
But postponed waiters — funnel-context dispatches that registered on
`notifier().Listeners` and returned — are woken today by `setMaxConcurrency`'s
direct `notify.Notify` (`limiter.go:131-139`). Under the new layering, if
capacity grows while no goroutine is parked in a blocking wait, the ping sits
unconsumed in the buffered-1 channel and postponed listeners starve.

Concrete failure: semaphore at 0 (or saturated by long-running work), only
postponed funnelWorks waiting, user raises the limit via `SetMaxConcurrency` →
nothing wakes. The sim never resizes, so no existing test catches it.

**Resolution (PN, 2026-06-10): drop `capacityIncreased() <-chan struct{}`;
replace with a bind-time callback.** The resource stays pure accounting plus
one registration point — the scheduler installs a hook when the resource is
bound (e.g. `setCapacityChangedFn(fn func(delta int))`, or passed at
construction). The resource author's obligation is one line: after raising
capacity, call the hook if non-nil. The scheduler's hook implementation routes
to its *full* waiter set — `Notify` per freed slot, `NotifyAll` on unlimited,
listeners included — mirroring today's `setMaxConcurrency` logic
(`limiter.go:131-139`).

Why over the alternatives: no consumer goroutine needed (a channel ping with
nobody parked just sits unconsumed — the original hole); the hook carries the
*delta*, which a unary channel ping cannot (a `0→n` resize must wake n
postponed waiters); the prioritized scheduler's per-resource forwarder
goroutines disappear entirely. For the built-in Semaphore, `SetMaxConcurrency`
may route through the scheduler directly (both framework-owned), but the hook
is the contract that makes *user* resources with out-of-band growth work.

Documentation obligation for the open extension point: the hook must be safe
to call from any goroutine; resource authors should not invoke it while
holding their own locks if avoidable (the framework's implementation is a
notifier poke, safe from anywhere).

Design-note amendment required: the "Resources: the open layer" section's
channel rationale paragraph is superseded.

---

## Finding 4 — Sim measurement scope and plan sequencing

**Severity: plan hygiene (red intermediate checkpoint as currently ordered).**
**Status: RESOLVED (2026-06-10) — drop in the Func-walker; task #5 before #4.**

1. **Where to implement the drop.** Task #5 says "drop a body's contribution
   while it drives a subwave." Implement the drop inside the sim's Func-walker
   at the `runSubjob` step rather than per-body-kind — that uniformly covers
   Launcher bodies, Accumulate, Flush, *and* skim handlers executed via
   block-and-help, all of which can run subjobs. (With Finding 2 resolved to
   the two-class rule, no drop is needed around blocking `submitTo` calls —
   the permit is held through capacity waits, so siblings can't enter and the
   `observed ≤ permits` assertion holds without it.)
2. **Reorder task #5 before task #4.** Dropping contribution under the *old*
   (no-suspend) behavior only under-counts, so the assertion stays valid — a
   green checkpoint. The current order (#4 then #5) has a known-red
   intermediate state, violating checkpoint-at-green.

**Resolution (PN, 2026-06-10): both points adopted as written.** Implement the
drop inside the sim's Func-walker at the `runSubjob` step (entry: decrement
the limiter counter; return: restore) — one point covers all body kinds, and
the span subsumes the subjob's internal block-and-help suspensions. Both
measurement edges skew toward under-counting (drop before the actual suspend,
restore after the reclaim completes), so `observed ≤ permits` stays sound.
Implementation order becomes **1, 2, 3, 5, 4, 6** — each checkpoint
independently green; the hang baseline persists until #4 lands, as expected.
Finer-grained reclaim verification (permit re-held between subjob end and body
continuation) belongs in a handle unit test, not the sim's coarse counters.
Update the WORKING_NOTES implementation plan to match.

---

## Finding 5 — "Single-goroutine" is really "externally serialized"

**Severity: doc correction + verification obligations.**
**Status: RESOLVED (2026-06-10) — restate claim + list HB edges; see below.**

On the task path the handle is touched by the dispatching goroutine (acquire —
and across postponed retries, potentially *different* workers re-invoking
`Execute`), then the body's worker (suspend/resume), then whoever runs
completion (release via `completedFn`). Each hand-off has happens-before via
the queues, so the no-internal-synchronization conclusion still holds — but the
note's claim ("happens on one goroutine") is false as written and could mislead
a future optimization that assumes goroutine-locality (e.g. goroutine-local
caching in the handle).

**Resolution (PN, 2026-06-10):** restate the design-note claim as "externally
serialized — never touched concurrently; every cross-goroutine hand-off has a
happens-before edge through the queue or notifier it travels on." The handle's
lifecycle touches up to four goroutine roles (more with Finding 1's POSTPONED
state, not fewer):

1. acquire — dispatching goroutine, or whichever worker re-invokes postponed
   work (retries can hop workers);
2. postpone/re-grant — the goroutine whose Execute failed to start the inner
   work; then whatever goroutine the listener notification wakes;
3. suspend/resume — the body's worker goroutine;
4. release — completion (`completedFn`) or a shutdown-path `Free`.

HB edges to list in the note as verification obligations:
dispatch→queue→worker; postponed-set→retry; listener→notify→waiter→re-grant;
body→completion. All are exercised by the existing sim under the task #6
non-short `-race` loop (funnel/skim-context dispatches produce postponed
retries; task bodies produce suspend/resume); the note should say which edges
that pass is trusted to cover, so a future change adding a hand-off knows it
owes one. Rejected: a debug-build owner-assertion on the handle — the owner
legitimately changes across phases, so it would need the full phase model to
avoid false positives; `-race` already covers the risk.

---

## Finding 6 — Notify on every capacity-returning transition

**Severity: implementation checklist (silent-failure risk).**
**Status: RESOLVED (2026-06-10) — generalized rule + unit tests; see below.**

Suspend frees the slot; the scheduler must wake waiters/listeners exactly as a
completion release does — that wake *is* the livelock fix. The note implies it
("availability-wakeup is the scheduler's job") but never states that `suspend`
triggers it. If forgotten, the block-and-help renotify chain may partially mask
the omission in tests while the livelock survives in a disguised form (slot
free, waiter asleep, woken only by unrelated traffic) — a starved corner that
only the non-short `-race` loop occasionally trips.

**Resolution (PN, 2026-06-10):** with Finding 1's POSTPONED state the rule
generalizes: **every transition that returns capacity triggers the scheduler's
availability-wakeup** —

- `suspend()` (HELD→SUSPENDED): returns the suspendable dimensions;
- `postpone()` (HELD→POSTPONED): returns the suspendable dimensions and,
  per Finding 1, possibly more (a pre-body hold-through reservation is
  releasable);
- `release()` from HELD: returns everything held.

Contrapositive, also pinned: `release()` from SUSPENDED/POSTPONED must NOT
re-notify for dimensions already returned at suspend/postpone time —
double-notify is not a correctness bug (spurious wakeups re-check and re-park)
but it is renotify-storm fuel and a "no inflation" conservation soft spot
(TODO.md, Formal verification section).

Design-note amendment: one sentence in the handle/scheduler contract —
"capacity-returning transitions (suspend, postpone, HELD-release) notify;
state-discarding ones (release from SUSPENDED/POSTPONED) do not." Task #1
pins both directions with direct-scheduler unit tests: (a) a waiter blocked on
a full semaphore is woken by a sibling's suspend; (b) release-after-suspend
wakes exactly once, not twice.

---

## Finding 7 — Reclaim must be help-shaped (plain-wait reclaim deadlocks)

**Severity: correctness requirement (task #4) + new test coverage obligation.**
**Status: RESOLVED (2026-06-11) — help-shaped reclaim + sim limiter-sharing
extension; see below.**

The pseudocode gives one reclaim shape:
`for !tryResume() { helpSelectFn(req.notifier()) }`. The tempting
simplification — a reclaimer holds nothing, so plain-wait everywhere —
**deadlocks**. Witness (combines the Finding 2 resolutions):

1. Parent body holds shared `limit=1` Limiter L (shared with an op inside the
   subwave it drives).
2. Parent calls `Skim` on the subwave mid-drive → bracket suspends L →
   subjob body **B** acquires L and runs.
3. Parent's `Skim` returns → reclaim → `tryResume()` fails (B holds L) →
   plain wait.
4. B blocks posting its result into the subjob's full skim queue —
   hold-through capacity wait per Finding 2, so B keeps L. Correct per rule.
5. The only consumer of the subjob's skim queue is the parent goroutine —
   plain-waiting in reclaim for L.

Cycle: reclaim(L) ← B completes ← B's post drains ← parent skims ←
reclaim(L). Created by the *interaction* of hold-through (4) with plain-wait
reclaim (3); neither alone is wrong.

**Resolution (PN, 2026-06-11):** the design note's original help-shaped
pseudocode stands, with the composition made explicit:

- **Principle:** any wait on a goroutine that currently has a driving duty
  must help its driven domain — including reclaim waits. (Same deep rule that
  makes top-level acquire-blocking help-shaped.)
- **Help domain = the pool whose skim context the goroutine currently
  occupies** (the subjob), NOT the handle's owning pool (the parent is
  unhelpable from a body goroutine — no `Receiver`). Wake source = the
  request's notifier. The cross-pool composition is load-bearing, not
  optional.
- **`CloseAndSkimAll` reclaim** is the same composition, vacuously plain
  (drained subjob = empty help domain). No special case.
- Task-goroutine brackets stay moot per Finding 2 (no brackets on bare task
  goroutines; if that ever changes, `taskExEnv` has no `Receiver`).

**Coverage obligation (task #6): extend the sim to share limiters across
waves/subjobs.** The sim's fresh-limiter-per-(sub)job structure is a legacy
holdover, not a design constraint (PN) — plans should be able to share a
parent limiter into a subplan. Both Finding 2's self-deadlock and the reclaim
deadlock above live in exactly that blind spot; a property-based sweep over
shared-limiter topologies beats a single hand-written regression scenario.
Note the active-concurrency accounting (Finding 4) must aggregate correctly
when one limiter's counter is fed from ops at multiple nesting depths.

---

## Finding 8 — "At most one handle per goroutine": make it structural, not accidental

**Severity: design commitment + free assertion.**
**Status: RESOLVED (2026-06-11) — skimmers committed limiter-free; root-stamp
assertion; see below.**

As filed: the claim holds today only because Skimmers have no limiter support
(no `WithLimits` plumbing in `skimop.go`), so work executed synchronously via
block-and-help never stamps its own handle.

**Resolution (PN, 2026-06-11):**

1. **Commitment: Skimmers (drain-side ops) never get `WithLimits`.** Not a
   missing feature — load-bearing for Finding 2: the hold-through class is
   safe because "queue drain never depends on permits," and skim handlers ARE
   the drain. Permit-gating them would make capacity waits permit-dependent,
   recreating the cycle class this design dissolves. Users throttle expensive
   skim-handler work inside the handler with their own primitives (already
   their concern per the user-blocking boundary). State as a commitment in the
   design note; the TODO item on per-op concurrency limits inherits a pointer.
2. **Consequence: the invariant holds by construction.** With skimmers
   limiter-free, no limiter-gated body is ever help-executed; task and funnel
   bodies run on fresh-root worker `ctxMeta`s (`parent == nil`, task #2). So
   every handle stamp site is a chain root → at most one handle per goroutine
   chain, structurally.
3. **Pin: `assert(meta.parent == nil)` at stamp time** — a single pointer
   compare, always-on (no walk; the hot path pays one nil check). Compounds
   inductively: every stamp site asserts its own level, so any future non-root
   stamp site (skimmer limiters, a new synchronous body kind) panics at first
   stamp, immediately and located.
4. `currentHeldRequest`'s parent walk in the suspend brackets is unchanged —
   it runs only at parking points (cold; the goroutine is about to park), with
   depth bounded by synchronous-nesting depth, and it **stops at the first
   stamped handle** (under the structural invariant, first = only, at the
   root). In the one remaining nesting shape — help-execution — an inner
   bracket's walk finds the *enclosing* body's handle, already SUSPENDED by
   the outer episode; `suspend()` returns false → no reclaim installed →
   correct, because the reclaim belongs to the episode that suspended it.
   Stop-at-first is sufficient because the state machine absorbs the
   discrimination; the bracket never needs to know whose handle it found.
5. **Invariant (Findings 1+8 combined): a stamped handle is only ever HELD or
   SUSPENDED.** POSTPONED exists only pre-body (the stamp happens at body
   entry, after the grant); DONE only post-unstamp (body exit restores the
   stamp before `completedFn` releases). The walk can never encounter
   PENDING/POSTPONED/DONE on a chain, so the bracket contract stays exactly
   `r != nil && r.suspend()` — no state-checking logic at the call sites.

---

## Finding 9 — Small contract pins for task #1

**Severity: test/spec details.**
**Status: RESOLVED (2026-06-11) — always panic on framework bugs (PN); see
the legality table below.**

**Method/state legality (illegal = panic, always — framework bugs fail loud).**
Per Finding 8's invariant, a stamped handle is only ever HELD or SUSPENDED,
and per Finding 1, retry entry sees only PENDING or POSTPONED (a previous
Execute either started the work → DONE + freed, or didn't → POSTPONED; HELD at
retry entry is a bug):

| method      | legal states                            | result                          |
|-------------|------------------------------------------|---------------------------------|
| `tryAcquire`| PENDING (acquire), POSTPONED (re-grant)  | true→HELD / false stays         |
| `postpone`  | HELD                                     | →POSTPONED                      |
| `suspend`   | HELD → true; SUSPENDED → false           | re-entrancy is the ONE silent case (nested brackets, by design) |
| `tryResume` | SUSPENDED (reclaim)                      | true→HELD / false stays         |
| `release`   | any, idempotent (DONE→no-op)             | by-state cleanup — deliberate safety property, not a bug-masker |

(Naming settled 2026-06-11: a single state-aware `tryAcquire` covers both
acquire and re-grant — a separate `tryReacquire` would force the caller to
mirror handle state into the work item (drift risk: silent rate re-pay or
spurious panic) just to re-tell the limiter what it already knows; the
caller's request is identical in both states. `tryResume` stays reserved for
bracket reclaim, keeping legal sets disjoint per call site so the panic rule
still localizes bugs.)

Remaining pins, as discussed:

- `release()` from SUSPENDED/POSTPONED must not double-credit accounting (the
  give-back happened at suspend/postpone; Finding 6 pinned the notify side).
  Unit test: acquire→suspend→release leaves the count where acquire→release
  does.
- Handle stamp save/restore uses the `prevWave` stack pattern
  (`funnelop.go:802-804`), same documented caveat about user code capturing
  ctx into goroutines that outlive the body. Finding 8's
  `assert(meta.parent == nil)` lives at the same site.
- Task #2 explicitly severs `parent` at task/funnel worker-context creation —
  fresh-rootness is enforced, not inherited (Finding 8's assertion is the
  tripwire).
- `request.notifier()` is never nil — the unlimited semaphore returns its real
  notifier (as `limiterImpl.notifier()` already does today despite its doc
  comment); with Finding 1's three-phase routing, nil would cost three
  nil-checks instead of one guarantee. Fix the stale doc comment.

---

## Finding 10 — Cross-subjob shared-limiter deadlock survives the suspend brackets (DISCOVERED during task #4)

**Severity: design gap (blocked flipping the sim's `Inherit` on; user-reachable).**
**Status: RESOLVED + IMPLEMENTED + VALIDATED (2026-06-13) — root cause was a
skim handler driving a subwave (monopolizing the sole serial skim driver);
fix is to disallow blocking gathers from skim handlers and redirect subwork to
a funnel (preferred) or task. Non-short `-race` loop 0 hangs/0 fails / 30 with
`Inherit` on (was 3/30). See "Resolution" below; the earlier two-mechanism
sketch is superseded.**

Task #4 landed the suspend-class brackets (skim methods, block-and-help wait,
top-level dispatch episode) with help-shaped reclaim. They dissolve the
original livelock and the shared-limiter *self*-deadlock witness
(`TestSharedLimiterSelfDeadlockWitness`, a two-level same-goroutine topology).
But flipping the generator's `LimiterConfig.Inherit` on (task #5's cross-subjob
sharing) — which is what was supposed to *prove* the fix — reveals a **new,
distinct hang** the brackets do **not** dissolve. Reproduced deterministically
at `-rapid.seed=15905911232756343239` (`-short`), trace-confirmed.

This falsifies a claim made and accepted during the original review:

- **Finding 2's parenthetical is wrong.** It asserted the hold-through
  invariant — "a permit wait never occupies bounded queue capacity … this
  holds even under adversarial limiter sharing between producer and consumer
  ops." Under cross-subjob sharing it does not hold in the form claimed.
- **Finding 7's "help the subjob" reclaim domain is incomplete.** When the
  subjob being driven drains to `ErrJobDone` but the permit still cannot be
  reclaimed (a sibling/cousin holds the shared permit), the reclaiming
  goroutine has no further help domain. The committed code falls back to a
  **plain notifier wait** (`reclaimRequest`, the `helping = false` branch) —
  which is *exactly* the "plain-wait reclaim deadlocks" shape Finding 7 warned
  against, now re-emerging one level out.

### Trace evidence (the shape, not yet a fully-closed cycle)

Two permits on one `permits=1` shared scheduler were held and never released at
the hang:

- A launcher request (`c84c0`) acquired the shared permit, posted its task, and
  the permit travelled to the task body on another worker.
- That task body produced a result and **hold-through-parked on a full skim
  outbox** (`skimPostWork` → Governor downstream wait) — keeping the permit,
  per Finding 2's rule.
- Draining that outbox requires the owning pool's driver to skim; the available
  driver goroutines were themselves tied up in suspend-bracket reclaims /
  block-and-help waits contending for the *same* shared permit. No goroutine
  was free to drain, so the hold-through producer never made progress, so the
  permit never freed. A cross-pool driver-scarcity cycle closed around a single
  shared permit.

The precise minimal cycle (which pools, which drivers) was not fully closed by
hand — it spans ≥3 pools — and characterizing it exactly is the first step of
the resolution.

### What is and isn't affected

- **Not affected (ships green):** the single-limiter, non-shared case — i.e.
  every op binding its *own* limiter, including a limited body driving a
  subwave whose ops use *different* limiters. This is the dominant real-world
  shape and the original livelock target. Validated: full non-short suite green
  ×3, `-race` short green, lint 0, the self-deadlock witness green.
- **Affected (deferred):** a user sharing one `Limiter` value across a parent
  op and an op dispatched inside a subwave the parent drives. Reachable via the
  public API (`NewSemaphore` returns a shareable `Limiter`). Pre-existing in
  spirit — before the suspend work this same topology hit the *original*
  livelock — so this is the next layer of the onion, not a regression.

### Resolution (PN, 2026-06-13): disallow blocking gathers from skim handlers

The earlier "two mechanisms (queue / help-outward)" and "stacked help domains"
directions were developed before the trace was fully re-read. Re-reading it
(the dump had **no reclaim** in flight) overturned the reclaim framing: the
hang was a **driver-scarcity cycle** — a single goroutine was the sole driver
of a nested gather chain, parked in the innermost gather, while an *outer*
level needed draining to free a shared permit the inner level needed. The
goroutine got there by a **skim handler driving a subwave** (`runSubjob` from a
Skimmer body), which monopolizes the wave's sole serial skim driver.

The fix is structural and far simpler than any drain-machinery change:

**Disallow blocking gathers (`Skim`/`SkimAll`/`CloseAndSkimAll`) from inside a
skim handler.** A skim handler must not drive a subwave — it would monopolize
the sole serial skim driver and deadlock. Subwork from a skim handler goes to a
**demand-driven** consumer instead, which never has a sole driver to
monopolize:

- **Preferred — populate a Funnel from the handler.** This is the right
  primitive, not a workaround: it *is* a map-reduce (the serial populate loop
  stays in the handler where the item and logic are; Accumulate = map; Flush =
  reduce + emit; Close + return replaces the gather). Drops only the inline
  wait-for-results, which is the deadlock-prone reliance.
- **Fallback — launch a task that drives the subwave** (for a genuinely
  structured sub-wave). Tasks are demand-spawned, so no monopolization.

Enforced by a parent-chain walk at gather entry (`ctxMeta.vetNotNestedInSkim`,
using the task #2 `parent` links): panic if an enclosing context is a skim
context. Funnel/task/top-level enclosing contexts are fine — funnels and tasks
are demand-driven.

**Why this is sufficient (and the committed brackets suffice unchanged):** with
skim handlers unable to gather, the skim driver is never monopolized → the
enclosing wave is always drainable → a task/funnel body driving a subwave
suspends its permit (committed bracket), and its reclaim can plain-wait safely
because the permit's holder is drained by the enclosing wave's *own, available*
skim driver. No accept-and-defer, no help-outward, no demand-spawn-skim-driver,
no metric.

**Use-case analysis (why disallow, not just discourage):** no case was found
where delegation to a funnel/task is impossible — a skim handler returns
`error`, not data, and emits via Submit, so all of its post-gather logic moves
into the funnel/task. The only thing lost is inline wait-for-results and strict
serial ordering of sub-computations — which are exactly the reliances that
monopolize the driver and deadlock. So the patterns where inline "is needed"
are the dangerous ones; disallow is the honest, enforceable choice.

**Depth-1 invariant:** all other queue depths are effectively 1 (hold-through
pins tight backpressure). Allowing skim-gather + buffering would have carved
out the one unbounded queue in the system; disallowing it preserves the uniform
depth-1 property.

### Implemented + validated (2026-06-13)

- `ctxMeta.vetNotNestedInSkim` at `Pool.Skim`/`Pool.SkimAll`; unit test
  `TestSkimHandlerDrivingSubwavePanics`.
- Sim: skimmer Handle bodies never generate subjobs (`newFunc` `allowSubjob`
  param = false for skimmers); subjobs still under launcher/funnel bodies.
- `LimiterConfig.Inherit` flipped to 0.25 (cross-subjob sharing on).
- Sim measures the gated quantity only (accumulate concurrency) — see Finding
  11; the flush overlap that produced the spurious `2 > 1` is intended
  pipelining, not over-admission.
- Validation: full suite ×5 green; `-race` short clean (except the known
  pre-existing leakguard test race); non-short `-race` loop **0 hangs, 0 fails
  / 30** (baseline was 3/30) with `Inherit` on.

---

## Finding 11 — Limiter semantics for funnels: intake-limited, drain-side-free

**Severity: design clarification (settled the funnel-limiter meaning; shaped the
sim measurement in Finding 10).**
**Status: RESOLVED (2026-06-13).**

Working through "what does a limiter mean for an accumulator" (and a couple of
wrong turns — per-call gating of both methods; then a permit-per-instance held
for the instance lifetime) landed on a clean principle by mapping a funnel onto
the **intake/drain** split:

- **Accumulate = intake.** Bounding how much work is admitted is exactly what a
  limiter is for (same as launcher tasks). `WithLimits` on a funnel is an
  **accumulate-execution concurrency** limiter.
- **Flush = drain.** It emits the accumulated aggregate downstream ("much of
  the time just a Submit to a skimmer"). It is the funnel's output path.

This generalizes **Finding 8** (skimmers are limiter-free because gating the
drain deadlocks): **drain-side operations — skim handlers AND funnel flushes —
are limiter-free; intake operations — launcher tasks AND funnel accumulates —
carry the limiter.**

Consequences:

- **Flush is not gated by the funnel limiter**, and that is required, not just
  permitted. The reduce/pipeline proof: at limit=1, the moment an instance
  flushes, continued demand must immediately create a new instance and
  accumulate into it *concurrently with* the flush. If flush consumed the
  accumulate permit, the next accumulate would stall on every flush boundary —
  no pipeline. So flush must never hold an accumulate permit.
- **No `WithFlushLimits`** — not deferred, but unwanted on principle (limiting
  the drain is the deadlock Finding 8 prevents). The use case it might seem to
  serve — bounding expensive flush-triggered work — is served by **flushing to
  a downstream limited launcher/funnel**: the limit lives on the next *intake*
  op, via ordinary composition. (As with skimmers, a user with heavy in-handler
  work bounds it inside the body with their own primitive.)
- **No separate instance-count limiter.** Instances are created on demand by
  concurrency and pooled/reused (not per-value or per-key), so instance count
  is bounded by accumulate concurrency (~N, within a small factor under
  flush/accumulate overlap — never unbounded). Keyed aggregation *would*
  decouple instance count from concurrency and could warrant its own resource —
  future, via the scheduler/resource model (Finding 1).
- **Sim measures the gated quantity only** (accumulate concurrency). A flush
  overlapping an accumulate — including a parent op's flush overlapping a
  subjob op's accumulate on a shared limiter — is **intended pipelining**, not
  over-admission; counting both produced the spurious `observed 2 > permits 1`.

Net: one knob on a funnel (`WithLimits` = accumulate-execution concurrency);
flush and instances ride along bounded by it; drain stays free.

---

## Finding 12 — Generalize the governor: downstream blockage brakes Pool goroutine creation

**Severity: pre-existing backpressure gap (resource-stability, not correctness);
home is the Pool/FunnelPool consolidation.**
**Status: OPEN — recorded for the consolidation pass; surfaced while settling
Findings 10–11.**

**The model.** Skimmers are the system's one serialization point and therefore
the sole source of backpressure: a skim handler runs serially (single driver
unless the user opts into multiple), it's the drain, and everything upstream
postpones/blocks when a skimmer's queue fills. Everything else is *elastic* —
launchers and funnels scale by adding goroutines (and, for funnels,
instances), subject to limiters. Scaling into a skimmer bottleneck is pointless
(it just manufactures stuck/idle goroutines and partial aggregates), so the
governor's real job is a **per-wave scaling brake**: detect a *wave's*
downstream blockage and stop creating goroutines/instances for that wave's work
until it clears — leaving other waves on the same fungible pool free to scale.

**Why only half-wired today.** Flush used to implicitly emit to a skimmer, so
the one wired loop — blocked skim emit → `job.governor` → block top-level
scatter (`launcherScatterWork.Execute`, `launcher.go:374`) — covered it. In the
new model flush can emit anywhere and launcher/funnel are independent ops, so
that inheritance no longer covers funnels: `cp.governor` (the FunnelPool's) is
incremented (`funnelPostWork.Waiting`, `funnelpool.go:260`) but **never
consulted** — no `cp.governor.Execute` exists, and `maybeSpawn` ignores it. So
funnel elasticity scales regardless of downstream blockage. Matters most for
*unlimited* ops (a limiter already caps scaling; an unlimited funnel feeding a
stalled skimmer scales unbounded).

**The lever — a per-wave admission gate, NOT a pool spawn-brake.** The gate sits
on the **wave's launch point** (`Start`/`Submit`), keyed by that wave's
downstream saturation, *independent of pool goroutine availability*: while a
wave's downstream skim is blocked, that wave admits no new work; other waves on
the same fungible pool are unaffected. It cannot be a spawn-brake, because the
pool is fungible:

- goroutines grown for a *healthy* wave will pick up a *saturated* wave's
  already-admitted work — so braking goroutine *creation* doesn't brake the
  saturated wave; and
- conversely the saturated wave must not launch into those goroutines at all.

The right shape already exists: `launcherScatterWork.Execute` gating the scatter
on a governor *is* a launch-gate. The generalization is only (1) make the flag
**per-wave** (Wave-owned, not per-pool `job.governor`), and (2) apply the same
gate to **funnel intake** (`Submit`), which has none today. Spawn decisions
(`trySpawnTaskWorker`/`maybeSpawn`) stay pure demand-driven — not a backpressure
point.

What the single per-wave gate bounds:

- *The saturated wave's elasticity* — directly (no new admission → in-flight
  drains → skimmer catches up → unblocks).
- *Goroutines* — for free: a gated wave generates no demand, so the pool spawns
  nothing for it; goroutine count tracks aggregate *healthy*-wave demand. (So no
  separate spawn-brake — it was neither necessary nor sufficient.)
- *Intake + memory* — existing queue-full backpressure (bounded rdvq outboxes).
- *Instances/partial-aggregate memory* — for free: instances are
  concurrency-demanded, and a gated wave generates no accumulate demand (closes
  Finding 11's memory question — no instance limiter even for unlimited ops).

**Deadlock-free** because the skimmer always drains — guaranteed by Finding 10
(a skimmer's sole serial driver can't be monopolized, since skim handlers can't
gather) — so the gate always clears; it pauses admission, never strands it.

**Why per-wave.** The destination is one fungible pool serving many waves; a
pool-wide gate would throttle healthy waves. Invisible today only because
`NewWave` mints its own pool (pool ≈ wave). The flag belongs with **Wave**,
alongside the flush/drain ownership the plan already migrates Pool → Wave.

**Home / shape.** Pool/FunnelPool consolidation: one fungible pool, per-wave
downstream-blocked flags (Wave-owned), a launch gate on `Start`/`Submit` that
pauses only saturated waves — dissolving today's `job.governor`-read-by-launchers
+ `cp.governor`-write-only split.
