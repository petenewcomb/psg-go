<!--
Copyright (c) Peter Newcomb. All rights reserved.
Licensed under the MIT License.
-->

# rdvq outbox reclamation — productionization design

This is the design for item 1 of the rdvq `emptyOutboxes` productionization plan:
**reclamation / scale-down** of the destination-owned outbox pool. It is the
load-bearing missing piece — without it the live outbox set grows to peak
concurrency and never shrinks, so a long-lived destination leaks the very memory
the pool was meant to bound.

It builds on the committed prototype (47975c2): the `empty`/`filling`/`full`
state machine with a monotonic generation in one atomic word (`outbox.go`), the
`emptyOutboxes` hint queue, and the destination-owned `outboxPool`. Read
`docs/rdvq-outbox-recovery.md` first for why the hint queue exists; this doc is
only about returning idle outboxes to the pool.

## The problem, stated precisely

The **live set** is exactly the set of outboxes physically present on the
`outboxes` nbcq. An outbox enters it when first filled (`publishFull` with
`addToOutboxes=true`) and thereafter cycles `empty ↔ full` *in place* — the
non-blocking hint-claim path fills an empty outbox without ever removing it from
`outboxes` (`addToOutboxes=false`), and the drain path marks it empty without
removing it either. So nothing on the steady-state hot path ever shrinks the set.

A burst of N concurrent producers mints N outboxes; when load falls back to a
lower plateau, all N remain pinned on `outboxes` forever. They are not bounded by
cores (a producer parked on a full outbox costs no core), so the set can be
arbitrarily larger than current concurrency. Reclamation must return the surplus
to `outboxPool`, whose `sync.Pool` backing then lets GC reclaim it — the
scale-to-zero.

## Why reclamation can only happen at an `outboxes` front-pop

This constraint shapes the whole design, so it is worth proving. To reclaim
outbox X we must satisfy four conditions at once:

1. X is drained (`empty` state) — no value is in flight.
2. We hold exclusive ownership of X — no concurrent producer is about to claim it.
3. X's single `outboxes` entry is removed — otherwise the live set does not shrink.
4. Any stale `emptyOutboxes` hint to X is rendered inert.

Condition 4 is handled by the monotonic generation, but only if the hint carries
the generation it was minted at. An `emptyOutboxes` hint is an `outboxHint{ob,
gen}` — the generation X held when the receiver marked it empty — and the claimer
does `ob.claimEmpty(hint.gen)`, claiming at exactly that generation. Returning X
to the pool calls `Reset`, which bumps the generation, so a `claimEmpty(g)` at the
hint's old generation can never match (the generation only increases, so it can
never return to `g`). This holds even across queues, because `outboxPool` is a
process-global omnipool shared by every `Queue[T]`: a hint on queue A pointing at
an outbox now living in queue B fails its claim CAS because B obtained X at a
strictly higher generation.

This generation-on-the-hint is load-bearing, and getting it wrong is subtle. A
bare-pointer hint whose claimer reads X's *current* generation (`g, _ :=
ob.loadState(); ob.claimEmpty(g)`) would defeat the guard entirely: a stale hint
to a reclaimed X sitting in the pool would read X's current pool generation, claim
and fill it *there*, and put X in two places at once (in the pool and back in
flight) — a use-after-reclaim that surfaces as a never-drained channel inside the
non-blocking `TryPushBack` and an eventual deadlock. The bug is invisible without
reclamation (a hinted outbox is always live on `outboxes`, so the state CAS alone
arbitrates), so it only appears once outboxes can re-enter the pool.
`TestTryPushBackSaturation` is the regression guard. Note this is why only
`emptyOutboxes` needs a stamped generation: `outboxes`/`fullOutboxes` membership
is itself the liveness marker (an outbox is popped off them before it is pooled),
so a popped entry is always a live, exclusively-held outbox whose current
generation is safe to read; only the lossy hints persist across a reclaim.

Conditions 2 and 3 are the binding ones. nbcq is a Michael-Scott queue with no
interior removal: the **only** way to take X's entry off `outboxes` is for some
goroutine to `TryPopFront` it, and we must win `claimEmpty` so a concurrent
hint-claimer loses. Therefore:

> **Reclamation is necessarily a front-pop of `outboxes` followed by a
> generation-guarded `claimEmpty`; the goroutine that wins both then `Put`s X back
> to the pool.** Pop + claim + Put is one indivisible reclamation act.

The dangerous alternative — marking X reclaimable on the drain side and leaving
its `outboxes` entry dangling for later cleanup — is **unsafe**: if X is pooled,
re-obtained, and refilled while its stale entry still sits on `outboxes`, X is now
present on `outboxes` twice. A later `borrowToFill` reads the stale entry,
`loadState` returns X's *current* (legitimately live) generation and `full` state,
and `claimFull` *succeeds* — because borrowToFill always acts on the current
generation it reads, the generation guard offers no protection here. X would then
be paced/drained through two distinct queue slots: corruption. The "exactly one
`outboxes` entry per live outbox" invariant is load-bearing, and reclamation
preserves it only by being the one that pops the entry.

## The trigger: piggyback on a successful hint-claim

The win the prototype bought is precisely that the hot non-blocking path
(`TryPushBack` under the hint-claim model) **does not pop `outboxes`** — it claims
an empty outbox in place via an `emptyOutboxes` hint and only consults
`outboxes.Empty()` for the allocate-vs-refuse bound. So reclamation cannot reuse
the hint claim itself (which gives no `outboxes` handle), and it must not
reintroduce a per-push scan.

The other obvious front-pop site, `borrowToFill` (the blocking `PushBackFunc`
path), is the wrong place on its own. The workq `Post` usage split is: top-level
producers BLOCK → `PushBackFunc` → `borrowToFill`; nested producers LISTEN →
`TryPushBack` only. And even a top-level producer calls `TryPushBack` first,
falling through to `PushBackFunc` only when that refuses — i.e. only under
backpressure. So **`borrowToFill` runs only when `outboxes` is saturated with full
outboxes (no slack to reclaim) and never when slack exists.** Reclaiming only
there would shrink the set exactly never in the steady-low-load regime that
motivates reclamation.

The resolution turns the constraint into the signal. A **successful
`emptyOutboxes` hint-claim is itself the "we have slack" event** — it means a free
outbox was found and put to work, so we are not under pressure. Piggyback the
reclamation there:

> After `TryPushBack` claims an empty via a hint and delivers the value
> (`publishFull(..., addToOutboxes=false)`), run a bounded **reclaim probe**: pop
> the front of `outboxes`; if it is `empty` and `claimEmpty` wins, `Put` it
> (immediate reclaim); otherwise push it back.

This fires exactly when reclaim is wanted and affordable, auto-backs-off under
pressure (when claims miss, no probe runs), and self-paces to the push rate. It
runs *after* `publishFull` so the value is already observable to the receiver
before the O(1) housekeeping pop — reclamation stays off the delivery-latency
path.

### Why no counter and no floor are needed

The probe regulates itself from structure rather than from a global count. It pops
the *front* of `outboxes`, and "front is empty" already correlates with
over-provisioning: at high utilization most outboxes are in flight, so the front
is usually `full` (→ push back, no reclaim); when over-provisioned the empty
fraction is high, so the front is usually `empty` (→ reclaim). That is a
negative-feedback loop with a stable equilibrium at the active working set,
achieved with **zero global state**. The earlier instrumentation counters
(`empties`, `missRefusals`) and the bench `miss/op` metric are therefore deleted —
they were diagnostics for the recovery investigation, not inputs to this policy.

This deliberately keeps **no warm idle reserve**: the probe drives idle empties
toward zero, so the live set converges to the *active* working set (outboxes that
are `full`/`filling` at any instant), and a subsequent demand fluctuation
re-`Get`s from the shared `sync.Pool` (channel already made — cheap, no `make`).
An explicit idle floor would re-introduce the global state we just removed; since
a warm-pool `Get`/`Put` round-trip is cheap, v1 ships with no floor and lets the
overhead-regime benchmark say whether pool round-trips ever surface on the tail.

### Skip-self, bounded to two pops

The probe must not reclaim the outbox it just claimed. At probe time that outbox
is `filling`/`full`, so the state check usually skips it — but a fast receiver can
drain it between `publishFull` and the probe, leaving it `empty` again, and
reclaiming the outbox we just used is backwards (it just proved it is the hottest
in rotation). So the probe explicitly skips `claimed` by pointer.

Skipping is *not* a floor: `claimed` is the outbox in use, not an idle reserve, so
keeping it holds no spare capacity for the next claim. Its only roles are to avoid
reclaiming the in-use outbox and to keep the single probe productive when
`claimed` sits at the front of `outboxes` — by holding it off-queue and looking
one past it:

```go
// reclaimProbe runs only after a successful emptyOutboxes claim, so a spare was
// just confirmed available. It reclaims at most one genuinely-idle outbox, never
// the one just claimed. Bounded to two pops of outboxes.
func (q *Queue[T]) reclaimProbe(claimed *outbox[T]) {
	ob, ok := q.outboxes.TryPopFront()
	if !ok {
		return
	}
	if ob == claimed {
		// Hold the in-use outbox off-queue so the next pop is guaranteed a
		// different outbox; restore it as soon as we have the next.
		next, nok := q.outboxes.TryPopFront()
		q.outboxes.PushBack(claimed)
		if !nok {
			return // only the in-use outbox was present
		}
		ob = next
	}
	g, st := ob.loadState()
	if st == outboxEmpty && ob.claimEmpty(g) {
		q.reclaimOutbox(ob) // immediate reclaim; stale hint goes gen-safe on Reset
	} else {
		q.outboxes.PushBack(ob) // in use, or claim lost the race
	}
}
```

Holding `claimed` off-queue guarantees the second pop is a different outbox, so no
second pointer re-check is needed. The window where `claimed` is briefly off
`outboxes` is safe: it is `full` and still on `fullOutboxes`, so a concurrent
drain delivers its value and pushes a hint as usual; whichever order the drain's
`markEmpty` and our `PushBack(claimed)` interleave, `claimed` ends on `outboxes`
exactly once with consistent state. The productivity gain lands where it matters
most: `claimed`-at-front has probability ~1/N, rare when over-provisioned (large
N) but common near the working set (small N), which is exactly when surplus is
scarce and each probe should count.

### The reclaim primitive

```go
// reclaimOutbox returns a drained, owned outbox to the shared pool. The caller
// must hold it in a state no other path will act on (won via claimEmpty) and have
// already removed its outboxes entry. Put → Reset bumps the generation, inerting
// any stale hint.
func (q *Queue[T]) reclaimOutbox(ob *outbox[T]) { q.outboxPool.Put(ob) }
```

This mirrors the Checkpoint-2 `reclaimInbox` contract (caller owns + has removed
from all collections).

## Deferred: the `retiring` relief accelerator

The hint-claim probe covers the steady and wind-down regimes. It does *not* fire
in the transient relief regime (`borrowToFill` block-fill, fresh-alloc), because
those paths run under backpressure and re-queue a *full* outbox, which cannot be
`Put` (it holds a value). A clean handle exists if we want it: a fourth state
`retiring`, set in place of `q.outboxes.PushBack(ob)` at those re-queue points,
meaning "not on `outboxes`; on next drain, `reclaimOutbox` instead of pushing a
hint." It is cheap — the state word already reserves two generation bits
(`genShift = bits.Len(3) = 2`), and `retiring = 3` is the fourth value in those
same bits with no narrowing.

It is **deferred**, not adopted: relief-regime surplus is mopped up by the
hint-claim probe as soon as slack returns, so `retiring` only buys *faster* shrink
during an active backpressure episode. Add it only if a benchmark shows
relief-regime shrink lag actually matters; until then the steady-state probe is
the whole mechanism and carries no extra state.

## Invariants and hazards (validation targets)

- **Exactly one `outboxes` entry per live outbox.** Preserved: every probe pop is
  paired with either a reclaim (entry gone, outbox pooled) or a push-back (entry
  back, once); `publishFull` adds once. Reclamation never leaves a dangling entry.
- **No double-Put.** An outbox is `Put` only by the goroutine that won its
  `claimEmpty(g)` after popping its entry; the claim CAS serializes contenders, so
  exactly one reclaimer fires per `empty` incarnation.
- **No use-after-reclaim.** A refill bumps the generation (`finishFill`), beating a
  reclaimer's `claimEmpty(g)`; a reclaim bumps it (`Reset`), beating a
  hint-claimer's `claimEmpty(g)`. The single atomic state word arbitrates; the CAS
  loser pushes back or drops harmlessly.
- **Cross-queue hint safety.** Covered by the monotonic generation through the
  shared omnipool. Worth an explicit test: reclaim on queue A, re-obtain on queue
  B, fire A's stale hint, assert no delivery to B.
- **Skip-self.** The probe never `Put`s `claimed`, even if a drain race leaves it
  `empty` at probe time.

Validation plan (item 4): rdvq short + `-race` + full stress; `TestBySimulation`
full `-race` + deep `rapid.checks` sweep (extend its outbox-accounting invariant
to assert the live set tracks concurrency and the single-entry invariant holds);
and the overhead-regime `BenchmarkQueueEmit` run to confirm the probe adds no
throughput regression and no tail cost from pool round-trips. The existing
`outboxpool_reclaim_proto_test.go` "every value received exactly once under
`-race`" check is the seed for the use-after-reclaim guard.
