# Glossary

A living reference for the project's settled vocabulary. Terms are added here
when their meaning has been agreed, and retired terms are listed at the bottom so
their replacements are discoverable. When code, comments, or docs conflict with
this file, this file states the intent and the conflict is a cleanup candidate.

## Roles and actions

- **worker** — any goroutine executing a queue's work-processing pass: a user
  goroutine working its wave's queue for the duration of a `Skim` call, a
  scheduler goroutine, an executor goroutine. The role attaches to the *queue*
  being worked — often a wave's own queue — not to the wave as such; a queue may
  have many workers, and most workers are passive (parked waiting for work) much
  of the time.
- **advance** — one iteration of a worker's loop: poll for work (fresh, then
  postponed, then newly accepted) and act on at most one item found. What acting
  means depends on the queue: an advance of the scheduler's queue *dispatches*
  (admission); an advance of a skim queue *executes* (handlers). The code's
  `ExecuteOne` is one advance; a worker whose poll comes up empty parks.
- **poll** — the looking half of an advance: checking the queue's sources for
  available work without blocking.
- **pump** — the user-facing act of advancing a wave's pending work and thereby
  relieving its backpressure: one stroke (`Pump`/`TryPump`) runs at most one
  pending item — a skimmer handler, a postponed retry — and includes the
  yield-to-scheduler breath. Submits pump the wave as needed before adding;
  the *Skimmer* op keeps its name (it defines what happens to results; pumping
  is what makes them happen).
- **drain** — pumping a wave until nothing is left (`Drain`/`TryDrain`,
  formerly `SkimAll`/`TrySkimAll`); does not itself close the intake — the
  terminal combination is `CloseAndDrain` (formerly `CloseAndSkimAll`).
- **retry** — a later attempt to execute a postponed work item. Retries have
  priority over accepting new work.
- **postpone** — a work item's non-start outcome when its execution may not
  block: the item parks on its queue's postponed list and is retried by workers'
  later passes, with priority over accepting new work.

## Notifications

See `notification-conservation.md` for the model these terms belong to.

- **readiness notification** (or **notification token**) — the nudge minted when
  capacity becomes available, conserved through forwarding until a worker
  consumes it productively. Its only job is to shorten a parked worker's wait:
  workers attempt all executable work on their own before parking, so the system
  makes progress even when a notification is legitimately dropped.
- **productive consumption** — a woken worker's retry actually starts work; the
  token is spent.
- **forwarding** — an unproductively woken worker passes its existing token
  onward so it keeps circulating (the old code's *renotify*). Distinct from
  **re-probe**, below, which mints a *new* token.
- **re-probe** — after a multi-unit capacity event (a weighted release, a limit
  raise), each productive consumer mints one fresh token while residual capacity
  may remain, so remaining claimants are offered it one by one.
- **listener** — a callback registered with a resource's notifier by a worker;
  when called, it wakes any future worker blocked on the same queue. One-shot:
  calling it consumes the registration.
- **waiter** — a goroutine parked awaiting notification: in this codebase,
  usually a worker parked on a generic `Waiters` set composed with a `Handoff`
  queue in a single select.

## Capacity

- **permit** — one unit of a limiter's concurrency capacity, drawn from a
  `permits.Pool` (formerly *slot* in the concurrency sense).
- **weighted acquisition** — an acquire of more than one permit at once; weights
  are unbounded.
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
  series beginning at `b6641cc`); superseded by **submit**/**start** and the
  skim-then-pump result-processing vocabulary.
- **skim (as the API verb)** — retired 2026-07-17 in favor of **pump**/**drain**
  (`Skim`→`Pump`, `SkimAll`→`Drain`, `CloseAndSkimAll`→`CloseAndDrain`): a
  stroke may run any pending item, not only a result handler, so "skim"
  over-promised. The **Skimmer** op and its handlers keep the name.
