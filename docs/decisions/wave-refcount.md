# Wave over a pooled `waveImpl` behind a RefCount handle

Status: design settled (2026-07-12); implementing in phases. Supersedes the re-arm /
epoch-fence Wave design in `nbcq-pinning-and-reclamation.md` (§"Tier R").

## Why

Today a `Wave` is a user-owned, un-pooled, zero-value struct **re-armed in place** by
`initState` — which re-`Init`s `skimQueue`/`workQueue`/`governor` (the count-restart the
twin-anchor nbcq design forbids) and reuses the same pointer across cycles, so a stale weak
holder from cycle *N* (a pooled `ctxMeta.wave`, a `parentWaves` entry) sees the same
`*Wave` in cycle *N+1* and misidentifies. This migration:

1. **Gen-guards wave identity** so referenceless holders can't misidentify across incarnations.
2. **Pools `waveImpl`** so framework-created **sub-waves** (the per-op load this project
   exists for) are allocation-free, with warm nbcq chunks.

It adopts `internal/omnipool.RefCount` (see `omnipool-refcount.md`).

## The model

**`Wave` is a copyable `struct{ h omnipool.Handle[*waveImpl] }`.** `NewWave()` is the only
constructor; there is **no re-arm** — Done/Close are terminal, the next use is a fresh
`NewWave`. `waveImpl` holds the former Wave substrate (`state`, `skimQueue`, `governor`,
`workQueue`, `caches`, `funnelInstances`, cached closures) plus an embedded `RefCount`, and
implements `Initer` (one-time queue `Init`, kept warm across recycles) + `Resetter`
(per-recycle clear: `funnelInstances`, residual skim results, stage→Open, fresh `doneChan`;
**never** re-`Init` the queues).

**Public `Wave` methods** upgrade, act, release:

```go
func (w Wave) SomeOp(...) ... {
    impl, ok := w.h.Get()
    if !ok { return /* wave done */ }
    defer omnipool.For[waveImpl]().Release(impl)
    return impl.someOp(...)
}
```

The `Get`-racing-recycle case is exactly the straddle proven in the omnipool tests.

### Reference classification

- **`*waveImpl` (strong, naked — hot path pays nothing):** every work item that holds an
  engagement reference — `taskWork`/`taskPostWork`/`skimPostWork`/`blockingWorkAdder`/
  `skimWork`/`launcherWork`/`launcherScatterWork`/`limiterScatterWork`/`funnelWork`/
  `funnelPostWork`/`flowFireWork`, and `funnelInstance` (holds an explicit
  `IncrementReference`). A work item's `*waveImpl` is valid exactly as long as its own
  engagement reference is held. The dispatching public method's `Get` yields the `*waveImpl`
  it hands to the items it creates.
- **`Wave` / `Handle[*waveImpl]` (weak, upgrade to use):** the user's `Wave`, `ctxMeta.wave`
  (carries no reference of its own), and `parentWaves`.

### RefCount accounting (parallel to the two `wavestate` counters, which are unchanged)

The `wavestate` engagement counters (`inFlightWork`, `totalReferences`) still drive
Open→Closed→Flushing→Done and the `onFlushing`/`onDone` callbacks. RefCount is the separate
object-lifetime counter, held by two things:

- **Wave's own ref** — `NewWave`'s `pool.Get` (refs=1) *is* it; dropped by `Release` at
  **Close**. Covers the open-but-idle window.
- **Engagement ref** — `AddRef` on `totalReferences` 0→1, `Release` on 1→0 (transitions
  `InFlightCounter` already returns). Keeps the impl alive for work that drains **after**
  Close. `totalReferences` stays pure work/funnels, so it cycles 0↔1 as the wave goes
  busy→idle→busy while open.

So: **2 ops (NewWave/Close) + 2 per `totalReferences` 0↔1 cycle**, plus transient per-method
`Get`/`Release` and weak-holder upgrades. Recycle fires when the last release lands (Closed
**and** drained) → `gen` bumps → every outstanding `Handle.Get` (user's `Wave`, stale metas)
then fails.

### No wavepermits gate

Permit cache nodes (`*permits.Cache`) are **forest-refcounted separate-heap objects**, and
wave-identity resolution (`ensureCache`/`ensureCacheChain`) walks only the **synchronous**
ancestor chain, which is provably still Open (wavepermits.go:16-19, 66-70). A descendant that
outlives its parent (the hand-off case) uses the `*Cache` **recorded on its body meta**, not
wave re-resolution, and keeps the parent's `Cache` object alive on its own child-ref
(`releaseCaches` already clears `wv.caches` and drops self-refs at Done). So recycling the
`waveImpl` is orthogonal to the forest — **no cache-node `AddRef`s, no subtree-drain wait.**
This is the note's rejected "re-key caches by handle" alternative, now free with `Handle`.

### Identity comparisons

- **Synchronous / live** (cache resolution `m.wave==wv`, `shouldBlock`, same-wave fire):
  always compare a live wave — gen-guard harmless, not load-bearing.
- **Cross-lifetime weak** (`ctxMeta.parentWaves` ancestry set, async `ctxMeta.wave`): can
  hold a recycled-and-reused ancestor, so `parentWaves[wv]` on a bare pointer could
  false-match a new wave on a reused impl. These become `{impl, gen}` `Handle`s / handle-keyed
  maps.

### Amendments carried in

- doneChan captured **with gen** at the single skim park (wave.go:605) — park on the captured
  channel so a stale wake can't strand the skimmer.
- Listener guard injected at `Accepted.Init` (rdvq/workq know nothing of gens).

## Phasing (each lands green; `-race` + `TestBySimulation` gated)

- **Phase A — substrate split + `NewWave`, un-pooled.** Extract `waveImpl`;
  `Wave = struct{ impl *waveImpl }` (raw pointer, GC'd, one impl per wave); public API →
  `NewWave()`; work-item `.wave` → `*waveImpl`; `wv.field`→`impl.field` throughout. Pure
  mechanical relocation, semantics-identical — the big churn, low risk, isolates it from the
  concurrency change.
- **Phase B — pool + RefCount.** `waveImpl` embeds `RefCount` (`Initer`/`Resetter`); `Wave`
  swaps the raw `*waveImpl` → `Handle`; the two-op accounting above; public methods
  `Get`/`Release`. Weak holders still bare (safe: Phase B doesn't yet recycle under stale
  weak holders because — see Phase C — the only cross-lifetime holders are `parentWaves`/
  async `ctxMeta.wave`; until they're handle-guarded, retirement is held by... see below).
- **Phase C — gen-guard the cross-lifetime weak holders.** `ctxMeta.wave` + `parentWaves` →
  `Handle`; the two amendments. The only delicate concurrency surface, and now scoped to just
  those two.

(Phase-B/C ordering note to resolve at implementation: recycling under an un-guarded stale
`parentWaves`/`ctxMeta.wave` is the exact hazard Phase C fixes, so either B and C land
together, or B keeps those holders pinned strong until C — decide when B is in hand.)

## Deleted by this design

`ensureInit`/`ensureArmed`/`initState` re-arm, the count-restart concern, zero-value
usability, the epoch-fence, and the entire wavepermits retirement gate.
