# PSG-Go Combiner Branch Working Notes

This document contains working notes and context for development on the `combiner` branch.

**►►► DISPATCH/EXECUTION SPLIT — INVARIANTS SETTLED (2026-07-14). Design-review outcome; supersedes the checkpoint's next-steps below.**

The split is BUILT AND WIRED, not an open architecture question: `defaultPool` is an admission-only
`workq.Scheduler` (its `Work` phase is a no-op, `scheduler.go:167`) and user bodies run on a separate
`bodyExecutor` (`execpool`); C1/B/C2a/C2b **and the C2c cutover** have landed. What remains is INVARIANT
COMPLETION — closing the scheduler-side waits that still block on user code. Three invariants, settled
with PN:

- **(I1) Every scheduler-goroutine wait must be bounded by the framework** — it terminates even if no
  user code ever makes progress. Executor block-as-demand qualifies (bounded by *unconditional spawn*;
  this DEPENDS on executors staying uncapped / nesting-bounded — capping them would make the handoff
  user-code-bounded and break I1). Scheduler-side drain waits and blocking permit acquires do NOT
  qualify and are forbidden on the scheduler. (This is the operative form of "never wait on user code";
  it names the liveness property directly, so it also forbids a *blocking* permit `AcquireWait` on the
  scheduler, which "not user code" left ambiguous — that's the executor's job.)
- **(I2) A user-code submit** either succeeds immediately (possibly POSTPONED — queued non-blocking for
  re-drive) or BLOCK-AND-HELPS (blocks while draining its own wave). It must never JUST-BLOCK.
  Block-and-help is mechanically a **single park whose selectFn composes every relevant wake source —
  and NEW SKIM WORK is always one of them**, regardless of what else the session awaits (a permit, a
  skim-queue slot, …). The composition lives in `skimSelect` (via `addWorkWhileMaybeBlocking`,
  wave.go:556) and is already how a permit block-and-help (`wv.block`) responds to skim work. A block
  that composes no skim-work is not block-and-help — it is a forbidden just-block (the anomaly:
  `skimPostWork`'s `BasicPushSelect`, wave.go:739).
- **(I3) A permit held by user code is SUSPENDED** (released to its cache → borrowable) for the entire
  duration of any block-and-help.

**The two deadlock strands (skim-handler-drives-subwave, `vetNotNestedInSkim` removed) map onto these:**
- **Strand 3** — scheduler workers wedge in `skimPostWork.Execute`'s bare-block `BasicPushSelect`
  (`wave.go:739`) = **I1 + I2** violation. Root: `ShouldBlock()` is true for `taskContext`
  (`ctxmeta.go:266`), routing a task-body result-post to the blocking branch instead of postpone.
- **Strand 2** — a block-and-help chain whose driver is a limiter-free skim handler; `currentHeldPermit`
  is nil the whole way down = **I3** violation (the parked ancestor's permit is never suspended /
  borrowable across the handler).
Both must be fixed to remove the guard — the hang is their *conjunction* (fix A alone: 63%→33%).

**Corollary (PN): `*PostWork` is unnecessary under I1.** The three types
(`skimPostWork`/`taskPostWork`/`funnelPostWork`, + the flow-fire post) are the pre-split shape of a
post-that-might-block carried as a re-drivable scheduler `Work`. They dissolve along two seams:
- `taskPostWork`/`funnelPostWork`/flow-fire → the scheduler's terminal handoff (CP1-CP3 pull →
  `PushBack`, not yet landed). Already I1-safe (TryPushBack-first, block only on spawn) — a pure
  SIMPLIFICATION, not a fix.
- `skimPostWork` → postpone (nested) or block-and-help via `yield` (top-level). Removing its bare-block
  branch is the I1/I2 DEADLOCK FIX.

**Sequenced removal (in progress, start = skimPostWork):**
1. **skimPostWork blocking removal (the fix).** (a) ✅ DONE (`91f0dda`) — fix A: `ShouldBlock` →
   top-level-only, nested posts POSTPONE [1000-check `-race` green, 379s]. (b) route the top-level
   result-post's block-and-help through `addWorkWhileMaybeBlocking`/`skimSelect` (the push is the
   confirm, composing new-skim-work drain) and DELETE the bare-block `BasicPushSelect` branch. (c)
   assess dissolving the type into the scheduler's native postpone.
2. **I3 — suspend across the limiter-free skim handler** (strand 2). Then remove `vetNotNestedInSkim`.
3. **Handoff `*PostWork` simplification** (CP1-CP3 pull-intercept) — task / funnel / flow-fire.

**⚠️ OPEN — verify + document (skimmer seriality under block-and-help).** THE CONTRACT (PN): a wave's
skim handlers run **serially provided the user calls that wave's top-level `Submit` and `Skim*` methods
serially**. If the user calls them concurrently (a wave explicitly shared across goroutines), handlers may
run concurrently — the user's own choice, and handlers must then be concurrency-safe. Simple rule:
**skimmer concurrency mirrors exactly the user's concurrency of top-level-`Submit`/`Skim*` calls; the
framework adds none.** User-applicable definition of the operative term: **a ctx is "top-level relative
to wave W" iff it is NOT internal to W** (not a ctx W minted for its own bodies/handlers). The user's
whole test is thus local — *did this call pass a context from inside W, or an outside/independent one?*
Outside ⇒ top-level ⇒ block-and-help + this serial contract; inside ⇒ nested ⇒ postpone. (Confirm this
matches the code's `IsTopLevel`/`ShouldBlock` determination when documenting.)
- **Verify:** the framework never runs a wave's skim handler off a *drive goroutine* (one the user called
  top-level-`Submit`/`Skim*` on). Block-and-help skims INLINE on the caller's goroutine (adds none);
  scheduler/executor never run handlers; fix A's postpone re-drives the *result-post* (push into
  `skimQueue`), NOT the handler. Confirm no path violates this.
- **Document:** the mirror rule + the surprise that a plain top-level `Submit` may run *pending* handlers
  inline (block-and-help) — always on the caller's goroutine, so it never breaks a seriality the user did
  not create. NOT specific to 1b: the **permit** block-and-help already skims (`wv.block`→`skimSelect`),
  so this covers every top-level block-and-help site.

---

**►►► SKIM-HANDLER-DRIVES-SUBWAVE DEADLOCK — DIAGNOSED, CHECKPOINT (2026-07-13). Resume in a fresh session.**

Investigated whether `vetNotNestedInSkim` (the guard forbidding a skim handler from driving a
sub-wave) is removable now that task-to-task scatter landed (commit `4519665`). Removing it
DEADLOCKS. Full diagnosis below; the experiment is saved as `.claude/skim-subwave-deadlock-experiment.patch`
(untracked, gitignored) and the tree was reverted to `4519665` (clean) so this session's throwaway
instrumentation/biased-config doesn't linger.

**Resume:** `git apply .claude/skim-subwave-deadlock-experiment.patch` re-applies the guard removal + candidate
fixes + trace instrumentation + biased sim config. Then loop the hang test:
`for i in $(seq 1 30); do timeout 15 go test -run TestBySimulation -count=1 -rapid.checks=1 -timeout 12s . 2>&1 | grep -q "test timed out" && echo HANG; done`

**Repro (in the patch's `simulation_test.go` bias + `plan.go`):** force every skim handler to drive a
subjob (`Skimmer.Handle.Subjob.Add=1.0`) with skim-handler subjobs enabled in the generator
(`plan.go`: skim `newFunc(..., allowSubjob=true)`), one shared 1-permit task limiter, zero SelfTime,
small counts. Hang rate at baseline (guard removed, no fix): **~63%**.

**Deadlock mechanism (PROVEN via runtime `-trace` + `internal/cmd/fmttrace`):** a three-way cycle —
1. Permit-holding task bodies park in `defaultPool.Post` (in-body `Submit` → `workerExEnv.ExecuteNowOrQueue`
   → `Scheduler.Post`), holding their permit, waiting for a scheduler worker.
2. `Post` can't complete — no scheduler worker is live.
3. Scheduler workers are backpressure-blocked INSIDE `skimPostWork.Execute` (`rdvq.BasicPushSelect`,
   `internal/rdvq/queue.go:96`) pushing skim results into a full skim queue — violating the
   always-live-dispatcher invariant. `block-as-demand` spawns a cascade of replacement workers, all
   backpressure-block (spawn tree observed: g20→g21→g34,g35→g23,g50,g51→g66→g67).
4. The skim queue can't drain because draining runs skim handlers that drive subwaves needing the
   permits held in (1).
Second strand: block-and-help acquire chains (`blockAcquire` → `wv.block`) where the DRIVER is a skim
handler (holds no permit), so `suspendHeldPermit`'s lend never fires — `currentHeldPermit(meta)` is nil
on the whole drive chain (chain-dump instrumentation showed every meta `held=false`, `permitRoot=false`;
`headGather` logs show `anyInUse=true suspended=0` forever).

**Two partial fixes tried (both in the patch):**
- **(A) `ctxMeta.ShouldBlock()` → top-level only (drop `taskContext`).** Rationale: a task body's
  in-body skim-post is downstream routing of already-bounded in-flight work, not new entry — it should
  postpone like skim/funnel bodies already do (they return false in `ShouldBlock`). taskContext is no
  longer special. Result: **halved the hang rate (63%→~33%).** Correct on its own; keep it.
- **(B) `gateAcquire` postpone-only (removed block-and-help).** Result: **0% hangs**, BUT every run
  PANICS — `ex.AddToListeners` for a top-level admission is a panic guard (set in
  `ctxMeta.ExecuteNowOrQueue`) because top-level dispatch work runs INLINE, not as a re-invocable queued
  item. WRONG — it threw out the block-and-help *driving* that admission needs.

**Corrected direction (PN):** postpone *permit acquisition*, NOT admission. Admission must KEEP
block-and-helping (the driver must keep draining the wave); only the permit-needing work postpones
(register on the permit cache waiters, re-drive when a permit frees) — don't park the goroutine on that
specific permit's wake. The real obstacle: **top-level admission work runs inline and can't postpone**
(the panic guard encodes "top-level blocks, never postpones"). So the fix = make top-level admission
RE-DRIVABLE = the **dispatch/execution split** (TODO.md's named next major piece; also what the
pre-existing ~1/120 nested-drain `-race` hang needs). Once admission is postpone-capable,
`vetNotNestedInSkim` becomes removable — the guard was compensating for block-and-help all along.

**Next steps (fresh session):**
1. Rework `blockAcquire`/`wv.block`: help-drain, then postpone the permit-work on the cache waiters
   (keep helping; don't park on the permit) — WITHOUT removing the help.
2. Make top-level admission re-drivable: replace the `ex.AddToListeners` panic guard in
   `ctxMeta.ExecuteNowOrQueue` with a real registration + re-drive of the postponed top-level work.
3. Re-run the biased hang loop → expect 0 hangs, no panic, clean assertions. Then full `-race`
   `TestBySimulation` gate with `vetNotNestedInSkim` removed.
4. Land fix (A) (`ShouldBlock` top-level-only) regardless — it's a correct, standalone improvement.

**Follow-up (PN):** audit how much `ctxType` still matters beyond top-level-or-not. Across the codebase
only `topLevelContext` (3 sites) and `skimContext` (3 sites) are ever explicitly compared; `taskContext`
and `funnelContext` never are. Likely collapses toward {top-level, skim, other}.

---

**►►► DEVELOPMENT.md TIGHTENING + CODEBASE COMMENT SWEEP — DONE (2026-07-13, uncommitted).**
DEVELOPMENT.md: comment/testing/design-guideline edits plus additions — naming conventions
stated by their reasons (prose call sites, no qualifier/type suffixes, project-wide metaphor
and family-identifier consistency, complete terminology retirement), the concurrency commit
gate (large `TestBySimulation -race` batch green BEFORE commit, generous `-timeout`), the
pattern-reuse rule (use/extend codified patterns before duplicating; codify used-but-uncodified
ones), tail latency as a design principle, and a "Refactoring & API Evolution" subsection
(end-state APIs first, no compat layers; gut-to-vestigial then strip; checkpoint at green).
BENCHMARKING.md now states the signal priority explicitly (p99/max primary, throughput
secondary, p50 curiosity).

Comment sweep applied across every package per the new rules (~150 findings from a 5-agent
catalog pass, individually reviewed): change-narration ("replaces the old X", "was inside Y",
wave/phase-era labels), past-state comparisons, point-of-use forward references, and code-echo
comments removed; past-bug-as-rationale comments converted to present-tense hazard statements
keeping their decision-doc citations. Stale FACTS fixed along the way: `internal/permits/doc.go`
claimed the package was "NOT yet wired into the live limiter" (it is the live permit core);
`flowinst.go` claimed instances are GC-owned (flowInstancePool exists); `sim` claimed v1
restrictions that no longer hold (Subjob is handled; multi-limiter binding is real);
`rdvq/queue_test.go` referenced the removed "shared channel" tier;
`rdvq/inboxpool_proto_test.go` described the landed reclamation protocol as a future fix.
GATE: gofmt clean, `go vet ./...` green, `go test -short ./...` green (comment-only change).

**Phase 3 backlog — smells surfaced by the sweep (next major work item):**
1. Black-box testing policy vs the 7 `*_internal_test.go` files (root: bodyctx, ctxmeta, flow,
   limiter, limiterset, weightedlimiter; internal/wavestate/inflight). Verdict each: migrate to
   `_test` package + `export_test.go`, or extract the capability into its own internal package
   (the limiter family smells like the latter). Only internal/permits uses export_test.go today.
2. `AccumulatorFactory.Close` firing at funnel refcount zero has NO test (an orphaned comment
   describing such a test was removed from funnel_test.go).
3. Wave-named-"pool"/retired-terminology identifiers: `funnelPool := wave` in maxholdtime_test.go
   / example_funnel_test.go / funnel_test.go; sim vocabulary still job/Subjob-based; decide
   rename scope. Also "funneld" typo in example_funnel_test.go.
4. funnel_legacy_bench_test.go does not compile under its `psg_wave3_legacy_bench` tag
   (streampool.Task[T] is gone) — port or delete; the tag name itself is retired terminology.
5. Design-doc shorthand labels in comments (Design B, Phase 2b C2, CP-B1b/CP-F7, Wrinkle 1,
   resolution (c), Decision N, step-N): decide an anchoring convention — keep only where a
   docs/ citation makes them resolvable.
6. Future-work items moved OUT of comments; ensure tracked: TryAcquireUpTo (weighted-acquisition
   step 3); step-4 surface work (typed Semaphore handle replacing Pool.Resource round-trip — now
   a TODO in permits.go; consumable-sorts-last rank refinement; weighted overdraft grant/refuse
   policy — the "why not grant yet" rationale stays in limiter.go); head-only gathering +
   demand-FIFO barrier for freelance-gather contention; manager/executor split postpone hook;
   ctxMetaMap retirement (meta-context-migration.md); task-context StartTask relaxation
   (post-Wave-5); sim probabilistic mode (doc.go advertises it; the runtime is Med/threshold-only
   — doc-vs-impl gap); the package rename (streampool.Wait).
7. Misc: internal/omnipool/struct.go doc says "Put()" but the API is Release; workq/work.go TODO
   references ReadyFn (apparently renamed AddToListeners); sim plan_test.go's 11k-line expected
   string is stale (file has TODO); otpsg logging.go wrappers never read a logger from context
   (misleading comment deleted — decide whether they should); benchapp app.go infertypeargs lint
   hints (lines 54, 91); `.claude/worktrees/nbcq-stack` is a stale worktree copy that pollutes
   repo-wide greps.

**►►► OMNIPOOL ADOPTION SWEEP — LANDED (2026-07-13).** Every hand-rolled refcount now rides
`omnipool.RefCounter`. Commits: `eaf4d47` (permits.Cache + TryAddRef), `540f6e7` (Cache.alive
removed — the refcount carries liveness; the rapid model learns destruction from a Reset-fired
hook), `c2950b0` (ctxMeta), `5cf07b0` (flowRiderNode), `ffddb42` (flow conservation hook
retired). The sweep is COMPLETE — Cache, ctxMeta, flowRiderNode migrated; `Demand` stays raw by
design (not refcounted — its generation and lifecycle are orthogonal).
- **omnipool gained three general primitives:** `RefCounter.TryAddRef() bool` (resurrection-
  refusing strong pin, refs>0 CAS — the steal's weak-upgrade / permits.Cache.tryPin);
  `Release(obj) (recycled bool)` (surfaces the last-drop so a consumer unwinds a linked chain
  ITERATIVELY — copy `next` out BEFORE Release, follow the bool); `RefCounter.RefExclusive() bool`
  (get_mut — sole-holder ⇒ mutate in place, used by the flow-fire COW).
- **Resetter contract = "make the object ready to reuse."** Owned-resource teardown may live in
  Reset or post-Release; the link a consumer unwinds iteratively stays OUT of Reset (so the
  cascade never recurses through Release). ctxMeta already minted at refs=1 ⇒ mapped straight.
- **flowRiderNode was 0-published; redesigned as an OWNERSHIP/MOVE model** — `newRiderNode`
  CONSUMES its `next` (moves the caller's ref in); sharing a tail = `nodeRef` (AddRef) first.
  The move collapsed the publish-nodeRef ceremony (a carrier owns the returned ref; `prepend` is
  one line; `replace` is Release+assign).
- **Two non-obvious sites made idiomatic:** the skim-ownership TRANSFER is a plain `Release` that
  ASSERTS it doesn't recycle; the fire COW uniqueness check is `RefExclusive()`.
- **Conservation hooks are a testing-privates smell → retired once the counter is audited:**
  `ctxMetaAllocHook` and `flowNodeAllocHook` GONE (double-free now caught by the counter's
  underflow panic; the conservation tests deleted). `flowSharedAllocHook` KEPT — its shared/
  coalescing nodes (`sharedNodePool`) are still hand-rolled, so `TestFlowCoalesceConservation`
  keeps its shared-balance half.
- **`DEVELOPMENT.md` (uncommitted, PN's edit): comment guidelines tightened** — no change-
  narration, no past-state comparisons, no points-of-use in code comments. FOLLOW-UP: scrub the
  already-committed session comments (Cache/ctxMeta/omnipool) against this.
- GATE: build ./... + all-module vet + lint(0) + full `-race` across the root module (all 11
  packages) + 1000-check `TestBySimulation -race` — green.

**►►► OMNIPOOL ENGINE UNIFICATION — LANDED (2026-07-12, commit `bb2c6ff`).** Full gate green
(build ./... + all-module vet + lint(0) + omnipool -race + streampool -race-short + 1000-check
`TestBySimulation -race`). The design is IN THE CODE + commit message; recap of the end-state:
- ONE engine `basePool[O comparable]` (object-typed): fields `newObject maker[O]`,
  `findRefCounter refCounterFinder[O] func(O) Ref` (nil ⇒ unmanaged; engine nil-checks in
  Get/Release), `reset resetter[O]`. Resolved ONCE per type; no per-op capability checks.
- FRONT-ENDS: `Pool[T] = struct{ basePool[*T]; copier }` + value-typed `Clone`; `CustomPool[T
  MakerTrait[P], P comparable] = basePool[P]` (generic alias). Traits à-la-carte: `MakerTrait`
  (required) + `ResetTrait`/`RefTrait` (`RefCount(P) Ref`).
- REFERENCE MODEL: ONE accessor `RefCount() Ref`. `Ref` interface = `AddRef()` (exported) +
  `activate()`/`release() bool` (unexported ⇒ sealed, only omnipool's counters satisfy).
  `RefCounted` requires ONLY `RefCount()`; omnipool NEVER assumes `AddRef` promoted on `O` (reach
  it via `obj.RefCount().AddRef()` / finder / trait). Dropped `GenRefCounted`/`GenRefCount()`/
  `GenRefTrait` — gen recovered by asserting `Ref → *GenRefCounter` (`genRefCounterOf`, the ONE
  gen-check point). So ONE `RefCounted` constraint covers a64+a128 ⇒ `CustomAddRef` works for
  both; reflection detection is a single `typ.Implements(RefCounted)`.
- COUNTERS: a64 `RefCounter` = signed `atomic.Int64` + bare `Add` (NO CAS: strong-only, no
  resurrection race); a128 `GenRefCounter` stays CAS. `activate` lost its `fresh` bool.
- HANDLES: `Handle[P comparable]` captures `*GenRefCounter` at mint (comparable ⇒ still a map
  key) ⇒ works with CustomPool too. Minters: free `NewHandle`(embed, HandleP=comparable+
  RefCounted) / `NewCustomHandle`(trait) / `pool.NewHandle` — all runtime gen-checked now (panic
  on a64). No free `AddRef` (embed uses promoted `obj.AddRef()`; trait uses `CustomAddRef`).
- `Put` fully retired → `Release`. One justified `//nolint:staticcheck` SA6002 in base.go.
- STREAMPOOL: the 5 `.GenRefCount().Inc()` sites → promoted `X.AddRef()`; tdigest `Put`→`Release`.
DEFERRED (never needed, closures/asserts sufficed): the separate sealed `refCounter`/
`genRefCounter` interface layer from mid-design — the final `Ref` interface IS that seal.
**►►► NEXT PHASE: the actual wave-refcount MIGRATION this was all foundation for** — see the
WAVE-REFCOUNT banner below; omnipool is now the clean substrate it needs.

**►►► NEXT (2026-07-12): WAVE-REFCOUNT — IMPLEMENTED, GATING.** Full end-state landed in one
pass (spec: `docs/decisions/wave-refcount.md`). `Wave = struct{ h Handle[*waveImpl] }`,
`NewWave()` only; `waveImpl` pooled + embeds `RefCount` (`Init` one-time warm queues / `Reset`
per-recycle clear). **Refs: owner ref (NewWave Get → Close Release on the winning CAS) +
PER-HOLDER refs** — each strong holder `AddRef`s at mint (under the dispatching `Get`) /
`Release`s at completion (paired with the wavestate ref at poolWork.Init/Close, funnelInstance
Increment/Decrement, flowFireWork). Rejected the collective 0↔1 hook.
KEY REFINEMENTS (this session, all landed & green on build/vet/lint/-race-short):
- **`ctxMeta.wave` is a NAKED `*waveImpl`, not a Handle** — every read provably live
  (synchronous or `syncParent`-bounded; verified no bare-`.parent` walk reads `.wave`).
- **Only `parentWaves` is gen-guarded** (`map[Handle]struct{}`, membership via `NewHandle(wv)`)
  — the one cross-lifetime weak holder; its sole job is rejecting upward dispatch/skim
  (`TestTaskCannotSkimParentJob`). FOLLOW-UP: swap the copied map for a refcounted+pooled
  `parentWaveSet` cons-list (flowRiderNode idiom; O(1)/dispatch, depth-robust, alloc-free).
- **omnipool.Handle**: `Is`/`Empty`/`Valid`; constraint `HandleP = interface{comparable;RefCounted}`;
  no `Peek`; log with `%v`.
- **Amendments 1 & 2 SUBSUMED** by per-holder refs (Skim holds a Get ref across the park;
  listeners unpark before recycle) — not implemented; validating via -race sim.
STATUS: build/vet/lint(0)/-race-short green; `TestBySimulation -race` 100 checks green; 1000-check
-race batch running. THEN: commit checkpoint → build the refcounted `parentWaveSet` → re-gate.
FORWARD THREAD (later): collapse per-holder refs → collective, and if so drop per-item
`totalReferences.Increment` too (structurally parallel).

--- superseded phasing below (kept for context) ---

**►►► NEXT (2026-07-12): WAVE-REFCOUNT PHASE A — substrate split + `NewWave`.** Design SETTLED
+ committed: `docs/decisions/wave-refcount.md` (read it first — it's the spec). Model recap:
`Wave` becomes a copyable `struct{ h omnipool.Handle[*waveImpl] }`, `NewWave()`-only (no
re-arm; Done/Close terminal); `waveImpl` = today's Wave substrate + embedded `RefCount`
(`Initer` one-time queue Init / `Resetter` per-recycle clear, never re-Init queues); public
methods `Get`→defer `Release`→impl call, Get-fail ⇒ done; RefCount parallel to the two
`wavestate` counters (wave's-own ref NewWave→Close + engagement ref on `totalReferences` 0↔1,
= 2 ops + 2/cycle); strong holders keep **naked `*waveImpl`**, weak holders (`ctxMeta.wave`,
`parentWaves`) become gen-guarded handles; **wavepermits gate DELETED** (cache forest is
orthogonal — synchronous resolution + recorded-cache reacquire). Phases A(split)/B(pool+RefCount)/C(gen-guard).
**PHASE A = mechanical substrate split, semantics-identical, un-pooled** (raw `*waveImpl`, GC'd,
one impl per Wave). Surface map (already scouted):
- `Wave`'s 6 EXPORTED methods → become `Wave` wrappers delegating to `waveImpl`: `Skim`,
  `TrySkim`, `SkimAll`, `TrySkimAll`, `Close`, `CloseAndSkimAll` (wave.go). The other ~20 wave.go
  methods → `*waveImpl` receivers.
- 5 op-boundary sites take the wave: `Skimmer.In`/`Launcher.In` (skimmer.go:56, launcher.go:119),
  `NewResequencer`/`NewRangeResequencer`/`newResequenceFunnel` (resequencer.go:42/87/121).
- `resolveWave` (wave.go:978) → returns `*waveImpl`. Work-item `.wave` fields → `*waveImpl`.
  `ctxMeta.wave`/`parentWaves` stay raw `*waveImpl` for Phase A (→ Handle in Phase C).
- Delete `ensureInit`/`ensureArmed`/`initState` re-arm; `NewWave` does the one-time init eagerly.
- API change (FINE — unreleased): `var wave Wave` → `NewWave()`. **Rewrite `reuse_test.go` +
  `alloc_test.go`** — they encode the deleted zero-value/`sync.Pool`-of-Waves reuse model; move
  them to the `NewWave` pattern (still prove alloc-free, now via the impl pool). ~20 files touch
  Wave; ALL-OR-NOTHING to green (no mid-checkpoint) — one focused pass, then `-race` + sim gate.

**►►► omnipool.RefCount LANDED (2026-07-11) — generation-guarded reference counting for
pooled objects, the foundation for the nbcq-reclamation Phase 2 (pooled-impl handle
migration).** `docs/decisions/omnipool-refcount.md`. Standalone green checkpoint: core
(`internal/omnipool/refcount.go`) + `struct.go` Pool wiring (Get/Release, `RefCounted`
trait-detected) + model/straddle/concurrency tests. Design SUPERSEDES the note's separate-words
third-counter form: a packed **atomic128 (gen, refs)** word makes recycle a single CAS
`(1,G)→(0,G+1)`, so there is **no distinguished retirement, no arm flag, no upgrade blips** —
the owner's held reference structurally prevents an early gen bump. API: `RefCount` (embed) /
`RefCounted{Resetter; refCount()}` / `NewHandle` / `Handle.Get` (fallible upgrade) / `AddRef`
(infallible clone) / `Pool.Get` / `Pool.Release`. a128 (not a uint64 bit-split) keeps the
primitive general — the wave's count is small (cache tree absorbs fan-out; roots-only), but hot
permit cache nodes are unbounded at scale.
**a128 CLEANUPS DONE (2026-07-11):** native a128 asm is TSan-invisible, so `-race` must force
the mutex fallback. The `../atomic128-go` fork was **RE-BASED onto upstream CAFxX/atomic128**
(branch `upstream-rebase`) — upstream caught up (BP fix, mutex fallback, AVX/GOAMD64 dispatch,
tagged) and moved to a **method API** (`u.Load()`); the fork now carries only a thin patch
(HasNative/DisableNative control + `//go:build race`→DisableNative). psg migrated to the method
API. Wired via LOCAL `go.work` (gitignored, lists ALL local modules — the pre-commit hook
iterates each). `PSGNATIVEA128` stays PSG-side in nbcq's init; omnipool's TestMain stopgap
retired. `Pool.Put`→`Pool.Release` rename completed across the tree. **Couldn't drop the fork:**
upstream has no runtime fallback-forcing (our order-through-the-word usage needs it under -race);
clean end state = a tiny upstream PR adding a race fallback, then drop the fork + migrate.
NEXT: adopt RefCount on `waveImpl` (the ~15-field strong/weak sweep + Get-side re-arm replacing
`initState`), demand identity, permit cache nodes. (Optional: the upstream race-fallback PR.)

**►►► flow-impl MERGED INTO combiner (2026-07-11).** 53 flow-impl commits (flow riders,
FlowKey/FlowTag/follow-ups, ctxMeta parent-refcount CP f193c7f, PinFlow/HoldFlow/OriginFlow,
CP-R7 psgwf deletion + otpsg v2) joined with combiner's 33 (weighted Layers 1-2, multi
suspend/reclaim fixes, benchmarks) from merge-base 300576b. Resolution record:
- Content conflicts (only two): internal/sim/run.go — union kept BOTH the multi-limiter
  wiring (activeLimit list) and the flow carrier oracle (carrierAdd/assertFlowInBody);
  otpsg/instrumented.go — flow-impl's v2 structure taken whole. psgwf modify/deletes →
  DELETE per CP-R7 (benchapp already migrated there; otpsg has no psgwf refs). One API
  migration site (flow_test.go WithLimits — flow-impl predates the Layer-1 OpOption
  dissolution; everything else auto-resolved to post-dissolution code).
- SILENT-RISK fixES (both flagged by the pre-merge briefing, both in wave/permit refcounts):
  (1) flowinst.go's async follow-up fire took a plain IncrementReference on the wave —
  sound on flow-impl (its premise: the triggering item's owner ref is still held), but
  under combiner's Flushing→Done claim interlock a broken premise would resurrect a claimed
  count SILENTLY; converted to TryIncrementReference with a panic tripwire (unreachable
  while the count→0 ordering invariant holds — riders release before owner refs at every
  site; funnel.go's flush-barrier defer ordering is the enforcing interlock and survived
  the merge intact). (2) flow-impl un-severed ctxMeta.parent across goroutine boundaries
  (refcounted lifetime link) and moved the old isolation onto permitRoot/syncParent();
  verified every permit/skim-nesting walk in the merged tree (currentHeldPermit,
  vetNotNestedInSkim, ensureCacheChain) steps via syncParent() — no bare .parent walks.
- GATE (merged tree): vet, lint 0, full -short -race, permits -race, flow/pin/hold/origin
  -race ×20, TestBySimulation -race 12×100 checks (multi wiring + flow oracle BOTH active),
  flush-heavy biased recipe 0/60 (600 checks, subjob/flush-heavy, default SelfTimes),
  differential benchmark spot-check: gate pair clean (±2%); subwave pair tails clean
  (p99-e2e +6.0% merged vs +3.5% pre-merge at count=12, within spread; p99.9 +1.4%) —
  subwave THROUGHPUT deltas were unusable (unpinned clock this run; pre-merge itself showed
  −28.5% with 3× run-to-run spreads, re-confirming the recorded judge-by-tails caveat).
  borrowSrcCtx entry CLOSED (obs (2) below).
NEXT: step 4 (consumable pass) on the merged base; streamotel (flow-impl's parked NEXT) is
the other open thread.

**►►► MULTI-LIMITER BENCHMARKS LANDED + BASELINE CLEAN (2026-07-10).** New
bench/BenchmarkMultiLimiter: differential pairs on the BenchmarkDispatch harness — limits1 vs
limits2 (two semaphores, both at full capacity D ⇒ identical effective bound ⇒ delta = pure
joint-GATE overhead) and limits1-subwave vs limits2-subwave (body drives a one-task subwave
via CloseAndSkimAll ⇒ delta = joint RECLAIM: per-hold scoping, lend/withdraw, fixpoint).
Method: pinned clock (min=max=1.6GHz, performance governor), count=6 medians, suspicious
cells re-confirmed at count=12; cross-commit baseline = 28bc0f4 (pre-fix parent) in a
worktree running the identical benchmark file (limits1* + Dispatch/streampool only — a
28bc0f4 limits2-subwave run literally wedges). RESULTS:
- **Joint gate ≈ free**: tails ±2%, throughput ±1% at every regime/workload; absolute alloc
  cost ≈ +1 alloc/task (second heldPermit + rest slice — as designed).
- **Joint reclaim: NO tail signal.** All tail deltas single-digit-% with mixed signs across
  regimes; the count=6 headline costs (balanced −13.6% tasks/sec; heavy-overload +21/+35%
  dispatch tails vs baseline) BOTH evaporated at count=12 (+6.7% and +1.2/+6.6% resp., inside
  spreads). Reclaim adds ~+2-3 allocs/task. The weighted-acquisition.md §"The joint reclaim"
  revisit trigger (reclaim-latency tails) does NOT fire.
- **No single-path regression from 408a85b**: Dispatch/streampool flat at every regime
  (heavy-overload even −3.5% p99-e2e); the wavestate claim interlock, TryIncrementReference,
  and permits trace branches cost nothing measurable.
- CAVEAT for future readers: subwave-variant THROUGHPUT is inherently noisy (±30% run-to-run
  at a pinned clock) because the suspend bracket lends the outer permit across the drive —
  effective body concurrency is unbounded there by design, so scheduler/GC variance dominates;
  judge those cells by tails, or bound inner concurrency in a future variant. Raw results in
  session scratchpad (bench_head_*.txt / bench_base_*.txt / confirm_*.txt).

**►►► MULTI SUSPEND/RECLAIM BUGS ROOT-CAUSED + FIXED (2026-07-09) — FOUR defects. Roots 1-3
diagnosed from the preserved dumps; root 4 (the persistent deadlock) required biased-repro
iteration + fmttrace runtime traces + NEW permits-layer instrumentation (kept). NOTE FOR PN:
roots 3-4 add a DESIGN rule — "a joint reclaim park holds only a canonical prefix, counting
FIFO registrations as holds" — recorded in weighted-acquisition.md §"The joint reclaim".
REVIEWED + SETTLED (PN, 2026-07-10): withdraw-and-requeue stands as committed, NO new
machinery. The review resolved the fairness concern: overtaking is per-round bounded
([withdraw → re-registration] window; repeats are progress-coupled), and ADMISSIONS never
lose position at all (Decision 4 single-FIFO-entry across the postpone/retry cycle + workq's
retry-all-postponed-before-ACCEPTING-new-work pass — a fact the workq docs misstated as
"fresh > postponed" priority; docs fixed). Yielding-head and senior-re-entry-tier recorded
as rejected alternatives; revisit only on reclaim-latency tails in multi benchmarks.
Side item from review → TODO.md: rename `permits` package → `pforest` (naming pass). GATE GREEN (2026-07-09): vet, lint 0, full -short -race, permits -race,
TestBySimulation -race 19×100 checks (11+8 across the sim alias-guard fix below; the one
intervening FAIL was a SIM plan-gen bug, not a wedge: drawLimiterBinding's alias guard
compared one inheritance level, so two indexes aliasing one Pool TRANSITIVELY dup-bound and
tripped addBinding's panic — fixed by comparing ultimate limiter IDs, which inheritance
mirrors); biased no-race multi repro 8/10 wedged → 0/12; single-limiter control 0/8 clean.
2b-iii COMMITTED with the fixes.**
- **BUG A root — Wave-pin TOCTOU at the Flushing→Done boundary (NOT a rest-hold-specific path).**
  suspendHeldPermit's IncrementReference-then-IsDone guard (5574a40) has a hole: an increment
  that RESURRECTS totalReferences 0→1 cannot stop a noMoreReferences already committed on the
  goroutine that dropped the last reference (wavestate ran `refs hit 0 → CAS(Flushing→Done) →
  onDone/releaseCaches` with the CAS AFTER the zero-crossing), while IsDone still reads the
  pre-CAS stage. The suspend then targets a cache mid-teardown and SuspendDriver's unconditional
  refs.Add(1) resurrects a DESTROYED node → ResumeDriver's ReleaseRef re-runs destroy on a node
  omnipool already re-issued → the second destroy corrupts an innocent wave's live cache. That
  one root explains BOTH race reports in multi_race_9.log (SuspendDriver-vs-Reset and
  Acquire-vs-Reset) AND the `demand homed under a different cache` panic (downstream corruption).
  Multi didn't create it — it widened the window (more pools per bracket) and added funnel-flush
  waves as frequent last-reference droppers. FIX (root-cause, not band-aid): the Done commit now
  CLAIMS the zero count before the CAS — InFlightCounter gains ClaimZero/ReleaseClaim (sentinel
  1<<40; stray misuse-class increments survive the release arithmetically) +
  IncrementUnlessClaimed; WaveState.TryIncrementReference = IncrementUnlessClaimed + IsDone
  backout. A pin and the transition serialize through the ONE atomic: pin-first → claim fails →
  wave stays Flushing, the pin's release re-triggers; claim-first → pin fails (Done committed).
  A pin on an idle-but-open wave (refs 0, stage Open) SUCCEEDS — required: a parked skim driver
  must keep lending its permit for work dispatched later (the simpler increment-if-nonzero form
  was rejected for exactly that liveness regression). noMoreReferences also gained the
  stage!=Flushing fast-out (so idle zero-crossings never claim) and releases the claim BEFORE
  close(doneChan) (a re-arm racing a standing claim would refuse the new cycle's pins).
  suspendHeldPermit + sweepFunnels use the pin. Unit: wavestate/inflight_internal_test.go.
- **BUG B root 1 — ctxmeta.go ExecuteNowOrQueue bracket reclaimed HEAD ONLY** (`h.reclaim`, a
  2b-ii oversight — wave.go's three sites got reclaimJoint): every multi body dispatching
  through the blocking path leaked its rest holds' suspensions permanently — permits lent and
  never reacquired, suspendedDrivers/pool.suspended pinned forever, so any overdraft evaluation
  waiting out suspensions waits forever (the WaitForNew mass-park in multi_wedge_4.log; g127 sits
  exactly in this call path). FIX: defer reclaimJoint.
- **BUG B root 2 — bracket re-entrancy premise false mid-reclaimJoint.** Wave.block's bracket
  comment assumed "the reclaim's own suspend finds the handle already suspended and no-ops" —
  true single-limiter (handle un-held during its own reclaim), FALSE for a joint set: after
  reclaimJoint reacquires the head, a rest hold's help-block re-engages the bracket (head held),
  and the nested unwind's UNCONDITIONAL joint reclaim re-drove holds the OUTER frame owns (its
  mid-reclaim rest hold, or a mid-admission hold owned by the gate loop) — two waiters on ONE
  demand mailbox (single-consumer by design) = dropped-wake wedge (g11's nested
  block→reclaimJoint→plain-Wait stack in multi_wedge_4.log). FIX: a bracket reclaims EXACTLY the
  holds it suspended — reclaimJoint gates each hold on its own suspendTarget (set by suspend,
  cleared by reclaim; an un-held hold's no-op suspend leaves it nil). Canonical order preserved;
  nested brackets now suspend/reclaim only the already-reacquired prefix, which is order-safe
  (every wait still points up-rank).
- **BUG B root 3 — RANK-INVERSION PARKS in the joint reclaim (found via runtime trace after
  fixes 1-3 unmasked it to 5/5 sim -race timeouts, then a 6-goroutine no-race wedge traced
  with fmttrace).** Mechanism (trace-confirmed): during reclaimJoint, a rest hold's
  help-block can reacquire that rest hold via its confirm WHILE the head is suspended (lent
  by the interior bracket) — order-inverted transiently, fine — but the unwind's head reclaim
  then PARKS waiting for the LOWER-rank permit while HOLDING the higher-rank one. A joint
  admitter parks in the CANONICAL posture (holds the lower rank, postponed on the higher):
  cycle closed. The head-only Held gate in suspendHeldPermit prevented the lend exactly when
  needed, and the reclaim's PLAIN-Wait branch (help domain exhausted, wv Done — the common
  case for the ctxmeta bracket) has NO bracket at all, so no lender exists there. FIX:
  (a) suspendHeldPermit engages if ANY hold of the set is held (not just the head), suspending
      each held one (per-hold suspendTarget scoping unchanged);
  (b) heldPermit.reclaim takes the set's higher-rank siblings and, before EVERY park (helping
      AND plain), LENDS any still held — plain Release + a new `lent` mark (no drive-target
      attribution; the Release wakes the sibling pool's head) — and reclaimJoint became a
      fixpoint loop over (suspendTarget != nil || lent), always resuming from the lowest
      rank; terminates because a park marks only ranks above the hold being reclaimed.
      Single-limiter path unchanged (rest empty, lends no-op).
  Admission is deliberately untouched: mid-sequence holds stay inUse (the gate runs before the
  body exists, so currentHeldPermit never resolves the set being admitted).
- **BUG B root 4 — FIFO-HEADSHIP INVERSION (the LAST down-edge; still 8/10 wedged after root
  3's lend rule; nailed by ADDING permits-layer trace instrumentation — enqueue/promote/
  retire/wake/Release/Suspend/Resume/gather-wait + handle-level suspend/lend/reclaim/acquire
  logs, now permanent).** Trace showed every wedged pool RESOLVING (release → wake head →
  retire) and permits activity CEASING while skimmers idled: the final cycle is through the
  DEMAND QUEUE, not permits alone. Shape: admitter W holds pool-A's permit (canonical
  mid-sequence), postponed waiting pool B. G's rest-hold-B demand is B's FIFO HEAD; the
  B-wake lands in its mailbox — but the block-unwind's deferred bracket reclaim must
  reacquire A (held by W) BEFORE the outer loop can consume that wake. G parks waiting A
  (low) while HOLDING B's HEADSHIP (high): headship reserves capacity exactly like a permit,
  so W can never take B, and B's free capacity is FIFO-reserved for a head that can never
  gather. THE ORDERING INVARIANT MUST COUNT QUEUE REGISTRATIONS AS HOLDS. FIX: the reclaim
  lend rule extends to registrations — before parking on X, any higher-rank sibling whose
  demand is Registered (new permits.Demand.Registered accessor) is WITHDRAWN via
  Demand.Invalidate (the FIFO's lazy dequeue; a head's withdrawal promotes + wakes the
  successor, so W admits). The owning loop (outer reclaim or admission gate) re-registers on
  its next confirm — Acquire re-enqueues an invalidated demand — losing only queue position;
  each surrender lets a canonical-posture admitter COMPLETE, so global progress is preserved.
  VERIFIED: biased no-race repro went 8/10 wedged → 0/12 clean at checks=30 (single-limiter
  control 0/8 clean throughout).
Files: internal/wavestate/{inflight,state}.go (+ new inflight_internal_test.go),
permithandle.go, wave.go (sweepFunnels + block comment), ctxmeta.go. A CONCURRENT SESSION
(stopped mid-flight 2026-07-09) contributed three behavior-preserving lint refinements to the
still-uncommitted sim wiring (plan.go maxBindings const; run.go make-with-cap + defer-in-loop →
single deferred closure); reviewed and kept. Its checks=200 batch was killed at iter 1 (no
results). NEXT: gate green (vet, lint, -short -race, permits -race, 12× sim -race checks=100
with the multi wiring) → commit wavestate+handle fixes and the 2b-iii sim wiring.

**Previous banner (the pre-diagnosis state, kept for the record):**

**►►► WEIGHTED SURFACE — LAYER 2 (multi-limiter). CHECKPOINT 2a LANDED (2026-07-08).** Design is
settled (weighted-acquisition.md §"Multi-limiter: the FIFO under joint admission"): CANONICAL
GLOBAL ACQUISITION ORDER → acyclic wait-for graph → deadlock-free; mid-sequence holds stay inUse
(safe, overdraft proof already accounts); consumables sort last (step-4). Rep chosen (PN): (A)
keep heldPermit as the per-limiter unit, add a heldPermitSet ordered wrapper in 2b. 2a (this
commit — ordering + sets + ordered-binding rep, NO behavior change, single-limiter runtime
unchanged): permits.Pool gets a process-global monotonic `rank` (Pool.Rank()) = the canonical sort
key. Launcher stores a canonically-ordered `bindings []binding[T]` ({pool, weigh}) replacing the
single (limiter, weigh); builder methods addBinding (sorted-insert by rank, dup=panic, copy-on-write
for In()-style immutability); WithLimiterSet/WithWeightLimiterSet added; LimiterSet (untyped,
[]*Pool) + WeightLimiterSet[T] ([]binding[T]) constructors sort+dedup+freeze. checkSingleBinding
panics on resolved >1 (multi deferred to 2b). Files: permits.go, limiterset.go (+_internal_test),
launcher.go, weightedlimiter_internal_test.go. Green: set/weighted unit, lint 0, -short -race,
permits -race, sim 300 checks. CHECKPOINT 2b LANDED (2026-07-08): the joint gate. Rep realized as (A)-variant: heldPermit stays
the per-limiter unit and carries an ordered `rest []*heldPermit` (the higher-rank holds; nil for
single/funnel — 0-alloc common case) rather than a separate threaded wrapper (which would add a
per-dispatch slice+struct alloc). acquireJoint drives head→rest via gateAcquire in canonical order
(mid-sequence block holds earlier limiters inUse; already-held pass through idempotently on
re-drive; overdraft refusal terminal). release()/taskWork.Free recurse over head+rest;
suspendHeldPermit suspends each hold onto ITS OWN drive-target cache; reclaimJoint reacquires in
canonical order. newScatterWork builds head + rest from r.bindings; checkSingleBinding dropped —
multi AND-composes. Green: joint-admission unit test (a body bound to A+B blocks both an only-A and
an only-B op → holds both), set/weighted units, lint 0, -short -race, permits -race, sim -race 12
runs/2400 cases (single-limiter path = set-of-one, no regression). 2b-iii (sim MULTI wiring, UNCOMMITTED — internal/sim/{launcher,plan,run}.go): runner binds a
deduped 1-2 subset of task limiters (drawLimiterBinding; guards against binding two indexes that
alias one Pool via shared inheritance); per-limiter weight (LimiterWeights) + tracker; threads a
LIST []activeLimit through executeFunc's Subjob suspend. It WORKS (checks=60 -race clean, joint
admission exercised) and IMMEDIATELY found TWO real multi suspend/reclaim bugs (dumps:
scratchpad/multi_race_9.log, multi_wedge_4.log):

  **BUG A — DATA RACE, rest-hold SUSPEND use-after-recycle (same class as 5574a40).** In
  suspendHeldPermit's rest loop, `r.suspend(wv.ensureCache(meta, r.pool()))` calls SuspendDriver
  on a Cache that a concurrent `funnelInstance.flush → DecrementReference → releaseCaches →
  destroy → Cache.Reset` is recycling. The head's IncrementReference(wv) bracket (5574a40) does
  NOT cover this — the recycling wave/cache is reached via a DIFFERENT path for the rest holds
  (the destroy is on a funnel-flush wave, not wv). Root-cause the exact wave/cache identity
  (is it an ancestor node in the rest pool's ensureCacheChain, unpinned by wv's ref?) then extend
  the pin to cover every hold's target.

  **BUG B — TRUE-WEDGE, reclaimJoint missed-wake.** 116 goroutines parked in WaitForNew (zero
  mutex, all select), reclaimJoint/reclaim on the stack. The rest-hold REACQUIRE (reclaimJoint
  reacquiring head then rest in canonical order, each help-shaped) strands — a wave never Done-
  signals its skimmer. Likely the reclaim of a rest hold help-drains the wrong wave, or the
  ResumeDriver/reacquire ordering across multiple holds drops a wake.

Both are in the 2b-ii multi suspend/reclaim machinery (the flagged-trickiest part), reachable
only when a MULTI-limiter body drives a subwave. The core joint GATE (5e09825) is unaffected
(single-limiter clean, joint-admission unit test green). The sim-wiring stays UNCOMMITTED until
these are fixed (it makes TestBySimulation red on multi). REPRO: the uncommitted sim-wiring at
checks=100 -race (or checks=200 for higher hit rate; distinguish real wedge [0 mutex] from the
delayq convoy [many mutex + progressing], which multi-limiter amplifies). NEXT (fresh session):
trace-hunt BUG A (pair-tally / cache identity) + BUG B (missed-wake), fix, then commit 2b-iii.
(Funnel stays single-WithLimits; funnel multi is a later extension.)

**►►► WEIGHTED SIM WIRING LANDED + FIXED A LAYER-1 OVERDRAFT BUG IT EXPOSED (2026-07-05).**
Wired weighted task limiters into internal/sim (sim/{launcher,limiter,plan,run}.go):
LimiterConfig.Weighted prob (task limiters only, permits≥2), sim.Launcher.Weight ∈[1,permits]
(clamped), weight-aware limiterTracker (enter/exit(w), max weight-sum ≤ permits), ensurePools
builds NewWeightedSemaphore + binds via .WithWeightLimits with a constant weigher. It dispatches
w≥2 and immediately wedged the sim (~1/3), which a runtime trace (725M, PSGTRACEINTERNALS + pair-
tallying poolWork.Init/Close WorkItem IDs) root-caused: 18 taskWorks Init'd (IncrementWork) but
never Closed → inFlightWork stuck nonzero → wave never Done → SkimAll/WaitForNew wedges (SAME
STACK as the Jul-4 pol_sim1 hang, but a DIFFERENT cause — red herring). ROOT: Layer-1
`weightedSemaphoreResource.Overdraft` REFUSED every demand at a nonzero ceiling on the false
premise "reaching overdraft ⟹ w>cap". It ignores HELD-BORROWABLE capacity: the proof requires
zero forest inUse but NOT zero held, so a demand of weight n≤cap reaches Overdraft transiently
when the gather hasn't assembled idle held capacity — the plain semaphore WAITS there; weighted
wrongly refused (terminal), stranding the postponed work. FIX (weightedlimiter.go): refuse ONLY
n>limit (permanently oversized); else wait, exactly like plain. Regression:
TestWeightedSemaphore_OverdraftWaitsWhenItFits. Verified: biased hunt config 0/12 (was 2-3/10);
sim -race 28 runs/~5600 cases clean at default Weighted=0.35; weighted+permits+sim unit green.
NOTE: this was NOT the pre-existing pol_sim1 hang (identical SkimAll-wedge stack, different cause).

**►►► pol_sim1 / skimSelect-WaitForNew w=1 HANG CONFIRMED FIXED (by 5574a40) via the CORRECT
recipe; remaining -race timeouts = delayq CONVOY (perf, not a wedge) (2026-07-06).** The Jul-4
pol_sim1 missed-wake (145 goroutines parked in rdvq handoff, ZERO mutex waiters) — same class as
flow-impl obs (1) below. CAVEAT ON THE FIRST PASS: an initial chase used ZERO SelfTime, which the
flow-impl recipe flags as a DEAD CONFIG (zero-delay -race ×2500 → 0 hits; the hang NEEDS real
delays/parked-worker windows), so that pass was uninformative. RE-VALIDATED with the flow-impl
recipe (DEFAULT SelfTimes, subjob-add probs raised: Launcher.Body 0.5 / Funnel.Accumulate 0.3 /
Funnel.Flush 0.5 / Skimmer.Handle 0.3, weighted OFF, -race, checks=10 → ~1/300 checks expected):
0 true wedges in 1200+ checks (~4 expected if unfixed; also 0 DATA RACE for obs (2)). Combined
with 5574a40's commit explicitly targeting the signature (Cache use-after-recycle corrupting the
wake chain; -race caught the clean race, un-raced it cascaded to the handoff wedge) → fixed.
The only -race timeouts under this recipe are the DELAYQ CONVOY: hundreds of scheduler workers on
the delayq Queue mutex (drainScheduled→delayq.Drain→foldUpdates, O(n) under a global lock) with
runnables still PROGRESSING — a slow-but-live convoy, NOT a deadlock (~2/120 iters). SEPARATE perf
item (foldUpdates scalability). Distinguish: convoy = many sync.Mutex.Lock waiters + progressing
runnables; real missed-wake = ZERO mutex waiters, all [select].


**►►► HANDED OFF FROM flow-impl (2026-07-06): two pre-existing funnel/permits infra
observations surfaced during the flow-rider-chain work.** Moved here so this thread owns
them — neither is a flow bug (flow code audited as nil-rider no-ops on the sim paths; sim
never calls WithFlow).

**(1) Rare sim HANG — skimSelect/WaitForNew. → RECONCILED 2026-07-06: CONFIRMED FIXED by
5574a40** (see the pol_sim1 banner above). Ran the flow-impl recipe below verbatim on combiner
(default SelfTimes + raised subjob-add probs, weighted off, -race, checks=10): 0 true wedges in
1200+ checks (~4 expected if live). Same class as pol_sim1; 5574a40's Cache use-after-recycle fix
covers it. (My own earlier chase's zero-SelfTime config was the dead zone the recipe warns about.)
  - Signature (flow-impl, first seen 2026-07-04, CP-F4 batch iter 27; last seen 2026-07-05
    R6a batch seed 6, intermittent, passed 2/2 on re-run): 10m -race timeout; 6 goroutines;
    NO mutex/semacquire waiters; 4 skim drivers parked ~9m in Wave.skimSelect via
    addWorkWhileMaybeBlocking/rdvq (top-level Run + two subjobs + a funnel-flush-driven
    subjob: funnelInstance.Run→flush→sim runSubjob→CloseAndSkimAll→WaitForNew); all executor
    workers idle-exited ⇒ missed-wake / stuck-reference class (some wave never Done-signaled
    its skimmer). Distinguish from the delayq convoy: convoy has many sync.Mutex.Lock waiters
    + progressing runnables; this hang has ZERO mutex waiters, all in [select].
  - ATTRIBUTION (flow-impl A/B): the pre-flow base 300576b — ZERO flow code — hung 1/30
    under the subjob/flush-heavy bias with the identical signature.
  - VALIDATED REPRO RECIPE (~9× ambient — use to confirm fixed or residual): -race,
    -rapid.checks=10, DEFAULT SelfTimes (zero-delay KILLS the repro — it needs real
    delays/parked-worker windows), planConfig Subjob.Add probabilities raised: Launcher.Body
    0.5, Funnel.Accumulate 0.3, Funnel.Flush 0.5, Skimmer.Handle 0.3 → ≈1/300 checks (vs
    ~1/2600 ambient). Dead configs: zero-SelfTime no-race ×300 and zero-SelfTime -race ×2500,
    0 hits both. (Dumps were preserved at the flow-impl scratchpad; discarded with that
    worktree 2026-07-11 after the merge — the issue is closed and re-derivable via the recipe.)

**(2) Funnel `borrowSrcCtx` -race (DISTINCT from the hang), ~1/400. → CLOSED (2026-07-11): the
flow-impl merge landed the ctxMeta parent refcount CP (f193c7f;
docs/decisions/ctxmeta-parent-refcount.md, "As implemented"), which pins the borrowed-from meta
so its ctxpool child can never be freed or re-stamped while a reader can still reach it. NOTE
the record's own caveat: the original race was never reproduced empirically even on the pre-fix
base (8,000 targeted runs) — the fix argument is structural, not observed before/after. Pre-fix
status kept for the record: STILL OPEN / UNCONFIRMED on combiner (2026-07-06)** — did NOT surface in the
1200+-check flush-heavy -race recipe run above
(0 DATA RACE), but that exercises TestBySimulation, not the flow-specific tests where flow-impl saw
it ~1/400 — so NOT disproven, just not reproduced here. The funnel.go borrowSrcCtx lifecycle
(Execute stashes c.borrowSrcCtx before the publishing handoff; Run reads it once at the top before
c.mu — funnel.go:421/447) exists identically on combiner, so the race is plausibly live; needs a
targeted repro (raise funnel-flush churn + ctxpool child reuse pressure) to confirm. NOT obviously
covered by 5574a40 (different mechanism: scheduler ctxpool-child Free racing Run's metaFromContext
read of the borrowed src ctx). Seen ~1/400 in
TestFlowDefinitionalFollowUp AND the broad flow suite (2026-07-05): `funnelInstance.Run` →
`borrowBodyContext` → `metaFromContext` READS a ctxpool child's value while an execpool worker
`ctxpool.(*child).Free()` WRITES it — use-after-free of the flush's scheduler ctx
(borrowSrcCtx). Root: funnel.go borrowSrcCtx lifecycle — the Run→body handoff does not
happen-before the scheduler freeing the ctxpool child. Attribution: the identical
non-definitional TestFlowFollowUpAnonymous exhibits the same pattern; flow code touches no
funnel.go. NOTE: flow-impl's 2026-07-06 R6b gate (700 TestBySimulation -race checks +
flow/coalesce -race ×20) did NOT surface it — may be very rare or affected by 5574a40; treat
as unconfirmed-post-fix.

**►►► WEIGHTED SURFACE — LAYER 1 LANDED (2026-07-05).** The weighted/plain limiter split +
weigher + w≥2 dispatch, single-limiter (multi-composition deferred). DESIGN REVISIONS this
session (recorded in weighted-acquisition.md): (1) plain and weighted limiters are FULLY
SEPARATE — no cross-assign either direction (dropped "weighted usable weight-1 in
WithLimits"); weight-1-on-weighted is explicit `NewWeightLimiter(wl, func(T)int{return 1})`.
So `Limiter` STAYS a concrete struct (no interface-ification; hot path & allocs unchanged).
(2) `WeightedLimiter` is an INTERFACE from the start (sealed via unexported `weightedPool()`;
concrete `*weightedSemaphore` — pointer-in-interface, no box); coexists with `WeightLimiter[T]`
(the limiter+weigher binding). (3) CORRECTION: `evaluateOverdraft` treats nil policy as
GRANT (not wait) — so plain semaphore KEEPS its explicit `Overdraft→(false,nil)`=wait; the
"shed to nil for the fast path" is a perf optimization DEFERRED to the walk-avoidance/
meta-redirect seam (nil→grant unsafe pre-step-4). Weighted policy: paused(cap0)→wait,
oversized(w>cap)→REFUSE `ErrWeightExceedsCapacity` (refuse is safe — never grants).
DISSOLVED `OpOption`/opoption.go → builder methods `Launcher[T].WithLimits(...Limiter)` /
`.WithWeightLimits(...WeightLimiter[T])`, `Funnel[T].WithLimits` (plain only — a funnel body
runs over an accumulated instance, no per-value weigher); constructors dropped `opts ...`;
migrated all call sites (main+psgwf+otpsg+streamgrpc+bench+tests). `heldPermit.weight` fed
from `weigh(value)` at dispatch (newScatterWork), else 1; `acquire` presents it.
Files: weightedlimiter.go (+ _internal_test/_test), errs.go, launcher.go, funnel.go,
resequencer.go, permithandle.go. Verified: weighted pool-level + end-to-end serialization
tests green; -short -race + permits -race green; sim -race 18 runs (~2700 cases) clean.
DEFERRED to Layer 2: sets (LimiterSet/WeightLimiterSet) + multi-limiter AND-composition
(they ride on unbuilt joint-acquisition core); binding >1 limiter panics as today. Then the
consumable pass (step 4). The sim still dispatches w=1 plain only — wiring weighted limiters
INTO the sim config to exercise w≥2 under simulation is a follow-on.


**►►► SIM `-race` LIFECYCLE BUG FOUND + FIXED (2026-07-05).** A `TestBySimulation -race`
batch hung (9m50s timeout) and, run in a loop, ~1/30 tripped the race detector: a
use-after-recycle of a permit `Cache`. Root cause: `SkimAll`'s suspend path
(`suspendHeldPermit`→`suspend`→`Cache.SuspendDriver`) is the ONE `ensureCache` caller that
targets the wave it is DRIVING TO DONE (all others dispatch INTO a wave the caller keeps
Open by construction). `ensureCache`/`cacheFor` return an UNPINNED cache (valid only under
the "wv still Open" invariant); that wave can reach Done concurrently → `releaseCaches` →
`ReleaseRef`→`destroy`→`omnipool.Put`→`Cache.Reset` nils `c.pool` (plain write) and
recycles the node, WHILE `SuspendDriver` does its `refs.Add(1)`/reads `c.pool`. Under
`-race` the detector fires on the recycled fields; without it the corrupted/`nil`-pool
cache is the missed-wake wedge (145 goroutines parked in `rdvq` handoff, no senders). FIX
(permithandle.go `suspendHeldPermit`): bracket the `ensureCache`+`SuspendDriver` setup with
`wv.state.IncrementReference()`/`defer DecrementReference()` — the SAME guard
`Wave.sweepFunnels` uses — so wv can't reach Done during setup; the cache's self-ref
outlives `SuspendDriver`'s pin, after which the cache survives on that pin (dropped by the
matching `ResumeDriver`) independent of wv's Done. `IsDone()` after the increment resolves
the lost-race case → no-op the suspend (nothing to drive; don't resurrect a dead wave's
forest). No `permits` change. Verified: audited all 3 `ensureCache` callers + the sole
`SuspendDriver` path (no sibling instances); [re-verify sweep pending]. The stale Jul-4
`pol_sim1.log` hang was the same defect's non-race face; `dump_1.log` (Jul-5) was NOT a new
bug — it is the pre-fix capture of the d2ec3e8 leak (failfile 10:53 predates the 11:15 fix;
now 440k model iters clean). Both prior scratchpad threads resolved.

**►►► VALIDATION-FIRST (before step 3/4/walk-avoidance impl): harden the weighted core.
Item 1 DONE + FOUND A REAL BUG (2026-07-05, d2ec3e8).** Built TestOverdraftEpisodeModel —
a GRANT-mode rapid model (the promise-mode TestPermitsModel never exercises grants/
episodes). Asserts the episode invariants every op (Σ excess + allowance == total;
conservation; ΣinUse ≤ cap + grant, folded into checkEpisode). It immediately caught an
overdraft ALLOWANCE LEAK: an exempt claimant under a standing episode ran the ordinary
w=1 gather (acquireInto), whose STEAL deposits real `held` into an excess-carrying cache,
covering overdraft WITHOUT refunding the allowance → Σ excess + allowance drifts below
total (grant capacity silently lost). This VIOLATED the recorded "descendants never
gather" rule (resolution (b) below). FIX: under a standing episode an exempt acquirer no
longer steals — up-walk inherits the parked hoard in place, then takes REAL free Resource
capacity (checkout, no excess) or claims from the allowance; the head's own gather
(headGather.acquireInto) is unaffected. Validated: episode model 20k×3 clean (was failing
in 1-2 runs), full gate + sim -race 20/20 regression. NOTE: the bug was UNREACHABLE in
production/sim (w=1-only, no episodes) — only weighted dispatch (step 4) would hit it, so
finding it now (cheap 3-line permits repro) vindicates validate-before-extend.
VALIDATION ITEM 2 DONE (09ad848): two adversarial concurrent -race tests —
TestConcurrentEpisodeClaimants (stands an episode deliberately per round — grants need
quiescence, which concurrency kills — then races exempt claimants + owner park/resume +
stranger suspender WITHIN it; oracle = endEpisode's allowance==total assert; measured
~15.7k concurrent excess-creating claims across 150 episodes) and
TestConcurrentOverdraftSuspendChurn (promoteScan churn + suspension counters/ResumeDriver
nudge + steal-vs-forest-mutation + blocking-waiter wedge detection). Both -race x20 clean.
No further bug found (the one real bug was the exempt-gather leak, fixed d2ec3e8). Weighted
core now validated by: sequential promise + grant rapid models (10-20k), concurrent
-race weighted/episode/suspension/churn stress.

**ROADMAP REVISED (PN, 2026-07-05, design session):**
- **`TryAcquireUpTo` DROPPED** — not needed for correctness (a demand only reaches
  overdraft when free < shortfall, so the resource self-accounts its own free to decide
  grant/refuse; a fitting demand never reaches the callback) AND a pessimization (the
  overdrafting outlier gains nothing by taking the free fragment — it just buries
  easily-reachable free-pool capacity as cached-borrowable others must steal back;
  all-or-nothing correctly leaves it in the pool). Recorded in weighted-acquisition.md
  ("Resource partial grants" + Rejected alternatives).
- **Concentration needs NO reclamation machinery** — destroy-drain is the safety net: a
  cache's held returns via steal-pull (alive) + destroy-drain (`destroy`→`counts.drain`
  at inUse==0→`resource.Release`). For overdraft it's exact: episode end IS the
  body-cache destroy, so the outlier's hoard drains back at completion. (Proactive
  shrink of long-lived IDLE cache = the separate narrower `Reclaim(n)`, motivated by
  shrinking capacity, not concentration.)
- **`NotifyAt` DROPPED too — RESOURCE CONTRACT SETTLED** (PN, 2026-07-05, recorded in
  limiter-resource-classes.md §"Resource contract, settled" + weighted-acquisition.md).
  `TryAcquire(n) (bool, error)`: (true,nil)=grant-from-free; (false,nil)="not from free,
  overdraft on the table" (holdable → gather then maybe Overdraft; consumable → wait,
  SELF-ARM the accrual wake — remember the rejected size, arm timer/poll, post Adjust —
  which IS Decision-3's "failed TryAcquire is the demand signal", so NotifyAt is
  redundant); (false,err)=TERMINAL refuse the resource is certain of regardless of the
  gather. `HoldableResource` = +Release +Overdraft(n)(bool,error). Overdraft STAYS as the
  three-way (grant/wait/refuse) because the TRUE ask is only known post-gather (n = w −
  forest-borrowable the gather assembled; the forest is resource-invisible) — the reason
  it can't fold into TryAcquire. CHANNEL-CHOICE RULE: err = ask-independent certain
  refuse (fast-fail); Overdraft = ask-DEPENDENT decision; pick per policy. A resource MAY
  err AND implement Overdraft consistently (e.g. an INSTANCE config-disabled from
  overdraft fast-fails via err while the type keeps the dormant Overdraft method) — the
  sole incoherent case is erring where Overdraft would GRANT. Not structurally enforced;
  benign if tripped (err wins → stricter policy, not a crash).
- **SEQUENCING (PN): surface FIRST (step 3), then ONE consumable pass (step 4).** Step 3
  = the weighted/plain surface + split (lights up the validated holdable core; sim can
  dispatch w≥2). Step 4 = one consumable pass — the consumable resource CLASS
  (limiter-resource-classes.md: pass-through, no caching forest) + a rate limiter, on the
  settled contract (TryAcquire(bool,error) self-arm + Adjust/balance wake), INCLUDING
  weighted consumables — done once AFTER the surface so the class is implemented a single
  time with weighted support from the start. Adding a rate limiter is the framework's
  FIRST consumable; the class is unimplemented today (only the holdable semaphore exists).
NEXT: step 3 (weighted surface + split), then walk-avoidance impl is optional/separable.

**►►► GATHER WALK-AVOIDANCE DESIGNED + enqueue w=1 gate LANDED (2026-07-05) —
docs/decisions/gather-walk-avoidance.md. LANDED (0c0623e): enqueue calls headGather
inline only for w≥2 (a w=1 instant head re-walked redundantly right after its
fast-path steal failed; the caller's confirm drives the single as-head gather).
DESIGNED follow-up (with the split/surface work): a seqlock `changeSeq` (bumped on
release/drain/raise — NOT deposit, which under the barrier only feeds the head's own
loop) gating three cached "don't walk" facts — pool `nothingBorrowableSeq` (coarse,
shared, plain+weighted), per-Demand `notEnoughSeq` (fine, weight-aware, weighted),
per-Cache up-propagated stamp index (weighted-only = the borrowable index; prune
subtrees stamped ≤ nothingSeq; short-circuits so propagation is O(short) amortized).
Bounds walks to O(real capacity changes) pool-wide (barrier ⟹ one gatherer) and the
weighted gather to O(W·changed-paths). Footgun: bump+stamp completeness = a miss is a
silent wedge; concentrate in counts methods + assert. Library framing (PN): bound the
worst case, don't measure a workload. Prereq: enumerate wake/re-drive sites to prove
the bound. NEXT enumeration + implement with step 3/4.**

**►►► WEIGHTED/PLAIN LIMITER SPLIT DESIGNED (2026-07-05) —
weighted-acquisition.md §"The weighted/plain limiter split". PN chose compile-time
enforcement (option ii). Supersedes the "collapse to one Limiter, weigher optional"
framing: weight-CAPABILITY is a limiter property (plain NewSemaphore vs
NewWeightedSemaphore), the weigher stays an (op,limiter) binding. Rationale: weight is
what lets "infeasible" be permanent — plain w=1 infeasible ⟺ paused ⟺ WAIT (nil
overdraft policy ⟹ the headGather nil early-out ⟹ NO walkCounts proof, the fast miss
path that fixes the limit-1 w=1 hot-path concern); weighted can be w>cap ⟺ per-unit
error ⟺ REFUSE (real policy, pays the proof). NewWeightLimiter takes a WeightedLimiter
not a bare Limiter ⟹ weighing a plain semaphore won't COMPILE (closes the silent-wedge
hole). OPEN: WeightedLimiter-vs-WeightLimiter[T] name collision + concrete-vs-interface
(naming pass); oversized default refuse-vs-soft-grant (step-4 policy). walkCounts
optimization: touch can't prune it (touch ≠ counts-changed; unsafe for anyBorrowable) —
fold anyInUse into searchList's existing walk instead (deferred, weighted-path only).
Implement with step 4. NEXT: step 3 (TryAcquireUpTo / NotifyAt), then step 4 (surface +
the split; the "flip semaphore overdraft policy" item below is SUBSUMED by the split —
plain sheds Overdraft entirely, weighted implements paused→wait/oversized→refuse).**

**►►► W2d REVIEW FOLLOW-UPS LANDED (2026-07-04) — PN's 7-point response
applied. Step 3 (TryAcquireUpTo / NotifyAt), then step 4 (surface + the limiter split
above).**
- **(1)+(5) Overdraft evaluation now UNIFORM across weights** (w≥2 gate removed —
  PN: not wrong, just inefficient; and the proof means w=1 reaches policy only at
  zero capacity). Made sound by TWO hardenings found via a biased-sim hang hunt
  (zero-SelfTime bias per the sim-trace-debugging skill; pre-fix ~5%/check, post-fix
  0/300):
  (a) **Proof-premise re-establishment in headGather**: gather-exhaustion / zero-inUse
  walk / Resource refusal were separate snapshots — capacity moving between them (a
  steal mid-transfer; commonly a destroy draining held back to the Resource's
  walk-INVISIBLE free pool) let policy be consulted while capacity was right there.
  Now a loop: gather → walkCounts (anyInUse ⇒ wait; borrowable-elsewhere ⇒ re-gather)
  → re-TryAcquire(shortfall) LAST (finish gather on success) → only a truly dry
  forest reaches policy.
  (b) **semaphoreResource.Overdraft = "not now" for every weight UNTIL STEP 4**:
  "not now" is the middle outcome (granted=false, err=nil) — keep waiting, no grant,
  no failure, and (PN correction) NO commitment: it is NOT a "promise", a later call
  can refuse; the resource only owes a future wake. Cannot grant yet because an
  episode owner's downstream dispatches are its causal subtree — exempt by design,
  but UNREPRESENTABLE until step 4's meta-redirect, so pre-step-4 they gate behind
  the owner's own episode = structural self-wedge (the actual sim hang: grants were
  COMMON, 72/120 biased iterations, mostly benign until the owner blocked on gated
  downstream work). STEP-4 POLICY IS OPEN (de-attributed — I over-claimed a
  "ratified two-sided policy"): only "not now while paused" is clear; grant vs
  refuse vs not-now for an oversized demand on a fixed ceiling is a step-4 call,
  decided with the memory limiter + weigher-error path.
  ALSO FOUND: latent W2a-era weight bug — semaphoreResource.TryAcquire(n) ignored n
  for bounded limits ("n is always 1" shortcut); fixed with InFlightCounter.
  AddIfUnder(n, limit) (atomic all-or-nothing).
- **(4) The 2026-07-04 lost-output sim FAIL**: PN attributes to a concurrent session
  killing tests indiscriminately; plausible and consistent (pre-W2d code lacked the
  uniform evaluation, so today's hang class was unreachable there). Closed.
- **(6) Reclaim-refusal direction (PN)**: panic to abort the driving body we can no
  longer resume, treated as a special case — the framework catches ONLY that typed
  sentinel at the dispatch boundary and returns the error as though the body returned
  it. Not general recovery (the never-recover stance covers USER panics; this is
  framework non-local exit, encoding/json-style). Alternative (return-and-continue
  unpermitted) rejected: silently violates the limiter contract. Implement at step 4
  with the weighted surface.
- **(7) Tests converted to NewDemand()** (~63 sites); Invalidate calls kept where they
  exercise the public API; modelUnit keeps the embedded-demand host pattern (mirrors
  heldPermit, documented in Demand.Init).
- (2) p50-for-tail trade ratified; (3) PN reviewed promoteScan by eye (2026-07-05) —
  APPROVED, complexity acknowledged and accepted (enqueue/promoteScan/retireHead/
  Invalidate + the headGather proof loop). No review items open on W2d.
- Gate: vet, lint 0, full -short, permits -race, rapid 10k, biased-sim 0/300,
  stock sim -race batch, root -race.

**Previous banner:**

**►►► CP-W2d LANDED (2026-07-04) — queue unification implemented per
weighted-acquisition.md "Queue unification". NEXT: weighted-acquisition step 3
(TryAcquireUpTo / NotifyAt), then step 4 (surface).** As landed:
- Pool: `queue nbcq.Queue[demandEntry]` (entry = {d, gen} — gen-stale entries are the
  lazy interior removal) + `head atomic.Pointer[Demand]` slot + `promoting` marker
  demand (exclusive pop right; readers see armed-non-exempt; wake() drops marker-window
  events — compensated). fifoMu/fifo/barrier DELETED; pool general notifier DELETED
  from permits (Release wakes the head slot or NOBODY; promotion cascade = the chain);
  episode extension serialization + od.total moved to od.mu inside the pooled object;
  initial-grant evaluation needs no lock (slot ownership = sole evaluator; od
  initialized unpublished).
- Protocol: fast path = bare head load (nil ⇒ today's lock-free machinery verbatim);
  miss/gated ⇒ enqueue (EVERY weight; body cache uniform) + promote-if-slot-nil
  (CAS nil→marker → scan); retirement (satisfy/refuse/invalidate/endEpisode) = CAS
  self→marker → scan (pop, skip gen-stale, Store head, post-install gen re-check with
  marker-swap reclaim vs racing Invalidate — exactly one party continues the scan; empty
  ⇒ Store nil + Empty re-check + re-close). Invalidate of a queued non-head is lazy
  (gen bump only). Parks AND postpones ride the demand mailbox: Cache.WaitersFor/
  ListenersFor(d) resolve mailbox-vs-episode-claimants; heldPermit gateAcquire/
  blockAcquire/reclaim re-plumbed; AcquireWait's transient no-target case loops (next
  Acquire enqueues).
- **REFINEMENT (caught by TestSemaphoreResource_ZeroBlocksAll): initial overdraft
  evaluation stays w≥2** — a w=1 head reaching default-GRANT let Semaphore(0) admit
  past a zero limit; w=1 exhaustion is always "capacity is zero right now" (raisable ⇒
  wait; ChainProbe reaches the head), never structural infeasibility. Episode
  extensions still cover w=1 claimants (wedge argument). Recorded in the doc's
  Implementation notes.
- Sequencing note: satisfied w=1 permits now back from the demand's BODY CACHE, so
  Invalidate-before-Release trips destroy's inUse tripwire (caught one test doing it);
  callers already sequenced correctly via heldPermit.
- Tests: barrier_test → head-slot semantics (w=1 queues in arrival order; promotion
  order assertions updated incl. lazy-stale-skip coverage); model oracle → "a miss
  always leaves a head standing; legitimacy checked when we ARE the head"; concurrent
  tests gained Invalidate hygiene (a final miss leaves the demand queued; the churn
  test re-homes per iteration).
- **Gate: vet, lint 0, full -short, permits -race ×10, rapid 10k, 41/41
  TestBySimulation -race (chunks of 10, full capture), full root -race ×1.
  BENCHMARKS (bench/BenchmarkDispatch heavytail, medians of 5, before=W2c):
  underload/balanced noise; overload p99-e2e −34%; heavy-overload p99-e2e −63%,
  p99.9-e2e −51%, tasks/sec +6.6%; p50-e2e +5–8% under overload (fairness
  redistribution — accepted). Tail-first priorities: clear win.**

**Previous banner:**

**►►► QUEUE UNIFICATION DESIGN RECORDED (2026-07-04) — implement as
CP-W2d (weighted-acquisition.md "Queue unification"; supersedes Decision 2's
representation + ALL of Decision 3).** Converged in the PN review thread, docs-first
by agreement. Core: ONE always-on lock-free demand FIFO (nbcq) for EVERY weight + a
CAS-managed head SLOT (the field formerly named barrier); fast path = a BARE LOAD of
the slot (PN — no CAS on the hot path), nil ⇒ ordinary lock-free acquire, success never
touches the queue (pure-w=1 pools keep today's machinery verbatim — Decision 3's
dormancy WITHOUT the exclusion rule); miss/occupied ⇒ enqueue + promote-if-slot-nil;
head retirement = pop-next (gen-stale entries skipped — lazy interior removal, nbcq
can't unlink; Decision 4's gen discipline now load-bearing) + ONE CAS self→successor
(no empty-slot window while waiters exist ⇒ ~zero sniping against queued demands) +
mailbox wake. Wake delivery serves parks AND postpones (mailbox = full notifier;
listeners half for the manager-postpone path — PN's "can't assume mailbox parking").
COLLAPSES: arming/disarm as concepts (armed ⇔ slot non-nil; no flush), fifoMu (episode
extension serialization moves to a small mutex inside the pooled od), the pool general
waiter/listener set on the permits path (Release wakes the head slot or NOBODY; the
promotion cascade IS the chain — W2b-i probe rules stay for workq consumers only).
LAYERS UNCHANGED: episodes (sentinel in the slot), suspension counters, exemption
anchor, head-only gathering, Decision 1. Trade recorded: while a head stands, each
contended admission pays one wake handoff instead of a snipe — expect BETTER P99/max,
possible peak-throughput cost; MEASURE per bench methodology (heavy-tailed blocking
I/O, tail metrics primary, P:D sweeps) before/after. Impl notes: w=1 head's body cache
optional (uniform acceptable); blockAcquire/reclaim park targets move to the demand
mailbox. Gate for W2d when implemented: the usual + the full 40× sim batch (this is a
wake-path rewrite) + before/after benchmarks.

**Previous banner:**

**►►► W2c REVIEW RESPONSE LANDED (2026-07-04) — PN's data-structure review
comments applied; overdraft state now DEMAND-ALLOCATED (PN follow-up during the session).**
- Pool.notify → **notifier**; cachePool → package-level var (heldPermitPool precedent);
  ListenersFor → **Listeners** (+ recorded WHY Listeners/Waiters stay separate and the
  Notifier is NOT exposed: they are the register/park side; every notify entry must route
  through the Pool — wake/ChainProbe — or it would bypass barrier/episode routing).
- **Overdraft state = pooled `overdraft` object behind `Pool.od atomic.Pointer[overdraft]`**
  (PN: demand-allocated, fifoMu-protected, reached through an atomic.Pointer): a Pool
  carries no episode state (and pays no claimants-notifier Init) until a grant installs
  one; retired to the omnipool at endEpisode. Write side under fifoMu (the install swaps
  the sentinel into fifo[0], so the FIFO lock is the natural guard — episodeMu DELETED;
  evaluations now serialize under fifoMu, whole grant = ONE lock section, lock order
  fifoMu → list locks, policy call under fifoMu documented no-callback). Lock-free readers
  (wake routing, excess return, claimant parks) are safe by the STRUCTURAL PIN: every such
  reader lives inside the episode subtree whose cache refs pin the anchor, and episode end
  IS the anchor's destroy — a live reader ⇒ un-recycled episode. Stale barrier readers
  (wake across an end) classify sentinels by an immutable `Demand.sentinel` flag and
  re-load p.od instead of dereferencing the stale pointer; nil ⇒ drop (same compensated
  class as the stale-head empty-mailbox drop). overdraftPolicy stays a Pool field
  (nil-field test per limiter-resource-classes.md — replaces the method-value closure,
  PN comment); pool.suspended stays a Pool field (suspension is drive attribution, not
  episode state; maintained unarmed too).
- **Demand: gen-only-size note struck (stale); mailboxReady lazy flag DELETED — omnipool
  Initer convention instead** (PN): Demand.Init (mailbox, once per object) / Reset
  (Invalidate) / NewDemand / Free + package demandPool; heldPermit keeps its BY-VALUE
  embedded demand and gains Init(){demand.Init()} (omnipool calls it on fresh handles) —
  no per-dispatch pool traffic added. Zero-value Demand is NO LONGER READY. Tests kept
  stack demands + explicit d.Init() (minimal churn); TestDemandPoolRoundTrip covers the
  pooled path.
- ANSWER-ONLY (PN to decide, not implemented): (a) w=1 queuing under an armed barrier —
  current miss-don't-register is recorded Decision 3; queuing w=1 would give strict
  cross-class arrival FIFO but puts registration (body-cache alloc + FIFO + mailbox) on
  the hot class and serializes multi-permit admission through head succession; today's
  cost is w=1 can be starved while a w≥2 FIFO stays non-empty. (b) Resource()
  type-assertion smell — only caller is SetMaxConcurrency; root fix is a typed handle
  kept by the constructor (or a distinct Semaphore type with the method), natural to fold
  into step 4's surface work; Pool.Resource() then dies.
- Gate: vet, lint 0, full -short (one hit of the KNOWN psgwf Example_clientTimeout
  real-clock flake under parallel load, 20/20 standalone), permits -race ×10+, rapid 10k,
  root -race, sim -race: **one UNEXPLAINED TestBySimulation -race chunk failure whose
  output was lost (only the FAIL line captured), then 40/40 consecutive green on
  identical reruns + no rapid failfile written ⇒ NOT a property failure (panic, race
  report, or binary timeout; another session was running concurrent -race sim batches on
  this machine — timeout under load is the benign candidate). Flagged, unresolved; if a
  sim FAIL recurs, capture full output first.**

**Previous banner:**

**►►► CP-W2c LANDED (2026-07-03h) — overdraft per weighted-acquisition.md
§Overdraft + resolutions (a)/(b)/(c). NEXT: weighted-acquisition step 3 (TryAcquireUpTo /
NotifyAt resource capabilities), then step 4 (surface builders + sets + opoption removal +
meta-redirect wiring).** Gate: vet, lint 0 issues, full -short, permits -race ×5 (incl the
over-subscribed canary + new overdraft suite), rapid 10k, 40/40 TestBySimulation -race in
foreground chunks. Design-to-implementation resolutions made this session (implementation
detail, not design changes — flag on review if any smells):
- **Episode anchor = pool-owned sentinel Demand installed at fifo[0] on grant** (barrier →
  `&p.episode`): "head stands until completion" without aliasing caller-reused demand
  storage — the CALLER's demand dequeues normally at grant (its d.cache persists as home
  AND as `p.episode.cache`/`episodeCache`, the exemption anchor); arrivals queue behind the
  sentinel so no successor gathers mid-episode; `Invalidate` at completion just drops the
  home ref, and the anchor cache's destroy (refs==0: body exited + subtree drained + all
  suspensions resumed) runs `endEpisode` — allowance-home assert, sentinel dequeue,
  promotion/disarm. Episodes are STRICTLY serial by construction.
- **Third park set `episodeNotify`**: while the episode STANDS, armed capacity events route
  there (the satisfied owner consumes no mailbox wakes; the actionable consumers are exempt
  claimants); exempt claimants park there in AcquireWait (three-way park-target switch:
  registered → own mailbox, standing-episode-exempt → episodeNotify, else general set).
  The chained bit rides through. Liveness is structural: a parked claimant's cache
  ref-pins the anchor, so episodeNotify can never strand a waiter across an episode end
  (episode end IS the anchor's destroy).
- **Exemption = chain-through-anchor OR home==anchor** (the resuming owner acquires from
  the anchor's PARENT, so the chain test alone misses it). Exempt claimants under a
  standing episode NEVER register (queueing behind the episode would deadlock its own
  drain) — they claim from the allowance (`counts.occupyTaking`) and extend.
- **Claim lands on `bestClaimCache`** — the chain cache (claimant → anchor, inclusive)
  needing the least allowance: the design's "lent capacity plus remaining allowance"
  honored as inherit-in-place, allowance-topped (first cut claimed on the claimant's own
  cache and over-asked the extension by the parked hoard it could have borrowed — caught
  by the counting-resource test). Per-cache all-or-nothing granularity stays (one Permit,
  one backing).
- **Stranger check anchored at the EVALUATOR's chain** (its claim cache → root), not just
  the head's: in-subtree suspended drivers on the claimant's own chain are causally inside
  it — chain-only-from-head would wedge an extension needed by the very drive a suspended
  ancestor is parked in. Off-chain-but-in-subtree (sibling branch) suspensions read as
  strangers — conservative, resolves at their resume (causally before episode end).
- **`Cache.Acquire` → `(Permit, error)`** (miss = zero Permit + nil error): the refusal
  channel the spec requires; AcquireWait invalidates on refusal; heldPermit latches
  `acquireErr` (sticky-terminal, stops all parking; gateAcquire/blockAcquire surface it;
  reclaim's refusal path leaves un-acquired like cancellation — REVISIT error surfacing
  with step 4; unreachable at w=1 until then).
- **`Demand.cache` → atomic.Pointer[Cache]** — the canary CAUGHT (first -race run) a
  latent W2b-era race: exemption readers reach hd.cache lock-free through a stale barrier
  pointer while the owner's post-satisfaction Invalidate writes it. Values stay
  benign-stale by doctrine; the ACCESS is now coherent. (Also cleanly covers the
  sentinel's anchor set/clear.)
- **Suspension counters (c) wired end-to-end**: `Cache.SuspendDriver/ResumeDriver`
  (ref-pinned target; counters bumped BEFORE the permit frees; resume decrements BEFORE
  reacquire + nudges via wake(true) when armed; destroy panics on a nonzero counter =
  bracket tripwire). streampool: heldPermit.suspend(target) with target =
  `wv.ensureCache(meta, h.pool())` at all four suspend sites (Skim / block / SkimAll /
  ExecuteNowOrQueue) — ensureCacheChain IS the mkdir-p of resolution (c).
- **Known over-ask — RESOLVED as a non-issue (PN, 2026-07-05), not a step-3 item.** The
  head's gather can't harvest Resource free-capacity smaller than the shortfall
  (all-or-nothing TryAcquire), so the overdraft ask can name more than the true net need
  when free permits sit stranded. But this is HARMLESS: the resource self-accounts its
  own free in the grant/refuse decision (a demand that would fit never reaches
  overdraft), so no wrong decision; and the stranded free stays in the pool where
  locality is BEST (TryAcquireUpTo would bury it — see the roadmap above, DROPPED). The
  looser `total` self-corrects at episode end. Borrowable fragmented across chain caches
  is likewise fine (descendants never gather — the exempt path claims from the allowance
  and the lifecycle drains it).
- **Test posture**: the shared test `semaphore` is now wrapped by `waitingResource`
  (Overdraft = always "not now") so every W2b-era test keeps its blocking semantics and
  oracles; the bare semaphore (default-GRANT) + counting/refusing resources live in
  overdraft_test.go (grant arc incl. extension + owner park/resume round-trip; refusal
  through Acquire and AcquireWait; proof gating on inUse; stranger block/resume;
  episodeNotify wake delivery; serialized concurrent episodes under -race). The rapid
  model stays "not now" mode (blocked-legitimacy oracle unchanged) — a grant-mode model
  with allowance accounting is a possible follow-up, not blocking. Sim: production
  limiters are w=1-only until step 4 ⇒ no registration ⇒ no episodes ⇒ pure regression.
- streampool `semaphoreResource` does NOT implement Overdraft ⇒ default-grant — inert
  until step 4 dispatches w≥2; decide per-limiter policy (grant vs refuse vs promise via
  NotifyAt) when the surface lands.

**►►► CP-W2b-ii LANDED (2026-07-03g) — its design pickup was: CP-W2c (overdraft; design settled —
see resolutions (a)/(b)/(c) + episode/allowance/standing-head spec in weighted-acquisition.md
§Overdraft; suspension counters per (c); wrinkles 4/5 resolved as body-cache episode end +
subtree exemption).** Gate: vet, lint, -short 11/11, permits -race incl the over-subscribed
canary ON the mailbox routing, rapid 10k, **40/40 TestBySimulation -race** (run in foreground
chunks — background batch shells were being externally reaped mid-run this session; 15
additional clean iterations from the two reaped partials). CP-W2b-i (wake chain) landed
1f27117 (negative-controlled chain tests). W2b-ii as landed:
- ZERO BROADCASTS: armed release/drain/raise/probe → the HEAD'S OWN MAILBOX via wake()'s
  barrier load (barrier is now atomic.Pointer[Demand], == fifo[0], nil iff empty; head's
  cache+mailbox are written before the publishing Store; a wake dropped into a stale head's
  empty mailbox during a transition is compensated — release decrements counts BEFORE the
  stale load, promotion happens-after, and the successor's park-time confirm re-reads counts);
  PROMOTION → one wake to the successor's mailbox (barrierPassed, outside fifoMu); DISARM →
  one chained seed to the general set (the freed uncontested capacity is a multi-permit event
  for the gated crowd). ChainProbe routes through wake(true), so armed probes reach the head.
- Each registered Demand carries a lazily-Init'd rdvq.Notifier mailbox (mailboxReady under
  fifoMu; Init BEFORE barrier publish). Registered demands park on their OWN mailbox —
  AcquireWait picks its park target by d.pool.Load() each iteration (registration happens
  inside Acquire; register-then-confirm covers the promotion-before-park race).
- W2b decisions carried over from the stash: registered ⇒ backing = body cache; d.cache
  persists across satisfied episodes (step-0 own-home occupy on resume; panic on re-home
  without Invalidate); satisfaction keeps the body-cache ref with the demand (destroy at
  dequeue would panic on inUse=w; W2c standing head builds on this); unregistered w≥2 =
  steps 1-2 + whole-grant TryAcquire(w) ONLY (atomic, not gathering; no single-victim steal —
  partials would strand or freelance-deposit), miss ⇒ register (uncontended w≥2 satisfies in
  one call: register→instant head→gather→dequeue→disarm); w=1 armed non-exempt fails WITHOUT
  registering; re-presented weight must equal the stamp (panic); stranger-free stash bits
  (barrier_test.go, export_test forest-walk oracles, weighted rapid model with per-unit
  demands + armed-legitimacy predicate now comparing &u.demand) applied nearly clean.
  Stash "w2b-barrier-pre-chain" can be DROPPED once W2b-ii commits (belt copy:
  scratchpad/w2b_barrier_wip.diff).

**WAKE DESIGN SETTLED (PN, 2026-07-03f, after two pushes on my WakeAll band-aids):**
- **Finding 1 (cost one wedged stress test): waiter-style rdvq Forward is TERMINAL** on the
  premise "failed retry ⇒ capacity already taken" — TRUE unarmed, FALSE under an armed barrier
  (capacity sits borrowable but gated; only the head can act). A gated waiter consuming a
  release wake drops it ⇒ head starves ⇒ wedge (TestConcurrentWeightedOverSubscribed, 6/6
  workers timed out). My fix #1 (armed release ⇒ NotifyAll) REJECTED: herd per armed release
  (armed TRANSITIONS are cold; armed RELEASES are not), and broadcast-to-find-a-known-recipient
  is the band-aid shape.
- **Finding 2: promotion broadcast unnecessary** — the promoted head is a KNOWN single party.
  Endpoint: **per-demand mailboxes** (each REGISTERED demand parks on its own notifier; armed
  release → head's mailbox; promotion → successor's mailbox; registered demands never park on
  the general pool set). Single-consumer + register-then-confirm ⇒ no lost wakes, no Forward
  duty, terminal-Forward premise never violated. Missed-wake closure: Release decrements counts
  BEFORE waking, so an undelivered head wake means the head's in-flight/confirm gather already
  sees the capacity in counts. [CP-W2b-ii]
- **Finding 3: disarm = destroy-drain = SetMaxConcurrency-raise = the SAME multi-permit
  under-notify class** (k waiters satisfiable, wake-one strands k−1 on borrowable capacity),
  which limiter-resource-classes.md ALREADY sentences to the chain ("No WakeAll anywhere").
  **NEW BUG (W2a-vintage, latent until step 4): WEIGHTED RELEASE under-notifies** — release(w=3)
  frees 3, wakes one, two waiters sleep over borrowable permits. So chain rule 2 is WEIGHTED
  CORRECTNESS, not the deferred "cleanliness" unification.
- **CP-W2b-i scope — the pool-internal chain, WakeAll deleted from permits**: seed = ONE wake
  per multi-permit event (destroy-drain held>0, capacity raise; disarm arrives with W2b-ii);
  consumer discipline becomes THREE-WAY: probe-SUCCEEDED while holding a received wake → emit
  exactly one fresh probe (rule 2; only wake-triggered successes — a never-parked acquirer is
  not a chain member); probe-FAILED → stop, no forward (rule 3 — safe unarmed: capacity truly
  gone); NO-PROBE (stale postpone listener whose work already ran) → Forward as today (renotify
  conservation — the stale-listener hang fix is PRESERVED, distinct from probe-failure).
  Pool-internal chain needs NO balance ledger (capacity is counts-visible; register-then-confirm
  covers undelivered seeds — the ledger is for resource-side invisible accrual, step 3). Probe
  emission needs no rdvq surgery: waiters (AcquireWait) know their Pool; listener-side works
  (limiterScatterWork/gateAcquire) know h.pool() — emit via a Pool chain-probe method. Chain
  must hold across BOTH consumer classes sharing pool.notify (a listener that consumes a chain
  wake productively and doesn't forward breaks it) ⇒ touches the eee5322 Notification consumer
  sites (workq controller / streampool) — sequenced FIRST per foundation-first, own sim gate.
- **W2b-i SITE MAP (code-read findings, 2026-07-03f):** the wake carries a CHAINED BIT
  ("announces possibly more than one consumer's worth" — release(w>1), destroy-drain(held>1),
  capacity raise, later disarm; plain release(1) unflagged so the w=1 hot path gains zero probe
  churn; a bare bit suffices — probes re-propagate it and the chain dies at the first failed
  probe, ≤1 dead wake per chain). RULE-2 HOOKS: (a) workq controller.starting() — the exact
  existing "productively used the saved notification" seam (it clears c.notification there);
  probe BEFORE clearing when chained. (b) permits.AcquireWait / streampool blockAcquire /
  heldPermit.reclaim — loop-exit success with m.Received() && chained → probe at the pool the
  site already knows. RULE 3: accepted.go:803's Forward-on-unused STAYS (the controller cannot
  tell a permit-probe failure from a governor/queue-space postpone — forwarding is conservative,
  never loses a wake, terminates at a waiter-style terminal); the strict stop applies only at
  permits-side loops where the probe is unambiguously the pool acquire — and W2a's AcquireWait
  (discard-on-failure) is ALREADY rule-3-compliant. postponedWorkWasExecuted (accepted.go:400)
  appears write-never — vestigial, check+strip in passing. PLUMBING CONFIRMED (rdvq read):
  Waiters.Deliver passes the Notification VALUE through ("preserving re-circulation identity")
  ⇒ the chained bit rides the listener→queue re-injection for free, and the controller's
  notification keeps n = the PERMITS notifier (Notifier.Notify builds {n: n} for listeners) ⇒
  ProbeOrigin at the controller emits at the right pool. IMPLEMENTATION SHAPE: rdvq —
  Notification gains `chained bool` + `Chained()` + `ProbeOrigin()` (n.NotifyChained(nil) when
  n set; no-op waiter-style — waiter sites probe via the pool they know) + `Notifier.
  NotifyChained(fallback)` (sets the bit in both delivery styles; Notify unchanged). permits —
  wake(chained bool); Release passes weight>1; destroy drain seeds chained iff held>1;
  Pool.WakeAll DELETED, replaced by exported chain-seed for SetMaxConcurrency's
  capacityChangedFn (always chained — cold) + exported Pool probe for streampool sites.
  Consumers — AcquireWait/blockAcquire/reclaim: on success-with-m.Chained() → pool probe;
  controller.starting() postponed case: if c.notification.Chained() → ProbeOrigin() BEFORE the
  clear. Tests — weighted-release under-notify regression (release(w=3) must admit 3 parked
  w=1 AcquireWaiters), destroy-drain multi-wake, chain termination; model untouched
  (sequential).
- Gate discipline per CP unchanged (vet/lint/-short/permits -race/rapid 10k/40× sim -race).

**Step-2 CP sequence (revised): CP-W2a LANDED 698bac4 → CP-W2b-i (wake chain) LANDED this
commit → CP-W2b-ii (FIFO+barrier+mailboxes, from stash) → CP-W2c (overdraft).** Spec:
`docs/decisions/weighted-acquisition.md` + `limiter-resource-classes.md`. Step 1 landed 9e03d83.
**API-FIRST (PN, in memory too): unreleased code — land end-state APIs first (behavior may lag),
NO compat layers, update call sites up front; upgrade tests as behaviors come online**
(permits-level concurrent/-race stress until step 4 gives the sim a weighted surface; sim still
gates every CP as regression). Design-to-implementation resolutions (2026-07-03, PN-confirmed):
- **(a) PER-HEAD BODY CACHE C_B^L, created at REGISTRATION** (w≥2 slow-path miss), child of C_W^L
  via the meta-recorded-cache redirect (ensureCacheChain already resolves sub-wave parents through
  the recorded cache) — the head's sub-waves parent under C_B^L, so episode end = C_B^L destroy
  (refs==0: body exited + sub-waves drained — existing lifecycle verbatim); destroy ⇒ inUse==0 ⇒
  "allowance necessarily home at completion" is STRUCTURAL. Gather hoards in C_B^L (clean own-held
  accounting, no sibling blur); invalidated hoard returns via the existing destroy/drain path.
  Ties the episode to the head BODY, not its possibly-long-lived wave (PN's requirement).
- **(b) ONE exemption rule, both phases**: while armed, an acquire proceeds iff its cache chain
  passes through the head's C_B^L. Gather phase degenerates to head-only (nothing under C_B^L
  yet); episode phase exempts exactly the causal subtree; siblings/ancestors/arrivals gated
  throughout (gated parties hold nothing inUse ⇒ liveness induction intact). Descendants NEVER
  gather — allowance fungibility (inUse past held) + episode extension substitute, so head-only
  gathering holds even mid-episode. Cost: unarmed = one nil load at Acquire entry; armed =
  piggybacks on the up-walk.
- **(c) DRIVE-TARGET SUSPENSION COUNTERS**: suspend bumps pool.suspended + suspendedDrivers on the
  drive-target wave's cache (mkdir-p a pass-through node if absent — impl detail vs a nil-bucket);
  reclaim decrements; same-goroutine bracketing. Stranger ⇔ pool.suspended ≠ Σ suspendedDrivers
  along the head's chain (exact: ensureCacheChain's mkdir-p guarantees true drivers have on-chain
  targets; catches parked siblings of on-chain ancestor waves driving L-less sub-waves, invisible
  to any forest walk). **Stranger present ⇒ NO overdraft — wait for the suspension to END**: cancel
  (→ full release) or resume (reclaim decrements, parks at the barrier as VISIBLE queued demand —
  grant may then proceed; requiring full release would wedge, since the barrier gates the
  stranger's reacquire). Sticky-head+FIFO ⇒ the head doesn't starve meanwhile (PN). Reclaim-side
  decrement must NUDGE the parked head to re-evaluate. Accepted consequence: a stranger whose drain
  itself needs this limiter never clears ⇒ head resolves only by invalidation deadline
  (fairness-over-head-progress, chosen). Model observable: suspended == Σ suspendedDrivers.
- **Steal of suspended permits STAYS GLOBAL** (PN probed, convinced): scoped lending = hold-and-wait
  by parked drivers (2-limiter parked-driver cycle deadlocks with no running body) + "whose token"
  unrepresentable (lent permits are fungible in the shared ancestor cache) + it is supply-side
  reservation redux (already rejected). (c) is the compensating fairness rule — two halves of one
  deal. Nice-to-have (NOT step 2): suspend-touches-hot so the coldest-first steal prefers old
  residue over fresh suspensions.
- **CP-W2a LANDED (this commit)**: end-state API — `Demand` (caller-held gen-stamped identity;
  registration behavior lands W2b), `Cache.Acquire(d, w)` / `AcquireWait(ctx, d, w)` (cancel
  invalidates d), heldPermit gains a demand (Reset went field-wise: the atomic must not be
  copied); `counts.stealOutUpTo` + `deposit` + `depositOccupy`; multi-source gather in
  acquireInto (hoard-retaining miss = cache-don't-return rollback; Resource arm all-or-nothing
  at the shortfall until step 3's TryAcquireUpTo); searchList gains `exclude` (gatherer never
  steals from itself). Model: weighted units (w ∈ 1..cap+1), per-unit demands, weight-aware
  blocked-legitimacy oracle (blocked ⟹ Σ borrowable anywhere + resource-free < w), 10k checks;
  new unit tests (fragment assembly, hoard retention incl. the stranded-free-permit
  all-or-nothing subtlety, deposit/partial-steal); weighted concurrent -race stress in an
  always-satisfiable mix ONLY (over-subscribed weighted fairness doesn't exist until the W2b
  barrier — add that stress WITH the barrier). Gate: vet/lint/pre-commit/full -short/permits
  -race/rapid 10k/**40×40 TestBySimulation -race clean**.
  **LIVENESS LESSON (cost one hang, 1/25 in the first batch): a completing transfer must never
  transit a stealable state.** First cut deposited a steal borrowable-then-occupied (two CASs);
  the old stealOut→checkout pair kept the permit HIDDEN mid-transfer. Exposed, two w=1 acquirers
  can bounce one permit between caches forever without parking — under the sim's virtual clock
  that's a hang, not just unfairness. Fix: `depositOccupy(n, w)` — deposit + occupy-if-covering
  in ONE CAS; partial (non-completing) takes stay borrowable BY DESIGN (Decision 1
  contestability, not a window). Generalize the rule to W2b/W2c: any transfer that completes a
  demand must land occupied atomically.
- **CP-W2b NEXT (demand FIFO + sticky-head barrier), design notes from the W2a pass**:
  (1) Pool gains a mutex-guarded FIFO of registered entries {d, captured gen, bodyCache, w} +
  `barrier atomic.Pointer[Cache]` holding the HEAD'S BODY CACHE — the one-load unarmed check AND
  the exemption anchor (rule (b): exempt iff acquirer's parent chain passes through it).
  (2) Registration in the w≥2 Acquire slow path: create the body cache (`c.NewChild()`, settled
  (a)) at registration; idempotent re-presentation by (d, gen); Permit backs from the body
  cache. Streampool meta-redirect (sub-waves parenting under C_B^L) has no production caller
  until step 4 — permits-level only for now.
  (3) The head's gather runs on ITS CALLER's goroutine: succession wakes the pool's waiters; the
  promoted caller's retry finds itself head and gathers. Gathering is head-only; non-head
  registered demands fail fast (postpone/park).
  (4) **WAKE-FORWARD CONSERVATION (found in the W2a pass — REQUIRED for W2b correctness):**
  `permits.AcquireWait` currently DISCARDS the rdvq Notification from a completed Wait (`_, err
  :=`) — today benign (a failed retry means somebody took the permit and will release+wake
  again), but under an armed barrier the freed permit is taken by NOBODY (gated), so a gated
  waiter consuming the wake and re-parking without Forward LOSES the wake and wedges the head.
  AcquireWait must adopt the m.Received→Forward discipline (see streampool blockAcquire /
  reclaim). The spec's "cheap fail-and-re-park" for non-head wakes is only correct WITH
  forwarding.
  (5) Model targets: barrier-pass liveness induction under adversarial nesting, FIFO
  no-starvation, weight-1 progress across epochs, head invalidation mid-gather (hoard
  disposition + successor promotion), identity ABA (stale head ref to recycled demand),
  blocked-legitimacy predicate scoped to unarmed (armed non-head blocking is legitimate);
  now-possible over-subscribed weighted -race stress.
  Steps 3 (TryAcquireUpTo/NotifyAt) and 4 (surface builders + sets + opoption removal + the
  meta-redirect wiring) follow, each separable.

**►►► RIDER-CHAIN REDESIGN: R1–R6b LANDED (2026-07-06); CP-R7 LANDED (164ff63 + 923c57a;
headline was stale "IN PROGRESS" at merge time — the ►► sub-banners below record the later
landings through OriginFlow 2026-07-10). Disposition (PN's call): psgwf DELETED ✅ +
otpsg-v2-on-flows DONE ✅. psgwf: benchapp
migrated off it; cancellation gap resolved as the "manual pattern" (carry a CancelCauseFunc
under a FlowKey + follow-up-cancels-at-end — ExampleWithFlow_perRequestCancellation); kills
the Example_clientTimeout flake. otpsg v2 (separate module): span's lifetime IS the flow —
`Traced(ctx,name)→(ctx,[]FlowOption)` carries the span as a path-scoped FlowKey value (for
child-span/log correlation, severs at fan-in) + a DAG-scoped anonymous FlowFollowUpFn that
ends it once at the flow's TRUE end (crosses funnels, covers async outliving the handler);
`Correlate`/`FlowSpan` read it in async bodies; propagation.go+per-op TracedTask/Skim/Funnel
DELETED, metrics.go+logging.go KEPT, Instrumented*=metrics∘logging.

►► ctxMeta PARENT REFCOUNT LANDED (2026-07-08, this commit) — the borrowSrcCtx UAF fixed
at the root. SPEC + as-implemented deviations: docs/decisions/ctxmeta-parent-refcount.md
(read "As implemented"). As landed: ctxMeta.refs (atomic, +Reset for pool copylocks) with
newCtxMeta/refMeta/unrefMeta cascade (subsumes releaseParent — skimCtxMeta transfers the
mint ref on the owned bare-ctx-skim chain); every meta stores selfCtx, freed ONLY at
refs==0, so a ctxpool child can never be re-stamped while reachable; parent stays linked
across async (keeps the name), permitRoot takes the sever's isolation role. KEY DEVIATIONS
from spec: (1) FOUR sync-only walkers needed the permitRoot stop, not two —
currentHeldPermit, vetNotNestedInSkim, flowBoundaryAboveWave, ensureCache/ensureCacheChain
(forest liveness argument is synchronous-extent-only) — all via ctxMeta.syncParent();
(2) spec's vetNotNestedInSkim pseudo-code would falsely panic task-from-skim-handler
(started at cm.parent unconditionally) — syncParent stepping fixes it; (3) stash-path
borrows (flush/fire Run) do NOT capture srcMeta.riders — the pin covers the meta, not the
rider chain (that's the deferred driver-link rider pin); behavior-neutral (fan-in severs
first). Shared core newBorrowedMeta (pin+permitRoot+selfCtx); borrowBodyContext takes
srcMeta explicitly (dispatch sites resolve it synchronously; funnelInstance/flowFireWork
Execute resolve-and-pin, Run borrows from the pin, unpins after borrow; non-handoff
Execute paths unpin). BONUS: the four funnel/skimmer submit sites that leaked their minted
meta ("needs the borrow-source fix first") now release it. Validation:
TestCtxMetaConservation (ctxMetaAllocHook seam, mirrors node conservation) +
TestBorrowBodyContext_ParentPinnedAcrossSourceRelease; permit-root tests re-pinned to
permitRoot/syncParent. GATE (all green, no false green): vet, lint 0, full -short ./...,
root -race -short, 30/30 TestBySimulation -race (checks=200, ~6000 cases), 2284 flow-suite
-race iterations — 0 DATA RACE, 0 unexpected failures (19× the known union flake, 0.8%,
matches base).
REPRO CAVEAT (honest): the ~1/400 race did NOT reproduce on the pre-fix base in 8000
targeted -race runs this session (originally seen under heavy ambient load), so the fix
rests on the structural argument, not an observed before/after. PRE-EXISTING FLAKES
surfaced while looping the flow suite -race on the BASE commit (not this change; not
chased): TestFlowAllocFloors fails under -race (alloc floors are documented no-race runs —
consider a skip-under-race guard) and TestFlowTagFunnelUnion (the KNOWN pre-existing flake,
see the CP-F6 note — "flushSawB, zero-deadline flush racing the 2nd accumulate"; re-measured
IDENTICAL base vs fixed this session: 4/400 each standalone -race, ~1/4 per full-suite -race
iter under load, ~≤1/300 no-race).
Tell the combiner thread obs (2) is FIXED here.

►► CP-F5b LANDED (2026-07-08, this commit) — sim steps-only flow scopes + carrier
conservation oracle; the async fire path (flowFireWork) is now under the sim's adversarial
schedules (it wasn't: CP-F5a's whole-run scopes always fire inline at scope exit). As
landed: Plan.FlowSteps (drawn with FlowConfig.StepsOnlyProb=0.5 given Flow → ~1/8 of
generated plans ambient) wraps ONLY the Steps loop in the WithFlow scope — the scope exits
with dispatched work outstanding, the follow-up fires async from the drain (the wave
keep-alive makes the drain wait for it; fires-exactly-once asserted via Eventually AFTER
the drain, replacing whole-run's fired-after-drained assert, which a legal mid-drain async
fire would violate). CARRIER ORACLE (flowState.carriers): the sim counts its own model
units — dispatched task (startTask → launcher-body defer), submitted skim item (submitTo →
handler defer), submitted funnel item (submitTo → per-INSTANCE accumulated count,
decremented at FlushFn end; one factory call = one instance, serialized under the instance
mu) — and the follow-up asserts carriers==0 at fire: every sim decrement happens-before
the framework's rider release, so nonzero ⇒ premature fire. Retry semantics follow the
existing exact-invocation-bounds invariant (dispRetry ⇒ work Freed-not-queued ⇒ keep the
count for the retry). DISABLED (carrierAssert=false) on cancellation plans — teardown
abandons units without running them, stranding the sim-side count (framework refs still
release; fires-once still asserted). Steps-only skim handlers also exercise F7 item-chain
riders in the sim for the first time (drain runs post-scope; handler sees the ITEM's
riders). NON-VACUOUS: TestFlowStepsOnlyScopeEndToEnd (hand plan: tasks + funnel w/
flush-submit + steps-only subjob, 50 runs = 150 scopes) asserts ≥1 async fire via the
flowStepsAsyncFires counter; MUTATION-CHECKED: removing the skim decrement trips the
oracle ("fired with 1 model carrier(s) outstanding"). GATE: vet, lint 0, full -short
./..., targeted ×5 plain + ×40 -race, 20/20 TestBySimulation -race (checks=200, ~4000
cases, steps-only scopes ambient).
►► OriginFlow LANDED (2026-07-10, this commit) — the READ half; the pinning/origin record
is now FULLY IMPLEMENTED (PinFlow/UnpinFlow 08aae99, HoldFlow fdcafbf+ea7241f, OriginFlow
here; §"As implemented: OriginFlow"). origin.go: one switch over the resolved meta —
(1) explicit origin link wins (NEW atomic field ctxMeta.origin): the FLUSH case — the last
accumulate's meta stamped from the step-2 rolling driver pin while held (in flush under
c.mu, onto the fan-in clone OR the executor path's own borrow via the new flush(ctx,
ownMeta) flag — both single-custody at stamp; cleared by releaseBodyContext = the pin's
validity window; the INLINE TAG-FREE flush runs ON the triggering accumulate's published
ctx — unstampable — and resolves its parent: the reader is already AT the origin's
position); (2) fire metas → ok=false (async = skim-typed permitRoot; INLINE scope-exit
fire = TOP-LEVEL-typed permitRoot — caught during impl; the pin MARKER disambiguates
pinned ctxs, also top-level permitRoots, whose origin IS their parent = the source ref
PinFlow deliberately kept); (3) default → meta.parent (dispatcher/drive/enclosing),
deliberately ignoring permitRoot — origin IS the cross-extent hop the refcounted parent
link exists for. Returns origin.selfCtx (alive for the caller's extent via parent-chain
refs; flush origin for the flush extent via the pin) so From/InFlow/PinFlow/HoldFlow
compose unchanged; expired-pin vet on entry (cold path per record). Tests
(origin_test.go): task+scope hops w/ composition; skim handler drive-vs-item
discrimination (item value on ctx NOT on origin; drive value on origin); flush sweep +
inline-tagged (SHARP assert via the sever: per-item value absent on flush ctx, present on
origin); fire absence async+inline; pin→source; hold→absent; pin-the-origin retention.
GATE: vet, lint 0, full -short ./..., root -race -short, flow/origin/pin/hold -race ×20
×9 runs (2 hits of the KNOWN pre-existing TestFlowTagFunnelUnion flake under concurrent
sim load — flushSawB line 692, the documented signature, rate matches the dossier; 0
DATA RACE, 0 origin/pin/hold failures), 20/20 TestBySimulation -race (see commit). NEXT:
streamotel consumer (otel-tracing-on-flows.md) — the full driver-attribution + retention
+ read stack is now in place.

►► HoldFlow LANDED (2026-07-10, this commit, PN design session) — the SAFE retention tier
(record §HoldFlow — read it; three designs died first: AfterFunc pattern SWALLOWED fire
errors; the fires→cancel→teardown sandwich failed because CANCEL IS A NOTIFICATION NOT A
BARRIER (Go contract: canceled ctx stays usable, esp. value reads — the woken-by-Done
goroutine arrives after teardown by construction); the forever-readable GC-snapshot pin
either LIES or HOLDS FLOWS OPEN for the handle's GC lifetime). RESOLUTION (PN): two
tiers — PinFlow/UnpinFlow unchanged (pooled primitive, one extent rule, body-ctx-class UB
caveats), HoldFlow = GC wrapper, NO UB ever, NOT sugar (own carrier refs, own state ⇒ own
verb; "hold" = the codebase's own word: flowInstance.holds). hold.go: snapshot at hold =
TWO GC copies of the chain, permanent +1 ref bias (never poolable): LIVE (real inst ptrs —
dispatch extends real lifetimes) + SEVERED (value-only); wrapper delegates ctx methods to
an atomically-swapped inner ctxpool child (both over one WithCancelCause(Background);
ctxpool cooperates — children of canceled parents fall out of the pool by design).
the returned CANCEL (PN: name it cancel not release — canceling is its visible effect; Once, idempotent): FIRES (inline, carrier=live hold meta, chain-order cover, GC
chain ⇒ concurrent readers safe) → SEVER (swap to value-only) → CANCEL(Join(cause,
fireErrs)); nil cause defaults to context.Canceled FIRST (fire error never the primary
cause). POST-RELEASE: reads = snapshot-as-of-hold FOREVER (copy-over-absent: the copy
must exist for race-freedom anyway; liveness truth lives in Err()/Cause, values are
facts); dispatch = defined ordinary-canceled (value-only riders). Documented asymmetry:
reads race-free vs cancel; dispatch is not (same class as any ending extent). Tests:
retention+snapshot-reads, nil-cause primary, dispatch (waits for work AND release),
post-release dispatch defined, CONCURRENT-READS-DURING-RELEASE -race (the crown jewel:
zero misses before/during/after), UnpinFlow(held) rejected, conservation arc (A4). GC
metas/nodes bypass alloc hooks (not pooled) ⇒ conservation clean by construction. Gate:
vet, lint 0, full -short ./..., root -race -short, hold+pin -race ×20, concurrent-read
×50 -race. NEXT: OriginFlow + the flush origin link, then streamotel.

►► PinFlow/UnpinFlow LANDED (2026-07-10, 08aae99) — the retention half of
context-pinning-and-origin-access.md (read its "As implemented"). pin.go: PinFlow MINTS
the pinned ctx (fresh meta: topLevelContext, permitRoot, pinLive marker, parent=src
ref'd for origin-chain walkability, riders=src chain under the pin's OWN
flowRefRiders+nodeRef, Background-rooted ctxpool child = the token); UnpinFlow validates
exact token (selfCtx identity + marker) + CAS pinLive→pinExpired (racing double-unpin
loses loudly), releases carrier refs in chain order (same walk-cover discipline as every
release site; the pin meta is the fire's last carrier), fires INLINE — DEVIATION: UnpinFlow
RETURNS error (fires join it, the WithFlow-scope-exit shape; async has no wave to root
at). ctxMeta gains pin atomic.Int32 (pinNone/pinLive/pinExpired) + vetNotExpiredPin,
checked at ensureCtxMeta (all dispatch derivations), WithFlow, PinFlow — detection window
= the meta's survival (an immediately-recycled token's re-pin degrades to an empty pin,
the documented residual; the validation test creates the deterministic window with an
in-flight task). Tests (pin_test.go): retention (values readable post-scope; follow-up
gated on unpin; fire error joins), dispatch-from-pin end-to-end (body reads values;
follow-up waits for work AND unpin; wave-less ⇒ op.In required, panic pinned),
exact-token/double/derivative/expired validation, compose+handoff (fires once),
degenerate bare-ctx pin, multi-goroutine concurrent dispatch (-race). Conservation: BOTH
conservation tests gain pin arcs asserting the standing pin as a DELIBERATE POSITIVE
(leaked pin visible) and zero after release. GATE: vet, lint 0, full -short ./..., root
-race -short, flow+pin -race ×20, pin+conservation -race ×20, TestBySimulation -race
batch (see commit). NEXT: OriginFlow + the flush origin link (the read half), then
streamotel.

►► FLOW OPTION VOCABULARY + SEQUENTIAL SEMANTICS LANDED (2026-07-10, this commit, PN
design session) — TWO changes, one checkpoint. (1) NewFlow() → Disconnect(): the old name
contradicted flow-design's own ontology ("flows are not created"); trail in flow-design.md
(diverge/divert = divergences PRESERVE inheritance; dam = noun-not-verb + dams spill;
isolate = sandboxes the work; stop = overclaims; FlowDisconnect = namespace artifact;
DisconnectFlow = claims the flow itself is cut). PREFIX RULE settled:
disambiguate-never-decorate — FlowFollowUp keeps its prefix (method siblings
k.FollowUp/t.FollowUp); Disconnect is bare (no sibling; lives only inside WithFlow where
flow is implied). Word-order families: verb+Flow when flow is the operation's object
(PinFlow/UnpinFlow/OriginFlow); Flow+noun for package-level flow-namespace symbols
(FlowKey/FlowTag/FlowOption/FlowFollowUp). (2) SEQUENTIAL OPTION SEMANTICS (PN, replacing
the order-independent build): an option list is SUGAR FOR NESTED SCOPES, one layer per
option, first outermost — later Value shadows earlier sibling; Suppress filters the chain
AS BUILT SO FAR (can suppress an earlier sibling; later re-add lands after); Disconnect
drops the whole working set (a preceding Value = well-defined shadowed nonsense; a
preceding follow-up still registers, gains no carriers, fires EMPTY at scope exit — the
nesting equivalence's answer, conservation-sound). buildFlowRiders rewritten as a
left-to-right fold: mint() layers instances; replace() swaps the working chain with
nodeRef/nodeUnref disposal (transient ref covers a refs-0 fresh top; the cascade stops at
the first held node — ambient carrier or an earlier follow-up's enclosing pin; net-zero on
purely inherited chains). Same-id Value+FollowUp no longer merge onto one node (each
option = own node; the fire's enclosing may now include an EARLIER same-key Value — F6
peel = own NODE only, coherent under nesting). settledVal/hasFollowUp/dedupe machinery
deleted. Tests: TestFlowDisconnectRoot FLIPPED (was pinning order-independence);
TestFlowOptionOrder (shadowing, sibling-suppress both orders, follow-up-behind-Disconnect
fires); TestFlowNodeConservation gains (A2) sequential-layer disposal arcs. GATE: vet,
lint 0, full -short ./..., root -race -short, flow/funnel -race ×20, conservation ×5,
TestBySimulation -race batch (see commit).

►► CONTEXT PINNING + ORIGIN ACCESS DESIGN CONVERGED (2026-07-09, PN design session) —
docs/decisions/context-pinning-and-origin-access.md (read it; this is the summary). TWO
SURFACES on top of the driver-contexts machinery: (1) PinFlow(ctx)→pinned / UnpinFlow(pinned) —
the pin MINTS a fresh ctx (the token IS the ctx, no release func); carrier refs (flow
stays open until last Unpin — leaked pin = follow-ups held open, documented like an
unclosed resource); WAVE-LESS + no parentWaves + permitRoot + no held/exEnv + pin marker
+ context.Background() root (stable ancestry; carries riders/values, never the source's
cancellation) — "an explicit pin is the purchase of Go's normal context contract".
Dispatch from a pin = ordinary bare-ctx top-level submission: resolveWave already forces
op.In(&wave) (PN's move — replaced my just-block special mode; consistency over modes),
help-shaped blocking + the usual multi-goroutine skim caveats apply since the wave is
EXPLICIT. (2) OriginFlow(ctx)→(ctx,ok) — NAMES SETTLED as FLOW-ANCHORED (PN): OriginFlow + PinFlow/UnpinFlow (after
parent/upstream/trigger/enclosing/driver/source all fell; scope words fail because the
relationship is CAUSAL across extents, not scoped — async drivers don't enclose;
"source"/"upstream" read as data-lineage, wrong at the skim handler; the record's Naming
section has the full trail + the qualification test). Ctx-shaped composable read (walks
the chain by re-application): task/acc→dispatcher, skim handler→the drive, flush→last
accumulate via the step-2 rolling pin + ONE new field (origin link stamped on the flush
body meta), fire→ok=false (the fire IS the continuation), pumps/top→false.
Read-within-extent; PinFlow it to keep it. PIN SEMANTICS SETTLED (2026-07-10): pinning a pinned ctx = a NEW INDEPENDENT pin (no shared counting — aliasing; handoff idiom = overlap: p2:=PinFlow(p1); UnpinFlow(p1)); pins compose freely (pinned source = stably valid, no extent-window precondition); UnpinFlow requires the EXACT token (ctx==selfCtx, loud on derivative/double); plain Go derivation transparent (WithCancel(pinned) IS the cancelable-retention composition); WithFlow(pinned)=normal call-scoped scope; PinFlow(bare ctx)=degenerate empty-flow pin; AFTER UnpinFlow the ctx + every derivative is INVALID (pin window IS the extent — one rule): unpin is NOT cancellation (in-flight work unaffected; post-unpin liveness coincidental never contractual), re-pin cannot resurrect (positional cover), detection best-effort (expired marker on cold paths; hot reads untaxed; post-reuse undetectable — accepted residual). "Driver" stays internal vocabulary
(driver-contexts.md). NEXT: implement (PinFlow/UnpinFlow first — incl. independent-pin composition, exact-token unpin, expired-marker checks on cold paths — then OriginFlow + the flush
origin link), each sim-gated; streamotel consumer follows on top.

►► DRIVER-CONTEXTS STEP 3 LANDED (2026-07-09, 55e16e6) — fire = the last carrier's
continuation (driver-contexts.md §Fire). CARRIER PLUMBING: unref/flowUnrefRiders gain a
carrier *ctxMeta — the meta whose release drops the ref — passed from every count→0 site
while its owner ref is still held: releaseBodyContext (m), skimWork.Free (the step-1
per-item child meta, whose OWNER REF NOW TRANSFERS Execute→Free so it survives to be the
carrier; nil if never executed), WithFlow scope exit (scope meta; inline fires always
COW), runFire's holds cascade (the inner fire's meta, pinned refMeta+nodeRef across the
cascade — without the node pin the outer's chain build would walk nodes freed by
releaseBodyContext). FIRE CHAIN BUILT AT DISPATCH (buildFireChain), NOT at fire-run —
THE key soundness lesson (cost two crashes): instance-ref cover is positional and
momentary. Every release walk drops instance refs head→tail, so at the count→0 trigger
only the SUFFIX (below the fired binding's node) is still covered; PREFIX instances
(post-registration bindings) may already be fired+recycled in the same walk → prefix is
copied VALUE-ONLY (id+val, inst=nil: reads yes, pinning/re-fire no). SECOND crash
(TestFlowCoalesceConservation): "one node per id per chain" is FALSE on the R6b fan-in
union chain (one node per coalesced LEAF); ref'ing sibling leaves re-drove
coalesceAtZero on recycled components → buildFireChain peels ALL matching nodes
(definitional: by id) and partitions at the DEEPEST match (the closing ref can't be
later; between-trigger-and-deepest live siblings degrade to value-only — conservatism
costs only lifetime pinning, same trade as the flush pin). ADOPT-OR-COW at fire-run
(flowFireWork.Run): refs==1 (only the dispatch pin) ⇒ returned custody ⇒ ADOPT the
carrier meta in place — keep position (parent+ref, parentWaves, wave), re-stamp
execution (ee, held=nil, permitRoot=true, ctxType=skim), riders=fireRiders, selfCtx
RE-HOMED onto the scheduler src ctx; the dispatch pin becomes the owner ref. Else COW
sibling via newBorrowedMeta(src, carrier.parent) + parentWaves copy, drop the pin.
fireRidersSet flag distinguishes empty-chain-carrier from no-carrier (teardown) legacy
fallback (enclosing-at-registration, unchanged). CANCELLATION RESOLVED (the doc's open
point): SHIELDED — fires are end-of-flow cleanup (otel span end); every arm roots ctx
ancestry at the scheduler src, the carrier contributes riders never cancellation;
TestFollowUpFireShieldedFromCancellation pins it. SEMANTIC REFINEMENT test (red first):
TestFollowUpFiresAsCarrierContinuation — fire sees a post-registration rider value +
fired-tag peeled. GATE: vet, lint 0, full -short ./..., root -race -short,
flow/funnel/ctxmeta -race ×20, step tests ×10, TestBySimulation -race batch (see
commit). NEXT: streamotel consumer (otel-tracing-on-flows.md open points; driver
attribution machinery now complete: skim child meta 9bb6af6, flush pin 86912a8, fire
continuation this commit).

►► DRIVER-CONTEXTS STEP 2 LANDED (2026-07-09, 86912a8) — flush rolling node-only
driver pin, per driver-contexts.md §Flush. As landed (funnel.go): funnelInstance gains
{driverMeta *ctxMeta, driverRiders *flowRiderNode} (mutated only under c.mu); accumulate
re-points the pin at its own body meta (refMeta) + that meta's rider head (nodeRef),
releasing the previous pair — four uncontended atomics, no alloc; flush releases the
final pair in a defer AFTER the flush body (panic-safe; release only returns pooled
objects, fires nothing, so ordering vs the barrier/tag-union defers is immaterial).
Node-only per the doc: NO flowRefRiders — the driver's own follow-ups may fire before
the flush reads; values stay readable on the pinned nodes. The inline past-deadline
flush trivially satisfies "driver = last accumulate" (it RUNS on the triggering
accumulate's ctx; the pin is released by its flush call). NO reader surface yet — the
accessor is streamotel-scope (otel-tracing-on-flows.md Open); the pin is reachable
through the instance. Validation: TestFunnelDriverPin (flow_internal_test.go) — white-box
pop/inspect/push-back via the owner lineage between accumulates (pin tracks the LAST
accumulate's meta+rider head) + ctxMetaAllocHook balance proves the flush release;
MUTATION-CHECKED both arcs (dropping the accumulate-side release of the previous pair,
or the flush-side release, each trips the balance assert). Leak backstop in every suite:
TestCtxMetaConservation's funnel workload. GATE: vet, lint 0, full -short ./..., root
-race -short, flow+funnel -race ×20, pin test ×10 plain ×20 -race, TestBySimulation
-race batch (see commit). NEXT: fire continuation (driver-contexts.md §Fire — last
carrier's context, adopt-or-COW, exEnv never carried, cancellation-ancestry OPEN needs a
test either way), then streamotel consumer.

►► DRIVER-CONTEXTS STEP 1 LANDED (2026-07-09, 9bb6af6) — skim per-item child meta;
the in-place rider override (skimmer.go skimWork.Execute) is GONE. The regression test
came first and pinned the misdelivery WORSE than the doc's prediction: within one drive,
a rider-free item after a rider-carrying one didn't just see the previous item's values —
by its turn the leaked chain was already RECYCLED (its refs die at the previous item's
skimWork.Free), so the handler read a DANGLING rider head and lost the drive's riders
entirely (TestFlowSkimRiderFreeItemIsolation; ordering made structural: the rider-carrying
item's handler submits the rider-free item, so it necessarily skims later in the same
drive — queue-order approaches were nondeterministic, warmed pools consistently reordered
two body-posted items). As landed (skimmer.go): per-item child meta in skimWork.Execute —
parent = drive meta via refMeta (synchronous, NOT permitRoot: vetNotNestedInSkim/permit
walk see through), ctxType skim, riders = item chain or driveMeta.riders for a rider-free
item (nearest-wins now structural), exEnv shared (ownsExEnv false), selfCtx via
ctxpool.WithValue, owner unrefMeta at handler exit (async handler dispatches keep it via
their parent ref). NO rider refs on the child: item chain held by wk's submit refs until
Free, drive chain by the drive scope — both cover the handler's synchronous extent;
handler dispatches take their own refs at borrow. flowBoundaryAboveWave/vet/held walks
audited: one extra in-wave link, same results. TestPermitScopingChains updated to the new
topology (child → drive skim meta → top-level). ALSO: TestFlowAllocFloors now skips under
-race via a root-package raceEnabled guard (race_on/off_test.go) — the documented
pre-existing -race flake from the refcount CP's dossier. GATE: vet, lint 0, full -short
./..., root -race -short, flow-suite -race ×20 + skim/ctxmeta -race ×50, regression test
red-on-base green-on-fix verified both ways, TestBySimulation -race batch (see commit).
NEXT: flush rolling node-only pin, then fire continuation (driver-contexts.md), each
sim-gated; THEN streamotel consumer.

►► DRIVER-CONTEXTS DESIGN CONVERGED (2026-07-09, PN design session) —
docs/decisions/driver-contexts.md (supersedes the refcount doc's "driver-link rider pin"
follow-up; read the doc, this is the summary). TWO CONTRACTS: (1) a ctxMeta is IMMUTABLE
for its ref'd lifetime, in all cases — mutation only in single-party custody (pre-publish,
or refs==1 returned-custody); (2) exEnv is OUTSIDE that invariant under a CUSTODY contract
(slot of the currently-executing extent; stamped at extent entry; NEVER read across an
async boundary — no structural enforcement possible, documented rule). DRIVERS: task/acc =
dispatcher (already have its chain); skim = drive flow via PER-ITEM CHILD META (parent =
drive meta, sync non-permitRoot, riders = item chain — kills the in-place override, which
is BOTH an invariant violation AND a live misdelivery bug: a rider-free item after a
rider-carrying one sees the previous item's riders (never restored); fix behind a failing
regression test; per-drive-restamp alternative REJECTED — unsound under lazy parent.riders
reads); flush = THE LAST ACCUMULATE (its returned deadline/finality made the flush due —
unifies inline/deadline/sweep; scheduler = just the timer) via a ROLLING NODE-ONLY PIN on
the instance (refMeta+nodeRef per accumulate, release prev pair, release after flush body;
NO flowRefRiders — observability must not delay driver fires; driver's follow-ups may have
fired, values stay readable); fire = THE LAST CARRIER'S CONTINUATION (fire ctx = carrier's
context, same tree position, riders = carrier chain MINUS fired binding — semantic
refinement: fire sees riders the carrier acquired post-registration, consistent w/ R6b
last-standing-branch; pin taken at count→0 dispatch while owner ref still held; refs==1 at
fire-run ⇒ ADOPT+mutate in place (returned custody), else COW SIBLING (same parent
refMeta'd, nodeRef'd remaining riders, NEVER copy exEnv); inline scope-exit fire always
COW). Executor-pumped bodies have NO driver link ever (framework plumbing; wave-ID
attribute covers substrate). OPEN (flagged in doc): fire cancellation ancestry under
adoption (carrier chain vs scheduler) — resolve at implementation with a test. Accessor
surface = streamotel session scope, not this record's.
NEXT: implement driver-contexts.md (fresh focused session; regression test for the skim
rider-leak FIRST, then skim child meta, then flush pin, then fire continuation — each
sim-gated), THEN streamotel consumer (otel-tracing-on-flows.md open points: fan-in helper
shape, wave-participation delimitation — driver attribution now settled here).

►► DESIGN LANDED THIS PHASE (2026-07-08): otel tracing model — docs/decisions/otel-tracing-on-flows.md
(9c6d950). Flow-native observability via existing riders (not an event stream); otel is one
lossy projection. Flow=primary=trace; spans bounded by (sub-)flows via follow-ups, not waves
(wave ID = attribute); 3 axes → parent/child (async lineage), aggregation links, driver links;
tag-defined flow = multi-trace graph (no trace-ID unify — non-scaling). streamotel = rename
otpsg→otel/, delete metrics/logging/instrumented, patterns + one fan-in helper (FOLLOW-UP,
after the refcount lands). CP-F5b sim-oracle extension (steps-only scopes + conservation oracle;
coalescing count is nondeterministic → assert conservation + ranges) also still pending.

R6b (definitional coalescing, union-find) is
implemented + green in the worktree (see "CP-R6b LANDED" in the flow section for
build pointers). Commit chain (newest first): effded2 R6b-handoff-banner · 806a506
R6a (definitional tag follow-up, shared chain) · 4c2be2e F7 · 5441c9a R5 (funnel
fan-in/F8) · d661035 R4 · ae7065a R3 · 473ebe0 R2b · ea4ca51 R2a · 210a0dc R1 ·
dbe0213 spec. TWO PRE-EXISTING FUNNEL/PERMITS INFRA BUGS handed off (full dossiers in
the combiner branch WORKING_NOTES, 2026-07-06): the skimSelect/WaitForNew HANG —
combiner reconciled it CONFIRMED FIXED by 5574a40 — and the funnel `borrowSrcCtx`
-race (funnelInstance.Run borrowBodyContext vs ctxpool child Free, ~1/400) — NOW
FIXED HERE by the ctxMeta parent refcount CP (2026-07-08, banner above); combiner's
WORKING_NOTES entry (2) should be closed pointing at that commit when this merges.

**►►► CP-R6b CORRECTED MODEL (2026-07-06, w/ PN) — CRITICAL for anyone touching
coalescing or writing its tests.** The spec's premise "independent flows converging
at a funnel coalesce" is refined: **the unit of aggregation is a funnel INSTANCE, not
a funnel.** A flow is defined by its DATA, not its operations — the set of items one
funnel instance accumulates IS one aggregated flow, whose definitional follow-up
fires once. Independent flows coalesce ONLY when they **co-accumulate in the same
instance**; flows in different instances are different flows and fire separately —
correctly. Whether two independent submits land in one instance is a **runtime
accident** (`Funnel.submit` → `ExecuteNowOrQueue` runs inline OR async; a funnel
keeps a QUEUE of instances, `funnel.go:~726`, and a concurrent/late submit that finds
the queue empty spins a fresh instance). ⇒ **No black-box "N submits → 1 fire"
assertion is deterministic** (2-flow scatters ~0.2% w/o race, more w/ race). Tests:
`TestFlowCoalesceMechanism` (white-box, DETERMINISTIC single-fire proof — drives the
union-find primitives directly); `TestFlowDefinitionalCoalesce` (black-box, robust:
requires coalescing OBSERVED across 40 iters + count always in [1,2], never asserts
==1); `TestFlowCoalesceConservation` (conservation + concurrent-downstream deref
stress, fire count range-checked not pinned). ⚠️ **Alloc-floor tests (`TestFlowAlloc*`)
must run WITHOUT -race** — `testing.AllocsPerRun` counts race-instrumentation allocs;
gate alloc floors no-race, concurrency tests with -race, separately.

**►►► FLOW DESIGN CONVERGED (2026-07-03, design session w/ PN); IMPLEMENTATION IN
PROGRESS on branch `flow-impl` (worktree). Parallel thread to the weighted-acquisition
work. SUPERSEDES the Flow-object surface everywhere it appears (API_DESIGN.md Flow
section, programming-model.md Wave+Flow framing, surface-lineage Flow bullet): there is
NO Flow type anymore.** Rationale chain recorded below so it isn't relitigated;
`docs/decisions/flow-design.md` is now the permanent record (docs pass done 2026-07-03 —
see open queue item 5).
- **CP-F1 LANDED (2026-07-03, this commit): keys/tags + path-scoped values end-to-end.**
  `flow.go`: FlowKey[V]/FlowTag/NewFlowKey/NewFlowTag (identity = *flowIdentity pointer,
  NONZERO size on purpose — zero-size allocs share an address), key.Value / key.From /
  tag.InFlow live; FollowUp/Suppress/NewFlow() declared per API-first but panic
  ("not yet implemented", CP-F2/F4). WithFlow = plain inline call; zero-opt degenerates
  to body(ctx); scope meta CLONES the ambient meta (wave/parent/parentWaves/ctxType/
  exEnv) so it is transparent to wave resolution, permit-chain walks (held stays nil,
  parent link preserved), and reentrancy typing; rider set = immutable snapshot
  `*flowRiders` (small slice, linear scan, replace-or-append shadowing).
  - **Propagation seams (one pointer copy each)**: borrowBodyContext captures riders
    from the submit-time ctx (body borrows happen synchronously AT DISPATCH — verified
    launcher newTaskWork + funnel newFunnelWork; the riders ride the DISPATCH chain,
    unlike meta.parent = the severed permit chain); ensureCtxMeta inherits riders
    verbatim on every derivation (+ gained a sourceMeta.wave==nil branch so a top-level
    scope meta doesn't pollute parentWaves with a nil key).
  - **Fan-in sever is STRUCTURAL**: funnelInstance.flush severs at entry via
    severFlowRiders (bodyMetaPool clone with riders=nil, released at flush return —
    synchronous extent). Covers BOTH drive paths: the executor Run path (naturally
    rider-free src) and the INLINE already-past-deadline flush, which arrives on the
    triggering accumulate item's ctx and was the leak path. Tested both.
  - **Scope meta/child NOT pooled** (plain alloc, GC-owned): the scope has no
    completion event until CP-F2 refcounts provide one, and a freed-then-recycled meta
    read through a retained scope ctx would misdeliver. One alloc per REGISTERING
    scope, never per dispatch; BenchmarkLauncherSkim floor CONFIRMED unchanged at
    1 alloc/op. Revisit pooling with CP-F2 (refcount zero = safe recycle point).
  - Gate: vet, lint 0 (after cache clean — a stale main-tree golangci cache leaked 5
    permits-WIP gosec findings into worktree runs; `golangci-lint cache clean` fixed),
    full -short suite, flow tests (propagation chain incl. sub-wave + skim, absence,
    degenerate, shadowing, in-body scope, sever ×2 paths, zero-identity panics),
    40× TestBySimulation -race batch (regression — sim has no flow surface yet).
  - **CP-F2 LANDED (2026-07-03, this commit): follow-ups end-to-end.** `flowinst.go`:
    flowInstance {fn, fnRiders, count atomic, active atomic} — one per FollowUp option
    per scope. Carriers: +1 scope (entry→exit, the lexical cover), +1 per work item
    (ref in borrowBodyContext at dispatch / unref in releaseBodyContext — SYMMETRIC BY
    CONSTRUCTION with the existing borrow/release pairing), +1 while fn runs (fire()
    borrows its ctx through the same ref/unref path ⇒ the design's "provisional ref"
    falls out of the symmetry for free). Derived metas (ensureCtxMeta) and the flush
    sever clone inherit WITHOUT refs — synchronous extents covered by their enclosing
    carrier; their release paths don't unref. Conservation is pairing-structural.
    - **Firing = {count, active} state machine**: 1→0 arms via CAS(active); the pass
      fires fn iff count==0, then resolves: count==0 after fn = TRUE END (quiescent
      forever — no carrier remains to re-ref; extensions that completed within the
      pass count as observed — kills the inline-drain refire livelock); count>0 =
      disarm + closed missed-wake window (recheck-and-reCAS after Store(false)).
      Each real extension's last release arms a fresh pass = "fires at each nominal
      end".
    - **Two firing paths**: scope-exit unref fires INLINE (user's own call site;
      makes "empty scope fires at return" deterministic); work-item completion unrefs
      fire via scheduler→executor (flowFireWork: bare workq.WorkItem embed — NO wave
      ref; funnelInstance.Execute handoff pattern verbatim; Free no-op, Run recycles)
      because release sites run inside Free machinery BEFORE the item's wave ref
      drops — an inline fn draining that wave would deadlock.
    - **fn ctx**: rooted at context.Background (a flow's end-reaction must not
      inherit the ended work's cancellation — nolint:contextcheck by design), meta
      carries fnRiders = single-entry bundle {id, settled val, [this instance only —
      NOT siblings]}; fn's dispatches work via the CP-F1 nil-wave ensureCtxMeta
      branch. **fn SIGNATURE DECISION (flag for PN): func(context.Context), NO error
      return** — a follow-up has no wave to surface an error through; an error return
      would be silent-discard dressed as API. Errors belong inside fn (dispatch into a
      wave fn drains).
    - buildFlowRiders two-pass (values settle, then instances wire against final
      bundle values ⇒ option order-independence); copy-append on entry.insts
      (ambient snapshot sharing); instances GC-owned this CP (pool + gen = CP-F4;
      firing is cold).
    - KNOWN CP-F3 GAP (documented in FollowUp godoc): tag refs release at accumulate
      completion — DAG union across funnels needs the transfer multiset.
    - Gate: vet, lint 0, full -short suite, follow-up tests (scope-exit inline,
      empty scope, async completion via executor path, extension-refire-then-true-end,
      bundle value + order-independence, concurrent stress ×5 -race), all flow tests
      -race ×2, alloc floor 1/op, 40× TestBySimulation -race batch.
  - **CP-F3 LANDED (2026-07-04, this commit): fan-in transfer/union.** Two design
    simplifications found at implementation (both RECORD for the doc pass —
    flow-design.md says "multiset ... count per entry"; reality is simpler):
    (1) **SET, not multiset**: refs are fungible covers, not per-item tokens — the
    funnel holds ONE ref per DISTINCT instance (first accumulate refs it; later ones
    see it present). collectFlowTags at accumulate entry (under c.mu, the only
    accumulate path), ref-before-item-release ⇒ never-transit-unreferenced without
    any skip-marking. (2) **ADOPTION, not ref-churn**: the flush takeover hands the
    union — refs included — to the flush body ctx (flowFanInContext replaces
    severFlowRiders: path riders sever, tag union takes over as the flush meta's
    rider set); releaseBodyContext at flush end releases exactly one ref per
    distinct instance = the one collect took. Pure handoff, zero churn. Downstream
    dispatches from the flush body ref the union insts themselves ⇒ the lifetime
    survives through arbitrary post-flush chains; InFlow(ctx) true in the flush body
    (presence ORs through fan-ins, as designed). funnelInstance gains `flowTags
    []flowRiderEntry` (mu-guarded; nil'd at takeover; flush ALWAYS runs — the
    per-instance wave barrier — so the refs always release).
    - NOTED for docs pass: SKIM is not a fan-in edge — results are data pulled by
      the driver, not a submit edge; per-item riders don't reach skim handlers (the
      driver's chain applies) and tag refs release at item completion, not skim.
      Matches the ontology (edges = submits + funnel accumulate→flush); flag if PN
      wants it reconsidered.
    - Gate: vet, lint 0, full -short suite, new tests (tag-crosses-funnel ×2 drive
      paths incl. not-before-flush + downstream-keeps-alive + value-still-severed;
      two-scope union) -race ×2 + all flow tests, alloc floor 1/op, 40×
      TestBySimulation -race.
  - **CP-F4 LANDED (2026-07-04, this commit): shaping complete — Suppress()/NewFlow()
    live + alloc guards.** buildFlowRiders is now phased for order-independence:
    pass 0 validates + detects NewFlow (fresh root = skip inheriting the base) +
    applies suppressions against the INHERITED set only; pass 1 values; pass 2
    follow-up instances. Same-call Suppress+Value/FollowUp on one id = suppress
    inherited, register fresh — documented in godoc. A suppressed subtree takes NO
    refs on the suppressed bundle's follow-ups (cannot delay their nominal end —
    tested with a still-running suppressed body). TestFlowAllocFloors guards the
    cost model in the suite (allocsPerOp floors): degenerate WithFlow = 0,
    value-registering scope ≤ 6 (meta + snapshot + entries + ctxpool child
    bookkeeping — per REGISTERING SCOPE, never per dispatch), key.From = 0;
    BenchmarkLauncherSkim floor unchanged at 1 alloc/op.
    **INSTANCE POOLING: CP-F4 dropped it claiming captured-gen ABA machinery was
    needed — WRONG (PN challenge, 2026-07-04; REVERSED).** ref==0 at terminal
    resolution (firingPass true-end branch) IS the no-readers guarantee: the scope
    exited, all items under any containing snapshot completed, fn ctx released; a
    ref-from-zero needs a ctx carrying the instance and all are dead by the same
    escape contract as body ctxs (violation = the EXISTING body-meta hazard class,
    not new). Pooling is sound with NO gen: recycle at the true-end branch (sole
    terminal point, serialized by the active flag), omnipool zero-on-Put. Land in
    a later CP; registration stays cold either way. Reconcile flow-design.md
    "pooled, generation-stamped" → "pooled, no generation needed" at the docs
    pass.
    Gate: vet, lint 0, full -short suite, new tests (suppress key+tag incl.
    no-refs-taken liveness, NewFlow fresh root w/ trailing-position
    order-independence, alloc floors) + all flow tests -race ×2; sim -race batch
    26/40 clean then ONE HANG at iteration 27 — see the OPEN hang item below.
  - **►► OPEN: RARE SIM HANG (1× observed, 2026-07-04, CP-F4 batch iter 27; dump
    preserved at scratchpad sim4_race_27.log — do NOT delete until fixed).**
    Signature: 10m -race timeout; 6 goroutines; NO mutex/semacquire waiters; 4 skim
    drivers parked 9m in Wave.skimSelect via addWorkWhileMaybeBlocking/rdvq
    (top-level Run + two subjobs + a funnel-flush-driven subjob:
    funnelInstance.Run→flush→sim runSubjob→CloseAndSkimAll→WaitForNew); all executor
    workers idle-exited ⇒ missed-wake / stuck-reference class (some wave never
    Done-signaled its skimmer).
    - **ATTRIBUTION RESOLVED: PRE-EXISTING, NOT FLOW (2026-07-04).** The A/B landed:
      the pre-flow base 300576b — ZERO flow code — hung 1/30 under the
      subjob/flush-heavy bias with the IDENTICAL signature (6 goroutines, 4 parked
      selects in skimSelect/WaitForNew, no lock waiters; dump preserved at
      scratchpad bias3_base_hang_1.log). Corroborating: flow seams audited
      line-by-line as nil-rider no-ops on sim paths (sim never calls WithFlow); 146
      clean 10m -race iterations across the CP-F1..F3 batches with flow code
      present.
    - **VALIDATED REPRO RECIPE (~9× the ambient rate — use this to hunt it):**
      -race, -rapid.checks=10, default SelfTimes (zero-delay KILLS the repro — it
      needs real delays/parked-worker windows), planConfig Subjob.Add probabilities
      raised: Launcher.Body 0.5, Funnel.Accumulate 0.3, Funnel.Flush 0.5,
      Skimmer.Handle 0.3 → ≈1/300 checks (vs ~1/2600 ambient: 1 hit in ~26
      100-check 10m iterations). Other configs tried and DEAD: zero-SelfTime
      no-race ×300 checks and zero-SelfTime -race ×2500 checks, 0 hits both trees.
    - **Leading hypothesis (unproven):** latent wake-loss in the W2b-i/ii
      rdvq/workq wake-chain rewiring (1f27117 chained-bit consumer discipline /
      cd09e5a per-demand mailboxes) — the only recent commits touching the
      implicated machinery; failure class (missed wake) matches change class (wake
      conservation). Surfaced now simply by exposure (~150 additional 10m -race
      iterations against this base across the flow batches).
    - **Next step (decision for PN):** this belongs to the permits/wake-chain
      thread, not flow-impl — hand this dossier + recipe over (or trace here:
      capture the biased repro with PSGTRACEINTERNALS + -trace per the
      sim-trace-debugging skill; the recipe makes the trace small enough to read).
  - **CP-F5a LANDED (2026-07-04, this commit): sim flow oracle, whole-run scopes.**
    internal/sim/flow.go + Config.Flow{ScopeProb:0.25} + Plan.Flow: a scoped
    (sub)plan's ENTIRE run (steps + drain) wraps in WithFlow with a per-plan
    NewFlowKey[int] (val=plan ID) + NewFlowTag FollowUp. Oracles: (1) propagation —
    every launcher/accumulate/skim body asserts each enclosing scope's value
    present+correct and tag present (drain inside scope ⇒ skim handlers covered);
    flush bodies assert value SEVERED + tag PRESENT (union); (2) inheritance probes
    DYNAMICALLY at subjob entry (flush-descended subjobs legitimately see severed
    ancestor values — only the ctx knows the path; present ⇒ value must match =
    misdelivery check); (3) nominal end — follow-up fires EXACTLY once, only after
    drained flag, within Eventually(10s) (not inline-deterministic: the flush ctx's
    adopted refs release just after the wave barrier drops). Covers cancellation
    plans too (fn fires under teardown — refcount soundness under cancel).
    **SIM PAID OFF IMMEDIATELY: found a real CP-F1 gap** — limiter-bound dispatch
    from a top-level scope panicked in ensureCache/ensureCacheChain (nil wave on the
    scope meta; unit tests never combined limiter+scope). Fix: wave-less metas are
    TRANSPARENT to permit ancestry (walk to nearest wave-bearing meta; skip in the
    ancestor-chain walk) — wavepermits.go, + TestFlowScopeWithLimiter regression.
    Gate: vet, lint 0, full -short suite (ExampleFunnel flaked once under parallel
    load, 3/3 standalone — the documented real-clock-example class, not a
    regression), sim -short ×5 with oracle active, 40× -race batch (counting
    failures; expected ambient hit rate of the KNOWN pre-existing hang ≈1-2/40 —
    verdict recorded with signature check against the OPEN item above).
    CP-F5b (steps-only scopes + carrier-counter async-fire oracle) deferred — own
    checkpoint.
  - **NEXT: CP-F6 — FollowUp ERROR PROPAGATION (PN, 2026-07-04; REVERSES the CP-F2
    no-error signature decision — my "no wave exists" rationale was FALSE).**
    ★★★ FINAL API + SEMANTICS (PN 2026-07-04 — this block SUPERSEDES the detailed
    mechanism prose below wherever they conflict; the below is kept for the reasoning
    trail). ★★★
    IMPLEMENTATION PROGRESS (worktree, uncommitted): STEPS 1-3 LANDED GREEN.
    1 (KeyFollowUp/TagFollowUp interfaces + Func adapters + FollowUpFn sugar, value as
    arg, type-erased fn on flowInstance), 2 (fnRiders = prefix-minus-self peel +
    inner-holds-outer via flowInstance.holds released at the single fire's completion;
    firingPass/active/re-fire DELETED; fires exactly once), 3 (unref/fire return error;
    WithFlow named-return joins inline fires body-FIRST then LIFO). Tests added:
    TestFollowUpFiresOnce, TestFollowUpNestedCoupling, TestFollowUpInlineErrors; re-fire
    test removed. Root suite + 200-check -race sim green. NOTE: TestFlowTagFunnelUnion is
    a PRE-EXISTING flake (~1/40 broad, verified by stash-A/B vs base — same flushSawB
    signature; zero-deadline flush racing the 2nd accumulate; CP-F3 code untouched). NOT
    a CP-F6 regression.
    STEP 4a LANDED (async flush-model dispatch, WAVE-ROOTED — PN chose option 1 over
    item-rooted/Background, 2026-07-05): unref(inline,wave); async path takes
    wave.state.IncrementReference() at the unref site (sound — triggering item's work ref
    not yet dropped: releaseBodyContext wave.go:189 precedes DecrementWork :193), dispatches
    flowFireWork carrying the wave; flowFireWork.Execute stashes the scheduler ctx as
    borrowSrcCtx (funnel precedent), Run builds a skimContext fire meta bound to the wave
    rooted at borrowSrcCtx (NOT the recycled per-item ctx — that was the ctxpool-lifetime
    trap), runs fn via runFire, routes fn's error through package-level flowErrSink to the
    wave (funnelErrSink shape), then DecrementReference. wave threaded
    releaseBodyContext(capture m.wave before Put) → flowUnrefRiders(r,wave) → unref → fire.
    Consequence: the wave's drain now WAITS for the fire (keep-alive), so a follow-up on an
    outstanding-work flow fires as part of the drain — verified by the async unit tests
    under -race. Gate (40x rapid.checks=60 -race) running.
    STEP 4b LANDED (funnel flush defer reorder): flush() now computes flushCtx at :590
    without deferring its release there; registers `defer c.wave.state.DecrementReference()`
    FIRST (runs LAST) then `defer releaseBodyContext(flushCtx)` AFTER (runs before the
    barrier), keeping the panicked-emitErr defer registered LAST (runs FIRST — it reads
    ctx=flushCtx before the release frees it). So a tag follow-up fired from the flush's
    union-release takes its wave keep-alive while the funnel barrier still holds the wave
    open — no Done-wave IncrementReference. Test TestFollowUpErrorCrossesFunnel (erroring
    tag follow-up crosses a funnel, error surfaces via wave.CloseAndSkimAll) green -race 30x.
    STEP 4 COMPLETE (4a+4b). -race gate 40/40 on 4a; RE-GATE after the 4b funnel change
    40/40 (rapid.checks=60 each, 0 hangs). golangci-lint clean. CP-F6 COMPLETE — all 5
    steps green. NOT committed yet (PN commits on request). CP-F5b (sim async-fire
    oracle) and CP-F7/F8 remain separate future checkpoints; the sim currently fires the
    scope follow-up INLINE (plan drains inside the scope body), so async firing is covered
    only by unit tests until CP-F5b.
      FIRE-ONCE, NO RE-FIRE. A followup's own rider is PEELED before its body runs
      (fnRiders = prefix-MINUS-self = the enclosing set), so its dispatches don't re-ref
      it: it fires EXACTLY ONCE on count→0 (a single atomic transition — one winner).
      DELETES the firingPass loop, the active flag, the true-end-vs-extension recheck,
      and the missed-wake window (flowinst.go:93–116) ENTIRELY. Re-attachment (extending
      the flow under the followup's own identity) is an EXPLICIT re-stamp inside the
      body — opt-IN, so "no extension" is the safe default and users never have to
      remember Suppress to avoid accidental re-fire (PN). NESTED-LIFETIME COUPLING
      SURVIVES: the peeled body still carries the ENCLOSING instances, so its extensions
      ref them and every OUTER followup still waits for the inner's subtree (the peel IS
      the LIFO unwind, made literal). The inner-holds-outer ref releases when the single
      fire COMPLETES (inline: body returns; async: fire-task completes) — no true-end
      loop needed.
      VALUE DELIVERED AS THE ARG. A key followup gets its key's value directly (From()
      reads absent inside, since self is peeled — the arg is the honest channel).
      TWO UNIQUE INTERFACES + Func adapters + Fn sugar (NOT bare func; NOT Handler/Task —
      both carry a callerErr arg a followup has no analog for; dropped, not repurposed —
      a coherent "flow failed" value is inline-only, so disqualified). NAMES LOCKED (PN
      2026-07-05): method Do (not Handle — Handle/Submit take a NOUN to handle; a followup
      is intrinsically a VERB/action; sync.Once.Do fire-once resonance). Interfaces fully
      Flow-qualified for consistency with FlowKey/FlowTag (future non-flow keys/tags):
        key:  FlowKeyFollowUp[T]{Do(ctx,value T)error} + FlowKeyFollowUpFunc[T] +
              k.FollowUp(iface) / k.FollowUpFn(func(ctx,T)error)
        tag:  FlowTagFollowUp{Do(ctx)error} + FlowTagFollowUpFunc + t.FollowUp(iface) /
              t.FollowUpFn(func(ctx)error)
      (Follower+Follow considered & rejected — -er begs Follow, clashes with Do; kept the
      noun FollowUp type + Do.) Value delivered as the Do arg, named to MIRROR/EQUAL the
      key var (txn key → `txn` value, deliberately shadowing; peel makes From absent inside
      so the shadow removes only what you shouldn't reach for). Op-builder sugar (In/limits)
      NOT added: the fire-wave is dynamic, no meaning for a followup; own pass if ever.
      ASYNC DISPATCH unchanged from the flush-model block below (IncrementReference on the
      finished wave + global-pool dispatch + package errSink), MINUS the re-fire delta.
    `FollowUp(fn func(context.Context) error)`. Propagation: INLINE (scope-exit)
    firing's error joins WithFlow's return via errors.Join, body error FIRST
    (first-error-primary; multiple followups fire + join in INVERSE registration
    order — LIFO, defer-like unwind [PN 2026-07-04]; guarantee scoped to SAME-PASS
    firings — cross-identity async ends have no relative order; name stays FollowUp:
    Defer REJECTED, run-once echo contradicts extension/refire semantics);
    innermost-scope-first is COMPOSITIONAL (inner
    WithFlow's join is the outer body's error — no mechanism). ASYNC (post-return)
    firing's error routes to the TRIGGERING work item's wave via the errSink →
    surfaces through its drain like a body error; errSink submission keeps the wave
    alive like any skim work (PN). Arm-time wave ref is sound: item unref precedes
    its wave-ref drop — EXCEPT the flush-adopted-union release, which currently runs
    AFTER the barrier drop (LIFO defers in flush()); FIX: register the
    releaseBodyContext defer AFTER the barrier defer so it runs BEFORE it (safe: the
    flush body has returned; dispatches captured riders at admission). Tests:
    inline-error-joins-return (body+followup, multi-followup order), async-error via
    drain, extension-firing error routing, flush-triggered firing under live
    barrier. Reconcile flow-design.md (fn signature + this model) at the docs pass.
    MECHANISM SETTLED (2026-07-04, after a full design pass — SUPERSEDES the earlier
    "capture + IncrementReference + bespoke errSink + misattribution corner" sketch,
    which was me hand-reimplementing what an ordinary WAVE WORK ITEM already gives).
    The firing STOPS being wave-less. Two firing sites, keyed by the existing `inline`
    flag, which now means DIRECT-CALL vs SUBMIT-INTO-FINISHED-WAVE:
      • inline=true (scope exit): direct `fn(scopeCtx)` on the user's own WithFlow call
        frame (safe — their goroutine); its error joins WithFlow's return (body err
        FIRST, created instances LIFO).
      • inline=false (work-item completion): fire via the FUNNEL-FLUSH MODEL (PN chose
        flush as the model, 2026-07-04 — SUPERSEDES the "submit as an ordinary task"
        idea; a direct inline fn is still rejected: that site, releaseBodyContext inside
        taskWork.Free wave.go:189, runs on a pool worker mid-completion and a direct fn
        draining its own wave on a saturated pool would wedge). Lift the flush trio:
          – KEEP-ALIVE BARRIER: at count→0 inside the finished item's completion, its
            wave W is still live (item DecrementWork wave.go:193 runs AFTER
            releaseBodyContext wave.go:189), so take W.state.IncrementReference() there —
            the funnel barrier (funnel.go:733), just taken at true-end on a dynamic W
            instead of at accumulate on a fixed wave.
          – DISPATCH: hand the body to the global pool (the existing flowFireWork /
            ForceFresh path, retained), riding the finished item's ctx, carrying
            fnRiders (prefix).
          – ERROR ROUTING: a package-level flow errSink (funnelErrSink shape,
            funnel.go:75/505) submits the body's error to W → surfaces via W's drain;
            release the IncrementReference AFTER any downstream Submit so totalReferences
            never transiently zeroes (flush defer ordering, funnel.go:604).
        The earlier "capture across an executor hop + misattribution corner" framing is
        RETIRED — this is the IncrementReference pattern adopted wholesale from flush,
        not hand-rolled.
      STRUCTURAL DELTA (the two things a follow-up has that a flush doesn't; both handled
      by keeping existing machinery): a funnel instance is bound to ONE wave and fires
      ONCE; a follow-up's wave is DYNAMIC (whichever DAG item finished last) and it can
      RE-FIRE (extension → re-quiesce). So the IncrementReference is taken/released PER
      FIRING on that firing's W, and the firingPass/count/active state machine is RETAINED
      (fire-once-per-quiescence, true-end detection, nested-lifetime holds). Flush supplies
      dispatch+keepalive+errSink; firingPass supplies the re-fire logic.
      DROP context.Background rooting (flowinst.go:118): the firing rides the finished
      item's ctx; if it's cancelled that's the user's call (fn checks ctx.Err()). The
      follow-up body is a launchable Task internally (Handler[struct{}]) so the errSink
      path is the ordinary one; user-facing signature stays func(context.Context) error
      (wrapped) — a bare Handler only earns its keep if follow-ups take op-options, which
      they don't.
    NESTED-LIFETIME RIDER MODEL (PN confirmed 2026-07-04; REPLACES the singleton
    fnRiders — flat siblings were wrong, LIFO held only for same-pass inline firings):
      • A follow-up fires with the rider set AS OF ITS OWN REGISTRATION POINT (the
        prefix: all values + follow-up instances registered at or before it), NOT a
        singleton {self}. VISIBILITY half: fn reads the ENCLOSING values/tags it was
        registered under (reqKey.From in an audit follow-up), not only its own key.
        LIFO firing peels the stack one layer per firing (innermost fires first seeing
        the whole stack; each outer sees one fewer).
      • ORDERING half — inner-holds-outer: at registration, an inner (later-registered)
        follow-up takes a persistent ref() on EACH enclosing instance, held for its
        whole life, RELEASED AT ITS OWN TRUE END (firingPass count-still-zero branch,
        flowinst.go:99). So an outer's count cannot reach zero — cannot fire — until the
        inner has fully quiesced. This sequences LIFO even when firings go async (the
        prefix set alone gives visibility but NOT ordering: shared body carriers drive
        all instances to zero together otherwise).
      • EXTENSION COUPLING IS INTENDED: true end = count still zero after firing = fn
        fired AND nothing it spawned is outstanding, so an extension re-raises the count
        and defers true end (and thus the peel) until the extension's whole subtree
        quiesces. Every ENCLOSING follow-up waits, transitively (cascade one layer at a
        time). This is the defer guarantee made to hold across async extension — plain
        defer can't express it. Real coupling (a slow inner extension holds every outer
        follow-up open); the decoupling escape hatch is a SINGLE follow-up that itself
        submits N concurrent tasks (PN).
    Return threading: fire()→raw fn err; firingPass()→errors.Join of its fires;
    inline path returns it up through unref(true) to WithFlow; Submit path routes the
    firing task's error through the wave's ordinary errSink. WithFlow's scope-exit defer
    must release created instances in REVERSE registration order (LIFO). Flush defer
    reorder (releaseBodyContext AFTER the barrier defer so it runs BEFORE it) still
    applies. Tests: inline-error-joins-return (body+multi-followup LIFO order), async
    firing surfaces via the finished wave's drain, extension delays every outer
    follow-up (nested-lifetime coupling), enclosing value readable in fn, flush-
    triggered firing under live barrier. Reconcile flow-design.md at the docs pass.
  - **★ RIDER REPRESENTATION REDESIGN — spec at docs/decisions/flow-rider-chain.md.
    CHECKPOINT PLAN (foundation-first, each -race-gated): R1 chain-swap (no-op) → R2
    refcount+pooling → R3 FlowOption interface + fluent bundle → R4 surface reframe
    (FlowFollowUp/Infuse) → R5 rebuild sever (F8) + fan-in union (F7) → R6 definitional-tag
    coalescing (union-find) → R7 CP-F5b oracle + psgwf/otpsg disposition.**
    - **CP-R1 LANDED (2026-07-05, worktree — NOT committed; PN commits on request):
      representation swap, GC-owned still.** Flat `flowRiders{[]flowRiderEntry{id,val,insts}}`
      snapshot → linked `flowRiderNode{id,val,hasVal,inst,next}` chain, ONE binding per node,
      walked head→next on read. `flowInstance.fnRiders *flowRiders` → `enclosing *flowRiderNode`
      = the instance's own node's `.next` (peel is structural — self's binding lives on its node,
      the fire carries next). `rebuild(head,stop,keep)` primitive added (drives Suppress now via
      walk-to-root drop-by-id; funnel sever in R5). buildFlowRiders: value-only nodes emitted
      DEEPEST (globally visible to later follow-ups), then follow-up nodes in option order (later
      nearer head → LIFO peel); values settled by linear scan of opts (NO maps — was 3 transient
      maps, cut to keep the registering path lean). collectFlowTags/flowFanInContext/funnel
      `flowTags` → chain form (union prepends deduped-by-instance-pointer tag nodes). flowRefRiders/
      flowUnrefRiders walk the chain (each instance on ≤1 node/chain ⇒ one ref/instance). `refs`
      field NOT added yet (R2). Seams touched: flow.go, flowinst.go, ctxmeta.go (field type),
      funnel.go (flowTags type), bodyctx.go (unchanged logic, type flows through). NO test touches
      rider internals — clean blast radius.
      GATE: vet clean, golangci-lint 0, full -short suite green, all flow tests -race green
      (incl. concurrent stress + funnel union), alloc floors green, **40/40 TestBySimulation
      -race (rapid.checks=60, seeds 1-40) — 0 fails, 0 hangs, 0 races** (known pre-existing rare
      hang did not surface; ambient ~1-2/40). Value-only scope measured **4 allocs/op** (down from
      flat ~6), tag-followup scope 6; **alloc ceiling lowered 6→4** (flat's meta+snapshot+entries+
      ctxpool → chain's meta+node+ctxpool). These warm allocs are what R2 pools toward 0.
    - **TWO INTENTIONAL SEMANTIC DELTAS (untested corners; spec-intended; vanish under R3/R4
      surface — FLAGGED to PN, PN's read: let them change).** The chain shadows-and-walks where
      flat merged-then-peeled: (1) ancestor `key.Value(V)` + inner STANDALONE `key.FollowUp(h)`
      (old surface): inside h's fire flat peeled V (From absent), chain shows V (ancestor value
      node is in `enclosing`). (2) a value BUNDLED with a follow-up under the same id is invisible
      to an EARLIER same-scope follow-up (rides the follow-up node, sits above the earlier one);
      flat's global value pass showed it. Narrow: a PURE value option (no follow-up under its id)
      stays globally visible (emitted deepest), so only bundled values differ. Both untested; both
      = the spec's `enclosing = node.next` end-state; standalone key follow-ups + this cross-key
      visibility go away when R3 makes keys bundle value+follow-up on one node.
    - **CP-R2 SPLIT (risk asymmetry, PN-approved 2026-07-05): R2a = pool scope meta +
      instances (existing recycle points, low risk); R2b = node refcount + pooling (new
      refcount racing carrier ref/unref — the critical part). R2b will be designed with the
      FAN-IN UNION as a ref holder in mind (PN).**
    - **CP-R2a LANDED (2026-07-05, worktree — NOT committed): scope meta + instance pooling.**
      Scope meta now `bodyMetaPool.Get()` (was `&ctxMeta{}` GC-owned), freed at WithFlow return
      via a defer registered FIRST (runs LAST — after the inline follow-up fires, which root
      their own metas and never read the scope ctx): `ctxpool.Free(scopeCtx)` + `bodyMetaPool.Put`.
      Retain-of-scope-ctx is now UB (spec-sanctioned). flowInstance pooled via
      `flowInstancePool = omnipool.For[flowInstance]()` + a `Reset()` (needed because count
      atomic.Int64 carries noCopy — omnipool's plain-copy zero would trip vet copylocks);
      `flowInstancePool.Get()` in buildFlowRiders, `Put` at fire-complete (unref inline branch +
      flowFireWork.Run async). Safe gen-free: count→0 fires exactly once (CP-F6) and no live
      chain reaches the instance's own node after that (the count==0 ⇒ no-reader invariant), so
      no ABA guard — VALIDATED under -race, not just argued.
      TEST FIX: TestFlowAllocFloors read `key.From` INSIDE the scope now (it previously retained
      the scope ctx past WithFlow return to measure the read — now UB under pooling).
      ALLOC WIN: value-only scope **4 → 1** (just the GC-owned node; R2b pools it to 0),
      tag-followup scope **6 → 2** (node + the `created` slice). Ceiling lowered **4 → 1**.
      GATE: vet, golangci-lint 0, full -short suite, all flow tests -race (incl. concurrent
      stress + funnel union + async firing), alloc floors, **40/40 TestBySimulation -race
      (rapid.checks=60, seeds 1-40) — 0 fails/hangs/races**.
    - **CP-R2b LANDED (2026-07-05, worktree — NOT committed): node refcount + pooling, universal.**
      `flowRiderNode` gains `refs atomic.Int64` + a `Reset()` (noCopy) + `flowRiderNodePool =
      omnipool.For[flowRiderNode]`. Primitives (flow.go): `newRiderNode` (pool Get + downlink
      nodeRef on next), `nodeRef`/`nodeUnref` (nil-safe; unref cascades reclaim down `next`,
      panics on underflow = double-release). REF OWNERSHIP (mirrors the flowRefRiders/
      flowUnrefRiders sites exactly): a node refs its `next` (downlink); a CARRIER meta refs its
      head — borrowBodyContext / scope meta (WithFlow) / fire meta (unref inline + Run) / flush
      adopt; an INSTANCE refs its `enclosing` head from registration to fire-complete (decision B —
      REQUIRED for the async fire: enclosing must survive the gap between count→0-dispatch and the
      worker building the fire meta, else the cascade from the triggering carrier's nodeUnref
      reclaims it first). ensureCtxMeta derivations take NEITHER ref (synchronous, parent-covered).
      UNION (decision A — universal): collectFlowTags prepends pooled union nodes, moving the
      funnel's carrier ref old→new head; flowFanInContext ADOPTS it (no new ref; c.flowTags nil'd),
      released by releaseBodyContext at flush end. Universal (all nodes pooled incl. union) avoids
      a mixed pooled/GC chain whose reclaim cascade would corrupt at flush. Two parallel counts
      stay separate: instance.count (firing) vs node.refs (pooling).
      SAFETY: nodeUnref underflow panic (double-free = loud, not silent use-after-recycle); a
      test-only `flowNodeAllocHook atomic.Pointer[func(int)]` (+1 Get/-1 reclaim; nil in prod, one
      uncontended relaxed load) drives **TestFlowNodeConservation** (white-box) — asserts balance→0
      across inline nesting, async drain, and funnel union; a leak leaves it positive.
      ALLOC: value-only scope **1 → 0** — the warm per-flow allocation is fully retired
      (meta+ctxpool+node all pooled). tag-followup 2 → 1 (the `created` slice remains). Ceiling
      **1 → 0** (hard floor now). CP-R2 (a+b) COMPLETE: zero warm allocation for the value path.
      GATE: vet, golangci-lint 0, -short suite, flow tests + conservation -race (x5),
      alloc floors (0), **80/80 TestBySimulation -race total (40 pre-safety + 40 with the underflow
      panic in place; rapid.checks=60, seeds 1-40 each) — 0 fails/hangs/races/panics**.
    - **CP-R3 LANDED (2026-07-05, worktree — NOT committed): typed key follow-up via explicit
      value arg; FlowOption stays a value STRUCT.** The spec's "FlowOption→interface + fluent
      `key.Value(v).FollowUp(fn)`" design was BUILT, MEASURED, and REJECTED: it costs ~2 warm
      allocs per registering WithFlow (value-only 0→2). Root cause: a generic `valueOption[V]` in a
      heterogeneous variadic can only dispatch through an interface method (`applyToFlow`), and
      interface dispatch is OPAQUE to escape analysis — `go build -gcflags=-m` confirmed BOTH the
      `...FlowOption` variadic and the builder escape to heap. OpOption's interface is fine because
      op construction is COLD; WithFlow is WARM (per request-scope), so the fluent surface would
      forfeit CP-R2's zero-warm-alloc win. Go constraint: typed-fluent-followup ⟹ generic option ⟹
      interface variadic ⟹ heap. **Decision (PN, Option 3): keep FlowOption a struct; a key
      follow-up takes its value as an EXPLICIT first arg** — `key.FollowUp(v, h)` /
      `key.FollowUpFn(v, fn)` (typed `func(ctx, V)`, the generic on the FlowKey[V] receiver, not a
      boxed option). Same `FollowUp` verb as tags; value unambiguous (written right there, captured
      at registration); one option → one bundle node. No standalone valueless key follow-up, no
      fluent chain; `Suppress`/`NewFlow`/bare-key have no FollowUp method so `Suppress().FollowUp()`
      is unexpressible (type-safety survives without composed interfaces). PN vetoed "Bundle" (too
      generic) — FollowUp(v, …) reads right and composes with Fn. Internal: FlowOption gains
      `hasVal` (true for Value + key follow-up, false for tag follow-up/suppress/new-flow);
      settledVal reads the bound value off either. buildFlowRiders / refcount / firing UNCHANGED
      from R2b (surface-only). Spec `flow-rider-chain.md` Options section rewritten to record the
      rejection + Option 3. ALLOC: value-only scope stays **0**; key bundle 1 (the `created` slice,
      cold). Floor unchanged (0). GATE: vet, lint 0, -short (modulo the known real-clock
      Example_clientTimeout/ExampleFunnel flakes — pass 3/3 standalone), flow + conservation -race,
      alloc floors 0, **40 TestBySimulation -race runs clean (24 distinct seeds; on top of R2b's
      80/80 identical concurrency)**.
    - **CP-R4 LANDED (2026-07-05, worktree — NOT committed): anonymous follow-up + bare presence.**
      `FlowFollowUp(h)`/`FlowFollowUpFn(fn)` = anonymous DAG-scoped follow-up: mints a fresh unnamed
      tag-kind identity per call (no handle ⇒ neither InFlow-queryable nor Suppressible), fires once
      at the true end, crosses funnels. `FlowTag.Infuse()` = bare presence: a valueless,
      follow-up-less marker so InFlow reports the tag with no lifetime (complements FollowUp;
      Suppress clears either). New `flowOptInfuse` kind; buildFlowRiders emits a valueless
      follow-up-less node (skipped when a follow-up under the id already provides presence).
      PRESENCE CROSSES THE FAN-IN: collectFlowTags now folds bare-presence nodes into the union too
      — follow-up nodes still ref one carrier per distinct instance; a presence node (no instance)
      contributes presence once per distinct id with NO ref (membership has nothing to keep alive).
      Values still sever. (This extends the EXISTING union; the R5 boundary rework subsumes it.)
      Refcount/firing unchanged. Tests: anonymous fires once + crosses funnel (not before flush) +
      two-independent; Infuse presence in-scope + downstream-across-fan-in + fires-nothing +
      Suppress-clears; conservation test gains an Infuse + anonymous-follow-up funnel scenario.
      GATE: vet, golangci-lint 0, -short (modulo the known psgwf Example_clientTimeout real-clock
      flake — 3/3 standalone), flow + conservation -race, alloc floors, **TestBySimulation -race:
      39/40 seeds pass; seed 6 hit the KNOWN PRE-EXISTING hang ONCE (intermittent — passed 2/2 on
      re-run), signature conclusively pre-existing (6 goroutines, 4 parked skimSelect←
      addWorkWhileMaybeBlocking←WaitForNew, NO mutex/semacquire waiters, ZERO flow-rider frames —
      the permits/wake-chain missed-wake class from the OPEN item above, not flow).** 41/42 -race
      runs green.
    - **CP-R5 LANDED (2026-07-05, worktree — NOT committed): funnel fan-in on the chain — flush
      sees the enclosing driver (F8) + boundary-scoped tag union/sever.** Boundary = Option A
      (PN-confirmed): `flowBoundaryAboveWave(ctx, wave)` = the rider head of the nearest meta ABOVE
      the funnel's wave, captured at DISPATCH (funnelWork.Init, from submitCtx — the borrow severs
      parent) and carried on the funnel work. KEY FIX vs the naive walk: the parent chain is
      synchronous-only (an async body meta has parent=nil), so the walk stops at the LAST in-wave
      meta when parent severs — its riders ARE the enclosing driver's head it captured at ITS
      dispatch (`for m.wave == wave && m.parent != nil`). The instance adopts the boundary from its
      first accumulate: c.flowTags STARTS as the boundary (union chain's tail = enclosing flow), with
      nodeRef + flowRefRiders(boundary) so the single flush-time release (releaseBodyContext walks the
      WHOLE flush chain) balances and enclosing follow-ups survive to flush regardless of driver
      timing. collectFlowTags(union, ctx, stop=boundary): walks each item's chain, STOPS at boundary
      (pointer ==), folds tag-kind nodes ABOVE it (per-item tags cross, dedup scans only the folded
      prefix); per-item VALUES above the boundary drop = the sever. Enclosing (at/below boundary)
      shared intact. flush riders = folded tags → boundary → enclosing; flowFanInContext adopts,
      releaseBodyContext releases (node + instance refs) at flush end. c.boundary nil'd at takeover.
      SEMANTIC FLIP (CP-F8, as predicted): the enclosing/driver flow's VALUES now CROSS to the flush
      (were severed) — only per-item riders added WITHIN the funnel's wave sever. Tests: rewrote
      TestFlowSeverAtFlush → TestFlowFlushSeesEnclosing (driver value crosses via a launcher-in-wave
      per-item scope whose value severs, both flush drives); TestFlowTagCrossesFunnel flushSawVal
      false→true; sim oracle assertFlowInFlush severs→crosses (+ flowExpectsForCtx comment).
      GATE: vet, golangci-lint 0, -short (root+sim), all flow + conservation -race, alloc floors,
      **40/40 TestBySimulation -race (seeds 1-40, rapid.checks=60; 0 fails/hangs/races/underflows)**.
      NOTE: independent-flows-WITH-values (no common driver) would leak item1's value into the
      boundary — that is the R6 COALESCING case, not yet handled; tags-only union is correct.
    - **CP-F7 LANDED (2026-07-05, worktree — NOT committed): skim handlers are flow continuations.**
      A queued skim result is a CARRIER of its producing item's riders, not a fan-in. skimWork gains
      `riders *flowRiderNode`; `captureRiders(meta.riders)` at submit (both submit + trySubmit) takes
      node + instance refs (overlapping the item's own — never transits unreferenced); skimWork.Execute
      overrides `meta.riders = wk.riders` so the handler runs under the ITEM's chain (item-over-driver
      shadowing — the item descends from the driver, whose cancellation ancestry the handler still
      rides via wk.wave.ctxMeta); skimWork.Free releases (flowUnrefRiders + nodeUnref), balanced across
      all paths (Free fires once — dequeued+executed, or freed-if-never-posted per skimPostWork.Free).
      REVERSES the CP-F3 "skim is not a fan-in edge" note: a tag follow-up on the item's flow now must
      NOT fire while the result awaits skimming — the skimWork's refs hold it until the handler
      completes. Applies to internal errSinks too (harmless — they read no riders; refs balance).
      Test: TestFlowSkimContinuation — skim handler sees the producing item's value (not just the
      driver's) + item tag present + the item follow-up fires only AFTER its result was skimmed.
      GATE: vet, golangci-lint 0, -short (root+sim), all flow + skim + conservation -race, alloc
      floors, **40/40 TestBySimulation -race (seeds 1-40, rapid.checks=60; 0 fails/hangs/races)**.
    - **CP-R6 SURFACE SETTLED (PN, 2026-07-05): no new `OnFlowEnd`. `FlowFollowUp(h)` is just an
      option carrying the handler; the CONSUMING CONTEXT binds the identity — `WithFlow(…,
      FlowFollowUp(h))` mints a fresh anonymous id (per-scope, R4), `NewFlowTag(FlowFollowUp(h))`
      binds h to the TAG's identity = the definitional follow-up. Coalescing IS how fan-in is really
      handled (not deferrable). Split: R6a shared-chain definitional (no union-find) → R6b cross-
      funnel coalescing.**
    - **CP-R6a LANDED (2026-07-05, worktree — NOT committed): definitional tag follow-up, shared
      chain.** `flowIdentity.definitionalFn` (bound by `NewFlowTag(FlowFollowUp/FollowUpFn(h))` —
      validates a valueless follow-up option, at most one). `flowInstance.definitional` marks it
      (Reset zeroes). buildFlowRiders: a definitional tag's `Infuse()` is skipped in the presence
      loop and handled in the follow-up loop — mints the definitional instance UNLESS a definitional
      instance for the id is already on the chain (walk `head`; enclosing infusion or an earlier one
      this scope ⇒ idempotent, mint nothing). Fires ONCE per flow; R5's pointer-dedup handles fork/
      reconverge; crosses a funnel once (folds/boundary-seeds like any tag follow-up). NOT coalesced
      across INDEPENDENT flows yet (2 fires — R6b). Tests: TestFlowDefinitionalFollowUp (once +
      idempotent-across-N-infusions + funnel-cross-once + not-before-flush), TestFlowDefinitionalTag
      Panics. GATE: vet, golangci-lint 0, -short (root+sim), all flow + conservation -race, alloc
      floors, **39/40 TestBySimulation -race (seed 6 = the KNOWN pre-existing hang, intermittent,
      passed 2/2 on re-run; skimSelect/WaitForNew signature, zero flow frames)**.
    - **CP-R6b LANDED (2026-07-06, worktree — NOT committed): definitional coalescing, union-find.**
      See the CORRECTED-MODEL banner up top (funnel INSTANCE = one aggregated flow; coalesce only
      co-accumulated flows; count is nondeterministic by design). Mechanism (flowinst.go "Coalescing
      (CP-R6b)" section): `sharedNode{parent,refs int,holds}` union-find hierarchy pooled via
      `sharedNodePool` (+`flowSharedAllocHook` conservation seam); `flowIdentity.mergeMu` (per-tag
      serial lock); `flowInstance.{id,shared}` (Reset zeroes). `mergeDefinitional` (link two live
      instances/roots under a fresh parent; idempotent no-op if same root; ref-before-release ⇒ no
      merge-vs-death race), `derefShared` (cascade + holds-migrate-up + underflow panic), `coalesceAtZero`
      (at count→0 under mergeMu: shared==nil→fire solo; merged-not-last→step aside, migrate holds,
      release enclosing, recycle; merged-last→adopt component holds, fire once). Merge site =
      `collectFlowTags` (walks the WHOLE union incl. boundary tail — the driver flow's own definitional
      instance rides there, never folded). Tests: TestFlowCoalesceMechanism (deterministic proof),
      TestFlowDefinitionalCoalesce (robust black-box), TestFlowCoalesceConservation (concurrent deref +
      shared/node conservation). GATE: vet, golangci-lint 0, full suite no-race (incl alloc floors),
      all flow/coalesce -race ×20+, **700 TestBySimulation -race checks (400+300, 0 fails)**. Full sim
      ORACLE modeling of coalescing deferred to R7.
    - **►► NEW OBSERVATION: rare pre-existing funnel borrowSrcCtx -race (distinct from the hang).**
      Seen ~1/400 in TestFlowDefinitionalFollowUp AND the flow suite: `funnelInstance.Run` →
      `borrowBodyContext` → `metaFromContext` READS a ctxpool child's value while an execpool worker
      `ctxpool.(*child).Free()` WRITES it (use-after-free of the flush's scheduler ctx / borrowSrcCtx).
      NOT R6a — R6a touched NO funnel code (flow.go/flowinst.go only); the identical non-definitional
      TestFlowFollowUpAnonymous exhibits the same pattern. Funnel/permits-thread infra bug (funnel.go
      borrowSrcCtx lifecycle — the handoff happens-before vs the scheduler freeing the ctxpool child).
      Hand off with the hang dossier.
    - **NEXT: CP-R6b — cross-funnel coalescing (union-find under a per-tag merge lock).** The R2b-class
      risk: sharedNode hierarchy (parent + refs), per-tag merge lock, merge at collectFlowTags (funnel
      = only merge site), find-to-root/same-root-no-op/live-operands-via-ref-before-release, deref
      cascade → root fires once, flush links a downstream instance into the component. DESIGN CARE +
      heavy -race sim + shared-node conservation. Then R7 CP-F5b oracle + psgwf/otpsg. ALSO the R5
      independent-flows-with-values boundary gap is separate (not R6).
  - **ORIGINAL REDESIGN SPEC NOTES (PN + design session, 2026-07-05; CONVERGED).** Replaces the flat COW
    flowRiders snapshot + per-instance fnRiders with a **refcounted, pooled linked chain**
    of one-entry nodes; **walk on read** (same complexity as the flat scan). Reshapes/SUBSUMES
    CP-F7 + CP-F8 (the funnel sever becomes a bounded chain rebuild = F8; skim-as-continuation
    stays) and supersedes CP-6's flat model, `fnRiders`, standalone `FlowKey.FollowUp`, and the
    FlowTag-centric follow-up surface. Key decisions locked this session:
      • Node = one entry (id, val, hasVal, inst) — a key's value+follow-up on ONE node; refcount
        per node, reclaim cascades (node refs its next; carriers ref the head); instance points
        at its parent node (peel free, no fnRiders).
      • FlowOption becomes an INTERFACE (like OpOption, 0-alloc via escape analysis + per-kind
        concrete types); composed FlowKeyOption[V] embeds it so the fluent `key.Value(v).FollowUpFn(fn)`
        bundles value+follow-up as ONE option → one node. Key follow-ups exist ONLY via the fluent
        form (value never ambiguous). Method verb is **Do**.
      • Surface reframed: **FollowUp is the primitive**, key/tag are qualifiers. `FlowFollowUp`/
        `FlowFollowUpFn` = anonymous DAG follow-up (default). `tag.Infuse()` = bare presence (valueless
        marker, no lifetime — new; CP-6 had no way to tag without a follow-up). Decision table +
        "value severs / lifetime & presence cross" as the teaching frame.
      • **Definitional tag follow-up** (attached to the tag's identity) fires ONCE per flow regardless
        of infusion count; complements (does NOT replace, PN) per-scope tag.FollowUp. Coalescing of
        independently-infused flows at a funnel = **serial union-find under a per-tag merge lock**
        (find-to-root, same-root no-op, live operands via the accumulate ref-before-release, funnel is
        the only merge site, flush links a downstream instance into the component). This is the highest-
        risk concurrent structure — gate hard.
      • Fan-in union: boundary = driving flow's chain head captured at funnel dispatch (shared intact =
        F8); collect-to-boundary per item; markers dedup by id, instances by pointer; materialize at flush.
      • Pooling: scope meta freed at return (retain-of-scope-ctx is UB, same as every framework ctx);
        nodes reclaimed by refcount; instances recycled at fire (gen-free). Retires both warm per-flow allocs.
      OPEN (impl-time, non-blocking): holds-off-the-walk (derive inner-holds-outer from the chain vs
      materialize — safe version known), on-demand read map (deferred until measured), generic-option
      0-alloc verification (escape analysis + benchmark). BUILD = fresh-session, multi-checkpoint, each
      -race-gated; supersede CP-6's flat surface as part of it.
  - **THEN CP-F7 — SKIM HANDLERS ARE FLOW CONTINUATIONS (PN, 2026-07-04; REVERSES
    the CP-F3 "skim is not a fan-in edge" note — my gloss was wrong).** A queued
    result is a CARRIER: skimmer.Submit captures the item's riders (ref at submit),
    the handler runs under them stamped as a CHILD of the driver's ctx (normal
    shadowing — per-key nearest-wins, ITEM over driver; no merge mechanism exists
    or is needed), release at handler end; handler dispatches extend the flow. A
    tag follow-up must NOT fire while results await skimming (reverses the CP-F2/F3
    behavior). NOT a fan-in: each invocation continues ONE item's path — values flow
    through. IMPLEMENTATION WRINKLE (mine): From() reads the nearest FLAT snapshot,
    sound only because every snapshot is merged at construction; the skim stamp
    must restore that invariant — either merge item-over-driver at stamp
    (per-invocation cost) or teach reads to walk meta.parent (then the flush sever
    clone must be an explicit BARRIER — a walking read must not see through
    riders=nil). Decide by alloc floors. Sim oracle: whole-run scopes coincide on
    both chains (CP-F5a assertions survive); add a divergent-chain oracle with
    CP-F7.
  - **THEN CP-F8 — FLUSH SEES THE ENCLOSING CHAIN (PN, 2026-07-04): the fan-in
    sever applies ONLY to per-item riders.** A flow-A body driving a subwave
    containing flow-B's funnel: Flush must still see A's values AND tags — A is
    invariant structural context ABOVE the fan-in (not part of the per-item
    ambiguity). Semantics: flush riders = enclosing-chain riders (ordinary
    inheritance) + item-tag union; item VALUES still sever. Current impl anchors
    the flush borrow at the scheduler ctx (rider-free) / triggering item — WRONG
    anchor. ANCHOR SETTLED (PN): NO capture — walk the meta parent chain up to the
    first meta ABOVE the funnel's wave; its flat snapshot IS the enclosing chain
    (merged-at-construction makes it one pointer read; sever-per-item and
    inherit-enclosing are the same act: take the boundary meta's riders, not the
    item's). CAVEAT (from CP-F1's own design): body metas have parent=nil — the meta
    chain is SYNCHRONOUS-ONLY (permit severing, load-bearing). FIX: resolve the
    boundary AT DISPATCH (submit ctx's chain is intact there): walk above the
    target wave, stamp the enclosing snapshot as ONE extra riders-only pointer on
    the body meta (currentHeldPermit walks meta.parent, a different field —
    permits untouched). Flush reaches the enclosing chain in one hop from any
    item; the executor-path borrowSrcCtx problem dissolves (boundary snapshot is
    invariant across a funnel's items by construction — take it from any).
    Think through wave reuse + multiple drivers. Sim oracle: ancestor expects in
    flush bodies flip from severed to PRESENT for values once this lands (adjust
    assertFlowInFlush).
  - **THEN CP-F5b** — sim model extension: flow scopes/followups in
    internal/sim scenarios + conservation oracle (every registered follow-up fires
    ≥1 and reaches true end after its subtree quiesces; no fire while carriers
    outstanding). Own model-design pass. Then the psgwf/otpsg disposition pass
    (below).
  - **psgwf/otpsg DISPOSITION SETTLED (PN, 2026-07-04; supersedes the audit-only
    framing below): psgwf = FULL DELETE after CP-F6–F8 land**, gated on a
    per-symbol audit (anything lacking a flow equivalent gets SURFACED, not
    silently dropped — pin.go semantics unread), examples ported as flow examples
    (kills the Example_clientTimeout flake). **otpsg = REBUILD AS V2 ON FLOWS, not
    shrink**: an otel span's lifecycle maps exactly onto a flow — span starts at
    scope entry, rides as a flow VALUE (child spans/log correlation in every
    body, fan-in semantics per tags), and span.End() is a FOLLOW-UP firing at the
    flow's TRUE END (covers async work outliving the handler; extension = honest
    span extension — previously inexpressible). V2 ≈ one helper returning
    []FlowOption (span value + end follow-up). propagation.go/tracing.go deleted
    as subsumed; metrics.go/logging.go/instrumented.go audited (keep iff they
    instrument ops in ways flows don't touch). Original recon notes:** psgwf's own doc.go
    is the flow facility's job description ("workflow context propagation…
    cancellation domains and context values that flow through PSG task chains") on the
    dead vocabulary — HIGH-confidence delete once follow-ups land; audit each exported
    symbol for a flow-native equivalent, port examples worth keeping as flow docs
    (kills the Example_clientTimeout real-clock flake with it). otpsg is PARTIALLY
    subsumed: propagation.go/tracing.go = what flow values do natively; but
    metrics.go/logging.go/instrumented.go = op instrumentation flows don't replace —
    decide shrink-to-instrumentation-core vs delete-and-compose. Check
    internal/benchapp/funnel.go's reference. Isolated module (own go.mod), so removal
    is clean either way.
- **Ontology: "flow" = the causal DAG itself** (nodes = work items; edges = submits + the
  funnel accumulate→flush fan-in). Two rider kinds propagate along it, split by ONE property
  — whether a merge operator exists at fan-in:
  - **Values (no canonical merge) = PATH-scoped**: verbatim inheritance along chains
    (including nil/empty — NO top-level default; initiation is always explicit), SEVERED at
    fan-in (a flush body reads nil — the truthful signal), re-asserted only by the fan-in
    owner (funnel-owns-aggregation: collect per-item in accumulate, pick/union/start-fresh
    in user code). Framework-collected value sets REJECTED (unbounded ctx pinning,
    per-window alloc, unanswerable dedupe).
  - **Lifetime / after-funcs (counting monoid) = DAG-scoped**: refs union through EVERYTHING
    by default, incl. funnels — an accumulate item's refs TRANSFER to the funnel instance at
    completion, the flush item takes over the instance set (never-transit-unreferenced, the
    W2a depositOccupy lesson), downstream submits inherit before the flush item releases.
    Shrink only at explicit suppression.
- **Surface: ONE function — `streampool.WithFlow(ctx, body, opts...)`** (evolution: Exec →
  Flow → WithFlow; no non-flow use case can exist — everything scope-expressible is a DAG
  rider, incl. the deferred priority feature). Runs body inline on the CALLER's goroutine
  with a flow-stamped pooled ctx; the lexical scope IS the flow root. **FINAL NAMING
  SCHEME (PN, 2026-07-03 — supersedes the same-day With*-option family):**
  - **Constructors (scoping declared by NAME, not sizeof)**: `NewFlowKey[V]()` =
    PATH-scoped key (any V incl. struct{} — a path-scoped marker severs);
    `NewFlowTag()` = DAG-scoped, STRUCTURALLY valueless (no type param, no value slot ⇒
    data-bearing DAG keys stay UNREPRESENTABLE — the guard survives without the sizeof
    rule). The sizeof(V)==0 scheme was agreed then REJECTED same-day (PN worry, valid):
    struct{}→bool refactor silently flips scoping; rule invisible at use sites; generic-V
    spooky. Explicit constructors keep the type-level guarantee, kill the cliff.
  - **Options are METHODS ON THE KEY/TAG** (kills the With/Flow prefix stutter; maximal
    static typing): `FlowKey[V].Value(v V)` (compile-time key→value binding),
    `.FollowUp(fn)` (both kinds; name chosen over After [context.AfterFunc fires on
    CANCEL — harmful echo], Close/Commit/Cleanup/Done/End — FollowUp teaches the
    extension semantics: the handler IS potentially more flow), `.Suppress()` (per-key
    targeted suppression; the free-function WithoutFlow is REDUNDANT and DROPPED).
    FlowTag has NO Value method — type system enforces valuelessness.
  - **`streampool.NewFlow()`** = the one package-level option: suppress-all reframed as
    fresh-flow-root (positive intent-naming; clears the INHERITED set only,
    order-independent vs sibling adds; "New" here is semantic — a new flow — accepted
    over the New*-constructor-convention nit).
  - **Read family settled: `key.From(ctx) (V, bool)`** (comma-ok; XFromContext idiom in
    method form; `Value` was taken by registration — good, reads shouldn't look like
    writes) + **`tag.InFlow(ctx) bool`** (presence, ORs through fan-ins). InFlow names
    the TWO-HOP structure (PN: the tag is on the FLOW; the ctx merely contains/reaches
    the flow — bare prepositions collapse the hops and land the tag on the wrong
    object); both parses converge true ("is checkout in the flow of ctx" / "is ctx's
    work in the checkout flow"); rhymes with the WithFlow/NewFlow* family. Accepted
    demerit: "inflow" noun homograph (visually broken by camelCase). Rejected en route:
    Tags [plural-noun misparse — PN], Tagged/Marked/Labeled [participial dodge, passed
    over], In/On [wrong object per two-hop], IsTagOf [correct, awkward],
    Contains/Covers/Reaches [math/CS-y — PN], Describes [tags are informationless],
    Active/Underway [claim untracked state]. Reads consult the nearest meta's rider
    set; (zero, false) on never-stamped ctxs — no panic.
  - **Declaration naming CONVENTION (PN, docs-borne, taught by examples): NO Key/Tag
    suffixes** — a KEY is named for the VALUE it carries (`requestCtx`, `tenant`, `txn`:
    every use site reads as a sentence about the value — txn.Value(t), txn.From(ctx),
    txn.FollowUp(commit)); a TAG is named for the FLOW it identifies (`checkout`,
    `ingestion`: checkout.InFlow(ctx), checkout.FollowUp(fn)). Enabled BY the method-shaped
    API (receiver position gives the noun its grammatical role — free functions would
    have needed the suffix back). Accepted caveat: noun keys can collide with the
    natural local for a read result (`txn, ok := txn.From(ctx)` is legal-but-ugly
    shadowing; users pick a short local) — traded for call-site readability where it
    counts.
  - Call shape: `streampool.WithFlow(ctx, body, requestCtx.Value(r.Context()),
    checkout.FollowUp(commitFn), audit.Suppress())`.
  **UNIFIED KEYSPACE + SCOPING-AS-KEY-PROPERTY (PN, 2026-07-03)**: one identity
  namespace; a key's bundle {value?, followups...} propagates AS A UNIT under the key's
  scoping — a follow-up scoped to a value's lifetime = register both under one path key
  ("fires when all work CARRYING the value completes" — release-the-carried-ctx case);
  commit-through-the-funnel uses a FlowTag (refs union through fan-ins; presence
  readable through fan-ins — the "tag" semantics). Genuinely different lifetimes = two
  keys, deliberately. Earlier same-day "type/instance orthogonality" bullet: instances
  stay internal as recorded; "types" therein = these keys/tags. Docs-pass details:
  follow-up fn optionally receiving the bundle value (typed handoff is free under the
  bundle). Ops keep bare `Submit(ctx, v)` — NO submit options, NO handles, NO
  lifecycle objects. Zero-opt WithFlow degenerates to `body(ctx)`. The mid-session
  singular WithFlow(ctx)-submit-option/`FlowFromContext` idea = sugar over one built-in
  key (or dropped — doc pass decides; NB the name WithFlow now belongs to the scope
  function). **`WithFlow` is OPTIONAL (PN): flows aren't created, they're always already
  there** — every bare Submit roots/extends the DAG with the ambient (possibly empty) rider
  set; the function only opens a lexical extent with a MODIFIED rider set; wrapping a
  single Submit = per-dispatch registration. Docs framing: "most programs never call it"
  (and are still fully in flows when they don't). Interop: a flow value is any user value incl. a live request ctx; otel
  spans propagate through foreign wrappers untouched (nearest-meta Value walk, same as
  ambient-wave resolution).
- **Type/instance orthogonality**: minted TYPE identifiers (cold-path; shared across many
  flows or unique per flow — the user's granularity dial) are the SHAPING identity
  (suppress / read / mix-and-match). INSTANCES (pooled gen-stamped state, one per
  registration) are the LIFETIME identity and are FULLY INTERNAL — no user handle.
  Same-type instances NEVER auto-merge (would weld concurrent requests sharing a
  package-level type).
- **Lifetime semantics**: the scope's own ref covers entry→return, so the attach window is
  race-free LEXICALLY (parent-covers-children; the early-fire multi-root race is
  unwritable). Multi-root = several submits in one scope — never special. Empty scope fires
  at return. Count-zero after scope exit = NOMINAL end → afterFn fires; the afterFn's own
  dispatches inherit the firing instance ambiently (framework provisional ref bridges
  fire→admission) → extension = a later nominal end fires again; TRUE end = a firing that
  extends nothing. Rejected en route (do not relitigate): NewFlow-returns-ctx object w/
  Dup/Close + gen-stamped user handle; op builder `.As(flow)` + use-site stacking;
  initiator-held ref + Close for multi-root (superseded by lexical root closure);
  per-registration suppression HANDLES (couple the suppression site to the registration
  site — replaced by types); framework auto-merge by type.
- **Cancellation stance**: riders are pure values — NO framework cancellation derives from
  a flow value; bodies consult a carried ctx's `Err()` explicitly. AfterFunc-on-cancel
  stays user-space on the user's own ctx; nominal-end events are the framework's. The
  completion hook is REQUIRED (PN): "commit upstream txn at flow end" and "cancel a carried
  ctx once unreferenced" are both just things fn does at nominal end — one mechanism.
  TODO.md:79 (joined-context adapter) RESOLVED: nothing merges cancellation scopes — not
  needed.
- **Cost model**: one rider-set pointer in the pooled ctxMeta; inherit by pointer (zero
  cost); pooled COW node only at registration/suppression edges; per-funnel-instance small
  multiset (dedupe by identity + count, the bindings-slice alloc pattern); types minted
  cold; reads = small linear scan; opts copy-out-never-retain (the verified WithLimits
  0-alloc discipline).
- **PANIC STANCE SETTLED (PN, 2026-07-03): the framework NEVER recovers.** The
  "recovered and surfaced as an error" claims (doc.go, programming-model.md ×2) were
  unimplemented fiction from the original design-doc drop (5160c50) — zero recover() in
  production code; funnel.go's panicked-sentinel defers are cleanup-on-unwind, not
  recovery; TODO.md:202 already presumed propagation. Claims STRUCK this session
  (doc.go + programming-model.md now state propagate + accounting-sound unwind +
  recover-in-your-own-body). Flow's body fn is therefore UNIFORM, not exceptional:
  plain function call on the caller's goroutine, panics propagate, error returned
  verbatim, NOT a dispatched work item (no wave membership / permits / backpressure);
  scope refs release via defer so a panicking scope stays conservation-sound; body ctx
  valid for the duration of the call (existing body-ctx escape contract).
- **CTX ROLES SETTLED (PN, 2026-07-03): Flow's ctx param = EXECUTION ancestry** (ambient
  rider inheritance + cancellation for dispatched work: a body ctx when nested, a stable
  app/base ctx at top level); **a request ctx enters as a flow VALUE** (`WithValue(reqKey,
  reqCtx)`) — data, consultative only (.Err()/deadline/span read by bodies), NEVER a parent
  of framework ctx derivation. Consequence: the fresh-parent pooled-ctx seam is NOT
  APPLICABLE to Flow (parents are pooled body ctxs / stable base ctxs — populations ctxpool
  already amortizes); the delegating-parent custom ctx idea is shelved alongside TODO.md:79
  (same reason: nothing derives from a fresh ctx). Residual, pre-existing + orthogonal:
  fresh reqCtx passed directly to a top-level Submit pays ctxpool's one-time
  childPool+AfterFunc on first touch. Cancellation model unchanged: request cancellation
  stops bodies only if the request ctx is in the execution ancestry (user's explicit
  choice + cost).
- **Open queue**: (3) verify opt alloc discipline (variadic +
  boxed payloads stay on stack); (4) naming SETTLED IN FULL (PN, 2026-07-04): NewFlowKey[V]/NewFlowTag stay a
  pair — no further unification; all option/function/read names settled (see surface
  bullet); (5) docs pass DONE (2026-07-03): new
  `docs/decisions/flow-design.md` (definitive record incl. the full rejection trail; its
  "Open details" section carries the follow-up-fn-receives-value sugar decision as OPEN,
  plus items (3)/(4) here); reconciled API_DESIGN.md (banner bullet + superseded notes on
  the Flow model item / API block / example), programming-model.md (Wave is THE
  user-facing type; flow facility framed designed-not-implemented), surface-lineage.md
  (new facet 9: Flow handle → flow facility), TODO.md:79 (RESOLVED — no adapter needed).

**►► OPEN FOR A FUTURE SESSION (PN, 2026-07-04): the RARE PRE-EXISTING SIM HANG.**
Missed-wake wedge, ~1/2600 ambient -race checks, reproduced on pre-flow base 300576b
(NOT flow); full dossier + dumps + VALIDATED repro recipe (~1/300 checks: -race,
default SelfTimes, Subjob.Add probs 0.3-0.5) in the flow block's "OPEN: RARE SIM
HANG" item below. Leading suspect: the W2b-i/ii wake-chain rewiring. Nobody is
actively on it; whoever picks it up starts from the recipe + the
sim-trace-debugging skill.

**►► CHALLENGE FOR THE PERMITS THREAD (from the flow session, 2026-07-04): does
Demand really need its gen-stamp?** Principle established while reversing the flow
instance-pooling decision (see the flow block): ABA/gen machinery is warranted only
where references outlive ownership BY DESIGN (untracked readers — rdvq inbox/outbox
hints, proven); it is waste where a conservation discipline tracks every reference
and recycle happens at a proven-quiescent point (body metas, heldPermits, funnel
shells, flow instances — all correctly gen-free). Demand's justification is the
deliberately-racy readers (barrier atomic.Pointer[Demand] loads, in-flight mailbox
wakes) — legitimate; BUT if demand recycle can be deferred to a tracked-quiescence
point (FIFO/barrier provably dropped it AND the wake-compensation window closed —
the counts-before-wake ordering may already be most of that proof), the gen could
go. Re-derive rather than assume.

**►►► WEIGHTED ACQUISITION — design recorded (2026-07-02); STEP 1 (mechanical weighting) LANDED
(2026-07-03): counts deltas take w, Cache.Acquire(w)/AcquireWait(ctx,w), Permit.weight,
searchList(l,w) [borrowable≥w — no w>1 spin], acquireInto single-source all-or-nothing at w; all
callers pass 1. Gate: build/vet/-short suite/permits -race incl rapid/25×25 TestBySimulation -race.
Steps 2-4 (gather+barrier, resource capabilities, surface) NOT implemented.
**OVERDRAFT designed + recorded (PN, 2026-07-03, weighted-acquisition.md §Overdraft):** armed +
zero-inUse = free exact infeasibility proof (retires capacity-visibility); ancestor-exempt trigger
(strangers block; ancestors resume causally after head); OverdraftResource{Overdraft(n) (granted,
err)} — policy only, resource-authored refuse errors, default-GRANT for non-implementing holdables,
consumables must implement (no pool-side proof); representation = POOL-LEVEL ALLOWANCE, d never in
held/checkedOut (conservation untouched; inUse>held cache-locally while granted; occupy claims /
release returns excess via CAS-local delta of max(inUse−held,0)); HEAD STANDS until completion
(seriality across park gaps + arrival blocking; allowance necessarily home at completion);
descendant shortfall ⇒ EPISODE EXTENSION (same call, added to aggregate, clears at ORIGINAL head
completion; refuse ⇒ unit error, no wedge); CONSUMABLES (PN, 2026-07-03 refinement): NO
consumable overdraft — grant-by-negative ≡ TryAcquire past zero, internal policy invisible to the
pool (OverdraftResource is HOLDABLE-ONLY); the consumable mechanism IS the shared sticky-head+FIFO:
w≥2 TryAcquire miss registers, barrier check in the pass-through gate blocks all acquisition while
armed (protects the accrual from w=1 racers), head woken by `NotifyAt(n) error` (reachable n = one
exact timer + sub-target suppression; unreachable n = resource-authored refusal error — feasibility
+ wake in ONE call), head dequeues AT ADMISSION (standing head is the holdable episode mechanism).
Weighted consumables must implement NotifyAt (needed for chatter fix anyway).
**MULTI-LIMITER × FIFO (PN, 2026-07-03):** single-limiter liveness induction INVALID under joint
admission — the mid-sequence hold (joint acquirer holds A un-lent while blocked at B's barrier) is
a new blocked-holding state; canonical order rescues (blocked-at-L ⟹ holds only <L, edges point
up-order, acyclic; descending induction). Policy pinned: mid-sequence holds NOT lent (lending ⇒
re-take A after B = down-order wait, reopens cycle + breaks atomic joint admission). Overdraft
proof already correct (mid-sequence holds ARE inUse — don't "fix"). Consumable barriers can't join
cycles AT ALL (PN): head satisfaction is time-driven not release-driven + dequeue-at-admission ⇒
barrier never stands through work; consumables-last ⇒ induction base trivially live.
KNOWN FLAKE (pre-existing by construction, surfaced during step-1 commit): psgwf
Example_clientTimeout — real-clock golden (10-20ms time.Sleep margins) where a worker's "task
completed" print races main's "skimming results" under machine load; ~1/15 under parallel
full-suite runs, 0/200 standalone. Not a step-1 regression (w=1 arithmetic identical; the flip is
pure goroutine scheduling). Fix candidates when picked up: wider margins, deterministic
ordering, or sim-clock drive.**
`docs/decisions/weighted-acquisition.md` (companion to limiter-resource-classes.md): counts layout
is weight-ready, ops are weight-1. Core: (1) gather-into-own-`held` + atomic occupy — a partial
gather is NOT hold-and-wait (hoard stays borrowable ⇒ "parked ⟹ borrowable" proof intact;
cache-don't-return IS the rollback, no give-back protocol); (2) demand-side head-of-line barrier
(PN): Pool-level FIFO of caller-held invalidatable demand identities, **sticky head, FIFO
succession, NO weight-based ordering** (max succession rejected — biases toward large demands);
gathering is HEAD-ONLY ⇒ gather-vs-gather livelock unrepresentable; barrier must gate steps 1–4
incl. acquireLocal (one atomic load, mirror of the release-side balance load) else step-1
recirculation starves the head invisibly; (3) arm only w≥2 — weight-1 never registers, mechanism
dormant for semaphore/rate pools; (4) identity = conservation token (satisfied-or-invalidated;
caller-held to dedupe postpone retries; gen-stamp for ABA). Supply-side reservation (x/sync-style)
rejected — breaks the liveness proof; demand-side barrier reaches the same fairness without it.
Weighing SURFACE already settled (dispatch-execution-split.md: static per-op panic / data-dependent
per-item unit error; "applicant" backlog note). Needs: TryAcquireUpTo capability (partial grants),
capacity visibility (infeasibility BEFORE arming), stealOutUpTo. Sequencing: mechanical w=1-caller
weighting (no-op, green) → gather+barrier behind model check → resource capabilities → surface
plumbing (own session). **SURFACE SETTLED (PN, 2026-07-02, recorded in the doc):** variadic builder
methods on the op type — `Launcher[T].WithLimits(...Limiter)` / `.WithWeightLimits(...WeightLimiter[T])`
+ `NewWeightLimiter[T](l, weigh)` constructor (New* consistency; Limiter→Limit general rename
REJECTED — "limit" = numeric ceiling throughout the package, type would collide; compile-time T via
receiver's param; `With` prefix = http.Request.WithContext copy-semantics convention;
WeightLimiter[T] = reusable same-T binding;
variadic slices stack-allocate IFF methods copy-out-never-retain — verified 0 allocs incl. multi-arg;
Funnel adds WithFlushLimits). Both methods compose + repeated calls ACCUMULATE (variadic = pure
sugar; enables base-op layering); one binding per limiter TOTAL across both methods (dup panics);
replace/last-wins + removal affordances REJECTED (silent constraint-dropping). Multi-limiter
representation (PN, final): every With* call COPIES into op-owned storage (1 construction alloc per
call) — adopt-the-variadic REJECTED (spread caller `WithLimits(mySlice...)` aliases; later element
mutation = silent constraint modification, same class as replace/last-wins; doc-only adoption too
weak for a limiting API); fixed inline array REJECTED (caps count, bloats every op-value copy).
Mitigations: single-limiter cut = plain fields, still 0 allocs; multi-limiter needs owned storage
ANYWAY (canonical-order sort + dup scan at bind time = the copy is canonicalization, not defense).
Accumulation copy-merges, never appends (backing shared among op value copies). **SETS (PN, 2026-07-02):**
ONE limiter type — AmountLimiter kind-split REJECTED (PN: no reason TO do it; the rationale offered
for it — type-guarding weight-blind amount binding — was invalid: unit coherence isn't
type-checkable). T/U weigher-op pairing stays unrepresentable via WeightLimiter[T]→same-T methods
only, no boxing, sets carry no weighers. PN sweep-ratifications (2026-07-02): every op gets the
FULL complement of the 4 binding methods; FLUSH LIMITERS DROPPED ENTIRELY (PN, supersedes his
earlier incl-flush answer — a flush needing limits attaches them to a launcher invoked FROM the
flush; kills the WithFlush* surface, the flush-weigher-arg question, C3's WithFlushLimits, and
permit-core's limited-flush model-check case; SKIMMER drain limiting STAYS — PN: a skim handler has
something to weigh [typed result], isn't pre-committed by an upstream limited op [flush only drains
what limited accumulates admitted], and runs in the user's context [a launched body wouldn't] —
permit-core's limited-drain model-check case remains, skim-scoped); weigher <0 panics
but ==0 VALID = nothing acquired, binding skipped that dispatch; weigh-once-per-dispatch confirmed;
opoption deletion confirmed; Limit-rename rejection confirmed. Construction-time static-infeasibility panic
DROPPED entirely (PN never wanted it — I had misread his sweep answer as keep-it; strike the static
branch from dispatch-execution-split.md "Infeasible demand" at implementation); ALL weighted
infeasibility = runtime distinct per-unit error, enforced at demand registration (before barrier
arming). Reusable canonicalized
sets: untyped `LimiterSet` (universal) +
`WeightLimiterSet[T]` (same-T, T inferred from members) — one-shot homogeneous variadic ctors;
single MIXED set REJECTED (no T witness / heterogeneous variadic untypable / boxing = T/U). Set
binding methods are SINGULAR (`WithLimiterSet(s)` — a set IS the bunch; multi-set = repeated calls
per accumulation law). Pure-set op = zero per-op alloc (shares frozen state); customizing op = one
bind-time merge. Dup panic spans all four methods + sets. NO static-weight form — always a function (constant closure covers the rare
case); deliberately retires the construction-time static-infeasibility panic (was advisory anyway —
capacity is dynamic) → all weighted infeasibility = per-unit distinct error at dispatch. opoption.go
DISSOLVES (WithLimits was the only OpOption; constructors drop opts). Gotchas: COW the bindings
slice (diverging-chains aliasing; needs dedicated test); weigh runs ONCE per dispatch on the
dispatching goroutine, stamped int, stable across postpone retries + demand identity; weigh<1 panics
(weigher bug), oversize-vs-capacity errors (data).

**►►► LIMITER RESOURCE CLASSES — design agreed, recorded (2026-07-02), NOT implemented.**
`docs/decisions/limiter-resource-classes.md`: the permit forest's premises (cache-don't-return,
inheritance, steal) hold only for conserved holdable permits — rate limiters break conservation,
external gauges break revalidation. Design: base `Resource{TryAcquire}` + `HoldableResource{+Release}`
discovered by ONE type assertion at NewPool (nil-field test on hot paths; whole policy bundle —
caching forest vs pass-through, postpone charge-rides vs release-and-reacquire, park/resume
alternation vs no-op — keys off it). Consumables = degenerate forest (no caches; every acquire is a
fresh step-3 TryAcquire). Wake: resource-owned production (timers arm lazily on failed TryAcquire),
Pool-supplied surface = ONE signed verb `Adjust(delta)` posting to a signed atomic `balance` (PN's
counter model — the execpool demand-counter pattern applied to wakes; superseded the counted-fan-out
Notify(n) and fit-matched per-waiter-demand drafts, both now in Rejected). Positive: serialized wake
chain (≤1 wake in flight; step-3 success decrements by amount + ALWAYS forwards one probe → unknown-
size events = Adjust(1); failure STOPS the chain but does NOT clamp — balance is the resource's delta
ledger, pool transacts-never-rewrites [clamp ⇒ phantom debt on +5/raced/−5 netting]; ⇒ wake seeds are
per-positive-Adjust events, NOT zero-crossing edges, else residue masks fresh posts; register-then-
check closes the missed-wake race; positive side = lossy hint / negative side = exact, drift bounded,
reconciliation-read is the seam if measured material). Negative (holdable-only, panics for consumable) =
reclaim debt subsuming Reclaim(n): immediate idle harvest (steal-with-Resource-as-sink; lazy debt
would strand vs event-less cached idle) + releases pay debt before caching (targeted suspension of
cache-don't-return; one atomic load on the release hot path) + repayment = ordinary resource.Release
calls. **No WakeAll anywhere** — even destroy-drain posts to the balance. Verb name still open
(Adjust vs Offer/Credit). `Reclaim(n)` = the
shrink-direction dual for holdables (memory/GC drift): a steal whose beneficiary is the Resource,
reusing the LRU walk + revalidating CAS; idle-only (recall-of-in-use stays rejected); also sharpens
SetMaxConcurrency lowering. Joint admission: holdables before consumables in the canonical order.
Mixed semantics = WithLimits composition, no third class.

**►►► `RenotifyFunc` → `Notification` LANDED + COMMITTED (`eee5322`, 2026-07-01) — the
conservation-discharge refactor (pickup #1). Gate green: full `-short` suite; rdvq `-race`
(saturation, 230s); 25×80-check `TestBySimulation -race` batch 25/25.** The bare
`RenotifyFunc func()` threaded through the block/wait paths is now a value struct
`rdvq.Notification{n *Notifier, fallback func()}` with `Empty`/`Consume`/`Forward`. Spec +
rationale: `docs/decisions/waiter-set-notification.md` (Status → "Landed"). Key points:
- **`wrappedRenotify` + its pool deleted.** The wrapper existed only to give listeners a
  renotify that re-circulates through the origin `Notifier`; that identity is now the zero-alloc
  `n *Notifier` field, carried by value. Leak-on-discard gone *by construction*.
- **`Notify` is total.** `Notifier`/`Waiters`/`Listeners` `.Notify(fallback)` run the fallback
  when no consumer takes the wake. Consumer loops: `renotifyFn != nil {renotifyFn()}` →
  `!m.Empty() {m.Forward()}`; productive use → `m.Consume()` (no-op intent marker). Forward is
  listener-recirculate (`n` set) vs waiter-terminal (`n` nil).
- **DECISION (PN):** the two *unguarded* `Waiters.Notify(unmetDemandFn)` sites (`queueFresh`,
  `Expedite`) go total too — they now `Nudge`-spawn on a no-parked-worker miss instead of
  dropping the signal. Safe: `Nudge` is `spawnConcurrencyLimit`-capped + self-correcting; the
  extra goroutines are warranted unmet-demand parallelism (the buffered-1→unbuffered shift). The
  spec's "accepted.go ×3" was a miscount (2 guarded sites); corrected in the spec.
- **`unmetDemandFn` split** from the threaded value: it is a `func()` fallback (role a), not a
  `Notification` (role b). `q.listener.Notify = q.waiters.Deliver` (new exported re-injection
  primitive) replaces the old `= q.waiters.Notify`.
- **"Pool the fallback" is out of scope** (PN confirmed): every fallback is `noop` or a
  once-cached method value (`unmetDemandFn` = `ensureWorker`), never a per-call closure. A
  fallback that ever needs per-call state binds as a method value on a lifecycle-pooled object
  (cf. `heldPermit.release`/`confirmFn`), NOT a self-returning pooled closure. `rdvq.NewNotification`
  is the seam for a producer that mints a terminal wake outside a `Notifier` (today: one workq test).
- **Design nit noted for later:** cached-bound-method may be over-used vs. an interface where the
  receiver is already a pooled pointer (alloc-free conversion). Own pass, not blocking.

**►►► DESIGN B chosen for the scheduled-flush deadline timer (PN, 2026-06-29). Replaces the
per-worker idle-suppression (DECISION B in scheduler.pull).** Goal: workers scale fully to zero; a
SINGLE scheduler-owned timer honors pending flush deadlines by waking/spawning a worker. Concrete plan
(designed against delayq.go + accepted.go):
- **delayq:** add `func (q *Queue[T]) NextDeadline() time.Time { return timeFromNanos(q.nextDeadline.Load()) }`
  (authoritative earliest, lock-free atomic read).
- **Accepted owns the timer** (`schedTimer *time.Timer` + `schedTimerMu` + `schedArmed time.Time`):
  - `armScheduledTimer(d)`: **only LOWERS** (mirrors delayq.lowerDeadline) — `if !schedArmed.IsZero()
    && !d.Before(schedArmed) { return }`; else Reset to `time.Until(d)` (clamp ≥0), set schedArmed=d.
    Zero d ⇒ Stop + clear. THE SUBTLE POINT: arm-only-lowers is REQUIRED — a naive "arm to exact each
    time" races a concurrent Schedule (a drain computing next=T2 can overwrite a concurrent
    Schedule's sooner T0 → missed wake). Sooner always wins; later re-arms only after the timer fires
    (schedArmed cleared on fire) or post-drain.
  - fired callback: clear schedArmed, then `if !waiters.Notify(nil) && unmetDemandFn != nil {
    unmetDemandFn() }` (wake a parked worker to re-drive→drainScheduled, else Nudge-spawn one).
  - Arm sites: `wakeScheduled` (delayq wake on lowering — read NextDeadline, arm) AND end of
    `drainScheduled` (re-arm for the returned next, advancing/clearing after a drain). Both inert for
    the per-wave workQueue (waves never Schedule → NextDeadline always zero; unmetDemandFn nil).
- **scheduler.pull:** delete the DECISION B block (`if deadlineCh != nil { idleCh = nil }`) so workers
  idle-exit freely.
- **WaitForNew:** stop arming the per-worker timer (the `timerp` block) — pass deadlineCh=nil; the
  Accepted timer handles wakes now. Strip the now-vestigial `deadlineCh` from the AddWorkFunc
  signature / scheduler.pull / wave addWorkFn / controller armedDeadline logic as a follow-up
  (gut-first: pass nil, leave the dead nil-channel select case, strip later).
- Validate: large -race TestBySimulation batch (the failure mode is a MISSED/DELAYED wake — subtle,
  may not trip -race; add targeted assertions or a flush-latency check). Do BEFORE C2 benchmarks
  (worker count / scale-to-zero is what the methodology measures).

**►►► DESIGN B IMPLEMENTED (2026-06-29) — build/vet/sim green; -race batch RUNNING.** Landed a
CLEANER formulation than the plan above: `armScheduledTimer` reads the AUTHORITATIVE earliest from
`delayq.NextDeadline()` (new lock-free atomic accessor) *under its own `schedTimerMu`* and Resets to
it — so the last of any racing arms always reflects the true earliest. No "arm-only-lowers"
bookkeeping, no `schedArmed` field. The concurrent-Schedule-vs-drain race that "arm-only-lowers" was
meant to fix is handled structurally: an arm triggered by a stale event still reads the *current*
atomic inside the lock. Edits:
- `delayq.NextDeadline()` — lock-free read of the next-deadline atomic (the single source of truth).
- `Accepted`: `schedTimerMu`/`schedTimer` fields; `armScheduledTimer()` (read-atomic-under-lock →
  Reset/Stop, lazy `time.AfterFunc`); `scheduledDeadlineFired()` (`Notify(unmetDemandFn)`-or-Nudge,
  mirrors `ForceFresh`). Armed from `wakeScheduled` (delayq lowering hook) + end of `drainScheduled`.
- Removed: per-worker timer in `WaitForNew` (passes nil deadlineCh); the `shouldStillWait`
  armed-deadline abort; controller `nextDeadline`/`armedDeadline` fields; `timerp` import; the
  DECISION B idle-exit suppression in `scheduler.pull`. Workers now scale fully to zero.
- Robustness: the first schedule always arms (any real deadline < `noDeadline`), so no missed flush.
  Out-of-band removals (ClaimForFlush/Reschedule-later don't fire the lowering wake) leave the timer
  on a stale-early deadline → a spurious early fire → drain finds nothing due → re-arms. Self-
  correcting, never a missed/late flush. A *missed* flush would hang TestBySimulation (caught).
- `deadlineCh` is now vestigial (always nil) through AddWorkFunc/scheduler.pull/wave addWorkFn — strip
  in a follow-up (gut-first).

**►►► CUTOVER FULLY SCOPED — FORK A (`combiner`, 2026-06-28e).** Deep read of the whole
dispatch surface (pool.go, ctxmeta.go, wave.go, launcher.go, limiter.go, funnel.go) settled the
design. Key facts the 2-line plan banner missed:
- **The existing design ALREADY splits admission from bodies.** Per-wave `workQueue` (`workq.Accepted`,
  `Init(nil)`) + `skimQueue` (`Pending`) run *admission* (the scatter-works) INLINE on the user /
  skim goroutines (`topLevelExEnv.ExecuteNowOrQueue` → `wave.workQueue`; skim drives `workQueue.
  ExecuteOne`). Only the BODY goes to the global `defaultPool` (`*PostWork.Execute → defaultPool.
  Post`). So C2 is NOT "rebuild dispatch" — it is "move the body off `defaultPool` onto an executor,
  and move NESTED admission off the body goroutine onto a scheduler."
- **THREE blocking body types** (each runs user code; each must run on the executor, never pin a
  scheduler): (1) task — `taskWork`; (2) funnel-accumulate — `funnelWork[T]`; (3) funnel-flush —
  `funnelInstance.Execute` (the user `Flush` handler), `ForceFresh`'d into the pool by `sweepFlush`/
  deadline-drain.
- **Admission chains differ (must unify).** Task: `launcherScatterWork`(governor) → `limiterScatterWork`
  (permit) → `taskPostWork`(post) → body. The body (`taskWork`) is already a pure body (no gate).
  Funnel: `funnelPostWork`(governor via onWait/`Waiting`) → post; **the permit gate is INSIDE
  `funnelWork.Execute`** (`gateAcquire`) — Wrinkle 1. Funnel-flush: bare `funnelInstance` (no
  decorator). Unify by reusing `limiterScatterWork`/`launcherScatterWork` around the funnel post-works,
  matching the task order **governor OUTSIDE permit** (a prior gate-hoist hit governor-ordering — the
  inversion is the trap).

**FORK A (chosen, see DECISIONS) — minimal, semantics-preserving:**
- **Bodies → `bodyExecutor` (`execpool.Executor[*workerExEnv]`).** Each body type gets `Run(ee)` (=
  `run(ee)` + self-`Free`, already 90% there) and a scheduler-side post-work whose `.Execute` does
  `bodyExecutor.PushBack(body)` + `ex.Starting()` + null-out (the EXISTING `wk.task=nil` ownership
  transfer; the controller `Free`s the post-work, the executor owns+frees the body). NO `HandedOff`.
- **Top-level admission stays inline** on `wave.workQueue` (user/skim goroutine) — preserves producer
  backpressure; the `PushBack` blocks the producer (safe: never an executor goroutine).
- **Nested admission (from a body on an executor) must NOT run inline** (its `PushBack` would block the
  executor waiting for an executor ⇒ deadlock). `workerExEnv.ExecuteNowOrQueue` drops the scatter-work
  to the scheduler non-blocking (push fresh + `Nudge`); a scheduler worker runs admission + the
  blocking `PushBack`. This is the deadlock-avoidance the split exists for.
- **`defaultPool` → `workq.Scheduler`** (drives nested admission + postponed + scheduled-flush);
  `streampool.Wait` reaps BOTH pools. The scaffold's `incoming` Handoff + `Scheduler.Post` are UNUSED
  in Fork A (top-level is inline, not Handoff-posted) — leave vestigial, remove later.
- Funnel-gate hoist: wrap `funnelPostWork` with `limiterScatterWork`(permit) [+ governor wrapper to
  keep governor-outside-permit]; remove `gateAcquire` from `funnelWork.Execute`; permit released at
  body end / `Free` (already idempotent). Funnel-flush: `funnelInstance.Run(ee)` + a flush post-work
  that `PushBack`s it; `sweepFlush`/deadline `ForceFresh` the post-work.

**DECISIONS (asked PN 2026-06-28e):**
- **D1 topology:** Fork A (top-level inline, `incoming` unused) vs Fork B (route top-level through the
  scheduler `incoming` Handoff — the earlier written plan; relaxes producer backpressure). → recommend A.
- **D2 funnel-flush:** flush body → executor too (honors always-live-scheduler) vs run on scheduler for
  the first cut (simpler, but a blocking `Flush` pins a scheduler). → recommend executor. **PN chose
  executor; DEFERRED to a follow-up CP after closer reading.** Reason: `funnelInstance.Execute`'s flush
  is synchronous under `c.mu`, and the per-instance wave barrier (`DecrementReference`) MUST drop
  *after* the user `Flush` (so a downstream `Submit` in `Flush` takes its ref before this one drops —
  else `totalReferences` transiently hits zero ⇒ premature wave Done = the leak class of the prior
  hang). Splitting that across the scheduler→executor handoff opens a new R1/R2 concurrency window in
  the delicate instance lifecycle and is too risky to bundle into the first cut. For CP-B1 the
  deadline/sweep flush stays on the scheduler (its nested submits drop to the scheduler non-blocking →
  no deadlock; it only *pins* a scheduler worker during a blocking `Flush`, which is bounded — flushes
  are rare vs accumulates). The already-past-deadline INLINE flush in `accumulate` already runs on the
  executor. Move `funnelInstance.Execute`→executor in a dedicated follow-up (CP-B1b).

**CP SEQUENCING (revised this session):**
- **CP-B1 (in progress):** bodies (task + funnel-accumulate) → `bodyExecutor`; nested admission →
  scheduler via `ForceFresh`; `defaultPool` STAYS `worker.Pool` (proven scheduler); funnel-flush stays
  on scheduler (D2 deferred). Isolates the body/executor split + deadlock-avoidance from the
  scheduler-swap. Validate large `-race` batch before commit.
- **CP-B1b:** `funnelInstance.Execute` flush → executor (the deferred D2), with the instance-split
  designed carefully.
- **CP-B2:** swap `defaultPool` `worker.Pool` → `workq.Scheduler` (DONE, commit 92b5f48); then delete
  `internal/worker` + the dead `workq.Queue`/`Worker` scaffold + strip vestigial `deadlineCh` (DONE,
  this cleanup).

**►►► RDVQ INBOX CLUSTER — remaining ~10/14 allocs/op (2026-06-29, pursuing option 3).** After the
meta pooling, BenchmarkLauncherSkim's remaining 14 allocs/op are ~99% waiter inboxes. Root cause:
`Waiters.WaitFunc` (rdvq/waiters.go:85) borrows a fresh inbox from `inboxPool` per call and reclaims it
ONLY when `clean` (PopFrontFunc received a value on the waiter channel). In the steady-state skim the
work arrives via the skimQueue's OWN inbox (a different channel), so the registered waiter is never
satisfied → PopFrontFunc marks it ABANDONED (pushes a zero-value marker, leaves ib in the emptyInboxes
collection) → clean=false → NOT reclaimed. The abandoned inbox is later popped by a sender (TryPushBack),
which drains the marker and DISCARDS it (→ GC). So one inbox struct + make(chan,1) leaks per skim.
Path: Wave.Skim → addWork → skimQueue.PopFrontFunc → workWaiters.WaitFunc (workWaiters = the Accepted's
q.waiters). Profile: rdvq.inbox[func()].Init 42% + inbox-pool struct Get + nbcq nodes.
- **ATTEMPTED option 3 (naive sender-reclaim) — FAILED, REVERTED.** Made `inboxOnlyQueue.TryPushBack`'s
  marker-drain branch reclaim the inbox. WRONG: an abandoned inbox is STILL OWNED by its receiver.
  `Queue.PopFrontFunc` (queue.go:369-371) explicitly HOLDS its inbox across retry iterations and RE-PASSES
  it, "preserving the reuse-without-requeue path for its own abandonment marker"; `Handoff.PopFrontFunc`
  borrows-per-call and DROPS the abandoned inbox to GC by design ("a later sender's TryPushBack drains
  [the marker]"). So the sender draining the marker does NOT have exclusive ownership — reclaiming steals
  an inbox the receiver will re-pass (or that must stay GC-owned), giving two receivers one inbox →
  lost-wakeup/double-receive. `saturation_test` (Queue, 8 prod × 8 drain × 40k) HUNG (125s). The
  "drop-and-let-GC" of abandoned inboxes is load-bearing, not an oversight.
- **CORRECT option 3 requires a GENERATION-STAMPED inbox** (the protocol the OUTBOX already has —
  queue.go:29/189: a stale reclaimed-and-reused outbox fails its CAS and is dropped). The inbox has none,
  so reclaim-and-reuse can't be disambiguated from a concurrent re-pass. Adding gen-stamping to the inbox
  is a change to the hairiest lock-free code in rdvq → needs the model-check + large -race treatment, a
  dedicated effort.
  - Rejected option 1 (thread a caller-held inbox through Waiters→workq→wave): leaks rdvq complexity into
    the callers + needs a holder that outlives a single drive (nothing does on the consumer side).
  - Rejected option 2 (check work before registering): reintroduces the missed-notification race that the
    register-then-confirm order exists to close.
- STATUS: 38→14 alloc reduction (meta pooling) stands, committed (50bf217).
- **GEN-STAMPED INBOX — design committed (4cb85b6, docs/rdvq-inbox-reclamation.md); prototype VALIDATED.**
  Design: 3-state gen-stamped inbox (free/waiting/delivering) with sole-receiver reclaim (senders only
  claimDeliver-or-skip, never reclaim) + a SINGLE gen bump on abandon (the only transition that disowns a
  registration a sender may have observed). Reference counting rejected (still needs a gen for ABA across
  pool reuse; doesn't evict the lingering hint; no multi-party reclaim to coordinate).
  - **DONE + VALIDATED (2026-06-29).** Live `inbox.go` (gen-stamped 3-state machine) + `inboxonly.go`
    (`TryPushBack` = claimDeliver-or-skip, no marker; `PopFrontFunc` register/abandon(+gen)/orphan-drain,
    always leaves the inbox free → callers Waiters/Handoff/Queue reclaim on EVERY PopFrontFunc, recycling
    abandoned inboxes). **CRUCIAL: captured-gen hints.** emptyInboxes holds `inboxHint{ib,gen}` (not a bare
    *inbox); a sender claims at the hint's CAPTURED gen. This is what makes the SHARED omnipool.For[inbox[T]]
    pool cross-queue-safe: an inbox abandoned in queue A (gen bumped) and reused in queue B via the shared
    pool leaves A's stale hint claiming at the old gen → fails, so A doesn't misdeliver into B's receiver.
    - **The bug this fixed:** my first cut claimed at the CURRENT gen → cross-queue misdelivery (a skimWork
      from a wave's skimQueue ran on a scheduler worker with a bare ctx → "Context not associated with a
      wave" panic). Diagnosed via stash-baseline (confirmed my change), then root-caused to the shared
      inbox[Work] pool + reclaim-of-abandoned + stale hint. Per-queue pool also fixes it but loses
      cross-queue reuse; PN directed the shared-pool fix → captured-gen hints (mirrors outboxHint).
    - **Prototype** (`inboxpool_proto_test.go`): TestInboxReclaim_Race (2 queues sharing 1 pool, churning
      receivers, per-queue value-range ownership) + TestInboxCapturedGenStaleHintInert (DETERMINISTIC
      cross-queue guard — orchestrates abandon-in-A/reuse-in-B/A-stale-hint; FAILS if trySend claims at
      current gen). Note: the stress test alone can't reproduce cross-queue (sync.Pool P-affinity), hence
      the deterministic guard.
    - **Gate MET:** full suite green; rdvq -race incl saturation_test; 25/25 TestBySimulation -race;
      **BenchmarkLauncherSkim 38 → 5 allocs/op** (meta pooling 38→14, gen-stamped inbox 14→5). NOT committed.
  - **Benchmarks (PN asks):** rdvq Queue benchmarks performance-NEUTRAL (EmitVsChan direct-handoff within
    noise; OutboxHintCycle/ChanCycle controls flat). NEW `BenchmarkHandoffVsChan` (handoff_bench_test.go):
    Handoff vs unbuffered chan, conc sweep — chan is ~2-4x faster for pure rendezvous (0 allocs both; the
    gap is Handoff's multi-step lock-free protocol vs the runtime's direct chan handoff). Handoff's cost
    buys its composable park (block-as-demand spawn, idle/ctx selectFn seam, LIFO scale-to-zero); ~1µs/
    handoff is negligible for the executor's blocking bodies.

**►►► DISPATCH ALLOC REDUCTION — top-level meta pooling (2026-06-29, in progress).** The bench
comparison showed streampool ~37 allocs/task vs naive-pool's 1; root-caused via `BenchmarkLauncherSkim`
(the main-module hot-path guard = 38 allocs/op, single-threaded Submit+Skim, no limiter — so it's CORE,
not the harness). Memprofile (`-memprofilerate=1`) attribution, three clusters:
1. **top-level/skim ctxMeta machinery (~65%)** — every top-level Submit/Skim from a bare ctx minted a
   fresh `&ctxMeta` + `&topLevelExEnv` + a ctxpool child (`newChildPool`+`AfterFunc`+`WithValue`+`&child`),
   none recycled. The body-meta path IS pooled (`bodyMetaPool`/`releaseBodyContext`); the top-level path
   had no matching free. (launcher.go:202 comment already half-knew: it roots the BODY at the stable
   caller ctx to dodge the per-dispatch child, but left the meta-stamped ctx itself allocating.)
2. **rdvq handoff inbox (~30%)** — `rdvq.inbox[func()].Init` + nbcq + omnipool Gets per dispatch (the
   executor handoff isn't recycling inboxes). C2-introduced. DEFERRED.
3. exEnv stack slice growth (`PushQueueFunc`/`PushGroup`). minor.
- **DONE: top-level SUBMIT + SKIM meta pooling — BenchmarkLauncherSkim 38 → 14 allocs/op** (2178 →
  645 B/op). Submit path validated 25/25 -race; combined (submit+skim) -race batch running. The remaining
  14 allocs are ~99% the rdvq-inbox cluster (the skim-queue handoff `inbox[func()].Init` + nbcq nodes) —
  the deferred follow-on; the meta machinery itself now contributes ~0.
  - SKIM path: `skimCtxMeta` now returns the `owned` signal; the 6 wave.go skim drivers (Skim, yield,
    block, TrySkim, SkimAll, skimAll) `defer releaseTopLevelContext` when owned. Handles the skim
    derivation CHAIN (bare-ctx skim mints top-level metaB + skim metaC; yield reuses the dispatch's
    top-level meta and mints only metaC) and the SHARED exEnv via three ctxMeta fields: `selfCtx` (child
    to free), `ownsExEnv` (free exEnv only where allocated — metaC reuses metaB's), `releaseParent`
    (bounded walk: free metaB too iff this call minted it; stop at a reused-ambient boundary so yield
    never frees the dispatch's meta). Nested skim (SkimAll→skimAll, yield/block on an already-skim ctx)
    reuses the ambient skim meta → owned=false → no double-release.
  - ESCAPE-SAFETY (skim ctx is the ambient root for handler-launched async work): SAFE because ctxpool is
    nearest-child-wins + structural cancellation — a handler-launched body resolves its OWN nearest meta,
    never the recycled skim meta's value; recycling swaps only the childKey value, not the cancellation
    chain. The launcher.go:202 comment is about reuse EFFICIENCY, not safety. Proven by the -race/sim
    batch (exercises skim + nested subwaves + handlers launching work).
- **(earlier sub-step) top-level SUBMIT meta pooling.** `ensureCtxMeta` draws the meta from `bodyMetaPool`
  (zeroed on Put → `held` nil as required); `topLevelExEnvPool = omnipool.For[topLevelExEnv]()` with a
  `Reset()` (clears workQueue+stacks, NOT the mutex — avoids copylock + keeps stack cap);
  `topLevelCtxMeta` returns an `owned` bool; `releaseTopLevelContext(ctx)` (mirrors releaseBodyContext)
  returns exEnv+meta to pools and `ctxpool.Free`s the child. Wired ONLY into the Launcher path
  (`vetStart`→`dispatch`, `defer releaseTopLevelContext` after `meta.Unlock`), which is provably safe: the
  body is rooted at `srcCtx`, so the meta-stamped ctx is used only for synchronous admission and never
  escapes (confirmed: postpone re-queues the work item, which carries the body ctx, not the meta ctx).
- **Result: BenchmarkLauncherSkim 38 → 32 allocs/op** (2178 → 1698 B/op). Suite green; -race batch running.
- **WHY ONLY 6:** Skim shares the same pools but doesn't release (its nested skimCtxMeta derivation, where
  the expensive per-call `newChildPool`/`AfterFunc` lives, is DEFERRED) — so skim DRAINS the shared meta+
  exEnv pools, masking part of the submit win. The two meta paths are coupled through the shared pools;
  the full payoff needs the skim path pooled too.
- **NOT safe to extend blindly:** Funnel/Skimmer submit borrow the body FROM the meta-stamped ctx (unlike
  Launcher), so freeing their meta needs the borrow-source fix (root body at srcCtx) FIRST. Skim's nested
  derivation needs the parent-chain liveness handled (free the whole owned chain together; don't free a
  meta still referenced via another meta's `parent`).
- **NEXT increments:** (a) skim-path meta pooling (biggest payoff: kills per-call newChildPool/AfterFunc +
  stops draining the shared pools); (b) funnel/skimmer submit (after rooting their bodies at srcCtx);
  (c) the rdvq inbox cluster. NOT committed (awaiting PN + -race green).

**►►► C2 BENCHMARKS — bench/ comparison submodule created (2026-06-29).** New isolated module
`github.com/petenewcomb/streampool/bench` (own go.mod, `replace ../`) for head-to-head comparisons vs
other frameworks — keeps their deps/licenses out of the root module (mirrors otpsg). Rationale (from PN's
"consider what competitors benchmark"): Go pool libs (ants/pond/tunny) compete on throughput + memory +
peak-goroutines; NONE benchmark tail-latency-under-blocking, which is exactly the split's moat. So the
harness reports BOTH turfs through one `dispatcher` interface (start/submit/drain/stop):
- competitors' turf: tasks/sec, allocs/task, B/task, peak-goroutines.
- our turf: p50/p99/p99.9 dispatch (enqueue→body-start) + e2e (enqueue→body-done) latency, heavy-tailed
  lognormal blocking work, swept P:D (underload→heavy-overload).
- Lineup: unbounded (explosion control), chan-semaphore, naive-pool (the "dispatcher-pinned/no-split"
  control), streampool. tdigest for streaming quantiles; sharded recorder; fixed warmup+window;
  `-benchtime=1x`.
- **First validated result** (heavytail/balanced/P=D=8): streampool matches/beats the bounded baselines'
  e2e tail (12.2ms p99) while bounding goroutines, at ~37 allocs/task vs naive-pool's 1; unbounded blows
  to 133k goroutines. Green: gofmt + vet + lint(0) + compiles.
- **KNOWN LIMITATION (documented in bench/README.md):** the flat independent-blocking-task workload is
  throughput-bound by D, so all bounded systems converge — it does NOT yet isolate the split's
  responsiveness edge. NEXT: (1) nested-dispatch workload (naive pool DEADLOCKS, streampool doesn't);
  (2) funnel+flush (exercises CP-B1b); (3) mixed latency-probe + heavy bodies; (4) ants/pond/conc in the
  lineup. NOT yet committed (awaiting PN).

**►►► CP-B1b DONE (2026-06-29) — funnel-flush body → executor (the deferred D2). build/vet/suite green;
-race batch confirming.** A blocking user `Flush` no longer pins a scheduler — the always-live-dispatcher
invariant now holds for *every* user body (task, funnel-accumulate, funnel-flush). The split, collapsed
onto `funnelInstance` itself (no new object — the instance is already the scheduled `Work` *and* now the
`execpool.Task`):
- `funnelInstance.Execute` (scheduler side, driven by a drained deadline or the sweep's ForceFresh) is now
  pure admission: it hands the flush body to `bodyExecutor` — `TryPushBack` first, then (only when the
  scheduler worker parks, via `shouldStillWait` with `ShouldBlockOrPostpone`) a blocking `PushBack`.
  No permit gate (flush is unlimited). `ex.Starting()` fires only on a successful handoff.
- `funnelInstance.Run(ee *workerExEnv)` (NEW, the executor Task body) holds the moved flush: borrow body
  ctx → `c.mu` → `flush` → read `detached` → unlock → recycle if detached. This is verbatim the old
  `Execute` body, now on an executor goroutine with the worker's `ee` (was a fresh `&workerExEnv{}`).
- **Why collapsing onto the instance is safe (not a separate post-work like funnelPostWork):** the
  controller calls `instance.Free()` right after `Execute`'s handoff, possibly *concurrently* with the
  executor's `Run`. That is fine because `Free()` is already a pure no-op (R2 design) and the instance
  already self-recycles — nothing on the scheduler side touches the instance after Starting (the buffer
  slot is dropped in `releaseOthers`).
- **Barrier ordering preserved for free:** `flush()` (user Flush body + the deferred
  `state.DecrementReference()`) moves atomically to the executor, so the barrier still drops *after* the
  Flush body regardless of which goroutine runs it; the per-instance barrier (held from allocate) keeps
  the wave out of Done across the scheduler→executor handoff window.
- **bodyCtx-reuse race avoided:** added `borrowSrcCtx` field, written in `Execute` before the publishing
  handoff (rendezvous = happens-before) and read *once* at the top of `Run` before `c.mu` — so the owner
  reuse-pop that may recycle a non-detached shell the instant `Run` releases `c.mu` never races it. The
  borrowed bodyCtx is a `Run` local (not an instance field), as it was in the old `Execute`.
- **Widened accumulate/flush window:** the deadline-drain→flush window is now longer (drain → handoff →
  executor `Run` acquires `c.mu`) but it was already an interleavable window handled by `c.mu` + R1
  (accumulate on a drained instance sees `Reschedule`==false and leaves it to the pending flush) +
  spent-shell recycle. Widening changes nothing structurally.
- **Remaining for full C2:** the C2 latency/alloc benchmarks (real methodology: P99/max, heavy-tailed
  blocking-I/O, swept P:D ratios). CP-B1b was the last code-structure piece of the split.

**►►► CP-B2 CLEANUP DONE (2026-06-29) — build/vet/lint/suite green; -race confirming.** Deleted the
obsolete worker-pool lineage now that `defaultPool` is `workq.Scheduler`:
- Removed `internal/worker/` (pool.go + test) — unreferenced.
- Removed `internal/workq/queue.go` + `worker.go` + `queue_test.go` — the `workq.Queue`/`Worker`/
  `ExecEnv`/`NewWorker` scaffold was used only by `internal/worker` + itself (dead cluster). `Accepted`,
  `Pending`, `Scheduler` are untouched and live.
- Stripped the vestigial `deadlineCh` (always nil since Design B) from `AddWorkFunc` and every
  implementor: `scheduler.pull`/`selectWork`, `wave.addWork` (×2), and the test addWorkFns. The
  deadline-wake path is now entirely the queue-owned timer.
- `timed_test.go`'s `TestAccepted_FutureDeadline_WakesParkedWorker` now validates Design B directly
  (the queue timer fires a waiters notification that wakes the parked worker at ~deadline).
- **Remaining for full C2:** CP-B1b (funnel-flush body → executor, the deferred D2 — delicate R1/R2);
  the C2 latency/alloc benchmarks (real methodology).

**CP-B1 IMPLEMENTED (2026-06-28e) — build/vet/sim green; -race batch pending.** Edits:
- `execpool.Executor.TryPushBack` (non-blocking direct handoff).
- `pool.go`: `bodyExecutor = execpool.NewExecutor(&workerExEnv{})`; `Wait()` reaps executor then
  scheduler; `defaultPool` STILL `worker.Pool`.
- `taskWork.Run(ee)` (= run+Free), `Execute` removed; `taskPostWork.Execute` → TryPushBack-then-(if
  ShouldBlockOrPostpone)PushBack to `bodyExecutor`, `ex.Starting()` + `task=nil` on handoff.
- `funnelWork.Run(ee)`, `gate`/`releasePermit` (permit hoisted out of `Execute`); `funnelPostWork.
  Execute` → permit gate + TryPushBack/Waiting-then-PushBack. `boundFunnelWork` no longer a workq.Work.
- `workerExEnv.ExecuteNowOrQueue` (nested, on an executor body) → `defaultPool.ForceFresh(work)`
  (non-blocking drop to scheduler) — the deadlock-avoidance (no inline blocking PushBack on an
  executor goroutine).
- `funnelInstance.Execute` UNCHANGED (flush stays on scheduler — D2 deferred to CP-B1b).
- The handoff is blocking ONLY on scheduler workers / top-level/skim producers, never an executor
  body goroutine (nested drops to the scheduler), so it can't wedge waiting for an executor.
- **BEHAVIORAL CHANGE (expected, deterministic): `Example_observable`** golden shifts — task C now
  dispatches promptly when a permit frees (10ms) instead of the top-level backpressure-help first
  skimming B (20ms); B is skimmed during SkimAll (30ms) instead. Correct results, order, and the
  concurrency-2 limit all hold. Direct consequence of the split: the top-level help-drain (the
  deadlock-avoidance, `wv.block`/`gateAcquire`, UNCHANGED) now only races the executor's independent
  body completion rather than driving the body itself. NOT a deadlock regression (help-drain still
  skims a blocked permit-holder's result). Golden needs updating — flagged for PN.

**►►► CP-B1 -race RESULT: HANG (spawn storm) — worker.Pool shortcut REJECTED (2026-06-28e).** The
40× `-race TestBySimulation` batch HUNG (10m timeout, iteration 1). Dump (`/tmp/race_batch.log`,
copied to scratchpad `cpb1-hang-dump.log`): **3981 `worker.Pool` worker goroutines** vs 93 idle
executors; 214 workers blocked on the single `delayq` mutex in `controller.drainScheduled`. =
**spawn storm**, NOT the prior leaked-ref class.
- **Root cause:** nested routing via `defaultPool.ForceFresh(work)` (workerExEnv.ExecuteNowOrQueue)
  spawns a `worker.Pool` worker per nested submit — its `Notify`-or-spawn spawns whenever no worker is
  PARKED, and under load none are parked (all contending on `delayq`), so every nested submit spawns.
  Positive feedback (more workers -> more `delayq` contention -> fewer parked -> more spawns) -> ~4000
  workers -> livelock -> timeout. The 93 idle executors prove it's NOT an executor shortage; the
  scheduler side melted down.
- **This is precisely the line 94-103 prediction:** the producer-side redirect is wrong for nested;
  "the blocking handoff MUST be the scheduler's Work," reached via a **demand-BOUNDED** drop, not a
  per-call spawn. `worker.Pool`'s per-call `TrySpawn` demand model cannot bound this.
- **VERDICT: the "keep worker.Pool for CP-B1, swap to Scheduler in CP-B2" sequencing FAILS — the two
  are coupled.** The handoff needs `workq.Scheduler` (on `execpool`, whose `RegisterUnmetDemand`
  COUNTER model bounds workers to actual unmet demand) AND the admit/handoff split.
- **SALVAGEABLE (correct, reusable):** the body-side cutover — `bodyExecutor`, `Executor.TryPushBack`,
  `taskWork.Run`/`funnelWork.Run`, `taskPostWork`/`funnelPostWork` -> PushBack, the funnel gate hoist.
  ONLY the nested-routing (`ForceFresh`) + the `worker.Pool` scheduler are wrong. Uncommitted (tree
  hangs under -race; do NOT commit).
- **NEXT:** wire `defaultPool` = `workq.Scheduler` and route nested admission to its bounded intake,
  Wait=admit / Work=PushBack. Then re-run the -race batch. The genuinely-hard C2 core; do it
  deliberately.

**►►► SCHEDULER INTEGRATION DONE (2026-06-28e) — build/vet/sim×3 green; -race batch RUNNING.**
Replaced the worker.Pool shortcut with `workq.Scheduler` (execpool, counter-demand):
- `pool.go`: `defaultPool = workq.NewScheduler()`. Removed the dead worker.Pool plumbing
  (`newWorkerState`, `workerEnvKey`, `workerEnvFromContext`) and the `internal/worker` import — the
  package is now unreferenced (delete in CP-B2 cleanup).
- `workerExEnv.ExecuteNowOrQueue` (nested) → `defaultPool.Post(ctx, work)` (the intake Handoff,
  block-as-demand COUNTER) instead of `ForceFresh`. Blocks the executor body only until a scheduler
  worker ACCEPTS (bounded by outstanding blocked Posts — execpool `maybeSpawn` ramps one spin-up at a
  time toward the `demand` counter and no further; verified in execpool/pool.go), deadlock-free
  (separate pool, unblocks at handoff before admission).
- `funnelInstance.Execute` (flush, still on scheduler per D2): now uses a fresh `&workerExEnv{}` for
  the flush body instead of `workerEnvFromContext` — scheduler workers carry no ctx exEnv, so the
  flush is self-contained (the LAST consumer of the worker-ctx exEnv; that's why the plumbing could
  go). Flush logic otherwise UNCHANGED (synchronous under c.mu — avoids the D2 R1/R2 split risk).
- Why this fixes the storm: worker.Pool's `TrySpawn`/`ForceFresh` spawn per-call (capped burst, but
  unbounded total under sustained demand). execpool spawns toward a balanced COUNTER, converging on
  outstanding demand. Hot nested path = Post (counter); only rare flushes use `Nudge`.
- Gate: 40× `-race TestBySimulation` (`/tmp/race_batch2.log`). If green → update `Example_observable`
  golden (the benign interleaving change persists; no nested there, so it's the body-executor split).

**►►► GREEN CHECKPOINT (2026-06-28e) — dispatch/execution split WORKS.** Validation:
- build + vet + full non-`-race` suite (all packages incl. psgwf): GREEN.
- `-race TestBySimulation`: **56 iterations clean** (6× then 50×, 701.9s; zero hangs/races/fails). The
  deterministic worker.Pool storm is GONE; no leaked-ref hang or data race surfaced. (Above the ≥25
  floor; a ≥300 soak still advisable before fully trusting the rare class — feedback_race_confirm.)
- `Example_observable` golden updated (the benign body-executor-split interleaving).
- UNCOMMITTED pending PN's go-ahead to commit (harness rule: commit only when asked).
- **Remaining for full C2:** CP-B1b (move funnel-flush `funnelInstance.Execute` → executor, the
  deferred D2 — needs the instance-split designed around the c.mu/barrier ordering); CP-B2 (delete
  `internal/worker`, now dead); audit the scheduler scaffold for vestigial bits (e.g. confirm
  `incoming`/Post/`Nudge` are all live now); then the C2 latency/alloc benchmarks (real methodology).

**►►► EXECUTOR WIRING REVERTED — BACK TO GREEN (`bfa4005`, 2026-06-28d).** The `HandedOff`
executor-wiring (`71d8699`) was REVERTED: it both (a) introduced an intermittent `-race`
`TestBySimulation` hang (~1/25, a leaked work-ref) and (b) was a **complexity smell** — it kept the
body flowing through the priority controller and bolted on a `HandedOff` flag to suppress the
controller's `Free`, creating two-owner contention + an unstated invariant. PN's call: the split
should *simplify*, so redo it the principled way. Branch is GREEN again (single pool; build + vet
+ `-race ×6` sim pass). The `workq.Scheduler` scaffold (`6cb1164`) + `execpool.Executor` remain,
unwired.
- **PRINCIPLED CUTOVER (next) — the body NEVER touches the priority controller.** The controller
  admits *scatter-works* only; on admission success `taskPostWork`/`funnelPostWork.Execute`
  `PushBack`s the body to the executor's Handoff via the EXISTING `wk.task = nil` ownership
  transfer (the controller `Free`s the scatter-work normally; the executor owns+frees the body).
  **No `HandedOff`, no `Execution` change, no controller `Free`-skip.** Topology (PN): `incoming`
  becomes the scheduler's admission-intake Handoff (`AddWork` pulls scatter-works); a SEPARATE
  Handoff is the executor's body intake. Nested admission runs on the scheduler (off the body's
  goroutine), so the blocking body-`PushBack` is on a scheduler worker, not the nested body —
  preserving nested non-blocking without a `HandedOff` flag. Funnel-gate hoist (Wrinkle 1) IS
  needed here (gate in `funnelPostWork` before the body crosses, since `funnelWork` now runs on the
  executor). This likely dissolves the leak (it lived in the `HandedOff` contention).
- **Dump signature (decisive):** at deadlock only **6 goroutines** — the timeout alarm, the test
  goroutine, and **4 parked in `skimSelect`** (1 top-level `CloseAndSkimAll` + **3 executor bodies
  driving nested `SkimAll`s**). **ZERO scheduler (`worker.Pool`) workers, ZERO `PushBack`-blocked,
  ZERO mutex/semacquire.** = the skill's "all workers exited, only SkimAll parked → lost
  wakeup / stuck reference" class, NOT a lock cycle.
- **Diagnosis (hypothesis, unconfirmed):** before the wiring, bodies ran *on* scheduler workers,
  keeping the scheduler pool warm while work was in flight. Now bodies run on the executor, so the
  scheduler scales to zero aggressively. A nested sub-wave's work then needs a scheduler to admit
  it (and an executor to run it), but the scheduler is gone and the re-spawn/re-wake is missed — OR
  a sub-wave reached Done and the parked `SkimAll` missed the Done wake. The wiring moved
  body-completion bookkeeping (`Free`→`DecrementWork`→Done/skim-wake) from the scheduler worker
  onto the executor goroutine — a candidate lost-wake site to scrutinize.
- **Repro is HARD (Heisenbug):** only reproduces under **`-race` at DEFAULT config (~1/25)**. Every
  bias tried SUPPRESSED it: zero `SelfTime` 0/60 (no-race) + 0/40 (-race); 2ms idle-timeout 0/40
  (no-race) + 0/30 (-race); `-race`+`-trace` 0/60 (trace overhead masks it). So it's timing-tight
  and tied to the default 1s idle + µs–ms SelfTime. The captured-trace approach failed (trace masks
  it); needs a different tactic — e.g. add invalid-state panics / targeted `trace.Logf` at the
  scheduler spawn-on-demand and the wave Done/skim-wake, or an "op started-vs-completed" diff to
  prove whether work is pending-unadmitted (lost spawn) vs. done-but-unwoken (lost Done-wake).
- **REFINED DIAGNOSIS (2026-06-28d, deeper dig — supersedes the lost-spawn guess above):**
  - **It is a LEAKED WORK/REFERENCE, not scale-to-zero.** Discriminator run: scheduler idle set
    to 1h (never scales to zero mid-run) STILL hangs (1/80) → scheduler scale-to-zero is NOT the
    trigger. The 1h-idle hang dump is the clean tell: ~80 *idle* scheduler workers (kept alive by
    1h idle), nothing running, and **ONE lone top-level `SkimAll` parked** — its wave never reached
    Done despite no in-flight work. `skimSelect` (wave.go:577) includes `case <-state.Done()` (a
    closed channel — reliable), so it is NOT a lost-Done-wake: the wave's in-flight/ref count is
    stuck > 0, so a body (`taskWork`/`funnelWork`) was `IncrementWork`'d at dispatch but its
    `Free`→`DecrementWork` NEVER ran (or a funnel-instance barrier leaked).
  - **The executor Handoff orphan/abandon machinery is CORRECT** (verified by reading
    `rdvq/inboxonly.go` PopFrontFunc + `handoff.go`): sender-before-abandon → orphan drained &
    run (ok=true); sender-after-abandon → abandonment marker makes TryPushBack skip the dead inbox
    → block-as-demand spawns fresh. So the leak is NOT a lost handoff there.
  - **Controller HandedOff path looks correct** (single-item-per-drive: a handed-off item Starts →
    tryAccepted returns → drive ends → executor.Reset clears wasHandedOff; releaseOthers nils the
    item's buffer slot so it isn't requeued; executor owns+Frees). No cross-item leak found by
    inspection.
  - **HEISENBUG resists ALL observation:** reproduces ONLY at DEFAULT config under `-race` (~1/25).
    SUPPRESSED/masked by: zero or small `SelfTime`, 2ms idle, amplified nesting, `-trace` (0/60),
    AND even lightweight atomic counters + a 2s ticker goroutine (0/60). Also: small samples are
    statistically meaningless here — P(0 hangs in 40 at 1/25) ≈ 20%, so earlier "suppressed"
    reads were underpowered. Any added goroutine/sync shifts the window.
  - **NEXT TACTICS (untried / promising):** (1) a NON-perturbing leak witness readable only from the
    `-timeout` goroutine dump — e.g. park a sentinel goroutine whose stack/select encodes the live
    body count, or have the executor-pool scale-to-zero point assert "no handed-off-but-unrun
    bodies"; (2) op-type bisect (funnel-only vs launcher-only) and limiter on/off — but ONLY with
    large samples (≥150 -race) or after finding a high-rate amplifier; (3) deep code review of the
    **funnel-instance barrier** lifecycle (the dumps prominently feature funnels) vs the new
    `funnelWork.Run`+`Free`/permit-release-moved-to-Free change; (4) since the leak is a missed
    `DecrementWork`, add a per-wave `IncrementWork`/`DecrementWork` pair-tally that PANICS on a
    detectable imbalance at a sync point (gut: the hang has no sync point, so this needs a teardown
    hook). The captured hang dumps are in the scratchpad (`noidle_72.log` = the clean 1-stuck-wave
    case).


**►►► C2 IN PROGRESS — `execpool.Pool[W]` FOUNDATION LANDED; SCHEDULER (workq.Scheduler)
NEXT (Phase 2b, 2026-06-28).** The pool-split is being built bottom-up: one shared
goroutine-pool foundation, two pools on it (executor + scheduler), then the live cutover.
Design converged through a long review with PN this session — the notes below SUPERSEDE the
earlier "uncapped executor" and "execpool forks worker.Core" sketches.

**►► SESSION 2026-06-28b DECISIONS (PN), refining the steps below:**
- **MERGE steps 2+3** — build `workq.Scheduler` in its *real post-cutover shape* and flip the
  live path in one landing, NOT a dormant inline-body intermediate first. Rationale: the
  intermediate's postpone path is dead/untestable (scheduler-run `taskWork` always `Starting()`s);
  the clean `Wait`/`Work` boundary only exists at cutover semantics; the executor foundation is
  already proven (step 1), so merge = wire proven executor to new scheduler `Worker`, not bring
  up both at once. Build the Scheduler ALONGSIDE legacy + test against the live executor in
  isolation, THEN one cutover flip (mitigates the red window).
- **KEY FINDING (the reason the scheduler decomposition is *required*, not optional):** the plan
  doc's "just redirect the two `*PostWork.Execute` Posts → `executorPool.PushBack`" is the
  *producer-side* redirect and is **WRONG for nested submits** — `PushBack` is a blocking
  rendezvous (no `TryPushBack`), and a nested submit runs the admission chain inline on its
  body's executor goroutine, so a producer-side `PushBack` blocks the body and violates
  "nested intake = non-blocking drop-and-go." The blocking handoff MUST be the **scheduler's
  `Work`** (nested drops to buffered `Accepted` non-blocking; a scheduler worker `PushBack`s).
  ⇒ `Wait` = non-blocking admit (governor + `Acquire`, postpone missers), `Work` = blocking
  `PushBack` of the admitted body. This requires **separating non-blocking admit from blocking
  handoff in the admission chain** (`launcherScatterWork`/`limiterScatterWork`/`*PostWork`) —
  the genuinely hard, concurrency-critical core of C2. (Recorded in the plan doc's C2-mapping
  banner.)
- **Top-level executor fast lane = DEFERRED follow-on** (see STEP 5 below): any-top-level (not
  just no-limiter), landed + benchmarked AFTER the split is green. First cut: ALL bodies
  (top-level + nested) go scheduler-intake → scheduler `Work` `PushBack`.
- **MERGED CP SEQUENCE (each a green checkpoint):**
  - **CP1** (additive, unwired): `workq.Scheduler` + scheduler `Worker` on `execpool.Pool[W]`,
    finishing `internal/workq/worker.go`'s draft — `Wait`=drain+collect+ (none ready) block on
    `Accepted` waiters composing pool idle+stop; `Work`=execute. Reconcile env-on-ctx
    (`workerEnvFromContext` vs ctxpool `W`). Isolated test (incl. postpone/anti-spin via nested
    scenarios + a real `execpool.Executor` for the handoff). Legacy `worker.Pool` untouched.
  - **CP2** (the admit/handoff split): refactor the admission chain so non-blocking admit
    (governor+`Acquire`, postpone-on-miss) is separable from the blocking `PushBack`; funnel-gate
    hoist (Wrinkle 1) lands here (gate inside `funnelPostWork` before the handoff). Green under
    single pool first if possible.
  - **CP3** (cutover flip): `defaultPool` → `workq.Scheduler`; bodies → executor (`run(ee)` +
    `defer Free`, delete `*Work.Execute`); `streampool.Wait()` reaps both pools. *Gate: full
    suite + -race + `TestBySimulation` reliably green + latency/alloc benchmarks (real
    methodology).*
  - **CP4**: delete `internal/worker`; trim workq's exported API.

**►► SESSION 2026-06-28c — TOPOLOGY PINNED (PN), supersedes the CP framing above where it conflicts:**
- **`worker.Pool` / `worker.Core` is OBSOLETE** — both pools are `execpool`. Scheduler =
  `execpool.Pool[*schedulerWorker]`; executor = `execpool.Executor[*workerExEnv]`. `internal/worker`
  gets deleted.
- **`workq.Queue.incoming` (the `Pending` field, the scheduler's intake where AddWork pulls work)
  becomes an `rdvq.Handoff[Work]`** — the producer→scheduler rendezvous. Its **block-as-demand IS
  the scheduler pool's spawn signal** (the producer's `PushBackFunc` selectFn fires
  `scheduler.pool.RegisterUnmetDemand`, exactly mirroring `execpool.Executor.PushBack`). So there is
  **no separate demand-counter to invent** — the Handoff supplies it. Handoff has `TryPushBack`
  (direct handoff, non-blocking miss) but **no `TryPopFront`**, so the controller's non-blocking
  pull probe (`TryAddNew` with nil waiters) becomes a no-op for `incoming`; the blocking
  `WaitForNew` path does `incoming.PopFrontFunc` composing the Accepted waiters' workWaitCh +
  scheduled deadline + execpool idle + stop. Schedulers never run bodies, so a scheduler is always
  promptly available to take from `incoming` (nested rendezvous is short — always-live-dispatcher).
- **The executor's Handoff is SEPARATE** from `incoming` (scheduler→executor, inside
  `execpool.Executor`).
- **The scheduler REUSES the controller** — `schedulerWorker.Wait` = `accepted.ExecuteOne` (pull
  from `incoming` Handoff, composing execpool's idle), `Work` = no-op. NO `ExecuteOne`
  decomposition and **NO env-on-ctx reconciliation**: the scheduler runs only admission
  scatter-works (`launcherScatterWork`/`limiterScatterWork`/`*PostWork`), which never need E; E is
  passed directly to the body's `run(ee)` on the executor. The body→executor hop lives inside
  `taskPostWork.Execute`/`funnelPostWork.Execute` (`→ executorPool.PushBack(body)`), and the
  existing `wk.task = nil` ownership-transfer means the executor owns+frees the body (no
  `HandedOff` signal needed).
- **Dispatch:** the admission scatter-work is PushBacked to `incoming` for the scheduler to admit
  (governor + non-blocking permit `Acquire`; success → `executorPool.PushBack(body)`; miss →
  postpone to Accepted). Bodies (`taskWork`/`funnelWork`) gain `Run(ee)` + self-`Free`; their
  `Execute(ctx,ex)` is deleted. Funnel-gate hoist (Wrinkle 1) folds in. `streampool.Wait()` reaps
  both pools.

- **`internal/execpool` FINAL SHAPE (landed, `fa17a48` + `f031f71`; isolated, unimported).**
  `Pool[W Worker]` is the **single shared spawn/lifecycle foundation** (NOT a fork that
  duplicates worker.Core — worker.Core is to be deleted; the scheduler reuses THIS). It owns
  the loop `for { Wait; Work } ; Close`, the capped demand-driven spawn, the refcount/`Wait`
  lifecycle, the **pooled idle timer** (`internal/timerp`), and the **reused worker ctx**
  (`internal/ctxpool.WithValue(poolCtx, w)` — carries `W`, `Done()` == poolCtx == stop). The
  worker is a `Worker` interface:
  - `Wait(workerCtx, idle <-chan time.Time) bool` — become idle / block for work (composing
    the supplied `idle` + ctx.Done stop), stash it, return false on idle-out/stop. **Idle is
    supplied by Pool** so it keeps idle-timeout policy.
  - `Work(workerCtx)` — execute what Wait stashed.
  - `Close(workerCtx)` — teardown; **Pool never touches W after Close** (poolable).
  - Spawn model = **demand counter, not edge+chain-heuristic**: `RegisterUnmetDemand` /
    `UnregisterUnmetDemand` (a source records work it couldn't place on a waiting worker, and
    un-records it when taken/withdrawn); `maybeSpawn` spawns while `unmetDemand > spawning`,
    capped by `spawnConcurrencyLimit` (=1), re-evaluated when a worker establishes (frees a
    spin-up slot). Precise ramp, **no over-shoot tail**; `Wait`'s bool is just continue/stop.
    The cap is load-bearing **independent of backpressure** (spin-up cost / goroutine glut,
    NOT throttling admitted work — admission already happened upstream).
  - `Executor[E]` = the concrete executor on `Pool[*executorWorker[E]]`: its Worker waits on
    an `rdvq.Handoff` (PopFront), runs `Task[E]`. `PushBack` is block-as-demand —
    RegisterUnmetDemand on the first park, Unregister on return (delivered/cancelled).
  - **No `Locked`-suffixed methods** (PN standing pref; lock contract in comments).
  - Verified each commit through the FULL pre-commit hook (suite + -race + golangci 0).

- **STEP 2 = `workq.Scheduler` on `execpool.Pool[W]` (NEXT).** Build the scheduler as a
  second pool on the SAME `Pool[W]`, **in package workq** (the pool is a workq impl detail;
  this also lets workq's exported API shrink). The scheduler's `Worker` decomposes the
  existing `Accepted.ExecuteOne` (settled with PN):
  - `Wait` = ExecuteOne's **find** phase (fresh → postponed → scheduled priority). Found
    ready work → stash + return immediately (busy, not idle). Nothing ready → call
    **`AddWork`** (register as an available waiter) and **block** there (composing the pool's
    `idle` + ctx stop); return the pushed item, or false on idle-out/stop. **AddWork
    registration IS the idle/available point.**
  - `Work` = ExecuteOne's **execute** phase on the stashed item (controller `work.Execute` +
    `Starting`/postpone bookkeeping + `onSecure`). One ExecuteOne = one Wait + one Work; the
    only blocking point is AddWork. VERIFY the **postpone path** (work that registers a
    listener and doesn't Start) lives in Work and re-queues, so the worker loops back to Wait.
  - Demand wiring = workq's existing `unmetDemandFn` re-expressed: a `Post` that can't hand
    off to an AddWork-waiting worker → `pool.RegisterUnmetDemand`; a worker whose Wait returns
    work → `UnregisterUnmetDemand`.
  - **Env-on-ctx reconciliation (open):** the worker ctx carries `W` (ctxpool), but the
    scheduler's bodies (`work.Execute`) fetch their env via `workerEnvFromContext`/
    `workerEnvKey`. Step 2 reconciles: either the scheduler's `W` stamps the env under
    `workerEnvKey` in `Work`, or `workerEnvFromContext` migrates to `ctxpool.GetValue[W](ctx).env`.
  - **SCAFFOLD ALREADY EXISTS — `internal/workq/worker.go` is a DRAFT of exactly this** (its
    header: "DRAFT — first cut of the Worker driver"). It has `Worker[E]` with `selectWork`
    (the ONE canonical AddWork block: inbox/outbox/workWait/deadline/idle/done/ctx), `pull`
    (drain `incoming` → fresh), idle/onSecure/onWait, and a `Help` nested-drive sketch (the
    block-and-help the limiter reclaim path wants). Its `DriveOne` still delegates to the
    legacy `ExecuteOne` as a stopgap (line ~140: "the native driveOne will return the pair
    directly"). **Step 2 = finish this draft natively and reshape it to the `Pool[W]` Worker:**
    `Wait` = `DriveOne`'s find half (drainScheduled → fresh; collect fresh/postponed; else
    `pull`→`selectWork` block at AddWork); `Work` = `controller.execute` on the stashed item
    **minus `onSecure`** (Pool.establish does that now). Verified split point: `collectAccepted`
    (find) vs `execute` (run) in `accepted.go` cleave cleanly; `onSecure` (accepted.go:605,
    release spawn token before body) maps onto `Pool.establish` at the Wait→Work boundary and
    leaves the controller.
  - **APPROACH = additive, no red window:** build `workq.Scheduler` + scheduler `Worker` in
    `workq` ALONGSIDE the legacy `ExecuteOne`/`worker.Pool` (both stay green), test Scheduler
    in isolation, THEN cut over (step 3), THEN delete legacy `ExecuteOne` + `worker` (backward
    compat intentionally dropped — PN: `ExecuteOne`'s buffer/postpone-loop complexity existed
    to protect `Accepted` from externally-pushed work; with `Scheduler` the public face it can
    be decomposed to fit `Worker` naturally). The other session also edits `workq`/`funnel` —
    ideally quiesce the tree for this build.

- **STEP 3 = cutover:** `defaultPool` → `workq.Scheduler`; the two body-running posts
  (`taskPostWork`/`funnelPostWork`, the only `defaultPool.Post` callsites) → `executor.PushBack`;
  fold `Free` into `taskWork`/`funnelWork.run`; delete `taskWork`/`funnelWork.Execute`; wire
  `streampool.Wait()` to reap both pools. **STEP 4 = delete `worker`; trim workq's exported
  API** (unexport Worker/NewWorker/With*/ExecEnv behind Scheduler; keep the producer + gate
  types). *Gate: full suite + -race + `TestBySimulation` + latency/alloc benchmarks (real
  methodology).*

- **STEP 5 (DEFERRED follow-on, decided w/ PN 2026-06-28) = top-level executor fast lane.**
  At top-level dispatch BOTH gates already run inline on the caller's goroutine
  (`meta.ExecuteNowOrQueue` → `launcherScatterWork.Execute` governor → `limiterScatterWork.Execute`
  permit gate → `taskPostWork.Execute`), so the scheduler is a pure relay for top-level work.
  Route top-level `taskWork`/`funnelWork` straight to `executor.PushBack`, bypassing the
  scheduler queue (saves enqueue + scheduler-worker wake + dequeue on the hottest path — a
  P99/max win). **Enabling condition is *top-level* (blocking-capable caller, admission done
  inline), NOT no-limiter** — a top-level *limited* launch acquires its permit inline too, so
  the lane is **any top-level dispatch** (PN, 2026-06-28; fairness is arbitrated at the permit
  pool, not the scheduler queue; suspend/reclaim is wave-queue-driven regardless of routing).
  Backpressure preserved: the governor's block-and-help still wraps dispatch inline; a full
  executor applies block-as-demand. **Nested submits CANNOT take this lane** (must be
  non-blocking drop-and-go; `executor.PushBack` is a blocking rendezvous, no `TryPushBack` by
  design) → they keep the buffered scheduler intake. Net: after the lane lands the scheduler
  handles ONLY nested + funnel-scheduled + postponed-limited-retry (the deferred/postponable
  admission — also where the scheduler's postpone path is actually exercised). **DEFERRED to
  AFTER the two-pool split (steps 2–4) is green + benchmarked**, so we measure the removed hop
  rather than assume it and don't entangle the lane branch with the already-large cutover.

- **C2a STATUS:** `run(ee)` extraction LANDED (`a107270`) — `taskWork`/`funnelWork` expose
  the Execution-free, ctx-free `run(ee *workerExEnv)` C2c needs. The **funnel-gate hoist was
  REVERTED** — it is NOT a clean mirror of the task path: the task gate sits inside
  `launcherScatterWork` (governor wraps it), but the funnel's governor rides
  `funnelPostWork.onWait`, so wrapping `funnelPostWork` with `limiterScatterWork` puts the
  gate OUTSIDE the governor and a top-level blocking submit hits the `ExecuteNowOrQueue`
  block-guard. **Deferred to C2c**, where the funnel seam-flip happens anyway (gate inside
  `funnelPostWork.Execute` before the post, preserving governor→gate order — needs sim
  validation).

- **MULTI-SESSION CAVEAT:** a concurrent session (session_011…) committed `a5abde3` +
  `bc187f3` (Resequencer/RangeResequencer + edge/edgegrpc demos) into this same working tree
  mid-session, and edits `funnel.go`/`edge*` live. Watch `git status` before committing;
  serialize on shared files (esp. `funnel.go`/`workq` for step 2).

**►►► B LANDED — DISPATCH INFRA (ISOLATED, NOT WIRED) (Phase 2b, 2026-06-27).** The two
building blocks the pool-split (C2) needs, both standalone with no live consumer yet:
- **B1: `rdvq.Handoff[T]`** (`internal/rdvq/handoff.go`) — the lock-free **unbuffered**
  rendezvous = the existing `inboxStackQueue` (inbox tier, LIFO warmest-first consumer
  selection) + a new sender-side `inboxWaiters`. Blocking `PushBack(ctx,value)` (park on
  `inboxWaiters` via the standard register-then-recheck confirm); receiver wakes one parked
  sender after registering its inbox; **no `TryPopFront`** (no outbox tier to poll). Senders
  are never stale (each actively sends), so NO renotify conservation is needed (unlike the
  permit pool). Tested: concurrent exactly-once, sender-blocks-then-delivers, ctx-cancel
  both sides; 20× `-race` + full rdvq `-race`.
- **B2: generic `worker.Core[E]`** (`internal/worker/pool.go`) — the demand-spawn +
  idle-exit + refcount/`Wait` lifecycle factored out of `worker.Pool`, with a **pluggable
  `WorkerLoop[E]`** and the demand source decoupled (`TrySpawn`). `worker.Pool` (the
  scheduler pool, `NewPool`) is rebuilt as `Core + sharedQueue + driveQueue` (the
  workq.Worker loop) — `defaultPool` and all its promoted methods unchanged. Isolated `Core`
  tests added (demand-spawn, Wait-join+reuse, scale-to-zero). Live path unchanged: full
  suite + 60× `-race` sim green.
- **Deferred to C2:** the **block-as-demand** hook (a `Handoff.PushBack` park → spawn an
  executor) is intentionally NOT in B1 — it lands when the executor pool is wired. The
  executor pool itself = `Core[E] + Handoff + a PopFront→Run loop` (C2).

**►►► C1 LANDED — NATIVE PERMIT CORE IN THE LIVE LIMITER; THE DEADLOCK IS FIXED
(Phase 2b, 2026-06-27).** The eager `limiter.go` request machinery (`directScheduler`/
`directRequest`/`request`/`acquireOrWait`/`reclaimRequest`/`applicant`/the `resource`
interface) is **replaced** by a native `internal/permits` integration on the **single**
`worker.Pool` (no pool split — that is C2). Surface unchanged. This is the
deadlock-fix milestone: **`TestBySimulation` is reliably green, including ≥300 `-race`
runs** (the pre-existing ~1/120 `-race` hang is gone). Verified: full `./...` `-short`
suite + `-race` + lint (golangci 0 issues) + `internal/permits` rapid/race.

- **Forest construction** (`wavepermits.go`): one `permits.Cache` per `(wave, Limiter)`
  = `C_W^L`. A Wave lazily owns `map[*permits.Pool]*Cache` (guarded), mkdir-p'd along the
  driving `ctxMeta.parent` chain at dispatch (`ensureCache`/`ensureCacheChain`/
  `createCache`); the immediate forest parent is the dispatcher's wave (`M.wave`), so the
  canonical "parked parent lends to sub-wave" case inherits directly and deeper gaps fall
  back to a forest-wide steal (still correct). Self-ref dropped at **wave-Done** via a new
  `wavestate` **`onDone`** hook → `releaseCaches`; descendant `NewChild` refs outlive.
- **Native handle** (`permithandle.go`): `ctxMeta.heldRequest request` → `ctxMeta.held
  *heldPermit` `{ownCache, permit}`; `currentHeldRequest` → `currentHeldPermit`. A zero
  `permit` IS the suspended state (re-entrancy no-op falls out). Created at dispatch
  (launcher `newScatterWork` / funnel `Init`), stamped at `borrowBodyContext`, acquired at
  the gate, released at completion (pooled via `heldPermitPool`).
- **Gate = three modes** (`gateAcquire`): nested/queued → non-blocking `Acquire` +
  `Pool.ListenersFor()` postpone; top-level → **block-and-help** on `Pool.Waiters()`
  (`blockAcquire`); mid-body reclaim (the suspend brackets) → **help-shaped** loop
  (`reclaim`). **`wv.block` is RETAINED and retargeted onto the Pool's waiters** — the
  earlier "no help loop / bounded skim-retry" sketch was WRONG (a plain reclaim deadlocks
  vs a result-poster; a `wv.skim` loop never sees a permit-free wake). Suspend/reclaim is
  **coarse per drive call**.
- **Lost-wakeup fix (a cutover regression):** a residual `-race` hang root-caused NOT to
  permit accounting (forcing unlimited permits → 120/120 `-race` pass) but to a **lost
  wakeup** the cutover introduced by splitting the Pool's wake into two bare `Notify(nil)`
  calls, dropping the **renotify conservation** the eager scheduler's single `rdvq.Notifier`
  had. A stale postpone listener swallows a bare wake without re-delivering it. Fix: keep the
  Pool's wait/wake as ONE `rdvq.Notifier`; `Release` wakes with `notify.Notify(nil)` (wrapped
  renotify → a consumer that can't use the wake re-delivers it down the chain to a real
  waiter). `WakeAll` (NotifyAll) only for multi-permit events (destroy / `SetMaxConcurrency`).
  Single wake + conservation, no thundering herd. (An earlier `WakeAll`-on-every-release was
  rejected as a band-aid.) Also fixed a reuse data race: `wavestate` `onDone` now runs BEFORE
  `close(doneChan)` so cache teardown completes before a re-arm can race it.
- **Deferred to C4 / not yet done:** the `applicant` sizing (`Processor`/`Value`/`Err`)
  was removed (re-derive natively when weighted resources land); multi-limiter still panics
  at construction (`opConfig.singleLimiter`); drain limiting (C3) untouched.

**►►► PERMIT CORE: HYBRID (LOCK-FREE HOT PATH, LOCKED FOREST) + WAIT/WAKE —
`internal/permits` (Phase 2a, 2026-06-26).** The concurrent core is a hybrid: the hot
acquire path is lock-free (atomic128 counter), the forest structure is an intrusive
doubly-linked list guarded by per-`Cache` mutexes. This **supersedes the fully-lock-free
`nbcq` forest** (`6ba8833`): that port had to drop the original move-to-back LRU
(reordering a shared lock-free queue isn't possible) and grew a sentinel-cycle steal +
lazy reaping + (planned) gen-tagged entries / a `next`-pointer fast lane just to claw the
LRU back. A per-`Cache` mutex around a DLL gives **O(1) interior move-to-back** (the
original LRU, restored), **exact removal** (no lazy reaping), and **direct front-to-back
traversal** (order-based camping, no sentinel/fast-lane) — far simpler. The lock is OFF
the common hot path; if a specific list ever shows up as a tail-latency bottleneck, swap
that list internal for lock-free without touching `Acquire`/`Release` (localized, gated on
measurement). This is essentially `e5b20b0`'s "lock-free hot path, locked steal/destroy",
chosen deliberately over the lock-free redux.
- **2a-i** packed `(held, inUse)` into one 128-bit atomic word (`atomic128`; no GC-shadow
  since both halves are scalars). Both halves are `uint64` amounts — a weighted Resource
  (memory limiter >4 GiB) is representable; weight-1 ops now, the width is headroom. Gated
  CAS transitions keep `0 ≤ inUse ≤ held` atomic. **(Unchanged by the hybrid pivot.)**
- **2a-ii (hybrid)** the acquire up-walk (steps 1–2) stays lock-free, ancestors **pinned
  by refcounts**. Each cache's children and the Pool roots are an intrusive `cacheList`
  (DLL) under a per-list mutex, kept coldest-first by **`touch`** (an acquire up-walk that
  passes a cache *unsatisfied* moves it to the back — O(1) relink under one lock; a
  satisfied hit pays nothing). The **steal** is a front-to-back DFS taking the first
  borrowable cache (the coldest), left in place so a still-borrowable victim is re-picked
  (order-based camping). **Deadlock-free:** the steal is the only op holding two list locks
  at once and always descends root→leaf; every other op (touch/pushBack/remove) takes a
  single list lock, so no cycle can form. `destroy` **unlinks exactly** then CAS-drains
  held to the Resource (`counts.drain` still coordinates with a concurrent `stealOut`).
- **2a-iii** the rdvq wait/wake (unchanged): non-blocking `Acquire` (manager admit) +
  blocking `AcquireWait(ctx)` (executor reacquire — parks, re-searches on each freed
  permit, confirm callback re-runs Acquire after registering = the lost-wakeup guard).
  Every `Release` and the capacity a `destroy` returns wake parked waiters (gated by a
  waiter count so the uncontended release is one atomic load).
- Validated: 50k `rapid` (algorithm, sequential) + `-race` stress ×10 (contended
  inherit/delta/steal, structural churn vs steal, `AcquireWait` liveness) + order-camping /
  `touch`-redirect / exact-removal unit tests; build + `golangci-lint` clean. The
  fully-lock-free `nbcq` forest, sentinel-cycle, lazy reap, and fast-lane are GONE.
- Committed `9af3d13` (includes the `docs/permit-core.md` reconciliation to the hybrid).

**►►► PHASE 2b DESIGN IN PROGRESS (2026-06-27) → `docs/plan/dispatch-execution-split-phase2b.md`.**
The dispatch-side design is being worked out in discussion; the plan doc holds the current
state. Converged so far: **one generic worker pool** (the current `worker.Pool` lifecycle —
demand-spawn + idle-exit + refcount/`Wait` — with a pluggable per-worker loop) instantiated
**twice** — an **executor pool** (runs user bodies, may block; simple `PopFront`→`Run` loop)
and a **scheduler pool** (`workq.Worker`-style over a shared `workq.Accepted`; non-blocking
permit acquire + governor, then hands off). The **scheduler→executor handoff is a NEW
unbuffered rdvq primitive** = `inboxOnlyQueue` + a sender-side `inboxWaiters` (= today's
`rdvq.Queue` minus the whole outbox tier, plus blocking `PushBack`; **no `TryPopFront`**) —
pure rendezvous, zero buffer dwell. The **body→scheduler intake stays the buffered
`Accepted`** (nested submit is non-blocking drop-and-go; postponed/scheduled are necessary
buffering). Key invariants: **LIFO consumer selection is load-bearing for scale-to-zero**
(FIFO would pin the pool); **a buffer-push and a spawn are the same event** (block-as-demand
→ P99 win); **demand fires only for spawn-gap buffering, never for backpressure**
(permit-free/governor-clear *wake* a parked scheduler, never spawn).
**FOREST CONSTRUCTION SETTLED:** one `permits.Cache` per `(wave, limiter)` (`C_W^L`),
**bodies are occupants** (a `Permit`, not a node); the L-forest mirrors wave nesting. At a
wave's first L-admission, **lazily mkdir -p the ancestor L-cache chain** (held=0
pass-throughs up to the nearest existing L-cache or Pool root — *don't* skip non-L
ancestors, else concurrent re-parenting), then acquire into the wave's own `C_W^L`.
Inheritance is **occupy-in-place** (`inUse++` on the ancestor, permit doesn't move);
**suspend = `Release` the body's own permit to its backing cache** (own wave for
checked-out, ancestor for inherited), **reclaim = `AcquireWait` from the wave cache
outward**. Cache refcount is **separate** (self-ref dropped at wave-Done; descendant refs
keep ancestors alive for sub-sub-waves). Multi-limiter: joint acquire in canonical order,
partial-miss → release+postpone. Rejected: per-body nodes+transfer, hoist-on-inherit
(alternation churn), skip-ancestors (re-parenting), Cache-as-single-permit/held-replication.
Pool all `Cache` allocs. Full write-up in the plan doc's "Forest construction" section.
**DRIVE RULE (settled):** a body holds its permit ONLY while running *its own user code*;
it lends for the **whole drive** (incl. running that sub-wave's skim handlers — handlers are
drain) and reacquires only when the drive call returns to its own code — **coarse, per
drive-call, NOT per-handler**. (Fix `permit-core.md`: strike "reacquire before each skim
handler" + the bound's "or a skim handler".) **DRAIN LIMITING (settled, opt-in):** dissolve
intake-vs-drain for limiting — `NewSkimmer(h, WithLimits)` limits a handler;
`NewFunnel(factory, WithLimits, WithFlushLimits)` limits accumulate (intake) and flush
(drain) separately. Default = limiter-free drain (common path unchanged). A limited
handler/flush acquires its OWN limiters (own cache, not the driver's permit) → just another
forest body. Revises "limiters gate intake, not drain"; deadlock-free by the per-limiter
machinery + "can't skim a wave you're part of" — **but MUST be model-checked** (parked
holder whose drain needs a permit, same-limiter-inherit + cross-limiter). Surface change to
ratify (WithLimits on Skimmer, WithFlushLimits on Funnel).
**GOVERNOR PLACEMENT (settled):** the per-wave `Governor`+`downstream` mechanism is
unchanged in purpose; the gate is checked on **both admission paths** (top-level skims if
clogged, scheduler postpones if clogged); `decrementDownstream` relief **wakes the
scheduler, never spawns**. Two retry triggers — permit-free + governor-clear — feed the one
`Accepted` waiter set.
**MIGRATION SEQUENCE (settled, no flag day, full detail in the plan doc):** 0) permit-core
hardening (pool `Cache`, model-check limited-drain + multi-limiter, fix `permit-core.md`);
1) **C1** permit core into the live limiter on the **single pool** (gut, don't remove) —
gated on **`TestBySimulation` reliably green = the deadlock-fix milestone**; 2) **B** the
dispatch infra (unbuffered rdvq primitive + generic-pool refactor), isolated; 3) **C2** the
pool-split cutover — gated on suite + sim + **latency benchmarks (real methodology)** = the
architecture+latency milestone; 4) **C3** drain limiting; 5) **C4** strip the dead eager
code. Key insight: C1 fixes the deadlock and is validated **before** any pool-split risk.
DESIGN COMPLETE — only the inbox-stack lock-freedom is deferred (measurement-gated).
**STEP 0 IN PROGRESS:** the steal now **ref-pins its victim** across `stealOut`
(`tryPin` — a conditional CAS that refuses to resurrect a cache committed to destroy;
`searchList` skips dying caches) — fixing a real near-bug (the victim is cross-subtree,
so only GC kept it alive across the take) and unblocking pooling; **`Cache` is now pooled**
via omnipool (recycled in `destroy`, safe only because of the pin); `permit-core.md`'s
"Driving is an alternation" + the concurrency-bound invariant are corrected. Validated:
`-race` ×10 + 50k rapid + lint. Remaining step-0 model-check items (limited drain,
multi-limiter) fold into C1/C3 where those patterns get wired. **NEXT = C1** (permit core
into the live limiter, single pool). See the plan doc.

**►►► PERMIT CORE SKETCH BUILT + MODEL-CHECKED — `internal/permits` (2026-06-26).**
Phase 1 of the dispatch/execution split: the isolated, model-checked hierarchical permit
cache that both `docs/permit-core.md` and `docs/dispatch-execution-split.md` mandate
building **before any cutover**. Isolated — nothing imports it yet, so zero risk to the
live limiter (the eager `directRequest`/`suspendForEpisode`/`reclaimRequest` in
`limiter.go` stay load-bearing until Phase 3). Four types with a real behavioral split:
`Resource` (pluggable accounting — the only thing that knows capacity; `TryAcquire(n)`/
`Release(n)`, weight-1 for now), `Pool` (the Resource boundary + forest root — the only
place permits cross in/out of the Resource; owns the steal; never caches), `Cache`
(per-unit forest node, cache-don't-return; `Acquire` does steps 1–2, delegates 3–4 to the
Pool), `Permit` (transient run-segment handle, alloc-free). Steal telemetry is structural
sibling-list order (**move-to-back**, no logical clock); `touch` fires only on an
*unsatisfied* pass (a hit pays nothing). Validated: 5 deterministic anchors (incl. the
canonical `limit==1` parked-parent-lends-to-sub-wave hang, dissolved) + 100k `rapid`
adversarial sequences + `-race`; `CheckInvariants` triangulates Σheld across caches / Pool
mirror / Resource in-flight, and an *independent* `HasBorrowable` oracle asserts liveness
vs the guided steal search. This is the structural fix for the pre-existing ~1/120 `-race`
`TestBySimulation` nested-drain hang: a parked holder's permit is idle hence borrowable,
so its sub-wave inherits it instead of livelocking.

  **PHASE 2 ENTRY POINT (next):** map the **manager and executor pools** onto
  `internal/worker.Pool` + `internal/workq.Queue`, with `internal/permits` as the
  foundation — managers admit *non-blocking* (acquire steps 1–4, postpone on miss),
  executors reacquire *blocking* (steps 1–5, wait). **The permit core's one open piece —
  the step-5 wait/wake trigger (event-based: wake when a contended permit frees) — is
  *defined by* those two callers, so co-design it in Phase 2, not standalone.** Place the
  governor's per-wave gate on the manager admission path. Phase 3 then sequences the
  migration off the eager `limiter.go` code (no flag day). Deferred: weighted amounts (the
  `Resource` `n` param already allows it) and cross-limiter joint admission. Spec +
  sequencing: `docs/dispatch-execution-split.md` and `docs/permit-core.md` "Open / next".

  Supporting refactors landed alongside: `ctxMeta.job` retired + the `wv`(`*Wave`)/
  `wk`(Work) naming convention applied package-wide, with redundant/dead params pruned
  across the dispatch + skim paths; `streampool.Wait` now clears the `internal/ctxpool`
  reuse caches after the worker join.

**►►► FUNNEL ENGINE REMOVED — FLUSHES ON THE SHARED POOL (2026-06-24).** The per-Wave
`funnelEngine` + flusher goroutine + `cpworker.go` are gone. `Funnel[T]` is now a plain
value `{wave, factory, limiter, id, instancePool, workPool}` (no inner heap object);
accumulator instances live in a per-Wave `funnelInstances sync.Map` keyed by funnel id.
Deadline flushes ride the global `defaultPool`'s scheduled queue; the end-of-work sweep is
a synchronous **enqueue-only** `wavestate.onFlushing` callback (replacing the `FlushChan`
close) that `ClaimForFlush`+`ForceFresh`es each live instance — the flush itself runs on a
pool worker via the same `funnelInstance.Execute` path as a deadline flush. No-deadline
instances are not scheduled (no 24h placeholder). Recycle rides the pop (rule R2);
`initState` clears the map. **Two spawn regressions found+fixed during verification:**
(1) DECISION B's deadline-parked worker pinned the `spawnConcurrencyLimit` token →
release it at the park point (`workq.WithOnWait`); (2) `ForceFresh`'s `Notify(demand)` was
a no-op with no parked waiter → spawn directly when `Notify` finds none. After both, the
`-race` `TestBySimulation` hang rate is **~1/120 — baseline parity**; the residual is a
**pre-existing** nested-drain deadlock (the dispatch/execution conflation, the split's
domain), not introduced here. Plan + full write-up: `docs/plan/funnel-engine-removal.md`.
Verified: full `./...`, `-race` suite, 120× `-race` `TestBySimulation` (parity),
`reuse_test.go`, alloc tests. (`Example_observable` is independently flaky — known.)

**►►► WAVE-SCOPED FUNNELS, NO EXPLICIT LIFECYCLE LANDED (2026-06-23).** Funnel has no
`Close`/`Dup` and no teardown: `Funnel[T]` is a plain value (no leakguard), and
`AccumulatorFactory` has no `Close` either. The whole mechanism is a contract — the
framework never touches an instance after `Flush`, and flushes every outstanding
instance before the wave drains (the per-instance wave-barrier ref + the end-of-work
flush sweep) — which lets users pool their own state against well-defined lifetimes.
`internal/leakguard` deleted. (An earlier cut, `a2fceba`, kept `factory.Close` +
wave-driven finalization; we then dropped `factory.Close` as unnecessary.) Plan:
`docs/plan/funnel-lifecycle.md`.

**►►► ZERO-VALUE WAVE LANDED (2026-06-23).** The 2026-06-21b Wave lifecycle (below)
is now implemented. `NewWave`/`Cancel`/`CancelAndWait` are gone; a zero-value
`var w streampool.Wave` self-inits on first use (`ensureInit`) and re-arms after a
drain (`ensureArmed`, dispatch-only) so a `*Wave` is reusable/poolable; the Wave owns
no ctx (flusher roots at Background, exits on `state.Done()`); top-level dispatch binds
via `op.In(&w)`; dispatch is unified through `topLevelCtxMeta` (cross-wave = redirect).
Plan + the two bugs found in verification: `docs/plan/zero-value-wave.md`. Verified:
full suite + `-race` suite + `reuse_test.go` + 40× `-race` `TestBySimulation`, all green.
Key subtlety: re-arm must be **dispatch-only** — a `CloseAndSkimAll` drives an empty
wave to Done during `Close`, so if skim re-armed on Done it would block forever.

**►►► B3 CUTOVER INTERMITTENT HANG — ROOT-CAUSED AND FIXED (2026-06-23).**
The ctxpool body+meta cutover (`e740d33`) intermittently wedged a wave at
`stage=Flushing inFlightWork=0 totalRefs=1` — one funnel instance never flushed, so its
per-instance barrier reference never dropped and the wave never reached Done (`SkimAll`
parked on `state.Done()`).
- **Root cause — a flush-signal subscription race in the funnel flusher.** The
  per-wave flusher goroutine read its end-of-work flush channel *inside* the goroutine
  (`worker.nextJobFlushCh = j.state.FlushChan()` at `funnelengine.go:161`). The wave's
  `Closed→Flushing` transition (`wavestate.noMoreWork`) *rotates* that channel — closes
  the old one (the flush signal) and installs a fresh one. When a funnel is created very
  late and its wave reaches Flushing within ~microseconds, the flusher goroutine can be
  scheduled to run its body *after* the rotation, so it subscribes to the **post-rotation**
  channel (never closed again) and parks forever, missing the wave's one end-of-work
  flush signal. The cutover didn't introduce the race but **widened the window**: the
  flusher's startup now does more work (`ensureCtxMeta` via ctxpool, ~29µs in the trace).
- **Proof (execution trace `trace.out`):** Wave `0x3c80014e2708` did its Flushing CAS at
  t=`005605053696`; its flusher `G=25814` didn't begin executing until `005605111488`
  (~58µs later) and ended its entire trace parked in `popSelect` on the post-rotation
  `nextJobFlushCh=0x3c8001c9e070`. Global tally: 4287 Accumulate vs 4286
  "received job flush signal" — exactly one instance's signal lost.
- **Fix:** capture `FlushChan()` **synchronously in `newFunnelEngine`** (which always
  runs during the wave's Open phase, strictly before any Flushing rotation) and pass it
  into the flusher. The pre-rotation channel is exactly the one closed at the first
  Flushing transition, and a closed channel always fires in `select` — so the signal
  can't be missed no matter how late the goroutine is scheduled.
- **Verification:** 300/300 plain + 40/40 `-race` of the reduced zero-delay repro (was
  reliably hanging pre-fix; pre-cutover baseline 0/150), full `./...` suite + linter
  green. All `TEMP B3.hang` diagnostics reverted.

**►►► SURFACE REDESIGN + DOC-ORG TARGET (LOCKED, 2026-06-21, design review w/ PN).**
A deep design pass converged the user-facing `streampool` surface and the target doc
organization. These SUPERSEDE earlier surface notes (incl. the 2026-06-20 "SURFACE
PINNED" line below) where they conflict. Authoritative until reflected into the docs.

**Surface (locked):**
- **Wave construction + lifecycle:** see the **WAVE LIFECYCLE FINALIZED (2026-06-21b)**
  block below — it SUPERSEDES the value-handle / `NewWave` / `Cancel`-`CancelAndWait`
  thinking that earlier versions of this bullet described. Net: **no constructor**
  (zero-value `var w Wave`, `*Wave`, lazy `ensureInit`), **drain-only** lifecycle
  (`Skim`/`SkimAll`/`CloseAndSkimAll` → `ErrWaveDone`; `CloseAndSkimAll` = terminal
  seal+drain, `SkimAll` = drain without sealing), no `Dup`. Surface evolution +
  rationale: `docs/decisions/surface-lineage.md`.
- **Ops are wave-agnostic** — `NewLauncher(h)` / `NewSkimmer(h)` / `NewFunnel(factory)`,
  no construction wave, no sentinel. Reusable specs definable before any wave (no
  wave-lifetime/creation-order coupling).
- **Routing = ambient + `op.In(wave)`.** In-body `op.Submit(ctx, v)` uses the body's
  ambient (framework-stamped) wave; `op.In(wave)` returns a cheap wave-bound value
  handle for top-level, redirect, or bind-once reuse. `In` (membership: the op's work
  is *part of* the wave) chosen over For/To; routing is handle-level so it never
  perturbs the ctx → Flow/trace propagate across redirects untouched.
- **Dispatch verb `Submit` kept** + **naming convention**: name ops as agent/role
  nouns distinct from their outputs — Launcher→`fetcher`; Funnel→`aggregator`
  (+`totals`); Skimmer→`collector` (+`results`). Rule: "-er for the op, plain noun
  for the output." `Start` = void-Launcher sugar. (Considered `Do`/asymmetric verbs;
  the naming convention makes `Submit` read right and keeps the clean
  `SubmitErr`/`SubmitResult` family + `ants`/`pond` familiarity.)
- **Funnel = wave-scoped**: per-(funnel,wave) accumulator instances owned by the wave,
  force-flushed at wave drain (the per-wave flusher). `Flush(ctx)` (outputs → ambient
  wave) + **`FlushTo(ctx, wave)`** (one-shot; outputs → given wave; FlushFn stays
  wave-agnostic, its ambient overridden — enables snapshot/staged capture, e.g.
  timer-driven). **NO Close/Dup** — finalization is wave-driven (in-flight==0 ∧
  sealed), which subsumes the old Funnel.Dup refcount and counts *all* feeders.
- **Limiters minimal**: standalone composable values — `NewSemaphore(n)`,
  `NewRateLimit(n, d)`; `WithLimits(...)` AND-composition, **jointly admitted in a
  global canonical order** (deadlock-free by lock-ordering, automatic, no object).
  **NO user-facing Coordinator/Scheduler, no `.Under`/grouping.** Ordered joint
  admission needs only the global order; the prioritized discipline (the only thing
  needing a central arbiter) is a single *internal* global arbiter, not exposed.
- **Flow kept, value handle**, `ctx, flow := NewFlow(parent)` — two-return (Flow *is*
  ctx-borne propagation, the deliberate exception to "no ctx from constructors").
  **Keeps Dup/Close** — the one legitimate refcount survivor (spans multiple waves; no
  single wave bounds it). Framework auto-ref/unrefs per work item; `FlowFromContext` =
  non-counting view; propagation requires ctx hygiene.
- **Three-type framing fixed**: user-facing types are **Wave + Flow** (+ ops as
  verbs); **Pool is internal** (auto-sized), mentioned only to explain sizing.
- **Deferred (no API named — own design effort)**: a declarative op-and/or-wave
  **scheduling priority** feeding work-dispatch ordering *and* the internal permit
  arbiter; anti-starvation-tempered; intake-side only; one internal global arbiter,
  no user grouping.

**Doc organization (target; strict only on release-able branches — refactor branches
may have docs lead code):**
- User-facing = **current state**: README, `doc.go` (absorbs `programming-model`),
  per-symbol API comments, `example_*_test.go`.
- **`docs/` root** = maintainer, current-state design.
- **`docs/decisions/`** = target-state design + rationale + superseded designs.
- **`docs/plan/`** = path-to-target (migration/sequencing); empty/deleted at rest.
- Positioning: concise comparison in README; deep evidence (ARCHITECTURE_COMPARISON
  source analysis, POSITIONING_RESEARCH) → `docs/decisions/`.
- `API_DESIGN.md` → reborn as the `docs/decisions/` target-surface record;
  `programming-model.md` → folds into `doc.go` (deferred to the code migration).

**►►► WAVE LIFECYCLE FINALIZED (2026-06-21b) — supersedes the Wave bullet above**
(the single-return / value-handle / adopt-parent-from-first-use thinking). Strict
stance: **ctx is DRIVER-SPECIFIC.** Like the internal Pool, a Wave owns NO ctx.
- **No constructor.** Ditch NewWave AND NewChild. `var w streampool.Wave` (zero
  value usable). A sub-wave is just a zero-value Wave first-used inside a body.
- **No Wave Cancel / Wait / CancelAndWait.** The only lifecycle op is the drain:
  `Skim` / `SkimAll` / `CloseAndSkimAll`, returning **ErrWaveDone** when
  in-flight==0 ∧ sealed. `SkimAll(ctx)` IS the structured scope; ctx is the driver's.
- **Cancellation = the drive ctx (pure).** Cancelling the SkimAll ctx → it returns
  ctx.Err(); in-flight work keeps running under its own submit ctxs (cancel those —
  usually the same ctx — to stop it). NO framework force-abort. No goroutine leak:
  workers belong to the GLOBAL pool (not per-wave); `streampool.Wait()` stops idle
  workers and joins them.
- **Lazy init + reuse.** A zero Wave self-inits its substrate on first ctx-bearing
  use (op dispatch into it, or Skim/SkimAll) via a race-safe `ensureInit(ctx)`, and
  is reusable after the drain returns (no explicit teardown call). The creation-time
  `parentJobs`/`shells.Init` stamping (was in NewWave) MOVES to `ensureInit`, keyed
  on the first-use ctx = the driving body's ctx → captures the DRIVING ancestry.
- **Driving ancestry only.** Parentage for limiters (permit inheritance) and the
  "can't skim a wave you're part of" guard is the DRIVING goroutine's ctxMeta
  nesting, captured at drive — never a creation/cancellation parent.
- **Funnel flusher re-homed** to exit on wavestate→Done (in-flight==0 ∧ sealed),
  not a CancelAndWait join.
- Wave stays `*Wave` (no value conversion); bind with `op.In(&w)`. The user owns
  pooling (`sync.Pool[*Wave]` or reuse a var). 3b (single-return NewWave) and 3c
  (value handle) are MOOT.
- **Context mechanism CONVERGED (2026-06-21/22, w/ PN) → `docs/decisions/body-context-pool.md`.**
  How "Wave owns no ctx" actually works. **The model converged on TWO decoupled pools**
  (a deliberate shift away from the single fused `{ctxMeta, childCtx}` unit the note's
  body still describes — reconcile the note on the next doc pass):
  1. **`internal/ctxpool`** reuses the **child `context.Context`** objects: a
     process-wide map of parent ctx → `childPool`, each handing out reusable
     `WithValue`-descendants of the parent (found by direct `ctx.Value`), auto-evicted
     via `AfterFunc` on parent cancel. Children pooled per-`childPool` (`nbcq`); the
     `childPool` struct itself is GC'd, not pooled (cold-path alloc; safe recycle would
     need a hot-path refcount — see the in-code comment).
  2. **A separate `*ctxMeta` pool** (streampool layer, B3) reuses the **values**:
     borrow a meta, stamp it (wave/ctxType/parent/heldRequest/parentJobs via
     `parentJobsFor`), set as the child ctx's value; return to its own pool on `Free`.
  Values pooled INDEPENDENTLY of contexts. Cancellation = pure source-ctx ancestry.
  Borrow → stamp → run → return. Three disciplines: **A** per-execution (async:
  work-item `Free`; inline: scope `defer`), **B** per-drive (skim), **C** per-lifetime
  (flusher). Collapses `waveCtx`/`execShell`/`Cancel`/`CancelAndWait` + the per-wave
  ctxMetaMaps. Plain ownership + GC, no refcount. Borrow-site map (#1–#4) in the note.
  - **IMPL STATUS (2026-06-22):**
    - **`ctxpool` LANDED** (`51554d5`) — generic child-ctx reuse + eviction; tested,
      unadopted. (Caught+fixed a draft bug: embedded zero-value omnipool silently
      defeated ctx reuse → per-`childPool` `nbcq`.)
    - **`bodyCtxPool` DROPPED** (`b7c132d`) — the fused single-pool design (`c159d25`);
      reverted the `ctx`/`pool` fields on `ctxMeta`. Stamp logic (`parentJobsFor`)
      preserved in history at `c159d25` for B3 re-derivation.
    - **`metaFromContext` read-seam LANDED** (`a8f05e8`) + **body-context borrow
      primitive LANDED** (`1a8d404`, `bodyMetaPool`/`borrowBodyContext`, unadopted).
    - **B3 SCOPE DISCOVERY + DESIGN CONVERGED → `docs/decisions/meta-context-migration.md`
      (rev 2, `c7abcdc`).** Migrating bodies to ctxpool is inseparable from migrating the
      meta-derivation machinery (ensureCtxMeta family): it finds source metas via
      ctxMetaValueKey (ctxpool bodies use childKey) and caches by ctx identity (ctxpool
      reuses ctxs). Design CONVERGED (w/ PN) to ONE unified model — **no lifecycle fork**:
      every meta is a ctxpool borrow differing only in hold scope (per-execution bodies /
      **per-call drivers** — NOT singletons; pooled N-at-once under concurrent driving)
      and ctxType. One lookup (`metaFromContext`=`GetValue`); `ctxMetaValueKey`/
      `ctxMetaMap`/`skimCtxMetaMap` all retire; cross-job derivation dissolves into the
      borrow. Rule: **borrow-at-entry, read-while-nested**. Decided: top-level
      Start/Submit drives a backpressure trySkim → **two nested borrows** (top-level ⊃
      skim) + the submitted work's separate body meta. `topLevelExEnv.Lock` removable
      (per-call metas are single-threaded — verify).
    - **CUTOVER LANDED (`e740d33`, 2026-06-23):** B3.meta + B3.A/B in one step —
      `metaFromContext` is ctxpool-aware; task and funnel bodies borrow via
      `borrowBodyContext`; `ensureCtxMeta` stamps a ctxpool child (no `AfterFunc(j.ctx)`).
      The cutover took the incremental path (dual-lookup read seam); the legacy
      `ctxMetaValueKey` read branch + `ctxMetaMap`/`skimCtxMetaMap` have since been
      RETIRED (2026-06-23, with the zero-value Wave cleanup): `metaFromContext` is now
      pure `ctxpool.GetValue`, and `internal/ctxmap` (the maps' backing) is deleted.
    - **POST-CUTOVER HANG FIXED (`00f7abc`, 2026-06-23):** flush-signal subscription race
      in the funnel flusher (see the top banner). 300/300 + 40/40 -race green.
    - **B3.C LANDED (2026-06-23):** runtime force-abort gutting was already in the cutover
      (Wave owns no `waveCtx`/`execShell` pool; `Cancel`/`CancelAndWait` no longer
      force-abort bodies). This pass removed the residue: the dead `worker.Pool.PoolCtx()`
      method (zero callers — it existed only so waves could derive a teardown `waveCtx`),
      the stale `Cancel` doc (it claimed it cancels task contexts), and `execShell`/
      `wave-5b` comment references across pool.go/job.go/ctxmeta.go/ctxpool.go.
    - **B3.D LANDED** (in the zero-value Wave migration, 2026-06-23): examples + sim
      reconciled to the new cancellation model (cancel the submit/drive ctx to stop a
      body; the Wave owns no ctx). The B3.meta unified-model retirement is also DONE
      (`metaFromContext` → pure `GetValue`; `ctxMetaValueKey` + the two maps removed).

**►►► DOC CONSISTENCY SWEEP (in progress, 2026-06-20).** Bringing all docs in line
with the converged target design. Committed so far this session: permit-core.md (new
spec); reconciled dispatch-execution-split.md, limiter-suspend-resume.md,
global-substrate-activation.md (banners), backpressure-and-reentrancy.md,
programming-model.md (reentrancy/scatter rule); trimmed dispatch.go; deleted
REVIEW_FINDINGS.md (code citations repointed to limiter-suspend-resume.md).

Root-doc triage done (no deletions needed beyond REVIEW_FINDINGS): CHANGELOG /
POSITIONING_RESEARCH / ARCHITECTURE_COMPARISON keep-as-is.

**DONE this session (also):** programming-model.md refactored into a focused
streampool guide (732→221 lines); its comparison section relocated into
ARCHITECTURE_COMPARISON.md. README swap done (old README deleted, README-proposed →
README, updated to the pinned surface). API_DESIGN.md reconciled (top banner + inline
fixes: Pool un-exposed, permit cache not global scheduler, reentrancy rule,
principle 7).

**REMAINING (process/roadmap docs — lower stakes):**
- **REFACTOR_PLAN.md** (now `docs/plan/`) — reconciled 2026-06-21: candidate-waves
  marked landed/superseded, the REVERSED Pool↔Wave direction fixed (Pool folded
  INTO Wave, internal), op-names refreshed, and the missing dispatch/execution-split
  + permit-core architecture wave added. Live status still lives in WORKING_NOTES.

**DONE since (this block was itself stale):**
- TODO.md "global permit scheduler" — already reads "no global scheduler" in the
  REFRAMED banner; op-names refreshed; the wave-5b section rewritten to the
  2026-06-21b finalized lifecycle (force-abort design retired).
- ARCHITECTURE_COMPARISON.md / API_DESIGN.md / POSITIONING_RESEARCH.md already live
  under `docs/decisions/`; REFACTOR_PLAN under `docs/plan/`. (The relocated
  comparison may still carry minor generic "scatter-gather-combine" phrasing.)

**SURFACE PINNED this session:** `streampool` package; no user-facing `Pool` (sizing
automatic; concurrency via Limiters); drop scatter/gather/PSG vocabulary; dispatch
verb `Submit`; `Flow` kept as the optional third concept. Go-forward calls made in
the guide/README/API_DESIGN — ratify or correct.

**►►► DEVELOPMENT HISTORY (excised 2026-06-23).** The dated implementation/design
journal that used to live here — the architecture pivot, the `psg.Pool`→`Wave` fold,
rdvq/workq consolidation, limiter suspend/resume, and earlier hang root-causes — now
lives in **`docs/decisions/development-history.md`**. This file keeps current status
only. (Relative "above"/"below" cross-references in the sections below that point into
that journal now resolve in the history doc.)

### Next session pickup

**Recent — allocation reduction on the comparison bench (2026-06-30).** Drove streampool
per-dispatch allocations **~37 → ~1.1 allocs/task** (the per-task-closure floor every
bounded pool pays), now flat across underload→heavy-overload: meta pooling (37→19),
gen-stamped inbox + `h.release` method-value cache (→7.3), abandon-path hint **reap**
(→2.6), `confirmFn` method-value cache (→1.1). Commits: `d1d6484` (bench fix), `ba65991`
(`h.release`), `21c74f7` (reap), + `confirmFn`. Harness = the `bench/` comparison submodule
(unbounded / chan-semaphore / naive-pool / streampool). The waiter-set design exploration
this prompted — is FIFO desirable, caller-held `Receiver`/`Waiter` params, permit-forest
affinity bucketing — is recorded durably in `docs/decisions/waiter-set-notification.md`
(net: keep the shared reclamation substrate + reap + per-use ordering; affinity bucketing
deferred, measurement-gated on a deep-forest workload that doesn't exist yet). Remaining
gap to naive-pool is the unavoidable shared per-task closure. Possible next bench work: the
**nested-dispatch** workload (where a naive pool deadlocks and the split should pay off) —
the comparison so far exercises streampool's overhead, not its differentiator.

**►► NEXT SESSION PICKUP (2026-06-30 → , order of readiness):**
1. ~~**`RenotifyFunc` → `Notification` refactor**~~ — DONE (`eee5322`, 2026-07-01). See the
   top banner + `docs/decisions/waiter-set-notification.md` (Status → Landed). The value
   struct settles as `Received()`/`Forward()` (no `Consume` — a no-op on a value receiver;
   productive use just drops the wake); `Empty()` became `Received()` (positive predicate).
2. **`select` scase-escape dig** (self-contained) — the residual ~0.06 alloc/task on the
   park path is the `select`'s scase array escaping behind `PopFrontFunc`'s callback
   indirection (`executorWorker.Wait`/`Wave.skimSelect`), NOT a closure (confirmed: caching
   the callback didn't move it). Investigate whether the scase escapes due to the indirect
   call and/or generic instantiation, and whether restructuring the callback seam avoids it.
3. **nbcq interface→value-struct audit** — nbcq can carry value structs now (historically
   pointer-only); sweep `Queue`/`Handoff` value types for interface/pointer values that
   could be value structs (leaner). Low priority.

**►►► NEXT = C2 — the pool-split cutover.** Phase 2b migration steps 0/C1/B are landed
(commits `ae6339f`, `551f4e6`, `cfdb039`); the example fix is `565b3b6`. C2 is the big one
and reshapes the live dispatch path. Full design in
`docs/plan/dispatch-execution-split-phase2b.md` ("The pool model", "The queue model",
mode mapping, governor placement, migration sequencing). Shape:
- **Executor pool** = `worker.Core[E]` (from B) + an `rdvq.Handoff[T]` (from B) + a simple
  `PopFront → Run` per-worker loop (no `workq.Worker`, no `TryPopFront`). Its demand is
  **block-as-demand**: a scheduler's `Handoff.PushBack` that finds no parked executor parks
  and triggers `Core.TrySpawn` — wire this `PushBack`-park→spawn hook on the `Handoff`
  (deferred from B; B1 left `Handoff` pure).
- **Scheduler pool** = the existing `worker.Pool` over `workq.Accepted`; per item:
  non-blocking `permits.Cache.Acquire` + governor check, then **blocking `Handoff.PushBack`**
  the admitted body to the executor; postpone on a permit miss. Top-level admission stays
  inline on the driver (skim-retry / block-and-help).
- **Gate:** full suite + `-race` + `TestBySimulation` + the **latency/alloc benchmarks**
  with the REAL methodology (heavy-tailed blocking-I/O work, P99/max, swept P:D ratios —
  see `[[feedback_bench_methodology]]` / BENCHMARKING.md), not a throughput microbench.
- **Recommended:** map it first (like C1/B did) before touching code — it's the
  architecture+latency milestone.

**Deferred backlog (after / alongside C2):**
- **C3 — drain limiting** (`WithLimits` on `NewSkimmer`, `WithFlushLimits` on `NewFunnel`);
  needs the limited-drain model-check (parked holder whose drain needs a permit).
- **C4 — residual cleanup**: any dead `BlockBehavior`/`shouldBlock` plumbing once C2
  reshapes dispatch. (No wake-efficiency item — the `Notifier` single-wake + renotify
  conservation already wakes exactly one consumer, so there's no thundering herd to fix.)
- **Multi-limiter** joint admission (currently panics at `opConfig.singleLimiter`); **weighted
  resources** (re-derive the removed `applicant` sizing natively).
- **Thread C** — `Try*` honoring non-zero non-Forever deadlines via bounded-wait
  (`Forever` sentinel + `dispatch (bool, error)` foundation at `5dc49c7`).
- **psgwf legacy-name retirement**; **bench.txt regeneration + chartgen alignment** (new
  metric names, e.g. `funnelLimit`).

## Open issues

### Deadline propagation in taskPostWork

`taskPostWork.newTaskPostWork()` receives a `deadline` parameter but doesn't store or use it. Sibling scatter work types (`taskPoolScatterWork`, `combineScatterWork`, `gatherScatterWork`) store and use theirs. Should add a `deadline time.Time` field and pass it to `BasicPushSelect` via context.

### Renotifier lifecycle (`wrappedRenotify` only now)

`rdvq.RenotifyFunc` is a bare `func()` with no `Free()`. After the orphan elimination, the only remaining workaround instance is `wrappedRenotify` in `internal/rdvq/notifier.go`, which self-frees inside its renotify callback — works only if the renotifier is invoked, leaks if it's replaced or discarded. Long-term: change `RenotifyFunc` to a `Renotifier` interface with `Renotify()` and `Free()` so the rdvq infrastructure can free unused renotifiers in all cases. Less urgent now that `orphanedTaskRenotify` is gone — only the rdvq-internal one remains.

Files affected: `internal/rdvq/notifier.go`, `internal/rdvq/waiters.go`, all `Notify()` callsites.

### ExecuteOrWait duplication

`taskPostWork.Execute` implements ~80 lines of try/subscribe/block logic that overlaps with `workq.ExecuteOrWait` and `workq.Governor.Execute`. It has unique requirements (custom `TryPushBack`, demand-registration side effects, blocking via `PushBackFunc` + `BasicPushSelect`) so it isn't a trivial extraction. Possibly worth a `TryPostBehavior` abstraction if other places grow similar shape, but not urgent.
