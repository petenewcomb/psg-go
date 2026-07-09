# ctxMeta parent refcount: pin the borrowed-from context, not sever it

> Decision record (2026-07-08, design session with PN). **Status: spec — converged,
> not yet implemented.** Fixes the pre-existing `borrowSrcCtx` use-after-free (the
> race handed to the funnel/permits thread) at its root, and makes the ctx/meta
> parent chain walkable across async boundaries — the prerequisite for driver-link
> tracing (`otel-tracing-on-flows.md`). Foundational core change; land as its own
> `-race`-gated checkpoint.

## The bug

A body context is `ctxpool.WithValue(srcCtx, m)` — it descends from the context it
was borrowed from. But `ctxMeta` and its ctxpool child are **pooled**, and they are
recycled on the *driver's* `releaseBodyContext` / `releaseTopLevelContext` —
regardless of whether an async body still references `srcCtx`. So an async body that
outlives its driver holds a `srcCtx` whose meta has been zeroed and re-stamped by an
unrelated borrow. That is the `borrowSrcCtx` race: `borrowBodyContext →
metaFromContext(srcCtx)` reads a ctxpool child's value while a worker frees it (seen
~1/400 in the flow suite; documented as pre-existing, not flow-caused).

Holding `bodyCtx` keeps `srcCtx` GC-reachable, but GC-reachability is not
pool-lifetime: the recycle is explicit. The reference has to be a **refcount**.

## The fix

Give `ctxMeta` a reference count. A meta is freed (its ctxpool child released, the
meta recycled) only when the count reaches zero.

- **`refs atomic.Int32`** on `ctxMeta`, plus a `Reset()` (required: the atomic's
  `noCopy` would trip vet under omnipool's zero-by-copy).
- **A child meta takes a ref on its parent** at creation, in *both* paths:
  `ensureCtxMeta` (sync derivation — already sets `parent`) and `borrowBodyContext`
  (async borrow — **stops severing** `parent`, sets it to `srcMeta`).
- **`unrefMeta` cascades**: at zero it returns an owned `topLevelExEnv`, frees
  `selfCtx`, recycles the meta, and drops its ref on `parent` — walking up.

```go
func refMeta(m *ctxMeta) { if m != nil { m.refs.Add(1) } }

func unrefMeta(m *ctxMeta) {
    for m != nil {
        if m.refs.Add(-1) != 0 { return }
        parent := m.parent
        if m.ownsExEnv {
            if ee, ok := m.executionEnvironment.(*topLevelExEnv); ok { topLevelExEnvPool.Put(ee) }
        }
        if m.selfCtx != nil { ctxpool.Free(m.selfCtx) }
        bodyMetaPool.Put(m) // Reset zeroes, incl refs
        m = parent          // cascade
    }
}
```

The cascade **subsumes `releaseParent`**: a reused ambient meta keeps its own self
ref, so a child's unref returns it to baseline rather than zero — the count stops at
the ownership boundary naturally. `releaseTopLevelContext` collapses to
`unrefMeta(m)`; `releaseBodyContext` drops the body's rider ref (may fire — unchanged,
must precede teardown) then `unrefMeta(m)`. Body metas now store `selfCtx` too, so
both kinds free uniformly.

Because `parent` is now an honest, refcounted, unbroken parent — not the narrow
severed permit chain — **it keeps the name `parent`.**

## Keep the ref at a synchronous safe point

The ref only closes the race if it is taken before the driver can free the meta.

- **Dispatch borrows (task, accumulate)** borrow synchronously at dispatch, on the
  dispatcher's goroutine, while `srcMeta` is provably alive → `refMeta(srcMeta)` in
  `borrowBodyContext` is safe.
- **Flush / fire** borrow at `Run`, off the stashed `borrowSrcCtx` — the racing read
  today. The protective ref must be taken where the scheduler **stashes** it,
  synchronously in `Execute` (where the driver still holds the ctx), not lazily at
  `Run`. So `borrowBodyContext` takes `srcMeta` **explicitly** (rather than
  re-reading it racily): `Execute` resolves and pins `srcMeta`; `Run` borrows from
  the pinned meta and releases it at body completion.

So the stash sites (`funnel.go` flush, `flowinst.go` fire) change too, not just
`borrowBodyContext`.

## The sever was doing double duty: add `permitRoot`

Nil-parent-at-async-borrow was not only a lifetime device — it was the **isolation
boundary** for the two synchronous-only walks:

- `currentHeldPermit` walks `parent` for the held limiter permit;
- `vetNotNestedInSkim` walks `parent` for an enclosing skim handler.

Both relied on the async boundary being nil. With `parent` now pointing at `srcMeta`,
an *unlimited* async body's `currentHeldPermit` would walk into its dispatcher's
permit (a worker inheriting a permit across the goroutine boundary — the exact thing
the sever prevented), and `vetNotNestedInSkim` would see the driver's stack. The
refcount takes over the *lifetime* role of the sever; a **`permitRoot bool`** flag,
set at `borrowBodyContext`, takes over its *isolation* role — the two walks stop
there.

```go
func (cm *ctxMeta) currentHeldPermit() *heldPermit {
    for m := cm; m != nil; m = m.parent {
        if m.held != nil { return m.held }
        if m.permitRoot { return nil } // async boundary
    }
    return nil
}
func (cm *ctxMeta) vetNotNestedInSkim() {
    for m := cm.parent; m != nil; m = m.parent {
        if m.ctxType == skimContext { panic(/* … */) }
        if m.permitRoot { return }
    }
}
```

Skim bodies are **not** `permitRoot` — they run synchronously under the drive meta
(not via `borrowBodyContext`), so the nesting check still sees them. Only
`borrowBodyContext` bodies set the flag.

## `parent` points at "the context borrowed from"

That is the **dispatch** ctx for task/accumulate and the **drive** ctx for
flush/fire/skim. So this change uniformly pins the chain (the bug fix) and, as a
by-product, makes the driver walkable on the continuation bodies. Task/accumulate
driver links — if ever wanted — remain the separate "capture a drive ctx at
execution" question; not in scope here.

## Validation

A meta conservation hook (mirroring `flowNodeAllocHook`): +1 on borrow/derive, -1 on
recycle, asserting every meta drawn from `bodyMetaPool` returns once a wave drains —
a leak leaves the balance positive, a double-free trips an underflow. Gate: vet,
lint, `-short`, and a large `TestBySimulation -race` batch (this is the machinery the
sim exercises hardest, and the race it should now stop reproducing). No commit on a
false green.

## Scope boundary

This checkpoint is the refcount + `permitRoot` only — it fixes the race and makes the
parent chain walkable, with no observable behavior change. **Follow-ups**, layered on
top:

- **driver-link rider pin** — reading a driver's *flow values* (`parent.riders`)
  needs an additional pin on the driver's rider head, because the child only refs the
  riders it inherited and skim *overrides* them with the item's chain;
- **`streamotel` consumer** — the tracing patterns + fan-in helper that read the
  parent/driver/wave via these accessors (`otel-tracing-on-flows.md`).

## Coordination

This is the principled fix for the `borrowSrcCtx` race handed to the funnel/permits
thread (combiner branch `WORKING_NOTES`). Landing it here means the flow-observability
work is what finally forces it — coordinate so it lands once, not twice.
