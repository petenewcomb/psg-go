# Meta-Context Migration (ctxpool adoption — the meta-derivation half)

> **DESIGN — for review (PN), 2026-06-22.** Surfaced by the B3 scope discovery:
> migrating task/funnel bodies to `borrowBodyContext` (ctxpool) breaks the
> dispatch/skim-from-body paths, because the meta-derivation machinery is not
> ctxpool-aware. This note designs that machinery's migration. Companion to
> `body-context-pool.md` (the borrow primitive) and the WORKING_NOTES Wave-lifecycle
> banner. Anchors thread B3; no code until ratified.

## The discovery

The borrow-site migration (task/funnel bodies borrow a ctxpool body ctx at dispatch,
run under it) works in isolation. It panics (`Context belongs to a child job`) the
moment **a body dispatches or skims**, because that path runs through
`ensureCtxMeta` / `topLevelCtxMeta` / `skimCtxMeta` (via `ctxMetaMap`), which:

1. **finds the source meta with `ctx.Value(ctxMetaValueKey{})`** — but a ctxpool body
   ctx carries its meta under ctxpool's `childKey`, so the lookup walks *past* it to
   an ancestor's meta; and
2. **caches derived metas keyed by ctx identity** — which ctxpool's **ctx reuse**
   would alias (same reused ctx object, different meta across borrows).

## Current model (as built)

Four `ctxType`s of meta: `topLevel`, `task`, `funnel`, `skim`. The **unifying
invariant** the current code maintains: *every* meta is findable via one lookup,
`ctx.Value(ctxMetaValueKey{})` — `ctxMetaMap` stamps top-level/skim metas under it;
the (old) `execShell` stamped task/funnel metas under it too. So a dispatch from any
context resolves its meta with one mechanism.

A dispatch carries **two** metas, easy to conflate:
- **The dispatcher meta** (`meta` from `vetStart` → `topLevelCtxMeta`): used to
  *enqueue* the new work — `meta.Lock()`, `meta.Group()`, `meta.ExecuteNowOrQueue`.
  For a top-level dispatch it is the top-level meta; for a dispatch from inside a
  body it is that **body's** meta.
- **The work's body meta** (borrowed in `newTaskWork`/`newFunnelWork`): stamped onto
  the ctx the body later *runs* under.

The break is in resolving the **dispatcher** meta when the dispatcher is a body.

## The lifecycle fork (why one mechanism doesn't fit all)

| Meta kind | Lifecycle | Keyed by | Mechanism |
|---|---|---|---|
| **topLevel / skim** (driver) | **stable singleton** — one per source ctx, reused for every dispatch from it, lives until the ctx is done | the **parent/user ctx** | `ctxMetaMap` (parent-ctx-keyed cache) |
| **task / funnel** (body) | **pooled, per-execution** — many per source ctx, transient | the borrowed **child ctx** | `ctxpool` (child-ctx-keyed pool) |

Two hard constraints make these genuinely different, not unifiable by fiat:

- **Driver metas must be stable singletons.** `topLevelExEnv` carries a `sync.Mutex`
  and the shared dispatch `workQueue`. Concurrent top-level dispatches from the same
  ctx **serialize on that one lock** and enqueue onto that one queue. Pooling a fresh
  meta per dispatch (ctxpool's model) would hand each dispatch its own lock/queue —
  a correctness bug. So driver metas cannot be ctxpool-pooled.
- **ctxpool is child-keyed, not parent-keyed.** `ctxpool.GetValue` resolves the meta
  on a *child* ctx (the borrowed one). A top-level dispatch arrives with the *user*
  (parent) ctx and must find/create the one stable driver meta for it — a
  parent-ctx-keyed lookup ctxpool does not provide. That is exactly `ctxMetaMap`.

So: **driver metas stay parent-ctx-keyed singletons; body metas are ctxpool-pooled.**
They are two mechanisms by nature.

## Proposed design

**1. Split the dispatcher-meta lookup by whether the source ctx already carries a
meta.** Rework `ensureCtxMeta` (and `ctxMeta`) so the source meta is found via
`metaFromContext` (ctxpool-aware), not `ctxMetaMap`'s internal `ctx.Value`:

- **Source has a meta** (a ctxpool body meta, or an existing driver meta on a
  re-entrant call): use it directly — same-wave returns it as-is; cross-wave derives
  a fresh meta accumulating `parentJobs`. **Do not cache** this in `ctxMetaMap` (the
  body ctx is reused — caching by it is the aliasing bug). No cache is needed: the
  body meta is already in hand, and the cross-wave derived meta is exactly what
  `borrowBodyContext` + `parentJobsForSource` already produce for the work's body
  meta — i.e. **`ensureCtxMeta`'s cross-job derivation is subsumed by the borrow.**
- **Source has no meta** (a fresh top-level dispatch from a user ctx): create the
  stable top-level meta (with `topLevelExEnv`) and cache it in `ctxMetaMap` keyed by
  the **user ctx** (stable — never a reused body ctx). Unchanged from today.

This confines `ctxMetaMap` to what it is good at — stable, parent-ctx-keyed driver
metas — and routes body-sourced dispatches through the ctxpool meta without caching.

**2. `skim` metas** stay on `ctxMetaMap` + `skimCtxMetaMap` (the second map marks "this
ctx is a skim ctx for wave j", for the re-entrancy guard). A skim is driven from a
user/driver ctx (stable), so the singleton model fits. A body that drives a *sub-wave*
skim passes its body ctx as the drive ctx; `skimCtxMeta` derives the skim meta from it
— same "source has a meta, derive without caching" path as (1).

**3. Body metas** via `ctxpool` (the committed `borrowBodyContext`), unchanged.

## The open decision: does `ctxMetaValueKey` actually retire?

Full retirement of `ctxMetaValueKey` (the stated B3 goal) requires **driver** metas to
leave it too. But driver metas are parent-ctx-keyed singletons that ctxpool
(child-keyed, pooled) does not host. Options:

- **(A) Keep `ctxMetaValueKey` as the driver-meta key (recommend; rename for clarity,
  e.g. `driverMetaKey`).** `ctxMetaMap` keeps stamping driver metas under it;
  `metaFromContext` stays **dual** (ctxpool child first, then driver key). Honest and
  small: it reflects that there genuinely are two meta lifecycles. "ctxMetaValueKey
  goes away" becomes "the *body* path leaves it; the driver path keeps a (renamed)
  key." Body ctxs are pure ctxpool; only driver ctxs use the key.
- **(B) Build a parent-ctx-keyed singleton variant in/around ctxpool** so driver metas
  also resolve through ctxpool and the key fully retires. More machinery (a
  singleton-per-parent mode distinct from the pool-per-parent mode) for a mostly
  cosmetic unification; the two lifecycles still exist underneath.

Recommendation: **(A)** — it matches the real structure (driver vs body), keeps the
change surgical, and avoids contorting ctxpool into a singleton store it is not.

## Sequencing (once ratified)

1. Rework `ensureCtxMeta`/`ctxMeta` per design point (1): source-meta via
   `metaFromContext`; no caching on the body-source path; `ctxMetaMap` only for the
   fresh-top-level case. Land green **before** re-applying the borrow migration.
2. Re-apply the stashed task/funnel borrow migration + execShell removal + Cancel gut.
3. Reconcile `ExampleWave_Cancel(_task)` + the sim to submit-ctx-rooted cancellation.
4. (Decision A) rename `ctxMetaValueKey` → `driverMetaKey`; confirm `metaFromContext`
   dual lookup is the single read seam.

## Status of the WIP

The task/funnel borrow migration + `execShell`/`waveCtx` removal + `Cancel` gut is
**stashed** (`git stash`, message "WIP B3 task+funnel borrow-at-dispatch …"), reset to
the green checkpoint `1a8d404`. It is reusable for step 2 above once the meta machinery
(step 1) lands.
