# OpenTelemetry tracing on flows: a composable model, not a wrapper

> Decision record (2026-07-08, design session with PN). **Status: design — converged,
> not yet implemented.** Supersedes the `otpsg` package (both its v1 per-op-span form
> and the v2 one-span-per-flow form). Establishes the model `streamotel` will express,
> and the scope decision that it is a *patterns package* — a guide plus runnable
> examples plus at most one small helper — rather than a set of otel wrappers.

## Why

`otpsg` v1 put a span per op with `defer span.End()` at the op boundary and threaded
context through a `PropagatedResult[T]` envelope. That ends a span before the async
work it spawned finishes, and reinvents context propagation the flow rider already
does. v2 collapsed the whole causal DAG into a single flat span per flow — too coarse
to represent the relationships that actually occur. Both were *wrapping otel*.

Two facts reframed the problem:

1. **Async/convergent tracing is the least-settled corner of OpenTelemetry.** The
   data model deliberately allows a span **exactly one parent** and represents every
   other causal predecessor as a **link** (a simplification from OpenTracing's
   multiple typed references). Links carry thin semantics, and the guidance for
   fan-in is "prefer links, and don't set a parent that doesn't fully enclose the
   child." This is a place to *assert an opinion*, not to hide one behind a wrapper.
2. **Flows already compute the distinctions otel tracing needs.** The path-scoped /
   DAG-scoped sever seam is exactly the within-transaction / cross-transaction
   classification; the follow-up is exactly async span enclosure; the F8 boundary is
   exactly "is there a fully-enclosing parent here." So the integration is
   *composition* of plain otel calls with flow primitives — there is almost nothing
   to wrap.

So `streamotel` illustrates composable patterns and marks the decision points,
recommending a lean where one is defensible, and provides a tool only where it earns
its keep without making more than the most fundamental decision for the user.

## The model

**The flow is primary; it is the trace.** A flow is defined by its data lineage, not
its operations. Spans are organized by that lineage. There are two senses of "flow",
split by the one property that also splits flow riders — whether a merge operator
exists at a fan-in:

- **Path-defined flow** = one *transaction*: an atomic set of external inputs and its
  end-to-end processing. Path-scoped riders (values) propagate along it and **sever at
  a fan-in** — so a transaction *branches* into contributors at a funnel. **A
  transaction is a trace.**
- **Tag-defined flow** = the *overall* flow: the whole connected causal component.
  DAG-scoped riders (tags) **cross fan-ins** (they union through funnels), so the
  tag-flow spans every transaction that converges. Its identity and lifetime are a
  coalescing definitional tag's (see `flow-rider-chain.md` §Fan-in): it is the
  aggregate that fires once at the true end. **The tag-flow is not a single trace; it
  is a graph of traces stitched by links (below).**

**Spans are bounded by (sub-)flows, never by waves.** A flow completes when its own
work-reference lineage drains — the refcount→0 that fires its follow-up — which is
independent of, and usually earlier than, any wave reaching Done. A shared or
long-lived wave *contains* a flow's work (trivially) but its boundary is
server-granularity, so it cannot bound a transaction span. Concretely:

- a purely-synchronous op span is bounded by its own body (`defer End` at return);
- a span covering **async extent** — a subsection whose work outlives the body that
  dispatched it — is bounded by a **(sub-)flow follow-up**, which fires when *that*
  sub-lineage's refcount hits zero. The follow-up is what supplies the enclosure a
  lexical scope lacks. **A parent in the async tree must therefore be
  follow-up-bounded**, or it would end before its async children run — violating
  otel's "parent fully encloses child".

**Waves are pure execution substrate.** Wave lifetimes overlap arbitrarily (a
long-lived wave interleaves with others; it serves many flows at non-adjacent points;
a sub-wave can outlive its dispatcher), so **waves do not form a tree** and a
wave-lifetime span has nowhere to sit. A flow's *participation* in a wave — its
contiguous same-wave subsection — is a legitimate sub-flow span, but the wave itself
is recorded as an **attribute** (a wave ID), not a span or a linked span.

## Three axes, three otel mechanisms

A flow's work sits at the intersection of three genuinely independent axes. They
cannot be folded into one hierarchy — the clearest proof is that at a fan-in the
**driver is continuous across the exact point where the data forks** (the same
driving context is behind both the pre-flush contributions and the post-flush
aggregate). So one axis gets the hierarchy and the others get links:

| Axis | Relationship | otel mechanism |
|---|---|---|
| **data lineage** (primary) | the async transaction DAG | **parent/child** — sub-flow nesting, follow-up-bounded |
| **aggregation** | data fan-in: a flush and its severed contributors | **links** (`link.kind = aggregated-from`), upstream, cross-trace |
| **driving control** | the synchronous chain that pumped a body | **links** (`link.kind = driven-by`), one hop, sideways, cross-trace |
| **execution substrate** | which wave ran this subsection | **attribute** (wave ID), not a span |

This is a deliberate **inversion of conventional otel usage**, where parent/child is
the synchronous call stack and links are async boundaries. It is inverted here
because this system inverts what is primary: the async data flow is the thing being
traced, and the synchronous call stack is incidental executor plumbing. Whatever is
primary earns the hierarchy.

**Aggregation links.** At a funnel flush the flow's sever seam already classifies
contributors. The **enclosing driving flow** (the F8 boundary — the flow above the
funnel's wave) is still *present* at the flush exactly when a transaction fully
encloses the funnel; that is the one span that may legitimately be the **parent**.
The independent contributors **severed**, so they are collected at accumulate and
attached as **links** at flush. Within-transaction contributors need no link — they
are already under the parent. So the three-way classification is: present ⇒ parent;
severed ⇒ link; same-lineage ⇒ neither.

**Driver links.** Each span records one link to its *immediate* driver; the full
driving chain is recovered by walking hop-to-hop. A driver is anchored to a single
flow but drives work across many, so driver links are generally cross-trace — and
their continuity across a fan-in is what proves the axes are independent. They are
told apart from aggregation links by `link.kind`, not by trace-locality (both cross
traces).

**Wave attribute.** Tag the wave-participation sub-flow span with a wave ID. Backends
group and filter by it without a span for the wave — which is right, because waves
don't nest and a wave-lifetime span would be long-lived and accrete unbounded links.

## Trace-ID policy: one trace + links, never unify

A transaction is a trace. When independent traces converge at a funnel, the aggregate
must live in **one** trace and **link** to the rest:

- **enclosing driver present** at the flush (within-transaction / fully-encloses) →
  the aggregate is parented there, staying in the **driver's trace**, with cross-trace
  aggregation links to the *other* inputs;
- **none present** (top-level funnel) → the aggregate starts a **fresh root** trace,
  with links to all inputs.

Either way every downstream span is singular, and the tag-defined flow is the
**graph of traces those links form**.

**We do not unify trace IDs across a cross-trace fan-in.** Keeping everything
downstream of such a funnel "in the same trace" as *every* input would require
emitting each downstream span once per input trace ID, and that multiplies at each
successive funnel — combinatorial. `streamotel` recommends "one trace + links" as the
default and does **not** bless unification in its patterns or tool. A user who
genuinely wants a single unified trace (a small, bounded aggregation where they will
accept the duplication) can compose it from the underlying otel + flow primitives —
possible, but not easy, exactly where a non-scaling choice belongs.

## Span naming

Path-flow spans and tag-flow (aggregate) spans are different creatures — "a request"
vs. "a join of requests" — and a reader should not have to infer which from context.
Use a naming convention (and a flow-type attribute) so the span name states *what kind
of flow node* this is, complementing `link.kind`'s *what kind of edge*. The trace
becomes self-describing.

## What `streamotel` is (and is not)

- **Rename.** `otpsg` → `streamotel`, in a subdir `otel/` (its own module, only so the
  otel dependency stays out of the main module).
- **Delete `metrics.go`, `logging.go`, `instrumented.go`.** Per-op count/duration/error
  meters and start/complete/error logs are unopinionated decisions the user makes
  better themselves, they aren't otel-specific, and they add a type-zoo without
  teaching anything. The module becomes *just tracing*.
- **No wrapping.** Everything is composition of plain otel with flow primitives:
  carry a span as a `FlowKey[trace.Span]` value; re-activate it in an async body with
  `trace.ContextWithSpan`; end it at the true end inside a `FlowFollowUpFn`; make a
  per-op child span with plain `tracer.Start`; attach fan-in links with
  `trace.WithLinks`. `streamotel` is a **doc guide plus runnable examples** that
  illustrate these and mark the decision points.
- **The one tool that earns its keep** lives at the fan-in: given the accumulate
  stream it (a) surfaces the enclosing parent *if the flow left one present* and (b)
  collects the severed contributors as `[]trace.Link`. It makes exactly one decision —
  within vs cross, read off the flow's own seam — and leaves span name, kind, parent
  choice, trace-ID policy, and sampling to the user.

## Decision points (user choices; `streamotel` recommends a lean)

| Decision | Options | Lean |
|---|---|---|
| flow entry | root span vs. child of caller's span | child-of-caller if a span rides the incoming ctx, else root |
| span end | op boundary (`defer End`) vs. flow true end (`FollowUpFn`) | narrowest honest boundary; follow-up only for spans that genuinely span their sub-flow |
| fan-in parent | enclosing driver, or none | the enclosing driver **iff the flow left it present** at the flush; else no parent |
| fan-in trace | one trace + links vs. unify all inputs | **one trace + links** (unify doesn't scale — primitives only) |
| async dispatch | parent-child continuation vs. producer/consumer + link | parent-child in-process; producer/consumer only across a real queue/process boundary |
| wave identity | attribute vs. linked wave-lifetime span | **attribute** |
| driver chain | one hop (followable) vs. transitive links | one hop, chain recovered by traversal |

## Grounding

- Single parent, links for everything else; scatter/gather "do not set a parent that
  does not fully enclose the child":
  [spec overview, Links](https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/overview.md)
- Batch/creation-context, PRODUCER/CONSUMER, "links are the only option in batch as a
  span can have a single parent":
  [messaging semconv](https://opentelemetry.io/docs/specs/semconv/messaging/messaging-spans/)
- "one span with multiple parents is not possible":
  [opentelemetry-go discussion #4923](https://github.com/open-telemetry/opentelemetry-go/discussions/4923)
- Cross-trace sampling of linked traces (the cost of cross-trace links):
  [spec issue #2918](https://github.com/open-telemetry/opentelemetry-specification/issues/2918)

## Open (for the implementation discussion)

- Exact shape of the one fan-in helper against the flow primitives (how it reads the
  present-driver span and collects severed contributors at accumulate).
- Driver-attribution at a fan-in against the F7/drive-separation code (which body is
  the "immediate driver" of a contributed accumulate).
- How a wave-participation sub-flow is delimited in practice (nested `WithFlow` vs. a
  lighter marker) — and whether that delimitation is a pattern or needs a primitive.
