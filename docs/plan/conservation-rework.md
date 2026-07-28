# The Conservation Rework

**Status: plan agreed 2026-07-17; implementation not started. Amended
2026-07-26: see "Amendment: the waiter-set balance" at the end. Phase 2 of
the reservation model — directed delivery, settled 2026-07-26
(`directed-delivery.md`) — supersedes seam 1's release-delivery shape for
permit pools: deliveries fill reservations under the pool mutex, and the
head wake becomes a completion signal.** This plan
consolidates the design walk-through of 2026-07-16/17 on the `combiner` branch.
It replaces the earlier "settled build spec" notes in `WORKING_NOTES.md`
(2026-07-15), most of whose wake-side design is rejected. The governing document
is `../notification-conservation.md`; the vocabulary is `../glossary.md`. The
reference for "how it worked before" is the net-zero-allocation baseline,
commit `6b4750c` (worktree at `.claude/worktrees/baseline`).

## What this rework is

The `combiner` branch removed the guard that forbade skim handlers from driving
sub-waves, exposing two deadlock families. Two successive fix attempts routed
capacity notifications *at* specific demands (per-demand mailboxes, then
persistent per-demand wake targets with suspension-based routing). Both were
rejected on review: addressing confines a notification to one demand's
registrations, where it can die while workers that could have used it exist
elsewhere — a structural violation of notification conservation, the model the
baseline embodied. This rework returns the wake side to the conserved-
propagation model and closes the exposed deadlock class on the acquire side,
where it belongs.

Current tree state: the notification-model docs are committed; `internal/dll`
is committed; everything else described here exists only as uncommitted,
partially-wrong work-in-progress that this plan supersedes. Rework the tree to
match the plan, reusing in-flight pieces only where the plan names them.

## The settled design, per seam

### 1. Permit-miss wakeup (wake side of `internal/permits`)

Baseline shape, restored and generalized:

- The `Pool` owns **one pool-scoped `rdvq.Notifier`** — the pool's notification
  domain. The per-demand mailbox (committed tree) and per-demand target
  (uncommitted) are both deleted.
- A postponing admission registers **its queue's listener** with the pool's
  notifier (`ex.AddToListeners`, exactly the baseline pattern): the callback
  wakes a parked worker of the queue holding the postponed work.
- Top-level blockers (block-and-help) park in the pool notifier's **waiter
  set**, composed with their wave's queue in one select, as before.
- Releases and capacity raises **mint into the pool's notifier**. Delivery
  walks listeners then waiters; every unproductive delivery forwards; a token
  ends only on domain exhaustion, into the mint's fallback (invariant 4 of the
  conservation doc). The one behavioral fix over the baseline: waiter
  deliveries carry the re-circulating forward too (the baseline's
  waiter-terminal shortcut was a latent conservation bug).
- **Re-probe for weighted events** (invariant 5): a productive consumer of a
  token re-mints one while residual capacity may remain. Replaces both the
  baseline's per-unit minting for limit raises and the interim "chained
  notification" machinery.

### 2. Nested-submit queueing (`ctxmeta.go`, `internal/workq`)

The original `Execution.Queue` seam, restored:

- The controller sets `ex.Queue = queueFresh` for the executions it runs (the
  member was lost in a migration; the baseline-era shape is
  `a2083a2`'s `Queue: c.queueFresh` and `meta.WithQueueFunc(ex.Queue, ...)`).
- `skimWork.Execute` installs `ex.Queue` into the meta's execution environment
  for the handler's duration (the old gather-work pattern).
- `ctxMeta.ExecuteNowOrQueue`'s nested branch: one registration-free inline
  try; a miss hands the work to the installed queue function — fresh on the
  driving controller's queue, retried by that worker's own cycle, registered by
  its listen-capable pre-park sweep. Executor-side nested dispatch keeps
  routing to the scheduler's intake (`workerExEnv` → `Post`).

### 3. The no-postpone tripwire (`workq.Accepted.ExecuteNowOrQueue`)

A top-level dispatch blocks to started-or-error; a nested dispatch queues
through `ex.Queue`. Nothing legitimately reaches "neither started nor errored"
at this seam anymore, so the old silent `postponed.PushBack` there becomes a
panic. Postponed is written only by the controller's requeue door.

### 4. The queue listener's action (`workq.Accepted`)

Per the conservation doc's death condition: the listener callback offers the
wake to a parked worker of its queue; on a **spawn-capable** queue (scheduler,
executor) it spawns one instead when none is parked — consuming the token
either way; on a **user-goroutine** queue (a wave's own) it declines when no
worker is parked, and the token walks on. The in-flight "always consume"
version is wrong (it swallows tokens on wave queues); the committed
`waiters.Deliver` bridge is wrong the other way (a decline consumed the
registration). Baseline shape (`q.listener.Notify = q.waiters.Notify`,
returning delivered-or-not) plus the spawn arm.

### 5. Withdraw before going deep (acquire side; the deadlock fix)

The invariant: **no goroutine is ever parked while a demand only it can attend
stands registered.** The one context that can violate it is pumping inside a
skim handler — the handler blocking-submits (top-level for its target) or
drains a fresh sub-wave whose admissions are gated behind the outer demand on a
shared limiter. (Handlers still cannot drive waves they are part of; executor
bodies postpone; held permits are already covered by lending.)

Discipline: the same bracket that lends the goroutine's held permits when it
goes deep also **withdraws (`Invalidate`) its standing demands**; the outer
acquire loop re-registers on its next retry. Costs accepted: the withdrawn
demand loses queue position and any gathered hoard (the hoard drains to the
pool; a weighted head re-gathers). This is the same-pool extension of the
existing cross-pool reclaim lend rule, on the same justification: a queue
position reserves capacity just like a permit. The "mark unattended in place"
alternative (keeps position and hoard, adds a demand state and skip logic) is
deferred unless measurement shows the churn matters.

### 6. The demand queue structure (`internal/permits` internals)

The mutex-guarded intrusive list (committed `internal/dll`) replaces the
lock-free demand queue (nbcq ring + atomic head slot + promoting-marker
protocol + generation-stamped lazy removal). Grounds: the queue is cold-path by
construction (misses, retirement, withdrawal, episode bookkeeping — never the
uncontended acquire, and under the pool-scoped notifier not the release path
either); withdraw-before-going-deep makes interior removal routine, which is a
one-line unlink under a mutex and constant churn under lazy retirement; and the
mutex version is auditable where the lock-free version was provable only by
`-race` campaign. Registered ⇔ linked; removal immediate.

To review on their own merits during implementation (flagged, not settled):
the lock-free barrier-anchor/exemption memo for the gating test, and the
episode-claimant list sharing the demand's links.

### 7. Block-and-help (`wave.go`, `permithandle.go`)

Falls out of seams 1 and 5 rather than needing its own design: the blocker
parks on the pool notifier's waiters composed with its wave's queue (baseline
shape); help executes wave work; entering help work that goes deep triggers the
seam-5 bracket. The interim machinery built for the addressed model — the
demand-park channel, suspension brackets around help, the per-demand listener
re-planting — is deleted rather than reworked. Reclaim keeps its existing
lend-rule structure, now sharing the seam-5 bracket.

## Sequencing

Checkpoint at green after each step; concurrency-touching steps gate on a large
`TestBySimulation -race` batch before commit (generous `-timeout`).

1. **rdvq**: waiter deliveries carry the re-circulating forward (the invariant-4
   fix); restore anything the in-flight edits removed that the plan keeps
   (`Waiters.Deliver` stays deleted only if seam 4 lands without it).
2. **permits**: pool-scoped notifier; dll FIFO retained from the in-flight
   rewrite with its wake side cut out; re-probe on weighted release and raise;
   `AcquireWait` parks in the pool's waiters. Gate: permits suite + rapid
   models under `-race`.
3. **workq**: seam-4 listener action; seam-2 `ex.Queue`; seam-3 tripwire;
   requeue-door cleanup (the `SuspendableWork` hook from the in-flight edits is
   deleted — nothing suspends demands anymore). Gate: workq suite.
4. **Root package**: seam-2 `ctxMeta`/skimmer wiring; seam-5 bracket extension
   in `suspendHeldPermit`/`reclaimJoint`; seam-7 block-and-help restoration;
   delete the dead suspension/target plumbing from `permithandle.go`.
5. **Gates**: unbiased sim `checks=100` loop, then the 1000-check `-race`
   batch, then full suites and lint.
6. **Rename sweep** (separate mechanical commit once green): `Skim`→`Pump`,
   `SkimAll`→`Drain`, `CloseAndSkimAll`→`CloseAndDrain`, internal
   `yield`→`pump`; sweep retired vocabulary (drive/driver/re-drive, slot) from
   comments; `Submit` documented as pumping the wave as needed.
7. **Sim token accounting** (planned observability, may trail the rework):
   mint/consume/exhaustion counters with balance asserted at quiescence.

## Rejected en route

Recorded here so they are not re-derived: addressed wake delivery in both
incarnations (per-demand mailboxes; persistent per-demand targets with
suspension routing and effective-head walks); per-unit token minting for
weighted events; suspension flags as wake-routing state; the
`SuspendableWork` postpone-door hook; "total wake" listener actions on
user-goroutine queues; re-forbidding sub-wave driving from skim handlers;
reifying blocked acquisitions as retryable work items.

## Amendment (2026-07-26): the waiter-set balance

**Design settled with PN 2026-07-26 (reservation agenda item 6); not built.**
This closes the delivery model's last lossy arm. Within this plan it
supersedes seam 4's user-goroutine-queue decline arm: under the
mode-directed delivery that landed after this plan was agreed (`e33ba48`,
2026-07-19, revising `notification-conservation.md`), a declined wake has
no conserved walk to rejoin — the attendant fired one-shot — so "decline
and walk on" is replaced by "record the miss in the balance."

**The hole.** `Waiters.Notify` on a queue with no spawn hook silently drops
a wake that finds no parked waiter (the nil-fallback noop,
internal/rdvq/waiters.go). For most flows that is harmless — work-supply and
lifecycle wakes announce facts a parking waiter's `confirmFn` re-derives
from queue state. But the relay-attendant wake announces a fact that lives
nowhere else: a registered demand's capacity event was consumed one-shot
pool-side (the head's attendant fired and cleared), and an unattended head
absorbs later events under the attempt-on-arrival presumption. Dropping
that one wake severs the whole signal chain — the quiet wedge of hang
diagnosis 6 (WORKING_NOTES, 2026-07-20): a postponed gated admission whose
only readiness signal relayed into an empty waiter set, followed by
19.8 seconds of silence over a fully free pool.

**The design.** `rdvq.Waiters` gains a **balance** — a counter of missed
notifications — making `Notify` total by construction:

- `Notify(missFn)`: wake one parked waiter; on a **miss** (no waiter took
  it), run `missFn` if provided, else record the miss in the balance.
  Worker-minting queues pass their spawn signal as `missFn`, so their
  misses never reach the balance;
  the signature and every call site (`Accepted.relay` included) are
  otherwise unchanged — the wedge closes with zero workq code.
- Consumption sits in the wait path as **register → `confirmFn` →
  try-consume → block**: a successful consume returns as a received wake
  without parking. The consume-implies-retry-sweep guarantee rides the
  existing caller structure (`ExecuteOrWait` re-attempts work after any
  received wake); no new coupling. A `confirmFn` decline leaves the balance
  untouched — decline-implies-sweep holds for workq's `ShouldWait` but is
  not provable for arbitrary callers at the primitive level; the cost is
  one bounded extra spin per token, never a loop.
- The balance is a **counter, not a sticky bit**. Pumping is legal from
  multiple goroutines (skim handlers need only be concurrency-safe), so N
  recorded misses must be able to abort N park attempts — service multiplicity
  matching event multiplicity. A bit collapses them and serializes ready
  work onto one pumper while its siblings park beside actionable items
  (liveness survives via the pumping loop; parallelism does not).
- `NotifyAll` stays **balance-neutral**: its broadcast is a re-check prompt
  whose information the re-check re-derives, and clearing the balance there
  would globalize a terminal-only argument — a mid-life `NotifyAll` with
  nobody parked would destroy live recorded misses, reopening the wedge.
- Lifecycle: `waveImpl.Reset` **zeroes the balance** with the other
  per-cycle clears (single-owner at refs==0, quiescent; the warm queues'
  never-re-Init contract is untouched). A nonzero balance on a *Done* wave
  is legitimate staleness — a recorded miss whose ready item an arriving
  pumper swept before anyone parked — so the zero assertion binds
  post-Reset, not at Done.

**The race-closing invariant, in checkable form:** "balance > 0 while a
waiter is parked" is never a stable state. Transient overlap is legal;
stable overlap is a missed wake. Enforced at build by serializing the
failed-wake→record step against register→confirm→consume→park under the
waiter queue's existing internal discipline.

**Vocabulary.** The parameter renames `fallback` → `missFn`: with the
balance beneath it, it is no longer a last resort, and "miss" joins the
attempt-found-nothing family the codebase already uses (a retry misses when
capacity is absent; a wake misses when no waiter is parked). The balance is
precisely what makes a miss *not* a classic missed wakeup. Rejected names:
`underflowFn` (side-ambiguous — underflow of waiters is overflow of
notifications), `unansweredFn`, `shortfallFn`, `surplusFn`.

**Verification.** Asserts: the balance never goes negative; balance == 0
immediately post-Reset. Sim: an idle-point probe asserting the invariant
above at every global-quiescence observation; mint/consume accounting per
waiter set (the Sequencing item-7 counters, now load-bearing); the
regression gate is the existing sim configurations that produced the quiet
wedge (~1/12 on the filtered loop, ~1/100 combined on the x8 loop) running
clean in large `-race` batches, with `hunt_quiet.sh` retained as the
capture harness. rdvq black-box suite: miss-then-immediate-Wait, N wakes
wake N sequential waiters, a non-nil `missFn` keeps the balance at zero,
decline leaves balance, NotifyAll balance-neutrality; plus a rapid property test asserting
no Wait blocks while the balance is positive.

**The completed model** (fold into `docs/notification-conservation.md` at
build): every delivery endpoint is total — mode-directed attendants
(reserved resources), worker-minting `missFn` (worker-backed queues), the
balance (waiter sets). Nothing anywhere drops a token.

**Rejected en route:** the sticky bit (under-wakes concurrent pumpers);
`NotifyAll`-clears-balance (terminal-only argument globalized); consuming
the balance before `confirmFn` (bypasses the contracted veto and the
`Blocking()` once-only sequencing); consuming on `confirmFn` decline
(unprovable at the primitive level).
