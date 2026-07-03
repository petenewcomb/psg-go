# PSG-Go Combiner Branch Working Notes

This document contains working notes and context for development on the `combiner` branch.

**►►► WEIGHTED ACQUISITION — design agreed, recorded (2026-07-02), NOT implemented.**
`docs/decisions/weighted-acquisition.md` (companion to limiter-resource-classes.md): counts layout
is weight-ready, ops are weight-1. Core: (1) gather-into-own-`held` + atomic occupy — a partial
gather is NOT hold-and-wait (hoard stays borrowable ⇒ "parked ⟹ borrowable" proof intact;
cache-don't-return IS the rollback, no give-back protocol); (2) demand-side head-of-line barrier
(PN): Pool-level FIFO of caller-held invalidatable demand identities, **sticky head, FIFO
succession, NO weight-based ordering** (max succession rejected — biases toward large demands);
gathering is HEAD-ONLY ⇒ gather-vs-gather livelock unrepresentable; barrier must gate steps 1–4
incl. acquireLocal (one atomic load, mirror of the release-side balance load) else step-1
recirculation starves the head invisibly; (3) arm only w≥2 — weight-1 never registers, mechanism
dormant for semaphore/rate pools; (4) identity = conservation token (satisfied-or-invalidated;
caller-held to dedupe postpone retries; gen-stamp for ABA). Supply-side reservation (x/sync-style)
rejected — breaks the liveness proof; demand-side barrier reaches the same fairness without it.
Weighing SURFACE already settled (dispatch-execution-split.md: static per-op panic / data-dependent
per-item unit error; "applicant" backlog note). Needs: TryAcquireUpTo capability (partial grants),
capacity visibility (infeasibility BEFORE arming), stealOutUpTo. Sequencing: mechanical w=1-caller
weighting (no-op, green) → gather+barrier behind model check → resource capabilities → surface
plumbing (own session). **SURFACE SETTLED (PN, 2026-07-02, recorded in the doc):** variadic builder
methods on the op type — `Launcher[T].WithLimits(...Limiter)` / `.WithWeightLimits(...WeightLimiter[T])`
+ `NewWeightLimiter[T](l, weigh)` constructor (New* consistency; Limiter→Limit general rename
REJECTED — "limit" = numeric ceiling throughout the package, type would collide; compile-time T via
receiver's param; `With` prefix = http.Request.WithContext copy-semantics convention;
WeightLimiter[T] = reusable same-T binding;
variadic slices stack-allocate IFF methods copy-out-never-retain — verified 0 allocs incl. multi-arg;
Funnel adds WithFlushLimits). Both methods compose + repeated calls ACCUMULATE (variadic = pure
sugar; enables base-op layering); one binding per limiter TOTAL across both methods (dup panics);
replace/last-wins + removal affordances REJECTED (silent constraint-dropping). Multi-limiter
representation (PN, final): every With* call COPIES into op-owned storage (1 construction alloc per
call) — adopt-the-variadic REJECTED (spread caller `WithLimits(mySlice...)` aliases; later element
mutation = silent constraint modification, same class as replace/last-wins; doc-only adoption too
weak for a limiting API); fixed inline array REJECTED (caps count, bloats every op-value copy).
Mitigations: single-limiter cut = plain fields, still 0 allocs; multi-limiter needs owned storage
ANYWAY (canonical-order sort + dup scan at bind time = the copy is canonicalization, not defense).
Accumulation copy-merges, never appends (backing shared among op value copies). **SETS (PN, 2026-07-02):**
ONE limiter type — AmountLimiter kind-split REJECTED (PN: no reason TO do it; the rationale offered
for it — type-guarding weight-blind amount binding — was invalid: unit coherence isn't
type-checkable). T/U weigher-op pairing stays unrepresentable via WeightLimiter[T]→same-T methods
only, no boxing, sets carry no weighers. PN sweep-ratifications (2026-07-02): every op gets the
FULL complement of the 4 binding methods; FLUSH LIMITERS DROPPED ENTIRELY (PN, supersedes his
earlier incl-flush answer — a flush needing limits attaches them to a launcher invoked FROM the
flush; kills the WithFlush* surface, the flush-weigher-arg question, C3's WithFlushLimits, and
permit-core's limited-flush model-check case; SKIMMER drain limiting STAYS — PN: a skim handler has
something to weigh [typed result], isn't pre-committed by an upstream limited op [flush only drains
what limited accumulates admitted], and runs in the user's context [a launched body wouldn't] —
permit-core's limited-drain model-check case remains, skim-scoped); weigher <0 panics
but ==0 VALID = nothing acquired, binding skipped that dispatch; weigh-once-per-dispatch confirmed;
opoption deletion confirmed; Limit-rename rejection confirmed. Construction-time static-infeasibility panic
DROPPED entirely (PN never wanted it — I had misread his sweep answer as keep-it; strike the static
branch from dispatch-execution-split.md "Infeasible demand" at implementation); ALL weighted
infeasibility = runtime distinct per-unit error, enforced at demand registration (before barrier
arming). Reusable canonicalized
sets: untyped `LimiterSet` (universal) +
`WeightLimiterSet[T]` (same-T, T inferred from members) — one-shot homogeneous variadic ctors;
single MIXED set REJECTED (no T witness / heterogeneous variadic untypable / boxing = T/U). Set
binding methods are SINGULAR (`WithLimiterSet(s)` — a set IS the bunch; multi-set = repeated calls
per accumulation law). Pure-set op = zero per-op alloc (shares frozen state); customizing op = one
bind-time merge. Dup panic spans all four methods + sets. NO static-weight form — always a function (constant closure covers the rare
case); deliberately retires the construction-time static-infeasibility panic (was advisory anyway —
capacity is dynamic) → all weighted infeasibility = per-unit distinct error at dispatch. opoption.go
DISSOLVES (WithLimits was the only OpOption; constructors drop opts). Gotchas: COW the bindings
slice (diverging-chains aliasing; needs dedicated test); weigh runs ONCE per dispatch on the
dispatching goroutine, stamped int, stable across postpone retries + demand identity; weigh<1 panics
(weigher bug), oversize-vs-capacity errors (data).

**►►► LIMITER RESOURCE CLASSES — design agreed, recorded (2026-07-02), NOT implemented.**
`docs/decisions/limiter-resource-classes.md`: the permit forest's premises (cache-don't-return,
inheritance, steal) hold only for conserved holdable permits — rate limiters break conservation,
external gauges break revalidation. Design: base `Resource{TryAcquire}` + `HoldableResource{+Release}`
discovered by ONE type assertion at NewPool (nil-field test on hot paths; whole policy bundle —
caching forest vs pass-through, postpone charge-rides vs release-and-reacquire, park/resume
alternation vs no-op — keys off it). Consumables = degenerate forest (no caches; every acquire is a
fresh step-3 TryAcquire). Wake: resource-owned production (timers arm lazily on failed TryAcquire),
Pool-supplied surface = ONE signed verb `Adjust(delta)` posting to a signed atomic `balance` (PN's
counter model — the execpool demand-counter pattern applied to wakes; superseded the counted-fan-out
Notify(n) and fit-matched per-waiter-demand drafts, both now in Rejected). Positive: serialized wake
chain (≤1 wake in flight; step-3 success decrements by amount + ALWAYS forwards one probe → unknown-
size events = Adjust(1); failure STOPS the chain but does NOT clamp — balance is the resource's delta
ledger, pool transacts-never-rewrites [clamp ⇒ phantom debt on +5/raced/−5 netting]; ⇒ wake seeds are
per-positive-Adjust events, NOT zero-crossing edges, else residue masks fresh posts; register-then-
check closes the missed-wake race; positive side = lossy hint / negative side = exact, drift bounded,
reconciliation-read is the seam if measured material). Negative (holdable-only, panics for consumable) =
reclaim debt subsuming Reclaim(n): immediate idle harvest (steal-with-Resource-as-sink; lazy debt
would strand vs event-less cached idle) + releases pay debt before caching (targeted suspension of
cache-don't-return; one atomic load on the release hot path) + repayment = ordinary resource.Release
calls. **No WakeAll anywhere** — even destroy-drain posts to the balance. Verb name still open
(Adjust vs Offer/Credit). `Reclaim(n)` = the
shrink-direction dual for holdables (memory/GC drift): a steal whose beneficiary is the Resource,
reusing the LRU walk + revalidating CAS; idle-only (recall-of-in-use stays rejected); also sharpens
SetMaxConcurrency lowering. Joint admission: holdables before consumables in the canonical order.
Mixed semantics = WithLimits composition, no third class.

**►►► `RenotifyFunc` → `Notification` LANDED + COMMITTED (`eee5322`, 2026-07-01) — the
conservation-discharge refactor (pickup #1). Gate green: full `-short` suite; rdvq `-race`
(saturation, 230s); 25×80-check `TestBySimulation -race` batch 25/25.** The bare
`RenotifyFunc func()` threaded through the block/wait paths is now a value struct
`rdvq.Notification{n *Notifier, fallback func()}` with `Empty`/`Consume`/`Forward`. Spec +
rationale: `docs/decisions/waiter-set-notification.md` (Status → "Landed"). Key points:
- **`wrappedRenotify` + its pool deleted.** The wrapper existed only to give listeners a
  renotify that re-circulates through the origin `Notifier`; that identity is now the zero-alloc
  `n *Notifier` field, carried by value. Leak-on-discard gone *by construction*.
- **`Notify` is total.** `Notifier`/`Waiters`/`Listeners` `.Notify(fallback)` run the fallback
  when no consumer takes the wake. Consumer loops: `renotifyFn != nil {renotifyFn()}` →
  `!m.Empty() {m.Forward()}`; productive use → `m.Consume()` (no-op intent marker). Forward is
  listener-recirculate (`n` set) vs waiter-terminal (`n` nil).
- **DECISION (PN):** the two *unguarded* `Waiters.Notify(unmetDemandFn)` sites (`queueFresh`,
  `Expedite`) go total too — they now `Nudge`-spawn on a no-parked-worker miss instead of
  dropping the signal. Safe: `Nudge` is `spawnConcurrencyLimit`-capped + self-correcting; the
  extra goroutines are warranted unmet-demand parallelism (the buffered-1→unbuffered shift). The
  spec's "accepted.go ×3" was a miscount (2 guarded sites); corrected in the spec.
- **`unmetDemandFn` split** from the threaded value: it is a `func()` fallback (role a), not a
  `Notification` (role b). `q.listener.Notify = q.waiters.Deliver` (new exported re-injection
  primitive) replaces the old `= q.waiters.Notify`.
- **"Pool the fallback" is out of scope** (PN confirmed): every fallback is `noop` or a
  once-cached method value (`unmetDemandFn` = `ensureWorker`), never a per-call closure. A
  fallback that ever needs per-call state binds as a method value on a lifecycle-pooled object
  (cf. `heldPermit.release`/`confirmFn`), NOT a self-returning pooled closure. `rdvq.NewNotification`
  is the seam for a producer that mints a terminal wake outside a `Notifier` (today: one workq test).
- **Design nit noted for later:** cached-bound-method may be over-used vs. an interface where the
  receiver is already a pooled pointer (alloc-free conversion). Own pass, not blocking.

**►►► DESIGN B chosen for the scheduled-flush deadline timer (PN, 2026-06-29). Replaces the
per-worker idle-suppression (DECISION B in scheduler.pull).** Goal: workers scale fully to zero; a
SINGLE scheduler-owned timer honors pending flush deadlines by waking/spawning a worker. Concrete plan
(designed against delayq.go + accepted.go):
- **delayq:** add `func (q *Queue[T]) NextDeadline() time.Time { return timeFromNanos(q.nextDeadline.Load()) }`
  (authoritative earliest, lock-free atomic read).
- **Accepted owns the timer** (`schedTimer *time.Timer` + `schedTimerMu` + `schedArmed time.Time`):
  - `armScheduledTimer(d)`: **only LOWERS** (mirrors delayq.lowerDeadline) — `if !schedArmed.IsZero()
    && !d.Before(schedArmed) { return }`; else Reset to `time.Until(d)` (clamp ≥0), set schedArmed=d.
    Zero d ⇒ Stop + clear. THE SUBTLE POINT: arm-only-lowers is REQUIRED — a naive "arm to exact each
    time" races a concurrent Schedule (a drain computing next=T2 can overwrite a concurrent
    Schedule's sooner T0 → missed wake). Sooner always wins; later re-arms only after the timer fires
    (schedArmed cleared on fire) or post-drain.
  - fired callback: clear schedArmed, then `if !waiters.Notify(nil) && unmetDemandFn != nil {
    unmetDemandFn() }` (wake a parked worker to re-drive→drainScheduled, else Nudge-spawn one).
  - Arm sites: `wakeScheduled` (delayq wake on lowering — read NextDeadline, arm) AND end of
    `drainScheduled` (re-arm for the returned next, advancing/clearing after a drain). Both inert for
    the per-wave workQueue (waves never Schedule → NextDeadline always zero; unmetDemandFn nil).
- **scheduler.pull:** delete the DECISION B block (`if deadlineCh != nil { idleCh = nil }`) so workers
  idle-exit freely.
- **WaitForNew:** stop arming the per-worker timer (the `timerp` block) — pass deadlineCh=nil; the
  Accepted timer handles wakes now. Strip the now-vestigial `deadlineCh` from the AddWorkFunc
  signature / scheduler.pull / wave addWorkFn / controller armedDeadline logic as a follow-up
  (gut-first: pass nil, leave the dead nil-channel select case, strip later).
- Validate: large -race TestBySimulation batch (the failure mode is a MISSED/DELAYED wake — subtle,
  may not trip -race; add targeted assertions or a flush-latency check). Do BEFORE C2 benchmarks
  (worker count / scale-to-zero is what the methodology measures).

**►►► DESIGN B IMPLEMENTED (2026-06-29) — build/vet/sim green; -race batch RUNNING.** Landed a
CLEANER formulation than the plan above: `armScheduledTimer` reads the AUTHORITATIVE earliest from
`delayq.NextDeadline()` (new lock-free atomic accessor) *under its own `schedTimerMu`* and Resets to
it — so the last of any racing arms always reflects the true earliest. No "arm-only-lowers"
bookkeeping, no `schedArmed` field. The concurrent-Schedule-vs-drain race that "arm-only-lowers" was
meant to fix is handled structurally: an arm triggered by a stale event still reads the *current*
atomic inside the lock. Edits:
- `delayq.NextDeadline()` — lock-free read of the next-deadline atomic (the single source of truth).
- `Accepted`: `schedTimerMu`/`schedTimer` fields; `armScheduledTimer()` (read-atomic-under-lock →
  Reset/Stop, lazy `time.AfterFunc`); `scheduledDeadlineFired()` (`Notify(unmetDemandFn)`-or-Nudge,
  mirrors `ForceFresh`). Armed from `wakeScheduled` (delayq lowering hook) + end of `drainScheduled`.
- Removed: per-worker timer in `WaitForNew` (passes nil deadlineCh); the `shouldStillWait`
  armed-deadline abort; controller `nextDeadline`/`armedDeadline` fields; `timerp` import; the
  DECISION B idle-exit suppression in `scheduler.pull`. Workers now scale fully to zero.
- Robustness: the first schedule always arms (any real deadline < `noDeadline`), so no missed flush.
  Out-of-band removals (ClaimForFlush/Reschedule-later don't fire the lowering wake) leave the timer
  on a stale-early deadline → a spurious early fire → drain finds nothing due → re-arms. Self-
  correcting, never a missed/late flush. A *missed* flush would hang TestBySimulation (caught).
- `deadlineCh` is now vestigial (always nil) through AddWorkFunc/scheduler.pull/wave addWorkFn — strip
  in a follow-up (gut-first).

**►►► CUTOVER FULLY SCOPED — FORK A (`combiner`, 2026-06-28e).** Deep read of the whole
dispatch surface (pool.go, ctxmeta.go, wave.go, launcher.go, limiter.go, funnel.go) settled the
design. Key facts the 2-line plan banner missed:
- **The existing design ALREADY splits admission from bodies.** Per-wave `workQueue` (`workq.Accepted`,
  `Init(nil)`) + `skimQueue` (`Pending`) run *admission* (the scatter-works) INLINE on the user /
  skim goroutines (`topLevelExEnv.ExecuteNowOrQueue` → `wave.workQueue`; skim drives `workQueue.
  ExecuteOne`). Only the BODY goes to the global `defaultPool` (`*PostWork.Execute → defaultPool.
  Post`). So C2 is NOT "rebuild dispatch" — it is "move the body off `defaultPool` onto an executor,
  and move NESTED admission off the body goroutine onto a scheduler."
- **THREE blocking body types** (each runs user code; each must run on the executor, never pin a
  scheduler): (1) task — `taskWork`; (2) funnel-accumulate — `funnelWork[T]`; (3) funnel-flush —
  `funnelInstance.Execute` (the user `Flush` handler), `ForceFresh`'d into the pool by `sweepFlush`/
  deadline-drain.
- **Admission chains differ (must unify).** Task: `launcherScatterWork`(governor) → `limiterScatterWork`
  (permit) → `taskPostWork`(post) → body. The body (`taskWork`) is already a pure body (no gate).
  Funnel: `funnelPostWork`(governor via onWait/`Waiting`) → post; **the permit gate is INSIDE
  `funnelWork.Execute`** (`gateAcquire`) — Wrinkle 1. Funnel-flush: bare `funnelInstance` (no
  decorator). Unify by reusing `limiterScatterWork`/`launcherScatterWork` around the funnel post-works,
  matching the task order **governor OUTSIDE permit** (a prior gate-hoist hit governor-ordering — the
  inversion is the trap).

**FORK A (chosen, see DECISIONS) — minimal, semantics-preserving:**
- **Bodies → `bodyExecutor` (`execpool.Executor[*workerExEnv]`).** Each body type gets `Run(ee)` (=
  `run(ee)` + self-`Free`, already 90% there) and a scheduler-side post-work whose `.Execute` does
  `bodyExecutor.PushBack(body)` + `ex.Starting()` + null-out (the EXISTING `wk.task=nil` ownership
  transfer; the controller `Free`s the post-work, the executor owns+frees the body). NO `HandedOff`.
- **Top-level admission stays inline** on `wave.workQueue` (user/skim goroutine) — preserves producer
  backpressure; the `PushBack` blocks the producer (safe: never an executor goroutine).
- **Nested admission (from a body on an executor) must NOT run inline** (its `PushBack` would block the
  executor waiting for an executor ⇒ deadlock). `workerExEnv.ExecuteNowOrQueue` drops the scatter-work
  to the scheduler non-blocking (push fresh + `Nudge`); a scheduler worker runs admission + the
  blocking `PushBack`. This is the deadlock-avoidance the split exists for.
- **`defaultPool` → `workq.Scheduler`** (drives nested admission + postponed + scheduled-flush);
  `streampool.Wait` reaps BOTH pools. The scaffold's `incoming` Handoff + `Scheduler.Post` are UNUSED
  in Fork A (top-level is inline, not Handoff-posted) — leave vestigial, remove later.
- Funnel-gate hoist: wrap `funnelPostWork` with `limiterScatterWork`(permit) [+ governor wrapper to
  keep governor-outside-permit]; remove `gateAcquire` from `funnelWork.Execute`; permit released at
  body end / `Free` (already idempotent). Funnel-flush: `funnelInstance.Run(ee)` + a flush post-work
  that `PushBack`s it; `sweepFlush`/deadline `ForceFresh` the post-work.

**DECISIONS (asked PN 2026-06-28e):**
- **D1 topology:** Fork A (top-level inline, `incoming` unused) vs Fork B (route top-level through the
  scheduler `incoming` Handoff — the earlier written plan; relaxes producer backpressure). → recommend A.
- **D2 funnel-flush:** flush body → executor too (honors always-live-scheduler) vs run on scheduler for
  the first cut (simpler, but a blocking `Flush` pins a scheduler). → recommend executor. **PN chose
  executor; DEFERRED to a follow-up CP after closer reading.** Reason: `funnelInstance.Execute`'s flush
  is synchronous under `c.mu`, and the per-instance wave barrier (`DecrementReference`) MUST drop
  *after* the user `Flush` (so a downstream `Submit` in `Flush` takes its ref before this one drops —
  else `totalReferences` transiently hits zero ⇒ premature wave Done = the leak class of the prior
  hang). Splitting that across the scheduler→executor handoff opens a new R1/R2 concurrency window in
  the delicate instance lifecycle and is too risky to bundle into the first cut. For CP-B1 the
  deadline/sweep flush stays on the scheduler (its nested submits drop to the scheduler non-blocking →
  no deadlock; it only *pins* a scheduler worker during a blocking `Flush`, which is bounded — flushes
  are rare vs accumulates). The already-past-deadline INLINE flush in `accumulate` already runs on the
  executor. Move `funnelInstance.Execute`→executor in a dedicated follow-up (CP-B1b).

**CP SEQUENCING (revised this session):**
- **CP-B1 (in progress):** bodies (task + funnel-accumulate) → `bodyExecutor`; nested admission →
  scheduler via `ForceFresh`; `defaultPool` STAYS `worker.Pool` (proven scheduler); funnel-flush stays
  on scheduler (D2 deferred). Isolates the body/executor split + deadlock-avoidance from the
  scheduler-swap. Validate large `-race` batch before commit.
- **CP-B1b:** `funnelInstance.Execute` flush → executor (the deferred D2), with the instance-split
  designed carefully.
- **CP-B2:** swap `defaultPool` `worker.Pool` → `workq.Scheduler` (DONE, commit 92b5f48); then delete
  `internal/worker` + the dead `workq.Queue`/`Worker` scaffold + strip vestigial `deadlineCh` (DONE,
  this cleanup).

**►►► RDVQ INBOX CLUSTER — remaining ~10/14 allocs/op (2026-06-29, pursuing option 3).** After the
meta pooling, BenchmarkLauncherSkim's remaining 14 allocs/op are ~99% waiter inboxes. Root cause:
`Waiters.WaitFunc` (rdvq/waiters.go:85) borrows a fresh inbox from `inboxPool` per call and reclaims it
ONLY when `clean` (PopFrontFunc received a value on the waiter channel). In the steady-state skim the
work arrives via the skimQueue's OWN inbox (a different channel), so the registered waiter is never
satisfied → PopFrontFunc marks it ABANDONED (pushes a zero-value marker, leaves ib in the emptyInboxes
collection) → clean=false → NOT reclaimed. The abandoned inbox is later popped by a sender (TryPushBack),
which drains the marker and DISCARDS it (→ GC). So one inbox struct + make(chan,1) leaks per skim.
Path: Wave.Skim → addWork → skimQueue.PopFrontFunc → workWaiters.WaitFunc (workWaiters = the Accepted's
q.waiters). Profile: rdvq.inbox[func()].Init 42% + inbox-pool struct Get + nbcq nodes.
- **ATTEMPTED option 3 (naive sender-reclaim) — FAILED, REVERTED.** Made `inboxOnlyQueue.TryPushBack`'s
  marker-drain branch reclaim the inbox. WRONG: an abandoned inbox is STILL OWNED by its receiver.
  `Queue.PopFrontFunc` (queue.go:369-371) explicitly HOLDS its inbox across retry iterations and RE-PASSES
  it, "preserving the reuse-without-requeue path for its own abandonment marker"; `Handoff.PopFrontFunc`
  borrows-per-call and DROPS the abandoned inbox to GC by design ("a later sender's TryPushBack drains
  [the marker]"). So the sender draining the marker does NOT have exclusive ownership — reclaiming steals
  an inbox the receiver will re-pass (or that must stay GC-owned), giving two receivers one inbox →
  lost-wakeup/double-receive. `saturation_test` (Queue, 8 prod × 8 drain × 40k) HUNG (125s). The
  "drop-and-let-GC" of abandoned inboxes is load-bearing, not an oversight.
- **CORRECT option 3 requires a GENERATION-STAMPED inbox** (the protocol the OUTBOX already has —
  queue.go:29/189: a stale reclaimed-and-reused outbox fails its CAS and is dropped). The inbox has none,
  so reclaim-and-reuse can't be disambiguated from a concurrent re-pass. Adding gen-stamping to the inbox
  is a change to the hairiest lock-free code in rdvq → needs the model-check + large -race treatment, a
  dedicated effort.
  - Rejected option 1 (thread a caller-held inbox through Waiters→workq→wave): leaks rdvq complexity into
    the callers + needs a holder that outlives a single drive (nothing does on the consumer side).
  - Rejected option 2 (check work before registering): reintroduces the missed-notification race that the
    register-then-confirm order exists to close.
- STATUS: 38→14 alloc reduction (meta pooling) stands, committed (50bf217).
- **GEN-STAMPED INBOX — design committed (4cb85b6, docs/rdvq-inbox-reclamation.md); prototype VALIDATED.**
  Design: 3-state gen-stamped inbox (free/waiting/delivering) with sole-receiver reclaim (senders only
  claimDeliver-or-skip, never reclaim) + a SINGLE gen bump on abandon (the only transition that disowns a
  registration a sender may have observed). Reference counting rejected (still needs a gen for ABA across
  pool reuse; doesn't evict the lingering hint; no multi-party reclaim to coordinate).
  - **DONE + VALIDATED (2026-06-29).** Live `inbox.go` (gen-stamped 3-state machine) + `inboxonly.go`
    (`TryPushBack` = claimDeliver-or-skip, no marker; `PopFrontFunc` register/abandon(+gen)/orphan-drain,
    always leaves the inbox free → callers Waiters/Handoff/Queue reclaim on EVERY PopFrontFunc, recycling
    abandoned inboxes). **CRUCIAL: captured-gen hints.** emptyInboxes holds `inboxHint{ib,gen}` (not a bare
    *inbox); a sender claims at the hint's CAPTURED gen. This is what makes the SHARED omnipool.For[inbox[T]]
    pool cross-queue-safe: an inbox abandoned in queue A (gen bumped) and reused in queue B via the shared
    pool leaves A's stale hint claiming at the old gen → fails, so A doesn't misdeliver into B's receiver.
    - **The bug this fixed:** my first cut claimed at the CURRENT gen → cross-queue misdelivery (a skimWork
      from a wave's skimQueue ran on a scheduler worker with a bare ctx → "Context not associated with a
      wave" panic). Diagnosed via stash-baseline (confirmed my change), then root-caused to the shared
      inbox[Work] pool + reclaim-of-abandoned + stale hint. Per-queue pool also fixes it but loses
      cross-queue reuse; PN directed the shared-pool fix → captured-gen hints (mirrors outboxHint).
    - **Prototype** (`inboxpool_proto_test.go`): TestInboxReclaim_Race (2 queues sharing 1 pool, churning
      receivers, per-queue value-range ownership) + TestInboxCapturedGenStaleHintInert (DETERMINISTIC
      cross-queue guard — orchestrates abandon-in-A/reuse-in-B/A-stale-hint; FAILS if trySend claims at
      current gen). Note: the stress test alone can't reproduce cross-queue (sync.Pool P-affinity), hence
      the deterministic guard.
    - **Gate MET:** full suite green; rdvq -race incl saturation_test; 25/25 TestBySimulation -race;
      **BenchmarkLauncherSkim 38 → 5 allocs/op** (meta pooling 38→14, gen-stamped inbox 14→5). NOT committed.
  - **Benchmarks (PN asks):** rdvq Queue benchmarks performance-NEUTRAL (EmitVsChan direct-handoff within
    noise; OutboxHintCycle/ChanCycle controls flat). NEW `BenchmarkHandoffVsChan` (handoff_bench_test.go):
    Handoff vs unbuffered chan, conc sweep — chan is ~2-4x faster for pure rendezvous (0 allocs both; the
    gap is Handoff's multi-step lock-free protocol vs the runtime's direct chan handoff). Handoff's cost
    buys its composable park (block-as-demand spawn, idle/ctx selectFn seam, LIFO scale-to-zero); ~1µs/
    handoff is negligible for the executor's blocking bodies.

**►►► DISPATCH ALLOC REDUCTION — top-level meta pooling (2026-06-29, in progress).** The bench
comparison showed streampool ~37 allocs/task vs naive-pool's 1; root-caused via `BenchmarkLauncherSkim`
(the main-module hot-path guard = 38 allocs/op, single-threaded Submit+Skim, no limiter — so it's CORE,
not the harness). Memprofile (`-memprofilerate=1`) attribution, three clusters:
1. **top-level/skim ctxMeta machinery (~65%)** — every top-level Submit/Skim from a bare ctx minted a
   fresh `&ctxMeta` + `&topLevelExEnv` + a ctxpool child (`newChildPool`+`AfterFunc`+`WithValue`+`&child`),
   none recycled. The body-meta path IS pooled (`bodyMetaPool`/`releaseBodyContext`); the top-level path
   had no matching free. (launcher.go:202 comment already half-knew: it roots the BODY at the stable
   caller ctx to dodge the per-dispatch child, but left the meta-stamped ctx itself allocating.)
2. **rdvq handoff inbox (~30%)** — `rdvq.inbox[func()].Init` + nbcq + omnipool Gets per dispatch (the
   executor handoff isn't recycling inboxes). C2-introduced. DEFERRED.
3. exEnv stack slice growth (`PushQueueFunc`/`PushGroup`). minor.
- **DONE: top-level SUBMIT + SKIM meta pooling — BenchmarkLauncherSkim 38 → 14 allocs/op** (2178 →
  645 B/op). Submit path validated 25/25 -race; combined (submit+skim) -race batch running. The remaining
  14 allocs are ~99% the rdvq-inbox cluster (the skim-queue handoff `inbox[func()].Init` + nbcq nodes) —
  the deferred follow-on; the meta machinery itself now contributes ~0.
  - SKIM path: `skimCtxMeta` now returns the `owned` signal; the 6 wave.go skim drivers (Skim, yield,
    block, TrySkim, SkimAll, skimAll) `defer releaseTopLevelContext` when owned. Handles the skim
    derivation CHAIN (bare-ctx skim mints top-level metaB + skim metaC; yield reuses the dispatch's
    top-level meta and mints only metaC) and the SHARED exEnv via three ctxMeta fields: `selfCtx` (child
    to free), `ownsExEnv` (free exEnv only where allocated — metaC reuses metaB's), `releaseParent`
    (bounded walk: free metaB too iff this call minted it; stop at a reused-ambient boundary so yield
    never frees the dispatch's meta). Nested skim (SkimAll→skimAll, yield/block on an already-skim ctx)
    reuses the ambient skim meta → owned=false → no double-release.
  - ESCAPE-SAFETY (skim ctx is the ambient root for handler-launched async work): SAFE because ctxpool is
    nearest-child-wins + structural cancellation — a handler-launched body resolves its OWN nearest meta,
    never the recycled skim meta's value; recycling swaps only the childKey value, not the cancellation
    chain. The launcher.go:202 comment is about reuse EFFICIENCY, not safety. Proven by the -race/sim
    batch (exercises skim + nested subwaves + handlers launching work).
- **(earlier sub-step) top-level SUBMIT meta pooling.** `ensureCtxMeta` draws the meta from `bodyMetaPool`
  (zeroed on Put → `held` nil as required); `topLevelExEnvPool = omnipool.For[topLevelExEnv]()` with a
  `Reset()` (clears workQueue+stacks, NOT the mutex — avoids copylock + keeps stack cap);
  `topLevelCtxMeta` returns an `owned` bool; `releaseTopLevelContext(ctx)` (mirrors releaseBodyContext)
  returns exEnv+meta to pools and `ctxpool.Free`s the child. Wired ONLY into the Launcher path
  (`vetStart`→`dispatch`, `defer releaseTopLevelContext` after `meta.Unlock`), which is provably safe: the
  body is rooted at `srcCtx`, so the meta-stamped ctx is used only for synchronous admission and never
  escapes (confirmed: postpone re-queues the work item, which carries the body ctx, not the meta ctx).
- **Result: BenchmarkLauncherSkim 38 → 32 allocs/op** (2178 → 1698 B/op). Suite green; -race batch running.
- **WHY ONLY 6:** Skim shares the same pools but doesn't release (its nested skimCtxMeta derivation, where
  the expensive per-call `newChildPool`/`AfterFunc` lives, is DEFERRED) — so skim DRAINS the shared meta+
  exEnv pools, masking part of the submit win. The two meta paths are coupled through the shared pools;
  the full payoff needs the skim path pooled too.
- **NOT safe to extend blindly:** Funnel/Skimmer submit borrow the body FROM the meta-stamped ctx (unlike
  Launcher), so freeing their meta needs the borrow-source fix (root body at srcCtx) FIRST. Skim's nested
  derivation needs the parent-chain liveness handled (free the whole owned chain together; don't free a
  meta still referenced via another meta's `parent`).
- **NEXT increments:** (a) skim-path meta pooling (biggest payoff: kills per-call newChildPool/AfterFunc +
  stops draining the shared pools); (b) funnel/skimmer submit (after rooting their bodies at srcCtx);
  (c) the rdvq inbox cluster. NOT committed (awaiting PN + -race green).

**►►► C2 BENCHMARKS — bench/ comparison submodule created (2026-06-29).** New isolated module
`github.com/petenewcomb/streampool/bench` (own go.mod, `replace ../`) for head-to-head comparisons vs
other frameworks — keeps their deps/licenses out of the root module (mirrors otpsg). Rationale (from PN's
"consider what competitors benchmark"): Go pool libs (ants/pond/tunny) compete on throughput + memory +
peak-goroutines; NONE benchmark tail-latency-under-blocking, which is exactly the split's moat. So the
harness reports BOTH turfs through one `dispatcher` interface (start/submit/drain/stop):
- competitors' turf: tasks/sec, allocs/task, B/task, peak-goroutines.
- our turf: p50/p99/p99.9 dispatch (enqueue→body-start) + e2e (enqueue→body-done) latency, heavy-tailed
  lognormal blocking work, swept P:D (underload→heavy-overload).
- Lineup: unbounded (explosion control), chan-semaphore, naive-pool (the "dispatcher-pinned/no-split"
  control), streampool. tdigest for streaming quantiles; sharded recorder; fixed warmup+window;
  `-benchtime=1x`.
- **First validated result** (heavytail/balanced/P=D=8): streampool matches/beats the bounded baselines'
  e2e tail (12.2ms p99) while bounding goroutines, at ~37 allocs/task vs naive-pool's 1; unbounded blows
  to 133k goroutines. Green: gofmt + vet + lint(0) + compiles.
- **KNOWN LIMITATION (documented in bench/README.md):** the flat independent-blocking-task workload is
  throughput-bound by D, so all bounded systems converge — it does NOT yet isolate the split's
  responsiveness edge. NEXT: (1) nested-dispatch workload (naive pool DEADLOCKS, streampool doesn't);
  (2) funnel+flush (exercises CP-B1b); (3) mixed latency-probe + heavy bodies; (4) ants/pond/conc in the
  lineup. NOT yet committed (awaiting PN).

**►►► CP-B1b DONE (2026-06-29) — funnel-flush body → executor (the deferred D2). build/vet/suite green;
-race batch confirming.** A blocking user `Flush` no longer pins a scheduler — the always-live-dispatcher
invariant now holds for *every* user body (task, funnel-accumulate, funnel-flush). The split, collapsed
onto `funnelInstance` itself (no new object — the instance is already the scheduled `Work` *and* now the
`execpool.Task`):
- `funnelInstance.Execute` (scheduler side, driven by a drained deadline or the sweep's ForceFresh) is now
  pure admission: it hands the flush body to `bodyExecutor` — `TryPushBack` first, then (only when the
  scheduler worker parks, via `shouldStillWait` with `ShouldBlockOrPostpone`) a blocking `PushBack`.
  No permit gate (flush is unlimited). `ex.Starting()` fires only on a successful handoff.
- `funnelInstance.Run(ee *workerExEnv)` (NEW, the executor Task body) holds the moved flush: borrow body
  ctx → `c.mu` → `flush` → read `detached` → unlock → recycle if detached. This is verbatim the old
  `Execute` body, now on an executor goroutine with the worker's `ee` (was a fresh `&workerExEnv{}`).
- **Why collapsing onto the instance is safe (not a separate post-work like funnelPostWork):** the
  controller calls `instance.Free()` right after `Execute`'s handoff, possibly *concurrently* with the
  executor's `Run`. That is fine because `Free()` is already a pure no-op (R2 design) and the instance
  already self-recycles — nothing on the scheduler side touches the instance after Starting (the buffer
  slot is dropped in `releaseOthers`).
- **Barrier ordering preserved for free:** `flush()` (user Flush body + the deferred
  `state.DecrementReference()`) moves atomically to the executor, so the barrier still drops *after* the
  Flush body regardless of which goroutine runs it; the per-instance barrier (held from allocate) keeps
  the wave out of Done across the scheduler→executor handoff window.
- **bodyCtx-reuse race avoided:** added `borrowSrcCtx` field, written in `Execute` before the publishing
  handoff (rendezvous = happens-before) and read *once* at the top of `Run` before `c.mu` — so the owner
  reuse-pop that may recycle a non-detached shell the instant `Run` releases `c.mu` never races it. The
  borrowed bodyCtx is a `Run` local (not an instance field), as it was in the old `Execute`.
- **Widened accumulate/flush window:** the deadline-drain→flush window is now longer (drain → handoff →
  executor `Run` acquires `c.mu`) but it was already an interleavable window handled by `c.mu` + R1
  (accumulate on a drained instance sees `Reschedule`==false and leaves it to the pending flush) +
  spent-shell recycle. Widening changes nothing structurally.
- **Remaining for full C2:** the C2 latency/alloc benchmarks (real methodology: P99/max, heavy-tailed
  blocking-I/O, swept P:D ratios). CP-B1b was the last code-structure piece of the split.

**►►► CP-B2 CLEANUP DONE (2026-06-29) — build/vet/lint/suite green; -race confirming.** Deleted the
obsolete worker-pool lineage now that `defaultPool` is `workq.Scheduler`:
- Removed `internal/worker/` (pool.go + test) — unreferenced.
- Removed `internal/workq/queue.go` + `worker.go` + `queue_test.go` — the `workq.Queue`/`Worker`/
  `ExecEnv`/`NewWorker` scaffold was used only by `internal/worker` + itself (dead cluster). `Accepted`,
  `Pending`, `Scheduler` are untouched and live.
- Stripped the vestigial `deadlineCh` (always nil since Design B) from `AddWorkFunc` and every
  implementor: `scheduler.pull`/`selectWork`, `wave.addWork` (×2), and the test addWorkFns. The
  deadline-wake path is now entirely the queue-owned timer.
- `timed_test.go`'s `TestAccepted_FutureDeadline_WakesParkedWorker` now validates Design B directly
  (the queue timer fires a waiters notification that wakes the parked worker at ~deadline).
- **Remaining for full C2:** CP-B1b (funnel-flush body → executor, the deferred D2 — delicate R1/R2);
  the C2 latency/alloc benchmarks (real methodology).

**CP-B1 IMPLEMENTED (2026-06-28e) — build/vet/sim green; -race batch pending.** Edits:
- `execpool.Executor.TryPushBack` (non-blocking direct handoff).
- `pool.go`: `bodyExecutor = execpool.NewExecutor(&workerExEnv{})`; `Wait()` reaps executor then
  scheduler; `defaultPool` STILL `worker.Pool`.
- `taskWork.Run(ee)` (= run+Free), `Execute` removed; `taskPostWork.Execute` → TryPushBack-then-(if
  ShouldBlockOrPostpone)PushBack to `bodyExecutor`, `ex.Starting()` + `task=nil` on handoff.
- `funnelWork.Run(ee)`, `gate`/`releasePermit` (permit hoisted out of `Execute`); `funnelPostWork.
  Execute` → permit gate + TryPushBack/Waiting-then-PushBack. `boundFunnelWork` no longer a workq.Work.
- `workerExEnv.ExecuteNowOrQueue` (nested, on an executor body) → `defaultPool.ForceFresh(work)`
  (non-blocking drop to scheduler) — the deadlock-avoidance (no inline blocking PushBack on an
  executor goroutine).
- `funnelInstance.Execute` UNCHANGED (flush stays on scheduler — D2 deferred to CP-B1b).
- The handoff is blocking ONLY on scheduler workers / top-level/skim producers, never an executor
  body goroutine (nested drops to the scheduler), so it can't wedge waiting for an executor.
- **BEHAVIORAL CHANGE (expected, deterministic): `Example_observable`** golden shifts — task C now
  dispatches promptly when a permit frees (10ms) instead of the top-level backpressure-help first
  skimming B (20ms); B is skimmed during SkimAll (30ms) instead. Correct results, order, and the
  concurrency-2 limit all hold. Direct consequence of the split: the top-level help-drain (the
  deadlock-avoidance, `wv.block`/`gateAcquire`, UNCHANGED) now only races the executor's independent
  body completion rather than driving the body itself. NOT a deadlock regression (help-drain still
  skims a blocked permit-holder's result). Golden needs updating — flagged for PN.

**►►► CP-B1 -race RESULT: HANG (spawn storm) — worker.Pool shortcut REJECTED (2026-06-28e).** The
40× `-race TestBySimulation` batch HUNG (10m timeout, iteration 1). Dump (`/tmp/race_batch.log`,
copied to scratchpad `cpb1-hang-dump.log`): **3981 `worker.Pool` worker goroutines** vs 93 idle
executors; 214 workers blocked on the single `delayq` mutex in `controller.drainScheduled`. =
**spawn storm**, NOT the prior leaked-ref class.
- **Root cause:** nested routing via `defaultPool.ForceFresh(work)` (workerExEnv.ExecuteNowOrQueue)
  spawns a `worker.Pool` worker per nested submit — its `Notify`-or-spawn spawns whenever no worker is
  PARKED, and under load none are parked (all contending on `delayq`), so every nested submit spawns.
  Positive feedback (more workers -> more `delayq` contention -> fewer parked -> more spawns) -> ~4000
  workers -> livelock -> timeout. The 93 idle executors prove it's NOT an executor shortage; the
  scheduler side melted down.
- **This is precisely the line 94-103 prediction:** the producer-side redirect is wrong for nested;
  "the blocking handoff MUST be the scheduler's Work," reached via a **demand-BOUNDED** drop, not a
  per-call spawn. `worker.Pool`'s per-call `TrySpawn` demand model cannot bound this.
- **VERDICT: the "keep worker.Pool for CP-B1, swap to Scheduler in CP-B2" sequencing FAILS — the two
  are coupled.** The handoff needs `workq.Scheduler` (on `execpool`, whose `RegisterUnmetDemand`
  COUNTER model bounds workers to actual unmet demand) AND the admit/handoff split.
- **SALVAGEABLE (correct, reusable):** the body-side cutover — `bodyExecutor`, `Executor.TryPushBack`,
  `taskWork.Run`/`funnelWork.Run`, `taskPostWork`/`funnelPostWork` -> PushBack, the funnel gate hoist.
  ONLY the nested-routing (`ForceFresh`) + the `worker.Pool` scheduler are wrong. Uncommitted (tree
  hangs under -race; do NOT commit).
- **NEXT:** wire `defaultPool` = `workq.Scheduler` and route nested admission to its bounded intake,
  Wait=admit / Work=PushBack. Then re-run the -race batch. The genuinely-hard C2 core; do it
  deliberately.

**►►► SCHEDULER INTEGRATION DONE (2026-06-28e) — build/vet/sim×3 green; -race batch RUNNING.**
Replaced the worker.Pool shortcut with `workq.Scheduler` (execpool, counter-demand):
- `pool.go`: `defaultPool = workq.NewScheduler()`. Removed the dead worker.Pool plumbing
  (`newWorkerState`, `workerEnvKey`, `workerEnvFromContext`) and the `internal/worker` import — the
  package is now unreferenced (delete in CP-B2 cleanup).
- `workerExEnv.ExecuteNowOrQueue` (nested) → `defaultPool.Post(ctx, work)` (the intake Handoff,
  block-as-demand COUNTER) instead of `ForceFresh`. Blocks the executor body only until a scheduler
  worker ACCEPTS (bounded by outstanding blocked Posts — execpool `maybeSpawn` ramps one spin-up at a
  time toward the `demand` counter and no further; verified in execpool/pool.go), deadlock-free
  (separate pool, unblocks at handoff before admission).
- `funnelInstance.Execute` (flush, still on scheduler per D2): now uses a fresh `&workerExEnv{}` for
  the flush body instead of `workerEnvFromContext` — scheduler workers carry no ctx exEnv, so the
  flush is self-contained (the LAST consumer of the worker-ctx exEnv; that's why the plumbing could
  go). Flush logic otherwise UNCHANGED (synchronous under c.mu — avoids the D2 R1/R2 split risk).
- Why this fixes the storm: worker.Pool's `TrySpawn`/`ForceFresh` spawn per-call (capped burst, but
  unbounded total under sustained demand). execpool spawns toward a balanced COUNTER, converging on
  outstanding demand. Hot nested path = Post (counter); only rare flushes use `Nudge`.
- Gate: 40× `-race TestBySimulation` (`/tmp/race_batch2.log`). If green → update `Example_observable`
  golden (the benign interleaving change persists; no nested there, so it's the body-executor split).

**►►► GREEN CHECKPOINT (2026-06-28e) — dispatch/execution split WORKS.** Validation:
- build + vet + full non-`-race` suite (all packages incl. psgwf): GREEN.
- `-race TestBySimulation`: **56 iterations clean** (6× then 50×, 701.9s; zero hangs/races/fails). The
  deterministic worker.Pool storm is GONE; no leaked-ref hang or data race surfaced. (Above the ≥25
  floor; a ≥300 soak still advisable before fully trusting the rare class — feedback_race_confirm.)
- `Example_observable` golden updated (the benign body-executor-split interleaving).
- UNCOMMITTED pending PN's go-ahead to commit (harness rule: commit only when asked).
- **Remaining for full C2:** CP-B1b (move funnel-flush `funnelInstance.Execute` → executor, the
  deferred D2 — needs the instance-split designed around the c.mu/barrier ordering); CP-B2 (delete
  `internal/worker`, now dead); audit the scheduler scaffold for vestigial bits (e.g. confirm
  `incoming`/Post/`Nudge` are all live now); then the C2 latency/alloc benchmarks (real methodology).

**►►► EXECUTOR WIRING REVERTED — BACK TO GREEN (`bfa4005`, 2026-06-28d).** The `HandedOff`
executor-wiring (`71d8699`) was REVERTED: it both (a) introduced an intermittent `-race`
`TestBySimulation` hang (~1/25, a leaked work-ref) and (b) was a **complexity smell** — it kept the
body flowing through the priority controller and bolted on a `HandedOff` flag to suppress the
controller's `Free`, creating two-owner contention + an unstated invariant. PN's call: the split
should *simplify*, so redo it the principled way. Branch is GREEN again (single pool; build + vet
+ `-race ×6` sim pass). The `workq.Scheduler` scaffold (`6cb1164`) + `execpool.Executor` remain,
unwired.
- **PRINCIPLED CUTOVER (next) — the body NEVER touches the priority controller.** The controller
  admits *scatter-works* only; on admission success `taskPostWork`/`funnelPostWork.Execute`
  `PushBack`s the body to the executor's Handoff via the EXISTING `wk.task = nil` ownership
  transfer (the controller `Free`s the scatter-work normally; the executor owns+frees the body).
  **No `HandedOff`, no `Execution` change, no controller `Free`-skip.** Topology (PN): `incoming`
  becomes the scheduler's admission-intake Handoff (`AddWork` pulls scatter-works); a SEPARATE
  Handoff is the executor's body intake. Nested admission runs on the scheduler (off the body's
  goroutine), so the blocking body-`PushBack` is on a scheduler worker, not the nested body —
  preserving nested non-blocking without a `HandedOff` flag. Funnel-gate hoist (Wrinkle 1) IS
  needed here (gate in `funnelPostWork` before the body crosses, since `funnelWork` now runs on the
  executor). This likely dissolves the leak (it lived in the `HandedOff` contention).
- **Dump signature (decisive):** at deadlock only **6 goroutines** — the timeout alarm, the test
  goroutine, and **4 parked in `skimSelect`** (1 top-level `CloseAndSkimAll` + **3 executor bodies
  driving nested `SkimAll`s**). **ZERO scheduler (`worker.Pool`) workers, ZERO `PushBack`-blocked,
  ZERO mutex/semacquire.** = the skill's "all workers exited, only SkimAll parked → lost
  wakeup / stuck reference" class, NOT a lock cycle.
- **Diagnosis (hypothesis, unconfirmed):** before the wiring, bodies ran *on* scheduler workers,
  keeping the scheduler pool warm while work was in flight. Now bodies run on the executor, so the
  scheduler scales to zero aggressively. A nested sub-wave's work then needs a scheduler to admit
  it (and an executor to run it), but the scheduler is gone and the re-spawn/re-wake is missed — OR
  a sub-wave reached Done and the parked `SkimAll` missed the Done wake. The wiring moved
  body-completion bookkeeping (`Free`→`DecrementWork`→Done/skim-wake) from the scheduler worker
  onto the executor goroutine — a candidate lost-wake site to scrutinize.
- **Repro is HARD (Heisenbug):** only reproduces under **`-race` at DEFAULT config (~1/25)**. Every
  bias tried SUPPRESSED it: zero `SelfTime` 0/60 (no-race) + 0/40 (-race); 2ms idle-timeout 0/40
  (no-race) + 0/30 (-race); `-race`+`-trace` 0/60 (trace overhead masks it). So it's timing-tight
  and tied to the default 1s idle + µs–ms SelfTime. The captured-trace approach failed (trace masks
  it); needs a different tactic — e.g. add invalid-state panics / targeted `trace.Logf` at the
  scheduler spawn-on-demand and the wave Done/skim-wake, or an "op started-vs-completed" diff to
  prove whether work is pending-unadmitted (lost spawn) vs. done-but-unwoken (lost Done-wake).
- **REFINED DIAGNOSIS (2026-06-28d, deeper dig — supersedes the lost-spawn guess above):**
  - **It is a LEAKED WORK/REFERENCE, not scale-to-zero.** Discriminator run: scheduler idle set
    to 1h (never scales to zero mid-run) STILL hangs (1/80) → scheduler scale-to-zero is NOT the
    trigger. The 1h-idle hang dump is the clean tell: ~80 *idle* scheduler workers (kept alive by
    1h idle), nothing running, and **ONE lone top-level `SkimAll` parked** — its wave never reached
    Done despite no in-flight work. `skimSelect` (wave.go:577) includes `case <-state.Done()` (a
    closed channel — reliable), so it is NOT a lost-Done-wake: the wave's in-flight/ref count is
    stuck > 0, so a body (`taskWork`/`funnelWork`) was `IncrementWork`'d at dispatch but its
    `Free`→`DecrementWork` NEVER ran (or a funnel-instance barrier leaked).
  - **The executor Handoff orphan/abandon machinery is CORRECT** (verified by reading
    `rdvq/inboxonly.go` PopFrontFunc + `handoff.go`): sender-before-abandon → orphan drained &
    run (ok=true); sender-after-abandon → abandonment marker makes TryPushBack skip the dead inbox
    → block-as-demand spawns fresh. So the leak is NOT a lost handoff there.
  - **Controller HandedOff path looks correct** (single-item-per-drive: a handed-off item Starts →
    tryAccepted returns → drive ends → executor.Reset clears wasHandedOff; releaseOthers nils the
    item's buffer slot so it isn't requeued; executor owns+Frees). No cross-item leak found by
    inspection.
  - **HEISENBUG resists ALL observation:** reproduces ONLY at DEFAULT config under `-race` (~1/25).
    SUPPRESSED/masked by: zero or small `SelfTime`, 2ms idle, amplified nesting, `-trace` (0/60),
    AND even lightweight atomic counters + a 2s ticker goroutine (0/60). Also: small samples are
    statistically meaningless here — P(0 hangs in 40 at 1/25) ≈ 20%, so earlier "suppressed"
    reads were underpowered. Any added goroutine/sync shifts the window.
  - **NEXT TACTICS (untried / promising):** (1) a NON-perturbing leak witness readable only from the
    `-timeout` goroutine dump — e.g. park a sentinel goroutine whose stack/select encodes the live
    body count, or have the executor-pool scale-to-zero point assert "no handed-off-but-unrun
    bodies"; (2) op-type bisect (funnel-only vs launcher-only) and limiter on/off — but ONLY with
    large samples (≥150 -race) or after finding a high-rate amplifier; (3) deep code review of the
    **funnel-instance barrier** lifecycle (the dumps prominently feature funnels) vs the new
    `funnelWork.Run`+`Free`/permit-release-moved-to-Free change; (4) since the leak is a missed
    `DecrementWork`, add a per-wave `IncrementWork`/`DecrementWork` pair-tally that PANICS on a
    detectable imbalance at a sync point (gut: the hang has no sync point, so this needs a teardown
    hook). The captured hang dumps are in the scratchpad (`noidle_72.log` = the clean 1-stuck-wave
    case).


**►►► C2 IN PROGRESS — `execpool.Pool[W]` FOUNDATION LANDED; SCHEDULER (workq.Scheduler)
NEXT (Phase 2b, 2026-06-28).** The pool-split is being built bottom-up: one shared
goroutine-pool foundation, two pools on it (executor + scheduler), then the live cutover.
Design converged through a long review with PN this session — the notes below SUPERSEDE the
earlier "uncapped executor" and "execpool forks worker.Core" sketches.

**►► SESSION 2026-06-28b DECISIONS (PN), refining the steps below:**
- **MERGE steps 2+3** — build `workq.Scheduler` in its *real post-cutover shape* and flip the
  live path in one landing, NOT a dormant inline-body intermediate first. Rationale: the
  intermediate's postpone path is dead/untestable (scheduler-run `taskWork` always `Starting()`s);
  the clean `Wait`/`Work` boundary only exists at cutover semantics; the executor foundation is
  already proven (step 1), so merge = wire proven executor to new scheduler `Worker`, not bring
  up both at once. Build the Scheduler ALONGSIDE legacy + test against the live executor in
  isolation, THEN one cutover flip (mitigates the red window).
- **KEY FINDING (the reason the scheduler decomposition is *required*, not optional):** the plan
  doc's "just redirect the two `*PostWork.Execute` Posts → `executorPool.PushBack`" is the
  *producer-side* redirect and is **WRONG for nested submits** — `PushBack` is a blocking
  rendezvous (no `TryPushBack`), and a nested submit runs the admission chain inline on its
  body's executor goroutine, so a producer-side `PushBack` blocks the body and violates
  "nested intake = non-blocking drop-and-go." The blocking handoff MUST be the **scheduler's
  `Work`** (nested drops to buffered `Accepted` non-blocking; a scheduler worker `PushBack`s).
  ⇒ `Wait` = non-blocking admit (governor + `Acquire`, postpone missers), `Work` = blocking
  `PushBack` of the admitted body. This requires **separating non-blocking admit from blocking
  handoff in the admission chain** (`launcherScatterWork`/`limiterScatterWork`/`*PostWork`) —
  the genuinely hard, concurrency-critical core of C2. (Recorded in the plan doc's C2-mapping
  banner.)
- **Top-level executor fast lane = DEFERRED follow-on** (see STEP 5 below): any-top-level (not
  just no-limiter), landed + benchmarked AFTER the split is green. First cut: ALL bodies
  (top-level + nested) go scheduler-intake → scheduler `Work` `PushBack`.
- **MERGED CP SEQUENCE (each a green checkpoint):**
  - **CP1** (additive, unwired): `workq.Scheduler` + scheduler `Worker` on `execpool.Pool[W]`,
    finishing `internal/workq/worker.go`'s draft — `Wait`=drain+collect+ (none ready) block on
    `Accepted` waiters composing pool idle+stop; `Work`=execute. Reconcile env-on-ctx
    (`workerEnvFromContext` vs ctxpool `W`). Isolated test (incl. postpone/anti-spin via nested
    scenarios + a real `execpool.Executor` for the handoff). Legacy `worker.Pool` untouched.
  - **CP2** (the admit/handoff split): refactor the admission chain so non-blocking admit
    (governor+`Acquire`, postpone-on-miss) is separable from the blocking `PushBack`; funnel-gate
    hoist (Wrinkle 1) lands here (gate inside `funnelPostWork` before the handoff). Green under
    single pool first if possible.
  - **CP3** (cutover flip): `defaultPool` → `workq.Scheduler`; bodies → executor (`run(ee)` +
    `defer Free`, delete `*Work.Execute`); `streampool.Wait()` reaps both pools. *Gate: full
    suite + -race + `TestBySimulation` reliably green + latency/alloc benchmarks (real
    methodology).*
  - **CP4**: delete `internal/worker`; trim workq's exported API.

**►► SESSION 2026-06-28c — TOPOLOGY PINNED (PN), supersedes the CP framing above where it conflicts:**
- **`worker.Pool` / `worker.Core` is OBSOLETE** — both pools are `execpool`. Scheduler =
  `execpool.Pool[*schedulerWorker]`; executor = `execpool.Executor[*workerExEnv]`. `internal/worker`
  gets deleted.
- **`workq.Queue.incoming` (the `Pending` field, the scheduler's intake where AddWork pulls work)
  becomes an `rdvq.Handoff[Work]`** — the producer→scheduler rendezvous. Its **block-as-demand IS
  the scheduler pool's spawn signal** (the producer's `PushBackFunc` selectFn fires
  `scheduler.pool.RegisterUnmetDemand`, exactly mirroring `execpool.Executor.PushBack`). So there is
  **no separate demand-counter to invent** — the Handoff supplies it. Handoff has `TryPushBack`
  (direct handoff, non-blocking miss) but **no `TryPopFront`**, so the controller's non-blocking
  pull probe (`TryAddNew` with nil waiters) becomes a no-op for `incoming`; the blocking
  `WaitForNew` path does `incoming.PopFrontFunc` composing the Accepted waiters' workWaitCh +
  scheduled deadline + execpool idle + stop. Schedulers never run bodies, so a scheduler is always
  promptly available to take from `incoming` (nested rendezvous is short — always-live-dispatcher).
- **The executor's Handoff is SEPARATE** from `incoming` (scheduler→executor, inside
  `execpool.Executor`).
- **The scheduler REUSES the controller** — `schedulerWorker.Wait` = `accepted.ExecuteOne` (pull
  from `incoming` Handoff, composing execpool's idle), `Work` = no-op. NO `ExecuteOne`
  decomposition and **NO env-on-ctx reconciliation**: the scheduler runs only admission
  scatter-works (`launcherScatterWork`/`limiterScatterWork`/`*PostWork`), which never need E; E is
  passed directly to the body's `run(ee)` on the executor. The body→executor hop lives inside
  `taskPostWork.Execute`/`funnelPostWork.Execute` (`→ executorPool.PushBack(body)`), and the
  existing `wk.task = nil` ownership-transfer means the executor owns+frees the body (no
  `HandedOff` signal needed).
- **Dispatch:** the admission scatter-work is PushBacked to `incoming` for the scheduler to admit
  (governor + non-blocking permit `Acquire`; success → `executorPool.PushBack(body)`; miss →
  postpone to Accepted). Bodies (`taskWork`/`funnelWork`) gain `Run(ee)` + self-`Free`; their
  `Execute(ctx,ex)` is deleted. Funnel-gate hoist (Wrinkle 1) folds in. `streampool.Wait()` reaps
  both pools.

- **`internal/execpool` FINAL SHAPE (landed, `fa17a48` + `f031f71`; isolated, unimported).**
  `Pool[W Worker]` is the **single shared spawn/lifecycle foundation** (NOT a fork that
  duplicates worker.Core — worker.Core is to be deleted; the scheduler reuses THIS). It owns
  the loop `for { Wait; Work } ; Close`, the capped demand-driven spawn, the refcount/`Wait`
  lifecycle, the **pooled idle timer** (`internal/timerp`), and the **reused worker ctx**
  (`internal/ctxpool.WithValue(poolCtx, w)` — carries `W`, `Done()` == poolCtx == stop). The
  worker is a `Worker` interface:
  - `Wait(workerCtx, idle <-chan time.Time) bool` — become idle / block for work (composing
    the supplied `idle` + ctx.Done stop), stash it, return false on idle-out/stop. **Idle is
    supplied by Pool** so it keeps idle-timeout policy.
  - `Work(workerCtx)` — execute what Wait stashed.
  - `Close(workerCtx)` — teardown; **Pool never touches W after Close** (poolable).
  - Spawn model = **demand counter, not edge+chain-heuristic**: `RegisterUnmetDemand` /
    `UnregisterUnmetDemand` (a source records work it couldn't place on a waiting worker, and
    un-records it when taken/withdrawn); `maybeSpawn` spawns while `unmetDemand > spawning`,
    capped by `spawnConcurrencyLimit` (=1), re-evaluated when a worker establishes (frees a
    spin-up slot). Precise ramp, **no over-shoot tail**; `Wait`'s bool is just continue/stop.
    The cap is load-bearing **independent of backpressure** (spin-up cost / goroutine glut,
    NOT throttling admitted work — admission already happened upstream).
  - `Executor[E]` = the concrete executor on `Pool[*executorWorker[E]]`: its Worker waits on
    an `rdvq.Handoff` (PopFront), runs `Task[E]`. `PushBack` is block-as-demand —
    RegisterUnmetDemand on the first park, Unregister on return (delivered/cancelled).
  - **No `Locked`-suffixed methods** (PN standing pref; lock contract in comments).
  - Verified each commit through the FULL pre-commit hook (suite + -race + golangci 0).

- **STEP 2 = `workq.Scheduler` on `execpool.Pool[W]` (NEXT).** Build the scheduler as a
  second pool on the SAME `Pool[W]`, **in package workq** (the pool is a workq impl detail;
  this also lets workq's exported API shrink). The scheduler's `Worker` decomposes the
  existing `Accepted.ExecuteOne` (settled with PN):
  - `Wait` = ExecuteOne's **find** phase (fresh → postponed → scheduled priority). Found
    ready work → stash + return immediately (busy, not idle). Nothing ready → call
    **`AddWork`** (register as an available waiter) and **block** there (composing the pool's
    `idle` + ctx stop); return the pushed item, or false on idle-out/stop. **AddWork
    registration IS the idle/available point.**
  - `Work` = ExecuteOne's **execute** phase on the stashed item (controller `work.Execute` +
    `Starting`/postpone bookkeeping + `onSecure`). One ExecuteOne = one Wait + one Work; the
    only blocking point is AddWork. VERIFY the **postpone path** (work that registers a
    listener and doesn't Start) lives in Work and re-queues, so the worker loops back to Wait.
  - Demand wiring = workq's existing `unmetDemandFn` re-expressed: a `Post` that can't hand
    off to an AddWork-waiting worker → `pool.RegisterUnmetDemand`; a worker whose Wait returns
    work → `UnregisterUnmetDemand`.
  - **Env-on-ctx reconciliation (open):** the worker ctx carries `W` (ctxpool), but the
    scheduler's bodies (`work.Execute`) fetch their env via `workerEnvFromContext`/
    `workerEnvKey`. Step 2 reconciles: either the scheduler's `W` stamps the env under
    `workerEnvKey` in `Work`, or `workerEnvFromContext` migrates to `ctxpool.GetValue[W](ctx).env`.
  - **SCAFFOLD ALREADY EXISTS — `internal/workq/worker.go` is a DRAFT of exactly this** (its
    header: "DRAFT — first cut of the Worker driver"). It has `Worker[E]` with `selectWork`
    (the ONE canonical AddWork block: inbox/outbox/workWait/deadline/idle/done/ctx), `pull`
    (drain `incoming` → fresh), idle/onSecure/onWait, and a `Help` nested-drive sketch (the
    block-and-help the limiter reclaim path wants). Its `DriveOne` still delegates to the
    legacy `ExecuteOne` as a stopgap (line ~140: "the native driveOne will return the pair
    directly"). **Step 2 = finish this draft natively and reshape it to the `Pool[W]` Worker:**
    `Wait` = `DriveOne`'s find half (drainScheduled → fresh; collect fresh/postponed; else
    `pull`→`selectWork` block at AddWork); `Work` = `controller.execute` on the stashed item
    **minus `onSecure`** (Pool.establish does that now). Verified split point: `collectAccepted`
    (find) vs `execute` (run) in `accepted.go` cleave cleanly; `onSecure` (accepted.go:605,
    release spawn token before body) maps onto `Pool.establish` at the Wait→Work boundary and
    leaves the controller.
  - **APPROACH = additive, no red window:** build `workq.Scheduler` + scheduler `Worker` in
    `workq` ALONGSIDE the legacy `ExecuteOne`/`worker.Pool` (both stay green), test Scheduler
    in isolation, THEN cut over (step 3), THEN delete legacy `ExecuteOne` + `worker` (backward
    compat intentionally dropped — PN: `ExecuteOne`'s buffer/postpone-loop complexity existed
    to protect `Accepted` from externally-pushed work; with `Scheduler` the public face it can
    be decomposed to fit `Worker` naturally). The other session also edits `workq`/`funnel` —
    ideally quiesce the tree for this build.

- **STEP 3 = cutover:** `defaultPool` → `workq.Scheduler`; the two body-running posts
  (`taskPostWork`/`funnelPostWork`, the only `defaultPool.Post` callsites) → `executor.PushBack`;
  fold `Free` into `taskWork`/`funnelWork.run`; delete `taskWork`/`funnelWork.Execute`; wire
  `streampool.Wait()` to reap both pools. **STEP 4 = delete `worker`; trim workq's exported
  API** (unexport Worker/NewWorker/With*/ExecEnv behind Scheduler; keep the producer + gate
  types). *Gate: full suite + -race + `TestBySimulation` + latency/alloc benchmarks (real
  methodology).*

- **STEP 5 (DEFERRED follow-on, decided w/ PN 2026-06-28) = top-level executor fast lane.**
  At top-level dispatch BOTH gates already run inline on the caller's goroutine
  (`meta.ExecuteNowOrQueue` → `launcherScatterWork.Execute` governor → `limiterScatterWork.Execute`
  permit gate → `taskPostWork.Execute`), so the scheduler is a pure relay for top-level work.
  Route top-level `taskWork`/`funnelWork` straight to `executor.PushBack`, bypassing the
  scheduler queue (saves enqueue + scheduler-worker wake + dequeue on the hottest path — a
  P99/max win). **Enabling condition is *top-level* (blocking-capable caller, admission done
  inline), NOT no-limiter** — a top-level *limited* launch acquires its permit inline too, so
  the lane is **any top-level dispatch** (PN, 2026-06-28; fairness is arbitrated at the permit
  pool, not the scheduler queue; suspend/reclaim is wave-queue-driven regardless of routing).
  Backpressure preserved: the governor's block-and-help still wraps dispatch inline; a full
  executor applies block-as-demand. **Nested submits CANNOT take this lane** (must be
  non-blocking drop-and-go; `executor.PushBack` is a blocking rendezvous, no `TryPushBack` by
  design) → they keep the buffered scheduler intake. Net: after the lane lands the scheduler
  handles ONLY nested + funnel-scheduled + postponed-limited-retry (the deferred/postponable
  admission — also where the scheduler's postpone path is actually exercised). **DEFERRED to
  AFTER the two-pool split (steps 2–4) is green + benchmarked**, so we measure the removed hop
  rather than assume it and don't entangle the lane branch with the already-large cutover.

- **C2a STATUS:** `run(ee)` extraction LANDED (`a107270`) — `taskWork`/`funnelWork` expose
  the Execution-free, ctx-free `run(ee *workerExEnv)` C2c needs. The **funnel-gate hoist was
  REVERTED** — it is NOT a clean mirror of the task path: the task gate sits inside
  `launcherScatterWork` (governor wraps it), but the funnel's governor rides
  `funnelPostWork.onWait`, so wrapping `funnelPostWork` with `limiterScatterWork` puts the
  gate OUTSIDE the governor and a top-level blocking submit hits the `ExecuteNowOrQueue`
  block-guard. **Deferred to C2c**, where the funnel seam-flip happens anyway (gate inside
  `funnelPostWork.Execute` before the post, preserving governor→gate order — needs sim
  validation).

- **MULTI-SESSION CAVEAT:** a concurrent session (session_011…) committed `a5abde3` +
  `bc187f3` (Resequencer/RangeResequencer + edge/edgegrpc demos) into this same working tree
  mid-session, and edits `funnel.go`/`edge*` live. Watch `git status` before committing;
  serialize on shared files (esp. `funnel.go`/`workq` for step 2).

**►►► B LANDED — DISPATCH INFRA (ISOLATED, NOT WIRED) (Phase 2b, 2026-06-27).** The two
building blocks the pool-split (C2) needs, both standalone with no live consumer yet:
- **B1: `rdvq.Handoff[T]`** (`internal/rdvq/handoff.go`) — the lock-free **unbuffered**
  rendezvous = the existing `inboxStackQueue` (inbox tier, LIFO warmest-first consumer
  selection) + a new sender-side `inboxWaiters`. Blocking `PushBack(ctx,value)` (park on
  `inboxWaiters` via the standard register-then-recheck confirm); receiver wakes one parked
  sender after registering its inbox; **no `TryPopFront`** (no outbox tier to poll). Senders
  are never stale (each actively sends), so NO renotify conservation is needed (unlike the
  permit pool). Tested: concurrent exactly-once, sender-blocks-then-delivers, ctx-cancel
  both sides; 20× `-race` + full rdvq `-race`.
- **B2: generic `worker.Core[E]`** (`internal/worker/pool.go`) — the demand-spawn +
  idle-exit + refcount/`Wait` lifecycle factored out of `worker.Pool`, with a **pluggable
  `WorkerLoop[E]`** and the demand source decoupled (`TrySpawn`). `worker.Pool` (the
  scheduler pool, `NewPool`) is rebuilt as `Core + sharedQueue + driveQueue` (the
  workq.Worker loop) — `defaultPool` and all its promoted methods unchanged. Isolated `Core`
  tests added (demand-spawn, Wait-join+reuse, scale-to-zero). Live path unchanged: full
  suite + 60× `-race` sim green.
- **Deferred to C2:** the **block-as-demand** hook (a `Handoff.PushBack` park → spawn an
  executor) is intentionally NOT in B1 — it lands when the executor pool is wired. The
  executor pool itself = `Core[E] + Handoff + a PopFront→Run loop` (C2).

**►►► C1 LANDED — NATIVE PERMIT CORE IN THE LIVE LIMITER; THE DEADLOCK IS FIXED
(Phase 2b, 2026-06-27).** The eager `limiter.go` request machinery (`directScheduler`/
`directRequest`/`request`/`acquireOrWait`/`reclaimRequest`/`applicant`/the `resource`
interface) is **replaced** by a native `internal/permits` integration on the **single**
`worker.Pool` (no pool split — that is C2). Surface unchanged. This is the
deadlock-fix milestone: **`TestBySimulation` is reliably green, including ≥300 `-race`
runs** (the pre-existing ~1/120 `-race` hang is gone). Verified: full `./...` `-short`
suite + `-race` + lint (golangci 0 issues) + `internal/permits` rapid/race.

- **Forest construction** (`wavepermits.go`): one `permits.Cache` per `(wave, Limiter)`
  = `C_W^L`. A Wave lazily owns `map[*permits.Pool]*Cache` (guarded), mkdir-p'd along the
  driving `ctxMeta.parent` chain at dispatch (`ensureCache`/`ensureCacheChain`/
  `createCache`); the immediate forest parent is the dispatcher's wave (`M.wave`), so the
  canonical "parked parent lends to sub-wave" case inherits directly and deeper gaps fall
  back to a forest-wide steal (still correct). Self-ref dropped at **wave-Done** via a new
  `wavestate` **`onDone`** hook → `releaseCaches`; descendant `NewChild` refs outlive.
- **Native handle** (`permithandle.go`): `ctxMeta.heldRequest request` → `ctxMeta.held
  *heldPermit` `{ownCache, permit}`; `currentHeldRequest` → `currentHeldPermit`. A zero
  `permit` IS the suspended state (re-entrancy no-op falls out). Created at dispatch
  (launcher `newScatterWork` / funnel `Init`), stamped at `borrowBodyContext`, acquired at
  the gate, released at completion (pooled via `heldPermitPool`).
- **Gate = three modes** (`gateAcquire`): nested/queued → non-blocking `Acquire` +
  `Pool.ListenersFor()` postpone; top-level → **block-and-help** on `Pool.Waiters()`
  (`blockAcquire`); mid-body reclaim (the suspend brackets) → **help-shaped** loop
  (`reclaim`). **`wv.block` is RETAINED and retargeted onto the Pool's waiters** — the
  earlier "no help loop / bounded skim-retry" sketch was WRONG (a plain reclaim deadlocks
  vs a result-poster; a `wv.skim` loop never sees a permit-free wake). Suspend/reclaim is
  **coarse per drive call**.
- **Lost-wakeup fix (a cutover regression):** a residual `-race` hang root-caused NOT to
  permit accounting (forcing unlimited permits → 120/120 `-race` pass) but to a **lost
  wakeup** the cutover introduced by splitting the Pool's wake into two bare `Notify(nil)`
  calls, dropping the **renotify conservation** the eager scheduler's single `rdvq.Notifier`
  had. A stale postpone listener swallows a bare wake without re-delivering it. Fix: keep the
  Pool's wait/wake as ONE `rdvq.Notifier`; `Release` wakes with `notify.Notify(nil)` (wrapped
  renotify → a consumer that can't use the wake re-delivers it down the chain to a real
  waiter). `WakeAll` (NotifyAll) only for multi-permit events (destroy / `SetMaxConcurrency`).
  Single wake + conservation, no thundering herd. (An earlier `WakeAll`-on-every-release was
  rejected as a band-aid.) Also fixed a reuse data race: `wavestate` `onDone` now runs BEFORE
  `close(doneChan)` so cache teardown completes before a re-arm can race it.
- **Deferred to C4 / not yet done:** the `applicant` sizing (`Processor`/`Value`/`Err`)
  was removed (re-derive natively when weighted resources land); multi-limiter still panics
  at construction (`opConfig.singleLimiter`); drain limiting (C3) untouched.

**►►► PERMIT CORE: HYBRID (LOCK-FREE HOT PATH, LOCKED FOREST) + WAIT/WAKE —
`internal/permits` (Phase 2a, 2026-06-26).** The concurrent core is a hybrid: the hot
acquire path is lock-free (atomic128 counter), the forest structure is an intrusive
doubly-linked list guarded by per-`Cache` mutexes. This **supersedes the fully-lock-free
`nbcq` forest** (`6ba8833`): that port had to drop the original move-to-back LRU
(reordering a shared lock-free queue isn't possible) and grew a sentinel-cycle steal +
lazy reaping + (planned) gen-tagged entries / a `next`-pointer fast lane just to claw the
LRU back. A per-`Cache` mutex around a DLL gives **O(1) interior move-to-back** (the
original LRU, restored), **exact removal** (no lazy reaping), and **direct front-to-back
traversal** (order-based camping, no sentinel/fast-lane) — far simpler. The lock is OFF
the common hot path; if a specific list ever shows up as a tail-latency bottleneck, swap
that list internal for lock-free without touching `Acquire`/`Release` (localized, gated on
measurement). This is essentially `e5b20b0`'s "lock-free hot path, locked steal/destroy",
chosen deliberately over the lock-free redux.
- **2a-i** packed `(held, inUse)` into one 128-bit atomic word (`atomic128`; no GC-shadow
  since both halves are scalars). Both halves are `uint64` amounts — a weighted Resource
  (memory limiter >4 GiB) is representable; weight-1 ops now, the width is headroom. Gated
  CAS transitions keep `0 ≤ inUse ≤ held` atomic. **(Unchanged by the hybrid pivot.)**
- **2a-ii (hybrid)** the acquire up-walk (steps 1–2) stays lock-free, ancestors **pinned
  by refcounts**. Each cache's children and the Pool roots are an intrusive `cacheList`
  (DLL) under a per-list mutex, kept coldest-first by **`touch`** (an acquire up-walk that
  passes a cache *unsatisfied* moves it to the back — O(1) relink under one lock; a
  satisfied hit pays nothing). The **steal** is a front-to-back DFS taking the first
  borrowable cache (the coldest), left in place so a still-borrowable victim is re-picked
  (order-based camping). **Deadlock-free:** the steal is the only op holding two list locks
  at once and always descends root→leaf; every other op (touch/pushBack/remove) takes a
  single list lock, so no cycle can form. `destroy` **unlinks exactly** then CAS-drains
  held to the Resource (`counts.drain` still coordinates with a concurrent `stealOut`).
- **2a-iii** the rdvq wait/wake (unchanged): non-blocking `Acquire` (manager admit) +
  blocking `AcquireWait(ctx)` (executor reacquire — parks, re-searches on each freed
  permit, confirm callback re-runs Acquire after registering = the lost-wakeup guard).
  Every `Release` and the capacity a `destroy` returns wake parked waiters (gated by a
  waiter count so the uncontended release is one atomic load).
- Validated: 50k `rapid` (algorithm, sequential) + `-race` stress ×10 (contended
  inherit/delta/steal, structural churn vs steal, `AcquireWait` liveness) + order-camping /
  `touch`-redirect / exact-removal unit tests; build + `golangci-lint` clean. The
  fully-lock-free `nbcq` forest, sentinel-cycle, lazy reap, and fast-lane are GONE.
- Committed `9af3d13` (includes the `docs/permit-core.md` reconciliation to the hybrid).

**►►► PHASE 2b DESIGN IN PROGRESS (2026-06-27) → `docs/plan/dispatch-execution-split-phase2b.md`.**
The dispatch-side design is being worked out in discussion; the plan doc holds the current
state. Converged so far: **one generic worker pool** (the current `worker.Pool` lifecycle —
demand-spawn + idle-exit + refcount/`Wait` — with a pluggable per-worker loop) instantiated
**twice** — an **executor pool** (runs user bodies, may block; simple `PopFront`→`Run` loop)
and a **scheduler pool** (`workq.Worker`-style over a shared `workq.Accepted`; non-blocking
permit acquire + governor, then hands off). The **scheduler→executor handoff is a NEW
unbuffered rdvq primitive** = `inboxOnlyQueue` + a sender-side `inboxWaiters` (= today's
`rdvq.Queue` minus the whole outbox tier, plus blocking `PushBack`; **no `TryPopFront`**) —
pure rendezvous, zero buffer dwell. The **body→scheduler intake stays the buffered
`Accepted`** (nested submit is non-blocking drop-and-go; postponed/scheduled are necessary
buffering). Key invariants: **LIFO consumer selection is load-bearing for scale-to-zero**
(FIFO would pin the pool); **a buffer-push and a spawn are the same event** (block-as-demand
→ P99 win); **demand fires only for spawn-gap buffering, never for backpressure**
(permit-free/governor-clear *wake* a parked scheduler, never spawn).
**FOREST CONSTRUCTION SETTLED:** one `permits.Cache` per `(wave, limiter)` (`C_W^L`),
**bodies are occupants** (a `Permit`, not a node); the L-forest mirrors wave nesting. At a
wave's first L-admission, **lazily mkdir -p the ancestor L-cache chain** (held=0
pass-throughs up to the nearest existing L-cache or Pool root — *don't* skip non-L
ancestors, else concurrent re-parenting), then acquire into the wave's own `C_W^L`.
Inheritance is **occupy-in-place** (`inUse++` on the ancestor, permit doesn't move);
**suspend = `Release` the body's own permit to its backing cache** (own wave for
checked-out, ancestor for inherited), **reclaim = `AcquireWait` from the wave cache
outward**. Cache refcount is **separate** (self-ref dropped at wave-Done; descendant refs
keep ancestors alive for sub-sub-waves). Multi-limiter: joint acquire in canonical order,
partial-miss → release+postpone. Rejected: per-body nodes+transfer, hoist-on-inherit
(alternation churn), skip-ancestors (re-parenting), Cache-as-single-permit/held-replication.
Pool all `Cache` allocs. Full write-up in the plan doc's "Forest construction" section.
**DRIVE RULE (settled):** a body holds its permit ONLY while running *its own user code*;
it lends for the **whole drive** (incl. running that sub-wave's skim handlers — handlers are
drain) and reacquires only when the drive call returns to its own code — **coarse, per
drive-call, NOT per-handler**. (Fix `permit-core.md`: strike "reacquire before each skim
handler" + the bound's "or a skim handler".) **DRAIN LIMITING (settled, opt-in):** dissolve
intake-vs-drain for limiting — `NewSkimmer(h, WithLimits)` limits a handler;
`NewFunnel(factory, WithLimits, WithFlushLimits)` limits accumulate (intake) and flush
(drain) separately. Default = limiter-free drain (common path unchanged). A limited
handler/flush acquires its OWN limiters (own cache, not the driver's permit) → just another
forest body. Revises "limiters gate intake, not drain"; deadlock-free by the per-limiter
machinery + "can't skim a wave you're part of" — **but MUST be model-checked** (parked
holder whose drain needs a permit, same-limiter-inherit + cross-limiter). Surface change to
ratify (WithLimits on Skimmer, WithFlushLimits on Funnel).
**GOVERNOR PLACEMENT (settled):** the per-wave `Governor`+`downstream` mechanism is
unchanged in purpose; the gate is checked on **both admission paths** (top-level skims if
clogged, scheduler postpones if clogged); `decrementDownstream` relief **wakes the
scheduler, never spawns**. Two retry triggers — permit-free + governor-clear — feed the one
`Accepted` waiter set.
**MIGRATION SEQUENCE (settled, no flag day, full detail in the plan doc):** 0) permit-core
hardening (pool `Cache`, model-check limited-drain + multi-limiter, fix `permit-core.md`);
1) **C1** permit core into the live limiter on the **single pool** (gut, don't remove) —
gated on **`TestBySimulation` reliably green = the deadlock-fix milestone**; 2) **B** the
dispatch infra (unbuffered rdvq primitive + generic-pool refactor), isolated; 3) **C2** the
pool-split cutover — gated on suite + sim + **latency benchmarks (real methodology)** = the
architecture+latency milestone; 4) **C3** drain limiting; 5) **C4** strip the dead eager
code. Key insight: C1 fixes the deadlock and is validated **before** any pool-split risk.
DESIGN COMPLETE — only the inbox-stack lock-freedom is deferred (measurement-gated).
**STEP 0 IN PROGRESS:** the steal now **ref-pins its victim** across `stealOut`
(`tryPin` — a conditional CAS that refuses to resurrect a cache committed to destroy;
`searchList` skips dying caches) — fixing a real near-bug (the victim is cross-subtree,
so only GC kept it alive across the take) and unblocking pooling; **`Cache` is now pooled**
via omnipool (recycled in `destroy`, safe only because of the pin); `permit-core.md`'s
"Driving is an alternation" + the concurrency-bound invariant are corrected. Validated:
`-race` ×10 + 50k rapid + lint. Remaining step-0 model-check items (limited drain,
multi-limiter) fold into C1/C3 where those patterns get wired. **NEXT = C1** (permit core
into the live limiter, single pool). See the plan doc.

**►►► PERMIT CORE SKETCH BUILT + MODEL-CHECKED — `internal/permits` (2026-06-26).**
Phase 1 of the dispatch/execution split: the isolated, model-checked hierarchical permit
cache that both `docs/permit-core.md` and `docs/dispatch-execution-split.md` mandate
building **before any cutover**. Isolated — nothing imports it yet, so zero risk to the
live limiter (the eager `directRequest`/`suspendForEpisode`/`reclaimRequest` in
`limiter.go` stay load-bearing until Phase 3). Four types with a real behavioral split:
`Resource` (pluggable accounting — the only thing that knows capacity; `TryAcquire(n)`/
`Release(n)`, weight-1 for now), `Pool` (the Resource boundary + forest root — the only
place permits cross in/out of the Resource; owns the steal; never caches), `Cache`
(per-unit forest node, cache-don't-return; `Acquire` does steps 1–2, delegates 3–4 to the
Pool), `Permit` (transient run-segment handle, alloc-free). Steal telemetry is structural
sibling-list order (**move-to-back**, no logical clock); `touch` fires only on an
*unsatisfied* pass (a hit pays nothing). Validated: 5 deterministic anchors (incl. the
canonical `limit==1` parked-parent-lends-to-sub-wave hang, dissolved) + 100k `rapid`
adversarial sequences + `-race`; `CheckInvariants` triangulates Σheld across caches / Pool
mirror / Resource in-flight, and an *independent* `HasBorrowable` oracle asserts liveness
vs the guided steal search. This is the structural fix for the pre-existing ~1/120 `-race`
`TestBySimulation` nested-drain hang: a parked holder's permit is idle hence borrowable,
so its sub-wave inherits it instead of livelocking.

  **PHASE 2 ENTRY POINT (next):** map the **manager and executor pools** onto
  `internal/worker.Pool` + `internal/workq.Queue`, with `internal/permits` as the
  foundation — managers admit *non-blocking* (acquire steps 1–4, postpone on miss),
  executors reacquire *blocking* (steps 1–5, wait). **The permit core's one open piece —
  the step-5 wait/wake trigger (event-based: wake when a contended permit frees) — is
  *defined by* those two callers, so co-design it in Phase 2, not standalone.** Place the
  governor's per-wave gate on the manager admission path. Phase 3 then sequences the
  migration off the eager `limiter.go` code (no flag day). Deferred: weighted amounts (the
  `Resource` `n` param already allows it) and cross-limiter joint admission. Spec +
  sequencing: `docs/dispatch-execution-split.md` and `docs/permit-core.md` "Open / next".

  Supporting refactors landed alongside: `ctxMeta.job` retired + the `wv`(`*Wave`)/
  `wk`(Work) naming convention applied package-wide, with redundant/dead params pruned
  across the dispatch + skim paths; `streampool.Wait` now clears the `internal/ctxpool`
  reuse caches after the worker join.

**►►► FUNNEL ENGINE REMOVED — FLUSHES ON THE SHARED POOL (2026-06-24).** The per-Wave
`funnelEngine` + flusher goroutine + `cpworker.go` are gone. `Funnel[T]` is now a plain
value `{wave, factory, limiter, id, instancePool, workPool}` (no inner heap object);
accumulator instances live in a per-Wave `funnelInstances sync.Map` keyed by funnel id.
Deadline flushes ride the global `defaultPool`'s scheduled queue; the end-of-work sweep is
a synchronous **enqueue-only** `wavestate.onFlushing` callback (replacing the `FlushChan`
close) that `ClaimForFlush`+`ForceFresh`es each live instance — the flush itself runs on a
pool worker via the same `funnelInstance.Execute` path as a deadline flush. No-deadline
instances are not scheduled (no 24h placeholder). Recycle rides the pop (rule R2);
`initState` clears the map. **Two spawn regressions found+fixed during verification:**
(1) DECISION B's deadline-parked worker pinned the `spawnConcurrencyLimit` token →
release it at the park point (`workq.WithOnWait`); (2) `ForceFresh`'s `Notify(demand)` was
a no-op with no parked waiter → spawn directly when `Notify` finds none. After both, the
`-race` `TestBySimulation` hang rate is **~1/120 — baseline parity**; the residual is a
**pre-existing** nested-drain deadlock (the dispatch/execution conflation, the split's
domain), not introduced here. Plan + full write-up: `docs/plan/funnel-engine-removal.md`.
Verified: full `./...`, `-race` suite, 120× `-race` `TestBySimulation` (parity),
`reuse_test.go`, alloc tests. (`Example_observable` is independently flaky — known.)

**►►► WAVE-SCOPED FUNNELS, NO EXPLICIT LIFECYCLE LANDED (2026-06-23).** Funnel has no
`Close`/`Dup` and no teardown: `Funnel[T]` is a plain value (no leakguard), and
`AccumulatorFactory` has no `Close` either. The whole mechanism is a contract — the
framework never touches an instance after `Flush`, and flushes every outstanding
instance before the wave drains (the per-instance wave-barrier ref + the end-of-work
flush sweep) — which lets users pool their own state against well-defined lifetimes.
`internal/leakguard` deleted. (An earlier cut, `a2fceba`, kept `factory.Close` +
wave-driven finalization; we then dropped `factory.Close` as unnecessary.) Plan:
`docs/plan/funnel-lifecycle.md`.

**►►► ZERO-VALUE WAVE LANDED (2026-06-23).** The 2026-06-21b Wave lifecycle (below)
is now implemented. `NewWave`/`Cancel`/`CancelAndWait` are gone; a zero-value
`var w streampool.Wave` self-inits on first use (`ensureInit`) and re-arms after a
drain (`ensureArmed`, dispatch-only) so a `*Wave` is reusable/poolable; the Wave owns
no ctx (flusher roots at Background, exits on `state.Done()`); top-level dispatch binds
via `op.In(&w)`; dispatch is unified through `topLevelCtxMeta` (cross-wave = redirect).
Plan + the two bugs found in verification: `docs/plan/zero-value-wave.md`. Verified:
full suite + `-race` suite + `reuse_test.go` + 40× `-race` `TestBySimulation`, all green.
Key subtlety: re-arm must be **dispatch-only** — a `CloseAndSkimAll` drives an empty
wave to Done during `Close`, so if skim re-armed on Done it would block forever.

**►►► B3 CUTOVER INTERMITTENT HANG — ROOT-CAUSED AND FIXED (2026-06-23).**
The ctxpool body+meta cutover (`e740d33`) intermittently wedged a wave at
`stage=Flushing inFlightWork=0 totalRefs=1` — one funnel instance never flushed, so its
per-instance barrier reference never dropped and the wave never reached Done (`SkimAll`
parked on `state.Done()`).
- **Root cause — a flush-signal subscription race in the funnel flusher.** The
  per-wave flusher goroutine read its end-of-work flush channel *inside* the goroutine
  (`worker.nextJobFlushCh = j.state.FlushChan()` at `funnelengine.go:161`). The wave's
  `Closed→Flushing` transition (`wavestate.noMoreWork`) *rotates* that channel — closes
  the old one (the flush signal) and installs a fresh one. When a funnel is created very
  late and its wave reaches Flushing within ~microseconds, the flusher goroutine can be
  scheduled to run its body *after* the rotation, so it subscribes to the **post-rotation**
  channel (never closed again) and parks forever, missing the wave's one end-of-work
  flush signal. The cutover didn't introduce the race but **widened the window**: the
  flusher's startup now does more work (`ensureCtxMeta` via ctxpool, ~29µs in the trace).
- **Proof (execution trace `trace.out`):** Wave `0x3c80014e2708` did its Flushing CAS at
  t=`005605053696`; its flusher `G=25814` didn't begin executing until `005605111488`
  (~58µs later) and ended its entire trace parked in `popSelect` on the post-rotation
  `nextJobFlushCh=0x3c8001c9e070`. Global tally: 4287 Accumulate vs 4286
  "received job flush signal" — exactly one instance's signal lost.
- **Fix:** capture `FlushChan()` **synchronously in `newFunnelEngine`** (which always
  runs during the wave's Open phase, strictly before any Flushing rotation) and pass it
  into the flusher. The pre-rotation channel is exactly the one closed at the first
  Flushing transition, and a closed channel always fires in `select` — so the signal
  can't be missed no matter how late the goroutine is scheduled.
- **Verification:** 300/300 plain + 40/40 `-race` of the reduced zero-delay repro (was
  reliably hanging pre-fix; pre-cutover baseline 0/150), full `./...` suite + linter
  green. All `TEMP B3.hang` diagnostics reverted.

**►►► SURFACE REDESIGN + DOC-ORG TARGET (LOCKED, 2026-06-21, design review w/ PN).**
A deep design pass converged the user-facing `streampool` surface and the target doc
organization. These SUPERSEDE earlier surface notes (incl. the 2026-06-20 "SURFACE
PINNED" line below) where they conflict. Authoritative until reflected into the docs.

**Surface (locked):**
- **Wave construction + lifecycle:** see the **WAVE LIFECYCLE FINALIZED (2026-06-21b)**
  block below — it SUPERSEDES the value-handle / `NewWave` / `Cancel`-`CancelAndWait`
  thinking that earlier versions of this bullet described. Net: **no constructor**
  (zero-value `var w Wave`, `*Wave`, lazy `ensureInit`), **drain-only** lifecycle
  (`Skim`/`SkimAll`/`CloseAndSkimAll` → `ErrWaveDone`; `CloseAndSkimAll` = terminal
  seal+drain, `SkimAll` = drain without sealing), no `Dup`. Surface evolution +
  rationale: `docs/decisions/surface-lineage.md`.
- **Ops are wave-agnostic** — `NewLauncher(h)` / `NewSkimmer(h)` / `NewFunnel(factory)`,
  no construction wave, no sentinel. Reusable specs definable before any wave (no
  wave-lifetime/creation-order coupling).
- **Routing = ambient + `op.In(wave)`.** In-body `op.Submit(ctx, v)` uses the body's
  ambient (framework-stamped) wave; `op.In(wave)` returns a cheap wave-bound value
  handle for top-level, redirect, or bind-once reuse. `In` (membership: the op's work
  is *part of* the wave) chosen over For/To; routing is handle-level so it never
  perturbs the ctx → Flow/trace propagate across redirects untouched.
- **Dispatch verb `Submit` kept** + **naming convention**: name ops as agent/role
  nouns distinct from their outputs — Launcher→`fetcher`; Funnel→`aggregator`
  (+`totals`); Skimmer→`collector` (+`results`). Rule: "-er for the op, plain noun
  for the output." `Start` = void-Launcher sugar. (Considered `Do`/asymmetric verbs;
  the naming convention makes `Submit` read right and keeps the clean
  `SubmitErr`/`SubmitResult` family + `ants`/`pond` familiarity.)
- **Funnel = wave-scoped**: per-(funnel,wave) accumulator instances owned by the wave,
  force-flushed at wave drain (the per-wave flusher). `Flush(ctx)` (outputs → ambient
  wave) + **`FlushTo(ctx, wave)`** (one-shot; outputs → given wave; FlushFn stays
  wave-agnostic, its ambient overridden — enables snapshot/staged capture, e.g.
  timer-driven). **NO Close/Dup** — finalization is wave-driven (in-flight==0 ∧
  sealed), which subsumes the old Funnel.Dup refcount and counts *all* feeders.
- **Limiters minimal**: standalone composable values — `NewSemaphore(n)`,
  `NewRateLimit(n, d)`; `WithLimits(...)` AND-composition, **jointly admitted in a
  global canonical order** (deadlock-free by lock-ordering, automatic, no object).
  **NO user-facing Coordinator/Scheduler, no `.Under`/grouping.** Ordered joint
  admission needs only the global order; the prioritized discipline (the only thing
  needing a central arbiter) is a single *internal* global arbiter, not exposed.
- **Flow kept, value handle**, `ctx, flow := NewFlow(parent)` — two-return (Flow *is*
  ctx-borne propagation, the deliberate exception to "no ctx from constructors").
  **Keeps Dup/Close** — the one legitimate refcount survivor (spans multiple waves; no
  single wave bounds it). Framework auto-ref/unrefs per work item; `FlowFromContext` =
  non-counting view; propagation requires ctx hygiene.
- **Three-type framing fixed**: user-facing types are **Wave + Flow** (+ ops as
  verbs); **Pool is internal** (auto-sized), mentioned only to explain sizing.
- **Deferred (no API named — own design effort)**: a declarative op-and/or-wave
  **scheduling priority** feeding work-dispatch ordering *and* the internal permit
  arbiter; anti-starvation-tempered; intake-side only; one internal global arbiter,
  no user grouping.

**Doc organization (target; strict only on release-able branches — refactor branches
may have docs lead code):**
- User-facing = **current state**: README, `doc.go` (absorbs `programming-model`),
  per-symbol API comments, `example_*_test.go`.
- **`docs/` root** = maintainer, current-state design.
- **`docs/decisions/`** = target-state design + rationale + superseded designs.
- **`docs/plan/`** = path-to-target (migration/sequencing); empty/deleted at rest.
- Positioning: concise comparison in README; deep evidence (ARCHITECTURE_COMPARISON
  source analysis, POSITIONING_RESEARCH) → `docs/decisions/`.
- `API_DESIGN.md` → reborn as the `docs/decisions/` target-surface record;
  `programming-model.md` → folds into `doc.go` (deferred to the code migration).

**►►► WAVE LIFECYCLE FINALIZED (2026-06-21b) — supersedes the Wave bullet above**
(the single-return / value-handle / adopt-parent-from-first-use thinking). Strict
stance: **ctx is DRIVER-SPECIFIC.** Like the internal Pool, a Wave owns NO ctx.
- **No constructor.** Ditch NewWave AND NewChild. `var w streampool.Wave` (zero
  value usable). A sub-wave is just a zero-value Wave first-used inside a body.
- **No Wave Cancel / Wait / CancelAndWait.** The only lifecycle op is the drain:
  `Skim` / `SkimAll` / `CloseAndSkimAll`, returning **ErrWaveDone** when
  in-flight==0 ∧ sealed. `SkimAll(ctx)` IS the structured scope; ctx is the driver's.
- **Cancellation = the drive ctx (pure).** Cancelling the SkimAll ctx → it returns
  ctx.Err(); in-flight work keeps running under its own submit ctxs (cancel those —
  usually the same ctx — to stop it). NO framework force-abort. No goroutine leak:
  workers belong to the GLOBAL pool (not per-wave); `streampool.Wait()` stops idle
  workers and joins them.
- **Lazy init + reuse.** A zero Wave self-inits its substrate on first ctx-bearing
  use (op dispatch into it, or Skim/SkimAll) via a race-safe `ensureInit(ctx)`, and
  is reusable after the drain returns (no explicit teardown call). The creation-time
  `parentJobs`/`shells.Init` stamping (was in NewWave) MOVES to `ensureInit`, keyed
  on the first-use ctx = the driving body's ctx → captures the DRIVING ancestry.
- **Driving ancestry only.** Parentage for limiters (permit inheritance) and the
  "can't skim a wave you're part of" guard is the DRIVING goroutine's ctxMeta
  nesting, captured at drive — never a creation/cancellation parent.
- **Funnel flusher re-homed** to exit on wavestate→Done (in-flight==0 ∧ sealed),
  not a CancelAndWait join.
- Wave stays `*Wave` (no value conversion); bind with `op.In(&w)`. The user owns
  pooling (`sync.Pool[*Wave]` or reuse a var). 3b (single-return NewWave) and 3c
  (value handle) are MOOT.
- **Context mechanism CONVERGED (2026-06-21/22, w/ PN) → `docs/decisions/body-context-pool.md`.**
  How "Wave owns no ctx" actually works. **The model converged on TWO decoupled pools**
  (a deliberate shift away from the single fused `{ctxMeta, childCtx}` unit the note's
  body still describes — reconcile the note on the next doc pass):
  1. **`internal/ctxpool`** reuses the **child `context.Context`** objects: a
     process-wide map of parent ctx → `childPool`, each handing out reusable
     `WithValue`-descendants of the parent (found by direct `ctx.Value`), auto-evicted
     via `AfterFunc` on parent cancel. Children pooled per-`childPool` (`nbcq`); the
     `childPool` struct itself is GC'd, not pooled (cold-path alloc; safe recycle would
     need a hot-path refcount — see the in-code comment).
  2. **A separate `*ctxMeta` pool** (streampool layer, B3) reuses the **values**:
     borrow a meta, stamp it (wave/ctxType/parent/heldRequest/parentJobs via
     `parentJobsFor`), set as the child ctx's value; return to its own pool on `Free`.
  Values pooled INDEPENDENTLY of contexts. Cancellation = pure source-ctx ancestry.
  Borrow → stamp → run → return. Three disciplines: **A** per-execution (async:
  work-item `Free`; inline: scope `defer`), **B** per-drive (skim), **C** per-lifetime
  (flusher). Collapses `waveCtx`/`execShell`/`Cancel`/`CancelAndWait` + the per-wave
  ctxMetaMaps. Plain ownership + GC, no refcount. Borrow-site map (#1–#4) in the note.
  - **IMPL STATUS (2026-06-22):**
    - **`ctxpool` LANDED** (`51554d5`) — generic child-ctx reuse + eviction; tested,
      unadopted. (Caught+fixed a draft bug: embedded zero-value omnipool silently
      defeated ctx reuse → per-`childPool` `nbcq`.)
    - **`bodyCtxPool` DROPPED** (`b7c132d`) — the fused single-pool design (`c159d25`);
      reverted the `ctx`/`pool` fields on `ctxMeta`. Stamp logic (`parentJobsFor`)
      preserved in history at `c159d25` for B3 re-derivation.
    - **`metaFromContext` read-seam LANDED** (`a8f05e8`) + **body-context borrow
      primitive LANDED** (`1a8d404`, `bodyMetaPool`/`borrowBodyContext`, unadopted).
    - **B3 SCOPE DISCOVERY + DESIGN CONVERGED → `docs/decisions/meta-context-migration.md`
      (rev 2, `c7abcdc`).** Migrating bodies to ctxpool is inseparable from migrating the
      meta-derivation machinery (ensureCtxMeta family): it finds source metas via
      ctxMetaValueKey (ctxpool bodies use childKey) and caches by ctx identity (ctxpool
      reuses ctxs). Design CONVERGED (w/ PN) to ONE unified model — **no lifecycle fork**:
      every meta is a ctxpool borrow differing only in hold scope (per-execution bodies /
      **per-call drivers** — NOT singletons; pooled N-at-once under concurrent driving)
      and ctxType. One lookup (`metaFromContext`=`GetValue`); `ctxMetaValueKey`/
      `ctxMetaMap`/`skimCtxMetaMap` all retire; cross-job derivation dissolves into the
      borrow. Rule: **borrow-at-entry, read-while-nested**. Decided: top-level
      Start/Submit drives a backpressure trySkim → **two nested borrows** (top-level ⊃
      skim) + the submitted work's separate body meta. `topLevelExEnv.Lock` removable
      (per-call metas are single-threaded — verify).
    - **CUTOVER LANDED (`e740d33`, 2026-06-23):** B3.meta + B3.A/B in one step —
      `metaFromContext` is ctxpool-aware; task and funnel bodies borrow via
      `borrowBodyContext`; `ensureCtxMeta` stamps a ctxpool child (no `AfterFunc(j.ctx)`).
      The cutover took the incremental path (dual-lookup read seam); the legacy
      `ctxMetaValueKey` read branch + `ctxMetaMap`/`skimCtxMetaMap` have since been
      RETIRED (2026-06-23, with the zero-value Wave cleanup): `metaFromContext` is now
      pure `ctxpool.GetValue`, and `internal/ctxmap` (the maps' backing) is deleted.
    - **POST-CUTOVER HANG FIXED (`00f7abc`, 2026-06-23):** flush-signal subscription race
      in the funnel flusher (see the top banner). 300/300 + 40/40 -race green.
    - **B3.C LANDED (2026-06-23):** runtime force-abort gutting was already in the cutover
      (Wave owns no `waveCtx`/`execShell` pool; `Cancel`/`CancelAndWait` no longer
      force-abort bodies). This pass removed the residue: the dead `worker.Pool.PoolCtx()`
      method (zero callers — it existed only so waves could derive a teardown `waveCtx`),
      the stale `Cancel` doc (it claimed it cancels task contexts), and `execShell`/
      `wave-5b` comment references across pool.go/job.go/ctxmeta.go/ctxpool.go.
    - **B3.D LANDED** (in the zero-value Wave migration, 2026-06-23): examples + sim
      reconciled to the new cancellation model (cancel the submit/drive ctx to stop a
      body; the Wave owns no ctx). The B3.meta unified-model retirement is also DONE
      (`metaFromContext` → pure `GetValue`; `ctxMetaValueKey` + the two maps removed).

**►►► DOC CONSISTENCY SWEEP (in progress, 2026-06-20).** Bringing all docs in line
with the converged target design. Committed so far this session: permit-core.md (new
spec); reconciled dispatch-execution-split.md, limiter-suspend-resume.md,
global-substrate-activation.md (banners), backpressure-and-reentrancy.md,
programming-model.md (reentrancy/scatter rule); trimmed dispatch.go; deleted
REVIEW_FINDINGS.md (code citations repointed to limiter-suspend-resume.md).

Root-doc triage done (no deletions needed beyond REVIEW_FINDINGS): CHANGELOG /
POSITIONING_RESEARCH / ARCHITECTURE_COMPARISON keep-as-is.

**DONE this session (also):** programming-model.md refactored into a focused
streampool guide (732→221 lines); its comparison section relocated into
ARCHITECTURE_COMPARISON.md. README swap done (old README deleted, README-proposed →
README, updated to the pinned surface). API_DESIGN.md reconciled (top banner + inline
fixes: Pool un-exposed, permit cache not global scheduler, reentrancy rule,
principle 7).

**REMAINING (process/roadmap docs — lower stakes):**
- **REFACTOR_PLAN.md** (now `docs/plan/`) — reconciled 2026-06-21: candidate-waves
  marked landed/superseded, the REVERSED Pool↔Wave direction fixed (Pool folded
  INTO Wave, internal), op-names refreshed, and the missing dispatch/execution-split
  + permit-core architecture wave added. Live status still lives in WORKING_NOTES.

**DONE since (this block was itself stale):**
- TODO.md "global permit scheduler" — already reads "no global scheduler" in the
  REFRAMED banner; op-names refreshed; the wave-5b section rewritten to the
  2026-06-21b finalized lifecycle (force-abort design retired).
- ARCHITECTURE_COMPARISON.md / API_DESIGN.md / POSITIONING_RESEARCH.md already live
  under `docs/decisions/`; REFACTOR_PLAN under `docs/plan/`. (The relocated
  comparison may still carry minor generic "scatter-gather-combine" phrasing.)

**SURFACE PINNED this session:** `streampool` package; no user-facing `Pool` (sizing
automatic; concurrency via Limiters); drop scatter/gather/PSG vocabulary; dispatch
verb `Submit`; `Flow` kept as the optional third concept. Go-forward calls made in
the guide/README/API_DESIGN — ratify or correct.

**►►► DEVELOPMENT HISTORY (excised 2026-06-23).** The dated implementation/design
journal that used to live here — the architecture pivot, the `psg.Pool`→`Wave` fold,
rdvq/workq consolidation, limiter suspend/resume, and earlier hang root-causes — now
lives in **`docs/decisions/development-history.md`**. This file keeps current status
only. (Relative "above"/"below" cross-references in the sections below that point into
that journal now resolve in the history doc.)

### Next session pickup

**Recent — allocation reduction on the comparison bench (2026-06-30).** Drove streampool
per-dispatch allocations **~37 → ~1.1 allocs/task** (the per-task-closure floor every
bounded pool pays), now flat across underload→heavy-overload: meta pooling (37→19),
gen-stamped inbox + `h.release` method-value cache (→7.3), abandon-path hint **reap**
(→2.6), `confirmFn` method-value cache (→1.1). Commits: `d1d6484` (bench fix), `ba65991`
(`h.release`), `21c74f7` (reap), + `confirmFn`. Harness = the `bench/` comparison submodule
(unbounded / chan-semaphore / naive-pool / streampool). The waiter-set design exploration
this prompted — is FIFO desirable, caller-held `Receiver`/`Waiter` params, permit-forest
affinity bucketing — is recorded durably in `docs/decisions/waiter-set-notification.md`
(net: keep the shared reclamation substrate + reap + per-use ordering; affinity bucketing
deferred, measurement-gated on a deep-forest workload that doesn't exist yet). Remaining
gap to naive-pool is the unavoidable shared per-task closure. Possible next bench work: the
**nested-dispatch** workload (where a naive pool deadlocks and the split should pay off) —
the comparison so far exercises streampool's overhead, not its differentiator.

**►► NEXT SESSION PICKUP (2026-06-30 → , order of readiness):**
1. ~~**`RenotifyFunc` → `Notification` refactor**~~ — DONE (`eee5322`, 2026-07-01). See the
   top banner + `docs/decisions/waiter-set-notification.md` (Status → Landed). The value
   struct settles as `Received()`/`Forward()` (no `Consume` — a no-op on a value receiver;
   productive use just drops the wake); `Empty()` became `Received()` (positive predicate).
2. **`select` scase-escape dig** (self-contained) — the residual ~0.06 alloc/task on the
   park path is the `select`'s scase array escaping behind `PopFrontFunc`'s callback
   indirection (`executorWorker.Wait`/`Wave.skimSelect`), NOT a closure (confirmed: caching
   the callback didn't move it). Investigate whether the scase escapes due to the indirect
   call and/or generic instantiation, and whether restructuring the callback seam avoids it.
3. **nbcq interface→value-struct audit** — nbcq can carry value structs now (historically
   pointer-only); sweep `Queue`/`Handoff` value types for interface/pointer values that
   could be value structs (leaner). Low priority.

**►►► NEXT = C2 — the pool-split cutover.** Phase 2b migration steps 0/C1/B are landed
(commits `ae6339f`, `551f4e6`, `cfdb039`); the example fix is `565b3b6`. C2 is the big one
and reshapes the live dispatch path. Full design in
`docs/plan/dispatch-execution-split-phase2b.md` ("The pool model", "The queue model",
mode mapping, governor placement, migration sequencing). Shape:
- **Executor pool** = `worker.Core[E]` (from B) + an `rdvq.Handoff[T]` (from B) + a simple
  `PopFront → Run` per-worker loop (no `workq.Worker`, no `TryPopFront`). Its demand is
  **block-as-demand**: a scheduler's `Handoff.PushBack` that finds no parked executor parks
  and triggers `Core.TrySpawn` — wire this `PushBack`-park→spawn hook on the `Handoff`
  (deferred from B; B1 left `Handoff` pure).
- **Scheduler pool** = the existing `worker.Pool` over `workq.Accepted`; per item:
  non-blocking `permits.Cache.Acquire` + governor check, then **blocking `Handoff.PushBack`**
  the admitted body to the executor; postpone on a permit miss. Top-level admission stays
  inline on the driver (skim-retry / block-and-help).
- **Gate:** full suite + `-race` + `TestBySimulation` + the **latency/alloc benchmarks**
  with the REAL methodology (heavy-tailed blocking-I/O work, P99/max, swept P:D ratios —
  see `[[feedback_bench_methodology]]` / BENCHMARKING.md), not a throughput microbench.
- **Recommended:** map it first (like C1/B did) before touching code — it's the
  architecture+latency milestone.

**Deferred backlog (after / alongside C2):**
- **C3 — drain limiting** (`WithLimits` on `NewSkimmer`, `WithFlushLimits` on `NewFunnel`);
  needs the limited-drain model-check (parked holder whose drain needs a permit).
- **C4 — residual cleanup**: any dead `BlockBehavior`/`shouldBlock` plumbing once C2
  reshapes dispatch. (No wake-efficiency item — the `Notifier` single-wake + renotify
  conservation already wakes exactly one consumer, so there's no thundering herd to fix.)
- **Multi-limiter** joint admission (currently panics at `opConfig.singleLimiter`); **weighted
  resources** (re-derive the removed `applicant` sizing natively).
- **Thread C** — `Try*` honoring non-zero non-Forever deadlines via bounded-wait
  (`Forever` sentinel + `dispatch (bool, error)` foundation at `5dc49c7`).
- **psgwf legacy-name retirement**; **bench.txt regeneration + chartgen alignment** (new
  metric names, e.g. `funnelLimit`).

## Open issues

### Deadline propagation in taskPostWork

`taskPostWork.newTaskPostWork()` receives a `deadline` parameter but doesn't store or use it. Sibling scatter work types (`taskPoolScatterWork`, `combineScatterWork`, `gatherScatterWork`) store and use theirs. Should add a `deadline time.Time` field and pass it to `BasicPushSelect` via context.

### Renotifier lifecycle (`wrappedRenotify` only now)

`rdvq.RenotifyFunc` is a bare `func()` with no `Free()`. After the orphan elimination, the only remaining workaround instance is `wrappedRenotify` in `internal/rdvq/notifier.go`, which self-frees inside its renotify callback — works only if the renotifier is invoked, leaks if it's replaced or discarded. Long-term: change `RenotifyFunc` to a `Renotifier` interface with `Renotify()` and `Free()` so the rdvq infrastructure can free unused renotifiers in all cases. Less urgent now that `orphanedTaskRenotify` is gone — only the rdvq-internal one remains.

Files affected: `internal/rdvq/notifier.go`, `internal/rdvq/waiters.go`, all `Notify()` callsites.

### ExecuteOrWait duplication

`taskPostWork.Execute` implements ~80 lines of try/subscribe/block logic that overlaps with `workq.ExecuteOrWait` and `workq.Governor.Execute`. It has unique requirements (custom `TryPushBack`, demand-registration side effects, blocking via `PushBackFunc` + `BasicPushSelect`) so it isn't a trivial extraction. Possibly worth a `TryPostBehavior` abstraction if other places grow similar shape, but not urgent.
