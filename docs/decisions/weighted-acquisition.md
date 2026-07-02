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
> **Status: agreed design; not implemented.** The weighing *surface* (how ops weigh
> tasks) was already settled in `dispatch-execution-split.md` and is not revisited
> here.

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

- **The weighing surface** (`dispatch-execution-split.md`, "Infeasible demand"):
  **static weight** (fixed per op) with `W > capacity` is a misconfiguration —
  panic at op construction like the `WithLimits` checks; **data-dependent weight**
  (e.g. bytes from the work item) with an oversized item is a runtime condition —
  fail the unit with a distinct error so the caller can reject/split/route. Never
  allow-anyway.
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

## Decision 3: arm only for w ≥ 2

A weight-1 waiter never needs assembly — any single freed permit satisfies it — so
weight-1 demands neither register nor arm. Weight-1 contention keeps today's fully
concurrent machinery (wake-one + renotify conservation + steal). Without this guard,
ordinary weight-1 saturation would serialize the entire pool through the barrier — a
regression on the common case. With it, the whole mechanism is dormant for semaphore
and rate pools: one always-false branch.

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
- **Resource partial grants**: gathering wants "give me up to n"; all-or-nothing
  `TryAcquire(remaining)` forces a retry loop that never learns the resource has 2
  of the needed 3 — untenable at byte granularity. Add a capability interface
  (`TryAcquireUpTo(n int) int`), discovered by type assertion like
  `HoldableResource`; resources without it fall back to the retry loop.
- **Infeasibility is enforced *before* arming** (an infeasible head is a permanent
  world-stop): the static/data-dependent policy from `dispatch-execution-split.md`
  applies at registration, which requires capacity visibility from the resource —
  a small capability question to resolve with `TryAcquireUpTo`.

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

1. **Mechanical weighting** — parameterize `counts` deltas, `Acquire(w)`,
   `Permit.weight` — with every caller passing w=1: a provable no-op, landed green.
2. **Gather + barrier + demand FIFO** behind the extended model check.
3. **Resource capabilities** (`TryAcquireUpTo`, capacity visibility for
   infeasibility).
4. **Surface plumbing** of the already-settled weighing semantics (static per-op /
   data-dependent per-item) through the admission chain — separable, biggest churn,
   own session.

## Rejected alternatives

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
