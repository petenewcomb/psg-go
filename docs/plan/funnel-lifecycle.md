# Plan — wave-scoped funnels, no explicit lifecycle

> **LANDED 2026-06-23.** A `Funnel[T]` is a plain value with no `Close`/`Dup` and no
> teardown; `AccumulatorFactory` has no `Close`; the funnel relies only on a
> well-defined instance lifetime. `internal/leakguard` is deleted (funnel was its
> only user). Verified: full suite + `-race` suite + otpsg + `reuse_test.go` + 40×
> `-race` `TestBySimulation`.

## Design

The locked surface is "Funnel = wave-scoped; no `Close`/`Dup`; finalization is
wave-driven." The key realization (w/ PN) is that **the point of a lifecycle is to let
the user pool allocations against well-defined lifetimes** — and the funnel can deliver
that with a *contract*, not a teardown callback. Two guarantees suffice:

1. **The framework never touches an [Accumulator] instance after calling its `Flush`.**
2. **Every outstanding instance is flushed before its wave drains to Done.**

Given those, a user can pool their own accumulator state and any factory-level
resources freely: an instance is done at `Flush`, and the whole funnel is done when the
wave's drain (`CloseAndSkimAll`) returns — a clean sync point the user already has
(they own the factory). So there is **no `AccumulatorFactory.Close`** and no
framework-mediated finalization.

### What the funnel is

- `Funnel[T]` wraps `*funnel[T]` directly (no leakguard handle). Copy it freely; it
  shares one `funnel` and is alive as long as its wave is. No `refCount`, no `ref`/
  `unref`, no `Close`/`Dup`.
- The funnel holds **no** per-wave reference and is **not** registered anywhere. The
  funnel value is GC'd when it (and its copies) fall out of scope.
- Each accumulator **instance** holds one per-wave reference (`IncrementReference` at
  creation, `DecrementReference` in `flush`). That per-instance barrier is the entire
  mechanism: the wave cannot reach Done while any accumulator is unflushed, and the
  end-of-work flush sweep (`cpWorker.flushAll`) force-flushes every outstanding instance
  at drain — which *is* guarantee (2).
- Spent instance shells are recycled to the instance pool incrementally (on the next
  reuse-pop) during normal operation; any still cached at drain are simply GC'd with the
  funnel. No teardown drain needed.

### How we got here

The first cut (commit `a2fceba`) kept `AccumulatorFactory.Close` and built wave-driven
finalization to call it: an engine funnel registry, a per-funnel wave reference, and a
`finalize` step in the flush sweep that closed the factory before dropping the
reference (so close errors surfaced via the still-running SkimAll). Then we questioned
whether factories need closing at all — they don't, given the contract above — so all
of that (registry, `finalize`, the funnel-level wave reference, `factory.Close`, and
`internal/leakguard`) was removed.

## Migration
- Removed `AccumulatorFactory.Close()` from the interface and every adapter
  (`AccumulatorFactoryFunc`, `FuncAccumulatorFactory`, `FuncErrAccumulatorFactory`) plus
  the `closeFn` parameters of `NewAccumulatorFactory`, `NewErrAccumulatorFactory`,
  `NewFnFunnel`, `NewErrFunnel`.
- Dropped every funnel `.Close()`/`.Dup()` in tests/examples; deleted
  `TestFunnelFactoryCloseFires` (no factory close to assert).

## Notes
- An **undrained wave** never flushes its instances and never reaches Done — that's the
  user's bug (like never closing a channel); nothing leaks that wouldn't already.
- The documented promise lives on [Accumulator] / [AccumulatorFactory] in
  `accumulator.go` — it's the user-facing contract that makes pooling safe.
