# PSG-Go Combiner Branch Working Notes

This document contains working notes and context for development on the `combiner` branch.

Major combiner architecture work is complete. Branch is now in cleanup and finalization phase.

## Limiter suspend/resume — DESIGN SETTLED + REVIEWED, ready to implement (2026-06-11)

**The design is finalized and written up in `docs/limiter-suspend-resume.md` —
that note is the source of truth — and has passed a full design review:
`REVIEW_FINDINGS.md` (all 9 findings resolved, 2026-06-10/11) is the review
record, with every resolution folded back into the note.** This section is the
working summary; the older "ROOT-CAUSED" section below is accurate *background*
on the bug, but its fix sketch (a `heldPermit` carried as a ctx value) is
**superseded** by the handle/scheduler/resource design in the note.

**The bug (confirmed):** intermittent `TestBySimulation -race` busy-spin
livelock — a concurrency permit held across a blocking skim (a body driving a
subwave via `CloseAndSkimAll`) starves a sibling unit of the same op that needs
the same permit. Pre-existing; orthogonal to the funnel work. Repro: **non-short**
`-race` (short mode suppresses it — it needs deep paths/subwaves). Signature: ~20
goroutines, **zero** mutex/sema waiters, spinning in `ExecuteOrWait`/`sim.Run`.
Baseline measured this tree: **3 hangs / 30 iterations** (12 checks each, 360
non-short `-race` checks). Build the loop with
`go test -c -race -o /tmp/psg.race.test .` then run
`/tmp/psg.race.test -test.run '^TestBySimulation$' -rapid.checks=N -test.timeout=…`
in a loop, treating a `-timeout` panic as a hang.

**The fix (design):** a concurrency permit gates *active computation*, not
*blocked-waiting*. **Two-class park rule:** suspend at delegation-shaped waits
(gathers + block-and-help — both have deadlock witnesses in the note); hold
through pure capacity waits (blocking posts — the held permit IS backpressure
propagation; safe by "a permit wait never occupies bounded queue capacity").
Mechanism:
- A limiter is driven by the framework through a **request handle** (state
  machine `PENDING → HELD ⇄ SUSPENDED → DONE` plus `HELD ⇄ POSTPONED` for
  granted-then-yielded pre-body grants) with `tryAcquire`/`postpone`/`suspend`/
  `tryResume`/idempotent `release` + phase-routed `notifier` (never nil).
  Illegal transitions **panic**; capacity-returning transitions (suspend,
  postpone, HELD-release) notify, state-discarding ones don't.
- `limiterImpl` is implemented by a **scheduler** (sealed, few): a *direct*
  scheduler over one resource now; *ordered*/*prioritized* later. The scheduler
  owns all lifecycle/routing/discipline.
- A **resource** (open extension point) is pure accounting: `demand(applicant)` /
  `tryAcquire(amount)` / `release(amount)` / `suspendable()` /
  `setCapacityChangedFn(func(delta int))` (bind-time growth hook; channel
  rejected — unconsumed-ping hole). The semaphore is a ~10-line resource.
- `ExecuteOrWait` splits into **routing + a shared `blockingAcquire`**;
  **reclaim is help-shaped** (plain-wait reclaim deadlocks — witness in the
  note); help domain = the pool whose skim context the goroutine occupies.
- Externally-serialized permit scoping via `ctxMeta.parent` + **fresh-root
  worker contexts** (explicitly severed at worker-context creation) + always-on
  `assert(meta.parent == nil)` at handle-stamp time. **Skimmers stay
  limiter-free** — load-bearing for the hold-through class (skim handlers are
  the drain; drain must stay permit-free).

**User-facing API delta (now):** `NewSemaphore(nil, n)` (scheduler is the
mandatory first arg, nil = self-scheduled; only nil supported yet). The
Semaphore now caps **active** concurrency, not in-flight — needs a CHANGELOG
entry + a doc update on `Semaphore`. Everything else (scheduler/resource
constructors, exported `Resource`, `NewLimiter`) is additive/deferred.

**Implementation plan — execution order #1, #2, #3, #5, #4, #6** (each
checkpoint independently green; the hang baseline persists until #4 lands —
expected):
- **#1 — DONE (`c96db84`).** Limiter core: `resource` iface (with
  `setCapacityChangedFn` hook) + semaphore resource + direct scheduler + the
  handle (full state machine incl. POSTPONED; illegal transitions panic).
  Unit tests pin: notify discipline (wake exactly once; discards silent);
  no double-credit; legality panics; suspend-frees-slot; growth wakes
  postponed listeners. **Deviations:** (a) the `ExecuteOrWait`
  routing/`blockingAcquire` split is deferred to #3 — it has no consumer
  until the gates drive handles, and landing it dead would only draw lint;
  (b) a legacy `tryAcquire`/`release`/`notifier` shim remains on
  `limiterImpl` so the existing `limiterScatterWork`/`funnelWork` gates stay
  behavior-identical until #3 deletes it (legacy release now notifies
  unconditionally per the new HELD-release rule; differs from the old
  under-limit check only while draining a `SetMaxConcurrency` shrink —
  benign extra wakes); (c) request handles are not pooled yet — add
  pooling in #3 when the gates own the lifecycle.
- **#2 — DONE (`2397419`).** `ctxMeta` permit scoping: `parent` link (set to
  `sourceMeta` at creation in `ensureCtxMeta`) + `heldRequest` field (stamped
  in #3); task/funnel worker contexts explicitly sever `parent` at creation
  (subjob `j.ctx` carries the dispatching body's foreign-pool meta);
  `currentHeldRequest` walks parent, stops at first stamped handle. Tests pin
  walk semantics + chain topology through real flows (task/subwave/skim/
  funnel). Note: psgwf `Example_clientTimeout` flaked once during
  validation — the known pre-existing timing flake (TODO.md), passes 7/7 on
  re-run; not related.
- **#3 — DONE (`2f27619`).** Gates drive request handles; legacy shim +
  `limiterCompletedFn` deleted. `acquireOrWait` (+ pooled `requestBlocker`
  latch) is the routing/blockingAcquire split, in `limiter.go`. Task path:
  request created at dispatch (applicant = `launcherWork`), gate in
  `limiterScatterWork` (`postpone()` on granted-but-not-started), handle
  travels in `taskWork` (stamp in Execute w/ root assert; release at
  completion via `completedFn`; idempotent backstop + recycle in Free).
  Funnel path: lazy request in `funnelWork` (persists across postponed
  retries), stamp in executeInner, release at body end, backstop in Free.
  Handles pooled (`directRequestPool`; single recycle point = owning work's
  Free). End-to-end test pins subwave-finds-body-handle via parent walk.
  Baseline hang observed 1/3 full-suite runs — expected until #4.
- **#5 — DONE (`bbe4211`).** Sim active-concurrency measurement
  (`limiterTracker` threaded down the Func walk; Subjob step drops/restores
  the body's contribution — under-counts pre-brackets, so green) +
  cross-subjob limiter sharing (`Limiter.InheritFromParent` /
  `LimiterConfig.Inherit`; controller aliases the parent's `psg.Limiter` AND
  tracker; child skips the assertion the owning ancestor performs).
  **Deviation:** the generator's default `Inherit` probability ships as **0**
  and must be **flipped to ~0.25 in the #4 commit** — enabling it before the
  brackets land makes the shared-limiter deadlock witnesses reachable and
  would make even the `-short` suite (pre-commit gate) hang-prone. Machinery
  is fully tested meanwhile (controller-aliasing unit test + hand-built
  two-level inherited-limiter plan at permits=2, contention-free).
- **#4** Bracket suspend-class episodes (skim methods + the block-and-help
  wait) with suspend + **help-shaped** reclaim (re-entrant via the already-
  SUSPENDED no-op; cancellation leaves SUSPENDED so completion `release`
  discards; help domain = the current pool's skim context).
- **#6** Verify: `go vet`, `go test -short`, `.githooks/pre-commit`, then the
  non-short `-race` loop → target 0 hangs over a large sample (now including
  the shared-limiter topologies), no concurrency-assertion failures, no
  permit-accounting panics.

**Key invariants to preserve:** externally-serialized handles (never touched
concurrently; HB edges via queues/notifiers — listed in the note); the
two-class park rule (suspend at delegation waits, hold through capacity
waits — the *classification* is the contract, not a site list); stamped
handles only ever HELD/SUSPENDED; skimmers limiter-free; active-concurrency
measurement in the sim. See the note's "Rejected alternatives" for dead-ends
already explored (flat tokens, selectFn inversion, 2PC reservation,
counted-suspend barrier, uniform-suspend-everywhere, plain-wait reclaim,
capacity-growth channel).

## TestBySimulation `-race` hang ROOT-CAUSED: limiter held across a blocking gather (2026-06-07)

**This is the actual gate-blocking bug** — pre-existing, in core backpressure/limiter code,
**orthogonal to the 1c-ii funnel rewrite** that this session started on. The 1c-ii work was
aimed at a theorized funnel `c.mu`/`SetPosition` deadlock that is **not** what fails the gate.

**Symptom:** intermittent `TestBySimulation` hang. Across ~20 captured hangs (baseline + WIP,
race + non-race) there were **zero mutex/sema waiters** — it is a **busy-spin livelock**, not a
blocking deadlock. Baseline (old code) reproduces it, so it is pre-existing.

**Root cause (trace-confirmed):** an op (a Funnel instance, or a Launcher task) holds a `limit=1`
concurrency **limiter permit** while it is **blocked in a gather** (`Skim`/`SkimAll`/
`CloseAndSkimAll` — e.g. a Funnel `Accumulate` that synchronously runs a subjob via
`runSubjob → sim.Run → Wave.CloseAndSkimAll`). Other work **in the same (sub)job** that needs the
same single-permit limiter can never acquire it (the holder is parked, not releasing), so the
backpressure **block-and-help** path (`Pool.block → ExecuteOne → addWorkWhileMaybeBlocking →
rdvq.Waiters.WaitFunc`) spins forever (the rdvq item counter climbed to ~920k in the captured
trace). The sim builds **fresh** limiters per (sub)job (`sim.Run` → new controller + `NewWave` +
`NewSemaphore`), so this is within-one-(sub)job contention, telescoped by recursion — NOT
cross-job reuse. Decisive confirmation: making all limiters unlimited → **0 hangs / 250** (vs
~1/25 with limiters).

**THE FIX (agreed with PN): a limiter suspend/resume protocol.** A permit gates *active
computation*, not *blocked-waiting*. The framework suspends the held permit at **every point
where the holder blocks waiting on other work** — gathers AND the block-and-help wait on a
backpressured Submit — and reacquires (blocking) on return. The **limiter arbitrates** what
suspend/resume mean:
- `limiterImpl` gains `suspend() bool` + `resume(ctx) error` (distinct from `tryAcquire`/`release`
  — you can't reuse acquire/release because for a rate limiter `resume`/reacquire would wrongly
  *re-pay the rate*).
- **Semaphore:** `suspend` = give back the concurrency slot (`inFlight--`, notify); `resume` =
  block until a slot is free (`inFlight++`, wait on the notifier). (Re-takes a *concurrency* slot,
  not a new admission.)
- **Rate limiter:** `suspend`/`resume` = **no-op** (admission paid once at start; nothing held
  during the op; no deadlock to dissolve, nothing to re-pay).
- **Combined:** suspend the concurrency dimension, leave the rate dimension.

**Implementation sketch:**
- A `*heldPermit{ impl limiterImpl; suspended bool; mu }` carried as a **ctx value** (NOT
  `ctxMeta` — it must survive the subjob's `NewWave` so the subjob's `CloseAndSkimAll` sees the
  parent's hold). Set when an op runs its body under a limiter (task dispatch + `funnelWork.Execute`).
- `Skim`/`SkimAll`/`CloseAndSkimAll` (and the block-and-help wait point): on entry, if a
  `heldPermit` is present and not already suspended, `suspend()` it; `defer` a **blocking**
  `resume(ctx)`. The `suspended` flag makes nested gathers no-ops (re-entrant safe).
- `completedFn`/`funnelWork` release at op-completion must consult the `heldPermit` so a
  cancellation mid-gather (suspended) does not double-release.

**Why it telescopes over arbitrarily-nested subjobs:** a subjob is only ever entered *through* a
gather (`CloseAndSkimAll`). Level *k*'s op holds `Lₖ`; to run level *k+1* it must gather → that
suspends `Lₖ` for the whole child → level *k+1* holds/suspends `Lₖ₊₁` at *its* gathers, etc. So no
`Lₖ` is ever held across the wait for level *k+1* at any depth. Any subjob code is either (a) on a
different goroutine with its own holds/gathers, or (b) synchronous on the parent's goroutine —
which is *by definition* inside the parent's gather (permit already suspended). No third case.

**Invariant to preserve:** *the only points where a permit-holder blocks-waiting or synchronously
runs other work on its own goroutine are the gather calls (and block-and-help submit points,
which fold under the same suspend rule).* Today block-and-help drains the **skim** queue (gated by
*different* limiters than the holder's scatter-side permit), and funnels postpone rather than
block-and-help — so a holder never synchronously runs work needing its own permit, and
gather-suspend alone is already sufficient. Suspending at the submit block-and-help point too is
the airtight/uniform rule (and frees the slot during the wait — strictly better concurrency).

**Rejected alternative:** "demote subjob submits to non-top-level so they postpone." Wrong lever
— the cause is the *held permit across a wait*, not the *top-level label*; a subjob's
`controller.Run` is a legitimate driver that should block-and-help; and it tangles with the
dispatch contract (the `ctxMeta.ExecuteNowOrQueue` `AddToListeners` panic-stub requires
`blockFn != nil` when `ShouldBlock()` — nilling `shouldBlock` for subjobs panics; tried, reverted).

**Sim trace-debugging toolkit (used to find this; has stale markers to fix):**
`PSGTRACEINTERNALS= go test -trace=/tmp/trace.out -rapid.checks=1` (loop until a `-timeout` hang
leaves a usable trace), then `internal/sim/analyze-sim-trace.sh` → `fmttrace`
(`internal/cmd/fmttrace`, separate module) → plan/started/completed/`incomplete.txt`;
`internal/cmd/fmttrace/{find,extract}-goroutine*.sh` to drill into a goroutine. **Stale (combiner
rename):** `extract-sim-trace.sh` greps `sim.Run: Test plan:` but the sim now logs the plan as
`%v` (`Plan#N…`); `extract-sim-completed.sh` greps `step M/M: done` but the sim logs `… ends at`.
Fix either the scripts or restore the markers in `internal/sim/run.go`. (Worth a reusable
debugging skill.)


## Architecture Highlights (Completed)

**Core Infrastructure:**
- LIFO stack architecture for natural worker scaling (eliminates controller complexity)
- Leakguard package for safe resource handle management with finalizer-based leak detection
- Demand-based worker spawning with explicit `taskWorkerDemand` counter
- Hardware-accelerated 128-bit atomics in nbcq for improved performance
- rdvq channel-based selectFn API: user code receives raw channels and returns small result types; `Inbox`, `Outbox`, `WaitInbox` are unexported

**Key Design Patterns:**
- Reference-counted CombineOp/GatherOp handles with Dup()/Close() semantics
- Pool-segregated combiner instances to avoid complex cross-pool handoff
- Trait-based generic collection system (FIFO for fairness, LIFO for scaling)
- Subscription-based coordination with Notifier/Listener pattern
- `taskPostWork`/`combinePostWork`/`gatherPostWork` use composition via `workq.Work` interface delegation; the workq framework keeps work items alive across retries so demand state persists.

## rdvq BufferedFunc Ordering Fix (2026-05-09)

The intermittent `unbalanced decrement detected` panic in `TestBySimulation` and the long-standing benchmark livelock both traced to a race in `rdvq.Queue.PushBackFunc`: `bufferedFn` ran *after* `q.fullOutboxes.PushBack` and `Notify`, so a receiver could pick up and recycle the buffered value before `bufferedFn` fired. `taskPostWork.bufferedFn = registerDemand` mutates per-message state (`taskWork.demandRegistered`) and the global demand counter; when it ran late on a recycled `taskWork`, the demand counter drifted negative, `IsZero()` started lying, and spawning stalled.

**Fix:** Move `bufferedFn()` before `q.fullOutboxes.PushBack(outbox)` in both PushBackFunc paths in `internal/rdvq/queue.go`. Documented contract on `BufferedFunc`: it runs synchronously and completes before the value can be observed by any receiver. Pinned with `TestQueue_BufferedFuncOrdering` (fast and slow paths). Companion psg changes: `taskWork.demandRegistered` now `atomic.Bool` (was a non-atomic bool touched from multiple goroutines), decrement-in-`Free()` for work freed before pickup, `Reset()` panic as invariant check.

**Implications for previously-planned work:**

- The "Benchmark Deadlock Issue" analysis (originally diagnosed as a circular task↔combiner queue dependency) was a misdiagnosis — the actual cause was the demand-counter corruption above. With the rdvq fix, that hypothesis no longer needs investigation.
- The "Task Worker Demand-Based Spawning v3 (dedicated spawner goroutine)" design was motivated by a livelock that turns out to be the same bug. The current `trySpawnTaskWorker`-from-demanding-sites model is sufficient now that the counter is honest.
- The "Combiner Worker Demand Tracking" plan was framed as CRITICAL because of the same misdiagnosed livelock. The existing two-trigger model — `maybeSpawn` (posting side) plus `unmetDemandFn` (receiving side, via `workQueue.ExecuteOne`) — covers the spawn decision points. The remaining "all combiners blocked on gatherQueue" scenario produces correct backpressure-by-design, not livelock.

## Verified-clean review (2026-05-09)

After the rdvq BufferedFunc ordering fix, did a once-over for analogous issues:

- **`internal/cpstate/state.go`** — `spawnedGoroutineCount` is reservation-coupled: every successful `ShouldSpawn{First,}Goroutine()` is paired with a `GoroutineExiting()` (or `GoroutineExiting → GoroutineRestarted` for end-of-work confirmation). Retry loops in `Should*` handle concurrent reservation contention correctly. Cancel/shutdown branch in `combinerpool.go` deliberately skips `GoroutineExiting()` (the comment is accurate — accounting doesn't matter once the job is shutting down). No drift potential analogous to `taskWorkerDemand`.
- **`gatherQueue` notification path** — `gatherPostWork.Execute` passes `bufferedFn = nil` for both `TryPushBack` and `PushBackFunc`, so the rdvq race never affected this path. Combiners blocked on `BasicPushSelect` for gather posting wake via standard buffered-channel mechanics when a gather receiver drains the outbox; no separate notification needed. Receive side (`gatherSelect`) uses standard rdvq waiter patterns.

## rdvq API cleanup and orphan elimination (2026-05-09 / 2026-05-10)

The rdvq receive/wait/push APIs were reshaped so user code interacts only with channels and small result types — no more `Inbox`, `Outbox`, or `WaitInbox` structs in user signatures, and no more `Emptied()` / `Filled()` bookkeeping calls. All three of those types are now unexported.

Final shapes (user-visible):

```go
type PopSelectFunc[T any] = func(inboxCh <-chan T, outboxWaitCh <-chan RenotifyFunc) PopSelectResult[T]
type PushSelectFunc[T any] = func(outboxCh chan<- T) bool
type WaitSelectFunc       = func(waitCh <-chan RenotifyFunc) RenotifyFunc

func (q *Queue[T]) PopFront(ctx, receiver) (T, error)
func (q *Queue[T]) PopFrontFunc(receiver, selectFn) (T, bool)
```

`PopSelectResult` has unexported fields and mutator methods (`InboxEmptied`, `OutboxReady`) that panic on misuse (nil renotifyFn, double-call, both-set). Idiomatic selectFn pattern: declare a named `result` return value, call `result.InboxEmptied(v)` / `result.OutboxReady(rf)` in the matching select case, bare-return everywhere — including the "neither fired" case.

The orphan-elimination plan (a long-standing TODO) became feasible during this cleanup: a registration-order flip inside `Queue.PopFrontFunc` (waitInbox before stack-inbox) made the dual-fire race that motivated psg's orphan task queue structurally impossible. With that, `Job.orphanedTasks`, `Job.orphanWaiters`, `orphanedTaskWork`, `orphanedTaskRenotify`, and `Job.tryGetOrphanedTask` were all removed. The runTasks selectFn collapsed from a nested orphanWaiters-wrapped wait to a flat select on inbox + outboxWait + idleTimer + ctx.

Companion fix: in `Queue.PopFrontFunc`, when post-selectFn cleanup drains a value as orphan AND `selectFn` had also picked up an outbox-wait notification, the renotifyFn is now forwarded (previously dropped — a "lost notification" bug, separate from but exposed during the orphan work).

## Sender shutdown notifies pending listeners (2026-05-25)

### The symptom

A CombinerPool worker goroutine on its exit path (the `defer worker.Release()` at `combinerpool.go:130` of `CombinerPool.goroutine`) cascades through `integrationExEnv.Release` → `baseExEnv.Release` → `Sender.Release` → `outbox.free` → `omnipool.PutCustom` → `outboxTrait.Reset` → `Listeners.Reset`. The Reset panicked if `Listeners.q` was non-empty.

Reliably exposed (~25% of test runs) by the new sim's submit-via-start pattern, which creates higher scatter pressure on CombinerPool outboxes than the old sim did.

### What the leftover listeners actually mean

The listeners on a Sender's outbox are work items that wanted to send a value *through this Sender* but couldn't (outbox was full). They subscribed for a "your outbox drained, try again" notification.

When the Sender shuts down, those work items still want to send — they just can't via this Sender anymore. The correct response is to notify them so they retry on a different Sender (i.e., another goroutine's worker). Leaving them subscribed to a dead Sender would silently abandon their work (or, worse, attach those subscriptions to the *next* user of the recycled outbox).

The `Listeners.Reset` panic correctly enforces an invariant for putting an outbox back into the pool: the listeners queue must be empty, because a recycled outbox carrying stale subscriptions would leak notifications into a context that doesn't own them. The panic did its job — it surfaced abandoned subscriptions that needed handling before recycle. The fix isn't to relax the invariant; it's to satisfy it by draining the subscriptions appropriately before Reset runs.

`outbox.free()` now calls `ob.listeners.NotifyAll()` when refcount reaches 0, before returning the outbox to the omnipool. Each listener wrapper's `notify` fires with `NoopRenotify`, the workq re-execute path picks the work up on the next available worker, the wrapper is returned to its pool, and the listeners queue is empty by the time `omnipool.PutCustom` invokes `outboxTrait.Reset`. The invariant holds; no panic.

### How the leftover got there in the first place

The work registers the listener at `combinerpool.go:294-307`:
```go
if !meta.ShouldBlock() {
    ex.AddToListeners(w.pool.combineQueue.ListenersFor(meta.Sender()))
    ex.AddToListeners(&w.pool.state.SpawnNotifier().Listeners)

    posted := tryPost()
    if !posted {
        waiting()
    }
    return posted, nil
}
```

The subscribe-then-retry is intentionally optimistic: subscribe first (so we don't miss a drain that happens between attempts), then retry. Either branch can leave the listener queued:

- `posted=true`: the retry succeeded. The work is done, but the listener subscription stays in the queue as a deliberate leftover.
- `posted=false`: the work is parked. The listener stays, waiting for a future emptied() to fire it.

Couldn't we just remove the listener when the retry succeeds? No — `outbox.listeners` is backed by `nbcq.Queue` (Michael-Scott lock-free queue), which supports only enqueue/dequeue. There's no mechanism to remove an arbitrary entry. So the leftover-on-success is structural, not a missing cleanup.

In normal operation the leftover is harmless: a future `emptied()` calls `Notify(nil)` which pops the wrapper and invokes its `notify`; the workq sees the work has either already completed (no-op) or is ready to retry (it retries); the wrapper goes back to its pool. The Reset panic fires only when `outbox.free()` decrements refcount to 0 *before* any subsequent `emptied()` consumed the leftover. The new sim's higher scatter pressure makes outboxes fill-and-release more quickly, widening the timing window where leftovers survive until free.

### How the retry actually lands on another Sender (verified)

The wiring is end-to-end asynchronous and lands correctly:

1. **`accepted.go:47`**: `q.listener.Notify = q.waiters.Notify`. The workq queue's Listener, when fired, just calls `q.waiters.Notify`.

2. **`waiters.go:125-134`**: `Waiters.Notify` is `w.q.TryPushBack(renotifyFn)` — atomic enqueue to the waiters' nbcq queue. No synchronous callbacks, no use-after-free risk during the call chain inside `outbox.free()`.

3. **`accepted.go:344-369` `WaitForNew`**: a blocked worker is waiting on the workq's `waiters`. When the renotifyFn lands in the waiters queue, that worker wakes up, returns from WaitForNew, and re-enters the work-execution loop.

4. **`accepted.go:407-417` `execute`**: a `combinePostWork.Execute` that returned `posted=false` (didn't call `ex.Starting()`) is marked postponed and kept in `c.q.postponed`. The next worker to pull from postponed re-runs `Execute` with **its own** ctx and `meta.Sender()` — a different Sender whose outbox may not be full.

So when a Sender shuts down with pending listener subscriptions, NotifyAll drains them; each wrapper signals the workq's waiters; some other worker picks up the postponed work and retries on its own Sender. Work isn't lost.

For the `posted=true` leftover case (the work already completed), the notification still fires, but the workq has nothing to do — the postponed queue doesn't contain that work item anymore, so the woken worker re-checks, finds nothing extra to run, and returns to waiting. Harmless.

### Files involved

- `internal/rdvq/outbox.go` — the fix (NotifyAll on refcount=0)
- `internal/rdvq/listeners.go` — Reset's strictness vs. Sender-shutdown semantics
- `combinerpool.go:294-307` — the subscribe-then-retry pattern that produces leftovers
- `internal/workq/accepted.go:47, 426-428, 344-369, 407-417` — the listener-to-waiters wiring and the postponed-work retry path
- `internal/rdvq/waiters.go:125-134` — Notify is just an atomic enqueue

## Pre-existing race in TestLogLeakStructured (observed 2026-05-26)

`internal/leakguard/structured_log_test.go:50` reads `bytes.Buffer.Len()` from the test goroutine while a GC-triggered finalizer goroutine is still writing to the same buffer via `slog.JSONHandler.Handle`. Reproduces under `go test -race ./internal/leakguard/` on this commit AND on the prior commit (verified by stashing Wave 2 changes and running the test), so it predates Wave 2 — captured here so a future investigator doesn't burn cycles re-confirming.

### Stack of the race

- **Read** (goroutine 9, main test): `bytes.(*Buffer).Len()` from `TestLogLeakStructured` at `structured_log_test.go:50`.
- **Write** (goroutine 18, finalizer): `bytes.(*Buffer).grow()` → `bytes.(*Buffer).Write()` → `log/slog.(*commonHandler).handle()` → `log/slog.(*JSONHandler).Handle()` → `log/slog.LogAttrs()` → `leakguard.LogLeak()` at `leakguard.go:130` → triggered from the finalizer closure registered in `handle.Init` at `leakguard.go:314`.

The test buffer is shared between the test (which constructs a `slog.Handler` writing into it, then asserts on contents) and the leak-logging finalizer (which fires whenever a `leakguard.Handle` is GC'd without `Close`).

### Why it races

The test deliberately drops a handle to trigger the leak path, then reads the buffer to verify the leak was logged. The test currently has no synchronization that waits for the finalizer's `slog` write to complete before the read — it relies on `runtime.GC()` returning after finalizers, but `runtime.GC()` only guarantees the finalizers have *started*, not that they have finished. The race detector catches the un-synchronized buffer access.

### Plausible fixes

- **Buffer with mutex.** Wrap the buffer in a small `mu sync.Mutex` and have both the slog handler and the test's reader acquire it. Cheap; isolates the test from finalizer timing.
- **Channel handshake.** Have `LogLeak` send on a channel after the slog write completes, test reads after receiving. More explicit but couples test to LogLeak internals.
- **Avoid finalizer in the test entirely.** Construct the leak condition via a direct call to whatever LogLeak does at line 130, no finalizer. Removes the race surface but also reduces what the test is verifying.

The mutex approach is the cleanest. Note: this is a test-only race; production `LogLeak` callers don't share a buffer with a reader, so no production fix needed.

### Files involved

- `internal/leakguard/structured_log_test.go:43-50` — the test that races
- `internal/leakguard/leakguard.go:130` — `LogLeak` (the writer side)
- `internal/leakguard/leakguard.go:314` — finalizer registration in `Init`

## Thread A: Handler[T] unification + op trio rename (2026-05-31 / 2026-06-01)

Substantial reshape on the `combiner` branch. **Status: largely complete.** All committed work is on origin; tests green; build green.

### Thread A: complete (landed in commit order)

1. `0509241` **psgfn**: add `Handler[T]` interface, `HandlerFunc[T]` / `ErrHandler` adapters, `NewAccumulator` constructor — additive.
2. `9ebc5cd` **psg**: migrate `Gatherer` to take `psgfn.Handler[T]`.
3. `24553ee` **Op trio step 1**: `Gather`/`Gatherer` → `Skim`/`Skimmer`. Wave methods, internal types, files renamed.
4. `942620f` **Op trio step 2**: `Combiner`/`Combine` → `Funnel`. `CombinerPool` → `FunnelPool` (transitional).
5. `91872d0` **Op trio step 3**: `TaskRunner` → `Launcher`.
6. `cac52fc` **Step 3 (arity collapse)**: single `Launcher[T]` takes `psgfn.Handler[T]`; Launcher0/Launcher2 removed; per-arity Task interfaces removed; `psgfn.Task` named func adapter with short-circuit-on-err.
7. `bdca508` **Step 4 (Submit family)**: `Submit(v)` / `SubmitErr(err)` / `SubmitResult(v, err)` plus Try variants plus `Start`/`TryStart` sugars; same family across Launcher / Skimmer / Funnel.

API_DESIGN.md contains the trio-rename rationale and a `considered & rejected` entry.

### Thread B: complete (B.1 + B.2 + worker plumbing + factory pass)

`bd3ff60` B.1: wave moves to constructor (required).
`8bf2541` B.2: nil-OK at construction; resolution via existing `ctxMeta.wave`.
`c187379` Worker plumbing: nil-wave dispatch works from inside ALL three op body types — task bodies (taskWork carries dispatching wave), Funnel Accumulate/Flush bodies (funnelWork carries it), and Skim handler bodies (via fixing `ensureCtxMeta` to preserve `wave` across same-job ctx transitions). Sim alternates explicit-vs-nil wave for both Skimmers and Launchers, exercising both code paths every run.
`32c0c62` Factory pass: psgwf and otpsg wrappers accept nil wave; `otpsg.Scatter` drops the wave param (resolves from ctx).

### psgfn fold + AccumulatorFactory + full convenience surface

`e853a2a` Big consolidation commit:
- **psgfn package deleted**; all types moved to top-level `psg` (Handler, HandlerFunc, Accumulator, FuncAccumulator, NewAccumulator, NewHandler).
- **AccumulatorFactory is now an interface** with `NewAccumulator() Accumulator[T]` + `Close() error`. `funnelOp.unref()` calls `factory.Close()` on the last-reference cleanup path; Close errors route through the framework err sink.
- **Adapter parallel set**: for each interface (Handler, Accumulator, AccumulatorFactory), there are both generic and err-only flavors. `FuncErrAccumulator` and `FuncErrAccumulatorFactory` store fns in struct fields directly — zero framework-added closures for the err-only path.
- **Op constructor progression** per type: `NewLauncher` (interface, alloc-free hot path) → `NewFnLauncher` (closure, T inferred) → `NewTaskLauncher` (no-arg) → `NewErrLauncher` (err-only). Same shape for Skimmer (minus Task) and Funnel.
- **Type aliases** for every void-T case: `Task`, `ErrHandler`, `ErrAccumulator`, `ErrAccumulatorFactory`, `TaskLauncher`, `ErrLauncher`, `ErrSkimmer`, `ErrFunnel` — all aliases for the `*[struct{}]` instantiations, named for intent.

### Threads B and C status

- **Thread B**: complete.
- **Thread C v0.1 landed**: `Forever` sentinel added (`time.Date(9999, 1, 1, ...)` UTC); Submit / SubmitErr / SubmitResult on Launcher pass it through to the dispatch path so the intent reads as "block until success." `Launcher.dispatch` returns `(bool, error)` so the Try* family no longer needs the TODO sentinel-error path. `Pool.block` treats `Forever` the same as zero (no timer) — block until cancellation / notification. Zero-deadline semantic in `TryExecuteNow` was already "attempt once" via the unset `ex.AddToListeners` (the blocking layer skips listener registration when AddToListeners is nil, so contended dispatch returns `(false, nil)` after one attempt). Doc-aligned; no behavior change for zero.

### Thread C — blocked on Pool/workq consolidation

The remaining Thread C work (Try* honoring non-zero non-Forever deadlines via bounded-wait blocking) can't land cleanly until the underlying inconsistencies in the Pool + workq integration are resolved. The investigation surfaced three:

1. **Work-type deadline propagation is uneven.** `limiterScatterWork`, `launcherScatterWork`, `funnelWork`: pass `w.deadline` to `ExecuteOrWait` / `governor.Execute`; the timer reaches `Pool.block`. `taskPostWork` receives a deadline parameter, doesn't store it, calls `BasicPushSelect` which only watches `ctx.Done()` and `outboxCh` — no timer. (Pre-existing open issue: "Deadline propagation in taskPostWork.")

2. **Dispatch entry points use different "should block" signaling.** `ExecuteNowOrQueue` sets `ex.AddToListeners` to a panicking func to enable the `ShouldBlockOrPostpone` path. `TryExecuteNow` leaves it nil, so the entire blocking loop in `ExecuteOrWait` is bypassed regardless of deadline. Naively setting `AddToListeners` in `TryExecuteNow` causes hangs (see #3).

3. **`errBlockWaitSignaled` conflates timer-fired with notification-received.** `Pool.block` converts both to `nil` before returning. `ExecuteOrWait` can't distinguish "deadline reached, give up" from "got a signal, re-check condition" — re-enters `blockFn` with an already-expired deadline, the new timer fires at 0ns, tight loop.

The structural fix overlaps with the destination doc's **Pool consolidation** (merge TaskPool + FunnelPool into one Pool, rationalize the workq integration). Doing Thread C now would mean wrestling the same inconsistencies twice. Defer Thread C until after the pool consolidation pass; it will likely fall out naturally once `taskPostWork`, the `AddToListeners` signaling, and the `errBlockWaitSignaled` conflation are unified.

### Wrap-pattern migration (landed `aab904c`)

All `psg.NewLauncher(wave, psg.NewTask(fn))` / `psg.NewSkimmer(wave, psg.NewHandler(fn))` / `psg.NewSkimmer(wave, psg.NewErrHandler(fn))` wrap patterns in tests/examples migrated to the convenience constructors (`NewTaskLauncher` / `NewFnSkimmer` / `NewErrSkimmer`). Two intentional stragglers left: `psgwf/scatter.go` and `internal/sim/run.go` both have `body := psg.NewTask(...)` as a named intermediate variable — the Task value is constructed for downstream framework use rather than wrapped inline, so the convenience form doesn't fit.

### Naming-pass deferred items (still relevant)

- `CombinerPool` → `FunnelPool` retained; goes away when Pool consolidates per the destination doc.
- `psgwf.GenericTaskRunner` (and related psgwf wrappers) still use legacy names; rename or retire with the broader psgwf migration.
- chartgen's bench-data parser still reads the historical metric name `combinerLimit`; legacy benchmark file emits `funnelLimit`. Re-align when `bench.txt` is regenerated post-rename.

## Pool consolidation — foundational analysis (2026-06-06)

Design pass for the TaskPool (`Pool`, job.go) + FunnelPool merge. **Settled
direction** (confirmed with PN): the two pools become **one demand-driven,
uncapped worker pool**; per-op `Limiter` is the *only* concurrency control.
Approach: upgrade the foundational internal packages (`workq` / `delayq` /
`*state`) into usefully-abstracted shared building blocks *first*, so the pool
usage simplifies and collapses naturally — wrestle each seam once, in the
foundation, where each integration change should be a simplification or no-op.
Checkpoint at stable (green) states along the way.

### The three foundational seams (why the merge is hard today)

1. **Worker-state accounting is implemented twice, in two styles, with
   different spawn policies.** Funnel pool uses `internal/cpstate.FunnelPoolState`
   (`spawnedGoroutineCount`/`liveGoroutineCount`, `ShouldSpawn{First,}Goroutine`
   capped at `maxConcurrency`, `TryIdleExit`, idle timeout/jitter,
   `spawnNotifier`). Task pool open-codes the same concerns inline in job.go
   (`taskWorkerDemand`, `taskWorkersSpawning`, `taskWorkerIdleTimeout/Jitter`,
   `latestTaskWorkerIdleExit`, `trySpawnTaskWorker`). Policies *differ*: task =
   demand-driven, uncapped, **scale-up aggressively** via a self-propagating
   spawn chain (job.go:801-808: a worker that secures its first task spawns the
   next iff demand remains); funnel = **minimize goroutines** (each goroutine is
   a separate accumulator instance → more partial aggregates to flush;
   funnelpool.go:30) and caps at `maxConcurrency`.

2. **Blocking path conflates two signals.** `Pool.skimSelect` sets
   `errBlockWaitSignaled` for BOTH "block-deadline timer fired" (give up) and
   "block-wait notification arrived" (re-check) — job.go:483-488 — and
   `Pool.block` flattens both to nil (job.go:332). `ExecuteOrWait` can't
   distinguish them. (Thread-C blocker #3.)

3. **Two "should-block" signaling conventions + dropped deadline.**
   `ExecuteNowOrQueue` sets a panicking `AddToListeners`; `TryExecuteNow` leaves
   it nil (`execution.go:24` `ShouldBlockOrPostpone`). And `newTaskPostWork`
   takes a `deadline` it never stores/uses (job.go:1012); the blocking post uses
   `rdvq.BasicPushSelect` (job.go:951) which watches only ctx + outbox, no timer.
   (Thread-C blockers #1, #2.)

### Funnel dual-concurrency finding (key)

FunnelPool has **two independent** concurrency controls today:
- pool-wide goroutine cap (`cpstate.maxConcurrency` via `ShouldSpawnGoroutine`,
  funnelpool.go:254,323) — the legacy CombinerPool limit; and
- per-op `Limiter` (`funnelWork.Execute`, funnelop.go:750-772), independent of
  goroutine count.
The destination keeps only the per-op Limiter, so the goroutine cap is deleted.
maxConcurrency is already *loosely* enforced (the receiving-side `unmetDemandFn`
spawn bypasses it), default is -1 (unlimited), so deletion is low-risk on
enforcement — but see the spawn-policy subtleties below.

### Subtleties uncovered (don't re-derive these)

- **`unmetDemandFn` effectively never fires for the funnel pool.** It triggers
  only when `workAddedCount > 1` within one `Accepted.ExecuteOne`
  (accepted.go:315), but `cpWorker.AddWork` queues at most one item per call.
  So funnel scale-up is driven *entirely* by the posting-side
  `ShouldSpawnGoroutine`, NOT by `unmetDemandFn`. ⇒ "delete the cap and lean on
  unmetDemandFn" would pin the pool at one goroutine. A real replacement spawn
  policy is required.
- With the **default unlimited cap, `ShouldSpawnGoroutine` always returns
  true**, so every *buffered* post currently spawns a goroutine. Deletion must
  pair with a deliberate spawn policy, not bare removal.
- **`spawnedGoroutineCount` vs `liveGoroutineCount` are inconsistent across the
  two spawn paths** (posting-side increments `spawnedGoroutineCount`;
  `unmetDemandFn`/`spawnNewGoroutine` increments only `liveGoroutineCount` via
  `GoroutineStarted`), reconciled by `GoroutineRestarted`. Confusing-by-accident;
  the consolidation should collapse to one clean source of truth.
- Funnel goroutine accounting serves **two roles**: (a) the cap [DELETE], and
  (b) **last-goroutine detection** — `GoroutineExiting()==true` drives the
  end-of-work `confirmEndOfWork` dance + final `flushAll` (funnelpool.go:188-215)
  [PRESERVE].

### Flush ownership constraint (PN, 2026-06-06)

**Flush mechanics must end up living with `Wave`, not `Pool` or `Funnel`.**
Flush is a per-batch-of-work concern, so in the three-type model it belongs to
Wave (batch lifecycle + drain), not the fungible worker Pool and not scattered
across the Funnel op. Today flush is split across the wrong owners:

- `FunnelPool.flushQ` (`delayq` of pending deadlines) + the deadline-timer
  driving in `cpWorker` (`flushToNextDeadline`/`flushAll`, cpworker.go) live on
  the *funnel pool*.
- End-of-work flush coordination — `jobstate.JobState.RegisterFlusher()`,
  `nextFlushChan`, `flushListener`, the `Closed→Flushing→Done` transitions —
  lives on the *Pool's state*.

Destination: a worker *drives* a flush but does NOT *own* it; the Flushing-stage
coordination and `flushListener` move to **Wave**; the Funnel op only *registers*
deadlines. Consistent with REFACTOR_PLAN Wave 5 ("migrate op-ownership + drain
machinery from Pool to Wave").

#### Refined decision (PN concurred, 2026-06-06): timed work in workq

The `flushQ` itself is best modeled as a **generic "timed work" facility built
into `workq`, instantiated per-pool** — NOT a bespoke per-Wave queue. Rationale:
draining is a worker-level task and workers span Waves, so one pool-level merged
delay queue (a single worker drives one timer for the soonest deadline across
all Waves) is correct and strictly more efficient than per-Wave queues (which
force a worker to select across N timers). A flush is just *a unit of work that
becomes ready at deadline T*; `workq` already selects fresh → postponed →
wait-with-timer, so "ready at T" is a natural third source folded into the same
`ExecuteOne` wait/select (the worker's select already watches the idle timer —
adding a next-deadline timer is incremental).

**Why early / why it matters for the merge:** the bespoke flush machinery in
`cpWorker` (`flushDeadlineTimer`, `flushToNextDeadline`, the extra select cases)
is the single biggest reason the funnel worker loop differs from the task worker
loop. Making timed work native to `workq` dissolves that specialness — both
loops just run `ExecuteOne`, delay queue transparent — shrinking the eventual
worker-loop merge surface. Hence this becomes checkpoint 1.

**Ownership = mechanism vs policy:**
- `workq` (per-pool) owns the *mechanism*: schedule a `Work` ready at T, arm one
  timer, promote due items to fresh work. Generic, not flush-specific (task-side
  backoff/retry could reuse it later). Keep this concern cleanly separated —
  composed into the worker's wait, NOT tangled into Accepted's fresh/postponed
  logic — to avoid scope-creeping workq.
- **Wave** owns the *policy/lifecycle*: it scheduled its accumulators' flushes so
  it holds references to them; **force-flush = expedite/`Remove` its own
  entries**; **drain barrier** = Wave is Done only when its flushes have fired;
  `WithFlushListener` (today a Pool option via `JobState`, psgopt/job.go:89)
  relocates here.
- **Funnel op** just schedules "flush this accumulator at T" against its Wave.

**Verified enablers:**
- Flush is idempotent — `funnelInstance.flush` has an "already flushed, ignore"
  guard under `c.mu` (funnelop.go:609-614) — so Wave force-flush racing a natural
  deadline firing is safe (no double-emit).
- `delayq.Remove(item)` is O(log n) — each `Item` tracks its heap position
  (delayq.go:149) — so a Wave expedites/cancels its entries directly via held
  references, no scan, no new per-Wave index. Sight-line: this drops to **O(1)**
  if `delayq` shifts from a binary heap to a timing wheel (TODO.md item — "avoid
  O(log n) heap overhead … esp. for Flush"). See "delayq optimization target"
  below. Coding the timed-work facility against `delayq`'s interface
  (`Schedule`/`Remove`/`Drain→(ready,next)`/`wake`/`Item`), not heap internals,
  keeps that swap a `delayq`-plus-`Item`-helper change.

  **delayq optimization target (derived with PN, 2026-06-06):** a *bucketed,
  tickless timing wheel*. The ordering structure holds **buckets, not individual
  items**, so its cardinality is decoupled from timer count (a million timers in
  one window = one bucket); the finest bucket width is set at **scheduler noise
  (~10ms)**, which makes the quantization lossless in practice — Go timer /
  goroutine / OS jitter already sit above that floor, so the "exact firing" a
  per-item heap preserves is illusory precision below the noise floor. Timer ops
  become uniformly O(1) (compute slot, append/unlink a list node). Two variants:
  *heap-of-buckets* (Kafka-style: next bucket via heap root, O(log B) on
  bucket create/destroy) or *wheel + occupancy bitmap* (next-non-empty via
  find-first-set, **true O(1) reschedule** — preferred for our flush churn,
  where every `Accumulate` pushes the deadline out). Hierarchy gives unbounded
  range. This is the standard high-perf tickless timer (Kafka hierarchical
  timing wheel + delay-queue-of-buckets; tickless cousin of Netty
  `HashedWheelTimer`). Stays behind the `delayq` interface; only the `Item`
  helper's internals change (slot/list-node instead of heap position). Optional
  worst-case refinement (likely unnecessary for flushes, since worker
  parallelism, not the heap, bounds flush throughput): partial bucket admission
  to hard-cap the ordered set. **Baseline for now stays the current binary
  heap** — all of the above is the deferred path behind the stable interface.
- `delayq.Init(wake func())` already has a wake hook — the seam to re-arm a
  worker's timer when the next deadline lowers.

**Likely bonus simplification:** a pending timed item *is* outstanding work, so
the Wave's normal drain accounting can subsume it, collapsing the end-of-work
`flushAll` (far-future `now`) + `RegisterFlusher`/`nextFlushChan` channel dance
into "expedite my timed entries, then drain as usual."

**Risk to validate:** converting flush from out-of-band worker-driven calls
(`cpWorker.flushToNextDeadline`) into a first-class `Work` item flowing through
`Accepted` (fresh/postponed/governor) changes contention/ordering. Arguably
better (flushes become governed, first-class work) but must be proven against
`TestBySimulation` (`-short`, full, and `-race`).

### Sequenced checkpoints (plan, revised 2026-06-06)

1. **Timed work in workq** (flush → workq mechanism + Wave policy) — add a
   generic per-pool "work ready at deadline T" facility to `workq`, folded into
   the `ExecuteOne` wait/select; migrate funnel flush onto it. Relocates flush
   ownership per the "Flush ownership constraint" section: mechanism in
   workq/Pool, policy (force-flush, drain barrier, `WithFlushListener`) on Wave.
   Dissolves the `cpWorker` flush-timer divergence (biggest worker-loop
   difference), so it's the highest-leverage merge-enabler. Validate against
   `TestBySimulation` (`-short`, full, `-race`).
2. **workq block-path rationalization** — split `errBlockWaitSignaled` into
   distinct deadline-reached vs notify-received signals; unify the
   `AddToListeners` should-block convention; thread `taskPostWork`'s dropped
   deadline. Behavior-neutral (only currently-unreachable deadline paths
   change). Dissolves all three Thread-C blockers. *(Bounded; sim-covered.)*
3. **Delete funnel `maxConcurrency`** — convert funnel spawning to a deliberate
   demand-driven policy (NOT bare removal; see subtleties), delete the cap +
   `spawnNotifier`/spawn-slot-wait machinery + `ShouldSpawnGoroutine`, preserve
   live-goroutine/last-goroutine accounting + idle-exit. Update
   `maxholdtime_test.go` (uses `WithMaxConcurrency(1)` to force one goroutine —
   re-express via a per-op Semaphore Limiter or natural light-load behavior).
   `funnel_legacy_bench_test.go` is build-tagged `psg_wave3_legacy_bench` (not
   in normal builds). Remove `psgopt.WithMaxConcurrency` + `opts.MaxConcurrency`.
4. **Worker-state unification** — with both pools demand-driven/uncapped, extract
   the shared spawn (demand counter + bounded spawning counter + chain-on-secure
   + idle-exit throttle) into one building block both pools use; collapse
   `cpstate` + the task-pool inline state onto it.
5. **Posting-path convergence** — `taskPostWork`/`funnelPostWork` onto a shared
   `ExecuteOrWait`-based shape; then the actual Pool/FunnelPool merge falls out.

Checkpoints 1–3 are largely independent and can be sequenced by appetite;
4–5 depend on the demand-driven convergence from 3.

#### Checkpoint 1 design (settled with PN, 2026-06-06)

**`delayq` is subsumed *inside* the workq executable-work queue (`Accepted`),
not wrapped beside it.** Public surface added: exactly `Schedule(w, deadline)`
and `Remove(w)`. Everything else is internal to `Accepted`: draining due items
into the fresh source within `ExecuteOne`, the deadline timer, and wiring
`delayq.wake → q.waiters.Notify`.

- **Handle is the `Work` itself** — no separate handle object, no allocation, no
  pooling. Schedulable work = **option A**: a `ScheduledWork` interface
  (`Work` + `Position() int` + `SetPosition(int)`); the heap position lives in
  the work object via a tiny embeddable helper (mirrors how `WorkItem` supplies
  `ID`/`Group`/`Free`). This is the same pattern `funnelInstance` already uses
  (`flushHeapPos`, funnelop.go:432,445), generalized.
- **No `Reschedule` method.** `delayq.Schedule` is idempotent-replace
  (delayq.go:138), and because the handle is the stable work object,
  `Schedule(w, laterDeadline)` *is* reschedule. The funnel needs this on every
  `Accumulate`; it falls out for free. Surface stays exactly Schedule + Remove.
- Once a timed item is **drained** into fresh it is stored as plain `Work`; its
  `Item`-ness is dormant until rescheduled. `delayq.Remove` is safe on
  already-drained/never-scheduled items, so a Wave force-flushing an entry that
  just fired naturally is a harmless no-op (consistent with the idempotent-flush
  guard).
- **Timer (internal):** wire `delayq.wake → q.waiters.Notify` so a *sooner*
  newly-scheduled deadline nudges a parked worker. For a deadline simply
  arriving, thread the current `next` deadline through the internal
  `AddWorkFunc` contract so the worker's existing select watches a
  workq-provided timer (efficient pooled timer, no goroutine-per-block); on fire
  the worker re-enters `ExecuteOne` and drains due items. `AddWorkFunc` is
  workq-internal, so the public surface is unaffected. (Fully inverting select
  ownership into workq is the eventual merge end state, out of scope here.)

#### Checkpoint 1 sub-steps (each independently green)

- **1a — DONE (`357c39a`).** `internal/workq`: `ScheduledWork` interface +
  embeddable `Scheduled` helper; `delayq` subsumed into `Accepted` (internal
  field + public `Schedule`/`Remove`; due-drain folded into
  `ExecuteOne`/`TryExecuteOne` via `controller.drainTimed`; `wake → waiters`).
  Additive — no psg caller schedules timed work yet; full suite + `-race`
  green; workq unit tests cover immediate-due / remove / in-place reschedule /
  future-deadline parked-worker wake. **Deviation from the plan:** the
  next-deadline wake uses an internal `time.AfterFunc` armed in `WaitForNew`
  (keeps 1a entirely within workq, zero psg changes) rather than threading the
  deadline through `AddWorkFunc`. That threading (to drop the per-fire
  goroutine by using the worker's own pooled-timer select) is **deferred to 1b**,
  where `cpWorker`'s select is reworked anyway — see the `TODO(checkpoint-1b)`
  in `accepted.go` `WaitForNew`.
- **1b-i — DONE (`942c3aa`).** Threaded the next-deadline through `AddWorkFunc`
  (trailing `timedCh <-chan time.Time`); `WaitForNew` arms a pooled timer and
  passes its channel, replacing 1a's `AfterFunc`. Implementers accept it;
  inert until 1b-ii (skim never schedules timed work; funnel flush still on
  `cp.flushQ`). Behavior-neutral; suite + `-race` green.
- **Rename DONE (`18295ab`).** `halfBoundFunnel` → `funnelInstance` (the
  "half-bound" term was a remnant of the dropped `Combiner[I,O]` output type).
- **1b-ii — DONE (`b9dbf2d`).** `funnelInstance` implements
  `workq.ScheduledWork`; funnel flush routes through `cp.workQueue.Schedule`/
  `Remove`; retired `cp.flushQ`, `cpWorker.flushToNextDeadline`/flush timer, the
  `funnelFlusher` interface (funnelmap.go deleted), and `FunnelPool` Yield. Net
  −25 lines. **Key learning:** an async end-of-work sweep (flushAll draining to
  `fresh` for ExecuteOne to run) *lost flushes* when the last goroutine exited
  before executing them — the sim caught it (variable undercounts). Reverted to a
  **synchronous** flushAll: `Accepted.DrainAllTimed(dst)` returns all pending
  timed items and flushAll flushes each via the instance's `Flush` (flush+unref),
  preserving the known-good end-of-work dance. Deadline-driven flush stays async
  (drainTimed→fresh→Execute). `funnelInstance` keeps both `Flush` (combined,
  end-of-work) and `Execute`+`Free` (split, deadline path); an instance is
  drained exactly once so only one path runs per instance. Full suite + full sim
  + `-race` (funnel/skim/maxholdtime/workq/sim) green; lint 0. Minor leftover:
  `funnelOp.instanceCount` is now write-only (InstanceCount() removed) — harmless,
  lint-clean; drop in a later cleanup.
  Original worked-out details (kept for reference):
  - `funnelInstance` already has `Position`/`SetPosition` (was for `funnelFlusher`'s
    `delayq.Item`); workQueue's internal delayq calls them identically, so the
    `queued`/`SetPosition(0)`-flips-`queued` coordination + lock ordering
    (delayq.mu → c.mu) carry over unchanged.
  - Add `Work` methods: **`ID()` must use `workq.NewWorkID()`** (a fresh field
    set at allocate), NOT `funnelInstanceID` — the latter is a separate counter
    and could collide with a `WorkItem` ID, tripping `requeueBuffer`'s
    "unexpected equal IDs" panic. `Group()` = `earliestGroup`. **`Execute`** =
    `ex.Starting()` then flush, acquiring the worker's `Sender` from the ctx
    exec env exactly like `funnelWork.executeInner` (`meta.executionEnvironment.(*cpWorker).Sender()`).
    **`Free`** = `Unref` (drop the ref held while queued). So the existing
    refcount lifecycle maps: Execute=flush, Free=Unref; do NOT embed `WorkItem`
    (its `IncrementWork`/`DecrementWork` would double-count against job state).
  - `funnel()` schedules into `cp.workQueue` (future) / keeps inline flush for
    already-past deadline; `Remove` on re-flush.
  - `cpWorker`: drop the `flushDeadlineTimerCh` driving + `flushToNextDeadline`;
    wire the 1b-i `timedCh` param into `popSelect` (re-drain on fire). Due
    flushes now surface via workq `drainTimed` → fresh → `funnelInstance.Execute`.
  - **End-of-work sweep**: `flushAll` must force ALL pending timed work out of
    `workQueue.timed` regardless of deadline. Add a workq capability (e.g.
    `Accepted.DrainAllTimed`, building on `delayq.Yield`/a far-future Drain) and
    invoke it on the `nextJobFlushCh` signal. Keep `RegisterFlusher`/
    `nextFlushChan` drain-barrier in job-state for 1b-ii (Wave relocation is 1c).
    No-deadline instances still get a far-future placeholder so the sweep finds
    them.
  - Drop the `flushQ` param from `funnelWork.Funnel` / `boundFunnelWork`.
  - Validate `TestBySimulation` (`-short`, full, `-race`) + `TestMaxHoldTime*` +
    funnel/skim, and goroutine/no-leak behavior at end-of-work.
- **1c-i — DONE.** Per-instance flush-barrier reference (design (i)),
  implemented as its own green checkpoint independent of the force mechanism.
  `jobstate`: `RegisterFlusher` (per-goroutine ref + channel) replaced by
  `IncrementReference`/`DecrementReference` (bare `totalReferences` ±, the
  latter advancing to Done on last) plus a no-ref `FlushChan()` (current
  rotating flush-signal channel). `funnelWork.Funnel` new-accumulator branch
  acquires one ref next to `op.ref()`; `funnelInstance.flush()` releases it via
  `defer` after `accumulator.Flush` (panic-safe; ordered so an emitting flush's
  Submit takes its work ref before the instance ref drops). `cpWorker`
  subscribes via `FlushChan()` (no ref, no unregister); dropped the
  `unregisterAsJobFlusher` field + all unregister calls. Synchronous `flushAll`
  + `funnelInstance.Flush` (uppercase) + `timedFlusher` retained for 1c-ii.
  Barrier is now purely per-instance: `Done ⟺ Closed ∧ totalReferences==0`
  where outstanding = work refs (via IncrementWork) + live-instance refs.
  Flushing-stage entry still driven by `inFlightWork→0` (instance refs don't
  touch inFlightWork), so a live-but-idle instance can't keep the job out of
  Flushing. Validated: full short suite + workq + `TestBySimulation`
  (`-short -race -count=3`) + full non-short `TestBySimulation -race` (45s) +
  funnel/skim/maxholdtime; lint 0.
- **1c-ii — NEXT.** See "**1c-ii CONSOLIDATED DESIGN (2026-06-07)**" below
  for the agreed plan (three orthogonal concerns: heap-position under
  `delayq.mu`; atomic `admitted`; per-instance lifetime ref + op-liveness-
  at-flush). It supersedes the earlier scaffolding sketch and the
  "1c implementation design — REFINED" section. The earlier framing
  (live-set + `Accepted.Expedite` + `queueFresh` promotion + a foundation
  `Expedite` already committed in `6df4218`) was developed *before* the
  pre-existing deadlock was discovered/bisected; the consolidated design
  reframes it around the deadlock fix.
### Pre-existing latent deadlock: instance lock held across user callback (BISECTED 2026-06-06)

`TestBySimulation` (full, `-race`) hangs intermittently (~30–50% of full
runs) — a **pre-existing, long-standing** deadlock, NOT a regression from
the 1c work. `git bisect` (good=`b6641cc`, bad=`33a6cd9`, hang=10-min
timeout) pins the first hang to **`63a4d57` "sim: rewrite for
Pool/Wave/Flow vocabulary"** — a **test-only** commit (`internal/sim/*` +
`example_combiner_test.go`, zero production code). So the rewritten sim
merely began *exercising* a latent production bug; the bug itself is
older still.

**Root cause:** `funnelInstance.funnel()`/`flush()` hold the per-instance
`c.mu` across the user `Accumulate`/`Flush` callback. When that callback
re-enters the framework and blocks — `Submit`/`Start` backpressure, or a
nested `CloseAndGatherAll`/`CloseAndSkimAll` draining a sub-job — `c.mu`
stays held; meanwhile a concurrent `delayq.Drain` holding `delayq.mu`
calls `SetPosition`→`c.mu` on that instance and blocks, and the sub-job's
progress needs that drain, so the wait is circular. Baseline `665fd1f`
dump: goroutine in `Accumulate`→`CloseAndSkimAll` holds the instance
`c.mu` 8 min; `funnelInstance.SetPosition` blocked on it 8 min. The
`SetPosition`-on-`c.mu` shape is *this* era's manifestation (delayq
flush); pre-delayq it manifested differently, but the lock-across-user-
callback core is the same and predates the `Job→Pool` rename.

**Implication:** the full-race sim was never reliably green (prior
"green" claims rested on too few samples — 1–2 lucky runs). It cannot be
a green gate for the 1c work until this is fixed. Candidate fixes (PN's
design domain — concurrency-critical): don't hold `c.mu` across the user
callback, or split a position-bookkeeping lock from the accumulator lock.

**Separately — a 1c-i flush-orphan hang (distinct, FIXED in WIP):** the
1c-i per-instance barrier ref turned an *orphaned* end-of-work flush into
a *hang* (12-goroutine signature, vs the 34-goroutine mutex-deadlock
above). Cause: `cpWorker.flushAll`'s `if nextJobFlushCh==nil return false`
guard (load-bearing only for the retired per-goroutine
`unregisterAsJobFlusher`) let an unsubscribed last goroutine exit without
flushing live instances → their barrier refs never drop → Done hangs.
WIP fix: `flushAll` always drains the shared scheduled queue and flushes
(any worker can; each flush drops the instance's own barrier ref).

**Uncommitted WIP (not yet committed):**
(a) `ScheduledWorkItem` embed refactor — `funnelInstance` embeds
`workq.ScheduledWorkItem` (WorkItem+Scheduled), dropping hand-rolled
`workID`/`flushGroup`/`flushHeapPos`/`id`+`funnelInstanceID`; overrides
`SetPosition`/`Free`, adds `Execute`. (b) the `flushAll` orphan-hang fix.
Both pass `-short`+targeted+workq/delayq unit tests + lint; they cannot
be validated against the full-race sim until the pre-existing deadlock is
resolved.

### 1c-ii CONSOLIDATED DESIGN (settled with PN, 2026-06-07) — supersedes the older "1c implementation design — REFINED" and "1c-ii — NEXT" notes below

> **TODO (REVIEW, 2026-06-07):** The 1c-ii implementation (committed:
> `ScheduledState` interface swap in delayq/workq, synchronous
> `Reschedule`/`ClaimForFlush`, and the funnelInstance lifetime rewrite —
> no per-instance refcount, op-liveness-at-flush, R1/R2) is considered
> independently valid and necessary, but **PN has not fully reviewed it**
> and it **may be more complex than necessary** — revisit for
> simplification. Note: it was implemented to fix a theorized funnel
> `c.mu`/`SetPosition` deadlock that turned out NOT to be what fails the
> `TestBySimulation -race` gate (see the limiter-held-across-gather
> root-cause section at the top of this file). So it is **unvalidated
> against a green gate** — the gate can't go green until the limiter
> suspend/resume fix lands. Re-validate (full `-race` sim, many runs)
> once that fix is in.

This is the agreed design. It resolves the pre-existing deadlock *and*
the flush/lifetime tangle by replacing the overloaded `queued` flag with
**three orthogonal concerns**, and it removes the far-future-placeholder
hack and a latent op-leak along the way.

**The three things `queued` was conflating (PN's decomposition):**

- **(a) heap membership + position** — touched *only* under `delayq.mu`
  (the heap's own lock). `SetPosition` becomes pure position bookkeeping
  and **never takes `c.mu`**. *This is the deadlock fix*: a `delayq.Drain`
  can no longer wait on a `c.mu` held across user `Accumulate`/`Flush`, so
  user code can never freeze the delay queue. No leaf lock needed.
- **(b) work-queue admission** — an `atomic.Bool` owned by
  `workq.Accepted`. `Schedule` sets it; `Expedite`/`forceAll` read it.
  This is the admission check that keeps `Expedite` from being a backdoor:
  `Expedite` only *re-prioritizes already-admitted* work, never injects
  new work past the governor. PN's rule: **`c.mu` (or rather the writer)
  gates *changes* to admitted, reads are lock-free.**
- **(c) instance liveness + object lifetime** — see the lifetime model
  below; the single per-instance ref, taken at creation, plus op-liveness
  dropped at flush.

**`ScheduledState`** (new): a struct `{ admitted atomic.Bool; position int }`
encapsulating (a)+(b) with the fields unexported. `ScheduledWork` /
`delayq.Item` expose `ScheduledState() *ScheduledState` *instead of*
`Position()/SetPosition(int)`; the embeddable `Scheduled`/`ScheduledWorkItem`
just hold one. `position` is delayq-owned (only mutated under `delayq.mu`);
`admitted` is atomic. The work type embeds it and never touches the fields
directly, so it *structurally cannot* hold a heap lock across user code.

**`Schedule(w, at)` with `at == 0` = indefinite (admitted, not heaped).**
Reverses the zero-`at` panic added in `2759116`. `Schedule` always sets
`admitted=true`; a non-zero `at` also creates the heap entry, a zero `at`
does not. This is the proper replacement for the `maxFlushAllSkew`/
`drainAllSkew` 24h placeholder — no fake deadlines, no heap churn for
no-deadline accumulators, smaller heap (= smaller contention surface).
`Expedite(w)`: read `admitted` (panic if never admitted); if it has a heap
entry, remove it; push to `fresh`. `forceAll` = `Expedite` over the live
set, heaped or indefinite alike. The far-future placeholder, `DrainAllScheduled`,
and the whole-queue end-of-work sweep all go away.

**(c) lifetime model — the spine.** Today `queued` doubles as the
"heap-membership ref held?" gate, the ref-drop is deferred to `Free`, and
`funnelInstance.free()` couples object-recycle with `op.instanceCount--` +
`op.unref()` at the *discard-pop*. That coupling is the bug: a flushed
**spent shell** lingers in `instanceQueue` (an nbcq — no mid-queue removal)
holding the creation `op.ref()`, so the op's refcount never reaches 0 and
the op **silently leaks** (`factory.Close()` never runs); nothing is
guaranteed to pop it after the last `flushAll`. New model:
- **One ref per instance, taken right after the factory call** (creation).
  No conditional schedule-time `Ref`, no per-heap-entry ref → nothing for a
  reschedule-vs-drain race to double-drop. The `queued` flag disappears.
- **Op-liveness (`instanceCount` + `op.unref()`) drops at flush**, not at
  discard-pop — flush is when the instance stops being a live accumulator.
  So the op can reach teardown even with spent shells still cached.
- **Object recycle (`pool.Put`)** happens on reuse-pop *or* at teardown.
- **`funnelOp.unref()` (op teardown) DRAINS `instanceQueue`** — pop all,
  assert each `accumulator == nil` (spent), drop them — instead of
  asserting the queue empty. The queue is a reuse *cache* the op cleans up,
  not a refcount the world must drain.
- **Reschedule check under `delayq.mu`** so no heap entry lingers: funnel's
  reschedule, after `Accumulate` returns, takes `delayq.mu` and re-adds
  only if the instance is still schedulable; if it was already drained, it
  skips the re-add (the accumulated data flushes via the pending `Execute`).
  This is safe now precisely because (a) makes `c.mu → delayq.mu` the only
  cross-order (nothing takes `delayq.mu → c.mu`), so it's acyclic. (The
  alternative — a generation/`ID` weak-ref guard on pooled-instance reuse,
  per the omnipool TODO — is the fallback if the synchronous reschedule's
  `delayq.mu` contention proves costly; reschedule-check is the default.)

**Naming (this session):** `funnelInstance.funnel()` → `accumulate()`;
`*Work.Funnel`/`boundFunnelWork.Funnel` → `Dispatch`. Defer the broader
combiner-era renames (`executeFunnel`, the `cp`/`cw`/`c*` "combiner"
prefixes, etc.) to a final name-reconciliation pass.

**Already in WIP toward this:** the `ScheduledWorkItem` embed (a step to
`ScheduledState`) and the `flushAll` orphan-hang fix. Both stay; the embed
evolves into `ScheduledState`.

**Validation gate:** full `TestBySimulation -race`, run *many* times (the
bug is intermittent), must be **green** — this is the first time it
legitimately can be, since the design fixes the pre-existing deadlock. Plus
`-short -race`, maxholdtime/funnel/skim, and no-leak at end-of-work. Use
the reduced-variability sim config + `PSGTRACEINTERNALS` if anything
regresses.

#### concern (c) RESOLVED — lifetime model pinned down (2026-06-07)

Tracing the code to start sub-step 1 surfaced two facts that revise the
plan:

- **Sub-steps 1, 2 and 4 are inseparable.** Swapping `delayq.Item` to
  `ScheduledState() *ScheduledState` makes the heap write `state.position`
  directly under `delayq.mu`, which structurally removes
  `funnelInstance.SetPosition` — but that override's `if p<=0 { queued=false }`
  is the *only* drain-signal clearing `queued` today, and `queued`'s clean
  removal *is* the new lifetime model. So the interface swap, the deadlock
  fix, and the lifetime model land as **one** change; none is green alone.
- **The validation gate is flaky until the deadlock is fixed**, so trust the
  `-race` sim only *after* the combined change lands.

**Parties holding an instance pointer:** A = in `instanceQueue` (reuse
cache); C = being processed by a `funnelWork` (Accumulate, holds `c.mu`);
B = a `delayq` heap entry; D = drained-from-heap, running
`funnelInstance.Execute` (deadline flush, holds `c.mu`). A and C are one
lineage (an instance is in the queue *or* being processed, never both); B
and D are the delayq lineage.

**Two rules make the model safe without a per-instance refcount:**
- **R1** — C never flushes/reschedules an instance whose `Position ==
  removed(-1)` (D already drained it); C accumulates and pushes back **live**,
  letting D's pending `Execute` flush the freshly-accumulated data. Because
  C's synchronous `Remove` and D's `Drain` serialize on `delayq.mu`, exactly
  one of them claims the flush — never both.
- **R2** — D captures `op := c.op` under `c.mu` and **never touches the
  instance object after `c.mu.Unlock`** (only op-level atomics). An owner
  reuse-pop can then recycle the spent shell concurrently with D finishing,
  race-free.

**Answers to the four questions:**
1. **`refCount` does NOT survive** — the per-instance `refCount` integer and
   the `queued` flag are both eliminated. Liveness = `accumulator != nil`;
   the design's "one ref" is the op-level `op.ref()` taken at creation,
   dropped at flush (op-liveness).
2. **Recycle (`pool.Put`) is owner-only, exactly once:** reuse-pop (spent
   shell from `instanceQueue`) or teardown (`funnelOp.unref` drains
   `instanceQueue`). Single nbcq popper guarantees once. D never recycles.
3. **The Drain-pop vs re-funnel double-unref is unreachable** in the new
   model: synchronous delayq ops give C-remove/D-drain mutual exclusion, R1
   makes C defer when D won, and with no per-instance refcount there is
   nothing to double-drop.
4. **Reschedule-check** = a synchronous `delayq` method (`Reschedule(item,
   at) (added bool)` + admit/remove variants) run under `delayq.mu` while
   holding `c.mu`; folds updates, reads the authoritative tri-state
   `Position` (0 = admitted/never-heaped, >0 = heaped, -1 = drained), re-adds
   only if not drained. Sole cross-order `c.mu→delayq.mu`, acyclic.

**Revised sub-step order:**
1. **Combined foundation + deadlock fix + lifetime** (was 1+2+4): introduce
   `ScheduledState` and swap the interface; remove `funnelInstance.SetPosition`
   (position is `delayq.mu`-only — the deadlock fix); retire `queued` and the
   per-instance `refCount`; one creation `op.ref()` dropped at flush;
   synchronous `Reschedule`/`Remove` under `delayq.mu` with R1/R2;
   `funnelOp.unref` drains `instanceQueue`. **Gate: full `-race` sim green.**
2. (b) `admitted` atomic + `Schedule(w,0)` indefinite + `Expedite` admission
   check; reverse the zero-`at` panic; delete the placeholder + skew consts.
3. live set + `forceAll` (Expedite over the set); retire synchronous
   `flushAll`/`Flush`/`scheduledFlusher`/`DrainAllScheduled`.
4. naming: `accumulate()` / `*Work.Dispatch`.

**Original sub-step order (superseded by the above; kept for history):**
1. `ScheduledState` + `ScheduledState()` interface swap (workq/delayq/heap);
   embed in `funnelInstance` (extends the WIP embed). Behavior-neutral.
2. (a) `SetPosition`/position → `delayq.mu`-only; drop `queued`'s
   drain-signal role. *Deadlock fix* — validate hard against full `-race`.
3. (b) `admitted` atomic + `Schedule(w,0)` indefinite + `Expedite` admission
   check; reverse the zero-`at` panic; delete the placeholder + skew consts.
4. (c) lifetime: one creation ref, op-liveness-at-flush, `funnelOp.unref`
   drains `instanceQueue`, reschedule-check under `delayq.mu`. Retire the
   `queued` flag entirely.
5. live set + `forceAll` (Expedite over the set); retire synchronous
   `flushAll`/`Flush`/`scheduledFlusher`/`DrainAllScheduled`.
6. naming: `accumulate()` / `*Work.Dispatch`.

- **1c** — Relocate flush *policy* to Wave AND parallelize the end-of-work sweep.
  Two coupled deliverables:

  1. **Unified drain barrier: a pending timed item *is* outstanding work.**
     Replace the bespoke `confirmEndOfWork` dance + `RegisterFlusher`/
     `nextFlushChan` with a single accounting where outstanding = regular work +
     pending timed (flush) items; `Done` ⟺ outstanding == 0. This is the
     keystone: it makes `Done` wait for every flush regardless of which worker
     runs it, and ensures **no worker exits while forced flushes remain**.

  2. **Parallel end-of-work sweep (retire the serial `flushAll`).** The current
     synchronous `flushAll` serially calls an *unbounded* number of user
     `accumulator.Flush` functions on one goroutine — a tail-latency landmine
     (pre-existing; 1b-ii preserved it). With barrier (1) in place, the
     async-to-`fresh` direction reverted in 1b-ii becomes *safe*: at quiescence
     of regular work (Wave Closed, no in-flight regular work), **`forceAll`
     Expedites the Wave's pending instances** (queues each as ready now — see the
     Schedule/Expedite split below) so they run through the normal parallel
     ready→`Execute` path, fanned out across the whole pool instead of
     serialized. The deadline-driven path is already parallel; this brings the
     forced sweep in line.

  **Multi-cycle flushing is intrinsic and stays.** A flush can emit downstream
  and create new funnel input (cross-hop / recursive), which creates new
  accumulator state needing its own flush. So flushing is a fixpoint: force →
  flush → maybe new work → drain → force again, until outstanding (work +
  pending flushes) hits zero. 1c doesn't remove this; it makes it fall out of
  the unified barrier (refcount→0) rather than the re-confirm loop. Cycles are
  bounded by dataflow depth; terminates iff the user dataflow terminates (same
  as today).

  Plus the ownership move: `WithFlushListener` → Wave; force-flush ownership
  (Remove + run handles) on Wave. The synchronous `flushAll` (b9dbf2d) is the
  correct *interim*; 1c is the destination.

  **1c implementation design — REFINED, fresh-session-ready (PN, 2026-06-06).**
  **[SUPERSEDED 2026-06-07 by "1c-ii CONSOLIDATED DESIGN" above — kept for
  history. This predates the pre-existing-deadlock discovery; its live-set/
  `Expedite`/`queueFresh`/per-instance-barrier framing is reframed there
  around the deadlock fix and the three-orthogonal-concerns decomposition.]**
  Decision (a): land the correct per-instance barrier + force-via-normal-path
  now; full sweep parallelism arrives with checkpoint 2/3 demand-driven
  spawning. Structure-for-parallel is the goal. The first 1c attempt was reset
  (back to `b9dbf2d` code) because three nuances reshaped it mid-flight; they're
  all captured below so a fresh session can implement it in one clean pass.

  **(i) Per-instance-lifetime barrier reference.** A `funnelInstance` holds one
  job/Wave reference for its whole life as a live accumulator: creation →
  flush. Acquire in `funnelWork.Funnel`'s new-instance branch (next to
  `op.ref()`): `job.state.IncrementReference()`. Release in
  `funnelInstance.flush()` after the real `accumulator.Flush` returns, via
  `defer` (so a panicking Flush still releases). NOT in `free()` — `free()` is
  lazy (a spent instance lingers in instanceQueue until a next pop that may
  never come → would hang). `flush()`'s `accumulator==nil` early-return makes
  the real flush (and the release) run exactly once. This replaces
  `RegisterFlusher`'s per-goroutine ref entirely; barrier is purely per-instance:
  `Done ⟺ Closed ∧ totalReferences==0` (work refs via IncrementWork +
  live-instance refs). Ordering: the flush's emit acquires its work ref *inside*
  `accumulator.Flush` (Submit→IncrementWork) before the instance ref releases,
  so totalReferences can't transiently hit zero across an emitting flush. On
  cancel refs leak, but doneChan isn't the cancel sync point — consistent with
  today's flusher refs. (jobstate: add `IncrementReference`/`DecrementReference`;
  make the flush signal a no-ref `FlushChan()`; drop `RegisterFlusher`.)

  **(ii) Wave-held live-instance set (force enumeration).** `FunnelPool` holds
  the set of its live (unflushed) instances — exactly the funnels it may force
  at end-of-work. Add at creation, remove in `flush()`. Reasons (PN): avoids
  scanning every Wave's funnels to flush a few, and keeps `workq` ignorant of
  the Wave/grouping concept. Generics wrinkle: `funnelInstance[T]` is generic but
  `FunnelPool` isn't, so the set holds the non-generic `workq.ScheduledWork`
  (start with `map[ScheduledWork]struct{}`+mutex; an intrusive list is the later
  allocation optimization — but note the lock-order/lifecycle care: snapshot or
  hold the set lock across the force loop, and force only calls Expedite which
  takes no instance lock).

  **(iii) Schedule vs Expedite — the backpressure split (KEY).** Two distinct
  timed-queue operations with different rules:
  - **`Schedule` (admit NEW timed work)** can grow outstanding work, so it MUST
    be backpressure-controlled: available only *within the controlled ExecuteOne
    flow*, never an exported `Accepted` method. Exporting it is a backdoor —
    arbitrary code could inject unbounded work past the governor. Grant it like
    `queueFn` (a capability, not a public method).
  - **`Expedite` (queue an already-scheduled item as ready NOW)** — it does NOT
    lower the item's deadline (that would still wait for a drain pass). It
    *removes* the item from the timed queue and hands it to the **ready (fresh)
    queue immediately**, so the next worker runs it. Admits nothing new (the
    item already passed admission at `Schedule`) ⇒ backpressure-neutral ⇒ safe to
    call anywhere, including `forceAll` from the dance (outside `AddWork`). May be
    a public method.
  - **`Expedite` PANICS if the item was never scheduled / already done** — a
    defensive contract assertion, not a silent no-op. Subtlety: a worker can
    *concurrently drain* the item (timed→ready via `drainTimed`) between
    `forceAll` selecting it and `Expedite` running; that's benign (it's already
    on its way) and must NOT double-enqueue or panic. So distinguish: never
    scheduled / already flushed → **panic**; scheduled-but-just-drained → no-op.
    Needs an atomic check-and-move against the timed structure plus a per-instance
    "scheduled" indicator (the currently-scheduled set membership, not mere
    heap-presence). Respect the `delayq.mu → c.mu` order (`Expedite` not called
    under the instance lock; `forceAll` not holding the set lock while calling a
    delayq op — drain removes under `delayq.mu→c.mu`, so that ordering would
    cycle).
  - **Ready-enqueue must carry the same spawn/bookkeeping** the controller's
    `drainTimed` promotion uses (push via `queueFresh` so `workAddedCount` →
    `unmetDemandFn` spawns a worker → parallel sweep). Reconciling that with
    `forceAll` running *outside* the controller (no live `queueFresh`) is an open
    implementation point — e.g. `Expedite` fires the pool's spawn notify itself,
    or hands ready items off through a controlled path.
  - `forceAll` = `Expedite(c)` over the Wave's currently-scheduled set.
  - delayq roles: `Schedule` = add/replace (admission); `Remove` =
    drop-if-present. `Expedite` is an `Accepted`-level op = confirm-scheduled
    (panic if never) + `delayq.Remove` + ready-enqueue — NOT a delayq deadline
    mutation.

  **(iv) Schedule-capability context (the forceAll-not-in-AddWork nuance).**
  `forceAll` is reached from TWO contexts: the `confirmEndOfWork` dance (loop
  body, *outside* `AddWork`) and the popSelect signal-followup (*inside*
  `AddWork`). Expedite is safe in both (no admission). But `Schedule` (admission)
  must be within the controlled flow — and funnel flush-scheduling happens during
  the funnel work's *Execute* (accumulate→deadline), which is within `ExecuteOne`
  but NOT literally `AddWork`. So grant the Schedule capability for the
  controlled `ExecuteOne` flow (AddWork + Execute, e.g. via the `Execution` /
  exec-env), not as a bare `AddWorkFunc` parameter (which wouldn't reach Execute
  or the dance). The capability itself is stateless (Schedule just enqueues to
  the timed delayq; backpressure is the governor wrapping `ExecuteOne`).

  **(v) Promote timed→fresh through `queueFresh` (parallelization key + the real
  backdoor).** `drainTimed` must promote due items via the controller's
  `queueFresh` (which bumps `workAddedCount` → fires `unmetDemandFn` to spawn a
  worker), NOT a direct `q.fresh.PushBack`. The direct push was the actual
  admission backdoor: it bypassed the spawn-trigger bookkeeping, so forced
  flushes would never spawn workers — defeating decision-(a)'s parallel sweep.

  **(vi) Lost-wakeup race — `shouldStillWait` must re-check timed.**
  `Waiters.Notify` drops the wake if no inbox is parked (`TryPushBack`→false,
  verified), so a `Schedule`/`Expedite` that races a worker entering its wait
  loses its notification. The confirm-protocol backstop (`shouldStillWait`)
  currently re-checks only fresh/postponed — so timed work added in that window
  is missed → potential hang. Fix: `shouldStillWait` also re-drains timed (due
  items → fresh, caught by its existing `TryAccepted`) and aborts the wait if the
  next deadline is now sooner than what the wait's timer was armed for (add
  `controller.armedTimedDeadline`, set in `WaitForNew`). Mirrors the fresh-work
  backstop.

  **(vii) Retire** synchronous `cpWorker.flushAll` + `funnelInstance.Flush`
  (uppercase combined). Single flush path = `Execute`(flush)+`Free`(unref);
  `flush()` also removes from the live set + releases the barrier ref.
  flush-Execute is not a poolWork → doesn't touch inFlightWork; the per-instance
  ref holds the barrier.

  **(viii) Goroutine loop / triggers.** Keep the `confirmEndOfWork` dance and the
  job flush signal (`noMoreWork` already re-fires it each time `inFlightWork`
  returns to zero → multi-cycle works). Swap actions: dance → `forceAll`
  (Expedite each live instance) then `continue` to pump the now-due flushes;
  signal-followup → `forceAll`; `executeFunnel` subscribes via the no-ref
  `FlushChan()`.

  **Validation:** `TestBySimulation` (`-short`, full, `-race`) is the safety net
  for the barrier/force — it caught the 1b-ii async-sweep lost-flush bug. Also
  `TestMaxHoldTime*`, funnel/skim, end-of-work no-leak.

**Open design question for checkpoint 3**: the post-cap-deletion funnel spawn
policy. Funnel wants *minimum* goroutines, so it can't adopt the task pool's
aggressive chain verbatim. Candidate: keep "ensure ≥1 goroutine on post" (the
`ShouldSpawnFirstGoroutine` role) as the floor, and add a genuine excess-work
scale-up trigger (since `unmetDemandFn` doesn't fire) — e.g. spawn when a post
can't hand off AND queue depth exceeds live goroutines. Needs validation against
`TestBySimulation` (run both `-short` and full, plus `-race`).

### Next session pickup (in rough priority order)

1. **Pool / workq consolidation** — see "Pool consolidation — foundational
   analysis (2026-06-06)" above. Checkpoint 1 progress: **1a, 1b-i, 1b-ii,
   rename, and 1c-i (per-instance barrier ref) are DONE** (committed). 1c-ii
   foundation (`6df4218`) + `delayUntil→at`/`timed→scheduled` rename (`2759116`)
   committed. **NEXT = finish 1c-ii per the "1c-ii CONSOLIDATED DESIGN
   (2026-06-07)" section above** — the design that fixes the **pre-existing
   deadlock** (bisected to `63a4d57`, a test-only sim commit) via three
   orthogonal concerns: heap-position under `delayq.mu`; atomic `admitted`
   (+ `Schedule(w,0)` indefinite, reversing the zero-`at` panic); per-instance
   lifetime ref with op-liveness-dropped-at-flush + `funnelOp.unref` draining
   `instanceQueue`; then live-set/`forceAll`; then `accumulate()`/`Dispatch`
   renames. **CAUTION:** the full `-race` sim was never reliably green
   (intermittent pre-existing hang); it becomes the gate only after the
   deadlock fix. Uncommitted WIP in the tree (`ScheduledWorkItem` embed +
   `flushAll` orphan-hang fix) folds into sub-steps 1 and 5.
   After 1c: checkpoint 2 (delete funnel `maxConcurrency` → demand-driven) then
   3/4/5.
2. **Thread C completion** — Try* honoring non-zero non-Forever deadlines via bounded-wait. Falls out of the consolidation; pick up the `Forever` sentinel and `dispatch (bool, error)` foundation from `5dc49c7`.
3. **psgwf legacy-name retirement** — `psgwf.GenericTaskRunner` and friends still use pre-rename vocabulary. Done as a stand-alone pass or rolled into a broader psgwf migration.
4. **bench.txt regeneration + chartgen alignment** — re-run benchmarks under the new metric names (`funnelLimit` instead of `combinerLimit`), then update chartgen to parse the new names. Required before the legacy bench file can come back online for chart generation.
5. **CombinerPool retirement** — once Pool consolidation lands, the CombinerPool→FunnelPool transitional name can go away. Stand-alone follow-up if not folded into the consolidation pass.

## Open issues

### Deadline propagation in taskPostWork

`taskPostWork.newTaskPostWork()` receives a `deadline` parameter but doesn't store or use it. Sibling scatter work types (`taskPoolScatterWork`, `combineScatterWork`, `gatherScatterWork`) store and use theirs. Should add a `deadline time.Time` field and pass it to `BasicPushSelect` via context.

### Renotifier lifecycle (`wrappedRenotify` only now)

`rdvq.RenotifyFunc` is a bare `func()` with no `Free()`. After the orphan elimination, the only remaining workaround instance is `wrappedRenotify` in `internal/rdvq/notifier.go`, which self-frees inside its renotify callback — works only if the renotifier is invoked, leaks if it's replaced or discarded. Long-term: change `RenotifyFunc` to a `Renotifier` interface with `Renotify()` and `Free()` so the rdvq infrastructure can free unused renotifiers in all cases. Less urgent now that `orphanedTaskRenotify` is gone — only the rdvq-internal one remains.

Files affected: `internal/rdvq/notifier.go`, `internal/rdvq/waiters.go`, all `Notify()` callsites.

### ExecuteOrWait duplication

`taskPostWork.Execute` implements ~80 lines of try/subscribe/block logic that overlaps with `workq.ExecuteOrWait` and `workq.Governor.Execute`. It has unique requirements (custom `TryPushBack`, demand-registration side effects, blocking via `PushBackFunc` + `BasicPushSelect`) so it isn't a trivial extraction. Possibly worth a `TryPostBehavior` abstraction if other places grow similar shape, but not urgent.
