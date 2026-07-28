# Plan — remove the funnel engine; funnel state on the Wave + flushes on the shared Queue

Status: **DESIGN — under review with PN, no code yet.** This is the authoritative plan
for collapsing the per-Wave funnel machinery. It is a **down-payment on the
`global-substrate-activation.md` §7 deletion list** (`cpWorker`, the per-producer
loops, the per-wave funnel substrate), approached from the funnel side, and it
**supersedes that doc's §6** (per-Wave flusher goroutine) and the funnel parts of §2
(`waveCtx`). It builds directly on the zero-value-Wave (`zero-value-wave.md`) and
no-explicit-lifecycle funnel (`funnel-lifecycle.md`) work already landed.

## The realization

Today a `Funnel[T]` is a handle around a heap `*funnel[T]`, and each Wave lazily builds
a `funnelEngine`: a second `workq.Accepted` (a private scheduled-flush queue) plus a
**persistent flusher goroutine** (`cpWorker`) that drives deadline-driven flushes and
the end-of-work sweep. None of that machinery is funnel-specific — it duplicates what
the Wave and the global pool already own:

1. **Funnel body work already runs on `defaultPool`** (`funnelPostWork → defaultPool.Post`).
2. **The shared global `workq.Queue` already exposes the entire scheduled-work API** it
   would need to carry flushes: `Schedule` / `Reschedule` / `ClaimForFlush` /
   `DrainAllScheduled` / `Expedite`, a `deadlineCh`, and a `wakeScheduled` hook that
   nudges a parked worker when an item comes due (`internal/workq/queue.go`,
   `accepted.go`). The `funnelEngine.workQueue` is a *copy* of this.
3. **The Wave is the natural owner of the live instance state.** Accumulator instances
   are wave-scoped (each holds a per-wave barrier reference; all are force-flushed
   before the wave drains). They belong *on the Wave*, not behind a per-funnel heap
   object.

This matches the documented target model: `global-substrate-activation.md` §1 puts
*scheduled flushes* on the shared global Queue and makes the Wave's own queue the
**skim engine** (driven by the user's `Skim`/`SkimAll` goroutine); `dispatch-execution-
split.md` makes the **draining goroutine the wave's one serial skimmer** and keeps
flushes limiter-free (limiters gate intake, not drain), so the draining goroutine can
run the end-of-work sweep with no permit entanglement.

## Target object model

```
Funnel[T any] struct {        // plain value, no inner pointer
    wave         *Wave        // optional ambient binding (op.In(&w) equivalent)
    factory      AccumulatorFactory[T]
    limiter      Limiter
    id           funnelID     // unique per NewFunnel; copies share it → same instanceQueue
    instancePool *omnipool.Pool[funnelInstance[T]]  // omnipool.For[...]; cached ptr
    workPool     *omnipool.Pool[funnelWork[T]]      // omnipool.For[...]; cached ptr
}
```

- Copying a `Funnel` copies `{wave, factory, limiter, id, pools}` — all cheap (the pool
  fields are the cached `omnipool.For` pointers); all copies route to the same
  instanceQueue because they carry the same `id`. The "copies share identity" contract is
  preserved by the id, not a shared heap object.
- **The typed omnipools live on the `Funnel`** (DECISION 2, PN), initialized with
  `omnipool.For[funnelInstance[T]]()` / `omnipool.For[funnelWork[T]]()`. Keeping them
  typed on the funnel — rather than type-erased behind the wave map — is what avoids
  boxing `T` and keeps the `Accumulator[T]` call path fully generic (instances are
  `*funnelInstance[T]`, never `*funnelInstance[any]`).
- `errSink` folds to **one wave-level** framework error sink (the handler is identical
  for every funnel — it just returns the error to surface via `SkimAll`).

```
Wave struct {
    ... existing skim engine (skimQueue + its Accepted driver) + governor + state ...
    funnelInstances sync.Map   // funnelID → *funnelInstanceState[T] (behind a thin iface)
}
```

- The map value is a **typed** `*funnelInstanceState[T]` — `{queue
  nbcq.Queue[*funnelInstance[T]], instancePool *omnipool.Pool[funnelInstance[T]]}` —
  stored behind a thin `funnelInstanceState` interface (`forceFlushAll(ctx)`;
  `recycleSpent()`) so the heterogeneous-`T` map can hold it. The **only** type erasure
  is at the map boundary; every per-item operation (push/pop, `Accumulate`, `Flush`,
  recycle) runs through the concrete `[T]` type, so `T` is never boxed (DECISION 2).
- The typed `funnelWork` reaches its queue by `LoadOrStore`-ing the entry under the
  funnel id and asserting it back to `*funnelInstanceState[T]` (cheap). First touch for a
  (wave, funnel) creates the state from the funnel's `instancePool`.
- `funnelInstance` loses its `*funnel[T]` back-pointer and instead carries `{wave,
  factory}` (copied from the Funnel value at creation); its instance pool is the one held
  by `funnelInstanceState[T]` (the funnel's cached `omnipool.For` pointer).

No `funnelEngine`, no `cpWorker`, no per-wave flusher goroutine, no
`Wave.fEngine`/`fEngineMu`, no dead `funnelQueue`, no recycler registry.

## The two flush paths in the new model

**Deadline-driven (mid-wave).** A live instance with a flush deadline `Schedule`s itself
on **`defaultPool`'s shared Queue**. A global worker wakes on the shared Queue's
`deadlineCh` when it comes due and runs the flush, dropping that wave's barrier
reference. This fires promptly even on a *fully idle* wave (no Skim, no Submit in
flight) — the global workers are always there (DECISION B keeps one warm while a deadline
pends) — so the earlier "idle wave with a pending deadline" caveat is retired.
**No-deadline instances are not scheduled at all** (no placeholder); they live only in
their queue + the wave map and are flushed by the end-of-work sweep.

**End-of-work sweep — a `wavestate` callback, synchronous, enqueue-only (DECISION, PN).**
`wavestate` fires an `onFlushing func()` callback on the `Closed→Flushing` transition
(replacing the `FlushChan` close — the flusher was its only listener), on whatever
goroutine drove the last `DecrementWork` / called `Close`. The wave's callback is a
synchronous sweep that **runs no user code — it only enqueues flush work**: it walks
**this wave's** `funnelInstances` map and, per live instance `X`:

```
if defaultPool.ClaimForFlush(X) { defaultPool.ForceFresh(X) }
```

- `ClaimForFlush(X)` removes `X`'s delayq entry (no-op if never scheduled) and arbitrates:
  `false` ⇒ a due `Execute` already owns the flush, skip; `true` ⇒ we won, enqueue it.
- `ForceFresh(X)` pushes `X` onto the global Queue's fresh list + fires demand (spawns a
  worker if all idled out). A global worker then runs **`funnelInstance.Execute →
  forceFlush`** — the *same* path as a deadline-driven flush, so deadline and end-of-work
  flushes are one mechanism. (It is **per-wave via the map**, *not* `DrainAllScheduled`:
  the scheduled heap is shared across waves.)
- **Enqueue-only removes the re-entrancy hazard**: the transition goroutine touches no
  `Flush` body and no skim queue, so it can't re-enter anything. The flushes run later on
  global workers, exactly as ordinary funnel body work does.
- Your reference-ordering invariant holds unchanged: a `Flush` body that emits downstream
  takes its work reference *before* the barrier drops, so `totalReferences` never
  transiently hits zero across an emitting flush. Emit ⇒ wave revives; no emit ⇒ the last
  barrier drops → Done → `SkimAll` returns `ErrWaveDone`.

## Concurrency: flush vs. sweep vs. recycle (the actual core)

Today, deadline-driven flushes AND the end-of-work sweep both run on the **single**
`cpWorker` flusher goroutine, so they never overlap. Moving deadline flushes onto the
**global pool** (N workers) while the sweep enqueues from the transition goroutine lets a
deadline `Execute` and the sweep race for the same instance (a scheduled-flush `Execute`
holds the per-instance barrier ref but does *not* gate `Closed→Flushing`). What keeps it
safe:

- **The flush is arbitrated, not serialized.** `ClaimForFlush` grants the flush to exactly
  one party (it serializes with the delayq drain on `q.mu`); the loser sees
  `accumulator==nil` under the instance mutex and no-ops. At most one `Flush` runs per
  instance regardless of who races. The sweep enqueues only the instances it *won*.
- **Recycle rides the pop, via rule R2.** `forceFlush` captures the owning state into a
  local *before* taking the instance mutex and touches only that local afterward — so it
  **never references the instance once it releases the mutex** (rule R2, already documented
  on `forceFlush`). A flushed instance is therefore out of the delayq and, once popped
  from its queue, held *exclusively* by the puller. **So any time a spent instance is
  pulled from the queue it can be recycled immediately** (PN) — which is exactly what the
  owner lineage already does (funnel.go:604). Recyclers lock/unlock the instance mutex
  first (a barrier against an in-flight `forceFlush`, which can fire `Done` before its
  deferred `Unlock`), then `Put`.
- **Recycle happens on pop:** the owner lineage during active accumulation (unchanged),
  and `initState` at re-arm draining each queue's leftover spent shells (the end-of-work
  flushes leave them cached) → lock/unlock + `Put`, then clear the map (drop stale
  `funnelID→state` entries so a reused/pooled `*Wave` doesn't accumulate them; ids are
  unique per `NewFunnel`). At re-arm the wave is Done ⇒ every flush `Execute` has
  completed ⇒ draining is quiescent.
- **Re-arm safety bracket.** The synchronous sweep brackets itself with
  `IncrementReference`/`DecrementReference` so a deadline `Execute` that drained *before*
  the sweep (which the sweep skips via `ClaimForFlush==false`) cannot drop the last
  barrier and drive the wave to Done — hence into a concurrent re-arm — *while the sweep is
  still ranging the map*. This is the analogue of the old `joinFlusher`-before-rearm
  barrier; verify under `TestBySimulation -race` rather than assume.
- **`FlushChan` is removed** (with its rotation hazard): the only listener was the flusher,
  now replaced by the `onFlushing` callback. The skim path (`skimSelect`) is **untouched**
  — it sees the sweep's enqueued flushes' downstream work and `Done` exactly as it sees
  the flusher's today.

## Worker idle vs. pending deadline (DECISION B, PN)

Global workers idle-exit after 1s (`WithIdleExit`), which would abandon a pending flush
deadline further out than that (the worker fires `idleCh` before `deadlineCh` in
`selectWork`, leaving the scheduled item with no watcher). Fix: **a worker does not
idle-exit while a scheduled deadline is pending** — in `Worker.pull`, suppress `armIdle`
when `deadlineCh != nil`. Cost: while a real flush deadline is outstanding, one shared
global worker stays warm instead of scaling to zero — strictly better than today's one
perpetual flusher goroutine *per funneling wave*. Bounded to **real** deadlines because
no-deadline instances are no longer scheduled (next section).

### Bug found in verification: DECISION B pinned the spawn-concurrency token

DECISION B introduced an intermittent nested-drain hang (≈1/40 `TestBySimulation` runs):
four `CloseAndSkimAll`s stacked, none reaching Done, no live pool worker. Root cause: the
pool's `spawnConcurrencyLimit` (1) is held by a worker from spawn until it **secures
work** (`onSecure`) or **exits**. A worker that idles out releases it on exit — but
DECISION B keeps a deadline-watching worker *parked instead of exiting*, so it held the
spawn token **forever**, pinning the limit. A `Close`-triggered flush sweep runs
`onFlushing` on the closing goroutine (which then parks in `SkimAll`), so the flush needs
a *freshly spawned* worker — and with the token pinned, no spawn happened. Under nesting
the wave wedged. Confirmed by bisection (revert B → 0 hangs) and the mechanism.

**Fix:** a parked worker has *settled out of the spawn stampede*, so it must release the
spawn token at the **park point**, not only at work-secure. Added `workq.WithOnWait` (fires
when `Worker.pull` commits to a blocking wait); `worker.Pool` wires it to
`releaseSpawn(false)` (release, no chain extension). Both `onSecure` and `onWait` go
through the same idempotent release, so the token is held only during the genuine
spawn→first-settle window — the de-stampede it was for — and a deadline-parked worker no
longer starves new spawns.

### Bug found in verification: `ForceFresh`'s demand was a no-op with no parked waiter

A second spawn gap, exposed under `-race`. `ForceFresh` fired demand via
`waiters.Notify(unmetDemandFn)`, but `Notify` only *delivers* the signal to a parked
worker inbox — `inboxOnlyQueue.TryPushBack` returns false (and never invokes the fn) when
no worker is waiting. So a flush the end-of-work sweep enqueued while **every** worker was
busy inside a body (no parked waiter — the wedge) never spawned a driver: it sat in
`fresh` forever. (`Post` avoids this by calling `unmetDemandFn` *directly*;
`queueFresh` gets away with `Notify`-only because it runs inside a live drive whose own
worker handles the base case.)

**Fix:** `ForceFresh` spawns directly when `Notify` finds no waiter —
`if !q.waiters.Notify(q.unmetDemandFn) && q.unmetDemandFn != nil { q.unmetDemandFn() }`.

### Verification: at baseline parity (pre-existing nested-drain hang)

With both fixes, the default-config `-race` `TestBySimulation` hang rate dropped from the
regressed **~1/40** back to **~1/120** — **the same rate as the pre-`combiner` baseline**
(dedicated per-wave flusher goroutines), confirmed by a 120× `-race` run of each. The
residual ~1/120 wedge is **pre-existing**: the baseline hangs identically (its dump shows
`funnelEngine.flusher`/`cpWorker` + nested `CloseAndSkimAll`), and it is the dispatch/
execution **conflation** — a pool worker that runs a blocking body (nested
`CloseAndSkimAll`, or a funnel accumulate blocked in the wave governor while holding the
instance mutex) can't also dispatch, so under deep nesting the relief path starves. This
is exactly what `dispatch-execution-split.md`'s manager/executor split exists to fix, and
is **not** introduced by this cut. Tracked as a known issue for that work.

So: the two spawn bugs above were genuine **regressions** introduced by moving flushes onto
the shared pool (the dedicated flushers had masked the dependency); fixing them restores
parity, and flush-on-pool is sound up to the pre-existing conflation.

## No-deadline instances are not scheduled

The old 24h `maxFlushAllSkew` placeholder existed only so `DrainAllScheduled` could find
no-deadline instances at end-of-work. The new sweep finds them via the wave's instance
map instead, so a no-deadline instance is **never scheduled** — it lives only in its
queue and is flushed by the sweep. This also keeps DECISION B's idle-suppression bounded
to genuine near-term deadlines rather than pinning a worker warm for a 24h placeholder.

## What deletes

`funnelengine.go` (the whole `funnelEngine` type, `spawnFlusher`/`joinFlusher`/
`flusherDone`, `registerFunnel`/`recycleFunnelInstances`, `funnelPostWork`); `cpworker.go`
(entirely); the dead `funnelQueue`; `Wave.fEngine`/`fEngineMu`; `wave.go`'s
`funnelEngine()` lazy-build; `ensureArmed`'s flusher-join; the `funnel[T]` heap struct
(its config folds into `Funnel[T]` and `funnelInstance`).

## Implementation — one cut (DECISION 3, PN: "do it all")

No vestigial-shell half-step. The artificial CP1/CP2 boundary (a `funnelEngine` that
exists only to be deleted next) creates a harder-to-reason-about intermediate than the
clean end state, and the substrate doc reached the same conclusion ("no seam is small";
the cutover is "likely ONE commit"). So this lands as a single cohesive change:

- Real-deadline funnel instances `Schedule`/`Reschedule`/`ClaimForFlush` on `defaultPool`
  (the embedded Queue already promotes those methods); add `defaultPool.ForceFresh` (the
  back half of `Expedite`: push to fresh + fire demand) for the sweep. No-deadline
  instances are not scheduled.
- `Funnel[T]` → `{wave, factory, limiter, id, instancePool, workPool}`; `funnelInstance`
  carries `{wave, factory}`; instanceQueues become the wave's `funnelInstances` `sync.Map`
  behind `*funnelInstanceState[T]`; `errSink` → wave-level.
- `wavestate`: replace the `FlushChan` close with an `onFlushing func()` callback; the
  wave sets it to its synchronous enqueue-only sweep (reference-bracketed). Recycle on pop
  (owner lineage) + `initState` (drain leftover spent shells, clear map).
- Delete `funnelengine.go`, `cpworker.go`, the flusher goroutine, the dead `funnelQueue`,
  the recycler registry, `Wave.fEngine`/`fEngineMu`, and the `funnel[T]` heap struct.

**Verify** (the design review's job is done; this is the safety net): full suite +
`-race` suite + `reuse_test.go` + 30–40× `-race` `TestBySimulation` (`sim-trace-debugging`
skill on any hang) + the alloc tests. The re-arm bracket and DECISION-B idle suppression
are the specific things to confirm under `-race` sim, not assume.

## Reconciliation with `global-substrate-activation.md`

- **§6 (the flusher)** is superseded: there is **no per-Wave flusher goroutine**.
  Deadline flushes ride the global workers (the shared Queue's `deadlineCh`); the
  end-of-work sweep is a synchronous enqueue-only `onFlushing` callback that pushes flush
  work to the global pool. (§6's "deadline-driven flushes still ride the generic Worker"
  was already the intended end state; this plan extends it to the end-of-work sweep too.)
- **§2 (`waveCtx`)** is already gone for the funnel path: the zero-value Wave owns no
  context; instances and flushes derive cancellation from the executing goroutine's ctx
  / the wave's `state.Done()`, not a wave-owned cancel.
- §1 (shared Queue holds scheduled flushes; Wave queue = skim engine) and §7 (deletion
  list) are **confirmed and partially executed** by this plan.

## Resolved decisions (PN, 2026-06-24)

1. **`funnelID` is a package-global monotonic counter** (like the group-id generator).
2. **The instance/work pools are typed and live on the `Funnel`** (`omnipool.For`).
   Type erasure exists only at the wave-map boundary (`*funnelInstanceState[T]` behind a
   thin interface); per-item handling stays `[T]`, so `T` is never boxed and the
   `Accumulator[T]` signature stays generic.
3. **One cut — no vestigial half-step.** Implement the full end state and verify, rather
   than landing an intermediate `funnelEngine` shell.
