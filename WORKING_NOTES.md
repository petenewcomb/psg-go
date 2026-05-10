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

## Open issues

### Deadline propagation in taskPostWork

`taskPostWork.newTaskPostWork()` receives a `deadline` parameter but doesn't store or use it. Sibling scatter work types (`taskPoolScatterWork`, `combineScatterWork`, `gatherScatterWork`) store and use theirs. Should add a `deadline time.Time` field and pass it to `BasicPushSelect` via context.

### Renotifier lifecycle (`wrappedRenotify` only now)

`rdvq.RenotifyFunc` is a bare `func()` with no `Free()`. After the orphan elimination, the only remaining workaround instance is `wrappedRenotify` in `internal/rdvq/notifier.go`, which self-frees inside its renotify callback — works only if the renotifier is invoked, leaks if it's replaced or discarded. Long-term: change `RenotifyFunc` to a `Renotifier` interface with `Renotify()` and `Free()` so the rdvq infrastructure can free unused renotifiers in all cases. Less urgent now that `orphanedTaskRenotify` is gone — only the rdvq-internal one remains.

Files affected: `internal/rdvq/notifier.go`, `internal/rdvq/waiters.go`, all `Notify()` callsites.

### ExecuteOrWait duplication

`taskPostWork.Execute` implements ~80 lines of try/subscribe/block logic that overlaps with `workq.ExecuteOrWait` and `workq.Governor.Execute`. It has unique requirements (custom `TryPushBack`, demand-registration side effects, blocking via `PushBackFunc` + `BasicPushSelect`) so it isn't a trivial extraction. Possibly worth a `TryPostBehavior` abstraction if other places grow similar shape, but not urgent.
