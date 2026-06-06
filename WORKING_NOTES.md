# PSG-Go Combiner Branch Working Notes

This document contains working notes and context for development on the `combiner` branch.

Major combiner architecture work is complete. Branch is now in cleanup and finalization phase.

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
- Flush is idempotent — `halfBoundFunnel.flush` has an "already flushed, ignore"
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
  `ID`/`Group`/`Free`). This is the same pattern `halfBoundFunnel` already uses
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
- **1b-ii — NEXT (the behavior-sensitive core).** Make `funnelInstance` a
  `workq.ScheduledWork`; route funnel flush through `cp.workQueue.Schedule`/
  `Remove`, retiring `cp.flushQ`, `cpWorker.flushToNextDeadline`, and the
  `funnelFlusher` interface. Worked-out details (don't re-derive):
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
- **1c** — Relocate flush *policy* to Wave: force-flush (Remove+run its handles),
  drain barrier (timed item = outstanding work; collapse `RegisterFlusher`/
  `nextFlushChan` where possible), `WithFlushListener` → Wave. The semantic move.

**Open design question for checkpoint 3**: the post-cap-deletion funnel spawn
policy. Funnel wants *minimum* goroutines, so it can't adopt the task pool's
aggressive chain verbatim. Candidate: keep "ensure ≥1 goroutine on post" (the
`ShouldSpawnFirstGoroutine` role) as the floor, and add a genuine excess-work
scale-up trigger (since `unmetDemandFn` doesn't fire) — e.g. spawn when a post
can't hand off AND queue depth exceeds live goroutines. Needs validation against
`TestBySimulation` (run both `-short` and full, plus `-race`).

### Next session pickup (in rough priority order)

1. **Pool / workq consolidation** — see "Pool consolidation — foundational
   analysis (2026-06-06)" above for the settled direction, the three seams, the
   funnel spawn subtleties, and the sequenced checkpoints. Start at checkpoint 1
   (timed work in workq — the highest-leverage merge-enabler; relocates flush
   ownership to Wave) per the analysis.
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
