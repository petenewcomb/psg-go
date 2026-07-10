# Context pinning and origin access: retention and the read surface

> Decision record (2026-07-09/10, design sessions with PN). **Status:
> `PinFlow`/`UnpinFlow` implemented (2026-07-10; see "As implemented");
> `OriginFlow` designed, not yet implemented. Names settled (see "Naming").**
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

## PinFlow / UnpinFlow: buying back the Go contract

**An explicit pin is the purchase of Go's normal context contract.** The
parent-refcount work already made a `ctxMeta` immutable for its ref'd
lifetime, so a context whose meta is deliberately held *is* an ordinary
immutable, shareable, retainable Go context — the pin makes the holding
explicit and paid-for. The names are flow-anchored because the flow is what
is actually pinned: carrier semantics holds the FLOW open, while the
context's extent (wave, permit, exEnv) is deliberately dropped:

```go
pinned := psg.PinFlow(ctx)    // inside the extent where ctx is valid
...                           // pinned is an ordinary Go context: share, store, retain
err := psg.UnpinFlow(pinned)  // releases; a flow ending here fires inline, errors join
```

`PinFlow` MINTS the pinned context rather than blessing the argument in
place: a fresh pooled meta and ctxpool child, stamped from the source at a
synchronous safe point. The pinned ctx itself is the token — there is no
side-band release handle to propagate, because the ctx is the thing the
caller wanted to propagate all along. Minting is what makes the hard parts
true by construction:

- **Carrier semantics.** PinFlow takes instance refs (plus node refs) on the
  source's rider chain — takeable safely only inside the extent, where the
  extent's own refs provably cover them (the positional-cover rule that shaped
  `buildFireChain`). A pinned ctx therefore *carries* its flows: follow-ups
  wait for the last UnpinFlow. This is the semantically honest reading of
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
- **Exact-token UnpinFlow.** The minted meta records a pin marker and its own
  `selfCtx`, so `UnpinFlow` requires the exact ctx `PinFlow` returned
  (`ctx == meta.selfCtx`) — unpinning a derivative, a non-pin, or the same
  token twice fails loudly at the call site instead of corrupting a
  bystander.

Concurrent use of one pinned ctx from many goroutines is safe: the pinned meta
is immutable, and every dispatch mints its own per-dispatch state (below).

### Pins compose; unpin ends the extent

**Pinning a pinned ctx mints a new, independent pin** — no counting on a
shared token. Each `PinFlow` call returns its own token with its own single
`UnpinFlow`; two subsystems handed the same pinned ctx each take their own
pin and never coordinate. (Counting on one token was considered and dropped:
it reintroduces aliasing within the pin family — one party's double-unpin
silently steals the other's.) Because a pinned source is stably valid by
definition, pinning FROM a pin has no extent-window precondition: pins
compose freely, anywhere, anytime. The handoff idiom follows: overlap, then
release — `p2 := PinFlow(p1); UnpinFlow(p1)`.

**Derivation is ordinary Go.** `metaFromContext` resolves through plain
wrappers, so `context.WithValue(pinned, …)` and `context.WithCancel(pinned)`
still find the pin — dispatch, `OriginFlow`, and `PinFlow` all work through
them — and `WithCancel(pinned)` is precisely the promised composition for a
cancelable retained ctx: the pin contributes the flow; cancellation is the
application's own layer, riding ordinary Go ancestry into dispatched bodies.
A framework derivation (`WithFlow(pinned, …)`) is a normal call-scoped scope,
not itself pinned; pin inside it to keep it. `PinFlow` of a bare, meta-less
ctx is the allowed degenerate case — a pin of the empty flow, consistent with
every bare Submit extending the ambient (possibly empty) flow.

**After `UnpinFlow`, the ctx — and every derivative of it — is invalid**:
the pin window IS the extent, and past its extent a framework ctx is invalid,
the same single rule as everywhere. Mechanically the refs drop, the meta's
count drains, and the ctxpool child recycles for re-stamping — a retained
unpinned handle can misdeliver a foreign flow's values through the reused
node. Three consequences stated plainly:

- **UnpinFlow is not cancellation.** Work dispatched from the pin before the
  unpin took its own refs at borrow and completes normally, holding the flow
  open until it is done. Those child refs can keep the pin meta alive PAST
  the unpin — so an unpinned handle may coincidentally keep working until
  the last child completes, then rot. Liveness after unpin is coincidental,
  never contractual.
- **Re-pinning cannot resurrect.** `PinFlow` after the unpin is invalid for
  the same positional-cover reason as everything else: the flow may have
  ended and its instances recycled. Overlap instead (the handoff idiom
  above).
- **Detection is best-effort, honestly bounded.** `UnpinFlow` flips the pin
  marker to expired (a monotonic write on a meta we minted), so the cold
  paths — dispatch, `PinFlow`, `UnpinFlow`, `OriginFlow` — panic on an
  expired pin caught before pool reuse. The hot value reads (`From`,
  `InFlow`) are not taxed with the check and stay documented-undefined;
  after the ctxpool node is reused, no detection is possible — the accepted
  residual class.

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

## The flow-origin accessor

One composable, ctx-shaped read. `OriginFlow` is the public name; "driver"
remains the internal term of art (`driver-contexts.md`) for the same
relationship:

```go
// OriginFlow returns a read-only context positioned at the originating flow
// of the body ctx belongs to — the context of whatever made this body run —
// so the existing reads compose: key.From(origin), tag.InFlow(origin), and
// OriginFlow(origin) walks further up the chain. ok is false where there is
// no origin.
func OriginFlow(ctx context.Context) (origin context.Context, ok bool)
```

Ctx-shaped rather than per-identity (`k.FromOrigin`, `t.InOriginFlow`): one
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
guarantees exactly that extent). To keep it, `PinFlow` it while still inside —
which is the whole retention story in one line, and why the accessor needs no
lifetime rules of its own.

Conceptually, the relationship is a hop across the nearest **branch point** of
the causal river network — see `flow-design.md`, "The river network" (PN,
2026-07-10), which grounds the per-path table, the single-hop composition
grain, and the fire's honest absence in one picture.

## Naming (settled: `OriginFlow`, `PinFlow`/`UnpinFlow`)

The accessor's relationship is *causal attribution of execution across
extents*: what made this body run. The test every candidate had to pass: "the
X of a flush is the last accumulate; the X of a handler is the drive" — both
sentences true without qualification. Candidates examined and why they fell:

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
- **driver / driving** — rejected on a principle, not a feel (PN, closing
  the search): drive vocabulary names ACTIVE execution-pumping, and this
  relationship is PASSIVE. The origin merely occasioned the body — returned
  a deadline, submitted a value, completed — and the framework's machinery
  ran it; nothing in the origin pumps its execution. The codebase's
  drive/driver vocabulary is all on the active side — the skim drive loop,
  `SuspendDriver`/`ResumeDriver`, drive-target attribution — and even for
  top-level submit and skimming it is precisely their HIDDEN block-and-help
  roles that vocabulary captures, not their primary source/sink roles. The
  internal collisions were therefore never user-invisible noise: the word
  correctly belongs to a different relationship, which is also why
  `driver-contexts.md`'s use of it for the passive relation kept reading
  badly. It stays internal vocabulary there; not the public name.
- **source** — fails the way "upstream" did, more quietly: it reads as *where
  the data came from*, and a handler's data source is the item's producer —
  whose context the handler already runs under (the CP-F7 continuation) —
  while this accessor returns the drive. Worse, source/sink is live domain
  vocabulary in this framework (a Skimmer is documented as "a terminal
  sink"), so "the source flow" points at the pipeline's production end —
  near-opposite of the referent. Internally, `borrowSrcCtx`/`srcMeta`
  already mean the borrow source, which for a flush or fire is the scheduler
  stash — precisely what this accessor looks past — so the implementation
  would have `src` and `Source` meaning different things.

**`origin` wins**: the passive-causal word — the origin of an execution is
what occasioned it, with no claim of having run it and no data-lineage
reading. Its one risk, an
ultimate-vs-immediate reading, is softened twice over: in graph vocabulary an
edge's origin is its immediate predecessor, and composition
(`OriginFlow(OriginFlow(ctx))` walking toward the root) makes
single-hop-ness self-evident. It is collision-free at the public surface
(internally only rdvq's `ProbeOrigin`), passes the qualification test on
every path, and — since a scope-flavored word cannot be right for a causal
relationship — it is also the one causal word in the codebase's watery
register: the origin of a river is where the flow begins. (The rest of that
register produced nothing that both fits and stays clear: headwaters ⇒
ultimate origin; wake ⇒ notification vocabulary; current/channel/stream ⇒
collisions.)

**Flow-anchored surfaces (PN):** `OriginFlow` rather than `OriginContext`,
`PinFlow`/`UnpinFlow` rather than `Pin`/`Unpin` — the flow is what these
represent and manipulate through the contexts involved. For the pin pair the
anchoring is outright more accurate: carrier semantics pins the FLOW open
while the context's extent is deliberately dropped, so `PinFlow` names the
true object and makes the leaked-pin consequence self-documenting. Word
order matters for the accessor (PN caught the first cut, `FlowOrigin`,
before it shipped): "flow origin" parses as *origin of my flow* — the
producer, the wrong reading, the very ambiguity that killed "source" —
while `OriginFlow`, adjective-noun, just IS *the originating flow*: the
drive of a handler, the last accumulate's flow at a flush, exactly how the
driver table speaks ("skim handler | the drive (skim) *flow*"). The family
also lands symmetric: pin flows, unpin flows, get the origin flow.

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

- `PinFlow` = structurally a top-level `WithFlow` scope meta (wave nil, riders
  carried) plus `permitRoot`, carrier refs (`flowRefRiders` + node ref), a pin
  marker, and a `context.Background()` root; pins are cold, so the pooled
  meta + child alloc is off the hot path.
- The flush driver link is the one new field: the flush body meta gets a
  pointer to the instance's pinned driver meta (set in `flush` while the
  rolling pin is still held; cleared with it).
- Gate expectations when implemented: the usual per-CP gate plus conservation
  extensions (a pinned-and-leaked ctx must show up in `TestCtxMetaConservation`
  as a deliberate positive; UnpinFlow restores zero), a pinned-dispatch end-to-end
  test (values delivered, follow-up waits for UnpinFlow), and a
  multi-goroutine pinned-dispatch -race test.

## As implemented: PinFlow / UnpinFlow (2026-07-10)

Faithful to the record, with two clarifications discovered at implementation:

- **`UnpinFlow` returns `error`.** The unpin is a synchronous user call site —
  the same shape as a `WithFlow` scope exit — so a fire it triggers runs
  INLINE as the pin's continuation and its error joins the return, exactly
  like scope-exit fires join `WithFlow`'s. (The alternative, a wave-rooted
  async fire, has no wave to root at: the pin is wave-less by design.)
- **The expired-marker detection window is exactly the meta's survival.** A
  pin with no surviving children recycles at the unpin, taking the marker
  with it — so a re-pin of an immediately-recycled token degrades to minting
  an empty pin rather than panicking. While anything still holds the meta
  (in-flight work dispatched from the pin), every framework entry — `PinFlow`,
  `WithFlow`, dispatch derivation (`ensureCtxMeta`) — panics on the expired
  pin. This is the record's "best-effort, honestly bounded" made precise.

The pin meta carries `parent` (ref'd) to the source meta, keeping the origin
chain walkable for the pin's lifetime — the position half of "structurally a
scope meta." Validation: retention/dispatch/compose/degenerate/validation
tests (pin_test.go), a multi-goroutine pinned-dispatch -race test, and
conservation arcs in both TestCtxMetaConservation and TestFlowNodeConservation
that assert the standing pin as a DELIBERATE positive (a leaked pin is
visible) and zero after release.
