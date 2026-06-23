# Plan — zero-value Wave implementation

> **LANDED 2026-06-23.** Implemented as planned, with two refinements found in
> verification: (1) re-arm had to be split out of `ensureInit` into a dispatch-only
> `ensureArmed` — a `CloseAndSkimAll` drives an empty wave to Done during `Close`, so
> a unified init re-armed the wave during its *own* skim and blocked forever; skim now
> observes Done, only dispatch re-arms. (2) Two `meta.wave == nil` gaps that `NewWave`
> used to cover had to be re-stamped explicitly: on the minted top-level meta in
> `topLevelCtxMeta` (else funnel submit borrows a `wave=nil` body → "child job" panic),
> and on the flusher's ctx meta (else a `FlushFn`'s ambient `op.Submit` panics, unlike
> an accumulate body's). Verified: full suite + `-race` suite + `reuse_test.go` + 40×
> `-race` `TestBySimulation`.

> **Path-to-target doc.** Implements the 2026-06-21b locked Wave lifecycle
> (`WORKING_NOTES.md` top banner; surface in `docs/decisions/surface-lineage.md`).
> Decisions (2026-06-23, w/ PN): **reuse-after-drain is in scope from the start**, and
> the removal of `NewWave`/`Cancel`/`CancelAndWait` + call-site migration land
> **together** (big-bang, one green landing) — not behind transitional shims.

## Target

`var w streampool.Wave` is usable with no constructor. The Wave owns no context.
Lifecycle is the drain (`Skim`/`SkimAll`/`CloseAndSkimAll` → `ErrWaveDone`). Bind with
`op.In(&w)`. A drained Wave re-arms on next use (reusable). No `NewWave`/`NewChild`/
`Cancel`/`CancelAndWait`.

## What we already verified (de-risks the change)

1. **Top-level dispatch from a bare ctx already works.** `vetStart`/`vetSkim` →
   `topLevelCtxMeta(ctx)` mints the meta via `ensureCtxMeta` when the ctx carries none,
   building the `topLevelExEnv` over `&w.workQueue`. The wave comes from the op binding
   (`resolveWave`) or the skim receiver — never from a wave-carrying ctx. So
   `op.In(&w).Submit(bareCtx, v)` and `w.SkimAll(bareCtx)` already function;
   `NewWave`'s returned ctx is used only by the no-`In` `op.Submit(ctx)` path.
2. **The flusher's meta is already `parent=nil`** (a fresh permit-root) and its
   done-watcher already selects `state.Done()`. So re-homing it off `j.ctx` changes
   only the *cancellation linkage*, not parentage.

## Mechanisms

### M1 — Race-safe lazy `ensureInit`
A zero-value Wave's substrate (`state`, `skimQueue`, `governor`, `workQueue`, closure
fields) is uninitialized. `ensureInit()` brings it up.

- **Zero-value stage is a lie.** `stageOpen = iota = 0`, so a zero `WaveState` reads
  "Open" with **nil** `nextFlushChan`/`doneChan`. Init cannot key off stage; use an
  explicit `initialized atomic.Bool` marker. The only state read on the cold zero value
  is `currentStage.Load()` (an atomic on a zeroed field — safe, reads Open).
- **Guard:**
  ```
  func (w *Wave) ensureInit() {
      if w.initialized.Load() && !w.state.IsDone() { return }   // fast: live, not done
      w.initMu.Lock(); defer w.initMu.Unlock()
      if w.initialized.Load() && !w.state.IsDone() { return }   // double-check
      if w.initialized.Load() {        // reuse: prior cycle reached Done
          if fe := w.fEngine.Swap(nil); fe != nil {
              <-fe.flusherDone          // join prior flusher BEFORE re-Init (barrier)
          }
          w.ctxMetaMap.Clear()         // drop the prior cycle's per-ctx caches
          w.skimCtxMetaMap.Clear()     // (these are what CancelAndWait used to clear)
      }
      w.state.Init(); w.skimQueue.Init(); w.governor.Init(); w.workQueue.Init(nil)
      w.protoBB.ShouldBlock = w.shouldBlock; w.blockFn = w.block
      w.tryAddWorkFn = w.tryAddWork; w.addWorkFn = w.addWork
      w.initialized.Store(true)
  }
  ```
  (`WaveState.IsDone()` = `currentStage.Load() == stageDone`, to add.)
- **Hot path:** the common case is one atomic-bool load + one atomic-int load.
- **Hook points** (the only entries that touch the substrate): `vetStart` (all
  dispatch), `vetSkim` (all skim/drain), `Close`, and `funnelEngine()` (NewFunnel
  before any dispatch). Each calls `ensureInit()` first.

### M2 — Flusher re-home (drop `j.ctx`)
Remove `Wave.ctx`/`cancelFn`. In `flusher()`: `ctx, cancel :=
context.WithCancel(context.Background())` (was `j.ctx`). Exit stays governed by
`state.Done()` (done-watcher unchanged). Replace the one other `j.ctx` use
(`funnel.go` factory-close error submit) with `context.Background()`. Net: the Wave
holds no ctx; cancellation rides the caller's ctx by ancestry; no force-abort.

### M5 — Dispatch unification (top-level submit for all ops; cross-wave redirect)
Dispatch is currently non-uniform: **Launcher** routes through `vetStart` →
`topLevelCtxMeta(ctx)`, which **mints** a top-level meta when the ctx carries none —
so `launcher.In(&w).Submit(bareCtx)` works. **Skimmer/Funnel** `SubmitResult` /
`TrySubmitResult` use `target.ctxMeta(ctx)`, which **panics if the ctx carries no
meta** — so top-level `skimmer.In(&w).Submit(bareCtx)` / `funnel…Submit(bareCtx)`
would panic. Top-level direct submit to skimmers/funnels is a real, tested pattern
(it works today only because the `NewWave` ctx carries the meta), so the zero-value
model needs all three ops to mint a top-level meta from a bare ctx.

Fix: route Skimmer/Funnel submit through `topLevelCtxMeta`'s mint-or-reuse (same as
Launcher), with a permissive ctx-type check (preserving today's no-restriction
behavior). `topLevelCtxMeta` reuses a same-wave meta already on the ctx (in-body
ambient submit — unchanged) and mints a fresh top-level meta otherwise (top-level
submit — newly works).

**Cross-wave = redirect (decided 2026-06-23, w/ PN).** Where `ctxMeta` panicked when
the ctx's meta belonged to a *different* wave, `topLevelCtxMeta`→`ensureCtxMeta`
instead mints a fresh meta recording the source wave as parent — turning a cross-wave
submit into a working **redirect** into the bound wave, matching the locked
`op.In(wave)` "place/redirect" intent. This is a behavior change: cases that
previously panicked now succeed. (Verify no test depends on the old panic.)

Bonus: hooking `ensureInit` at the top of `topLevelCtxMeta` then covers **all**
dispatch and skim in one chokepoint (Launcher via `vetStart`, Skimmer/Funnel via the
unified path, skim via `skimCtxMeta`) — only `Close` and `funnelEngine()` need a
separate `ensureInit` call.

### M3 — Surface removal
Delete `NewWave`, `Cancel`, `CancelAndWait` (and `newWaveSubstrate`, folded into
`ensureInit`). `Close` stays (it's the seal, distinct from a ctx Cancel).

### M4 — Reuse-after-drain
**Why it's in scope now, not a convenience:** it's the enabler for **pooling `*Wave`
structs** (`sync.Pool[*Wave]`, user/benchmark-side per the locked design) so sub-waves
are allocation-free — which the benchmarks need. A pooled `*Wave` re-borrowed from the
pool is a drained Wave being reused, so the re-arm path runs every pool cycle. Reuse
must therefore be cheap when no funnel ran (no flusher to join) and must fully reset
per-cycle state (the `state`, the queues, **and** the `ctxMetaMap`/`skimCtxMetaMap`
caches) so a reborrowed Wave carries nothing from its prior life.

Built into M1's `ensureInit`. The re-arm correctness argument:
- A wave is in "reuse" iff `initialized && state.IsDone()`. `IsDone` is only true after
  `noMoreReferences` closed `doneChan` — i.e. all work + funnel-instance refs drained,
  so the flusher's done-watcher has fired and the flusher goroutine is exiting.
- `<-fe.flusherDone` is therefore a **bounded** join and a **barrier**: the prior
  flusher has fully returned (its `defer close(flusherDone)` runs after `flusher()`
  returns) before we re-`Init` `state` — so no goroutine reads the old `WaveState`
  while we overwrite it. The new cycle gets a fresh engine on the next `NewFunnel`.
- Reuse is **sequential** ("after the drain returns"). Dispatch racing a concurrent
  self-drain stays a user error guarded by `panicIfDone` (unchanged).

## Call-site migration (big-bang, with the internals)
- `ctx, w := streampool.NewWave(ctx)` → `var w streampool.Wave` (keep using the
  original `ctx`).
- top-level `op.Submit(ctx, …)` / `op.Start(ctx, …)` → `op.In(&w).Submit(ctx, …)`.
- `defer w.CancelAndWait()` → rely on the drain (`CloseAndSkimAll`); to abort, cancel
  the ctx you drive with. **Audit:** any test relying on `CancelAndWait` to abort
  *undrained* work (vs. clean up post-drain) must convert to ctx-cancel or a full
  drain. ~17 test files + `internal/sim/run.go` + `example_*_test.go`. (Absorbs B3.D.)

## Build order (toward one green landing)
1. `WaveState.IsDone()`; `ensureInit` + `initialized`/`initMu` fields; fold
   `newWaveSubstrate` into `ensureInit`. Also drop the now-vestigial `wg`,
   `ctxMetaMap`, `skimCtxMetaMap` fields (never written/read; only `CancelAndWait`
   `Clear`ed them) + the unused `skimCtxMetaValueKey` type + the `ctxmap` import — so
   the reuse path has no caches to clear, only the flusher join + substrate re-Init.
2. M5: unify dispatch — route Skimmer/Funnel submit through `topLevelCtxMeta`; hook
   `ensureInit` at the top of `topLevelCtxMeta` (covers all dispatch + skim) + in
   `Close` and `funnelEngine()`.
3. M2: re-home flusher off `j.ctx`; remove `ctx`/`cancelFn` fields; `funnel.go` →
   `Background`.
4. M3: delete `NewWave`/`Cancel`/`CancelAndWait`.
5. Migrate all call sites (tests, sim, examples).
6. Verify.

## Verification
- Full `./...` suite + linter.
- **`-race` `TestBySimulation` hard** (loop): M2 touches the flusher the B3 hang lived
  in, and M4 is brand-new cross-cycle concurrency. Add a focused reuse test: drive one
  `var w` through several `CloseAndSkimAll` cycles (with funnels, so the flusher
  re-spawns) under `-race`.
- Confirm a drained-then-reused wave re-arms (stage back to Open, fresh flusher,
  cleared caches) and a sub-wave (fresh zero value per body) still works.
- **Pooled-reuse test** (the motivating case): a `sync.Pool[*Wave]` driving many
  Get → dispatch+funnel → `CloseAndSkimAll` → Put cycles concurrently under `-race`,
  asserting no cross-cycle state bleed and zero per-cycle Wave allocs (alloc benchmark).

## Risks
- **`ensureInit` hot-path cost** — keep it to the two atomic loads above; no lock on the
  live path.
- **`CancelAndWait` abort dependence** in tests (see migration audit) — the most likely
  source of migration friction.
- **Re-arm vs straggler** — mitigated by the `flusherDone` join barrier; the key
  invariant is that `IsDone` ⇒ flusher already exiting, so the join cannot deadlock.
