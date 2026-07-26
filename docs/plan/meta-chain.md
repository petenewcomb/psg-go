# Bounded Context Descent and the Meta Chain

**Status: design settled 2026-07-26 (worked turn-by-turn with PN); not
built.** Resolves the TODO.md work item "Meta-chain relay pinning."
Companions: `forest-severability.md` (permit accounts ride meta lifetime, so
this plan bounds their memory too) and
`../decisions/ctxmeta-parent-refcount.md` (the 2026-07-08 fix whose
lifetime-ref contract this plan narrows). Vocabulary: "generation" belongs
exclusively to omnipool's recycle counter; a dispatch link is a **hop**, the
workload is a **relay**, the count is **relay length**.

## The problem

A body context is `ctxpool.WithValue(srcCtx, m)` — it descends from the
dispatch context, so every `Value`/`Done`/`Deadline` lookup traverses the
source chain for the body's lifetime. Because the intermediate wrappers are
pooled (recycled explicitly, not GC-collected), each child meta holds a
lifetime ref on its parent meta to keep that chain pool-valid — the
2026-07-08 fix for the `borrowSrcCtx` use-after-free. The consequence: in a
fire-and-forget relay, the live hop's meta transitively pins every ancestor
meta and pooled child back to the origin — O(relay length) — all freed in
one `unrefMeta` cascade when the last hop completes. The conservation hook
balances only at that moment and never sees the transient growth. Under the
lazy forest, contended relays' accounts ride the same curve through the
meta-held account ref.

## The construction rule (context layer)

The pooled intermediates contribute exactly one thing to the context chain:
their meta stamp, read nearest-first. User values, `Done`, and `Deadline`
pass through untouched, and all framework state that must flow hop-to-hop
(parentWaves, riders, the ancestry itself) travels in the meta. So:

**A pooled child is always built on the nearest ancestor that is not our
own wrapper — its user base.** At borrow time the framework distinguishes
two cases by pointer comparison (`srcCtx == srcMeta.selfCtx`):

- **Clean pass-through** (the user handed a body context straight to
  Submit): the new child is built on the source child's own base, which by
  induction is a pure user context. The body's delegation chain contains
  **no pooled object at all**, and context safety needs **zero** meta
  parent refs.
- **Interleaved derivation** (the user derived their own context from a
  body context and submitted that): the derived object is opaque and
  internally references our wrapper, so the new child builds on it and the
  borrow **pins the base's stamped meta** — a lifetime ref, exactly as
  today, and necessarily transitive: in nested interleavings the delegation
  chain passes through every ancestor's user segment and the wrapper below
  it, so validity requires the whole pin chain. Early release of these pins
  was examined and rejected as unsound.

Structural consequence: **pooled children never stack** — the ctxpool tree
flattens to depth one, every pooled child's parent is a user context
(debug-asserted). Semantics are unchanged in both cases: lookups reach
exactly the user content they reach today, minus transparent wrappers.

**Flows.** `WithFlow` scope wrappers are framework wrappers and are skipped
the same way; rider propagation never rode the context chain (meta-side
verbatim copy on sync derivation, dispatch-time capture with real refs at
async boundaries — the body's rider ref release firing a flush is
unchanged). Flush/fire continuation borrows resolve their base through the
stashed drive context of the rolling last accumulate — semantically what
delegation reached anyway, and consistent with the settled flow decisions
(fan-in severs path riders before user code; fire carries its own
enclosing). `flowBoundaryAboveWave` is a synchronous walk and is
unaffected. The deferred driver-link rider pin stays deferred, unchanged.

## Exit-splice (meta layer)

The construction rule makes early ref release *permissible*; the relay is
fixed only by choosing a release point. The policy: **splice-to-nearest-
keeper at the owner's exit**. After the account-word CAS, the exiting meta
examines its parent; if skippable, it walks the dead prefix greedily to the
nearest keeper (or nil), takes one ref on it, swings its own parent edge,
and releases the old parent — the iterative `unrefMeta` cascade frees the
entire segment.

- **The skip test is shared with prefix materialization**, read from the
  account word: skip `never-opened` (exited, no account, can never lend)
  and `account-closed` (exited and settled); stop at `unopened` (a live
  body — it may yet open an account; splicing past it would recreate the
  retroactive-ancestor gap) or an **open account** (still a credit
  discovery point, exited-with-claimants included). One classification for
  walker and splicer.
- **Safety of the search:** the exiting owner's ref pins its parent; every
  dead meta in the prefix holds the ref its own splice handoff left on its
  target — the prefix is transitively pinned, and dead metas' edges are
  **final** (last written by their owners at exit, no writers remain). The
  search takes no refs mid-walk; one `AddRef` on the keeper, one release.
  A keeper flipping dead right after the swing is the standard advisory-
  liveness race: the successor's greedy splice eats the overhang.
- **Single writer per edge, ever:** creation (pre-publication) and the
  owner's one exit-splice. No observer severs, no upgrade protocol. Where
  the account splice shelters under `p.mu`, the meta splice is lock-free —
  single-writer plus edge-finality plus transitive pinning do the mutex's
  work.
- **Async walkers pay the price:** prefix materialization (and the future
  driver-link tracing) cross the chain outside any synchronous extent and
  can have a pin pulled mid-walk, so they step with the validated protocol:
  read the edge; `TryAddRef` the target (the meta's embedded
  `omnipool.RefCounter` provides it); re-read the edge — unchanged means
  the ref landed on the right incarnation, changed or nil means retry
  higher or stop. Type-stability makes a doomed read discardable; edges
  move only up the ancestry, so retries progress. Sync walkers (the four
  `syncParent` walks) are pinned by open owner scopes and are untouched.

## Accounting

| Shape | Metas + pooled children | Accounts | Context chain |
|---|---|---|---|
| Clean relay, happy | 2 hops' worth (live + one dead, eaten at next exit-splice) | none | depth 1 to user ctx |
| Clean relay, contended | 2 | O(1) | depth 1 |
| Structured tree | as today (children exit first; splice never fires) | per contention | unchanged |
| Fan-out fire-and-forget | ≤1 dead dispatcher meta per live child-set (last child's splice frees it) | rides metas | depth 1 |
| Long-lived body under dead ancestors | dead prefix ≤ its ancestry depth at creation, cleaned at its exit | — | — |
| Interleaved relay | O(relay length), lifetime pins (required) | rides metas | user's own chain, already O(relay length) |

The governing law: **the framework never adds a per-hop growth law the user
didn't already create with their own context objects.** Clean pass-through:
they add nothing per hop, we add nothing. Interleaved: their own context
chain is O(relay length) and GC-alive through the live body regardless;
ours rides it at a constant factor — Go-semantics parity.

## Verification

Debug asserts: a pooled child's parent is never our own wrapper; meta
parent edges change only through the expected-value store discipline (a
failure means the single-writer premise broke).

Conservation: `ctxMetaAllocHook` (and the ctxpool counterpart) gain a
**high-water mark** — the pinning was always invisible to the end-state
balance, so peak-live is the observable. Under a relay sim workload, peak
simultaneous metas stays bounded by a constant times live bodies.

Regression, both directions: (a) the `borrowSrcCtx` UAF class —
`TestBorrowBodyContext_ParentPinnedAcrossSourceRelease` extended with the
new clean-case assertion, now stronger: a body context built on its user
base stays fully correct **after its source meta recycles**; the
interleaved case still pins the base's stamped meta. (b) Semantics parity:
black-box tests that submitter cancellation, deadline, and values reach
bodies identically in clean and interleaved shapes.

Sim: the fire-and-forget relay workload asserting the high-water bound —
the same workload the forest spec's chain-boundedness checker needs, now
serving both layers — with contention onset at random hops so prefix
materialization genuinely races exit-splices, in large `-race` batches;
flow-side rider conservation hooks under the same workload.

Build-time audit list: no cancel/deadline-bearing framework wrapper may be
skipped by user-base computation (flush/fire stash sites first); every
async boundary takes rider refs at capture; ctxpool children built on
arbitrary user parents in every borrow path.

## Rejected alternatives

- **Severing context descent outright / snapshotting values** at async
  boundaries: Go's context API cannot enumerate keys; arbitrary-value flow
  into bodies is longstanding observable behavior (OTel spans included).
- **Early release of interleaved pins** (e.g., at exit): unsound — nested
  interleaved delegation needs the transitive wrapper chain valid for any
  live descendant context.
- **Ancestry-dead markers with observer severs** on metas: multi-writer
  edges, an upgrade protocol, and no benefit over exit-splice, which
  bounds the same residue with a single writer.
- **Lifetime refs with pointer-only severing** (the severability session's
  interim position): bounds walk lengths, not memory — correct while the
  context anchor forced lifetime refs, superseded once the construction
  rule removed it.
- **One-hop (non-greedy) exit-splice**: leaves dead overhang whose cleanup
  depends on tidy alternation; the greedy loop costs the same handoff and
  is robust to bursts.

## Relation to existing records

The 2026-07-08 decision record's lifetime-ref contract narrows: the parent
ref's context-safety role survives only for interleaved bases; its
walkability role is served by the step protocol. `permitRoot` isolation is
untouched. The forest-severability plan's scoping note ("account memory
rides the meta chain") resolves in the good direction with zero changes to
the forest design.
