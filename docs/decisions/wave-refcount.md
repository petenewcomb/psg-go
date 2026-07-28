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

- **`*waveImpl` (strong, naked — hot path pays a few cheap CAS):** every work item —
  `taskWork`/`taskPostWork`/`skimPostWork`/`blockingWorkAdder`/`skimWork`/`launcherWork`/
  `launcherScatterWork`/`limiterScatterWork`/`funnelWork`/`funnelPostWork`/`flowFireWork`,
  and `funnelInstance` — holds **its own** RefCount reference: `AddRef(impl)` when the
  `*waveImpl` is stamped onto it (minted under the dispatching method's live `Get`, so
  `refs≥1` by construction — the `AddRef`-from-zero panic is unreachable), `Release(impl)`
  at completion/`Free`. A work item's `*waveImpl` is valid exactly as long as its own
  reference is held. The dispatching public method's `Get` yields the `*waveImpl` it hands
  to the items it creates and pins them across their minting.
- **`Wave` / `Handle[*waveImpl]` (weak, upgrade to use):** the user's `Wave`, `ctxMeta.wave`
  (carries no reference of its own), and `parentWaves`.

### RefCount accounting (parallel to the two `wavestate` counters, which are unchanged)

The `wavestate` engagement counters (`inFlightWork`, `totalReferences`) still drive
Open→Closed→Flushing→Done and the `onFlushing`/`onDone` callbacks, entirely unchanged.
RefCount is the separate object-lifetime counter, held by:

- **Owner ref** — `NewWave`'s `pool.Get` (refs=1) *is* it; dropped by `Release` at **Close**
  (only on the goroutine that wins the Open→Closed CAS, so double-Close stays a no-op).
  Covers the open-but-idle window (and the never-had-work idle-Close→Done→recycle).
- **Per-holder refs** — each strong holder above `AddRef`s at mint and `Release`s at
  completion (see Reference classification). This is what keeps the impl alive for work that
  drains **after** Close, without any coupling to the `totalReferences` claim interlock:
  a holder's own reference is minted under the dispatching `Get` and is thus provably safe,
  and its lifetime is locally verifiable at the holder, not derived from a wavestate crossing.

Recycle fires when the last release lands (owner dropped at Close **and** every holder
drained) → `gen` bumps → every outstanding `Handle.Get` (user's `Wave`, stale metas) then
fails. Ordering note: a recycling `Release` runs `Reset` (re-opens wavestate); it fires only
when `refs` hit 0 = single-owner-at-recycle, so `Reset` never races a live holder.

> **DECIDED (2026-07-12): per-holder refs now; collective later.** We rejected the collective
> "`AddRef` on `totalReferences` 0→1 / `Release` on 1→0" scheme for this landing precisely
> because it welds RefCount correctness onto the wavestate claim interlock. Per-holder is a
> few more cheap a128 CAS ops per work item but is fully decoupled and locally verifiable.
> **Future optimization thread:** once this is green, revisit whether the per-holder refs can
> be elided back toward a collective count — and if so, whether the *same* structure lets us
> stop incrementing `totalReferences` per in-flight work item too (the RefCount ref and the
> `totalReferences.IncrementWork` are structurally parallel; a scheme that batches one likely
> batches both).

### No wavepermits gate

Permit cache nodes (`*permits.Cache`) are **forest-refcounted separate-heap objects**, and
wave-identity resolution (`ensureCache`/`ensureCacheChain`) walks only the **synchronous**
ancestor chain, which is provably still Open (wavepermits.go:16-19, 66-70). A descendant that
outlives its parent (the hand-off case) uses the `*Cache` **recorded on its body meta**, not
wave re-resolution, and keeps the parent's `Cache` object alive on its own child-ref
(`releaseCaches` already clears `wv.caches` and drops self-refs at Done). So recycling the
`waveImpl` is orthogonal to the forest — **no cache-node `AddRef`s, no subtree-drain wait.**
This is the note's rejected "re-key caches by handle" alternative, now free with `Handle`.

### Identity comparisons — REVISED (2026-07-12): only `parentWaves` is gen-guarded

Under **per-holder refs**, `ctxMeta.wave` (a meta's OWN wave) is a **naked `*waveImpl`**,
not a Handle. Every read of it is provably live: it is read only synchronously on a
goroutine whose dispatch/body pins that wave, or via a `syncParent` walk that stops at
`permitRoot` and therefore only ever touches the current goroutine's stack of nested,
still-executing bodies (each holding its wave's per-holder reference). A meta CAN outlive
its wave via the refcounted `.parent` link, but **nothing reads `.wave` across that link**
— the wave-bearing walks all use `syncParent`; the bare-`.parent` walks (origin, unrefMeta
cascade) read `.selfCtx`/`.refs`, never `.wave`. So the earlier "async `ctxMeta.wave`"
hazard was an artifact of the superseded collective-ref model.

The **only** genuinely cross-lifetime weak holder is **`ctxMeta.parentWaves`**: a descendant
records its ancestor waves but holds NO reference on them, so an ancestor may recycle and
be reused while a stale entry survives. A bare-pointer key would then false-match a new
wave on the reused impl and fire a **spurious** "child wave" panic. So `parentWaves` is
keyed `map[omnipool.Handle[*waveImpl]]struct{}`, membership tested by `NewHandle(liveWave)`
(the stored handles are never `Get`-upgraded — pure identity+gen comparison). Its sole job
is rejecting upward dispatch/skim (into an ancestor wave from a descendant ctx —
`TestTaskCannotSkimParentJob`); load-bearing (prevents inverted permit-forest edges).

**`parentWaveSet` (landed, `parentwaveset.go`):** the per-dispatch copied map is replaced by
a refcounted+pooled struct that CONTAINS a map (a map, not a cons-list — capacity preserved
via `clear()` on Reset). It embeds `omnipool.RefCount`, so same-wave/wave-less derivations
SHARE one set by pointer (`retainParentWaveSet` = AddRef, no copy) and only a cross-wave hop
draws a pooled set and copies into its retained-capacity map; the map keys stay gen-guarded
`Handle`s. A meta holds a reference from assignment to its Reset (`releaseParentWaveSet`). The
set carries no Handle of its own (strong holders only → the embedded generation is inert),
which motivates the follow-up split of `omnipool.RefCounted` (a64, no gen) from a
`GenRefCounted` (a128) that only `Handle` requires — `parentWaveSet` needs only the former.

### omnipool.Handle API

`Is(p) bool` (identity, no gen check — the synchronous/live comparison), `Empty() bool`
(the zero handle), `Valid() bool` (bound and current-gen — referenceless liveness). The
handle constraint is `HandleP = interface{ comparable; RefCounted }` — comparable can't be
embedded into `RefCounted` itself, which is used as an ordinary interface value (the pool's
type assertions). No raw `Peek`: a site that knows a handle is live was handed the naked
pointer to begin with; the sole exception (foreign-but-provably-live chain walk) is a
non-issue because `ctxMeta.wave` is naked. Log a handle with `%v`.

### Amendments — SUBSUMED (2026-07-12), not implemented

The two amendments below were premised on the collective-ref model and are subsumed by
per-holder refs; validate via the `-race` sim rather than adding the machinery:

- doneChan gen-capture at the skim park — unnecessary: `Wave.Skim` holds its own `Get`
  reference across the entire park, so the impl cannot recycle (and re-mint doneChan) while
  a skimmer is parked on it.
- `Accepted.Init` listener guard — unnecessary: at Done every parked listener has already
  unparked (observed Done / ErrWaveDone) before the last reference drops and the impl
  recycles, so no cycle-N listener survives into cycle N+1.

## Landing (single all-or-nothing pass to green; `-race` + `TestBySimulation` gated)

**DECIDED (2026-07-12): land the full end-state in one pass — no un-pooled intermediate.**
The earlier A/B/C phasing (A un-pooled, B pool+RefCount, C gen-guard weak holders) is
superseded: an un-pooled Phase A would wrestle the same ~30-file `*Wave`→handle seam twice,
against the "wrestle each seam once" principle. Because pooling recycles `waveImpl`
immediately, the cross-lifetime weak holders (`ctxMeta.wave`, `parentWaves`) must be
gen-guarded in the same landing (they'd otherwise misidentify a reused incarnation). Scope:

1. Extract `waveImpl` (today's `Wave` substrate) + embed `RefCount`; `Init` (one-time warm
   queue setup) / `Reset` (per-recycle clear, never re-Init queues, never touch RefCount).
2. `Wave = struct{ h omnipool.Handle[*waveImpl] }`; `NewWave()` only; public methods
   `Get`→defer `Release`→impl. Delete `ensureInit`/`ensureArmed`/`initState` re-arm.
3. Owner ref + per-holder refs (accounting above). Strong holders keep naked `*waveImpl`.
4. Gen-guard the two cross-lifetime weak holders: `ctxMeta.wave` + `parentWaves` → `Handle`.
5. The two amendments (doneChan gen-capture at skim park; `Accepted.Init` listener guard).

Rewrite `reuse_test.go` + `alloc_test.go` off the deleted zero-value / `sync.Pool`-of-Waves
reuse model onto `NewWave()` (now allocation-free via the impl pool).

## Deleted by this design

`ensureInit`/`ensureArmed`/`initState` re-arm, the count-restart concern, zero-value
usability, the epoch-fence, and the entire wavepermits retirement gate.
