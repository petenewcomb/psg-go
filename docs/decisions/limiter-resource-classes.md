# Limiter resource classes: consumable vs holdable, and the resource-facing contract

> Decision record (2026-07-02). Captures the design for generalizing the limiter beyond
> semaphores — rate limiters, external-gauge limiters, memory limiters — without
> compromising the permit forest (`docs/permit-core.md`, `internal/permits`). Headline:
> the forest's machinery (cache-don't-return, inheritance, steal) is a theorem about
> *conserved, durable, revalidation-free* permits; only the semaphore family satisfies
> its premises. Other classes get a pass-through path selected by a construction-time
> capability check. The resource-facing seam is a single signed verb — `Adjust(delta)`
> posting to a `balance` counter — whose positive side drives a serialized wake chain
> (no `WakeAll`, no per-waiter demand bookkeeping) and whose negative side is a reclaim
> debt that the forest pays down as work completes. **Status: agreed design; not
> implemented.** The live `permits.Resource` interface, `NewSemaphore`, and the gate
> paths in `limiter.go` predate this record.
>
> **Sequencing (PN, 2026-07-05):** this consumable class is the framework's first
> non-semaphore resource, and a **rate limiter is its intended first instance**. It
> lands as the **one consumable pass** at the end of the weighted-acquisition
> sequencing — *after* the weighted/plain surface is in place — so the class is built
> once with weighted-consumable support from the start (`weighted-acquisition.md`
> §"one consumable pass"). The resource-facing interface settled 2026-07-05 — see
> "Resource contract, settled" below: `NotifyAt` and `TryAcquireUpTo` are both dropped,
> the wake for a consumable is the self-arm on a failed `TryAcquire` (Decision 3's lazy
> arm) posting `Adjust`, and its feasibility is `TryAcquire`'s new `error` return.

## Context: what the forest actually assumes

The permit forest's three superpowers each rest on a premise about the permits
themselves:

- **Cache-don't-return** assumes an idle checked-out permit remains a *valid claim on
  capacity* indefinitely — durable.
- **Inheritance and the steal** (acquire steps 1, 2, and 4) occupy an existing permit
  *without consulting the Resource* — revalidation-free.
- **Conservation** (`Σ held == checkedOut ≤ capacity`) assumes permits are tokens that
  exist until returned — conserved.

A counting semaphore (including a weighted one — the `atomic128` counters are already
`uint64` amounts) satisfies all three. The two limiter classes we want next each violate
a different premise:

**Rate limiters break conservation.** A rate token is *consumed at admission*, not held
for a duration. Under the forest unchanged: a finished body's token cached in `held` is
a spent event masquerading as idle capacity — inheritance and steal would grant
admissions at some multiple of the configured rate, silently (no existing invariant even
typechecks conceptually for a bucket). The park/resume alternation ("reacquire your base
on return from the drive") double-charges a second token for the same admission. And the
gate's postpone path (`limiterScatterWork.Execute`: release the permit while the work
waits for queue space, re-acquire on retry — "Acquire is state-free") consumes a fresh
token per retry. `Release` itself is meaningless: a consumed token cannot be given back.

**External gauges break revalidation.** Capacity floats with the outside world
(downstream queue depth, memory watermark), independent of `Release`. Occupying a cached
or inherited permit skips the Resource, so admission happens against an arbitrarily
stale capacity check. And since the design rejects active recall, a falling gauge has no
way to bite on cached idle capacity — `Σ held` stays high until caches destroy.

A **pure** external gauge turns out to be *consumable-shaped*: if the gauge reads a
metric that the running work itself inflates, the "hold" lives in the world — the
feedback loop closes through the metric, and there is literally nothing to release.
`TryAcquire` is a fresh threshold check; done.

**Memory limiters are holdable but drift.** Reservation-style memory limiting is a
conserved weighted semaphore — the forest applies in full. But its capacity is an
*estimate* over a shifting substrate: bodies use more or less than their declared
weight, the GC returns memory on its own schedule, other processes eat the headroom.
When real capacity shrinks below `checkedOut`, lowering the internal ceiling only gates
step 3; the forest's cached idle permits remain occupiable via steps 1/2/4, so bodies
keep admitting against memory that no longer exists. The resource needs a shrink lever
into the forest.

## Decision 1: two resource classes, discovered by type assertion

```go
// Resource is the minimal admission contract every limiter class implements.
type Resource interface {
	TryAcquire(n int) bool
}

// HoldableResource is the conserved-token extension: every successful TryAcquire
// must eventually be matched by a Release. Only holdables get the caching forest.
type HoldableResource interface {
	Resource
	Release(n int)
}
```

The Pool asserts **once, at construction**, and stores the narrowed result:

```go
p.holdable, _ = r.(HoldableResource) // nil ⇒ consumable
```

Hot paths test a nil field, not a repeated interface assertion, and the *entire policy
bundle* keys off that one immutable construction-time fact:

| policy | holdable | consumable |
|---|---|---|
| forest | full caching forest | pass-through (no caches) |
| gate postpone | release permit, re-acquire on retry | charge rides with the postponed work |
| park/resume | release on park, reacquire on resume | charge-once-at-intake; alternation is a no-op |
| `Permit.Release` | `inUse−−`, permit stays cached | handle evaporates; nothing returns |
| negative `Adjust` | reclaim debt (Decision 4) | panics — no `Release` currency to pay with |

**The base is named `Resource`, not `ConsumableResource`.** The embedding reads as
"holdable is-a resource," which is true for the method set but false for the calling
protocol: code written against the consumable protocol never calls `Release` (leaking a
semaphore permit forever), and code that releases a rate token is an error. Neither
class substitutes for the other — the assertion is a *capability discriminator*, not
polymorphism, and the Pool runs a different protocol per class. Naming the base
`Resource` also narrows the existing `permits.Resource` rather than renaming it.

**The compiler enforces half the contract for free.** With `Release` absent from the
base interface, the consumable path *cannot* be written to release — the bug class where
a rate token gets "returned" is unrepresentable, strictly better than a `Release` that
panics or no-ops.

*Caveat (accepted):* structural typing means implementing `Release(int)` opts a resource
into the holdable protocol silently. Non-risk in practice — `Limiter` is sealed,
resources are framework-authored in a closed internal package, and the constructor set
is small and deliberate. A marker method can harden this later if the package ever
opens.

### Resource contract, settled (2026-07-05, multi-session design)

Three sessions of whittling landed the final resource-facing shape. It **supersedes
the `bool`-only `TryAcquire` above** and **drops both `TryAcquireUpTo` and `NotifyAt`.**

```go
// Resource: the base admission contract. The error carries TERMINAL infeasibility
// the resource is certain of independent of any gather.
type Resource interface {
	TryAcquire(n int) (bool, error)
}

// HoldableResource: conserved tokens (the caching forest) + Release + the
// ask-dependent overdraft decision.
type HoldableResource interface {
	Resource
	Release(n int)
	Overdraft(n int) (granted bool, err error)
}
```

`TryAcquire(n)` returns three signals:

- `(true, nil)` — granted from the free pool.
- `(false, nil)` — not from free, **overdraft still on the table**. For a *holdable* the
  Pool then gathers the forest (which may satisfy `n` with no overdraft at all) and,
  only if that exhausts under the zero-inUse proof, calls `Overdraft`. For a
  *consumable* there is no forest, so this means "not enough yet — wait," and the
  resource **self-arms** its wake: it remembers the rejected `n` and arms a timer (or
  a gauge poll), posting an `Adjust` when its level reaches `n`. That self-arm is
  exactly Decision 3's "a failed `TryAcquire` is the demand signal," and it is what
  makes `NotifyAt` unnecessary — the rejection already carries the size, and the
  barrier guarantees `n` is the head's.
- `(false, err)` — TERMINAL refusal the resource is certain of regardless of the gather
  (a consumable's `n >` its bucket ceiling; a hard-wall holdable's "no overdraft,
  ever"). The unit fails with `err`. The gather is not aborted — `err` bites only if
  the gather exhausts — but a terminal `n` will exhaust it.

`Overdraft(n)` stays a **separate post-gather call, and must, because the true ask is
only known there.** For a holdable the `n` handed to `Overdraft` is `w` minus what the
gather assembled from the resource-INVISIBLE forest borrowable, so the resource cannot
compute it at `TryAcquire`. Its three outcomes are all ask-dependent — **grant** (a
soft cap chooses to exceed), **wait** (`granted=false, err=nil`: paused, wait for a
raise), **refuse** (`err`: e.g. a soft margin `M` that refuses iff `n > M`) — so all
three need `n` and belong here, not at `TryAcquire`.

**Channel-choice rule.** `TryAcquire err` and `Overdraft` are *alternative* feasibility
channels: `err` is a refusal you are certain of **independent of the ask** (a
fast-fail); `Overdraft` is a decision that **depends on the post-gather ask**. Return
`err` only where you would refuse regardless of `n`; if the outcome depends on `n`,
return `(false, nil)` and decide at `Overdraft`. A resource MAY use both **consistently**
— e.g. an instance whose *configuration* disallows overdraft entirely legitimately
fast-fails at `TryAcquire` with `err` while the type still carries the `Overdraft`
method (dormant for that instance) — as long as they agree. The sole incoherent case is
erring at `TryAcquire` where `Overdraft` would have **granted** (pre-refusing a demand
you'd have granted). Not structurally enforced — both live on the same resource — so a
documented rule; benign if tripped (the `err` just wins → a stricter policy than
written, not a crash).

**Dropped by this contract:**
- **`TryAcquireUpTo`** — `weighted-acquisition.md` Rejected alternatives: unnecessary
  (the resource self-accounts for its own free; a demand that would fit never reaches
  overdraft) and a pessimization (it buries reachable free capacity as cached-borrowable
  others must steal back; concentration is bounded by destroy-drain).
- **`NotifyAt`** — folded into `TryAcquire`'s `(false, nil)` self-arm (wake) plus `err`
  (terminal refuse). A consumable's ask is `w`, known at `TryAcquire`, so it needs no
  separate feasibility+wake call.

## Decision 2: consumables are a degenerate forest, not a special case

Pass-through needs no branching in the search code: with nothing ever retained
(`Permit.Release` returns nothing to any cache, and no caches exist), steps 1, 2, and 4
auto-miss and **every acquire lands on step 3 — a fresh `TryAcquire` at the Resource** —
which is simultaneously the fresh check a gauge needs and the exactly-once consume a
rate token needs. A consumable Pool allocates no caches at all; `ensureCache` /
`wavepermits.go` forest construction is skipped for it. The Pool-side split is
internally consistent: every path that calls `Resource.Release` today (cache destroy's
CAS-drain, the permit-handle release) exists only for the caching forest, which only
holdables get.

## Decision 3: the wake contract — a signed balance and a serialized wake chain

Today's wake model strands non-semaphore waiters: the Pool wakes parked `AcquireWait`s
on `Permit.Release` and cache destroy, but rate capacity appears with *time* and gauge
capacity with *external events* — under the current contract a blocked acquire on a
drained bucket parks forever. The semaphore's `capacityChangedFn = p.WakeAll` wiring
(`NewSemaphore`) is the embryo of the fix. The generalized contract:

- **The resource owns all wake production.** Timers (rate pacing, burst policy), gauge
  callbacks, ceiling changes — the Pool never learns that time exists. This keeps timers
  behind the resource seam where the simulation can virtualize them, and matches the
  Design B precedent of the queue owning its deadline timer.
- **Timers arm lazily: a failed `TryAcquire` is the demand signal.** A rate resource
  arms its timer on the first failing acquire and lets it lapse when uncontended — no
  pacing machinery runs while the bucket keeps up.
- **The Pool owns the single park point.** Blocked acquires wake uniformly on forest
  events (a permit went borrowable) and resource events through one waiter set — no
  split registration, no new missed-wake seam.
- **The surface handed to the resource at `NewPool` is one signed verb,
  `Adjust(delta int)`**, replacing `capacityChangedFn`. It posts `delta` — the change in
  resource capacity, in `TryAcquire` units, relative to what the Pool has already acted
  on — to a signed atomic **`balance`** counter, and (when the balance is newly
  positive) sends **a single wake**. Concurrent adjusts net arithmetically.

### The wake chain (balance > 0)

At most one resource-originated wake is in flight at a time — never a fan-out. This is
the `execpool` demand-counter pattern (ramp toward a balanced counter, one signal at a
time) applied to wakes. **The balance is a delta ledger owned by the resource; the Pool
only transacts against it** (decrements on chain admission, credits on debt repayment)
**and never unilaterally rewrites it.** Wakes are seeded per-event, not by a
zero-crossing edge — standing residue (see rule 3) must not mask a fresh announcement.
A wake fires on exactly three occasions:

- **seed:** any positive `Adjust`, when no chain is already running;
- **link:** a chain member's success-forward (rule 2);
- **deferred seed:** a registering waiter confirming against a positive balance — the
  register-then-confirm discipline `blockAcquire` already uses, closing the missed-wake
  race and covering capacity announced while no one waited (the first later arrival
  confirms via step 3, decrements, forwards, and the chain resumes for the rest).

The chain rules:

1. **A woken waiter re-runs the ordinary acquire.** On success *at step 3* (resource
   check-out of amount w): `balance −= w`. A waiter satisfied from cache or steal
   (steps 1/2/4) consumed no resource capacity and does **not** decrement.
2. **Success always forwards exactly one wake**, regardless of the balance. That
   terminal probe (which fails and stops, rule 3) is what lets a resource post
   `Adjust(1)` for capacity of *unknown* size — a gauge threshold crossing — and have
   the chain discover the true extent. Cost: at most one spurious wake per event.
3. **Failure stops the chain — and nothing else.** The failed waiter re-parks and does
   not forward; the balance is **not** clamped. A failed acquire proves capacity is
   absent *now* (typically raced away by step-3 acquirers that never parked and so
   never decremented), but the residue is the resource's ledger, and rewriting it
   corrupts netting: a resource that posts `+5`, sees the headroom raced away, and
   later posts `-5` to record the shrink expects net zero — a clamp would turn that
   into phantom debt and a double-counted harvest. The residue is behaviorally cheap
   to keep because the chain is *failure-terminated, not balance-terminated*: a
   stale-high balance never lengthens a chain. No waiter is stranded by stopping:
   forest events (a permit going borrowable) wake independently, and future capacity
   produces a future `Adjust`, which re-seeds.

Termination: every hop either admits (progress) or is the terminal failure; chain
length ≤ admissions + 1, independent of the balance's magnitude. An undeliverable wake
(no waiters) leaves the balance standing for the deferred seed to consume.

**Precision is asymmetric, deliberately.** The negative side is exact — debt changes
only via `Adjust` and repayment drains, both Pool-visible. The positive side is a lossy
hint: step-3 racers consume announced capacity without decrementing (they never parked,
and ordinary free capacity is indistinguishable from announced capacity at the
resource), so residue accumulates. Its costs are bounded — one futile probe per seeded
chain, and a later `Adjust(-n)` nets against stale surplus, under-harvesting by the
residue and delaying the debt's bite until the resource's own `TryAcquire` gating and
subsequent posts absorb it. If measurement ever shows the drift material, the seam is a
reconciliation read (resource-visible balance), not a redesign.

**Why no `WakeAll`, anywhere.** Enumerating the events: ceiling raise by known k →
`Adjust(k)`, chain wakes exactly the admittable waiters; token maturation →
`Adjust(1)` per token (or `Adjust(k)` for a late-timer burst); gauge crossing of
unknown size → `Adjust(1)` + rule 2. Even the Pool-internal `WakeAll` at destroy's
multi-permit drain converts: post the drained `held` to the same balance. Broadcast
under capacity/waiter mismatch is a thundering herd (N wake, N−1 fail and re-park);
the chain wakes (admissions + 1) waiters by construction. The accepted cost is
serialized inter-wake latency — a park/wake handoff plus one acquire per hop — on a
path that is by definition cold (saturation with parked waiters, on rare capacity
events).

**Why no per-waiter demand bookkeeping.** The previous draft of this record specified
fit-matched delivery (wake the front waiter only if its declared need fits, à la
`x/sync/semaphore`), requiring every registration to carry its awaited amount — either
peek-before-commit surgery on the shared `rdvq` substrate or a parallel permits-level
registry. The chain makes *the acquire itself the fit check*: each woken waiter takes
what it needs and forwards. No fit policy (FIFO-no-skip vs best-fit starvation
trade-off), no amount plumbing, no rdvq changes. See Rejected alternatives.

## Decision 4: negative balance is the reclaim debt (holdables only)

`Adjust(-n)` is the shrink lever — it subsumes the separate `Reclaim(n) int` verb an
earlier draft specified. Semantics:

- **Immediate idle harvest at post time.** A debt cannot be purely lazy: cached-idle
  permits generate no future release events, so a lazy debt would strand against them
  until wave destroy. `Adjust(-n)` first runs the reclaim walk — **a steal whose
  beneficiary is the Resource**: the steal search verbatim (front-to-back coldest-first
  LRU, first-borrowable victim, revalidating `stealOut` CAS) composed with the drain
  `destroy` already does (CAS `held` back through `checkedOut`, `resource.Release`).
  Only the un-harvestable remainder — permits currently `inUse` — stays as negative
  balance.
- **Releases pay the debt before caching.** While `balance < 0`, every `Permit.Release`
  drains the released permit to the Resource (backing cache `held−−`, `checkedOut−−`,
  `resource.Release(w)`, `balance += w`) instead of leaving it cached. This is a
  *targeted suspension of cache-don't-return*, and it is correct rather than merely
  convenient: the resource has declared systemwide capacity loss, so caching for
  locality is precisely wrong until the debt clears. Hot-path cost: one atomic load of
  the balance per release — the same shape as the existing waiter-count gate that keeps
  the uncontended release cheap.
- **Wakes are suppressed while `balance ≤ 0`** — waiters legitimately wait longer
  because there is genuinely less capacity.
- **Repayment needs no new resource-side machinery.** The debt liquidates as ordinary
  `resource.Release(w)` calls, which the resource's own accounting already understands.
  The resource never polls for a shortfall; it posts the delta and observes repayment
  through its normal interface.
- **Holdables only.** Debt is paid in `Release` currency, which consumables don't have
  (a consumed rate token is gone; a pure gauge holds nothing). `Adjust` with a negative
  delta on a consumable Pool panics.

This stays on the right side of `permit-core.md`'s rejected-alternatives line, but
sharpens it: what was rejected was *recall of in-use permits* — preemption-shaped, with
a return obligation and a second protocol. The harvest takes only the **borrowable
subset** (the steal we already committed to, pointed at a different sink), and the debt
waits for `inUse` permits to be released by their own bodies. `inUse` stays untouchable:
estimation error on a *running* body is unfixable by any limiter, so in-flight work
drains naturally — the same line `SetMaxConcurrency` already draws. For lock-order
analysis the harvest *is* a steal: same per-list locks, same root→leaf order — it widens
nothing.

**`SetMaxConcurrency` gets sharper for free.** Today a lowered ceiling barely bites
until caches destroy — cached idle permits keep a long-lived wave's subtree
self-admitting at the old ceiling indefinitely. Under this contract, lowering posts
`Adjust(-(k_old - k_new))`: idle harvested immediately, the rest as work completes. And
lowering stops waking waiters into a guaranteed re-park (today's `capacityChangedFn`
fires on lowering too — pure churn).

**Liveness weakens honestly, not accidentally.** A harvest can take the last borrowable
permit out from under a parked `AcquireWait`; the waiter re-searches on the next wake
and finds genuinely less capacity — because there *is* genuinely less. Same "liveness
conditional on the world" bucket as the gauge, alongside infeasible demand. The
deadlock-freedom argument survives: blocked acquires are behind either running bodies
(which yield) or real capacity loss (which is the limiter doing its job). The
revalidating CAS guarantees the harvest never takes an `inUse` permit and never races a
concurrent occupy into a wrong grant — inherited from the steal path rather than
re-proven.

## Joint admission ordering

In the canonical global acquisition order (`permit-core.md`, "Cross-limiter joint
admission"), **holdables sort before consumables**. The lock-ordering deadlock argument
holds for any total order, and this way a joint admission never consumes a rate token
only to postpone on a semaphore miss — the abort path stays refund-free. Consumable
resources also never block others while "held" (they aren't), so they are safe last.

## Mixed semantics compose; no third class

A resource that wants both a conserved reservation *and* a fresh external check ("admit
while gauge < X, tracking our own reservations") is not a new class — it is two limiters
AND-composed via the existing `WithLimits`: a holdable reservation semaphore plus a
consumable gauge, with the ordering rule above. This keeps `holdable ⟺ cacheable` exact
and avoids a per-occupy revalidation hook in the forest.

## Spec and code updates this implies (when implemented)

- `permit-core.md`: invariants are semaphore-shaped (`Σ held ≤ capacity` is meaningless
  for consumables — a bucket has no fixed C); scope them to holdable Pools, and extend
  the holdable conservation statement to cover the debt drain. Sharpen "Rejected: active
  recall" to distinguish harvest-idle + debt (in) from recall-in-use (still out).
- `limiter.go`: the postpone-release in `limiterScatterWork.Execute` and its "Acquire is
  state-free" comment are holdable-only; consumable charges ride with the postponed
  work. `NewSemaphore`'s `capacityChangedFn = p.WakeAll` migrates to `Adjust` (raise →
  positive, lower → negative, no wake churn on lowering).
- `internal/permits`: `Pool.WakeAll` and its destroy-drain call site convert to the
  balance; guard the seam before `NewRateLimit` exists — the current interface happily
  accepts a naive rate implementation whose failure mode (cached spent tokens
  over-admitting at N× the rate) is invisible to every existing invariant and test.
- Model check: the forest invariants stay for holdables plus new debt properties
  (ledger conservation — the Pool's transactions against the balance sum with the
  resource's posts; wake-chain termination; no stranded waiter under residue — the
  per-`Adjust` seed, deferred seed, and forest wakes jointly cover). Consumable Pools
  need their own (small) property set — exactly-once consume, no retained state,
  postpone does not re-charge.

## Struct mapping (internal/permits)

The design lands almost entirely in `Pool`; **`Cache` and `counts` change not at all**,
which is the concrete form of "the forest is untouched; other classes route around it."

- **`Resource`** becomes `TryAcquire(n int) (bool, error)` (the settled contract
  above — the `error` is consumable/hard-wall terminal refuse); `HoldableResource` adds
  `Release(n int)` and `Overdraft(n int) (bool, error)`. Holdable `TryAcquire` returns a
  nil error (its feasibility is the gather + `Overdraft`); the existing overdraft plumbing
  in `permits.go` already has `OverdraftResource.Overdraft`, so this pass folds it under
  `HoldableResource` and drops the standalone `NotifyAt`. The only internal `Release`
  caller — `destroy`'s drain — becomes `p.holdable.Release`, unconditionally safe
  (destroy runs only on caches; consumable pools have none).
- **`Pool` gains three fields**: `holdable HoldableResource` (nil ⇒ pass-through; set
  once in `NewPool`, nil-tested on hot paths), `balance atomic.Int64` (the signed
  ledger — int64, unlike the uint64 `counts` amounts), and `chain atomic.Bool` (CAS-
  guarded "resource-originated wake in flight" — the at-most-one-chain token).
  `roots`, `notify` (still the only park point), and `cachePool` are unchanged.
  `WakeAll` is deleted; `Adjust(delta)` and an internal `credit(n)` (shared by positive
  posts and destroy's drain, replacing its `WakeAll`) are added. The negative-`Adjust`
  harvest is `searchList` + `stealOut` + `holdable.Release` — the existing steal
  machinery with the Resource as sink; no new synchronization.
- **`Permit.Release` is the one hot-path change**: after `counts.release()`, one atomic
  load of `balance`; if negative, attempt `counts.stealOut()` on the *own backing
  cache* and drain that permit to the Resource (`holdable.Release(1)`;
  `balance.Add(1)`) instead of caching + waking. A concurrent acquire winning the
  just-released permit fails the CAS benignly — the debt catches the next release.
  Repayment overshoot under concurrency lands as positive residue, already tolerated.
- **Micro-decisions surfaced** (implementation-time): `Permit` shape for consumables (a
  second `pool *Pool` field for a cache-less held marker, vs a separate handle at the
  streampool layer); chain-duty attribution (any woken waiter checks `p.chain` —
  simple over-approximation — vs a chain-marked `rdvq.Notification`); `acquireInto`
  must report *which arm* satisfied (rule 1 decrements only on the step-3 check-out,
  never cache/steal hits); consumable pools need `Pool.Acquire`/`Pool.AcquireWait`
  entry points (the step-3 + park loop with steps 1/2/4 absent) since `Cache.Acquire`
  presumes a cache.

## Rejected alternatives

- **`ConsumableResource` as the base interface name.** Implies holdable is-a consumable;
  behaviorally neither substitutes for the other (see Decision 1).
- **`WakeAll` in the resource-facing surface** — and now anywhere. No surviving use
  case (see Decision 3); broadcast under capacity/waiter mismatch is a thundering herd,
  and the balance-driven chain covers batch and unknown-size events self-meteringly.
- **Counted fan-out `Notify(n)`** (the previous draft of this record). Wakes up to n
  waiters in parallel; needed a fan-out-bound policy for large weighted n (to keep
  `WakeAll` from sneaking back in), still herded under mismatch, and pushed toward
  per-waiter demand bookkeeping for weighted acquire. The balance + chain gets the
  batch case with strictly less machinery, at the cost of serialized inter-wake latency
  on a cold path.
- **Per-waiter demand bookkeeping / fit-matched delivery** (`x/sync/semaphore`-style
  wake-front-if-fits, registrations carrying their awaited amount). Superseded: the
  chain makes the acquire itself the fit check, so neither the peek-before-commit
  change to the lock-free `rdvq` substrate (cf. the gen-stamped-inbox saga; the
  substrate deliberately serves heterogeneous amount-free waiting,
  `waiter-set-notification.md`) nor a parallel permits-level needs registry is needed.
  Its one residual advantage — skipping a too-big waiter to wake a smaller one behind
  it — is a utilization/starvation trade-off we now don't have to adjudicate.
- **Wake-with-grant** (deliver the capacity itself with the wake, a direct handoff
  eliminating the re-acquire race). The limit case of precision; deferred-rejected
  because it moves conservation accounting into the delivery path (the granted amount
  must land in the waiter's cache atomically with the wake) and tangles with
  cache-don't-return locality. The chain gets most of the benefit without touching
  conservation.
- **A standalone `Reclaim(n) int` verb** (the previous draft). Subsumed by the negative
  balance, which is strictly stronger: the synchronous version could only harvest
  currently-idle permits and returned the shortfall for the resource to retry/poll; the
  debt self-liquidates as bodies release, with repayment arriving through the
  resource's ordinary `Release` interface.
- **Resource-owned `Listeners` that the Pool subscribes to.** `rdvq.Listeners`
  registrations are one-shot FIFO pops, forcing a standing subscription through a
  rendezvous primitive (re-register per fire); worse, waiters registering on the
  resource's set directly would split the park point (forest events vs resource events)
  and open a missed-wake seam. Production stays resource-owned; the *pipe* is
  Pool-supplied.
- **A per-occupy `Revalidate`/`CanOccupy` hook to keep gauges cacheable.** The caching
  buys nothing when every occupy must hit the Resource anyway; pass-through is strictly
  simpler and makes the fresh check structural.
- **A third resource class for mixed reservation+gauge semantics.** Composition via
  `WithLimits` already expresses it (see above).
- **`Release`-as-refund for rate tokens** (to keep one interface). Defining refund
  semantics just to let the postpone path release-and-re-acquire re-introduces the
  double-charge hazard it papers over; charge-rides-with-the-work is the correct rate
  semantics and needs no refund.

## Deferred

- The verb name. `Adjust(delta int)` over a signed `balance` is the working choice
  (`Notify` no longer fits a verb that can revoke); alternatives (`Offer`, `Credit`)
  welcome before implementation.
- Whether ordinary forest wakes (`Pool.wake` on release-to-borrowable) eventually
  unify with the chain, or stay a separate single-wake path. They are conserved and
  cheap today; unification is cleanliness, not necessity.
- Concrete `NewRateLimit` / gauge constructors and their resource implementations
  (token-bucket policy, gauge polling cadence) — each is a small accounting object
  behind the seams above.
