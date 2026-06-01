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

### Naming-pass deferred items (still relevant)

- `CombinerPool` → `FunnelPool` retained; goes away when Pool consolidates per the destination doc.
- `psgwf.GenericTaskRunner` (and related psgwf wrappers) still use legacy names; rename or retire with the broader psgwf migration.
- chartgen's bench-data parser still reads the historical metric name `combinerLimit`; legacy benchmark file emits `funnelLimit`. Re-align when `bench.txt` is regenerated post-rename.
- Several `psg.NewLauncher(wave, psg.NewTask(fn))` and similar wrap-pattern call sites remain in tests/examples (perl-migration didn't catch multi-line ones). Not broken; just stylistically older. Migrate opportunistically to the New*Launcher convenience constructors.

### Next session pickup

- **Thread C**: Forever sentinel + zero-deadline polarity flip. Single coherent change with API impact across all `Try*` methods.
- **Opportunistic wrap-pattern migration**: replace remaining `NewSkimmer(..., NewHandler(fn))` and `NewLauncher(..., NewTask(fn))` with the `NewFn*` / `NewTask*` / `NewErr*` constructors. Low priority; cleanup.
- **Pool consolidation**: retire CombinerPool/FunnelPool transitional name and merge with the worker Pool per the destination doc. Bigger architectural change.

## Open issues

### Deadline propagation in taskPostWork

`taskPostWork.newTaskPostWork()` receives a `deadline` parameter but doesn't store or use it. Sibling scatter work types (`taskPoolScatterWork`, `combineScatterWork`, `gatherScatterWork`) store and use theirs. Should add a `deadline time.Time` field and pass it to `BasicPushSelect` via context.

### Renotifier lifecycle (`wrappedRenotify` only now)

`rdvq.RenotifyFunc` is a bare `func()` with no `Free()`. After the orphan elimination, the only remaining workaround instance is `wrappedRenotify` in `internal/rdvq/notifier.go`, which self-frees inside its renotify callback — works only if the renotifier is invoked, leaks if it's replaced or discarded. Long-term: change `RenotifyFunc` to a `Renotifier` interface with `Renotify()` and `Free()` so the rdvq infrastructure can free unused renotifiers in all cases. Less urgent now that `orphanedTaskRenotify` is gone — only the rdvq-internal one remains.

Files affected: `internal/rdvq/notifier.go`, `internal/rdvq/waiters.go`, all `Notify()` callsites.

### ExecuteOrWait duplication

`taskPostWork.Execute` implements ~80 lines of try/subscribe/block logic that overlaps with `workq.ExecuteOrWait` and `workq.Governor.Execute`. It has unique requirements (custom `TryPushBack`, demand-registration side effects, blocking via `PushBackFunc` + `BasicPushSelect`) so it isn't a trivial extraction. Possibly worth a `TryPostBehavior` abstraction if other places grow similar shape, but not urgent.
