# omnipool.RefCount: generation-guarded reference counting for pooled objects

Status: implemented (core + model/straddle/concurrency tests), `internal/omnipool`.

## Problem

A pooled object that is reused carries a hazard the moment anything *referenceless*
can still reach it after it has been returned to the pool: a parked goroutine, a
listener in a foreign set, a cache node in another subtree, a stale
context-derived entry. Such a holder cannot safely dereference the object,
because between capture and use the object may have been recycled and handed to
an unrelated owner. The dispatch code already lives with this (waveImpl, permit
cache nodes, the weighted-acquisition demand identity, funnel instances), and has
been solving it ad hoc with bespoke generation stamps and revalidating CAS loops.

`omnipool.RefCount` codifies the pattern once: a pooled object is reused only when
its **last** reference is released, and a referenceless holder can attempt to
reacquire and discover — race-free — whether the object it remembers is still the
same incarnation.

## Design

An embeddable `RefCount` carries an atomic **(gen, refs)** pair:

- `refs` — the object-lifetime reference count. `Pool.Get` hands out the first
  reference; `AddRef` clones a reference the caller already holds; `Handle.Get`
  upgrades a weak handle; `Pool.Release` drops one. The object returns to the
  pool on the last release.
- `gen` — the incarnation identity, bumped on every recycle. A `Handle` captures
  `gen`; `Handle.Get` succeeds only if the object is still at that generation.

References are split into **strong** and **weak**:

- **Strong** references are the naked `*T`. Holding one keeps the object alive;
  the hot path pays nothing (no checks). `Get`, `AddRef` (from an existing
  strong ref, infallible), and a successful `Handle.Get` each yield one, balanced
  by `Release`.
- **Weak** references are copyable `Handle[P]` values (`{*T, gen}`), for
  referenceless holders. `Handle.Get` is the fallible weak→strong upgrade.

```go
type RefCount struct { /* atomic128 (refs, gen) */ }
type RefCounted interface { Resetter; refCount() *RefCount }   // sealed; embed RefCount

func NewHandle[P RefCounted](obj P) Handle[P]        // weak capture, +0
func (h Handle[P]) Get() (obj P, ok bool)            // fallible upgrade, +1 on ok
func AddRef[P RefCounted](obj P)                      // infallible clone, +1 (a ref must exist)

func (p *Pool[T]) Get() *T                            // first strong ref, +1
func (p *Pool[T]) Release(obj *T)                     // -1, recycle on last (Put is a deprecated alias)
```

The constraint sits on the **pointer type** `P` because `RefCount`'s accessor is a
pointer-receiver method (it holds an atomic that must not be copied), so only
`*T` — never `T` — satisfies `RefCounted`. `Resetter` is mandatory: a managed
object is recycled in place, so its payload is cleared field-wise on release and
never wholesale (which would copy the embedded atomic and destroy the generation).

## Why no distinguished retirement

Earlier drafts (see the tier-R design in `nbcq-pinning-and-reclamation.md`) used
two *separate* atomics for `gen` and `refs`, which forced a distinguished
retirement step: an explicit, once-per-incarnation act that bumped the generation
**early** — while `refs` was still ≥ 1 — plus an "armed" flag. That existed only
to close a resurrection race the separate atomics could not: a releaser
decrements `refs` 1→0 and is about to bump `gen`; concurrently an upgrade
increments `refs` 0→1, reads the not-yet-bumped `gen`, matches, and walks away
holding a reference to an object being recycled.

Packing `gen` and `refs` into **one 128-bit word** dissolves it. Recycle is a
single CAS `(1, G) → (0, G+1)` — bump and zero, atomically. A concurrent upgrade
either lands its `+1` before the recycle (which then fails, retries, sees
`refs ≥ 1`, and does not recycle) or after (it reloads, sees `G+1`, and fails
cleanly). Consequently:

- **No arm flag, no distinguished retirement.** The owner's own held reference
  keeps `refs ≥ 1` until it is done, so the generation can never bump early.
- **No upgrade blips.** `Handle.Get` reads the generation and, on a mismatch,
  returns without ever touching `refs` (check-then-inc via CAS). The entire
  "counters are add-only / phantom 0→1→0 blips are legal" apparatus evaporates.
- **Double-recycle is impossible by construction** — the recycle CAS expects
  exactly `(1, G)`; a second stale releaser fails.

"Is this engagement over?" (Done) and "is this the same incarnation?" (recycle)
are orthogonal questions. The generation answers only the second and bumps only
at recycle; Done stays with the consumer's own engagement counters. The old
early-bump conflated them — an artifact the third-counter refinement was already
peeling apart.

## Representation: atomic128, not a bit-split uint64

The word is a full `atomic128.Uint128` — 64-bit `gen` and 64-bit `refs`, no
bit-splitting. A `uint64` split (e.g. 48-bit gen / 16-bit refs) was considered
and rejected for the *general* primitive: while the wave's own reference count is
small (engagement + a few concurrent upgraders + one root cache node per limiter
— the permit cache tree absorbs fan-out a level down), `RefCount` is general, and
a planned consumer — hot permit cache nodes pinned by every concurrent LRU/steal
walk — has a count that is unbounded at this project's target scale. A128 removes
the ceiling for every consumer at a bounded, cold-path cost (the CAS is
per-upgrade / per-release, never per-op). The API is representation-independent,
so a `uint64` variant remains a self-contained future change for a consumer
proven small.

## Race-detector policy

Native 128-bit CAS is inline asm the race detector cannot see, so memory ordered
*through* the word (e.g. a payload a consumer reads under a freshly-upgraded
reference) draws TSan false positives even though the CPU's `CMPXCHG16B` does
provide the ordering. The instrumented mutex fallback (a per-object `sync.Mutex`
guarding the value) is TSan-visible and race-clean. Therefore the fallback must be
forced under `-race`. This policy lives centrally in the `atomic128-go` fork (a
`//go:build race` init calling `DisableNative()`), so every consumer inherits it;
`PSGNATIVEA128=0` also forces it for explicit fallback testing off the race path.
The mutex fallback is also allocation-free and has a valid zero value (unlike the
`atomic.Value` fallback it replaced), so a freshly created managed object needs no
initializing store before its first operation.

## Validation

- **Model** (`refcount_model_test.go`): a `rapid` state machine over
  Get/AddRef/Release/NewHandle/Handle.Get against a reference model, asserting the
  exact `(refs, gen)` word after every operation and that `Handle.Get` succeeds
  exactly when the object is still live at the captured generation.
- **Straddle** (`refcount_straddle_test.go`): the three deterministic
  recycle-vs-upgrade orderings, including the mid-flight "recycle between the
  upgrade's load and its CAS" case — the stale CAS fails and the retrying
  `Handle.Get` observes the bumped generation. No resurrection.
- **Concurrency** (`refcount_concurrent_test.go`): a full-op hammer and a
  resurrection race (producers recycle while consumers race to upgrade published
  handles, touching payload only under a held reference), green under the
  instrumented `-race` path.

## Adoption

Adopt where a pooled object has referenceless holders that can act after recycle
**and** staleness would misbehave (misdeliver, misidentify, corrupt) rather than
merely waste a step, **and** no fused state word already carries the generation
more cheaply. Planned: `waveImpl`, the weighted-acquisition demand identity,
permit cache nodes. Not candidates: inbox/outbox (generations fused into wider
state words), nbcq chunks/elements (count-guarded at the anchors), ctx
metas/exEnvs (staleness structurally unreachable), pooled timers (stale fires
self-correct). `RefCount` is for staleness that would misbehave; where a recheck
is idempotent, recheck.
