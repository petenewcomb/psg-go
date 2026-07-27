# Directed Delivery

**Status: design settled 2026-07-26 with PN; not built.** This is phase 2 of
the reservation model. Phase 1 (the reservation ledger, the lazy forest, the
missed-notification balance) is recorded in the 2026-07-22/23 WORKING_NOTES
settlements, `forest-severability.md`, and `conservation-rework.md`; the
delivery model it plugs into is `../notification-conservation.md`. This
document supersedes several phase-1 details, listed under "What this
retires."

## Vocabulary

Inherited terms, defined elsewhere: **demand** (the registered identity of
one waiting acquisition; `../glossary.md`), **attendant** (a registered
demand's current wake target; `../notification-conservation.md`
§Registration semantics), **account** (the forest's per-admission ledger
object; `forest-severability.md`), **park-as-reserved**, **episode**, and
**registration attachment** (a parked body's units held as reserved, and
the two structures that make a park's or a postponed admission's units
discoverable to its wave's subtree; the 2026-07-22/23 reservation
settlements, to be consolidated at build).

Terms this design introduces or narrows:

- **reservation** — units set aside for a specific party, recorded as
  `reserved` at that party's account. Every unit not in-use and not in the
  pot lives in some reservation.
- **holder** — the party a reservation belongs to: a parked body or a
  registered demand.
- **claimant** — a party blocked at the claim gate (the reserved → in-use
  transition immediately before a body runs): a registered admission whose
  units were drained before it could claim, or a returning parked body
  whose units were lent and not yet repaid.
- **shortfall** — the gap between a reservation's balance and its holder's
  weight.
- **delivery** — a release (or capacity raise) placing units into
  reservations under `p.mu`.
- **spill** — delivery continuing past satisfied reservations into
  successive shortfalls, in service order.
- **service order** — the one order in which waiting parties are satisfied:
  claimant queue front to tail, then demand queue front to tail.
- **drain** — units leaving a reservation for another party's benefit;
  covers both borrowing and a claimant's recall. (Distinct from the
  glossary's wave-drain sense; context disambiguates.)
- **beneficiary** — the party a drain serves; drain ordering is defined
  relative to it.
- **pot** — the `Resource`'s free capacity: what remains checked in after
  every reservation and in-use unit is accounted.
- **anchor** — the published boundary of the demand queue's front-loaded
  shape: the head-most unsatisfied demand, or nil. Successor of the
  phase-1 anchor (`internal/permits/permits.go:204`), keeping its
  barrier-gate role.

## The opportunity

Under phase 1 as settled, a release that reaches the pool tier lands in free
capacity (`Resource.Release`) and the standing head demand's attendant gets a
wake; the head's retry then re-runs the gate. For a demand whose weight
exceeds what any single release frees, capacity assembles only through
repeated wake–retry–miss cycles — one futile wake per trickled unit — and
what protects the accumulating capacity between cycles is the head-of-line
barrier, an implicit claim on the pool's free capacity that appears nowhere
in the ledger.

Directed delivery moves the units themselves at release time: under `p.mu`,
a released unit goes into a waiting reservation, and the wake fires only
when a reservation completes. The gains, in order of weight:

1. **No futile wakes.** A demand's attendant fires exactly once, when its
   reservation is whole; the woken retry cannot miss on the reserve side
   (unless a borrow intervened — see the drain discipline). This deletes the
   wake–retry–miss loop for accumulating weighted demands and is a direct
   tail-latency win for gated admissions under contention.
2. **The granted-units fact becomes durable ledger state.** "You have been
   granted capacity" lives in the demand's own reservation, re-derivable by
   any later retry, instead of existing only in a transient wake. (The
   missed-notification balance closes the same fragility at the delivery
   end; this removes it at the source.)
3. **Explicit ownership.** The head's implicit claim on free capacity was
   the last piece of implicit ownership in the reservation model. As an
   ordinary reservation it is visible to the same lending, recall, and
   settlement rules as everything else.

Cost bound: when no shortfall exists anywhere, nothing changes — the release
fast path stays lock-free behind the same bare anchor load that gates
acquisition today (`internal/permits/permits.go:731`), and the money-moving
step runs only inside the `p.mu` section a contended release already takes.

## The delivery order

A release delivers, in order:

1. **Overdraft excess home** (unchanged from phase 1).
2. **The claimant queue** — bodies blocked at the claim gate waiting for
   units. Repayment is **always pool-scoped**: the release fills the
   front-most claimant shortfall, regardless of whose chain the releaser is
   on. Permits are fungible and the ledger tracks no pairwise debt, so
   "repay the releaser's own chain" was a routing heuristic, not a
   correctness rule; the only parties whose liveness depends on repayment
   are claimants, and every receivable ends either in a claim (served here)
   or in departure settlement (abandoned, `lent` zeroed under `Free`'s
   `p.mu` section). This *strengthens* the claim gate's liveness contract:
   the front claimant is repaid by the next release of anyone in the pool,
   not just its own borrowers' completions.
3. **The demand queue** — registered demands, in arrival order (see spill).
4. **The pot** — the `Resource`'s free capacity, receiving only the residue.

Tiers 1–2 return owed units; tiers 3–4 allocate free ones. Claimants before
demands is load-bearing, not a preference: a claim settles when its units
come home, and if a release could be intercepted by a demand, a claimant
would park indefinitely behind fresh arrivals.

A consequence of tiers 2–3: **a release touches the `Resource` only for the
residue.** Units delivered into reservations are ledger transfers under
`p.mu`; the `Resource`'s checked-out count keeps covering them, exactly as
it covered `held` capacity in the old forest.

## Spill and the anchor

Within each queue, delivery fills the front-most unsatisfied reservation
first and **spills down the line**: it continues past satisfied entries into
successive shortfalls until the units run out. Invariant: **the pot is
nonempty only when no shortfall exists in either queue.**

The demand queue at rest is therefore front-loaded: a satisfied prefix, at
most one partially-filled entry, an empty suffix. The **anchor** publishes
that boundary — the head-most unsatisfied demand, or nil — and serves three
roles at once:

- **spill target**: delivery starts at the anchor and pushes it tailward as
  entries complete;
- **drain entry point**: borrows drain the deepest non-empty reservation
  first, which at rest is the anchor; draining pushes it headward, and when
  every entry is satisfied a drain starts at the tail, whose reopened
  shortfall re-establishes the anchor;
- **barrier gate**: the acquisition fast path stays lock-free on a bare
  anchor load, as today — but the honest gate condition becomes "an
  unsatisfied reservation exists," not "the queue is nonempty." When every
  queued reservation is satisfied and residue sits in the pot, a fresh
  arrival takes from the pot and jumps nobody.

Two qualifiers keep this honest:

- An in-subtree borrow through a registration attachment targets a
  *specific* demand's reservation regardless of position, so the
  front-loaded shape can be transiently perturbed; spill's front-first
  priority restores it. "Deepest non-empty first" is the drain definition;
  the anchor is the common-case hint.
- The **claimant queue is never front-loaded at rest**: a claimant arrives
  carrying whatever its park-scoped reservation still holds after loans
  (its unlent residue), so junior claimants hold real units. The claimant
  queue needs only the spill half of the anchor discipline (fill the front
  shortfall first); its reservations participate in draining like any
  others (see the drain discipline).

Start order decouples from arrival order: a satisfied front whose holder is
slow to wake no longer delays its successors — they complete, wake, and may
start first, while the front's units stay protected in its reservation. The
old model serialized successors on the head holder's wake latency; nothing
required that.

## Queue membership and retirement

The claim retires a demand — always and only. With that single rule the
ledger's structures partition the world exactly:

- **demand queue**: every registered admission that is not yet running —
  waiting, satisfied-but-unwoken, dispatching, parked in a blocking post,
  handed off — all of it;
- **claimant queue**: admissions and returning parked bodies blocked at the
  run boundary (the claim gate);
- **in-use**: running bodies, exact by the claim's own definition
  (reserved → in-use immediately before the body executes).

A registered admission's claim takes `p.mu` once at the run boundary:
success retires the entry; a miss retires it and registers the claimant in
the same section. Happy-path admissions never registered, so the fast-path
claim stays atomics-only.

This supersedes the 2026-07-23 verification-pass item-1 finding (the
executor-handoff window as "reserved-at-leaf-unreachable, no mechanism").
The demand entry *is* the pending-claim ledger state: the admission's units
stay discoverable and drainable through the entire dispatch path, including
the blocking executor hand-off (`wave.go:941`, `funnel.go:1065`). A drain
racing the claim just blocks the claim into the claimant queue, where it
outranks every demand. The collision the finding feared is rare by ordering
(a mid-dispatch entry is satisfied, hence in the drained-last front prefix),
last-resort by the drain discipline, and handled by machinery rather than
proved absent.

Claim-only retirement is also what keeps the door open for gated drain-side
work. Skim handlers execute inline on the pumping goroutine — no executor
rendezvous — so any future gated skim work needs its demand to survive
until immediately before the handler runs, which is precisely what "the
claim retires" gives every execution shape with no per-path rule. The
`limiter-suspend-resume.md` commitment "Skimmers never take `WithLimits`"
(and its sibling, unlimited flushes) should be read as contingent on the
old model — the cycle it guards against (drain gated on permits held by
parked producers) is the shape episodes and reservation lending now cover,
as funnel bodies already demonstrate. Whether to actually lift either
restriction is a separate decision (see TODO); this design just stops
re-entrenching them.

## The drain discipline

Borrowing and recall are one mechanism: every unit that leaves a reservation
for another party's benefit follows the same ordering, defined relative to
the **beneficiary**'s place in the service order (claimant queue front to
tail, then demand queue front to tail; a parked body joins at the tail of
the claimant queue when it returns, so it sits behind every queued party).

- **Sources behind the beneficiary drain freely, deepest-first**: demands'
  reservations tail-first, then parked bodies' reservations, then junior
  claimants' reservations tail-first. Units behind the beneficiary cannot
  advance their own holders until the beneficiary completes — deliveries
  serve the front first — so moving them forward costs their holders
  nothing in service order and accelerates the completion the whole queue
  waits on.
- **Sources ahead of the beneficiary are last resort only**, nearest the
  front last. Draining them delays a senior party for a junior's benefit;
  liveness may require it, so it is ordered, not forbidden.
- **There are no exclusions.** A claimant's reservation is protected by
  delivery order (any drain of it is repaid front-first), not by a rule.
  The front claimant's recall is simply this discipline run for the
  beneficiary that has nothing ahead of it: it reaches every reservation
  in the pool.

The claim gate's protections from phase 1 reduce accordingly. Retired: the
per-account claimant counters (the self→root increments), the
refuse-new-loans rule at counted accounts, and the account-targeted wake
scan ("wake one claimant whose chain passes through the landing account").
What enforces claimant priority is delivery order alone. Surviving state:
the claimant queue, and one pool-level count of waiting claimants for the
release fast path's lock-avoidance guard. New loans elsewhere can delay a
claimant but not starve it — every borrower that runs completes, and every
release lands on the front claimant first. That convergence claim is
checked, not trusted (see Verification).

## The Resource taxonomy

Directed delivery needs partial draw (filling a weighted shortfall from pot
residue takes `min(free, shortfall)`), and the delivery model's dependence
on releases makes explicit which resources can participate at all. The
single `Resource{TryAcquire; Release}` interface
(`internal/permits/permits.go:65`, whose doc already anticipates
`TryAcquireUpTo`) splits:

- `HoldableResource{TryAcquireUpTo; Release}` — units can be held idle and
  returned. The full reservation model: queues, reservations, lending,
  claims.
- `OverdraftResource{HoldableResource; Overdraft}` — holdable plus the
  overdraft policy hook, unchanged. Overdraft nests under holdable only: an
  episode settles by excess draining home through releases, which only
  holdable resources have.
- `EphemeralResource{TryAcquire}` — consume-on-acquire, no release (rate is
  the promised example the old interface could never actually serve: no
  releases means no capacity events, so a rate-backed pool would wedge its
  queue). An ephemeral pool degenerates to an admission gate: barrier,
  FIFO, all-or-nothing head retry — no reservations, no lending, no
  claimants, no forest. With no releases, the resource itself must re-drive
  a waiting head; the design is **deferred**, with one sketch recorded:
  `TryAcquire` returning a don't-retry-until `time.Time`, folding the
  refill schedule into the refusal.

`TryAcquireUpTo` subsumes the happy path for holdable pools: an arrival that
draws `k < w` was about to register anyway (the barrier already passed it,
so the queue held no shortfall), and `k` becomes its reservation's starting
balance. Implementations should be a single atomic `min(free, n)` draw —
one pot state then yields at most one partial draw (the next draw finds
zero), concurrent arrivals resolve as one-full-one-partial, and the racing
window between draw and registration is closed by the registration's
`p.mu` sweep, with the drain discipline as backstop.

## What this retires

From the 2026-07-22/23 settlements, superseded above:

- release-side chain repayment ("root-most lender on the releaser's own
  chain"), the root-lender hint and its validate-and-rewalk machinery, the
  root-first-repayment rationale and the "inner lender transiently
  uncovered" analysis — repayment is the claimant queue, always;
- the per-account claimant counters, the refuse-new-loans freeze, and the
  account-targeted claimant wake scan — delivery order alone;
- the verification-pass item-1 vacuity finding — the demand entry covers
  the window, and the claim gate handles the collision;
- head-of-line gathering as a distinct mechanism — assembly into the head's
  reservation is ordinary delivery; the revocability rule generalizes from
  loans to *all* backing of not-yet-started admissions, however funded.

Vocabulary settled with this design: the party a reservation belongs to is
a **holder** (a parked body or a registered demand; "owner" retires as a
noun — what ownership meant survives as recall priority). Units live in
**reservations**; deliveries fill them, drains empty them. The waiter-set
balance counts **missed notifications** (the `missFn` family; the
"banked wakes" phrasing that briefly appeared in `conservation-rework.md`'s
amendment was swept to this term the same day). The forest object remains the
**account** (2026-07-26 terminology settlement).

## Rejected en route

- **Front-only delivery without spill** (satisfied front blocks successors):
  strands capacity in the pot while shortfalls stand, and re-serializes
  successors on the front holder's wake latency.
- **Retirement at the wake-taking retry**: leaves the dispatch path dark —
  exactly the window the reservation model exists to cover.
- **Retirement at the executor hand-off**: adopted briefly, then superseded
  by claim-only retirement; it left a third, structureless state
  (handed-off-but-unclaimed) and needed a per-execution-path rule that
  inline-executed gated work (gated skim handlers, gated flushes) would
  break.
- **Chain-scoped repayment with a pool-scoped special case** (repay the
  claimant queue only when the releaser's chain has no lender): the special
  case *is* the general case once pairwise debt is acknowledged as
  untracked; two repayment modes for one fungible ledger.
- **Excluding claimants' reservations from draining**: protects nothing —
  junior residues are idle until the front completes anyway, and the front
  is repaid first by delivery order; an exclusion would wall off real
  capacity at true scarcity.
- **A sticky satisfied bit instead of counted shortfalls** and other
  bit-for-counter collapses: same argument as the missed-notification
  balance — multiplicity must match.

## Verification

Build-time asserts: delivery-order conformance (no unit past a more-senior
unfilled shortfall); pot nonempty ⇒ no shortfall in either queue; anchor =
head-most unsatisfied demand or nil, and the fast path gated exactly when a
shortfall exists; the demand entry leaves the queue exactly at the claim
(success, or retire-and-register-claimant in one `p.mu` section); the
conservation identity (checked-out == Σ reserved + in-use) through ledger
transfers that bypass the `Resource`.

Sim: an idle-point probe (both queues empty at quiescence ⇔ nothing gated
pending; delivery-discipline conformance at every observation — the
front-loaded-shape check applies to the demand queue only, and only modulo
in-subtree borrows and arrival races); per-pool mint/consume accounting,
now load-bearing for the convergence claim (every release lands on the
front claimant while one stands) as well as the missed-notification
balance; drain-order conformance per borrow event; the wake-as-completion
property (a reserve-side retry after a completion wake succeeds unless a
drain intervened). Regression gates unchanged: the quiet-wedge sim
configurations clean in large `-race` batches; the rdvq suite for the
balance.

Doc obligations at build: `../glossary.md` is reconciled — its
Notifications section still defines the superseded token-conservation
vocabulary (forwarding, productive consumption, notification token), and
the terms above (reservation, holder, claimant, spill, drain, pot, anchor)
move in once built. `../notification-conservation.md` absorbs the
completed delivery modes — completion wakes for reserved resources,
`missFn` for worker-minting queues, the missed-notification balance for
waiter sets, and directed delivery as the reserved-resource mode's final
form (the "narrowing is sound only as the resource's own proof under its
own lock" test is met by construction: every unit moves under `p.mu` with
both queues visible).

## Open points

- Ephemeral re-drive design (deferred; the don't-retry-until sketch above).
- The barrier exemption classes (`exemptFromBarrier`,
  `internal/permits/permits.go:813`: chains through the head's body
  account, the overdraft episode holder resuming) against the new anchor
  semantics — the exemption idea survives, but "the head's body account"
  needs restating when the anchor is a queue boundary rather than a single
  head.
- Whether to lift the skimmer `WithLimits` prohibition and gate funnel
  flushes (TODO items; this design removes the structural obstacle, not
  the decision).
- Build seams inherited from `forest-severability.md` (account-word layout,
  happy-load placement) are unchanged by this design.
