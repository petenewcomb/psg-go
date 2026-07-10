# Context pinning and driver access: retention and the read surface

> Decision record (2026-07-09, design session with PN). **Status: converged,
> not yet implemented; the accessor's public name is OPEN (see "Naming").**
> Builds on `driver-contexts.md` (implemented), which put the lifetime
> machinery in place; this record designs the two public surfaces that read
> and retain it. Generalizes what `otel-tracing-on-flows.md` needs — the otel
> fan-in helper becomes one composition of these primitives.

## The two questions

First: applications need to read the flows of the context that *drove* the
current body — the skim drive from inside a handler, the last accumulate from
inside a flush — and to keep walking up that chain. `driver-contexts.md` made
every driver's meta and rider chain alive and immutable for exactly the extent
that needs them, but deliberately left the read surface to this discussion.

Second: framework contexts break Go's normal context contract. An ordinary
`context.Context` is immutable, freely shareable, and retainable forever; ours
are pooled and call-scoped — retaining one past its extent is undefined,
because the pooling is what avoids a large alloc/init tax on every unit of
work. That trade is right for the hot path, but applications legitimately need
to keep a context: stash a request's flow for a background job, hand it to a
callback that outlives the handler. Today there is no defined way.

The two questions meet: a driver context read inside an extent is only valid
within that extent, so retaining *it* needs the same answer as retaining any
framework ctx.

## Pin / Unpin: buying back the Go contract

**An explicit pin is the purchase of Go's normal context contract.** The
parent-refcount work already made a `ctxMeta` immutable for its ref'd
lifetime, so a context whose meta is deliberately held *is* an ordinary
immutable, shareable, retainable Go context — the pin makes the holding
explicit and paid-for:

```go
pinned := psg.Pin(ctx)   // must be called inside the extent where ctx is valid
...                      // pinned is an ordinary Go context: share, store, retain
psg.Unpin(pinned)        // releases; the flow may now end
```

`Pin` MINTS the pinned context rather than blessing the argument in place: a
fresh pooled meta and ctxpool child, stamped from the source at a synchronous
safe point. The pinned ctx itself is the token — there is no side-band release
handle to propagate, because the ctx is the thing the caller wanted to
propagate all along. Minting is what makes the hard parts true by
construction:

- **Carrier semantics.** Pin takes instance refs (plus node refs) on the
  source's rider chain — takeable safely only inside the extent, where the
  extent's own refs provably cover them (the positional-cover rule that shaped
  `buildFireChain`). A pinned ctx therefore *carries* its flows: follow-ups
  wait for the last Unpin. This is the semantically honest reading of
  retention — keeping a context that says "I am part of flow X" means flow X
  is not over — and it is what makes later dispatch sound (the chain's
  instances cannot have been recycled). The corollary is stated like any
  resource: **a leaked pin holds its follow-ups open forever**, not just
  memory.
- **No stale extent state.** A body ctx carries two pieces of extent-scoped
  state that must not survive it: the limiter handle (`held`, released at body
  end) and the execution environment (live executor state; see the exEnv
  custody contract in `driver-contexts.md`). The minted pin simply never
  carries either — `held` nil, exEnv nil, `permitRoot` set, so a later permit
  walk stops at the pin instead of reaching a recycled handle.
- **Wave-less.** The pin carries no ambient wave and no `parentWaves` (below).
- **Stable ancestry.** The pinned ctx roots at `context.Background()`, not the
  source ctx: a framework source is itself a pooled child whose node can be
  recycled and re-stamped after the extent — a retained ctx must not read
  through it. The consequence is the same shield the fire continuation chose:
  a pinned ctx carries the flow's riders and values, never the source's
  cancellation or `context.WithValue` ancestry. (An application that wants a
  cancelable retained ctx composes one: flow values ride the pin; its own
  cancellation is its own business.)
- **Validated Unpin.** The minted meta carries a pin marker, so `Unpin` on a
  non-pin or an already-unpinned ctx fails loudly instead of corrupting a
  bystander. Pins may be counted (Pin the same pinned ctx again) with the
  existing refcount underflow panic as the double-release tripwire.

Concurrent use of one pinned ctx from many goroutines is safe: the pinned meta
is immutable, and every dispatch mints its own per-dispatch state (below).

### Dispatch from a pinned ctx: outside the framework, explicitly

A pinned ctx is a way to go *outside* the framework — it keeps the flow and
drops the extent, the wave included. Making the pin wave-less turns that
stance into enforced, consistent behavior with zero new machinery:

- `resolveWave` already panics on a wave-less source without `op.In(&wave)`:
  dispatch from a pin requires naming a wave (a fresh sub-wave or an explicit
  existing one). Nothing is implicit.
- The wave-less-meta derivation already exists — a top-level `WithFlow` scope
  meta has exactly this shape — so the pin introduces no new meta species.
  A dispatch derives a fresh top-level meta (parent = the pinned meta, riders
  inherited verbatim) and draws a pooled `topLevelExEnv` wired to the named
  wave's intake, exactly as any bare-ctx top-level submission does. The exEnv
  was never the source's job at top level; it is minted per dispatch.
- Blocking behavior is therefore ordinary top-level behavior — help-shaped,
  including the backpressure yield that skims inline — and the ordinary
  documented caveats govern: submitting to and skimming a wave from multiple
  goroutines requires thread-safe handlers. Because the user explicitly named
  the wave, stumbling *implicitly* into a concurrently-driven wave — the
  actual footgun — is impossible. (A special "just-block, never help" dispatch
  mode for pins was considered and dropped: consistency beats a mode.)
- Dropping `parentWaves` is deliberate: the child-wave cycle guard protects a
  live extent from deadlocking against its own wave ancestry, and a pin has no
  extent — an outsider dispatching anywhere is a fresh top level.

Inside a body, dispatch through the body ctx; the pin is for *later*. A body
that pins its own ctx and immediately dispatches through it gets top-level
treatment (blocking, fresh permit root) rather than in-body treatment — legal,
but almost never what it wanted.

## The driver accessor

One composable, ctx-shaped read (working name; see "Naming"):

```go
// DriverContext returns a read-only context positioned at the driver of the
// body ctx belongs to, so the existing reads compose: key.From(d),
// tag.InFlow(d) — and DriverContext(d) walks further up the driver chain.
// ok is false where there is no driver.
func DriverContext(ctx context.Context) (d context.Context, ok bool)
```

Ctx-shaped rather than per-identity (`k.FromDriver`, `t.InDriverFlow`): one
function instead of two per identity type, and composition gives the chain
walk — the property that makes this more general than the otel use case. What
it returns follows the driver table in `driver-contexts.md`:

| body            | returns                                                       |
|-----------------|---------------------------------------------------------------|
| task/accumulate | the dispatching body's position (the meta parent link)        |
| skim handler    | the drive: the per-item child meta's parent, already alive    |
| funnel flush    | the last accumulate, via the instance's rolling pin — plus the one piece of plumbing this record adds: a driver link stamped onto the flush body meta from the pin |
| follow-up fire  | `ok = false`: the fire *is* the last carrier's continuation — its own ctx already reads the driver's flows; there is no second driver behind it |
| executor pumps, top level | `ok = false` — honest absence                       |

Validity: the returned ctx is readable within the current synchronous extent
(its liveness comes from the machinery `driver-contexts.md` built, which
guarantees exactly that extent). To keep it, `Pin` it while still inside —
which is the whole retention story in one line, and why the accessor needs no
lifetime rules of its own.

## Naming (OPEN)

The accessor's relationship is *causal attribution of execution across
extents*: what made this body run. Candidates examined and why they fell:

- **parent** — collides with Go's derivation intuition (and cancellation
  expectations) and with the internal `meta.parent`, which for a flush is a
  *different* link (the scheduler-side borrow source) than what this returns.
- **upstream** — reads as "the immediately preceding work in the flow," which
  this is not: a skim handler already runs under the preceding work's context
  (the item continuation); this accessor returns the drive.
- **trigger** — rejected on feel, despite the internal precedent ("the
  triggering accumulate").
- **enclosing / outer / surrounding** — strictly correct only where the
  driver's extent dynamically contains the body's (the skim handler; inline
  flush/fire). Every async case falsifies the containment claim: the
  dispatcher has returned before the task body runs; the sweep flush outlives
  the last accumulate's frame. Even at the flow level, F8's "enclosing flow"
  is the severed boundary-above view, while this accessor returns the driving
  item's full chain — the word under-describes the result.
- **driver** — accurate everywhere, and the term of art in the decision
  records; PN finds it confusing, though the colliding senses
  (`SuspendDriver`, "the sole serial skim driver") are internal vocabulary a
  public API user never meets. Kept as the working name.

A scope-flavored word cannot be right because the relationship is causal, not
scoped; the search continues among causal words (driver, origin, cause) and
the codebase's watery register, which so far has produced nothing that both
fits and stays clear (headwaters ⇒ ultimate origin; wake ⇒ notification
vocabulary; current/channel/stream ⇒ collisions).

## Rejected alternatives

- **`Pin` returning a release func**: a second, side-band thing to thread
  around when the ctx is what the caller wants to propagate.
- **Same-ctx `Pin(ctx)`/`Unpin(ctx)` (no mint)**: after the last unpin the
  ctxpool child recycles, so a stale `Unpin` silently decrements an innocent
  meta; and the un-minted ctx retains the extent's `held`/exEnv, which a later
  dispatch would misuse — "this extent has ended" is unrepresentable, so the
  dispatch paths cannot defend themselves.
- **Observer-only pins** (node + meta refs, no instance refs): reads work, but
  later dispatch is unsound (the chain's instances may be recycled — the
  positional-cover problem), and dispatch-from-a-kept-ctx is a primary reason
  to keep one.
- **Per-identity driver reads** (`FromDriver`/`InDriverFlow`): doubles the
  read surface per identity type and loses chain composition.
- **A non-helping ("just-block") dispatch mode for pinned sources**:
  superseded by wave-less pins — explicit wave naming makes pinned dispatch an
  ordinary top-level submission under the ordinary caveats.
- **An opaque `Flow` handle type instead of ctx-shaped reads**: safer against
  misuse but doubles the read surface and forfeits composition with `From`/
  `InFlow`/`WithFlow` as they exist.

## Implementation notes

- `Pin` = structurally a top-level `WithFlow` scope meta (wave nil, riders
  carried) plus `permitRoot`, carrier refs (`flowRefRiders` + node ref), a pin
  marker, and a `context.Background()` root; pins are cold, so the pooled
  meta + child alloc is off the hot path.
- The flush driver link is the one new field: the flush body meta gets a
  pointer to the instance's pinned driver meta (set in `flush` while the
  rolling pin is still held; cleared with it).
- Gate expectations when implemented: the usual per-CP gate plus conservation
  extensions (a pinned-and-leaked ctx must show up in `TestCtxMetaConservation`
  as a deliberate positive; Unpin restores zero), a pinned-dispatch end-to-end
  test (values delivered, follow-up waits for Unpin), and a
  multi-goroutine pinned-dispatch -race test.
