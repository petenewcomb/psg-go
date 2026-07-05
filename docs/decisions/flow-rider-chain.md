# Flow riders as a refcounted chain, not a flat snapshot

> Decision record (2026-07-05, design session). **Status: spec — converged, not yet
> implemented.** Redesigns the rider-set representation introduced in CP-F1 and reshaped
> in CP-F6. It **supersedes**: `flow-design.md`'s "Cost model" flat copy-on-write
> snapshot and the per-instance `fnRiders`, and it **settles CP-F7/F8's walk-vs-flatten
> question in favor of walk** (the funnel sever becomes a chain operation). Companion to
> `flow-design.md`, whose surface, lifetime, and error semantics are unchanged — only the
> internal representation of the rider set changes.

## Why

The shipped representation is a flat, immutable `flowRiders` snapshot carried by pointer
on each `ctxMeta`. It reads well (a single nearest node, pre-merged) but pays for that at
every registering `WithFlow` with a **copy-on-write of the whole ambient set**, plus a
second **per-follow-up `fnRiders`** snapshot (the enclosing set the fire carries). Both
are **once per flow** — warm, not cold, for per-request flows — and both are GC-owned.
That is out of step with the framework's zero-warm-allocation discipline (everything else
is pooled), and the flat form is also what forced the awkward "merge item-over-driver at
stamp *or* teach reads to walk" fork that CP-F7/F8 were going to have to resolve.

A **linked chain of rider nodes**, walked on read, removes both allocations' *copies*: a
registering scope allocates **one** node (its own additions) linked to the inherited
head; a follow-up's enclosing set is simply **its parent node** — no `fnRiders` at all.
Walking is the *same* complexity as scanning the flat snapshot (both linear over the same
entries), so there is no read regression; a lookup *map* would be the only speedup, built
lazily on demand if a workload ever justifies it. And the node was always going to need a
reference count — it is captured by async carriers and outlives the `WithFlow` call — so
pooling the nodes falls out of the count that has to exist anyway.

## The node

**One entry per node** — a single identity's binding (its value and/or its follow-up),
inlined, no backing slice:

```go
type flowRiderNode struct {
    id     *flowIdentity  // the key or tag this binding is under
    val    any            // the key's value, when this registration set one
    hasVal bool           // distinguishes Value(nil) from "no value" (and tag nodes)
    inst   *flowInstance  // the follow-up, when this registration set one
    next   *flowRiderNode // the enclosing chain (toward the root); nil at a flow root
    refs   atomic.Int64   // carriers + child nodes pointing here; reclaimed at 0
}
```

- **A key's value and its follow-up live on the SAME node.** The CP-6 bundle is physical,
  not two nodes: `val`+`inst` under one `id`. `From` returns from the nearest node with
  `hasVal`; presence/instance-collection reads `inst`; `Suppress(id)`/sever drop the whole
  node by id / by the identity's scoping kind.
- **One alloc per registration, no backing slice.** The flat model's node carried
  `entries []flowRiderEntry`; here each entry *is* a node. A `WithFlow` adding a single
  bundle (the common case) is one node; several become several nodes chained ahead of the
  inherited head — same total bindings on the walk, only more nodes (and a longer
  reclaim-cascade) for the rare wide scope.
- **Immutable after construction** except `refs`. Fields are never mutated once published,
  so any number of goroutines walk a node concurrently without synchronization. A
  modification (register/suppress/sever) produces *new* nodes and leaves the old ones
  intact for everything still pointing at them.
- `m.riders` on `ctxMeta` becomes `*flowRiderNode` (the chain head). It is **inherited by
  pointer, not severed**, at every dispatch/borrow (`borrowBodyContext`,
  `ensureCtxMeta`) — exactly as the flat pointer is today. This is a *different* pointer
  from `ctxMeta.parent` (the permit chain, which **is** severed at async body borrows so a
  worker cannot find its dispatcher's permit); the rider chain crosses async by design.

## Surface: one follow-up, qualified

The follow-up is the **primitive**; keys and tags are **qualifiers** on it, and
suppress/new-flow are **boundaries**. This reframes CP-6's "tags are how you attach a
follow-up" into a smaller, teachable shape (and demotes `FlowTag` to what it actually
earns: a *name* for presence and targeted suppression).

| You want to… | Reach for | At a fan-in |
|---|---|---|
| carry data downstream | `key.Value(v)` | **severs** (data has no merge) |
| run something once at the end | `FlowFollowUp(fn)` | crosses (DAG) |
| …with that data in hand | `key.FollowUp(v, fn)` | severs (path) |
| …and suppress/query it by name | `tag.FollowUp(fn)` | crosses (DAG) |
| infuse work so bodies can ask "am I in it?" | `tag.Infuse()` + `tag.InFlow(ctx)` | crosses (DAG) |
| keep a concern out of a subtree | `x.Suppress()` | — |
| start a clean flow | `NewFlow()` | — |

- **`FlowFollowUp(fn)` is the default** — an **anonymous, DAG-scoped** follow-up: "run this
  once at the flow's true end, across aggregation." No type to mint. It reuses the tag
  interface (valueless): `FlowFollowUp(FlowTagFollowUp)` / `FlowFollowUpFn(func(ctx) error)`.
  Internally it mints a fresh **unnamed** DAG identity per registration — tag-kind so the
  sever keeps it, but no exposed handle, so it can be neither queried nor suppressed.
- **Reach for a key** only when the follow-up needs a **value** (and you accept it *severs*
  at aggregation); **for a tag** only when you need a **name** — to `InFlow`-query the flow
  or `Suppress` it. `tag.Infuse()` is bare presence (a valueless infusion node, no
  lifetime) for the "membership only" case; `tag.FollowUp(fn)` is presence **plus** a
  lifetime. The metaphor carries: a flow is *infused* with a tag, tints/infusions **blend**
  at a fan-in (the union), and `Suppress` clears it from a subtree.
- The single fact that resolves almost every "which one": **a value severs at a fan-in; a
  lifetime or a presence crosses it.** That picks key vs tag/anonymous. The second, smaller
  question is only "do I need a *name*."

The follow-up interfaces and their `Do` method are unchanged (`flow-design.md`): a keyed
follow-up is `FlowKeyFollowUp[V]`, a tag/anonymous one is `FlowTagFollowUp`.

## Options: a value struct, with an explicit-value key follow-up

> **Superseded during CP-R3 (2026-07-05) by a benchmark.** The design below replaces an
> earlier "composed interfaces + fluent bundle" sketch (`FlowOption` an interface,
> `key.Value(v).FollowUp(fn)` fluent). That sketch was **measured and rejected**: it costs
> **~2 warm allocations per registering `WithFlow`** (value-only scope 0 → 2), because a
> generic `valueOption[V]` in a heterogeneous variadic can only be dispatched through an
> interface method (`applyToFlow`), and interface dispatch is **opaque to escape analysis** —
> `go build -gcflags=-m` confirms both the `...FlowOption` variadic and the builder escape to
> the heap. `OpOption` gets away with the interface because op construction is cold; `WithFlow`
> is warm (per request-scope), so the fluent surface would forfeit exactly the zero-warm-alloc
> win CP-R2 landed. The Go constraint in one line: *typed fluent follow-up ⟹ generic option ⟹
> interface variadic ⟹ heap.*

`FlowOption` is a **value struct** (a small kind-tagged record; no interface boxing), so a
registering scope allocates nothing warm. All constructors return it:

```go
func (k FlowKey[V]) Value(v V) FlowOption                                    // bind a value
func (k FlowKey[V]) FollowUp(v V, h FlowKeyFollowUp[V]) FlowOption           // bind v AND hook it
func (k FlowKey[V]) FollowUpFn(v V, fn func(context.Context, V) error) FlowOption
func (t FlowTag)    FollowUp(h FlowTagFollowUp) FlowOption                    // valueless hook
func (t FlowTag)    FollowUpFn(fn func(context.Context) error) FlowOption
func (k FlowKey[V]) Suppress() FlowOption
func (t FlowTag)    Suppress() FlowOption
func NewFlow() FlowOption
```

- **A key follow-up takes its value as an explicit first argument**: `key.FollowUp(v, h)` /
  `key.FollowUpFn(v, fn)`. This keeps the property the fluent form was reaching for — the value
  the follow-up receives is unambiguously the `v` written right there, captured at registration
  (no ambient lookup, no order dependence) — while staying 0-alloc and preserving the typed
  `func(ctx, V)` signature (the generic lives on the `FlowKey[V]` receiver, not on a boxed
  option). It is one option → **one node** (`val` + `inst` together), the same bundle node the
  fluent form would have built. Same `FollowUp` verb as a tag's, with the value added.
- There is **no standalone key follow-up without a value** and **no fluent chain**: `Suppress`,
  `NewFlow`, and a bare `FlowKey` simply have no `FollowUp` method, so `Suppress().FollowUp()`
  is not expressible — the "type system forbids it" property survives without composed
  interfaces (there is nothing to chain onto).
- **Node invariant**: on a key node, `inst != nil ⇒ hasVal` — value and follow-up always arrive
  together (a key follow-up carries its value). A key bundle is exactly one option → one node;
  construction-merge only ever reconciles repeated *values* under one id. Internally the struct
  carries a `hasVal` bit (true for `Value` and for a key follow-up; false for a tag follow-up,
  suppress, new-flow) so `settledVal` reads the bound value off either shape.

## Reads

`FlowKey.From` walks `head → next → …` for the **first `valueRider` node** with the id;
`FlowTag.InFlow` for the first node with the id (a tag only ever has `followUpRider`
nodes, and presence is what it reports). Nearest wins, so an inner scope's binding shadows
an outer one, and a `Suppress`/sever that omits the id reads as absent. Absent ids walk to
the root. This is O(total bindings), identical to the flat scan.

An optional **on-demand map** (`map[*flowIdentity]…` built lazily and cached on the head)
is the only way to beat linear; deferred until a measured workload with deep sets and
hot reads justifies it. Not in the first cut.

## Reference count and reclaim

The count is the piece that makes every node poolable and bounds chain depth to *live*
nesting rather than total-ever.

- **A node holds one ref on its `next`**, taken when the node is published, released when
  the node itself is reclaimed.
- **A carrier (a body ctx) refs the head** it captures at `borrowBodyContext`, and
  **unrefs at `releaseBodyContext`**. The scope's own meta refs its head for the
  `WithFlow` call, released at return.
- **Reclaim cascades**: when a node's `refs` hits zero it is returned to its pool and
  drops its ref on `next`, which may reclaim `next` in turn, down the chain.
- **Branching falls out of the count**: two nested scopes over one ambient node make its
  inbound count 2; it is reclaimed only when both let go. No special case.

Because a node reclaims as soon as its carriers drain, the live chain is only as deep as
the current `WithFlow` nesting (plus any outstanding async work still holding a head).
Sequential re-stamps *enter and exit* — each exits and reclaims — so they never
accumulate; only genuine concurrent nesting adds depth, bounded by the stack. This
replaces the flat model's "collapse to distinct ids" self-bounding with "collapse via
reclaim," same bound, no per-scope copy.

A **follow-up instance points at its parent node** as its enclosing set (its fire's rider
context). That node stays alive exactly as long as it has carriers — which is exactly as
long as the instance can still be referenced — so the instance owns nothing extra and the
CP-6 "peel" is free (the enclosing set is literally the node below where it registered).

## Construction: one bounded-rebuild primitive

All set modifications are one operation with a filter predicate and an optional stop
boundary:

```go
// rebuild walks head down to stop (exclusive), copying each node keep() accepts (with
// next rewired past the dropped nodes) and skipping the rest, then links the copied
// prefix onto stop (shared, untouched). stop == nil walks to the root. Because a node is
// one binding, keep is a whole-node predicate — no in-node filtering, no splitting.
func rebuild(head, stop *flowRiderNode, keep func(*flowRiderNode) bool) *flowRiderNode
```

- **Plain registration** (`WithFlow` adding values/follow-ups) is *not* a rebuild: it is
  one fresh node per addition, chained ahead of the inherited head. O(1) per addition, no
  walk.
- **`NewFlow`**: a fresh root — the scope's own addition nodes with the last `next == nil`.
  Nothing inherited; no rebuild.
- **`Suppress(id)`**: `rebuild(head, nil, keep = node.id != id)` — walk to the root, copy
  the kept prefix, drop **every** node under `id` (a repeated-registration id must not
  re-surface below the nearest). Cold path; O(depth). No user-facing depth limit —
  "suppress this id, period."
- **Funnel sever**: `rebuild(head, funnelDispatchNode, keep = node.id.kind == flowTagIdent)`
  — the same op keeping only **tag-identity** nodes (path-scoped keys sever — both their
  value *and* follow-up nodes — while DAG-scoped tags survive), **stopped at the funnel's
  dispatch-point node**. The bound is the whole of CP-F8: everything *above* the boundary
  (the enclosing flow driving the subwave) is shared **intact — values and all** — while
  only the per-item portion *below* it severs to tags. Walking to the root instead would
  wrongly strip the enclosing flow's values. (Note the predicate is the *identity's*
  scoping kind, not the node's value/follow-up kind: a key's follow-up node severs too.)

The stop boundary is thus an *internal* capability the sever uses (boundary = the driving
flow's chain head captured when the funnel was dispatched), not a user-exposed knob.

The cross-item **tag union** at a fan-in is a separate accumulation, not part of the
per-item sever — see "Fan-in" below.

## Fan-in: union, and definitional-tag coalescing

The funnel accumulate→flush is the **only fan-in** in the framework (skim is a
continuation, not a join — CP-F7; a nested subwave join shares an ancestor, so no merge),
so it is the only place per-item tags union and the only place independent tag lifetimes
coalesce.

**Union (per-item tags → flush).** The boundary is the driving flow's chain head, captured
by pointer at funnel dispatch and ref'd until flush. As each item accumulates (under the
funnel instance's `mu`), walk its chain from the head, **stop at `== boundary`** (pointer
identity — chains are shared by pointer), and fold the tag-kind nodes above it into the
funnel instance's union: **markers dedup by id** (pure membership, no ref), **follow-up
instances dedup by pointer** (one adopted ref each — the CP-6 ref-before-release: the
accumulate item still holds its ref while the funnel takes one). At flush, materialize the
union into a short node chain linked onto the shared boundary — that is the flush body's
rider head, released (with adopted refs) at flush end. So a flush reads as a plain walk:
union first, then the enclosing flow intact (values and tags), per-item values severed. An
item submitted under `NewFlow` (not descending from the driver) never hits the boundary and
simply contributes its whole severed tag chain — correct, it is a different flow root.

**Definitional tag follow-up.** A follow-up attached to a tag's *identity* (at declaration)
fires **once per flow** carrying the tag, regardless of how many points infused it —
infusion is idempotent with respect to it. Within a shared chain, every infusion dedups to
one instance (found by id on the walk, not re-created); no coalescing needed. It
*complements* per-scope `tag.FollowUp` (a distinct per-registration lifetime), it does not
replace it.

**Coalescing (independent flows).** When flows with **no common ancestor** each infuse T and
converge at a funnel, their separate instances must merge into one lifetime so the
definitional follow-up fires once. Mechanism — **serial union-find under a per-tag merge
lock**:

- Each definitional-tag instance keeps its own refcount (its carriers) plus a **shared-node
  pointer**, nil until it first merges. Shared nodes form a refcounted hierarchy; a
  component's root, fully dereffed, fires the follow-up once.
- **Merge** (two instances meet at accumulate): find each one's **root** (walk shared-node
  parents), and if the roots are equal it is a **no-op** (already merged — the common case,
  since a funnel re-meets the same instances on every item); else link the roots under a new
  parent, adjusting refs. All of this under the **per-tag merge lock**, which serializes the
  one residual concurrency the funnel `mu` doesn't cover: two *different* funnels racing to
  union the same still-unmerged roots. Operands are provably **live** at the merge (the
  accumulate item still holds the ref — ref-before-release), so there is no merge-vs-death
  race.
- **Deref**: an instance's own count → 0 derefs its shared node; a shared node → 0 derefs its
  parent; cascade; a fully-dereffed root with no parent means the whole aggregated flow is
  done → fire once. (No path compression — a deref is O(component depth); fine, merges are
  rare.)
- **Downstream** (post-flush work carrying T) is not a fresh merge: the flush mints one
  instance for its output flow and links it into the component root (serial, under the same
  lock), and downstream carriers ref it like any node — so the follow-up also waits for
  post-funnel work.

The day another op introduces a fan-in, the "funnel is the only merge site" assumption
reopens — worth a comment on the merge code.

## CP-F6 coupling on the chain

CP-F6's **inner-holds-outer** (an inner follow-up holds a ref on every enclosing instance
until its own fire completes, so an outer waits for the whole nested subtree) is an
**instance-count** relationship, orthogonal to how the rider set is stored. It carries
over: "the enclosing instances" is now *walk the parent node chain and collect the
instances*, instead of reading the flat `fnRiders`. The explicit holds remain the
firing-order mechanism (they bridge the registration→fire gap regardless of
representation). Whether the fire's own ref on the parent chain's instances (via
`flowRefRiders` over the walked chain) lets us *derive* the holds more cheaply is an
implementation detail to settle against the `-race` gate, not a semantic change.

The **peel** and **value-as-arg** are unchanged: the fire runs under the parent node (self
peeled by construction — self's entry lives in *its* node, the fire carries `next`), so
`From` reads absent inside and the value arrives as the `Do` argument.

## Pooling summary

With retain-of-a-scope-ctx defined as undefined behavior (a scope ctx is call-scoped like
every framework-provided ctx), all three warm per-flow allocations are pooled:

- **Scope meta** — pooled like every other meta, freed at `WithFlow` return (async work
  carries its own body meta and never resolves the scope meta; retain-safety was never the
  real constraint).
- **Rider nodes** — refcounted (above), reclaimed to a pool when carriers drain.
- **Follow-up instances** — recycled at fire completion, **generation-free** (fire-once +
  retain-is-UB means no reference can appear after the count hits zero, so no ABA guard).

## Concurrency and validation

Nodes are immutable-but-for-`refs`, so reads need no synchronization; the risk surface is
the refcount/reclaim protocol (atomic count, cascade on zero, pool recycle) racing carrier
ref/unref and reclaim. This is exactly the class the simulation exercises. The
implementation lands in green checkpoints, each gated by a large `TestBySimulation -race`
batch, with the count/cascade under scrutiny — no commit on a false green.

## Open details

- **Tag-union node shape** at a fan-in (chain union of per-item tag sets) — lands with
  CP-F7/F8.
- **On-demand read map** — deferred until measured.
- **Holds derivation** — whether the chain lets inner-holds-outer be read off the walk
  rather than materialized; settle during implementation.
