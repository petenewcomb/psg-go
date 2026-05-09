# PSG-Go Combiner Branch Working Notes

This document contains working notes and context for development on the `combiner` branch.

Major combiner architecture work is complete. Branch is now in cleanup and finalization phase.

## Architecture Highlights (Completed)

**Core Infrastructure:**
- LIFO stack architecture for natural worker scaling (eliminates controller complexity)
- Leakguard package for safe resource handle management with finalizer-based leak detection
- Demand-based worker spawning with explicit `taskWorkerDemand` counter
- Hardware-accelerated 128-bit atomics in nbcq for improved performance
- Orphan task buffering with notification infrastructure

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

After the rdvq fix, did a once-over for analogous issues:

- **`internal/cpstate/state.go`** — `spawnedGoroutineCount` is reservation-coupled: every successful `ShouldSpawn{First,}Goroutine()` is paired with a `GoroutineExiting()` (or `GoroutineExiting → GoroutineRestarted` for end-of-work confirmation). Retry loops in `Should*` handle concurrent reservation contention correctly. Cancel/shutdown branch in `combinerpool.go:229-236` deliberately skips `GoroutineExiting()` (the comment is accurate — accounting doesn't matter once the job is shutting down). No drift potential analogous to `taskWorkerDemand`.
- **`gatherQueue` notification path** — `gatherPostWork.Execute` passes `bufferedFn = nil` for both `TryPushBack` and `PushBackFunc`, so the rdvq race never affected this path. Combiners blocked on `BasicPushSelect` for gather posting wake via standard buffered-channel mechanics when a gather receiver drains the outbox; no separate notification needed. Receive side (`gatherSelect`) uses standard rdvq waiter patterns.

## Open issues

### Deadline propagation in taskPostWork

`taskPostWork.newTaskPostWork()` (job.go:1148) receives a `deadline` parameter but doesn't store or use it. All sibling scatter work types (`taskPoolScatterWork`, `combineScatterWork`, `gatherScatterWork`) store and use their deadlines. Should add a `deadline time.Time` field and pass it to `BasicPushSelect` via context.

### Orphan renotifier lifecycle

`rdvq.RenotifyFunc` is a bare `func()` with no `Free()`. Both `orphanedTaskRenotify` (job.go) and `wrappedRenotify` (rdvq/notifier.go) work around this by self-freeing inside their renotify callback — works only if the renotifier is invoked, leaks if it's replaced or discarded. Long-term: change `RenotifyFunc` to a `Renotifier` interface with `Renotify()` and `Free()` so the rdvq infrastructure can free unused renotifiers in all cases.

Files affected: `internal/rdvq/notifier.go`, `internal/rdvq/waiters.go`, `job.go`, all `Notify()` callsites.

### ExecuteOrWait duplication

`taskPostWork.Execute` (job.go:1018-1099) implements ~80 lines of try/subscribe/block logic that overlaps with `workq.ExecuteOrWait` and `workq.Governor.Execute`. It has unique requirements (custom `TryPushBack`, demand-registration side effects, blocking via `PushBackFunc` + `BasicPushSelect`) so it isn't a trivial extraction. Possibly worth a `TryPostBehavior` abstraction if other places grow similar shape, but not urgent.
