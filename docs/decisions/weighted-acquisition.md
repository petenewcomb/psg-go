# Weighted acquisition: head-only gathering behind a demand-side FIFO barrier

> Decision record (2026-07-02). Companion to `limiter-resource-classes.md`; specifies
> how the permit forest (`docs/permit-core.md`, `internal/permits`) generalizes from
> the weight-1 cut to weighted (amount-based) acquisition — the memory-limiter case.
> Headline: a partial gather into the acquirer's own `held` is not hold-and-wait
> (gathered permits stay borrowable), so assembly needs no rollback protocol; and a
> **demand-side** head-of-line barrier — a FIFO of invalidatable demand identities,
> sticky head, no weight-based ordering — provides x/sync/semaphore-grade fairness
> *without* supply-side reservation, preserving the "parked ⟹ borrowable"
> deadlock-freedom proof and making gather-vs-gather livelock unrepresentable.
> **Status: steps 1–2 implemented** — step 1 (mechanical weighting) landed 9e03d83;
> step 2a (multi-source gather + Demand identity) 698bac4; step 2b (FIFO + sticky-head
> barrier + per-demand mailboxes, wake chain) 1f27117 + cd09e5a; step 2c (§Overdraft:
> episode sentinel, allowance, extensions, suspension counters) 081b57c; step 2d
> (queue unification: nbcq FIFO + head slot) 91844ea. The remaining work and its
> scope were revised 2026-07-05: **`TryAcquireUpTo` is DROPPED** (unnecessary — the
> resource self-accounts for its own free capacity in the overdraft decision — and a
> pessimization; see "Resource partial grants" and Rejected alternatives). What
> remains: the **user-facing surface** (the weighted/plain limiter split etc.) next,
> then **one consumable pass** — the consumable resource class
> (`limiter-resource-classes.md`) + a rate limiter, on the settled resource contract
> (`TryAcquire(n) (bool, error)` — self-arm + terminal-refuse error; `NotifyAt` and
> `TryAcquireUpTo` both dropped), INCLUDING weighted consumables — after the surface is
> in place (one implementation of the consumable
> class, not two). The weighing *surface* (how ops weigh tasks) was already settled in
> `dispatch-execution-split.md` and is not revisited here.

## Context

### What weight-1 hardcodes

The `counts` layout is weight-ready by design — `(held, inUse)` are `uint64` *amounts*
in one `atomic128` word, chosen so "weights slot in later by parameterizing the
deltas, not the layout" (counts.go). But every operation is weight-1: `acquireLocal`
bumps `inUse+1`, `checkout` is `+1/+1`, `release` is `−1`, `stealOut` is `held−1`,
`acquireInto` calls `TryAcquire(1)`, `Cache.Acquire()` takes no weight, and
`Permit{backing *Cache}` carries none (so a weighted `Release` couldn't know its
amount). The deferred-backlog entry is WORKING_NOTES "weighted resources (re-derive
the removed `applicant` sizing natively)".

### What is already settled elsewhere

- **The infeasibility rule** (`dispatch-execution-split.md`, "Infeasible demand",
  as amended by this record): an oversized demand — weight that can never fit any
  capacity — is a runtime condition; fail the unit with a distinct error so the
  caller can reject/split/route. Never allow-anyway. (The split doc's separate
  *static-weight* branch — panic at op construction — is dropped by this record;
  see "User-facing surface". PN never wanted it.)
- **The structural assumption** (`permit-core.md`, deadlock-freedom): "an op of
  weight > 1 takes its whole weight atomically at admission, so there is no
  intra-acquisition hold-and-wait." This record shows how multi-source assembly
  honors that assumption's *intent* (no liveness-relevant hold-and-wait) without
  requiring the take to be literally single-CAS.

### The two problems weighted assembly creates

A weight-w acquire may need permits from several sources (free Resource capacity plus
several steal victims). Naively, partial assembly is hold-and-wait; and two
concurrent assemblers over insufficient capacity can steal each other's partial
progress forever (symmetric-gatherer livelock). Separately, large requests starve:
under fragmentation, weight-1 racers perpetually consume capacity a large waiter is
trying to accumulate.

## Decision 1: gather into your own `held`; occupy atomically

The load-bearing observation: a steal transfers into the acquirer's cache's **`held`,
not `inUse`**. A gatherer that has assembled 4 of 5 and parks is a cache with
`held=4, inUse=0` — every gathered permit is *borrowable, by anyone, the whole time*.
Therefore:

- **"Parked ⟹ borrowable" survives intact.** A blocked weighted acquire holds no
  `inUse` permit; its hoard is contestable. Partial gathering was never hold-and-wait
  in the sense the deadlock-freedom proof cares about.
- **Rollback is unnecessary: cache-don't-return *is* the rollback.** A gather that
  comes up short (or whose demand is abandoned) just stays — step-1 fodder for the
  retry, steal fodder for anyone else meanwhile. A postponed manager admit that
  gathered partially needs no give-back protocol.
- **The hoard is the natural accumulation point** across successive wake-chain wakes:
  pull each freed permit into `held`; when `held − inUse ≥ w`, one gated CAS
  (`acquireLocal(w)`) occupies.

The algorithm: **fast path** — single-cache atomic occupy up the ancestor chain
(`acquireLocal(w)`; needs one node with borrowable ≥ w); **slow path** — register a
demand and, as head (Decision 2), gather into own `held` from free Resource capacity
and forest steals, then occupy atomically from the own cache; park (keeping the
borrowable hoard) between gathering steps.

## Decision 2: a demand-side head-of-line barrier — FIFO, sticky head, no max

> **Representation superseded (2026-07-04)** by "Queue unification" below: the
> mutex-guarded slice becomes an always-on lock-free (nbcq) FIFO + a CAS-managed
> head slot, and EVERY weight queues. The principles here — demand-side not
> supply-side, sticky head, arrival order with no weight-based ordering, gate
> every arm while a head stands, head-only gathering — all carry forward.

The fairness/livelock mechanism is a **barrier on other acquirers**, not a
reservation of capacity. The distinction is what keeps the proof intact:

- **Supply-side reservation** (x/sync/semaphore-style: freed capacity earmarked for
  the head waiter, idle-but-non-borrowable) violates "parked ⟹ borrowable" and
  reopens the cycle the forest's liveness proof closes. Rejected.
- **A demand-side barrier** blocks *acquisition*, never *release*. The head's hoard
  remains borrowable in principle (the invariant holds); it is merely uncontested in
  practice, because everyone who could steal it is queued behind the barrier.

Mechanics (PN, this session):

- **Registration**: a w ≥ 2 acquire that misses the fast path registers an
  **invalidatable demand identity** in a Pool-level FIFO and waits its turn. It does
  **not** freelance-gather first — **gathering is head-only**, so gather never races
  gather and the symmetric-gatherer livelock is *unrepresentable*, not merely
  mitigated.
- **Sticky head, FIFO succession, no weight-based ordering anywhere.** The barrier
  belongs to the front demand until it is satisfied or invalidated; the successor is
  simply the next registered demand in arrival order. A running-max (or any
  weight-sensitive) succession was rejected: it biases the pool toward satisfying
  only large demands — a bigger arrival preempts or perpetually outranks smaller
  registered demands (inverse starvation). FIFO retires one demand per pass, so
  every registered demand's wait is bounded by the finite queue ahead of it —
  starvation-free among registered demands.
- **While armed, the barrier gates every acquisition arm — steps 1–4, not just
  resource/steal.** This is the subtle requirement: under cache-don't-return, a
  completing sibling's permit stays in its cache and the next local body occupies it
  via a step-1 hit that never touches the Pool. A barrier gating only steps 3–4 lets
  that recirculating local capacity bypass the head invisibly, and the head starves
  while the pool looks busy. So `acquireLocal` gains a barrier check — one atomic
  load on the lock-free hot path, the exact mirror of the `balance` load
  `Permit.Release` gained in `limiter-resource-classes.md`. Unarmed: a nil load and
  a predictable branch. A barrier miss composes with the existing miss handling
  (blocking callers park on the Pool's waiters; the manager's non-blocking admit
  postpones).

### Liveness, restated

Induction over barrier passes:

1. While armed, the head H is the sole acquirer. Every running body eventually
   completes or parks — both drop `inUse`, and **neither requires acquiring**
   (parking is free; only *resuming* acquires, and a blocked resumer holds nothing
   `inUse`). All capacity flows monotonically toward borrowable, and only H may take
   it.
2. H assembles `w_H ≤ capacity` in finite time, or its identity is invalidated.
   Either way the barrier passes to the next registered demand (or disarms).
3. A blocked acquirer waits at most the finite FIFO ahead of it (registered demands)
   or one barrier epoch (weight-1 acquirers, who never register).

### Multi-limiter: the FIFO under joint admission (PN, 2026-07-03)

The induction above is **invalid as stated** once joint admission exists: a joint
acquirer that satisfied limiter A and is registered/waiting at B's barrier holds
A-permits **un-lent while neither running nor parked** — a *mid-sequence hold*, a
blocked-holding state with no single-limiter analogue. A's head may need exactly
those permits, and they flow only when B resolves.

**The canonical global acquisition order keeps this acyclic.** A demand blocked at
limiter L holds permits only at limiters ordered before L, so every wait-for edge
points strictly *up* the order — no cycle can close. Liveness restates as
descending induction: the highest limiter's capacity is held only by
fully-admitted running bodies (anyone blocked there holds only lower limiters), so
it drains by ordinary complete-or-park; its waiters admit, run, and release their
lower holds; repeat downward.

- **Mid-sequence holds are NOT lent** (policy, load-bearing): letting the blocked
  joint acquirer's A-permits be borrowable would unblock A's head sooner, but on
  B-success the acquirer must re-take A — possibly gone, re-registering at A while
  holding B: a *down-order* wait, exactly the edge that reopens the cycle, and a
  breach of atomic joint admission. The cost of holding is latency coupling (A's
  head waits on B's resolution), not deadlock.
- **The overdraft proof already handles this correctly — do not "fix" it**:
  mid-sequence holds are `inUse` in `counts`, so the zero-in-use proof cannot pass
  while a joint acquirer is mid-flight. Correct: that capacity *will* return.
- **Consumable barriers cannot participate in mutual contention at all (PN)**: a
  consumable head's satisfaction depends only on *time* (accrual), never on
  anyone's release, and the head dequeues **at admission** — the barrier never
  stands through work execution. With consumables sorted last in the canonical
  order (already decided for the refund-free abort), the induction's base case is
  therefore trivially live: it resolves autonomously (exact-timer wake) or ends in
  `NotifyAt`'s refusal error. Spent tokens create no wait-for edges either —
  nobody waits for tokens to come back.

Model-check additions: the ordered-wait DAG (blocked-at-L ⟹ holds only < L);
no-lending-mid-sequence; descending-order drain with consumables-last; interleaved
armed barriers across pools under joint admission; consumable-base autonomy.

### The joint reclaim: every park holds only a canonical prefix (2026-07-09; reviewed PN 2026-07-10)

The induction above covers admission, where the order is enforced by construction.
The **suspend/reclaim path breaks it structurally**: a drive episode lends the whole
joint set, and the reacquire cannot avoid re-taking a LOWER limiter after a higher
one has re-landed — a rest hold's help-block confirm acquires that rest hold while
the head sits suspended, and the interior bracket's unwind must then wait for the
head. That is precisely the "re-take A while holding B" down-order edge the
admission policy avoids by never lending mid-sequence — but a reclaim has no such
option: lending is its purpose. The sim's multi wiring produced both faces as real
deadlocks (2026-07-09, trace-confirmed):

- **Permit face**: the reclaim parks waiting the lower rank while holding the
  higher-rank permit; a canonical-posture admitter (holds lower, postponed on
  higher) closes the cycle.
- **Headship face**: the reclaim parks waiting the lower rank while its
  higher-rank demand stands REGISTERED — at the pool's FIFO head, that
  registration reserves capacity exactly like a permit (only the head gathers), so
  the same cycle closes through the queue: the admitter holding the lower-rank
  permit waits for the reserved slot, and the slot's owner waits for that permit.

**The rule**: a reclaim-time park on hold X must hold NOTHING of rank above X —
neither a permit (lend it: plain release + a `lent` mark for the joint fixpoint to
reacquire) nor a queue registration (withdraw it: `Demand.Invalidate`, the FIFO's
lazy dequeue — a head's withdrawal promotes and wakes the successor). The canonical
below-X prefix may stay held: with both permit and registration holds counted, every
wait-for edge again points strictly up the order, restoring the admission
induction's acyclicity argument for reclaims. The withdrawn demand's owner loop
re-registers on its next confirm (Acquire re-enqueues an invalidated demand),
paying only its queue position; each surrender lets a canonical-posture admitter
complete, so progress is global — a surrendered slot is consumed by an admission
that then releases capacity.

**Fairness cost, bounded (review resolution, PN 2026-07-10).** Only reclaims pay
the position loss, and the loss is per-round bounded: the overtaking window is
[withdraw → re-registration], which closes when the down-rank wait resolves —
arrivals after re-registration queue behind the returning demand as usual. A
repeat round requires an interior bracket to re-suspend the lower hold AND that
permit to be lost again during the help window, so repeats are coupled to system
progress (helped work ran), never a tight loop. Admissions lose nothing at all,
by two existing mechanisms working together: Decision 4 keeps a postponed
admission's demand identity registered as ONE FIFO entry across the whole
postpone/retry cycle (it never surrenders its position), and workq's selection
pass re-attempts every postponed work item before ACCEPTING any new work (a
blocked accept is interrupted by a postponed item's readiness wake), so new work
that might consume overlapping permits is structurally behind every pending
retry.

Rejected alternatives for restoring the reclaimer's exact position:

- **Yielding head** — a fifth head-slot state that keeps queue position but
  releases the capacity reservation while its owner waits down-rank. Position
  and reservation are fused in the standing-head discipline on purpose, and
  unfusing them inside the W2b/W2d promotion/retirement CAS protocol (a re-arm
  must displace a successor possibly mid-gather) is the pool's most delicate
  machinery, bought for a per-round-bounded fairness gain.
- **Senior re-entry tier** — a second demand queue, served ahead of fresh
  registrations, that withdrawn demands re-enter (the workq fresh/postponed
  shape with the priority inverted: seniority restoration rather than
  failed-once deprioritization). Cheaper than the yielding head and
  order-preserving within the tier, but still a second queue threaded through
  promoteScan and the empty-slot reopen dance, for the same bounded gain.

Revisit either only if reclaim-latency tails surface in the multi-limiter
benchmarks.

Mechanics as implemented (permithandle.go): per-hold `suspendTarget` scopes each
suspend bracket to exactly the holds it suspended; `reclaimJoint` runs a
lowest-rank-first fixpoint over (suspended ∨ lent) holds; `heldPermit.reclaim`
applies the lend/withdraw rule before every park, in both its helping and plain
branches. Admission is untouched: mid-sequence holds stay inUse and un-lent (the
policy above is load-bearing and unchanged — the gate runs before the body exists,
so the suspend machinery never resolves a set mid-admission).

## Decision 3: arm only for w ≥ 2

> **SUPERSEDED (2026-07-04)** by "Queue unification" below. Excluding weight-1 from
> the queue was itself weight-based ordering — the exact bias Decision 2 rejects —
> and let weight-1 starve for as long as the FIFO stayed non-empty. The property
> this decision actually protected (the mechanism is dormant for pure weight-1
> pools; no serialization of the common case) is preserved by the empty-slot fast
> path instead of by an exclusion rule: no waiter ⇒ no head ⇒ the ordinary
> lock-free machinery, verbatim.

As originally recorded: a weight-1 waiter never needs assembly — any single freed
permit satisfies it — so weight-1 demands neither register nor arm. Weight-1
contention keeps today's fully concurrent machinery (wake-one + renotify
conservation + steal). Without this guard, ordinary weight-1 saturation would
serialize the entire pool through the barrier — a regression on the common case.
With it, the whole mechanism is dormant for semaphore and rate pools: one
always-false branch.

## Decision 4: the demand identity is a conservation token, caller-held

Registered demand must be **satisfied or explicitly invalidated, never dropped** —
the same conservation discipline as `execpool.RegisterUnmetDemand`/`Unregister` and
the `rdvq.Notification` work. The identity is supplied by the *caller* of
`Cache.Acquire(id, w)`, not minted per call, for a reason beyond invalidation
plumbing: **the postponed manager retries the same demand repeatedly** — a per-call
identity would register a fresh demand per retry; the caller-held identity dedupes
the whole postpone/retry cycle to one FIFO entry (re-presenting the same id is
idempotent).

- **Invalidation edges**: satisfied; `AcquireWait` ctx cancelled; postponed work
  dropped/cancelled; wave teardown; optionally a demand deadline. Invalidating the
  *head* passes the barrier (successor = next FIFO entry). An invalidated demand's
  partial hoard stays borrowable in its cache — conservation needs no give-back
  (Decision 1).
- **ABA care**: pooled identity objects need generation-stamping or a CAS'd state
  machine — a stale head-field reference to an invalidated-and-reused identity is
  the same bug shape as the rdvq inbox reuse (captured-gen hints,
  `rdvq-inbox-reclamation.md`).

## Queue unification (PN, 2026-07-04): one always-on lock-free demand FIFO

> Supersedes Decision 2's representation and all of Decision 3. Converged in the
> W2c data-structure review thread: (a) gated weight-1 starving behind a non-empty
> FIFO is weight-based ordering by class; (b) registered demands cannot assume
> mailbox *parking* — w ≥ 2 arrives through the manager-postpone path too, so wake
> delivery must serve listeners and waiters uniformly; (c) the wake chain already
> serializes admission, so the mutex-guarded slice duplicates queue structure the
> notifier machinery (nbcq) already provides. **Status: implemented (step 2d,
> this commit)**; one refinement surfaced by the tests, recorded under
> "Implementation notes".

### Structure

- **One demand FIFO, always on, for every weight** — an nbcq of `*Demand` (the
  queue needs concurrent producers but only a logically-single consumer: promotion
  is the sole consumption point and the head slot serializes it). Every acquire
  that cannot be satisfied immediately enqueues; a satisfied acquire never touches
  the queue.
- **The head slot** — `head atomic.Pointer[Demand]` (the field formerly named
  `barrier`): the current head demand, held OUTSIDE the queue (nbcq cannot peek
  without popping; the slot is the sticky head). Non-nil ⇔ someone is waiting ⇔
  every acquisition arm is gated (Decision 2's gate-all-arms rule, unchanged)
  unless exempt (chain through the head's cache / episode owner at its anchor).
- **Interior removal is lazy** (nbcq cannot unlink interior nodes): Invalidate
  retires the entry by generation — Decision 4's ABA discipline becomes
  load-bearing — and promotion skips gen-stale entries when popping.

### Protocol (CAS only at transitions; the hot path only loads)

- **Fast path (PN): a bare load, nothing stronger.** Load the head slot; nil ⇒
  attempt the ordinary lock-free acquire (steps 0–4 / whole-grant) and, on
  success, return — the queue and slot are never touched, no CAS, no barrier
  beyond today's one-load check. This is the pre-unification unarmed path
  verbatim, so pure weight-1 pools keep their fully concurrent machinery.
  Staleness is the existing benign doctrine: a stale nil leaks one snipe
  (compensated by the promoted head's park-time confirm re-reading counts); a
  stale head over-gates one attempt (retried).
- **Enqueue**: slot non-nil (and not exempt), or the fast-path attempt missed ⇒
  push the demand, then, if the slot is nil, promote (pop-front-into-slot CAS —
  possibly popping an earlier arrival, preserving order; popping *yourself* is the
  instant-head case, and the uncontended w ≥ 2 single-call satisfaction survives:
  enqueue → instant head → gather inline → hand off).
- **Head retirement** (satisfaction or invalidation): pop the next non-stale
  entry and CAS the slot **directly from self to successor** — one CAS, so no
  empty-slot window exists while waiters remain, which is what bounds sniping to
  ~zero against queued demands. Wake the successor's mailbox. Empty queue ⇒ CAS
  the slot to nil; the enqueue-side promote check closes the race with a
  concurrent arrival.
- **Wake delivery serves parks AND postpones**: the demand mailbox is a full
  notifier — `AcquireWait` parks on its waiter half; a postponed manager registers
  its retry on its listener half (Notify already prefers listeners). No separate
  wait-target classes remain.

### What collapses

- **Arming/disarm**: gone as concepts — "armed" degenerates to "the slot is
  non-nil"; there is no mode to enter or leave, no disarm condition, no flush.
- **The pool's general waiter/listener set retires from the permits path**: every
  waiter is a queue entry with a mailbox. `Release` wakes the head slot's mailbox
  if a head stands, else nobody (nobody is waiting). Multi-permit events need no
  chained walk: **the promotion cascade is the chain** — each satisfied head
  promotes a successor whose confirm re-reads counts. W2b-i's probe rules remain
  for the workq/queue-space consumers they also serve; the permits pool no longer
  needs them.
- **fifoMu**: the queue is lock-free and the slot is CAS-managed. What fifoMu
  also guarded — episode-extension serialization and `od.total` — moves to a
  small mutex inside the pooled `overdraft` object (episode-cold by definition).

### What layers on unchanged

Episodes (the sentinel occupies the head slot; claimants; the structural pin;
endEpisode retires slot → successor), suspension counters and the stranger check,
the exemption anchor (the slot demand's cache), head-only gathering, and Decision
1's hoard discipline.

### Fairness and the stated trade

Strict arrival order among ALL waiters, uniformly — the liveness induction's step
3 loses its two-class split (every waiter's wait is bounded by the finite queue
ahead of it, period). While a head stands there is no capacity sniping (the slot
gates), and the single-CAS handoff leaves no inter-head window; the only leak is
the empty-queue transition race — one benign, compensated acquire. And that leak
is not a fairness violation at all (PN): it can only occur between demands that
arrived within the same race window, and arrival order is undefined at that
resolution — whichever serialization the race produces IS a valid arrival order
for those two demands. The compensation discipline covers liveness; this covers
fairness. The cost:
while a head stands, each contended admission pays one wake handoff instead of a
snipe — expected to *improve* P99/max (no starvation tail) at some peak-throughput
cost on saturated pools. Measure before/after per the benchmark methodology
(heavy-tailed blocking-I/O work, tail metrics primary, P:D sweeps); the fast path
means an uncontended pool pays nothing.

### Implementation notes

- Weight-1 registrants carry demands with mailboxes like everyone else; whether a
  w = 1 head needs a body cache (its "gather" is a single take that could back
  from the registering cache) is an implementation detail — uniform-with-w ≥ 2 is
  acceptable, it is the cold path.
- streampool's block-and-help loops (`blockAcquire`, `reclaim`) currently park on
  the pool's general waiters; they move to the demand mailbox (the wave.block
  plumbing takes the park target as a parameter already).
- **The overdraft evaluation is uniform across weights (PN, 2026-07-04 —
  superseding the short-lived w ≥ 2 gate from the first cut)**, made sound by
  **proof-premise re-establishment in headGather** (found by a biased-sim hang
  hunt, ~5%/check before, 0/300 after): the gather's exhaustion, the zero-inUse
  walk, and the Resource's refusal are separate snapshots, and capacity moving
  between them — a steal mid-transfer, or (the common case) a cache destroy
  draining held back to the Resource's walk-invisible FREE pool — let the policy
  be consulted while capacity was right there. The head's evaluation now loops:
  gather; walk (anyInUse ⇒ wait; borrowable-elsewhere ⇒ re-gather); re-attempt
  TryAcquire(shortfall) LAST and finish the gather on success; only a truly dry
  forest with a fresh refusal reaches the policy. Consequently a weight-1 head
  reaches the policy only at literally zero capacity.
- **streampool's `semaphoreResource` answers "not now" for every weight until
  step 4.** "Not now" is the middle outcome (granted=false, err=nil): keep
  waiting, no grant, no failure, no commitment — re-driven by a release or a
  `SetMaxConcurrency` raise. It cannot GRANT yet because an episode exempts the
  grantee's CAUSAL SUBTREE from the head-of-line gate, and the subtree is not
  representable until step 4 wires the body-cache meta-redirect — pre-step-4 an
  episode owner's own downstream dispatches are gated behind its own episode, a
  structural self-wedge (this, not any over-grant, is what the sim hang hunt
  surfaced). **The step-4 policy is OPEN**, not settled (correcting an earlier
  over-attribution): the only clear part is "not now" while PAUSED (limit 0 keeps
  blocking every weight until a raise, preserving the pause contract). For a
  demand heavier than a nonzero fixed ceiling, grant / refuse / not-now are all
  defensible — grant briefly exceeds a soft cap (the doc's default-grant
  rationale, but that argument fits demands *near* capacity, not ones structurally
  larger than the whole limit); refuse matches "weighted infeasibility is a
  per-unit error" and is natural for a memory limiter (item bigger than the whole
  budget); not-now waits for a raise that may never come. Decide with the memory
  limiter and the weigher-error path at step 4. A non-implementing resource still
  default-grants at any weight.
- **Latent weight bug found by the policy test**: `semaphoreResource.TryAcquire(n)`
  ignored n for bounded limits (a W2a-era "n is always 1" shortcut) — admitting 1
  while the pool checked out n. Fixed with `InFlightCounter.AddIfUnder(n, limit)`
  (atomic all-or-nothing, the contract the gather's shortfall arm assumes).
- **Measured (2026-07-04, bench/BenchmarkDispatch, heavytail, medians of 5)**:
  underload/balanced within noise (the fast path is untouched); overload
  p99-e2e −34%; heavy-overload p99-e2e −63%, p99.9-e2e −51%, throughput +6.6% —
  the starvation tail the strict arrival order was predicted to remove. p50-e2e
  +5–8% under overload: the fairness redistribution (the median no longer snipes
  past the unlucky), the accepted side of the trade.

## Struct / API mapping (internal/permits)

- `Cache.Acquire(id, w)` / `Cache.AcquireWait(ctx, id, w)`; `Permit` gains a `weight`
  field (Release must know its amount).
- `counts` transitions parameterized exactly as the layout anticipated:
  `acquireLocal(w)` (gated `inUse+w ≤ held`), `checkout(w)`, `release(w)`, and
  `stealOut` → `stealOutUpTo(w) uint64` (take `min(borrowable, w)`, returning the
  amount — still a single CAS).
- `Pool` gains the head field (atomic pointer to the front demand) plus a small
  mutex-guarded FIFO of registered demands — cold by construction (only w ≥ 2
  waiters), so a locked list is fine.
- The barrier check lands in `acquireLocal` and `acquireInto` (one atomic load).
- **Resource partial grants (`TryAcquireUpTo`) — considered, then DROPPED
  (2026-07-05).** The idea was that a gather that can't take the whole shortfall from
  the free pool (`TryAcquire(remaining)` is all-or-nothing) strands the free fragment
  `F < remaining`, inflating the overdraft ask; `TryAcquireUpTo(n) int` would take
  `F` and shrink the ask. Two findings retire it:
  (1) **Not needed for correctness.** A demand only ever reaches the overdraft
  evaluation when `free < shortfall` (if `free ≥ shortfall`, `TryAcquire` already
  satisfied it). So the resource, which *is* the accountant for its own
  `free = capacity − checkedOut`, computes the true deficit `shortfall − free` itself
  and decides grant/refuse on that. A demand that would fit using the free capacity
  never reaches the callback, so there is no spurious refusal to prevent.
  (2) **It is a pessimization.** The demand that reaches overdraft is over capacity
  and overdrafts regardless, so taking `F` does not help it finish — it only converts
  easily-reachable free-pool capacity into cached-borrowable buried in the outlier's
  body cache, which every subsequent small demand must then *steal* (a forest walk)
  to reach. All-or-nothing is the *good* behavior: it leaves `F` in the pool where
  locality is best. See Rejected alternatives.

  Concentration is bounded by the cache lifecycle, so no reclamation machinery is
  needed for it: a cache's held returns to the Resource by two paths — *steal-pull*
  while it lives, and *destroy-drain* when it dies (`destroy` → `counts.drain` at
  `inUse == 0` → `resource.Release`). For the overdraft case this is exact: the
  episode end *is* the body-cache destroy, so the outlier's whole gathered hoard
  drains back to the Resource at completion. (Proactive shrink of long-lived *idle*
  cache — a persistent wave that gathered and went quiet — is the separate, narrower
  `Reclaim(n)` case in `limiter-resource-classes.md`, motivated by shrinking capacity
  (memory/GC drift), not by concentration.)
- **Infeasibility handling**: superseded by the overdraft design (see "Overdraft"
  below) — detection is the armed + zero-in-use proof, needing no capacity
  visibility from the resource; the outcome is overdraft, wait ("not now"), or the
  distinct per-unit error.

## Overdraft: infeasible demand under the armed barrier (PN, 2026-07-03)

**Detection is free and exact.** Barrier armed + zero `inUse` anywhere + head's
gather exhausted + `TryAcquire(remaining)` refused = nothing inside the system can
ever change the answer — dynamically-proven infeasibility at current capacity. No
pool-level `inUse` aggregate and no resource capacity-visibility capability needed
(this retires the registration-time check sketched in the struct mapping): while
armed, occupies are blocked and only releases move the world, so the head performs a
cold forest walk (searchList-shaped, any `inUse > 0`?) after a failed gather.

**Ancestor-exempt trigger (PN).** Overdraft is evaluated only when every suspended
holder is on the head's own driver chain — their resume is causally *after* the
head's wave drains, so they can never observe the over-commitment. A *stranger* (a
suspended holder off that chain) blocks overdraft: its resume races the
over-commitment and would stack reacquisition pressure on it. (Strict total
quiescence was rejected: the head's ancestors are suspended *by construction* under
nesting, so nested demands could never overdraft.)

**The capability** — policy only, no accounting duties:

```go
type OverdraftResource interface {
	Resource
	// Called when the pool has exhausted its own means for the head demand.
	//   granted=true           — grant: proceed over the limit now (install an episode)
	//   granted=false, err=nil — not now: the head keeps waiting and re-asks on the
	//                            next capacity change; NO commitment (a later call may
	//                            grant, wait again, or refuse). The resource owes a
	//                            future wake, not eventual satisfaction.
	//   err != nil             — refuse: the unit fails with err (the resource's own
	//                            reason — no sentinel required; PN)
	Overdraft(n int) (granted bool, err error)
}
```

Type-asserted at `NewPool`. **Holdable-only** (PN, 2026-07-03): a consumable
"overdraft" is mechanically indistinguishable from regular acquisition — the
resource is the sole accountant, and grant-by-going-negative is just `TryAcquire`
returning true past zero, internal policy the pool never sees (see the consumable
section below for what consumables actually need). Panic inside the call is the
resource's prerogative for must-never-happen cases. Refusal errors propagate on
existing channels (blocking top-level → `Submit`'s error; postponed manager → error
sink → `SkimAll`; mid-body → `AcquireWait`'s error) and invalidate the demand,
passing the barrier. "Never" as the third state's name was rejected: the assertion
is present-tense policy ("not at any capacity I'm currently willing to reach"), not
prophecy — a resubmission after a capacity raise may succeed. **Non-implementing
holdables default to GRANT** (PN): at the proven-infeasible point the unit is
satisfiable only by overdraft, and a briefly exceeded concurrency cap beats a killed
unit; resources whose limits are hard safety walls (memory) implement the capability
to refuse.

**Representation (PN): a pool-level allowance; overdraft never enters `held`.** The
conservation law `Σ held == checkedOut` survives untouched — the granted amount `d`
enters neither `held` nor `checkedOut`. It is a parallel allowance for
`inUse`-excess: `inUse > held` is permitted cache-locally only while a grant stands,
governed by `Σ max(inUse − held, 0) + allowance-remaining = d`. An occupy that
cannot fit under `held` claims excess from the allowance and pushes `inUse` past
`held`; a release returns excess to the allowance — the returned amount is the delta
of `max(inUse − held, 0)` across the decrement, computable inside the existing CAS
loop, so no per-occupy tagging. Park/lend needs zero special cases: the parking
head's excess flows back to the allowance; descendants occupying in place claim from
it ("available to add to Caches' `inUse`" — PN). Unstealable and uncacheable by
*structure*: steals move `held`, and `d` is never in `held`.

**The head stands until completion (PN).** The head remains in place — enqueued,
barrier armed — until it fully releases at completion. This is the seriality
guarantee across park gaps (while the head is parked its subtree can transiently hit
zero-in-use; without the standing head the next FIFO demand could pass the proof and
stack an independent overdraft) and it keeps new arrivals from accreting demand into
the over-committed window. At completion the subtree's `inUse` has drained, so the
allowance is necessarily fully home: zero the counter, dequeue, pass the barrier.

**Episode extension (PN).** A descendant demand that exceeds lent capacity plus
remaining allowance may request an *additional* overdraft — the same capability
call, for the shortfall, evaluations serialized within the episode. Granted: the
amount is added to the outstanding aggregate and remains until the *original* head
completes — one episode, one owner, monotone growth, a single clear point. Refused:
that unit takes the distinct error path (its sub-wave drains with the error, the
head resumes and completes — no wedge). Not now: it parks and re-asks on the
next wake.

**Consumables: the sticky-head+FIFO is the mechanism; there is no consumable
overdraft (PN, 2026-07-03).** What actually protects a large consumable demand is
the barrier — without it, weight-1 racers drain every refill before a w=5 demand
ever sees 5; the notify-target alone cannot help, because nothing would guard the
accrual it is timing. So the same Pool-level FIFO/sticky-head object serves both
classes, with the pass-through mechanics pinned as:

- a w ≥ 2 demand whose `TryAcquire(w)` misses **registers in the FIFO** (same
  w ≥ 2 arming rule; weight-1 contention keeps the ordinary wake path);
- while armed, the **barrier check in the pass-through gate blocks all
  acquisition** (one atomic load, the analogue of the `acquireLocal` check);
- the head is satisfied by the resource's internal accrual under barrier
  protection. **The wake and feasibility both ride `TryAcquire(w) (bool, error)`
  now — `NotifyAt` is dropped** (superseded 2026-07-05; the standalone
  `NotifyAt(n) error` design below is kept only as the record of how we got here).
  A consumable's ask is `w` (no gather to reduce it), so on a missed `TryAcquire`
  the resource **self-arms** the wake for `w` — remember the rejected size, arm one
  exact timer (`(w − level) / rate`, or fold into the gauge poll), post `Adjust`
  when it matures; sub-target maturations are suppressible while a head stands
  (waste by construction — the barrier blocks everyone they could serve). An
  *unreachable* `w` (larger than the resource can ever accrue) is the `error`
  return of that same `TryAcquire` — terminal refuse, feasibility and the acquire
  attempt in one call, no separate notify method. (Full contract:
  `limiter-resource-classes.md` §"Resource contract, settled".)
- the head **dequeues at admission** — charge-once semantics has no completion
  event, and the standing head is the holdable overdraft-episode mechanism, not a
  barrier feature.

Per-class asymmetry, in one view: the FIFO/head is shared; what differs is how the
head is satisfied (gather vs. accrual + notify-target), what stands after
satisfaction (the holdable overdraft episode vs. nothing), and where infeasibility
is known (the pool's zero-in-use proof vs. the resource's `NotifyAt` error).
Weighted-usable consumables must implement the notify-target — which they need for
the wake-chatter fix regardless — a constraint on framework-authored resources, not
a user trap. A resource that *wants* oversized-admission semantics (negative
bucket, repaid by refill) implements it as internal `TryAcquire` policy, invisible
to the pool.

Rejected along the way: **consumable overdraft as a pool-visible event** (PN: the
grant is mechanically indistinguishable from regular acquisition — decrement the
counter, just past zero; the pool holds no state either way, so it was internal
`TryAcquire` policy dressed in the holdable concept's clothes);
**force-accounting the overdraft into `held`** (stealable
the moment the head parks; cache-don't-return makes it reusable after completion —
never repaid until cache destroy); **an unconditional `Acquire(n)` force-accounting
primitive on `HoldableResource`** (the resource needn't know — the standing barrier
does the everyone-else-refused scoping); **per-cache `inUse > held` with a
`Permit`-carried overdraft and repay-on-any-drop** (park/resume churn: repay +
re-grant brackets around every drive); **"Never" as the refuse state's name**;
**strict-quiescence suspend rule**; **error-only descendant rule** (superseded by
episode extension); **a pool-side give-up timeout** (an arbitrary clock deciding a
semantic question the resource can answer exactly).

Model-check additions: the episode invariant (`Σ excess + allowance = d`);
conservation untouched by grants; standing-head seriality across park gaps;
extension serialization; descendant-refusal unwedging; ancestor-exempt detection
(stranger present ⇒ no grant).

## User-facing surface (settled 2026-07-02; sequencing step 4)

> **Refined 2026-07-05 — see "The weighted/plain limiter split" below.** Two
> orthogonal facts the original framing conflated: the *weigher* is an (op, limiter)
> binding, but weight-*capability* is a *limiter* property (plain vs weighted
> semaphore), with different overdraft policies and a compile-time guard. That
> supersedes "the same Limiter may be weighed or not" wherever it appears here.

The *weigher* is a property of the **(op, limiter) pair** — a shared memory Limiter
needs a different extractor per op — so it lives on the op. **Builder methods on the
op type, returning the op type** (the `In(wave)` copy-with-modification pattern;
`With` prefix per the `http.Request.WithContext` copy-semantics convention):

```go
// WeightLimiter[T] pairs a WEIGHT-CAPABLE limiter with this op-type's weigher — a
// reusable typed binding (define once, share across same-T ops). The WEIGHER is a
// property of the binding (a weight-capable limiter may still be bound weight-1 by
// one op and weighed by another); weight-CAPABILITY is a property of the limiter
// (only NewWeightedSemaphore, not NewSemaphore — the compile-time guard below).
func NewWeightLimiter[T any](l WeightedLimiter, weigh func(T) int) WeightLimiter[T]

// Reusable canonicalized sets: built once (sorted into canonical acquisition
// order, duplicate-scanned, frozen), bound to many ops. One-shot homogeneous
// variadic constructors — no builder chaining, no inference gaps.
type LimiterSet struct{ ... }               // untyped: reusable across ops of EVERY T
func NewLimiterSet(ls ...Limiter) LimiterSet
type WeightLimiterSet[T any] struct{ ... }  // typed: reusable across same-T ops
func NewWeightLimiterSet[T any](wls ...WeightLimiter[T]) WeightLimiterSet[T]

// Binding methods. The ...s methods are plural because their arguments come in
// bunches; the set methods are singular because a set IS the bunch (a variadic
// of sets would aggregate aggregates — multiple sets compose by repeated calls
// under the accumulation law).
func (r Launcher[T]) WithLimits(ls ...Limiter) Launcher[T]                  // weight 1 each
func (r Launcher[T]) WithWeightLimits(wls ...WeightLimiter[T]) Launcher[T]
func (r Launcher[T]) WithLimiterSet(s LimiterSet) Launcher[T]
func (r Launcher[T]) WithWeightLimiterSet(s WeightLimiterSet[T]) Launcher[T]
```

```go
var std         = NewLimiterSet(conns, disk)                 // no T: universal reuse
var memByBuf    = NewWeightLimiter(mem, func(it Item) int { return len(it.Buf) }) // mem: NewWeightedSemaphore
var itemWeights = NewWeightLimiterSet(memByBuf)              // T inferred from members

launcher := NewLauncher(handler).       // T inferred from handler
    WithLimiterSet(std).
    WithWeightLimiterSet(itemWeights)   // zero explicit type parameters anywhere
```

(Supersedes the singular `WithLimit`/`WithWeightLimit` from earlier in this thread:
the variadic plural reads identically for one limiter, binds several in one call —
better multi-limiter forward-compat — and the first-class `WeightLimiter[T]` value
recovers the shareable-binding property options had, in the only scope where a
weigher is shareable anyway. Verified 0 allocs/chain including multi-arg variadic
calls: the call-site backing array is constant-size and stack-allocated **provided
the methods copy out of the variadic slice and never retain it** — storing it would
heap-allocate and alias the caller's array.)

### The weighted/plain limiter split (PN, 2026-07-05)

Refines — and partly reverses — the "collapse to one Limiter, weigher optional"
framing. There are two orthogonal facts:

- **The weigher is an (op, limiter) binding** (unchanged): which extractor produces
  the weight, per op.
- **Weight-CAPABILITY is a limiter property** (new): whether the limiter's overdraft
  policy can answer a demand that cannot fit the current capacity.

Why it is a real distinction, not just surface: **weight is what lets "infeasible" be
permanent.**

- A **plain** (unweighted) semaphore: a w=1 demand fits any capacity ≥ 1, so it
  reaches the overdraft evaluation only when capacity is 0 (paused) — always
  transient, always WAIT. It needs no overdraft policy → `pool.overdraftPolicy` is
  `nil` → the head's miss takes the FAST path (the `nil` early-out in `headGather`,
  no `walkCounts` proof). The common case.

  > **CORRECTION (PN, 2026-07-05, Layer-1 impl):** this "plain → `nil` policy → wait,
  > fast path" is WRONG against the code. `evaluateOverdraft` (permits.go) treats a
  > `nil` policy as **GRANT** ("non-implementing holdable: grant"), not wait — the
  > permissive default for a resource that opts out of overdraft. Plain
  > `semaphoreResource` implements `Overdraft` returning `(false,nil)`=wait PRECISELY
  > to override that nil-default grant, because granting is unsafe pre-step-4 (the
  > episode owner's own downstream dispatches wedge behind its episode;
  > `TestSemaphoreOverdraftPolicy` pins a paused semaphore to wait). So in Layer 1
  > **plain KEEPS its explicit wait policy** — the "shed `Overdraft` → `nil` → fast, no
  > proof" step is a PERF optimization deferred to the gather-walk-avoidance /
  > meta-redirect seam, where the `nil`→grant-vs-wait semantics get resolved. Layer 1
  > loses no correctness, only the not-yet-built fast path.
- A **weighted** semaphore: infeasibility can also mean `w > cap` — permanent at the
  current ceiling, a per-unit data error → REFUSE. Needs a real policy.

The semantic split converges with the performance fix (this is what makes it
compelling): plain = `nil` policy = fast; weighted = policy = pays the proof (rare,
heavy). Two constructors:

```go
func NewSemaphore(n int) Limiter                 // plain: nil overdraft, wait-only, FAST miss path
func NewWeightedSemaphore(n int) WeightedLimiter // paused→wait, oversized→refuse; pays the proof
```

**Compile-time enforcement (option ii, PN chose 2026-07-05).** The weigher pairing
takes a weight-capable limiter, so weighing a plain semaphore WON'T COMPILE — this
closes a silent-wedge hole: a `w > cap` demand on a `nil`-policy pool would hit
`nil`→wait and hang forever, exactly the failure the "weighted infeasibility is a
distinct per-unit error" rule exists to prevent. `NewWeightLimiter` (above) takes a
`WeightedLimiter`, not a bare `Limiter`.

**REVERSED (PN, 2026-07-05): plain and weighted limiters are fully SEPARATE — the
types do not cross-assign in either direction.** The earlier "a `WeightedLimiter` is
still usable weight-1 in `WithLimits`/`LimiterSet`" convenience is dropped. Rationale:
weight-capability exists because the resource measures something NON-uniform (bytes,
cost units), so using such a limiter weight-1 charges a meaningless "1 byte"/"1 unit"
per dispatch — almost always a mistake. The legitimate cases separate cleanly (a
concurrency cap is always weight-1 → plain `Limiter`; a byte/cost budget always
weighs → weighted), and genuine weight-1-on-a-weighted-pool survives EXPLICITLY as
`NewWeightLimiter(wl, func(T) int { return 1 })` — more honest than an implicit
cross-assign. Separation makes the option-ii guard automatic in BOTH directions:
plain→weighted won't compile (the silent-wedge hole) AND weighted→plain won't compile
(no accidental byte-budget-as-weight-1), with NO subtyping — so `Limiter` STAYS a
concrete struct (no interface-ification; the hot path is unchanged and the surface
stays allocation-neutral).

**Overdraft policy defaults:**

- plain semaphore: **Layer 1 keeps the explicit `Overdraft`→`(false,nil)`=wait method**
  (NOT `nil` — see the CORRECTION above: `nil`→grant, which is unsafe pre-step-4). The
  "shed to `nil` for the fast path" is deferred.
- weighted semaphore: `maxConcurrency == 0 → wait` (paused); else the proof
  guarantees zero inUse, so reaching overdraft means `w > cap` → `refuse` with a
  per-unit error. **STILL OPEN**: whether a weighted *concurrency* semaphore should
  soft-GRANT oversized instead (brief over-concurrency is harmless) rather than
  refuse — a memory limiter refuses (hard wall), a concurrency cap arguably grants.
  The split does not force this; it only requires weighted ≠ plain.

**Naming SETTLED (PN, 2026-07-05):** the weight-capable type is a **`WeightedLimiter`
interface from the start** (not a concrete `WeightedSemaphore`) — so weighted rate
limiters implement the same interface later with no breaking change. The concrete impl
is `*weightedSemaphore` (a pointer, so interface storage never boxes/allocates); the
interface is sealed via an unexported pool accessor. The concrete-`Limiter` /
interface-`WeightedLimiter` asymmetry is deliberate and accepted: plain stays a simple
sealed value, weighted gets extensibility. `WeightedLimiter` (weight-CAPABLE) and
`WeightLimiter[T]` (limiter+weigher pairing) coexist — the `-ed` and the `[T]` carry
the distinction; no rename.

**Separate perf note — the weighted-path `walkCounts`.** The full design for
bounding these walks — pool `nothingBorrowableSeq`, per-demand `notEnoughSeq`, and a
weighted-only per-cache tree index — is `gather-walk-avoidance.md`; the summary: for
weighted pools the head's proof re-establishment still runs `walkCounts`, and the
`touch` mechanism cannot prune it: `touch` fires on unsatisfied up-walks, not on the releases/steals
`walkCounts` reads, so an "untouched" subtree that just received a release holds
borrowable capacity a prune would miss — reopening the grant-while-capacity-exists
hole. The correct optimization is to fold the `anyInUse` check into the gather's
existing `searchList` traversal: when `acquireInto` returns nil, `searchList` has
just walked the whole forest confirming no borrowable, so `walkCounts`'s
`anyBorrowable` is redundant and only `anyInUse` is new — one walk instead of two.
Deferred until the weighted path is measured.

- **`weigh` is `func(T) int` against the receiver's own type parameter — checked by
  the compiler.** No boxed `any`, no construction-time type assertion; the
  wrong-`T`-weigher bug class is unrepresentable.
- **There is no static-weight form, and no construction-time infeasibility check
  (PN).** Cases with a non-1 weight but no need to weigh are rare, and a constant
  closure is trivial to write. The static-weight construction panic in
  `dispatch-execution-split.md` ("static `W > capacity` panics at op
  construction") is **dropped, not merely unenforceable** — PN never wanted it;
  strike that branch from the split doc's "Infeasible demand" section when this
  lands. All weighted infeasibility follows one rule: an item whose weight can
  never fit fails at runtime with a distinct per-unit error.
- **`opoption.go` dissolves.** `WithLimits` was the only `OpOption`; binding moves to
  the methods, so `OpOption`/`opConfig`/`resolveOpConfig`/`singleLimiter` are deleted
  and constructors drop `opts ...OpOption` (wrappers `NewFnLauncher`/
  `NewTaskLauncher`/`NewErrLauncher` slim accordingly). Migration is nearly
  verbatim: the option call `WithLimits(a, b)` becomes the method call
  `.WithLimits(a, b)`.
- **Every operation gets the full complement of the four binding methods (PN) —
  and flush limiters are dropped entirely (PN)**: no `WithFlush*` methods at all.
  A flush that needs limiting attaches the limiter to a launcher invoked from the
  flush body — the limited work then flows through the ordinary body path with
  its existing deadlock-freedom guarantees, instead of a special limited-drain
  mode. This dissolves the flush-weigher argument-type question (the framework
  holds only the opaque `Accumulator[T]` at flush admission — there was nothing
  natural to weigh), removes `WithFlushLimits` from the C3 backlog item, and
  removes permit-core.md's "parked holder whose drain itself needs a permit"
  model-check obligation for flush (strike the flush half of its "drain may now
  be limited — opt-in" paragraph at implementation). **Skimmer drain limiting
  (C3's limits on `NewSkimmer`) stays**, on PN's three-way contrast with flush: a
  skim handler has something to weigh (its typed result value, unlike the opaque
  accumulator); it is not already effectively committed by an upstream limited
  operation (a flush only drains what limited accumulates already admitted); and
  it executes in the user's context, which a launched body would not — so the
  launch-a-limited-op workaround would change execution semantics rather than
  merely relocate the limit. permit-core.md's limited-drain model-check case
  therefore remains, scoped to skim.
- **One limiter type (PN: no `AmountLimiter`).** The T/U weigher-pairing hazard
  (PN's "recipe for disaster") is unrepresentable without any limiter-kind
  machinery: a weigh function exists only inside `WeightLimiter[T]`, which flows
  only into same-`T` binding methods — no boxing anywhere (that died with the
  `OpOption` shape), and the untyped `LimiterSet` carries no weighers at all,
  which is precisely what lets it be untyped. An `AmountLimiter` kind split (a
  second static limiter type that only `NewWeightLimiter` accepts) was rejected
  because **there was no reason to do it — the rationale offered for it was
  invalid** (PN): the hazard it claimed to close, binding an amount-denominated
  limiter weight-blind, is a unit-coherence error that a type cannot check, so
  the split had no remaining justification.
- **Sets amortize canonicalization** — the always-copy cost (sort into canonical
  acquisition order + duplicate scan) is a property of the limiter *collection*,
  so it runs once per set at construction; sets are immutable after (a mutable
  shared set would be silent constraint modification with fan-out). A pure-set op
  binds by sharing the frozen state: zero per-op alloc, zero per-op sorting; an op
  that customizes (set + ad-hoc bindings) pays one bind-time merge of pre-sorted
  lists. The **single mixed set** (`LimiterSet[T]` holding both kinds — the first
  sketch) is rejected: its constructor has no argument witnessing `T`
  (`NewLimiterSet[T]()` forces explicit instantiation; a heterogeneous variadic is
  untypable; boxing reopens the T/U disaster). Splitting by kind makes each
  constructor's variadic homogeneous, so `T` infers from members everywhere it
  exists (`NewWeightLimiterSet()` with zero args is uninferable — and pointless,
  so acceptable). The naming lands as a family: `Limiter`/`LimiterSet`/
  `WithLimits`/`WithLimiterSet` ∥ `WeightLimiter[T]`/`WeightLimiterSet[T]`/
  `WithWeightLimits`/`WithWeightLimiterSet`.
- **Accumulation**: all four binding methods compose (everything bound applies —
  joint admission), and repeated calls of any of them **accumulate**:
  `WithLimits(a).WithLimits(b)` ≡ `WithLimits(a, b)` — the variadic is pure sugar,
  one appends-bindings law with no within-call/across-call distinction. This enables
  the layering pattern copy-builders exist for (a base op with shared limits,
  specialized copies appending more — the `.In` shape). Accumulation order is
  semantically irrelevant: joint admission acquires in the canonical *global*
  limiter order, never binding order. **Replace/last-wins is rejected** — silently
  dropping a bound limiter removes a safety constraint; likewise no removal
  affordance (a copy needing fewer limits is built from scratch, keeping
  constraint-removal loud).
- **Rules**: nil `weigh` panics (at `NewWeightLimiter` construction, and again at
  binding for a zero `WeightLimiter` value); a given limiter (shared
  `*permits.Pool` identity) may appear in **at most one binding total, across all
  four binding methods, all sets, and all calls** — a duplicate panics at the
  second binding (for sets: at set construction within a set, at bind across
  sets/ad-hoc); the multi-limiter not-yet-implemented panic fires at the second
  binding (earlier than today's dispatch-time `singleLimiter`); a weigher
  returning < 0 panics at dispatch (a weigher bug), while **zero is valid and
  means nothing need be acquired** — that binding is skipped for that dispatch
  (PN); oversized-vs-capacity remains the data case and gets the error.
- **Naming (PN, 2026-07-02)**: constructor is `NewWeightLimiter` — the `New*` prefix
  is unbroken across the package; a bare `WeightLimit` value-builder was considered
  and dropped for consistency. A general `Limiter` → `Limit` type rename was
  considered and **rejected**: "limit" already means the numeric ceiling throughout
  the package (`setMaxConcurrency(limit int)`, "raising the limit", capacity prose),
  so a `Limit` type would collide with that sense in adjacent code; the `-er` suffix
  disambiguates mechanism from parameter, matches ecosystem precedent
  (`x/time/rate.Limiter`), and keeps the design docs' "per-limiter"/"cross-limiter"
  vocabulary aligned with the type. The `WithLimits(...Limiter)` noun mismatch is
  effect-naming with stdlib precedent (`context.WithTimeout` takes a `Duration`).
- **Representation: every binding call copies into op-owned storage (PN,
  2026-07-02 — supersedes both the fixed-inline-array and adopt-the-variadic
  sketches).** Adopting the call-site variadic slice founders on the spread
  caller: `WithLimits(mySlice...)` passes the caller's slice directly, and later
  *element mutation* by the caller would silently alter the op's limiter set —
  silent constraint modification, the same failure class that rejected
  replace/last-wins. Capacity-clipping doesn't help (guards appends, not element
  writes), and documented-adoption (`io.MultiReader` precedent) is too weak a
  guarantee for a limiting API. A fixed inline array was rejected separately (caps
  the binding count, duplicates call-site storage, and bloats every op value by
  N×2+1 words copied on every op copy). So: one construction-time alloc per
  `With*` call, and the "ugh" is mitigated twice over. (a) The single-limiter cut
  keeps plain fields (`limiter Limiter; weigh func(T) int`) — no slice, measured
  **0 allocs** with every method inlining — so the alloc exists only once
  multi-limiter lands. (b) When it does, the constructor needs owned storage
  *anyway*: joint admission acquires in the canonical global limiter order and the
  duplicate-binding scan runs at bind time, so the copy is where validation and
  canonicalization (sorting bindings into acquisition order) happen — a
  dispatch-ready structure, not defensive overhead; adoption could never have
  survived canonicalization. Accumulation still **copy-merges, never `append`s** —
  the backing is op-owned but shared among op value copies, so diverging chains
  from a common base would alias through spare capacity. Dispatch only reads the
  fields. The one alloc no API shape avoids: a *capturing* weigher closure
  allocates once at creation, user-side; non-capturing weighers are static.
- **Evaluation**: `weigh` runs exactly once per dispatch, on the dispatching
  goroutine (where `value T` is in hand), stamped as an `int` on the work item —
  stable across postpone retries and the demand-FIFO identity, which assume a fixed
  registered weight. Past `Submit`, the typed closure never leaves the typed frame.

Rejected shapes from this design thread: variadic `WithLimits(...Limiter)` plus
parallel weight-annotation options (identity-matching misconfig class);
`WithLimit(l, ...LimitOption)` with `Weight(n)`/`WeighBy[T](fn)` (boxed weigher,
construction-time assertion instead of compile-time check); a static int form at all
(above); weigher-on-the-Limiter (breaks limiter sharing across ops of different `T`);
a `Weight(l) int` interface on `T` (intrusive; items shouldn't know limiters exist);
generic `OpOption[T]` (infects every option's call site).

## Interaction with limiter-resource-classes.md

- The `Adjust`/`balance` chain rules are already written in amounts (`balance −= w`
  on a step-3 check-out) and compose: the head's gather decrements by what it takes
  from the resource.
- While armed, chain wakes reaching non-head waiters cause a cheap fail-and-re-park
  (they hit the barrier). Routing resource wakes directly to the head is a deferred
  refinement, not a correctness need.

## Costs, stated plainly

- **Full pool serialization while armed** — the fairness-over-throughput trade,
  chosen deliberately. Bounded by assembly time, which head-only gathering itself
  minimizes (uncontested).
- **One atomic load** on the acquire hot path (mirroring the release-side balance
  load).
- **A never-yielding body stalls the pool while armed** (today it starves only the
  large waiter). The invalidatable identity is the escape hatch — a demand can carry
  a deadline and abandon.

## Model-check targets

- Barrier-pass induction (liveness restated above) under adversarial nesting.
- Gather conservation: hoard permits remain borrowable; abandoned hoards are
  steal-recoverable; `Σ held == checkedOut` through gather/occupy/abandon.
- FIFO no-starvation among registered demands; weight-1 progress across barrier
  epochs.
- Head invalidation mid-gather (hoard disposition, successor promotion).
- Identity ABA (stale head reference to a recycled demand).
- The old symmetric-livelock scenario, now expected unreachable (gather is
  head-only).

## Sequencing (gut-first discipline)

> Revised 2026-07-05: `TryAcquireUpTo` and the "capacity visibility for
> infeasibility" of the old step 3 are both gone — infeasibility is the overdraft
> zero-inUse proof (landed 2c), and partial grants were dropped (above). Steps 1–2
> (incl. 2c overdraft, 2d queue unification) are landed; the remaining order is the
> surface, then one consumable pass.

1. **Mechanical weighting** — parameterize `counts` deltas, `Acquire(w)`,
   `Permit.weight` — with every caller passing w=1: a provable no-op. LANDED.
2. **Gather + barrier + demand FIFO** behind the extended model check, then §Overdraft
   episodes (2c) and the queue unification (2d). LANDED + validated (sequential
   promise/grant rapid models, concurrent -race episode/suspension/churn stress).
3. **Surface plumbing** per "User-facing surface" above (`WithLimits`/
   `WithWeightLimits` builder methods + `WeightLimiter[T]` + the weighted/plain
   limiter split; `opoption.go` removal) — biggest churn, own session. Lights up the
   validated holdable weighted/overdraft core end-to-end (the sim can then dispatch
   w ≥ 2). NEXT.
4. **One consumable pass** — the consumable resource class
   (`limiter-resource-classes.md`: pass-through, no caching forest) + a rate limiter +
   the resource-driven wake (the settled contract: `TryAcquire(n) (bool, error)` —
   self-arm the wake on a `(false, nil)` miss and post `Adjust`, terminal refuse via
   the `error`; no `NotifyAt`, no `TryAcquireUpTo`), INCLUDING weighted consumables.
   Done as one pass AFTER the surface (step 3) so the consumable class is implemented
   once, with weighted support from the start, rather than a w=1 rate limiter now and
   weighted consumables later (PN, 2026-07-05).

## Rejected alternatives

- **`TryAcquireUpTo` (resource partial grants)** — PN, 2026-07-05. Not needed for
  correctness (the resource self-accounts for its own free capacity; a demand that
  would fit never reaches overdraft), and a pessimization (the overdrafting outlier
  gains nothing by taking the free fragment — it just buries easily-reachable
  free-pool capacity as cached-borrowable others must steal back; all-or-nothing
  correctly leaves it in the pool). Concentration is bounded by destroy-drain, not by
  a partial-grant primitive. See "Resource partial grants".
- **Running-max (or any weight-sensitive) succession** — PN: biases toward
  satisfying only large demands; inverse starvation of smaller registered demands
  under sustained large arrivals. FIFO + sticky head instead.
- **Supply-side reservation** (x/sync/semaphore-style earmarking of freed capacity).
  Violates "parked ⟹ borrowable"; reopens the deadlock cycle. The demand-side
  barrier reaches the same fairness without touching the proof.
- **Barrier gating only steps 3–4.** Cache-don't-return recirculates capacity
  through step-1 hits the Pool never sees; the head starves invisibly.
- **Freelance gathering** (non-head w ≥ 2 acquires assembling concurrently).
  Reintroduces gather-vs-gather livelock; head-only gathering makes it
  unrepresentable.
- **Per-call demand identity.** Double-registers the postponed manager's retries;
  the caller-held identity dedupes the demand's whole lifecycle.
- **Weight-1 demands registering in the FIFO.** Serializes ordinary saturation
  through the barrier — a common-case regression for zero benefit (single-permit
  demands need no assembly).
