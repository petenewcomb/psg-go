# Plan — wave-driven funnel finalization (retire Funnel Close/Dup)

> **LANDED 2026-06-23.** Implemented as designed: `Funnel[T]` is a plain value
> (`inner *funnel[T]`, no leakguard); `Close`/`Dup`/`refCount`/`ref`/`unref` are gone;
> the funnel holds one per-wave reference (`IncrementReference` at `NewFunnel`,
> `DecrementReference` in `finalize`); `funnelEngine` keeps a `funnelFinalizer`
> registry and `cpWorker.flushAll` finalizes every funnel after the instance sweep
> (`factory.Close` → recycle → drop the wave ref). `internal/leakguard` is deleted
> (funnel was its only user). Verified: full suite + `-race` suite + otpsg +
> `reuse_test.go` + factory-close test + 40× `-race` `TestBySimulation`.

> **Path-to-target doc.** Implements the locked "Funnel = wave-scoped; NO Close/Dup;
> finalization is wave-driven (in-flight==0 ∧ sealed)" surface (see
> `surface-lineage.md`, `API_DESIGN.md`). Concurrency-critical (funnel instance
> lifecycle + the per-wave flusher).

## Target

A `Funnel[T]` is a plain value bound to its wave at construction. It has **no
`Close` and no `Dup`**. Its factory-level state (`AccumulatorFactory.Close()`) and its
pooled storage are finalized by the wave when the wave drains — subsuming the old
handle refcount, which "counts all feeders" via the wave's in-flight tracking.

## Current model (what we're replacing)

`Funnel[T]` wraps a `leakguard.Handle[funnel[T], funnelHandleTrait[T]]`. `funnel.refCount`
counts three things: the handle baseline (`NewFunnel` sets 1; `Dup` +1, `Close` −1), each
live instance (allocate `ref()`, flush `dropInstanceLiveness()`→`unref()`), and each
in-flight `funnelWork` (`Init` `ref()`, `Free` `unref()`). When `refCount` hits 0, `unref`
drains the instance reuse cache, calls `funnelFactory.Close()` (errors → errSink →
SkimAll), and recycles the `funnel[T]`. Separately, each live instance holds a per-wave
barrier ref (`state.IncrementReference`/`DecrementReference`) that keeps the wave out of
Done until every accumulator has flushed — **this stays**.

## Why the refcount is redundant

The end-of-work flush sweep (`cpWorker.flushAll`, driven by the FlushChan when
`inFlightWork→0`) runs at the **Flushing** stage:
- `inFlightWork==0` ⇒ no `funnelWork` in flight ⇒ no work refs.
- the sweep force-flushes every still-pending instance ⇒ no instance refs after.

So at the sweep, a funnel has no feeders; finalizing it there is safe. The funnel no
longer needs its own refcount — its lifetime is the wave's, and "alive while it has
feeders" is already enforced by the wave's `inFlightWork`/`totalReferences`.

## Design

1. **`Funnel[T]` becomes a plain value** wrapping `*funnel[T]` directly. Remove
   `leakguard`, `funnelHandleTrait`, `Funnel.Close`, `Funnel.Dup`, `refInner`/`ref`/
   `unref`/`dropInstanceLiveness`'s unref, and `funnel.refCount`. (`instanceCount` may
   stay as a teardown assertion.) Copying a `Funnel[T]` shares it; it is alive as long
   as its wave is.
2. **The funnel holds one per-wave reference** (`state.IncrementReference` at
   `NewFunnel`, `DecrementReference` at finalize) — the proper replacement for the old
   handle-baseline ref. It does NOT touch `inFlightWork` (so it never blocks the
   Closed→Flushing transition), but it keeps the wave out of **Done** until the funnel
   is finalized. This is what makes finalization (and `factory.Close` errors) land
   *before* Done, while SkimAll is still draining.
3. **Engine funnel registry.** `funnelEngine` gains a registry (heterogeneous T → a
   `funnelFinalizer` interface) of the funnels created on it. `NewFunnel` registers
   (during Open; thread-safe — funnels may be created concurrently). The flush sweep
   reads it after Close (Open < Flushing, so no register-vs-finalize race).
4. **Finalize in the end-of-work sweep.** `cpWorker.flushAll`, after draining scheduled
   instances (which drops every instance's barrier ref), finalizes each registered
   funnel: drain its `instanceQueue` cache → pool, call `funnelFactory.Close()` (errors
   → errSink → the still-running SkimAll), recycle the `funnel[T]`, then drop the
   funnel's per-wave reference (§2). The last such `DecrementReference` is what advances
   the wave to Done — so factory-close errors are queued before Done and surface via
   SkimAll (strictly better than today's `defer funnelOp.Close()`, which usually runs
   *after* SkimAll returns).
4. **Reuse.** On wave re-arm (`ensureArmed`) the prior engine is joined and dropped; its
   funnels were finalized during that cycle's drain. The next cycle's `NewFunnel` calls
   build a fresh engine + funnels. No funnel survives across cycles.
5. **Retire `internal/leakguard`** if funnel was its only user (it is, in non-test code)
   — pending its own tests/other users.

## Migration
- Drop every `defer funnelOp.Close()` / `aggregator.Close()` in tests + examples + the
  reuse test. Remove the `Funnel.Close`/`Dup` doc references. No `.Dup()` callers exist.

## Risks / notes
- **Creating a funnel from inside a flush body** (during the end-of-work sweep) would
  register it after `finalizeFunnels` already drained the registry → its per-wave
  reference never drops → the wave wedges. This is an exotic pattern (funnels are
  normally created at wave setup, not from flush bodies) that the existing flusher
  design also handles poorly; not addressed here. Normal flush→downstream-`Submit`
  (e.g. a FlushFn feeding a skimmer) is fine — that adds skim work the drain processes,
  not a new funnel.
- **Undrained wave leaks its funnels' factory state.** Acceptable and consistent with
  wave-driven finalization (an undrained wave leaks everything); related to the
  streampool.Wait-clears-ctxpool TODO.
- **Concurrent NewFunnel** on one wave must register safely (mutex/nbcq).
- **factory.Close timing change:** fires during the drain now, not on user Close — a
  behavior improvement (errors surface), but verify no test asserted the old timing.

## Verification
- Full suite + `-race` suite; the funnel-heavy tests (`funnel_test.go`, `maxholdtime`,
  `example_funnel`, `reuse_test.go`) and `TestBySimulation -race` loop. Confirm
  factory-close still surfaces errors (a test with an erroring `closeFn`).
