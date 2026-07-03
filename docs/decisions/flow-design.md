# Flow: riders on the causal DAG

> Decision record (2026-07-03, design session). **Status: converged design; NOT
> implemented — no code exists yet.** Companion to the WORKING_NOTES "FLOW DESIGN
> CONVERGED" block, which this doc renders permanent. It **supersedes the refcounted
> `Flow` object** everywhere it appears (`API_DESIGN.md`'s `NewFlow` / `FlowFromContext`
> / `Dup` / `Close` / `WithAfterFunc`, `programming-model.md`'s "two user-facing types"
> framing, the surface-lineage Flow mentions): **there is no Flow type anymore.** What
> replaced it is a facility — one scope function plus user-minted keys and tags — over
> a structure the framework already maintains.

## The problem

Real workflows carry concerns that cut across the work they dispatch. A request handler
wants its request context — deadline, cancellation signal, OpenTelemetry span — visible
to every body that runs on the request's behalf, across waves and through aggregation.
A transaction wants a commit to fire once all work touching it has completed, including
follow-on work that the completion handler itself dispatches. Neither concern belongs to
any single wave: a wave is a batch boundary, and these lifetimes cross batches.

The constraints are the usual streampool ones. Body contexts are pooled and reused
(`body-context-pool.md`), so anything that rides them must survive reuse without
per-dispatch allocation. The hot path — `Submit` through admission to body execution —
must not pay for a feature most dispatches don't use. And the design must interoperate
with ordinary `context.Context` usage: user values placed on a submit ctx already
propagate (the nearest-meta `Value` walk), and that must keep working untouched.

The old answer was a user-facing `Flow` type: a refcounted, ctx-borne value handle with
`Dup`/`Close` and a `WithAfterFunc` option (see `API_DESIGN.md`, now marked superseded).
It worked on paper but put the reference-counting discipline in user hands and modeled
the wrong thing — as if flows were objects users create, rather than structure that is
already there.

## Ontology: flows are not created

A **flow is the causal DAG the framework already maintains**: nodes are work items;
edges are submits plus the funnel accumulate→flush fan-in. Every dispatch extends this
DAG whether or not any API is called — flows always exist. The API therefore creates
nothing; it only shapes **riders**: data and hooks that propagate along the DAG's edges.

Riders come in exactly two kinds, split by one property — **whether a merge operator
exists at fan-in**:

- **Values are path-scoped.** A value (a request ctx, a tenant, a transaction handle)
  has no canonical merge: when a funnel folds ten items into one flush, there is no
  truthful answer to "which request ctx does the flush carry?". So values inherit
  verbatim along chains — including nil/empty; there is no top-level default, and
  initiation is always explicit — and are **severed at fan-in**. A flush body reads
  absent, which is the truthful signal, and the fan-in owner re-asserts whatever is
  right: collect per-item values in the accumulate step, then pick one, union them, or
  start fresh in user code (funnel-owns-aggregation, same principle as the payload
  itself).
- **Lifetimes and follow-ups are DAG-scoped.** Reference counts form a counting monoid —
  they merge trivially — so lifetime refs **union through everything**, including
  funnels. "All work carrying this has completed" is meaningful across a fan-in in a way
  "the value at this point" is not. The set shrinks only at explicit suppression.

That one property drives the whole surface: path-scoped identities are **keys** (they
carry a value), DAG-scoped identities are **tags** (structurally valueless — see below).

## The surface

One function, two constructors, method-shaped options, two reads. Everything else —
instances, refcounts, the COW rider sets — is internal.

### `WithFlow`: a lexical scope, not a work item

```go
err := streampool.WithFlow(ctx, body,
    requestCtx.Value(r.Context()),
    checkout.FollowUp(commitFn),
    audit.Suppress())
```

`streampool.WithFlow(ctx, body, opts...)` runs `body` **inline on the caller's
goroutine** with a flow-stamped pooled ctx; the lexical scope is the flow root. It is a
plain function call, not a dispatched work item: no wave membership, no permits, no
backpressure, panics propagate (uniform with every other body — the framework never
recovers anywhere; see "Panics" below), and `body`'s error is returned verbatim. With
zero options the call degenerates to `body(ctx)`. Scope refs release via `defer`, so a
panicking scope stays conservation-sound, and the body ctx is valid for the duration of
the call (the existing body-ctx escape contract).

`WithFlow` is **optional**. Flows aren't created — they are always already there: every
bare `Submit` roots or extends the DAG with the ambient (possibly empty) rider set.
`WithFlow` only opens a lexical extent with a *modified* rider set; wrapping a single
`Submit` is how you register riders per-dispatch. Most programs never call it — and are
still fully in flows when they don't. (The name evolved Exec → Flow → WithFlow; no
non-flow use case can exist, because everything scope-expressible is a DAG rider,
including the deferred priority feature.)

Ops keep bare `Submit(ctx, v)`: no submit options, no handles, no lifecycle objects.

### Keys and tags: scoping declared by name

```go
var requestCtx = streampool.NewFlowKey[context.Context]() // path-scoped, carries a value
var checkout   = streampool.NewFlowTag()                  // DAG-scoped, valueless
```

- `NewFlowKey[V]()` mints a **path-scoped** key. Any `V` is allowed, including
  `struct{}` — a path-scoped *marker* is coherent (it severs at fan-in like any value).
- `NewFlowTag()` mints a **DAG-scoped** tag. It takes no type parameter and has no value
  slot, so a data-bearing DAG-scoped key is *unrepresentable* — the type system enforces
  the no-merge-operator split rather than a runtime rule.

Keys and tags are minted cold (package-level or per-unit-of-structure — the user chooses
the granularity: one shared across many flows, or one per flow).

### Options are methods on the key or tag

- `key.Value(v V)` — attach a value under a path-scoped key. Compile-time key→value type
  binding; `FlowTag` has no `Value` method at all.
- `key.FollowUp(fn)` / `tag.FollowUp(fn)` — register a completion hook that fires at the
  identity's nominal end (below). The name teaches the extension semantics: the handler
  is potentially *more flow*, a follow-up, not a terminator.
- `key.Suppress()` / `tag.Suppress()` — stop inheriting that identity into this scope.
- `streampool.NewFlow()` — the one package-level option: clears the entire *inherited*
  rider set (fresh flow root). Suppress-all reframed with positive intent-naming; it is
  order-independent with respect to sibling options, which add to the fresh set. ("New"
  here is semantic — a new flow — accepted over the New\*-means-constructor convention
  nit.)

A key's bundle `{value?, follow-ups...}` propagates **as a unit** under the key's
scoping (one identity namespace; scoping is a key property). So "fire when all work
*carrying this value* completes" — the release-the-carried-request-ctx case — is one
path-scoped key holding both the value and the follow-up. "Commit once everything,
*including work downstream of aggregation*, completes" is a `FlowTag` follow-up, whose
refs union through fan-ins. Genuinely different lifetimes are two keys, deliberately.

### Reads

- `key.From(ctx) (V, bool)` — comma-ok value read; the `XFromContext` idiom in method
  form. (`Value` was already taken by registration — good: reads shouldn't look like
  writes.)
- `tag.InFlow(ctx) bool` — presence; ORs through fan-ins.

Reads consult the nearest meta's rider set and return `(zero, false)` on a never-stamped
ctx — no panic. Because propagation uses the same nearest-meta `Value` walk as
ambient-wave resolution, riders (and otel spans carried as values) survive foreign
`context.WithValue`/`WithCancel` wrappers untouched.

### Declaration naming convention

The convention is docs-borne, taught by example, with **no `Key`/`Tag` suffixes**:

- A **key is named for the value it carries**: `requestCtx`, `tenant`, `txn`. Every use
  site then reads as a sentence about the value — `txn.Value(t)`, `txn.From(ctx)`,
  `txn.FollowUp(commit)`.
- A **tag is named for the flow it identifies**: `checkout`, `ingestion`, `audit` —
  `checkout.InFlow(ctx)`, `checkout.FollowUp(fn)`.

The method-shaped API is what makes this possible: receiver position gives the noun its
grammatical role, where free functions would have forced the suffix back
(`WithFlowValue(txnKey, t)`). Accepted caveat: a noun key can collide with the natural
local for a read result — `txn, ok := txn.From(ctx)` is legal-but-ugly shadowing; users
pick a shorter local. That trade goes to call-site readability where it counts.

## Lifetime semantics

The scope's own reference covers entry→return, so the attach window is race-free
**lexically**: parent-covers-children, and the early-fire race (work completing before a
sibling attaches) is unwritable. Multi-root — several top-level submits in one scope —
is therefore never special. An empty scope fires its follow-ups at return.

- **Nominal end**: the identity's count reaches zero after the scope has exited → the
  follow-up fires.
- **Extension**: the follow-up's own dispatches inherit the firing instance ambiently (a
  framework-held provisional ref bridges fire→admission), so a follow-up that submits
  more work extends the flow — a later nominal end fires the follow-up again.
- **True end**: a firing that extends nothing.

At a funnel fan-in, an accumulate item's DAG-scoped refs **transfer** to the funnel
instance at the item's completion — the instance never transits an unreferenced state
(the same never-transit-unreferenced discipline as `depositOccupy` in the permit
accounting; see `weighted-acquisition.md`) — and the flush item takes over the
instance's accumulated set; downstream
submits inherit before the flush item releases. Path-scoped values, per the ontology,
sever at the same edge: the flush body reads absent, and re-assertion is the fan-in
owner's user code.

Internally there are two identities, and only one is visible. The minted key or tag is
the **shaping** identity — what you suppress, read, and mix-and-match; minted cold,
shared or per-flow at the user's discretion. Each *registration* creates an **instance**
(pooled, generation-stamped state) — the **lifetime** identity — and instances are fully
internal: no user handle exists. Same-type instances never auto-merge; merging by type
would weld together concurrent requests that happen to share a package-level key.

## Context roles and cancellation

`WithFlow`'s ctx parameter is **execution ancestry**: it supplies ambient rider
inheritance and the cancellation lineage for dispatched work — a body ctx when nested, a
stable app/base ctx at top level. A request ctx enters as a flow **value**
(`requestCtx.Value(r.Context())`): data, consultative only. Bodies read its `Err()`,
deadline, or span explicitly; it is **never a parent of framework ctx derivation**.

Riders are pure values — no framework cancellation derives from any flow value, and the
framework never merges cancellation scopes. An AfterFunc-on-cancel remains user-space on
the user's own ctx; nominal-end events are the framework's. The completion hook is the
required primitive: "commit the upstream transaction at flow end" and "cancel a carried
ctx once nothing references it" are both just things `fn` does at nominal end — one
mechanism, no cancellation machinery.

This resolves TODO.md's "joined-context adapter" question (deferred "until Flows
decides"): nothing merges two independent cancellation scopes, so no adapter is needed.
For the same reason the fresh-parent pooled-ctx seam is not applicable here (parents are
pooled body ctxs or stable base ctxs, populations `ctxpool` already amortizes), and the
delegating-parent custom-ctx idea is shelved. One residual, pre-existing and orthogonal,
cost: a fresh request ctx passed directly to a *top-level* `Submit` pays `ctxpool`'s
one-time childPool+AfterFunc setup on first touch. The cancellation model itself is
unchanged: request cancellation stops bodies only if the request ctx is in the execution
ancestry — the user's explicit choice, at the user's explicit cost.

## Panics

The framework never recovers panics, anywhere. (The old "recovered and surfaced as an
error" claims in `doc.go` and `programming-model.md` were unimplemented fiction from the
original design-doc drop and were struck in `0e486f4`; the funnel's panicked-sentinel
defers are cleanup-on-unwind, not recovery.) `WithFlow`'s body is uniform with dispatched
bodies in this respect: a plain call on the caller's goroutine whose panics propagate,
with scope refs released on the unwind so accounting stays sound.

## Cost model

The design adds one pointer — the rider set — to the pooled `ctxMeta`, and pays only at
the edges where the set changes:

- **Inheritance is by pointer**: a dispatch under an unchanged rider set copies one
  pointer, zero allocation.
- A **pooled copy-on-write node** is built only at registration/suppression edges
  (i.e., inside a `WithFlow` that actually modifies the set).
- Each funnel instance keeps a **small multiset** of inherited refs (dedupe by identity
  plus a count — the bindings-slice allocation pattern already used elsewhere).
- **Keys and tags are minted cold**; reads are a small linear scan over the rider set.
- Options follow the **copy-out-never-retain** variadic discipline verified 0-alloc for
  `WithLimits`: the option values (and their boxed payloads) stay on the caller's stack.

Instances are pooled, generation-stamped, and internal — no per-flow allocation escapes
to the user.

## Rejected alternatives

Recorded so none of this is relitigated.

- **The refcounted `Flow` object** (`NewFlow(parent)` returning `(ctx, Flow)`, with
  `Dup`/`Close`, `FlowFromContext`, and `WithAfterFunc` — the old `API_DESIGN.md`
  surface). It made users perform reference-counting discipline (`Dup` when handing off,
  `Close` on every path) that the lexical scope now performs structurally: the scope's
  own ref makes the attach window race-free with no user action, and a forgotten `Close`
  is unwritable. It also modeled flows as created objects, which the ontology rejects,
  and required a generation-stamped user handle for state that is now fully internal.
- **Op builder `.As(flow)` plus use-site stacking** — died with the flow-as-object
  model it decorated (once flows are structure rather than objects, there is nothing to
  pass to an op); it would also have scattered the concern across every dispatch site
  instead of one lexical extent.
- **A `WithFlow(ctx)` submit option / `FlowFromContext`** (a mid-session idea; also the
  name now belongs to the scope function). With bare `Submit` kept option-free and every
  such use expressible as an ordinary user-minted path key, no built-in key is
  warranted; dropped.
- **Initiator-held ref + `Close` for multi-root** — superseded by the lexical root
  closure: the scope ref covers all roots opened within it, so multi-root needs no
  ceremony at all.
- **Per-registration suppression handles** — coupling the suppression site to the
  registration site inverts the actual need (a downstream scope decides what *not* to
  inherit); replaced by per-key/tag `.Suppress()` on the minted identity.
- **Framework auto-merge of same-type instances** — would weld concurrent requests that
  share a package-level key into one lifetime.
- **Framework-collected value sets at fan-in** — unbounded ctx pinning, a per-window
  allocation, and an unanswerable dedupe question; the funnel owner collecting per-item
  in accumulate keeps aggregation user-defined, like the payload's.
- **The `sizeof(V)==0` scoping rule** (valueless key ⇒ DAG-scoped) — agreed and then
  rejected the same day: a `struct{}`→`bool` refactor would silently flip a key's
  scoping; the rule is invisible at use sites; and it is spooky under generic `V`. The
  explicit `NewFlowKey[V]`/`NewFlowTag` constructors keep the type-level guarantee
  (data-bearing DAG keys unrepresentable) without the cliff.
- **A free-function / `With*`-prefixed option family** (`WithFlowValue(key, v)`,
  `WithoutFlow(key)`, …) — the `With`/`Flow` prefix stutter, weaker static typing (no
  compile-time key→value binding), and worse prose at call sites; method-shaped options
  won, and `WithoutFlow` became redundant next to per-key `Suppress()` plus `NewFlow()`.
- **Naming trail, `FollowUp`**: `After` was rejected for its harmful echo of
  `context.AfterFunc`, which fires on *cancel* — the opposite trigger;
  `Close`/`Commit`/`Cleanup`/`Done`/`End` all suggest termination, where the semantics
  are extension (the handler is potentially more flow).
- **Naming trail, `InFlow`**: `Tags` (plural-noun misparse), `Tagged`/`Marked`/`Labeled`
  (participial dodge), bare `In`/`On` (wrong object — the tag is on the *flow*; the ctx
  merely reaches the flow, and collapsing the two hops lands the tag on the ctx),
  `IsTagOf` (correct but awkward), `Contains`/`Covers`/`Reaches` (too math/CS-flavored),
  `Describes` (tags carry no information), `Active`/`Underway` (claim state the
  framework doesn't track). `InFlow` names the two-hop structure, converges true under
  both parses ("is checkout in the flow of ctx" / "is ctx's work in the checkout flow"),
  and rhymes with `WithFlow`/`NewFlow*`. Accepted demerit: the "inflow" noun homograph,
  visually broken by the camelCase.

## Open details

Small items deliberately left to the implementation pass; nothing here moves the design:

- **Follow-up signature sugar**: whether `fn` optionally receives the bundle's value for
  a path-scoped key (a typed handoff is free under the unified bundle). Undecided.
- **Option allocation verification**: confirm the variadic options and their boxed
  payloads stay on the stack (the `WithLimits` discipline), with the usual escape
  analysis + benchmark check.
- **Constructor naming remainder**: whether the shaping-identity constructors stay the
  `NewFlowKey[V]`/`NewFlowTag` pair or unify further (WORKING_NOTES open-queue item 4);
  the option, function, and read names above are settled.
