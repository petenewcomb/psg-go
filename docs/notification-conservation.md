# Notification Conservation

**Status: settled design, 2026-07-16.** This document specifies the notification
model that the work queues (`internal/workq`), the rendezvous queues
(`internal/rdvq`), and the permit pools (`internal/permits`) share. The model is
older than this document: its principle has long been stated in
`decisions/backpressure-and-reentrancy.md` ("Notification Conservation and
Cross-System Backpressure"), and the shape described here is the one proven out
at the net-zero-allocation baseline (`6b4750c`). What was missing was a single
prominent statement of the *rules* — two separate deadlock hunts on the
`combiner` branch traced back to migrations that broke them silently, with the
prior statement neither consulted nor cross-referenced from the code being
changed. This document consolidates and expands that principle into named
invariants. The obligation they impose is distributed across every wake-consuming
site in the codebase, which is also the main reason `workq` and `rdvq` must never
be exposed as public packages: using them correctly requires honoring a contract
the compiler cannot check.

## Why notifications exist at all

Two principles come first, because they scope everything a notification is *not*
responsible for:

1. **Liveness rests on workers arriving, never on a notification.** A worker (any
   goroutine advancing a queue — polling for work and acting on what it finds: a
   user goroutine inside a `Skim` call working its wave's queue, a scheduler
   goroutine, an executor goroutine) must attempt and fail every potentially
   executable work item before it blocks. So a worker that arrives for any reason at all sees, on its own, any
   work that capacity already makes executable. Notifications exist **only to
   unpark a worker that is otherwise waiting for new work**.

2. **A parked worker cannot sleep through executability.** The park protocol is
   register-then-confirm: the worker registers its wait *before* its final retry
   sweep, and only then blocks. A worker may still park just after an item
   becomes executable — the sweep and the enabling event can interleave — but
   because registration preceded the sweep, the event's notification finds a
   registered wait, so this worker (or another worker of the same queue) is
   guaranteed to wake again promptly.

Given these, a notification is a small thing: a nudge that ends one park. The
subtlety is entirely in never *losing* the nudge while it is still needed.

## The token model

A **notification token** is minted whenever capacity becomes available that could
make postponed work executable: a permit released to a pool, a concurrency limit
raised, a governor clearing, a buffered queue position freeing. The token then
propagates until exactly one of three things happens:

- **Productive consumption.** A woken worker's retry sweep actually starts a
  *postponed* work item. The token is spent; the enablement it represented has
  been acted on. Starting *fresh* work does not consume a token — fresh work was
  not waiting on the resource that minted it — a distinction the prior decision
  doc drew and the controller still honors (a saved notification is cleared only
  when a postponed item starts, and is forwarded otherwise).
- **Forwarding.** The woken worker's sweep started nothing (the capacity was
  taken by a racing acquirer, or the worker's postponed items wait on something
  else). The worker forwards the token — it re-enters the propagation domain and continues to
  the next registered listener or parked waiter.
- **The terminal fallback.** Delivery found no registered listener and no parked
  waiter anywhere in the domain, so the token's fallback action runs. On a
  spawn-capable pool (scheduler, executor) the fallback is the demand-spawn
  signal — postponed work may remain with no worker to try it, and no worker
  would otherwise arrive, so the chain ends by *creating* the worker it could
  not find. Only where no postponed work can be left unattended is the fallback
  a true drop — a user-goroutine domain, where admission is governed and the
  work waits for the user's return. Either way this is legal because of
  principle 1: any worker that arrives later re-attempts everything after
  registering, and the capacity is already visible.

Propagation visits **listeners first, then waiters** — listeners represent
in-process postponed work, waiters represent goroutines with nothing else to do —
and each notification domain is a `Notifier` (a `Listeners` set plus a `Waiters`
set) scoped to the resource that mints into it: one per permit pool, one per
governor, one per buffered queue's free-position events.

Two registration kinds — different in what a delivery does, alike in that
neither may end a token:

- **Listeners are one-shot proxies and their deliveries re-circulate.** A queue
  with postponed work registers its single listener with the resource's notifier;
  the listener's action is to wake one parked worker *of that queue*. Delivery
  consumes the registration — harmless, because every listen-capable retry sweep
  re-registers it, and the token that consumed it is still alive. If the woken
  worker cannot use the token, the forward re-enters the *resource's* notifier and
  the walk continues.
- **Waiters re-check and, on failure, forward like everyone else.** A goroutine
  parked in the resource's own waiter set re-checks the resource when woken (its
  park confirm); a failed re-check tells it nothing it may act on — under
  arrival-order reservation, capacity can sit free yet be held for a demand
  ahead of it — so it forwards the token onward.

A token ends in exactly one way besides productive consumption: **exhaustion of
the domain** — the walk finds no registration left to offer it to, and the
terminal fallback runs. The sets-empty test belongs entirely to the notifier; no
resource semantics (headship, eligibility, gating) participate in it. But
sets-empty alone does not license a drop: if postponed work remains with nobody
to try it, the fallback must produce a worker — which is exactly what the
demand-spawn fallback does on spawn-capable pools. A genuine drop is the
fallback only where unattended postponed work is impossible or deliberately
waits (user-goroutine domains under the governor). Exhaustion-with-drop is then
the nobody-parked proof in general form: everyone parked was offered the token
and failed, and anyone not parked is running and will attempt everything on its
own before parking — including the claimant the capacity is reserved for.
Termination is reachable, not just legal, because offers pop one-shot
registrations and a woken party **forwards before it re-registers** (in the
baseline this ordering is the forward at the top of the retry loop): a bouncing
token strictly shrinks the set it can still visit and reaches the fallback the
moment the sets empty.

One correction to the baseline code is folded in here: its notifier attached
the re-circulating forward only to listener deliveries — a waiter's forward ran
the mint's fallback, a no-op for permit releases, making waiters terminal
sinks. That was a latent conservation bug, not a design choice: it contradicts
the principle as documented even then ("notifications must keep flowing until
someone can act or there is genuinely nothing left to wake"), nothing in the
code defends it, and it leaks even with weight-1 permits once waits compose —
a waiter's failed re-check of *its* condition proves nothing about the
different condition of the registrant beside it that the fungible token could
have satisfied. It hid because the leak needs that coincidence; arrival-order
reservation would have promoted it from rare to systematic. Under the current
model, waiter deliveries re-circulate like listener deliveries, and only
exhaustion ends a token.

## Fungibility, and why this is just counting

Tokens are **not addressed**. A token minted by permit pool A being absorbed by a
worker whose postponed item was actually waiting on queue capacity B is fine —
B's own token is still circulating and will find A's claimant. Conservation is
therefore a counting invariant, not a routing one:

> At every moment, circulating tokens ≥ capacity events not yet productively
> consumed.

Each enabling event mints; each productive start spends one token and one
enablement; forwards preserve the count; a token dies only on domain exhaustion,
where the count argument shows it surplus. Nothing needs to know *which*
capacity a token stands for.

FIFO admission order (the permit pool holds freed capacity for the demand first
in line) is not in tension with this — it is complementary. Reservation
guarantees that when the token finally reaches the one worker that can retry the
head demand's work, that wake is productive: bystanders were gated off the
reserved capacity, so it cannot have been raced away. Conservation guarantees
that worker is eventually reached. Reservation makes the final hop
deterministic; conservation makes reaching it inevitable.

## The invariants

These five rules are the whole contract. The baseline honored them implicitly —
with one latent exception: rule 4 was violated by its waiter-terminal shortcut,
a rarely-triggered conservation leak this model fixes. Every deadlock in this
model's history has been a violation of one of these rules.

1. **Mint after visibility.** A token is minted only after the capacity it
   announces is observable (the release lands, *then* the notify). Otherwise a
   woken worker's retry could miss capacity that is "coming."

2. **Attempt after registering.** A worker re-attempts every potentially
   executable item after registering any wait and before blocking on it — at both
   registration levels (a listener planted during a retry sweep, and the park
   confirm). Together with rule 1 this is what makes the nobody-parked drop safe:
   a dropped token implies nobody was registered at delivery, and anyone who
   registers later sweeps later, seeing the capacity directly.

3. **Consume or forward.** Every delivered token is either spent by starting work
   or forwarded. No consumer may swallow a token it did not use. This is the
   distributed obligation: it binds every park site, every select composition,
   every controller loop that saves a notification across an iteration.

4. **Death only by exhaustion, into the terminal fallback.** No delivery
   outcome ends a token — a failed re-check proves nothing under arrival-order
   reservation. A token ends only when the walk finds no listener or waiter left
   to offer it to, a test that belongs to the notifier alone — and what runs
   then is the token's fallback: the demand-spawn signal wherever postponed work
   could otherwise be left with no worker to try it, a true drop only where it
   cannot. Reachability of exhaustion rests on one-shot registrations plus
   forward-before-re-register; safety rests on invariant 2 plus the spawn
   fallback.

5. **Re-probe for multi-unit events.** An event that frees more than one unit — a
   weighted permit release, a concurrency-limit raise — can satisfy several
   claimants, and one token wakes one worker. The rule: each *productive* consumer
   of a token re-mints one token if residual capacity may remain, so satisfiable
   claimants admit one by one until the first failed re-check terminates the
   chain. (Chosen over minting `k` tokens for a `k`-unit event: with weighted
   limiters, `k` is arbitrarily large, and a token herd for a weight-1000 release
   that satisfies two claimants is pure waste. The unlimited/unknown-headroom
   flip, e.g. `SetMaxConcurrency(-1)`, remains the one broadcast case.)

## Bounds

The model's costs are bounded by two structural facts:

- **Chain length** is at most the number of registered listeners plus parked
  claimants in one notifier's domain: one listener per queue with postponed work
  on that resource — in practice the waves with work in flight plus the two
  process-wide pools (scheduler and executor) — each hop costing one wake and one
  retry sweep.
- **Retry sweep size** stays small because postponed work has strict priority over
  accepting new work, so postponement is self-limiting; and a wave whose user
  stops skimming stops admitting new work through its governor, so an abandoned
  wave cannot grow its postponed set while its standing demands await the user's
  return.

If profiling ever shows the walk cost mattering, the sanctioned optimization is a
**head-directed fast path**: offer the token first to the worker set that can
retry the head demand's work, falling back to the full chain if not consumed
there. Addressing as a hint is compatible with the model because the fallback is
structural; addressing as the *mechanism* is rejected below.

## Rejected alternatives

- **Addressed delivery** (each waiting demand carries its own notifier, or a
  single persistent wake target, and the resource routes each capacity event to
  the head demand's). Twice attempted on the `combiner` branch, twice the root of
  traced deadlocks: routing confines the token to one demand's sets, and whenever
  the addressee cannot hear — its owner is off executing something else, its
  one-shot registration was consumed by a declined delivery — the token dies
  while parked workers that could have used it exist elsewhere. The failure is
  structural, not a bug in the attempts: addressing replaces the counting
  argument with a routing argument, and the routing argument needs auxiliary
  machinery (suspension flags, displacement walks, spawn-on-wake guarantees) to
  patch each hole it opens. Conservation needs none of it.
- **Per-unit minting for weighted events** — rejected under invariant 5: weights
  are unbounded.
- **Broadcast on every event** (`NotifyAll`): wakes every worker to satisfy one;
  the herd's retry sweeps are pure contention. Reserved for genuinely global
  condition changes (shutdown, unlimited flips).

## Verification

Planned, not yet built: **token accounting in the simulator**. The sim's
instrumented builds count mints, productive consumptions, and exhaustion deaths
(each carrying its emptied-domain proof), and assert balance at quiescence. No
production mechanism — observability only. The
recurring failure mode this catches is the silent one: a migration that changes a
wake's delivery style and turns a forward into a no-op, which today surfaces only
as a rare hang under the simulator's adversarial schedules.
