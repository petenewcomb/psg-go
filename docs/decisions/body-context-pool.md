# Body-Context Pool (ctxMeta reuse, source-ctx-keyed)

> **DESIGN — converged with PN 2026-06-21. Anchors thread B (the 2026-06-21b Wave
> lifecycle). LANDED in the B3 cutover (`e740d33`) + B3.C.** Authoritative for the
> context model. This is the mechanism that replaced the per-wave `waveCtx` +
> `execShell` machinery — so the "Today … a Wave owns three ctx handles" snapshot in
> the Problem section below is the **pre-change** state (now removed; kept for
> rationale). See `WORKING_NOTES.md` (top banner) for the surrounding lifecycle,
> `surface-lineage.md` for the superseded forms, and `context-metadata.md` for the
> (dated) prior caching record this evolves.

## Problem

The finalized Wave lifecycle says **ctx is driver-specific — a Wave owns no ctx**
(like the internal Pool). Today a `Wave` owns three ctx handles and the body-execution
machinery is built on them:

- `Wave.ctx`/`cancelFn` — pool ctx `= WithCancel(parent)` (`job.go`, from `NewWave`)
- `Wave.waveCtx`/`waveCancel` — `= WithCancel(defaultPool.PoolCtx())`, the root every
  `execShell` body ctx derives from (`job.go:189`, `execshell.go:115`)

`execShell` (`execshell.go`) bundles three things into each reused per-wave body ctx:
1. a reusable `ctxMeta` (re-stamped per borrow) — **the part worth keeping**;
2. per-wave cancellation, `WithCancel(waveCtx)`, so `Wave.Cancel` force-aborts the body;
3. a distinct done-channel per shell (so bodies don't all park on one `waveCtx.Done()`).

The finalized model **drops force-abort** (cancellation = the drive/submit ctx, pure).
Once (2) goes, (3) — which existed only to give (2) distinct channels — goes with it.
What's left is (1): a poolable, re-stampable `ctxMeta` plus the child ctx that carries it.

## Model

Build on what `ctxmap` (`internal/ctxmap/ctxmap.go`) already does: keyed by
`context.Context` identity, it mints a value **once per unique context** (fast-path
`cache.Load(ctx)`), stamps it onto a child ctx via `WithValue(ctx, key, value)`, and
**auto-evicts via `context.AfterFunc(ctx, remove)`** when the ctx is cancelled. Since
user/driver contexts are typically reused across a whole batch, mint-once-per-unique-ctx
already gives high reuse.

**The change:** the cached value per source ctx goes from a single `*ctxMeta` to a
**pool of `{meta, childCtx}` shells**. Each pooled `ctxMeta` owns its child ctx, minted
once as `WithValue(sourceCtx, ctxMetaValueKey{}, &meta)`.

The pool is the **same `nbcq` reuse-cache pattern** already used for per-execution
shells (`execShellPool.free`, `nbcq.Queue[*execShell]`, `execshell.go:65`) and spent
funnel shells (`instanceQueue`, `nbcq.Queue[*funnelInstance[T]]`, `funnel.go:318`): a
lock-free free-list of shells plus the mint inputs (the source ctx and the
ctx-determined `parentJobs`). Borrow = `TryPopFront` (or mint on miss); return =
`PushBack`. The high-water build-up of idle shells is the same concern flagged for the
other two nbcq caches (TODO wave-5b "trim excessive build-up") — a shared trim/cap over
the pattern would cover all three.

To run a body (or any internal work needing a wave-stamped ctx):

1. **Borrow** a `{meta, childCtx}` from the pool keyed by the source (submit/drive) ctx.
2. **Stamp** the meta with the call-specific fields: the **wave** (the meta is now
   *wave-agnostic* — wave is per-borrow, not per-mint), `ctxType`, `parent`
   (severed to `nil` for a fresh permit-root async body; the enclosing meta otherwise),
   `parentJobs`, `heldRequest`, and the executing `executionEnvironment`.
3. **Run** the body under `childCtx`.
4. **Return** the `{meta, childCtx}` to its pool.

The child ctx is a genuine stdlib descendant of the source ctx, so cancellation,
deadline, and value propagation (Flow, trace) all flow by ancestry — **no `waveCtx`,
no force-abort, no custom context type, and zero per-borrow alloc** in steady state
(mint only when the pool is empty, i.e. concurrency under that source ctx exceeds the
cached count). `childCtx.Done()` is the source ctx's done channel: cancel the submit
ctx and the body stops — the entire cancellation model, for free.

### Why the meta carries the wave as a stamp, not as identity

A pooled meta is reused across waves and across borrows. The wave (and `job`/`parentJobs`)
are stamped at borrow and cleared on return, exactly as the transient fields are today
(`execShell.borrow`/`giveBack`, `execshell.go:85`/`106`). Each concurrent borrow takes a
**distinct** meta out of the pool, so there is no concurrent mutation of a shared meta.

## Borrow sites and return disciplines

Every site that runs a **user body** needs a wave-stamped ctx. There are exactly four,
collapsing to three disciplines:

| # | Site | User body | Discipline |
|---|------|-----------|-----------|
| 1 | `taskWork.Execute` (`job.go:124`, via `runInShell`) | task body | **A** |
| 2 | `funnelWork.Execute` (`funnel.go:776`→`809`, via `runInShell`) | Accumulate body | **A** |
| 3 | `skimWork.Execute`→`handler.Handle` (`skimop.go:243`) | skim handler | **B** |
| 4 | `funnelInstance.flush`/`forceFlush` (`funnel.go:452`,`473`) + `flushAll` (`cpworker.go:172`) | FlushFn body | **C** |

- **A — per-execution borrow** (task #1, accumulate #2): the pooled-per-body case.
  - *Async* (handed to a global-pool worker): the **work item owns the borrow** and
    returns it in `Free` — right alongside the limiter `request` it already releases
    there (`taskWork.Free`, `job.go:136`). No need to capture or thread the submit ctx
    to the worker: the work item carries the already-stamped `childCtx`.
  - *Inline* (run synchronously via `ctxMeta.ExecuteNowOrQueue`/`TryExecuteNow`,
    `ctxmeta.go:139`/`185`): a scope-bounded borrow, `defer`-returned when the inline
    execution finishes (this is what `runInShell`'s `defer giveBack` does today).
- **B — per-drive borrow** (skim #3): the `Skim`/`SkimAll` driver borrows one
  wave-stamped *skim* ctx for the drive; serial handlers run inline under it. Returned
  when the drive returns. (Today the handler runs under the driver's `skimCtxMeta` ctx.)
- **C — per-lifetime borrow** (flusher #4): the funnelEngine flusher is a singleton
  goroutine; it mints one *funnel*-stamped ctx at startup and holds it until
  `wavestate→Done`. Pooling buys nothing for a singleton — mint-and-hold. (Today:
  `WithCancel(j.ctx)` + `ensureCtxMeta`, `funnelengine.go:121`.)

**Not borrow sites** (framework plumbing, run no user code): `taskPostWork`,
`skimPostWork`, `funnelPostWork` (queue handoff) and `limiterScatterWork`,
`launcherScatterWork` (admission gates).

## Lifetime: plain ownership + GC, no refcount

The borrower (work item, or inline scope) holds its `{meta, childCtx}` **outright**
between borrow and return. Returning is a free-list push. If the source ctx was already
cancelled and `ctxmap` has evicted the pool from its map, the push lands on a pool
object that is otherwise unreferenced and about to be GC'd — harmless, and skippable as a
micro-opt that does not matter. The pool object, its free-list, and any never-returned
metas all GC together once nothing references them. **No refcounting.**

## What this collapses

- `Wave.waveCtx` / `waveCancel` / `ctx` / `cancelFn` — **gone.** A `Wave` owns no ctx.
- `Wave.Cancel` / `Wave.CancelAndWait` — **gone.** Drain-only lifecycle
  (`Skim`/`SkimAll`/`CloseAndSkimAll` → `ErrWaveDone`); cancellation is the drive ctx.
- `execShellPool` — **dissolves** into the source-ctx-keyed pool; loses `waveCtx`, the
  per-shell `cancel`, and `release()`.
- `Wave.ctxMetaMap` / `skimCtxMetaMap` — **leave the Wave** and become a process-wide
  (wave-agnostic) ctx→pool map, since the meta carries the wave as a per-borrow stamp.
- `ensureCtxMeta`'s cross-job `AfterFunc(j.ctx, cancel)` wiring (`ctxmeta.go:375`) —
  **vanishes**; cancellation is now pure source-ctx ancestry.
- The funnel flusher's base ctx → `defaultPool.PoolCtx()`, exit purely on
  `wavestate→Done` (no `CancelAndWait` join; see WORKING_NOTES).

Net: **less** context machinery, not more — B is a simplification.

## Open implementation questions

1. **ctxmap value-as-pool is not a drop-in `T` swap.** `ctxmap.Map.WithValue` stamps
   `WithValue(computedCtx, key, value)` and dual-caches under both the source and the
   stamped ctx, assuming the value *is* what rides the ctx. A pool needs a purpose-built
   variant: the map yields the pool; the pool mints per-meta child ctxs.
2. **Per-borrow stamping vs. per-mint derivation.** The `parentJobs` accumulation and
   parent-linking that `ensureCtxMeta` does at mint move into the stamp. The
   source-ctx *ancestry basis* (`srcJob` = the source ctx's own wave, `srcParentJobs` =
   its accumulated parentJobs) is fixed per pool and cached at Init; but the
   **effective `parentJobs` depends on the target wave** (`parentJobsFor`): same-wave
   passes the basis through, cross-wave joins `srcJob` into it (mirroring
   `ensureCtxMeta`). `job`/`wave`/`parent`/`ctxType`/`heldRequest`/exEnv are per-borrow.
   The cross-wave branch allocates a fresh map per call — cache it on the pool if one
   source ctx repeatedly targets the same other wave (deferred; steady state is
   same-wave, which is alloc-free).
3. **Skim-driver (B) and flusher (C) sources.** Confirm the skim driver borrows per
   drive from the user `Skim` ctx's pool, and the flusher mints from `defaultPool`'s ctx.
4. **Behavior change (user-visible).** `ExampleWave_Cancel` (`example_cancel_test.go`)
   demonstrates `Cancel()` force-aborting in-flight bodies — impossible under the new
   model. Rewrite it to cancel the drive/submit ctx, and document the semantics.

## Relationship to C1 / C2

This is thread B and is largely decoupled, but the flusher re-home (#4 / discipline C)
touches the dispatch/flusher code that **C1** (dispatch/execution split) consolidates,
and the pooled-meta lifetime echoes **C2**'s permit-core "pools outlive their units"
ownership. Land B as a simplification over the current substrate; let C1/C2 inherit the
simpler context model rather than re-deriving it.
