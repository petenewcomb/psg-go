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
  world-stop): the runtime rule — distinct per-unit error for a weight that can
  never fit — applies at demand registration, which requires capacity visibility
  from the resource — a small capability question to resolve with
  `TryAcquireUpTo`.

## User-facing surface (settled 2026-07-02; sequencing step 4)

Weight is a property of the **(op, limiter) pair** — a shared memory Limiter needs a
different extractor per op — so the surface lives on the op. **Builder methods on the
op type, returning the op type** (the `In(wave)` copy-with-modification pattern;
`With` prefix per the `http.Request.WithContext` copy-semantics convention):

```go
// WeightLimiter[T] pairs a Limiter with this op-type's weigher — a reusable
// typed binding (define once, share across same-T ops). Weight is a property of
// the BINDING, not the limiter: the same Limiter may be bound weight-1 by one op
// and weighed by another.
func NewWeightLimiter[T any](l Limiter, weigh func(T) int) WeightLimiter[T]

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
var memByBuf    = NewWeightLimiter(mem, func(it Item) int { return len(it.Buf) })
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

1. **Mechanical weighting** — parameterize `counts` deltas, `Acquire(w)`,
   `Permit.weight` — with every caller passing w=1: a provable no-op, landed green.
2. **Gather + barrier + demand FIFO** behind the extended model check.
3. **Resource capabilities** (`TryAcquireUpTo`, capacity visibility for
   infeasibility).
4. **Surface plumbing** per "User-facing surface" above (`WithLimits`/
   `WithWeightLimits` builder methods + `WeightLimiter[T]`; `opoption.go` removal) —
   separable, biggest churn, own session.

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
