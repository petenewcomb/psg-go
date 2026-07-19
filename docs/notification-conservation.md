# Notification Propagation

**Status: settled design, 2026-07-19.** This document specifies the notification
model that the work queues (`internal/workq`), the rendezvous queues
(`internal/rdvq`), and the permit pools (`internal/permits`) share. It
supersedes the token-conservation model settled here on 2026-07-16: building
that model out on the `combiner` branch produced three traced deadlocks, each a
different disguise of the same structural flaw (a recipient's local judgment
silencing the rest of the domain — see History). The rules below replace token
accounting with unconditional bounded delivery. The obligations they impose are
distributed across every wake-handling site in the codebase, which is also the
main reason `workq` and `rdvq` must never be exposed as public packages: using
them correctly requires honoring a contract the compiler cannot check.

## Why notifications exist at all

Two principles come first, because they scope everything a notification is *not*
responsible for:

1. **Liveness rests on workers arriving, never on a notification.** A worker
   (any goroutine advancing a queue — polling for work and acting on what it
   finds: a user goroutine inside a `Skim` call working its wave's queue, a
   scheduler goroutine, an executor goroutine) must attempt and fail every
   potentially executable work item before it blocks. So a worker that arrives
   for any reason at all sees, on its own, any work that capacity already makes
   executable. Notifications exist **only to unpark a goroutine that is
   otherwise waiting**.

2. **A parked goroutine cannot sleep through executability.** The park protocol
   is register-then-confirm: the goroutine registers its wait *before* its
   final retry or re-check, and only then blocks. A parker may still park just
   after an enabling event — the re-check and the event can interleave — but
   because registration preceded the re-check, the event's delivery finds a
   registered wait, so the parker is guaranteed to wake again promptly.

Given these, a notification is a small thing: a nudge that ends a park. The
subtlety is entirely in never *withholding* the nudge from a place that needs
it.

## The delivery rule

A **capacity event** is anything that could make postponed or blocked work
executable: a permit released to a pool, a concurrency limit raised, a governor
clearing, a buffered queue position freeing — and, per the cascade rule below,
a head demand retiring or withdrawing. Each resource owns a **notification
domain**, whose shape depends on whether the resource reserves:

- an **unreserved resource** (queue space, a governor) owns a general
  `Notifier` — a `Listeners` set plus a `Waiters` set;
- a **reserved resource** (a permit pool, under arrival-order reservation)
  owns its demand queue itself, whose registered demands carry their
  attendants, plus a fallback `Notifier` for the unregistered.

Delivery is **never gated by consumption**. Whether an event has been
"satisfied" is a global predicate over concurrently changing registrations —
undecidable at any single site — so no recipient's local judgment ("my sweep
started something", "I spawned a worker") may narrow delivery. The only sound
narrowing is a proof by the resource itself, under its own lock, about its own
reservation state — which is exactly what mode-directed delivery below is.

### Reserved resources: mode-directed delivery

Arrival-order reservation makes "only the head can gather" the resource's own
rule, provable under its own mutex — so delivery beyond the head's attendant is
provably futile, not merely wasteful. A capacity event on a pool delivers by
mode:

1. **Standing head**: wake the head demand's attendant — nothing else. Every
   other registrant's re-check is a guaranteed miss by construction, and an
   unregistered attempt lands behind the head and misses too.
2. **No head, capacity free**: walk the pool's fallback notifier — the
   queue-interest listeners described under Registration semantics, waking one
   parked worker per interested queue. This is the only channel by which
   unregistered postponed work (the attendance rule's withdrawn-demand class)
   hears about capacity.
3. **No head, no listeners**: silence — provably nobody anywhere is waiting,
   and any later arrival sees the capacity directly (mint-after-visibility
   plus attempt-on-arrival).

Head-directed delivery is sound only because of two invariants, and both are
load-bearing:

- **Attendance**: a registered demand always has a live attendant (a parked
  owner, a planted relay, or a cycling worker) — there is no deaf head. This
  is what the twice-rejected addressed-delivery attempts lacked.
- **No exempt parking**: claimants exempt from the head-of-line barrier
  (overdraft-episode resume, chains through the head's body cache) must never
  park in the pool's notification domain — they draw from the episode
  allowance or retry through their own queue's machinery.

### Unreserved resources: full delivery

Every capacity event is delivered once, at mint time, to every place in the
domain that could be enabled by it:

- **Every registered listener is relayed.** A listener is a work queue's proxy:
  its action is to wake **one** parked worker of that queue, because a queue's
  workers are interchangeable — any one of them sweeps the whole queue. A relay
  that finds no parked worker declines harmlessly (on a spawn-capable queue it
  may spawn a worker as a side effect). The walk continues past every listener
  regardless of what the relay did.
- **Every parked waiter is woken.** Waiters are blocked parties in the
  resource's own waiter set, not interchangeable with each other: all re-check,
  those still blocked re-park (their registration renewed by the park
  protocol).

After delivery the event is spent. There is no circulating token, no
forwarding obligation, no exhaustion fallback: a domain that delivered to
nobody simply had nobody parked, and principle 1 covers everyone running.

## The cascade rule (multi-admission events)

A single event can legitimately enable **multiple** admissions: a weight-`k`
release can satisfy several queued demands, and a capacity raise can admit
many. One propagation cannot deliver them all — head-of-line admission
serializes claimants, and a woken non-head waiter may re-check and re-park
*before* the head retires. The cascade rule covers the remainder:

> **Every head retirement (satisfaction) and every head withdrawal
> (`Invalidate` of a head) is itself a capacity event and propagates afresh:
> it wakes the successor head's attendant — or, when the queue drains empty
> with capacity still free, fires the pool's fallback walk, so unregistered
> work hears the moment the reservation lifts.**

Admissions therefore cascade one per propagation until the first head that
cannot gather, and each propagation in the cascade is paid for by actual
progress (an admission or a withdrawal). This is the promotion cascade of
`weighted-acquisition.md`, generalized. (Chosen over minting `k` deliveries for
a `k`-unit event: with weighted limiters `k` is arbitrarily large while the
satisfiable claimants may be few.)

## Registration semantics

- **Registered demands carry their attendant.** A demand standing in a pool's
  arrival-order queue records its current attendant: the parked owner's wake
  target (a blocking submit), or the waiter set of the queue holding its
  postponed work (a listen-capable postpone). The attendant is written before
  the final re-check that precedes parking — the same race-closing order as
  every park — and cleared on wake or withdrawal. Head-directed delivery reads
  it under the pool's mutex.
- **Attendance bounds registration.** A demand may stand registered only while
  something attends it — a parked owner, a planted relay, or an actively
  cycling worker. An attempt that leaves none of these behind withdraws the
  demand before returning (re-registering on the next attended attempt).
- **Queue-interest listeners cover the unregistered.** A queue holding
  postponed pool-gated work whose demand was withdrawn keeps a listener
  planted with that pool's *fallback* notifier — a coarse per-queue interest
  bit, not tied to any demand. It is planted before the withdrawing attempt's
  final re-check, so a release interleaving with the withdrawal cannot fall
  into silence.
- **Listeners are one-shot.** A walk pops every listener it relays; the queue
  re-plants at its next retry. The gap between a pop and the re-plant is
  covered by the pillars: the woken (or spawned, or next-arriving) worker
  attempts everything before parking, and its final pass re-plants before its
  last re-check.
- **Waiter registrations are park-scoped**, renewed by each park confirm.

## Spawning

Liveness rests on workers arriving. Spawn signals therefore ride the
work-supply path — excess fresh work wakes a parked worker or spawns one — and
a listener relay on a spawn-capable queue that finds no parked worker may spawn
as a side effect. A spawn never substitutes for delivery and never stops the
walk: it supplies a worker, it does not consume an event. Wave queues cannot
spawn; their attendance contract is the governed-abandonment rule — a wave
nobody is blocking on admits no new work through its governor, so its postponed
set cannot grow while its demands await the user's return.

## Bounds

- **Reserved resources, head mode**: exactly one wake per event (the head's
  attendant). Cascades add one wake per admission or withdrawal — bounded by
  work actually done, never by an event's weight.
- **Reserved resources, fallback mode**: one wake per interested queue — at
  most the queues holding unregistered gated work for that pool.
- **Unreserved resources**: one wake per queue with a registered listener plus
  one wake per parked waiter. Each wake costs one retry sweep or one re-check.
- **Retry sweep size** stays small because postponed work has strict priority
  over accepting new work, so postponement is self-limiting.

FIFO admission order (arrival-order reservation, head-of-line gathering) is
unchanged by this model and is specified in `weighted-acquisition.md`.

## Rejected alternatives

- **Token conservation** (mint → forward → consume-or-forward → exhaustion
  fallback, with probe-on-consumption): the predecessor model, settled
  2026-07-16, superseded by this document. See History.
- **Addressed delivery as a universal mechanism** (each waiting demand carries
  its own notifier and the resource routes every capacity event to the head
  demand's). Twice attempted on the `combiner` branch, twice the root of
  traced deadlocks. The failure was the deaf addressee: routing to a target
  that might not be able to hear, with auxiliary machinery (suspension flags,
  displacement walks, spawn-on-wake guarantees) patching each hole. The
  head-directed mode above is the honest resurrection of this idea, for
  reserved resources only, standing on two invariants those attempts lacked:
  reservation makes non-head delivery provably futile (so directing loses
  nothing), and attendance makes the head always able to hear (so directing
  loses no one) — with the fallback notifier catching the population
  addressing cannot see. Addressing anywhere those proofs do not hold remains
  rejected.
- **Per-unit minting for weighted events**: weights are unbounded; the cascade
  rule admits exactly as many claimants as the capacity supports at one
  propagation per admission.
- **Full-herd broadcast** (wake every parked worker of every queue): a queue's
  workers are interchangeable, so waking more than one per queue buys nothing
  but contention. Reserved for genuinely global condition changes (shutdown,
  the unlimited/unknown-headroom flip such as `SetMaxConcurrency(-1)`).

## History: the token-conservation model

The 2026-07-16 model treated each capacity event as minting one conserved
token, propagated hop by hop until a "productive consumption" (a postponed item
starting), with death only at domain exhaustion into a terminal fallback, plus
a probe-on-consumption debt added 2026-07-19. Building it out produced three
traced deadlocks on the `combiner` branch, each a different site exercising the
same flaw — a local judgment terminating propagation the rest of the domain
still needed:

1. **Unattended registration** — a one-shot admission miss left a demand
   standing head with no listener, waiter, or cycling worker attending it
   (fixed by attendance-backed registration, retained above).
2. **Cross-resource consumption stranding** — a pool's final token was consumed
   by starting an item postponed on *queue space*; the pool's parked head
   claimant, reachable only by the walk the consumption terminated, slept
   forever. Patched by probe-on-consumption; the patch treated a symptom.
3. **Spawn-claims-consumption** — a queue listener with no parked worker
   spawned one and reported the token consumed; the pool's parked head
   claimant, next in the very walk the claim terminated, slept forever.

The baseline (`6b4750c`) itself carried a latent form of the same flaw: its
notifier attached the re-circulating forward only to listener deliveries, so a
waiter delivery was a terminal sink. Under unconditional delivery the entire
class is structurally impossible: no recipient can terminate a walk, because
the walk owes every registrant delivery before it ends.

## Verification

The sim's planned observability hooks count, per domain: events minted,
head-directed wakes, fallback relays (delivered / declined), waiter wakes,
cascade lengths, and admissions. The checkable claims: every registered demand
has a non-empty attendant whenever its owner is parked; cascade length equals
admissions plus withdrawals, ending only at a head that cannot gather or an
empty queue (the latter having fired the fallback if capacity remained); and a
standing head with free capacity and a parked owner is a bug by definition —
the state all three traced deadlocks shared.
