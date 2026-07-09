# Driver contexts: who drove a body, and how its context stays readable

> Decision record (2026-07-09, design session with PN). **Status: converged, not
> yet implemented.** Supersedes the "driver-link rider pin" follow-up sketched in
> `ctxmeta-parent-refcount.md` — the pin as sketched there mostly dissolves under
> scrutiny, and what replaces it is smaller, differently placed, and states two
> load-bearing contracts. This is the prerequisite for driver links in
> `otel-tracing-on-flows.md`.

## The question

Driver links need a body to read its *driver's* flow values at body-run time.
The parent refcount made the driver's **meta** reachable and alive across async
boundaries, but not its **rider chain**: a child refs only the riders it
inherited, and several paths run under riders that differ from their driver's.
The naive answer — pin the rider head wherever a meta is pinned — turns out to
target the wrong thing on every path where it would fire. Working through who
the driver actually *is* on each path reshaped the design.

## Two contracts, stated first

**A `ctxMeta` is immutable for its ref'd lifetime — just as contexts themselves
are. In all cases.** Mutation is legal only in single-party custody: before the
meta is first reachable by a second party, and again when `refs == 1` and the
sole holder is the mutator (custody has returned to one party; nobody else can
observe the change). The skim handler's in-place rider override was the one
violator (below); the fire's adopt-in-place case (below) is the one legitimate
use of returned custody.

**`executionEnvironment` is outside that invariant, under its own custody
contract.** exEnv is not meta data — it is dynamically-scoped *executor* state
that rides the meta because ctx is the only channel that reaches from a body
extent into a `Submit` call. It conflates three concerns, all of them "who/how
is executing right now":

| concern | state | why it mutates |
|---|---|---|
| dispatch routing | `topLevelExEnv.workQueue` → the wave's intake (blocking, backpressure) vs `workerExEnv` → `defaultPool.Post` (executor self-deadlock avoidance) | determined by the current executor, not the flow |
| extent bookkeeping | `groupStack`, `queueFnStack` | pushed/popped around extents — in-body submits inherit the extent's group; the skim driver publishes its queueFn so driven-extent submits post into its own select loop |
| dispatch serialization | the top-level `mu` | a shared ctx (a `WithFlow` scope, a reused ambient meta) may be submitted from concurrently |

The contract: the exEnv slot belongs to the extent currently executing under
the meta — stamped at extent entry (dispatch-time nil, worker `ee` at run,
re-stamped on fire adoption), mutated only from within the extent, and **never
read across an async boundary**: a parent's exEnv is another execution's live
mutable state (a `workerExEnv`'s stacks are serving whatever that worker runs
now). There is no good structural enforcement — the type system cannot scope a
field to "the current extent," and hoisting exEnv off the meta just reinvents
the slot, since submit internals have nothing but ctx to find the current
extent's group/queueFn/route — so this is a documented rule, load-bearing for
reviewers of any future accessor that walks `parent`.

## Who the driver is, per path

| body | driver | what makes its context readable |
|---|---|---|
| task / accumulate | the dispatching body | nothing new: the body's inherited riders *are* the driver's chain, captured and ref'd at borrow |
| skim handler | the drive (skim) flow | the per-item child meta (below): the drive chain stays intact at `parent` |
| funnel flush | **the last accumulate** | the instance's rolling node-only pin (below) |
| follow-up fire | **the last carrier** | the fire *is* its continuation: it runs under the carrier's own context (below) |
| executor-pumped anything | — | the scheduler/sweep stash is framework plumbing, not a driver; **no link**, ever — the wave-ID attribute covers execution substrate |

The flush answer is what killed the original pin shape: nothing other than the
last accumulate's context can be correct, because the last accumulate is the
one whose returned deadline (or whose being final before close) *made the
flush due* — the scheduler is just the timer executing that instruction. It
also unifies the three flush paths (inline past-deadline, deadline drain,
end-of-work sweep), which otherwise attribute three different drivers.

## Skim handlers get a per-item child context

Today `skimWork.Execute` runs the handler by overwriting the **drive meta's**
riders in place with the item's chain (skimmer.go) — a leftover from before the
parent refcount, when a child meta couldn't safely outlive anything. It has to
change, and not only for driver links:

- **It violates the immutability invariant** on a meta whose ref'd lifetime is
  the whole drive.
- **It is a live misdelivery bug**: the override happens only when the item has
  riders and is never restored, so within one drive a *rider-free* item
  processed after a rider-carrying one sees the previous item's flow values.
  The sim can't catch this (it asserts expected keys present, never foreign
  keys absent). The fix lands behind a failing regression test pinning exactly
  this sequence.
- **Handler-dispatched work refs the handler's ctx** and keeps it alive until
  the work completes, reading `parent.riders` lazily — so the handler's meta
  must be a stable per-item object, not a drive-wide slot being re-stamped
  under concurrent readers.

Shape: the handler runs under a borrowed child meta per item — `parent` = the
drive meta (synchronous derivation, **not** a permitRoot: the handler runs on
the drive goroutine and must stay visible to `vetNotNestedInSkim` and the
permit walk), riders = the item's chain (the F7 nearest-wins continuation,
now structural instead of mutational; a rider-free item inherits the drive's
riders, per item, correctly), exEnv shared with the drive like any derived skim
meta. The driver's riders stay intact at `parent.riders`, alive for the
handler's synchronous extent by the drive scope's own refs — no pin needed.

Cost: a pooled meta borrow + ctxpool child + refcount pair per skimmed item on
the drain path — allocation-free steady-state. **Rejected**: one child meta per
*drive* with riders re-stamped per item — unsound the moment anything reads
`parent.riders` lazily (misdelivery and a plain data race), which is the whole
point of the feature.

## Flush: a rolling node-only driver pin on the instance

Each accumulate re-points the instance's driver pin at its own context:
`refMeta` on the accumulate body's meta plus `nodeRef` on that meta's rider
head, releasing the previous pair — taken at the accumulate, a synchronous safe
point; released after the flush body runs. Four uncontended atomics per
accumulate, no allocation.

**Node-only, deliberately**: no `flowRefRiders`. Instance refs would make the
driver's own follow-ups wait on the aggregate's flush — observability changing
fire timing. Accepted consequence: the driver's follow-ups may already have
fired when the flush reads its values; the values live on the pinned nodes and
stay readable regardless. (Flow *lifetime* crosses the fan-in via the tag
union, a separate, already-landed mechanism.)

The flush body's ctx ancestry (cancellation) is unchanged — scheduler-rooted;
the driver context is reachable through the pin, not through `parent`.

## Fire: the last carrier's continuation

The fire runs under **the last carrier's context** — the context whose release
dropped the final carrier ref — occupying the carrier's position in the tree
(same parent; a continuation, not a child), with the riders adjusted: the
carrier's chain **minus the fired instance's binding** (no re-fire, per F6's
peel), rebuilt-prefix mechanics when the fired node is interior, a repoint when
it is the head.

This is a deliberate semantic refinement over the current
enclosing-at-registration set: the fire sees riders the last carrier acquired
*after* registration (e.g. a nested `WithFlow`'s values when the last carrier
was dispatched from inside one). That is what "continuation of the last
carrier" means, and it is consistent with the already-settled R6b stance that
the last-standing branch is the principled true end of a coalesced flow.

Mechanics settled:

- **Pin at the count→0 dispatch** (a synchronous safe point: the releasing
  meta's owner ref is still held there — rider release precedes `unrefMeta` in
  `releaseBodyContext`): the fire work takes `refMeta` on the carrier's meta.
  The fire therefore pins the carrier — and, via the cascade, its whole parent
  chain — until the fire completes: the driver chain is walkable *during* the
  fire, which is intended.
- **Sole-ref test is dynamic, at fire-run time**: `refs == 1` (the carrier's
  owner release has completed; no async children survive) ⇒ **adopt and mutate
  in place** — custody has returned to a single party, so the immutability
  invariant is satisfied, not excepted. Otherwise ⇒ **copy-on-write**: a fresh
  pooled sibling — same parent (`refMeta`'d), identity stamps copied, remaining
  riders installed under their own `nodeRef`s — and the carrier's meta is
  released normally. The inline scope-exit fire always takes the COW path (the
  scope's own release runs after the fires).
- **exEnv is never carried**: an adopted meta gets the fire worker's `ee`
  re-stamped (legal under returned custody); a COW sibling stamps fresh and
  never copies the carrier's — per the custody contract.
- The pre-refcount reason fires rooted at the scheduler stash — "the recycled
  per-item ctx was a ctxpool-lifetime trap" (CP-F6 4a) — no longer binds: the
  pin is exactly what keeps the carrier's ctx un-recycled.

**Open implementation detail, flagged not settled**: cancellation ancestry.
Adopting the carrier's context makes the fire cancelable by the carrier's
ctx chain, where today it rides the scheduler's. A fire is end-of-flow
work with its own error routing (the wave errSink); whether it should be
shielded from a long-gone submitter's cancellation is to be resolved at
implementation, with a test either way.

## What the otel layer gets

The lifetime machinery above is this record's scope. The accessor surface —
how `streamotel`'s fan-in helper reads the present driver span, and what the
public read primitive looks like — stays with the `streamotel` implementation
discussion (`otel-tracing-on-flows.md`, "Open"). What is guaranteed here: on
every path with a meaningful driver, that driver's meta and rider chain are
alive and immutable for exactly the extent that needs to read them, and on the
paths without one (framework pumps), absence is honest and deliberate.

## Rejected alternatives

- **Pin the rider head at the Execute stash** (the original sketch): pins the
  scheduler's chain — the one "driver" that should never be linked.
- **Save the skim drive's displaced rider head in a side slot**: a patch on top
  of the mutation hack; the child meta removes the hack.
- **Per-drive skim handler meta with per-item rider re-stamp**: unsound under
  lazy `parent.riders` reads (misdelivery + data race).
- **Instance refs in the driver pin**: observability must not delay the
  driver's fires.
- **Driver links for executor-pumped flushes/fires** (to whatever the worker
  base ctx carried): the pump is machinery; a link to it is noise.
- **Hoisting exEnv off the meta**: ctx is the only channel from a body extent
  into submit internals; a separate carrier would just be this slot again.
