# Surface lineage — superseded design facets and why they were dropped

This is the **historical record** of how the user-facing surface evolved to its
current locked form. It exists so the rest of the docs can state the *current* design
without re-litigating rejected alternatives, and so a reader who remembers an older
shape can find out what replaced it and why.

**Current locked design lives elsewhere** — this doc only records what was *superseded*.
For the current target see: the WORKING_NOTES top banners (live status), the per-symbol
API docs / `doc.go` / README (current surface), and the topic decision docs
(`body-context-pool.md`, `meta-context-migration.md`, `backpressure-and-reentrancy.md`,
`permit-core.md`).

> Convention: each facet is **was → now → why**. "now" is the current locked truth as
> of the 2026-06-21b Wave-lifecycle finalization; if a later decision moves it, update
> the "now" line here and in the live docs.

---

## 1. Naming: `psg` / scatter-gather → `streampool`

- **was:** package `psg`, module `psg-go`; the model described as "scatter-gather".
- **now:** package + module `streampool` (rename landed `b2212f9`). Surface verbs kept
  (`Submit`, `Skim`), ops named as agent/role nouns (Launcher/Skimmer/Funnel).
- **why:** "scatter-gather" undersold the streaming/pipeline nature and collided with
  the unrelated MPI collective; `streampool` names what it is (a pooled stream of work).

## 2. The job/pool type: conflated `Job`≈`Pool` → split → folded into `Wave`

- **was (psg-go):** `psg.New(ctx)` / `NewJob(ctx)` returned a `*Job` that conflated the
  *batch of work you await* with the *worker pool that runs it*. `NewPool` /
  `NewTaskPool` / `NewCombinerPool` exposed worker containers directly.
- **interim (early API_DESIGN):** split into two user-facing types — `Wave` (the batch)
  and `Pool` (the fungible worker container) — with `NewWave` on a default `Pool` plus
  an optional `NewPool` for tuning.
- **now:** **one user-facing type, `Wave`**; the worker `Pool` is **internal**, a single
  process-wide auto-sized default pool (`internal/worker`, `defaultPool`). No `NewPool` /
  `WithPool` / `PoolOption` on the surface. Per-op concurrency is expressed with
  **Limiters**, not pools. (The Pool→Wave fold went through a `type Wave = Pool` alias
  bridge, then a rename flip; `meta.job`→`meta.wave` terminology followed.)
- **why:** users care about "this batch of work and when it's done", not about worker
  containers. Limiters compose where pools don't (an op can have several; ops can share
  one), so concurrency control belongs there. One type removes the Job/Pool conflation
  that caused the original confusion.

## 3. Wave construction: `NewWave` (all forms) → **no constructor** (zero-value Wave)

Three successive shapes, all now superseded:

- **3a — two-return:** `NewWave(ctx) → (waveCtx, wave)`. The returned ctx carried the
  wave so top-level `op.Submit(ctx, …)` could route by ctx.
  - dropped because: it conflated **routing** (which wave the work joins) with
    **propagation** (ctx-borne values), forced ctx-juggling at every call site, and made
    the wave a ctx value rather than a plain handle.
- **3b — single-return value handle:** `NewWave(ctx) → Wave` (no ctx out); routing via
  ambient + `op.In(wave)`.
  - dropped because: a constructor that takes a ctx still implies the wave has a
    *creation/cancellation* ctx, but cancellation is **driver-specific** (see §4) — the
    ctx belongs to whoever drives the drain, not to the wave.
- **3c — value-handle semantics** (value receivers, `Wave` passed by value).
  - dropped because: a Wave is mutable per-batch state with an internal lifecycle; a
    pointer (`*Wave`) is the honest representation and avoids accidental copies.
- **now (2026-06-21b, locked):** **no constructor at all.** `var w streampool.Wave` — a
  zero-value `Wave` is immediately usable. It self-inits its substrate on first
  ctx-bearing use (op dispatch into it, or `Skim`/`SkimAll`) via a race-safe
  `ensureInit(ctx)`, keyed on the first-use ctx so it captures the **driving** ancestry.
  It is reusable after a drain returns (no explicit teardown). Bind with `op.In(&w)`.
- **why:** the Wave owns no ctx (like the internal Pool), so there is nothing for a
  constructor to capture; a sub-wave is then *just* a zero-value `Wave` first-used inside
  a body (no separate child constructor — see §5); and zero-value-usable + reusable is
  the most Go-idiomatic, allocation-friendly shape. 3b and 3c are moot under "no ctx".

## 4. Wave lifecycle: `Cancel`/`CancelAndWait`/`Wait`/force-abort → **drain-only**

- **was:** `Wave.Cancel()` / `CancelAndWait()` (cancel a wave-owned ctx, force-abort
  running bodies, join goroutines) and a wave-level `Wait()`.
- **now:** the only lifecycle operations are the **drain** — `Skim` / `SkimAll` /
  `CloseAndSkimAll` — which return **`ErrWaveDone`** when in-flight==0 ∧ sealed.
  `SkimAll(ctx)` *is* the structured scope. There is **no framework force-abort**:
  cancelling the drive ctx makes the drain return `ctx.Err()`, while in-flight work keeps
  running under its own submit ctxs (cancel those — usually the same ctx — to stop it).
  No goroutine leak: workers belong to the global pool, and `streampool.Wait()` stops and
  joins idle workers.
- **why:** "Wave owns no ctx" (§3) leaves nothing for `Cancel` to cancel; cancellation is
  the driver's ctx. Drain-only collapses the lifecycle to one well-defined completion
  condition and removes the force-abort machinery (and its teardown-ordering hazards).

## 5. Sub-waves: `NewChild` → zero-value Wave first-used in a body

- **was:** `parent.NewChild(ctx) → Wave`, where the parent's drain waited for child
  drains (creation-time parentage).
- **now:** a sub-wave is a zero-value `Wave` first used inside a body; its parentage —
  for limiter permit inheritance and the "can't skim a wave you're part of" guard — is
  the **driving goroutine's ctxMeta nesting captured at drive**, never a
  creation/cancellation parent.
- **why:** with no constructor there is no creation moment to attach a child to; driving
  ancestry is the only parentage that is actually meaningful for permits and the
  reentrancy guard.

## 6. Wave context & cancellation plumbing: `waveCtx` + per-wave `execShell` pool → none

- **was:** each Wave owned a `waveCtx` (= `WithCancel(parent)`) and an `execShell` pool —
  per-wave reusable `{ctx, cancel, meta}` shells that rooted body contexts at `waveCtx`
  so `Cancel` could force-abort them.
- **now:** the Wave owns no ctx and no shell pool. Body contexts are **borrowed per
  dispatch** from `internal/ctxpool`, keyed on the submit ctx, so cancellation rides
  submit-ctx ancestry. (The residual `worker.Pool.PoolCtx()` that let waves derive a
  teardown `waveCtx` was removed in B3.C.)
- **why:** consequence of §3/§4 — no wave-owned ctx means no force-abort, so the shells'
  only purpose (a wave-rooted cancelable body ctx) disappears.

## 7. Context-meta pooling: single fused `{ctxMeta, childCtx}` unit → two decoupled pools

- **was:** one `bodyCtxPool` handing out a fused unit that bundled a reusable child
  `context.Context` with its `ctxMeta` value.
- **now:** **two decoupled pools** — `internal/ctxpool` reuses the child `context.Context`
  objects (parent-ctx-keyed, `AfterFunc` eviction), and a separate `bodyMetaPool`
  (`omnipool`) reuses `*ctxMeta` *values*, stamped per borrow. See `body-context-pool.md`.
- **why:** the number of distinct user contexts and the number of live metas scale
  independently; decoupling lets each be reused on its own cadence and keeps ctx reuse
  from being defeated by meta churn (a fused unit zeroed the child on every meta reset).

## 8. Routing: ctx-carried wave → ambient + `op.In(&w)`

- **was:** the wave was found via the ctx returned from `NewWave` (routing-by-ctx).
- **now:** ops are **wave-agnostic** (`NewLauncher(h)` / `NewSkimmer(h)` /
  `NewFunnel(factory)`). In-body `op.Submit(ctx, v)` routes to the body's **ambient**
  (framework-stamped) wave; `op.In(&w)` returns a cheap wave-bound handle for top-level
  dispatch, redirect, or bind-once reuse. Routing is handle-level, so it never perturbs
  the ctx — flow-rider/trace propagation crosses redirects untouched.
- **why:** separates routing from propagation (the same split that killed 3a); lets one
  op spec be reused across many waves; keeps the ctx clean for genuine propagation.

## 9. Flow: refcounted ctx-borne value handle → the flow facility (`WithFlow` + keys/tags)

- **was:** `Flow`, the one ctx-borne, refcounted user type: `NewFlow(parent)` returned a
  carrying ctx plus a handle with `Dup`/`Close`, configured via `WithAfterFunc` — a value
  handle users *created* to span waves (API_DESIGN.md's Flow section, kept there as the
  historical record).
- **now:** **no Flow type.** A flow is the causal DAG of work the framework already
  maintains — flows always exist and are never created. Riders on it are shaped by the
  lexical scope `streampool.WithFlow(ctx, body, opts...)` with user-minted
  `NewFlowKey[V]` (path-scoped values) / `NewFlowTag()` (DAG-scoped lifetimes)
  identities; the scope's own reference replaces the user-held refcount. Design converged
  2026-07-03, not yet implemented: `docs/decisions/flow-design.md`.
- **why:** the handle put the `Dup`/`Close` refcount discipline in user hands where the
  lexical root closure makes the attach window race-free structurally, and it modeled
  flows as created objects when the causal structure is already there for every dispatch.
