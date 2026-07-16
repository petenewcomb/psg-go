# Glossary

A living reference for the project's settled vocabulary. Terms are added here
when their meaning has been agreed, and retired terms are listed at the bottom so
their replacements are discoverable. When code, comments, or docs conflict with
this file, this file states the intent and the conflict is a cleanup candidate.

## Roles and actions

- **worker** — any goroutine executing a queue's work-processing pass: a user
  goroutine working its wave's queue for the duration of a `Skim` call, a
  scheduler goroutine, an executor goroutine. Always relative to a *queue*, never
  a wave; a queue may have many workers, and most workers are passive
  (parked waiting for work) much of the time.
- **pass** — one iteration of a worker's processing loop: find one work item
  (fresh, then postponed, then newly accepted) and execute it.
- **skim** — the user-facing act of processing submitted results via a wave's
  `Skim`/`SkimAll` (formerly *gather*).
- **drain** — bringing a wave or sub-wave to completion by working its queue
  until it is done.
- **retry** — a further attempt to carry out a direction given at submit time,
  applied to postponed work. The direction is never re-given; only the attempt
  repeats.
- **postpone** — a work item's non-start outcome when its execution may not
  block: the item parks on its queue's postponed list and is retried by workers'
  later passes, with priority over accepting new work.

## Notifications

See `notification-conservation.md` for the model these terms belong to.

- **readiness notification** (or **notification token**) — the nudge minted when
  capacity becomes available, conserved through forwarding until a worker
  consumes it productively. Exists only to unpark a worker otherwise waiting for
  new work; liveness never rests on one.
- **productive consumption** — a woken worker's retry actually starts work; the
  token is spent.
- **forwarding** — an unproductively woken worker passes its token onward so it
  keeps circulating (the old code's *renotify*).
- **listener** — a one-shot proxy registration a queue plants with a resource's
  notifier; its delivery wakes one of the queue's workers and re-circulates if
  unproductive.
- **waiter** — a goroutine parked directly on a resource as a claimant; its
  failed re-check terminates a token.

## Capacity

- **permit** — one unit of a limiter's concurrency capacity, drawn from a
  `permits.Pool` (formerly *slot* in the concurrency sense).
- **weighted acquisition** — an acquire of more than one permit at once; weights
  are unbounded, which is why multi-unit capacity events use the re-probe
  discipline rather than per-unit notification minting.
- **demand** — the registered identity of one waiting acquisition in a permit
  pool's arrival-order queue; satisfied or explicitly withdrawn, never dropped.
- **free outbox** — an available buffered position in a rendezvous queue
  (formerly *slot* in the queue-capacity sense). Outboxes themselves may be
  removed in a future rendezvous-queue simplification.
- **governor** — the per-wave backpressure gate: while downstream (results
  awaiting skim) is saturated, upstream admission waits; a wave whose user stops
  skimming therefore stops admitting new work.

## Retired terms

- **drive / driver / re-drive** — retired 2026-07-16. "Driver" implied a single
  active puller where the reality is many, often passive, workers; it also got
  mis-applied to scheduler/executor goroutines, which execute work across waves
  and never relate to a single one. Use **worker** (of a queue), **pass**,
  **skim**, or **drain** as appropriate; for "re-drive" use **retry**, prompted
  by a **readiness notification**.
- **slot** — retired 2026-07-16. Use **permit** for the concurrency-limit sense
  and **free outbox** for the queue-capacity sense.
- **gather / scatter** — the pre-repositioning API vocabulary (see the rename
  series beginning at `b6641cc`); superseded by **skim** and **submit**/**start**.
