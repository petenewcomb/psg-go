# Permit-Forest Severability and Lazy Materialization

**Status: design settled 2026-07-25 (worked turn-by-turn with PN, 2026-07-23
through 07-25); implementation not started. Vocabulary updated 2026-07-26.**
This plan governs the permit forest — the accounts and their parent chains —
under the reservation model recorded in `WORKING_NOTES.md` (the ratified
credit-model block, the teardown settlement, and the absentia/ownership
consolidation). It supersedes two earlier decisions inside those blocks: the
teardown item-5 choice of *lock-free path compression*, and the
forest-restructure block's *one forest object per in-flight admission (cost
accepted)*. Companion plan: `conservation-rework.md`; vocabulary:
`../glossary.md`.

Throughout, an **account** is the body-1:1 forest object — today's
`permits.Cache` (internal/permits/permits.go:399), whose per-(wave,pool) role
the restructure re-keys per admission, and whose stealing-era name dies with
stealing. An account is **open** until settlement **closes** it (a terminal,
single-store flag). "Meta" means `ctxMeta`, and `p.mu` means the pool's
structure/ledger mutex.

## The problem this solves

The body-1:1 restructure gave every admission its own account, chained by
submission ancestry. That exposed a growth law the old wave-level conflation
had quietly bounded: in a fire-and-forget relay — each body completes by
dispatching its successor and exiting — the successor's account pins its
dead dispatcher's account, one per hop, unbounded. The first settled
fix (teardown item 5, option 1) was lock-free path compression: a terminal
closed flag, always-CAS parent pointers, and splice-past-the-closed at
account creation and at each claim.

Working that choice into a full protocol exposed three costs, each real:

1. **The boundedness gap.** One halving step per claim bounds closed residue
   by *peak once-live* ancestry, not current live ancestry: an owner's edge
   freezes at its last claim, and ancestors that die afterward become
   residue no actor ever removes. Closing the gap (loop-until-live splicing)
   was agreed, but the analysis showed the residue class is intrinsic to
   compression-at-claims.
2. **The hot-path close.** The closed flag must be stored under `p.mu` (both
   the settlement site and the claimants→0 site), and with an account per
   admission that put one pool-wide mutex acquisition on **every**
   completion — a serialization point of exactly the kind the atomic
   `counts` hot path exists to avoid.
3. **The machinery.** Safe lock-free traversal near concurrent splicing
   demanded a family of guarded protocols: ref-before-CAS handoffs,
   generation-validated `TryAddRef` crossings, optimistic re-validation for
   mutex-side walks racing lock-free compressors.

The settled resolution dissolves all three at once by attacking the premise:
**most bodies never need an account at all.**

## The lazy forest

An account exists exactly when the body's state must be **discoverable by
others** — as a lender, a standing registrant, or a link in someone else's
chain. Private state stays off the forest: the happy reservation
(acquire→claim with no wait, debit-first on the pool's atomic counts, the
reservation as handle-local state) and running tenure (`pool.inUse` remains
a pool-level aggregate, per the teardown item-7 quiescence identity).

**Two independent triggers open accounts:**

- **T1 — a park while holding units.** Park-as-reserved converts the
  parker's tenure to account-homed, lendable margin (ownership ladder:
  parked = lendable), and a pumping park anchors its wave-attached episode's
  loan accounting on the account. All unit-holding park kinds are this one
  trigger: pump parks (nested drain, cross-wave submit), gate parks
  mid-acquire (ranks already reserved while waiting on a higher rank),
  governor parks. Permit-free parks (idle queue workers, teardown joins,
  timers, spawn-guaranteed producer parks) hold nothing lendable and open
  nothing — matching the pinned-invariant verification pass's
  "lending-irrelevant" verdict.
- **T2 — postponement.** A registered demand's standing reservation is a
  registration attachment on its wave, backed by account-homed reserved; the
  queue relay attending the demand needs a discoverable home.

**The blocking claim is derived, not independent.** A claim can block only
while loans are outstanding against its reservation; loans can stand only
against discoverable margin; a never-parked, never-postponed body's
reservation is private. So a blocking claim presupposes a prior opening (a
wake-of-park's claim, a postponed retry's claim), and its self→root
claimants walk runs over a chain the original opening already built.

**Non-triggers:** happy acquire/claim/release, dispatch (ancestry rides the
meta chain — see below), funnel accumulate calls (ordinary admissions, per
the funnel-parentage resolution), never-gated works, borrowing (a borrower
is by definition parked or postponed; the receivable lives on the lender's
account).

Two consequences worth stating as design facts: accounts open only on an
already-blocking path (both triggers fire on the way into a park or
postponement — the cost hides behind an imminent block); and a fully happy
lineage — including a fully happy relay — opens no accounts,
takes no `p.mu`, and closes nothing. The relay growth law largely evaporates
rather than being compressed away.

## Prefix materialization

Lazy opening introduces one genuine gap: the **retroactive ancestor**. If A
dispatches C, both account-less, and C parks first, C's account must not
link past the account-less A — A may park later with lendable margin that
C's blocked descendants could never discover, breaking the pinned liveness
invariant (every wait whose liveness requires lending-across is a submission
edge, episode, or registration within reach). And there is no repair path:
accounts enter the forest only as leaves.

**The rule:** a body's first structural need opens its own account *and* an
account for every still-live ancestor on its dispatch ancestry that lacks
one, linked in true order. The meta chain supplies that ancestry, already
captured and pinned — the parent link is refcounted, child holds parent for
its lifetime (ctxmeta.go:43-58; `docs/decisions/ctxmeta-parent-refcount.md`),
so the walk needs no additional pinning. Ancestors that already exited are
skipped forever — correctly, since an exited account-less body can never
become a lender. The invariant is then preserved exactly: any ancestor that
could ever lend to me was live at my opening and is on my chain.

Rejected: *dispatchers always open* (kills the gap but forfeits the
happy-relay win — an account and a closing exit per hop again).

## The account word

Each admission's meta carries an **account word** with a single-transition
discipline: exactly one successful CAS ever, `unopened → account` or
`unopened → never-opened`. The word never changes again; readers treat a
closed account's word and a never-opened marker identically (skip).

- **Opening** (owner or descendant, on a contended path): walk up the meta
  chain classifying account words — open account: stop, attachment point;
  closed account or never-opened: skip upward; unopened: collect. Then
  install top-down: Get an account, plain-store its parent edge
  (pre-publication), CAS the word. A lost CAS resolves by **adoption**:
  another descendant installed first → discard ours unpublished, adopt
  theirs; the owner marked never-opened → skip, attachment point unchanged.
  The same rule covers a descendant racing the owner's own opening — no
  special case.
- **Exit:** every body's exit CASes its account word
  `unopened → never-opened` before completing. This is the serialization
  point with foreign opening: if the marker wins, racing openers see it and
  skip; if an install won, the exit's CAS fails and the owner inherits the
  anchor account — its exit settles it (empty ledger: close under `p.mu`,
  done). **This one uncontended CAS per exit is the design's entire
  hot-path cost.**
- **Ref wiring:** omnipool's Get arms refs=1; that armed ref is handed
  through the account word to the **meta**, which holds it for the meta's
  lifetime and releases it in the meta's recycle path. Account-word readers
  are safe with plain pointers: they reach the word only through the
  lifetime-ref-pinned meta chain, and the meta's account ref pins the
  account. No generation-counted handles anywhere in this design.

## Synchronization: the mutex owns the structure

**`p.mu` owns all forest structure**: edge creation into the published
forest, splicing, severing, closing, and every walk that traverses other
bodies' accounts (the money walks: release routing by reclaim priority, the
blocking claim's claimants increment/decrement, delivery to claimants,
credit discovery and escalation). The complete lock-free surface is:

| operation | discipline |
|---|---|
| happy-claim parent check | one atomic load; parent open → done |
| account-word CAS | single-transition, adoption on failure |
| closed-flag read | acquire load |
| refcount ops | omnipool `RefCounter` |

Two properties make the mutex regime simple where the lock-free design was
hard:

- **Reachable-implies-pinned.** Forest edges are removed only under `p.mu`,
  so any account a mutex holder reaches via an edge is held by that edge.
  No optimistic traversal, no re-validation.
- **Frozen liveness.** Closing happens only inside `p.mu` sections, so a
  mutex holder never sees an account close mid-walk; every account it
  observes open stays allocated and edge-stable for the critical section.

A claim that finds its parent account closed takes `p.mu` and splices there
— once per closure per child, only on materialized (already contended)
lineages. Waking is a claim, so every park/wake cycle re-splices. The parent
edge's writers are: creator (pre-publication) and mutex holders. Claim
serialization — headship admits one evaluator before Starting
(internal/permits/permits.go:93, :316), program order after — remains
documented as the reason the happy path's unlocked load is single-writer
coherent.

## Severing: `parent == nil` encodes ancestry-closed

The account's flag stays **two-state** (open/closed, closing terminal). A
nil parent means "nothing live above me": for an open account, it is a
root; for a closed account, its whole ancestry is closed
(**ancestry-closed**). No third flag state.

Any mutex-held walk that runs to root through closed accounts severs on the
way: nil each closed intermediate's parent (a monotone frozen-value → nil
transition, releasing that account's ref on the next), leaving each closed
intermediate ref'd only by its meta. The first open account below the
segment keeps its one-hop edge into the segment head — severing an *open*
account's parent would be a foreign write to a claim-owned word and buys
nothing (its walk is one closed hop then nil). The segment head stays
additionally ref'd by its remaining children until each splices away at its
own next claim.

**Why money never routes through severed territory, in one line: only
closed accounts are ever skipped, and a closed account's ledger is zero,
terminally.** Corollaries: an account holding a claimant's increment has
claimants>0, cannot close, and so can never leave that claimant's chain —
the un-freeze decrement revisits exactly the incremented set; likewise
lent>0 blocks closing, so a borrower's release walk always finds its
lender.

## Memory and refs

An account has exactly two kinds of refs:

1. **The meta's lifetime ref** (the armed ref, via the account word) —
   released when the meta recycles.
2. **Child parent-edge refs** — created at install/splice, released at
   splice/sever, all under `p.mu`.

Recycle at refs==0 is purely structural — settlement already routed every
unit before closing, so destroy touches no money (unlike today's destroy,
which releases the account's Resource draw,
internal/permits/permits.go:429-470; that work moves to settlement). The
recycle assert `account is closed with zero ledger` holds by construction:
the meta ref outlives every closing site, and claimants>0 pins the account
through the claimant's own edge chain.

**Scoping decision (explicit):** meta-to-meta chain lifetime is *out of
scope*. Child metas hold parent metas for their lifetime (the 2026-07-08
parent-refcount contract, forced by ctxpool context descent), so a
fire-and-forget relay pins O(relay length) metas until its final cascade —
and contended relays' accounts ride that curve through ref kind 1. This
design is strictly no worse than the eager forest on that axis and strictly
simpler than any early-release variant; the meta-chain fix is its own work
item (TODO.md, "Meta-chain relay pinning") and improves the account curve
for free when it lands.

## Settlement integration

Departure and exit-settlement (teardown items 1-4) are unchanged: one
`p.mu` section routes unlent reserved by reclaim priority, abandons
receivables, and closes the account as its last act. The second closing
site is the claim-resolution decrement that takes an exited account's
claimants to zero. A body with nothing but a `never-opened` marker settles
nothing — its exit is the one CAS alone. Attachment anchors, registration
teardown, and the Done-implies-queues-empty argument (teardown item 6) are
untouched.

## Verification obligations

Debug asserts:

- account-word single-transition (a second successful CAS is a bug);
- close-at-most-once, and ledger-is-zero at the closing store;
- closed-at-recycle (nonzero words on a pooled account's return);
- Done-implies-queues-empty and no-attachments-at-Reset (teardown item 7,
  unchanged).

Sim checkers (the real gate, per DEVELOPMENT.md's `-race` batch rule):

- the per-pool quiescence ledger: reserved/lent/claimants/loans-outstanding
  all zero at episode end;
- **chain-boundedness under a relay workload**: live chain
  length stays O(live ancestry) — under this design the bound is enforced by
  splice-at-claim plus sever-at-root rather than lock-free halving, and the
  relay case additionally asserts near-zero account opening;
- **laziness**: a fully happy workload opens zero accounts
  (alloc-floor-style check, run without `-race` per the established flow
  alloc-floor convention).

## Rejected alternatives

- **Walk-time-only splicing under `p.mu`** (the pre-catch design): the
  all-happy path never takes the mutex, so nothing ever walks, and relays
  leak — the original catch.
- **Child lists** (eager exit-relink) and **forced periodic walks**: walk
  back the restructure or band-aid it with a controller.
- **Eager account per admission** ("cost accepted"): revoked — it is what
  made closing a hot-path mutex touch and the forest a relay leak.
- **Lock-free path compression as the primary mechanism** (teardown item-5
  option 1, and its elaborations: one-step halving, loop-until-live,
  ref-before-CAS handoffs, generation-validated crossings, optimistic
  mutex-walk re-validation): superseded whole. Lazy opening removes the hot
  path from the forest, which removes the reason structure edits had to be
  lock-free, which deletes the machinery.
- **Dispatchers always open accounts**: closes the retroactive-ancestor gap
  but forfeits the happy-relay win.
- **Three-state flag (open / closed / ancestry-closed)**: subsumed by the
  nil-parent encoding — same information, no third state, no upgrade
  protocol.
- **Generation-counted handles** (for account words or meta-chain steps):
  made unnecessary by lifetime refs — the meta chain pins itself, the meta
  pins its account, and nothing is ever freed under a reader.
- **Early meta-ref release / meta-layer severing** (with TryAddRef guarded
  walks): solving the relay meta-pinning inside this spec; moved out of
  scope to its own work item instead.

## Open points

- **Account-word granularity.** Forests are per-pool; a multi-rank body
  holds reserved units in several pools, so "the account word" is
  per-(admission, pool). Proposed: a small per-meta account list
  generalizing the existing per-wave cache list pattern (`createCache`
  under `cachesMu`, wavepermits.go:97-112), with the exit's never-opened
  marker closing the list head in one CAS. Layout to be confirmed at build
  time.
- **Placement of the happy-claim parent load** in the built claim path
  (relative to the existing Starting hook, accepted.go), and of the opening
  call on the two trigger paths.
- The queue-level **token buffer** (reservation item 6) remains the last
  unsettled item of the reservation agenda, independent of this plan.
