# Meta-Context Migration (ctxpool adoption — the meta-derivation half)

> **LANDED 2026-06-23.** The single-lookup target below is implemented:
> `metaFromContext` is now `ctxpool.GetValue[*ctxMeta](ctx)` and nothing else;
> `ctxMetaValueKey`, `ctxMetaMap`, `skimCtxMetaMap`, and their backing package
> `internal/ctxmap` are deleted. `ensureCtxMeta`/`topLevelCtxMeta`/`skimCtxMeta` remain
> as the derivation functions (they stamp ctxpool children), per the unified model.

> **DESIGN — for review (PN), 2026-06-22 (rev 2).** Surfaced by the B3 scope
> discovery: migrating task/funnel bodies to `borrowBodyContext` (ctxpool) breaks the
> dispatch/skim-from-body paths because the meta-derivation machinery is not
> ctxpool-aware. **Rev 2 retracts the "lifecycle fork" of rev 1** (driver metas are NOT
> stable singletons — see below); the model is now a single unified per-call/per-execution
> borrow. Companion to `body-context-pool.md`; anchors thread B3. No code until ratified.

## The discovery

The borrow-site migration (task/funnel bodies borrow a ctxpool body ctx at dispatch,
run under it) works in isolation. It panics (`Context belongs to a child job`) the
moment **a body dispatches or skims**, because that path runs through
`ensureCtxMeta`/`topLevelCtxMeta`/`skimCtxMeta` (via `ctxMetaMap`), which (1) finds the
source meta with `ctx.Value(ctxMetaValueKey{})` — but a ctxpool body ctx carries its
meta under ctxpool's `childKey`, so the lookup walks *past* it to an ancestor's meta;
and (2) caches derived metas keyed by ctx identity — which ctxpool's **ctx reuse**
aliases.

## Retraction of rev 1's "lifecycle fork"

Rev 1 claimed driver metas (top-level/skim) are *stable singletons per source ctx* and
so don't fit ctxpool. **That was wrong**, on two counts (PN):

- **Call-scoped, not wave-scoped.** A driver meta is needed only for the duration of one
  `Skim`/`SkimAll` (or top-level `Start`/`Submit`) *call*, held across the handler
  bodies that call runs, then released. That is a plain borrow/hold/return.
- **Pooled, not 1:1.** Driving is supported from multiple goroutines concurrently (each
  its own ctx, or even the same ctx), so there are N live driver metas at once — one per
  in-flight call. That is ctxpool's pool-per-source, same as body metas. "One stable
  meta per ctx" was an artifact of `ctxMetaMap` caching, which per-call borrowing
  replaces.

A bonus falls out: each driver meta is then **single-threaded** (one call, one
goroutine), which is what makes the `topLevelExEnv.Lock` removable (below).

## The unified model

**One mechanism.** `metaFromContext(ctx)` = `ctxpool.GetValue[*ctxMeta](ctx)` — a single
lookup. `ctxMetaValueKey`, `ctxMetaMap`, and `skimCtxMetaMap` all retire. Every meta is a
ctxpool borrow; they differ only in **hold scope** and **ctxType stamp**:

| Meta | ctxType | Borrowed at | Held until | Discipline |
|---|---|---|---|---|
| body (task/funnel) | task/funnel | dispatch (`newTaskWork`/`newFunnelWork`) | work `Free` | per-execution |
| skim driver | skim | `Skim`/`SkimAll` entry | call return | per-call |
| top-level driver | topLevel | `Start`/`Submit` entry | call return | per-call |

**Lookup rule: borrow at entry, read while nested.** A raw user ctx appears only at the
*entry* of a top-level `Start`/`Submit` or a `Skim` call — where `ctxpool.WithValue`
borrows (its parent-keyed pool selection handles the raw ctx). Everywhere nested, the
ctx is already a borrowed child and `GetValue` reads it. There is no parent-keyed
"singleton" lookup; rev 1's point (b) was muddled and is withdrawn.

**Cross-wave derivation is subsumed by the borrow.** `ensureCtxMeta`'s cross-job
`parentJobs` accumulation is exactly what `borrowBodyContext` + `parentJobsForSource`
already compute. So the derive-and-cache machinery doesn't move to ctxpool — it
*dissolves*; the borrow does it.

## Borrow/return points per entry (the detail that bit us)

**A top-level `Start`/`Submit` is not one borrow — it drives a backpressure skim.** The
top-level dispatch (`ctxMeta.ExecuteNowOrQueue`, `TryExecuteNow`) calls `wave.yield`,
which `trySkim`s completed work and **runs skim handler bodies** before enqueuing the
submitted work. So one top-level call touches THREE meta concerns:

1. **the top-level driver meta** — drives the call; `ShouldBlock()=true` (backpressure
   may block on a permit) and enqueues the submitted work via its `topLevelExEnv`.
2. **a skim meta for the backpressure `trySkim`** — the handlers it runs must be in
   **skim** context: `ShouldBlock()=false` (a skim handler must not block-dispatch) and
   the nested-skim guard must fire. (Today `yield`→`skimCtxMeta` derives this.)
3. **the submitted work's body meta** — borrowed at `newTaskWork`, run later on a
   worker, released at `Free`. Distinct ctxType (task), distinct time. **Not** unifiable
   with 1 or 2.

**DECIDED (PN, 2026-06-22): (i) two nested borrows, at least for now.** Concerns 1 and
2 differ in `ShouldBlock` and ctxType but run in *sequence* on the same goroutine within
the call (skim phase, then enqueue phase), never concurrently. The top-level borrow
(ctxType=topLevel) drives the call; for the `yield` phase it makes a short **nested skim
borrow** (child, ctxType=skim) under which the backpressure handlers run, returned when
`yield` returns; then it enqueues. This mirrors today's top-level⊃skim parent/child,
keeps `ShouldBlock`/nesting honest, and the extra borrow is a pool hit. (Rejected (ii)
one-borrow-phased-ctxType: a meta whose ctxType mutates mid-call is fragile.)
`TryStart`/`TrySubmit` are the same minus the blocking enqueue. A plain `Skim`/`SkimAll`
is just concern 2 standalone (borrow skim meta, hold across handlers, return).

**Funnel `Submit`** mirrors the launcher: a dispatcher meta (the body's, when submitted
from inside a body; or a top-level borrow at top level) enqueues; the funnel work's body
meta is borrowed at `newFunnelWork`.

**Nested (dispatch/skim from inside a body):** the body ctx is already a borrowed,
self-describing child — `GetValue` resolves its meta directly; no entry borrow.

## `topLevelExEnv.Lock` — what it guards, and why it's removable

`ctxMeta.Lock()` (only when `IsTopLevel`) takes `topLevelExEnv.mu` around the whole
dispatch (`launcher.dispatch`: `Lock` → `Group` → `ExecuteNowOrQueue` → `Unlock`). It
guards the meta's **own** mutable `exEnv` state — `groupStack`, `queueFnStack` — against
concurrent dispatches that **share** that meta (today: two goroutines on the same
top-level ctx → same cached meta). The shared dispatch `workQueue` it points at is the
wave's and already thread-safe; admission is governed by the limiter/governor, not this
lock. Under per-call borrows each driver meta is single-threaded, so nothing shares the
stacks → **the Lock has nothing to guard and can be dropped.** *Verify* there is no
second consumer of `topLevelExEnv.mu` and that concurrent backpressure `yield`s on the
shared queue need no serialization beyond what `workq` provides.

## Sequencing (once ratified)

1. **Meta machinery → ctxpool, single lookup.** Replace `ensureCtxMeta`/`ctxMeta`/
   `topLevelCtxMeta`/`skimCtxMeta` + `ctxMetaMap`/`skimCtxMetaMap` with ctxpool borrows
   at the entry points (top-level dispatch, skim drive) per the table; `metaFromContext`
   becomes pure `ctxpool.GetValue`; retire `ctxMetaValueKey`. Land green standalone.
2. **Re-apply** the stashed task/funnel body borrow migration + `execShell`/`waveCtx`
   removal + `Cancel` gut (it composes once 1 lands).
3. **Reconcile** `ExampleWave_Cancel(_task)` + the sim to submit-ctx-rooted cancellation.
4. Confirm `topLevelExEnv.Lock` removal; drop dead derivation code.

## Implementation strategy (incremental, green at each step)

Key insight that lets this land green *before* the body migration: at the current
checkpoint bodies use `execShell`, which **reuses one meta pointer** (re-stamped in
place), so `ctxMetaMap`'s ctx-keyed cache is harmless. ctxpool hands a **fresh meta
pointer per borrow**, so the same cache would alias — but only *after* the body
migration. So the machinery can be made ctxpool-ready incrementally now.

The unified entry logic (replaces the `ensureCtxMeta`/`topLevelCtxMeta`/`skimCtxMeta`
+ `ctxMetaMap` derivation) at a dispatch/skim entry with source `ctx`, target wave `W`,
desired driver `ctxType`:

```
if m, ok := metaFromContext(ctx); ok {
    if m.job == W { return ctx, m }              // 1a reuse (body self-dispatch / re-entrant driver)
    // 1b cross-wave: driver meta for W, parentJobs += m.job, EPHEMERAL (no cache)
    return borrowDriver(ctx, W, ctxType, parentJobsForSource(ctx, W))
}
// 2 no source meta: fresh top-level driver for W from a STABLE user ctx
return borrowDriver(ctx, W, ctxType, nil)   // (or ctxMetaMap until per-call borrows land)
```

`parentJobsForSource` (already in `bodyctx.go`) covers 1b's ancestry and 2's nil. The
cross-job derivation that `ensureCtxMeta` did is exactly this — it **dissolves** here.

Increments:
1. **`ctxMeta` → `metaFromContext`** — DONE (`bc8040e`). Pure read+validate; drops one
   `ctxMetaMap` use. Green standalone.

**FINDING (2026-06-22, attempting increment 2): the meta-machinery rewrite and the body
migration are ONE coupled cutover, not separable.** Two reasons:

- **Stamping ambiguity.** A body-sourced derivation whose result nested code looks up
  via the ctx — specifically a **skim meta** (handlers run under the skim ctx) when
  skimming a sub-wave *from a body* — must be stamped onto a ctx that
  `metaFromContext` resolves to it. Post-migration the source is a ctxpool body ctx.
  Stamping the derived meta via `context.WithValue(ctx, ctxMetaValueKey, m)` is
  **shadowed**: `metaFromContext` checks ctxpool first and `GetValue` returns the
  *body's* meta on the ancestor child. Flipping the lookup order breaks plain body
  ctxs. The only stamp that wins is a **ctxpool borrow** of the derived meta — i.e. the
  per-call driver borrow. (A cross-wave *dispatch* meta is used transiently for the
  enqueue and is NOT looked up via ctx by nested code, so it needs no stamp — only
  ctx-resolved metas, skim especially, force the borrow.)
- **Can't build/test before bodies migrate.** At the current checkpoint bodies are
  `execShell` ctxs (under `ctxMetaValueKey`, reusing one meta pointer), so there are no
  ctxpool body ctxs to exercise the new path; a separate "increment 2" would be dead
  code. The ctxpool-body-source handling must land *with* the body migration, gated by
  the sim.

**Revised plan — increments 2+3+4 collapse into one cutover** (do with fresh context;
the sim/`-race` is the gate): re-apply the stashed body migration AND make the meta
machinery resolve+stamp driver metas through ctxpool per-call borrows (entry logic
above), retiring `ctxMetaValueKey`/`ctxMetaMap`/`skimCtxMetaMap` together. Then B3.D
(examples/sim reconcile). The `bc8040e` `ctxMeta` change stands either way.

## WIP status

Task/funnel borrow migration + `execShell`/`waveCtx` removal + `Cancel` gut is **stashed**
(`git stash@{0}`), tree reset to the green checkpoint `1a8d404`. Reusable for step 2.
