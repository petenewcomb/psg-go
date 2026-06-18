# Global-substrate activation — design (DESIGN REVIEW, pre-code)

Status: **DESIGN — under review with PN, no code yet.** This is the authoritative
plan for the next consolidation phase (supersedes the retracted per-job
"cp-5 FunnelPool→worker.Pool cutover"; see WORKING_NOTES "CORRECTION
(2026-06-16, PN)"). It grounds the converged model — one global `worker.Pool` +
one shared `workq.Queue` + a context-free unified `E`, with admit/drain/cancel/
flush all **per-Wave** — in the actual current code.

---

## 0. The naming trap that reshapes everything

`Pool` (job.go:29) is **the JOB**, not a worker pool. A `Wave` binds 1:1 to one
`Pool` (`wave.go:22`, `ownsPool`). That single struct conflates **two roles** the
consolidation must split:

| Role | Today (on `Pool`/job) | Destination |
|---|---|---|
| **Worker substrate** | `taskQueue` (rdvq) + task workers (`runTasks`); `FunnelPool` goroutines (`cpWorker`); spawn machinery (`cpstate`, demand counters, idle/jitter) | **Global `defaultPool` + shared `workq.Queue`** (already exist, dormant) |
| **Per-batch control** | `governor`; `skimQueue`; per-job `inFlight`/drain; cancellation (`cancelFn`); the funnel flush hook | **The new `Wave`** (per-wave governor, skim Queue, in-flight, waveCtx, flusher) |
| **Job lifecycle** | `jobstate.JobState`: Open→Closed→Flushing→Done; `inFlightWork`+`totalReferences`; `FlushChan` rotation; `doneChan` | **OPEN (Q1)** — folds into `Wave` (per-wave lifecycle) |

So "delete the per-job pools" = dismantle `Pool`'s substrate + control roles into
the global pool and the Wave, leaving (a reshaped) job-lifecycle as the Wave's
internals. `Pool` as a type likely **disappears**, its lifecycle state becoming
the Wave's.

---

## 1. Target object model

```
package-global (pool.go):
  defaultPool *worker.Pool[*workerExEnv]   // embeds the shared task/funnel workq.Queue
      ├── shared Queue  (incoming handoff + accepted priority engine + scheduled flushes)
      └── demand-driven goroutines, each holding one *workerExEnv (context-free E)
  func Wait()  =  defaultPool.Wait()

per-Wave (wave.go, rewritten):
  type Wave struct {
      waveCtx    context.Context     // WithCancel(poolCtx); subwave = WithCancel(parent.waveCtx)
      cancel     context.CancelFunc
      governor   workq.Governor      // top-level admission gate; aggregates this wave's sources
      skimQueue  workq.Queue         // SEPARATE queue, driven by user Skim goroutines (NOT the pool)
      inFlight   <per-wave drain counter>            // → wave done when 0
      lifecycle  <jobstate-like Open→Closed→Flushing→Done>   // Q1
      shells     <per-wave execCtx shell pool (nbcq)>        // wave-5b
      flusher    <per-wave goroutine: job-end force-flush of not-yet-due funnels>
  }

per-execution (wave-5b):
  the WORK item carries its wave (already true: taskWork.wave, funnelWork.wave).
  At Execute, the body borrows an execCtx shell from its wave, stamps the worker's
  E into shell.meta, runs under shell.execCtx, returns the shell.
```

**Two Queues, by design** (confirmed against legacy `Pool.taskQueue` vs
`Pool.skimQueue`):
- **Task/funnel engine** — the ONE shared `workq.Queue` embedded in `defaultPool`,
  driven by pool goroutines. Task and funnel work are both `workq.Work` run by the
  same workers (this is why `taskExEnv`+`cpWorker` collapse into one `E`).
- **Skim engine** — a **per-Wave** `workq.Queue`, driven by **user `Skim`/`SkimAll`
  goroutines** (`DriveUntilDrained`), `unmetDemandFn == nil` (no spawn — the user
  goroutine is the driver). Skimmers are the serial drain / backpressure source.

---

## 2. The wave-5b context model (settled in WORKING_NOTES; restated)

Three contexts by ancestry `poolCtx → waveCtx → execCtx`:

- **`poolCtx`** — the global pool's context. Cancels ONLY when `Wait()` is
  outstanding AND refs hit 0 (definitive teardown). **Replaces `worker.Pool`'s
  `stop chan`** (same `refs==0 && waiting` condition); the pool EXPOSES it so waves
  derive from it. → *This is the first concrete code step: `stop chan` → `poolCtx`,
  expose `PoolCtx()`.*
- **`waveCtx = WithCancel(poolCtx)`** — per-wave cancel + global teardown both via
  stdlib ancestry. A subwave = `WithCancel(parentWave.waveCtx)`.
- **`execCtx = WithCancel(waveCtx)`** — pooled per-wave (nbcq; prior art =
  `funnelInstanceQueue`), reused not cancelled. **The ctxMeta lives on the execCtx,
  never on the worker.** Distinct per-shell done channels avoid shared-`waveCtx.Done`
  park-lock contention.

**E placement = per-worker (A, settled).** `Sender`/`Receiver` (the rdvq buffering
substrate, now internal) stay bound to the worker goroutine — capacity scales with
worker count = system parallelism. The worker hands its `E` to `work.Execute` via a
**generic workq channel** (a ctx value under a workq key, or a field on
`Execution`) — **NOT** via the worker ctx's ctxMeta. `work.Execute` (which knows its
wave) borrows an execCtx shell, stamps `E` (+ group + heldRequest) into
`shell.meta`, runs the body under `shell.execCtx`, returns the shell. Borrow/return
drives the per-wave in-flight counter.

**Worker** holds `E` + a `poolCtx`-derived ctx used ONLY for the idle/cancel
`selectWork` case. Bodies never run under the worker ctx (overturns the FIRST-CUT
`execCtx()` in worker.go:249 that returns `w.ctx`).

**Why cross-wave handoff is safe:** a full outbox is owned by the DOWNSTREAM queue
(rdvq design), not the Sender, so a send **completes at handoff-or-buffer, not at
consume**. The per-wave in-flight decrements at send-completion → a wave drains
without waiting on a downstream receiver; an abandoned buffered item, if later
dequeued, runs under an already-cancelled borrowed shell → body aborts cleanly.

---

## 3. The unified producer — `workq.Queue.Post` (already hardened)

All three legacy producers (`taskPostWork` job.go:948, `funnelPostWork`
funnelpool.go:241, `skimPostWork` job.go:556) are the **same loop skeleton**
(`tryPost → ShouldBlockOrPostpone? → listen | block`), differing only in:

| | bufferedFn | onWait (governor reg) | block select |
|---|---|---|---|
| task | `registerDemand` | — (none) | `BasicPushSelect` |
| skim | `nil` | `work.Waiting(&job.governor)` | `BasicPushSelect` |
| funnel | `maybeSpawn` | `work.Waiting(&pool.governor)` + spawn | **custom `spawnWaitCh` select** |

`Queue.Post(ctx, ex, shouldBlock, w, onWait)` (queue.go:77) already absorbs the
first two columns: `bufferedFn = rdvq.BufferedFunc(unmetDemandFn)` (uniform demand →
pool spawn), `onWait` = the governor-registration hook. **The funnel's custom
`spawnWaitCh` select is DROPPED** (DECISION C, WORKING_NOTES): `worker.Pool`'s
uncapped demand model replaces cap-aware elastic scaling, so funnel `Post` becomes
the clean skim/task-style handoff. `ShouldSpawn*`/`SpawnNotifier`/`MaxConcurrency`
delete.

So each producer collapses to one call:
```go
posted, err := q.Post(ctx, ex, meta.ShouldBlock(), w, onWait)
// q = defaultPool's shared Queue (task/funnel) OR wave.skimQueue (skim)
// onWait = nil (task) | func(){ w.Waiting(&wave.governor) } (skim/funnel)
```

**Note:** the `dispatch.go` sketch's `q.Post(ctx, ex, deadline, w)` signature is
stale (no `deadline` param; handoff uses ctx). Fix when un-excluding.

---

## 4. `submit` — the uniform dispatch (dispatch.go, to un-exclude)

```
submit(ctx, meta, ex, q, wave, w):
    handoff := func(ctx, ex) error { posted, err = q.Post(ctx, ex, meta.ShouldBlock(), w, onWait); return err }
    if !meta.IsTopLevel() { return handoff(ctx, ex) }          // non-top-level: unconditional
    return wave.governor.Execute(ctx, ex, deadline, bb, handoff) // top-level: admission gate
```

- **Non-top-level submits are NEVER gated** (deadlock-freedom invariant): a body
  holding permit P that had to acquire before acceptance would hold-and-wait.
- **Limiting is post-admission, at the worker** (`runUnderLimiter`, head of
  `Work.Execute`): acquire-or-postpone; a rejected request becomes the scheduler's
  on-deck candidate and registers on the wave governor. (limiter wiring already
  lives in `limiterScatterWork` / `request`; this phase keeps it transitional — the
  scheduler/on-deck→governor registration is a later step, see Q5.)
- `q` routes by op type only: launcher/funnel → shared pool Queue; skim →
  `wave.skimQueue`.

---

## 5. The unified `E` and the body-entry cutover

`workerExEnv` (pool.go:42, context-free `integrationExEnv` + no-op Lock/Unlock +
`ExecuteNowOrQueue → defaultPool`) already satisfies BOTH `workq.ExecEnv` (empty)
and the main-package `executionEnvironment` (8 methods, all from `integrationExEnv`
except the three it defines). It **subsumes** `taskExEnv` (one-group, no stacks)
and `cpWorker` (group stacks + funnel driver state).

Body-entry sites that move from per-engine exEnv to the borrowed shell + unified E:
- **Task body** (`taskWork.Execute`, job.go:118-137): stamps `meta.wave`,
  `meta.heldRequest`, asserts `parent==nil`. Today runs on `taskExEnv` via the
  worker ctx. → runs on the borrowed shell whose `meta.executionEnvironment` is the
  worker's `*workerExEnv`.
- **Funnel body** (`funnelWork.executeInner`, funnelop.go:798-826): the ONE type
  assertion `meta.executionEnvironment.(*cpWorker)` (funnelop.go:801) →
  `.(*workerExEnv)`; `cw.executeFunnel` (which calls `bc.Funnel(ctx, sender)`)
  moves onto the unified E (drops `IncrementCompleted`, a write-only cpstate
  metric). `PushGroup/PopGroup` already on `integrationExEnv`.

`taskExEnv`, `cpWorker`, `topLevelExEnv`(?) disposition — see Q3.

---

## 6. The flusher (validated design; now per-Wave, not per-pool)

End-of-work force-flush of not-yet-due funnel instances moves OFF the worker loop
(legacy: `cpWorker.flushAll` via `nextJobFlushCh`) to a **dedicated per-Wave
goroutine** watching the wave-lifecycle FlushChan:
```
flusher (started in NewWave, before any work):
  state, ctx, cancel := wave.newShellState(); defer cancel(); defer state.Release()
  for {
    flushCh := wave.lifecycle.FlushChan()   // re-subscribe each cycle (rotated)
    select {
    case <-flushCh: wave.flushAll(ctx, state.Sender())   // DrainAllScheduled + forceFlush each
    case <-wave.lifecycle.Done(): return
    }
  }
```
Validated facts (WORKING_NOTES "Flusher prototype"): the flusher uses its OWN
sender/ctx (instances resolve the sender from the executing goroutine at flush
time, not at allocate); `flushAll` is idempotent (`ClaimForFlush` vs deadline
Execute; `flush` no-ops when `accumulator==nil`); references don't gate
Closed→Flushing (an idle live instance still lets the wave flush). Deadline-driven
flushes still ride the generic `Worker` (`selectWork`'s `deadlineCh`).
Riskiest edge: a self-recursive funnel re-populating across Flushing cycles; add a
buffered-signal backstop if the sim hangs.

---

## 7. Deletion list (end state)

`cpstate` package; `cpWorker`; `taskExEnv`; `Pool.taskQueue`/`runTasks`/spawn-task
machinery; `FunnelPool` (goroutine/spawnNewGoroutine/state/unmetDemandFn);
`taskPostWork`/`funnelPostWork`/`skimPostWork` (→ `Post`); the per-producer
hand-rolled loops; `ShouldSpawn*`/`SpawnNotifier`/`MaxConcurrency`; idle
jitter/throttle. Options fallout: `WithMaxConcurrency`/`WithIdleTimeout`/
`WithIdleJitter` (used by `maxholdtime_test.go:36` SERIAL assumption,
`example_funnel_test.go:54`, `funnel_legacy_bench_test.go:602`) — `maxholdtime`
must migrate to a limiter (`WithLimits`); others become no-ops or delete.

---

## 8. Proposed checkpoint sequence (each lands green)

The foundation debate (incremental-in-place vs parallel-build-then-cut) concluded
"no seam is small" because producers/queues/spawn/governor are mutually coupled.
With the global substrate already dormant-but-compiled, the realistic sequence:

1. **`poolCtx` foundation** (worker.Pool internal): `stop chan` → `poolCtx`/cancel,
   expose `PoolCtx()`. Still dormant. *Green, self-contained.* — folds into commit
   with step 2 (Q2). **✓ DONE (2026-06-17, uncommitted):** `stop chan` →
   `poolCtx context.Context`+`poolCancel`; `stopWorkersLocked`=`poolCancel()`
   (idempotent), `rearmStopLocked`=fresh `WithCancel` when cancelled; `spawnWorker`
   captures `poolCtx.Done()` (behavior-identical to the old captured `stop`);
   `PoolCtx()` exposed for waves. Worker exec ctx still the `newState` placeholder
   (deriving it from poolCtx is wave-5b/cp-3). Build/vet/`-short ./...`/lint(0)/
   `TestBySimulation -race rapid.checks=100` all green.
2. **Wave substrate, dormant**: build the new `Wave` internals (waveCtx, governor,
   skimQueue, in-flight, shell pool, flusher, lifecycle) ALONGSIDE the legacy
   `Pool`-bound Wave, not yet dispatched into. *Green if it compiles unused* (may
   trip `unused` lint → may have to merge with step 3).

   **FINDING (2026-06-17): CP2 fuses into CP3 at the ctxMeta seam — earlier than
   hoped.** The execCtx-shell must carry a reusable `*ctxMeta`, but ctxMeta creation
   is deeply `Pool`(job)-bound: the `ctxMetaMap` cache, the `j.ctx` `AfterFunc`
   cancel-chaining (ctxmeta.go:425-431, links each derived ctx to the job ctx), and
   `ctxMeta.job *Pool`. The shell *replaces* the `ctxMetaMap` caching (the shell IS
   the cached, reused meta) and `ctxMeta.job` must become a wave/lifecycle reference
   under waveCtx ancestry (not `j.ctx`). So the shell pool cannot be built against
   the *current* ctxMeta shape without the CP3 ctxMeta rework. **`poolCtx` (CP1) was
   the only cleanly-separable dormant piece.** CP2+CP3 proceed together as the cut.

   **⇒ KEY CP3 DECISION (open, for next session): ctxMeta in the per-Wave model.**
   - `ctxMeta.job *Pool` → what? (a wave/lifecycle backref; the per-wave `jobstate`
     replaces the per-job one). The `parentJobs` map, cross-job panics, and the
     `currentHeldRequest` parent-walk all key off `job` identity — re-target to
     wave/lifecycle identity.
   - The `ctxMetaMap.WithValue` caching (one meta per ctx, job-keyed) → the execCtx
     **shell** is the reused meta; borrow stamps `executionEnvironment`(=worker E),
     `group`, `heldRequest`, `wave`; the shell's ctx is `WithCancel(waveCtx)` (not
     `WithCancel(ctx)`+`AfterFunc(j.ctx)`).
   - Top-level/skim ctxMeta (`topLevelCtxMeta`/`skimCtxMeta`, the USER-goroutine
     side) still need job/wave-keyed caching for the dispatch entry — only the
     WORKER-side meta becomes a shell. So two meta lifecycles: user-dispatch (cached,
     `topLevelExEnv`) and worker-exec (shell, `workerExEnv`).
3. **Cut over — atomic-ish**: wire `submit` + per-wave execCtx borrow; collapse the
   three producers onto `Post`/skim `Post`; switch body-entry to the unified E
   (funnelop.go:801); start using `defaultPool`. Delete the legacy substrate. This
   is the big one — likely ONE commit because the unified E is unused until wired
   (`unused` lint) and the three producers share the wave/governor.
4. **Validate**: funnel + task + skim suites, `-race`, `TestBySimulation`
   (`sim-trace-debugging` skill on hang), sim TEMP teardown config.
5. **Cleanup**: delete superseded prototype tests; options fallout; doc pass.

Whether **skim can cut over first** (separate queue, no spawn coupling, no governor
admission since skim is always non-top-level) as an earlier independent green step
is the most promising sub-seam — see Q4.

---

## 9. Review resolutions + the remaining crux

**Resolved with PN (2026-06-17):**

- **Q1 — jobstate is per-Wave. CONFIRMED.** The Wave owns a `jobstate.State` (the
  "wave lifecycle"); `Acquire`/`Release` the global pool bracket the wave's active
  span; `psg.Wait()` is the global join. The `totalReferences` vs `inFlightWork`
  split (references = live funnel instances that do NOT gate Closed→Flushing) must
  survive per-wave.
- **Q2 — fold `poolCtx` into the Wave-substrate commit.** No standalone commit for
  an exposed-but-unconsumed ctx.
- **Q3 — `topLevelExEnv` stays.** Its mutex (concurrent top-level dispatch on one
  wave) and group allocation are load-bearing. "Re-pointed at `defaultPool`" = its
  `ExecuteNowOrQueue` destination changes from `&job.workQueue` (per-job priority
  engine, which disappears) to the shared Queue (via `defaultPool`), exactly as
  `workerExEnv` already does. Whether top-level dispatch still flows *through*
  `ctxMeta.ExecuteNowOrQueue` or `submit()` replaces it is the crux below.
- **Q4 — skim is NOT always non-top-level (sketch was WRONG).** `Skimmer.Submit`
  resolves `ctxType` from the caller (skimop.go:136): from a top-level goroutine it
  is `topLevelContext` → `IsTopLevel`/`ShouldBlock` true → a governor-gated
  top-level submit; only from inside a body is it nested. So skim shares the wave
  governor and the new Wave substrate → it CANNOT cut over before that substrate
  exists. **Decision (my call): no skim-first; fold skim into the main cut.**
  (Behavior note: a top-level skim submit becomes governor-gated, which legacy
  did not do — arguably more correct, but flag it in validation.)
- **Q5 — limiter wiring stays transitional; don't bend over backwards to avoid
  touching it** (PN). Preserve `suspendForEpisode`/`reclaimRequest` (cross-job
  permit suspend across a blocking dispatch) and `governor.Waiting` as-is; defer the
  scheduler/on-deck→governor redesign. Touch limiter code where natural, no forced
  detours.
- **Q6 — E-passing: decide in impl.** Lean (b) a field on `workq.Execution` (no
  per-exec ctx alloc; Execution is already threaded) unless it muddies the
  workq/psg contract, then (a) ctx value under a workq key.

**THE REMAINING CRUX — dispatch layering: `submit` vs `ExecuteNowOrQueue` vs `Post`.**

Two different `ExecuteNowOrQueue`s exist:
- `ctxMeta.ExecuteNowOrQueue` (ctxmeta.go:184) — the **dispatch entry**. Top-level
  path does the ceremony (`suspendForEpisode`+reclaim, `wait()`, `job.yield()`
  backpressure, force block-not-listen) then calls the exEnv method.
- `executionEnvironment.ExecuteNowOrQueue` — the inner routing onto the priority
  engine.

The work that dispatch runs is the **producer** (`*PostWork`), whose `Execute` IS
the handoff loop; the **body** (`taskWork`) always runs later on a worker. So today
the dispatch path never runs a body inline — it runs the *handoff* on the caller.

**Therefore the new `submit()` (= `governor.Execute` → `Post`) is a near-exact
replacement for `ctxMeta.ExecuteNowOrQueue`:** `Post` hands the **body work** off
directly, and the producer work-types (`taskPostWork`/`funnelPostWork`/
`skimPostWork`) **delete entirely**. Open sub-questions for PN:

1. **Top-level ceremony — KEEP it (PN).** `suspendForEpisode`+reclaim AND
   `wait()`+`job.yield()` relocate into `submit`'s top-level path. The
   yield/backpressure is the **"old (accepted) work before new work" principle** —
   process already-accepted work before admitting new top-level intake — which is
   **independent of the governor** (the governor gates on downstream saturation;
   the yield ensures forward progress of in-system work). Both mechanisms coexist.
   In the new model `yield` becomes a help-drive (nested `Worker.DriveOne`) over the
   wave's already-accepted work (shared Queue priority engine + wave skim queue);
   working out exactly which queue(s)/wave-scope is a hardening detail.
2. **`executionEnvironment.ExecuteNowOrQueue` — KEEP it (PN).** Real inline-execution
   cases survive, e.g. driving a sub-wave's skimmers from a body (the body runs the
   sub-work on its own goroutine rather than handing off). So `workerExEnv.Execute‑
   NowOrQueue` is load-bearing and the priority-engine inline-or-postpone path stays.

**Dispatch composition (hardening note, resolve at the keyboard for checkpoint 3).**
With producers (`*PostWork`) deleted, `executionEnvironment.ExecuteNowOrQueue` now
operates on **body work** directly: try to run it inline (subwave/skimmer case),
else postpone to the priority engine. `Post` is the handoff used when work must
reach a *different* worker (top-level intake; the can't-run-inline path). The exact
composition — does `submit` call `exEnv.ExecuteNowOrQueue` which internally `Post`s
on the can't-run-inline branch, or does `submit` choose `Post` vs inline up front —
gets pinned against `accepted.ExecuteNowOrQueue`/`ExecuteOne` when coding, not in
this review. The DECISIONS above are settled; this is mechanism.

---

## 10. Design review status: COMPLETE (2026-06-17)

All open questions resolved with PN. The model: one global `defaultPool` + shared
`workq.Queue` + context-free unified `E`; per-Wave `jobstate` lifecycle + governor +
skim Queue + execCtx-shell pool + flusher; `submit` (governor-gated top-level,
unconditional non-top-level) relocates `ctxMeta.ExecuteNowOrQueue`'s ceremony and
collapses the three `*PostWork` producers onto `Queue.Post`; `cpstate`/`cpWorker`/
`taskExEnv`/`FunnelPool`/per-job pools delete. Checkpoint sequence in §8 (poolCtx
folded into the Wave-substrate commit per Q2). Ready to implement on PN's go-ahead.
