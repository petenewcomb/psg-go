<!--
Copyright (c) Peter Newcomb. All rights reserved.
Licensed under the MIT License.
-->

# rdvq inbox reclamation — generation-stamped inbox design

This is the design for reclaiming **abandoned inboxes** in `inboxOnlyQueue` (the
base tier under both `Waiters` and `Handoff`/`Queue`), so the inbox pool recycles
abandoned inboxes instead of leaking one per abandoning wait. It is the inbox-side
analogue of the committed outbox generation protocol — read
`docs/rdvq-outbox-reclamation.md` first; this doc assumes its `empty`/`filling`/
`full` + monotonic-generation-in-one-atomic-word pattern.

## The problem, stated precisely

`Waiters.WaitFunc` (and `Handoff`/`Queue` receivers) `borrowInbox()` per call and
`reclaimInbox()` **only when `PopFrontFunc` reports `clean`** — i.e. when a value
was received directly on the inbox channel. When the receiver instead gives up
(work arrived via a *different* channel — the common skim case, where the value
comes off the `Queue` outbox, or a `Waiters` waiter that is never the delivery
target), `PopFrontFunc` marks the inbox **abandoned**: it pushes a zero-value
marker into the cap-1 channel and leaves the inbox on the `emptyInboxes`
collection. The abandoning receiver must NOT reclaim it, because:

- **`Queue.PopFrontFunc` holds and re-passes the same inbox across retries**
  (queue.go:369-371), draining its own marker via PopFrontFunc's reuse-without-
  requeue branch — so the receiver still owns it; and
- a sender's `TryPushBack` will pop the abandoned inbox, drain the marker, and
  (today) **drop it to GC**.

So an abandoned inbox is leaked to GC every time. Measured: after dispatch-meta
pooling, `BenchmarkLauncherSkim` sits at 14 allocs/op, ~10 of which are this
churn (`rdvq.inbox[func()].Init` + the inbox-pool struct `Get` + nbcq nodes), all
on the skim park path.

### Why the naive fix fails (recorded so we don't retry it)

Making the sender reclaim the abandoned inbox in `TryPushBack`'s marker-drain
branch (commit attempted 2026-06-29, reverted) **hangs `saturation_test`**: the
abandoned inbox is still owned by its receiver, which re-passes it. The sender
reclaiming it hands the same inbox to a second receiver via the pool → two
receivers, one channel → lost wakeup / double-receive. The drop-to-GC is
load-bearing precisely because the marker protocol carries **no way to tell a
"the receiver is done with this" abandon from a "the receiver will re-pass this"
abandon**, and no way to fail a stale reclaim against a re-pass.

That missing disambiguation is exactly what a generation stamp provides — same as
the outbox, where "a stale reclaimed-and-reused outbox fails its CAS and is
dropped."

## The design: a generation-stamped inbox

Replace the **zero-value channel marker** (which also collides with a legitimate
zero value) with an explicit atomic state machine in one word, mirroring
`outbox.state`. The cap-1 channel reverts to carrying *only real values*. Two
decisions keep it minimal:

- **Sole-receiver reclaim.** The inbox has one logical owner — the **receiver** —
  so it is the *only* party that ever reclaims, on **both** the clean and abandon
  paths. Senders never reclaim; they deliver-or-skip. (Reclaiming from the sender
  side is exactly what the reverted naive attempt did.) Reference counting was
  considered and rejected: it would still need a generation for ABA-safety across
  pool reuse, would not evict the lingering stale hint from the lock-free queue,
  and there is no multi-party reclaim to coordinate — one owner suffices.
- **Bump the generation only on abandon.** The generation exists solely to
  invalidate a sender that *observed a waiting registration the receiver then
  disowned*. On the clean path the value was wanted and delivered; a delayed sender
  that later delivers into a *reused* waiting period still hands a value to a
  receiver that wants one — benign. Abandon is the unique transition that turns
  "wants a value" into "doesn't" while a sender may already hold that observation,
  so it is the only point a stale sender view must be invalidated. No bump on
  register, deliver, clean-receive, re-pass, or Reset.

This collapses the state set to three — no explicit `abandoned` state; abandon is
`waiting→free` *carrying* the gen bump:

```
inboxState (low bits of state word; generation in the rest)
  inboxFree       // pooled / not registered / disowned-by-abandon (gen already bumped)
  inboxWaiting    // registered as a hint on emptyInboxes; receiver may block on ch
  inboxDelivering // a sender won the claim and is sending the value into ch (transient)
```

As with the outbox, `emptyInboxes` holds **stale-tolerant, generation-stamped
hints** — `inboxHint{ib, gen}` (NOT a bare `*inbox`), the gen captured at
registration — not authoritative membership. A popping sender claims at the
**hint's captured generation**, so a duplicate hint or a hint to a since-abandoned
incarnation is inert.

> **Why captured-gen, and why it's load-bearing for the SHARED pool.** The inbox
> pool is the process-global `omnipool.For[inbox[T]]`, shared across every
> `inboxOnlyQueue` of the same `T` (the wave's skim `Queue[Work]`, the scheduler's
> `incoming Handoff[Work]`, …). Only an **abandon** leaves a lingering hint (a clean
> delivery pops it), and abandon **bumps the gen**. So an inbox abandoned in queue A
> (gen `g`→`g+1`) and then reused — *in another queue B via the shared pool* — leaves
> A holding a hint at gen `g`; A's sender claiming at the captured `g` fails (the inbox
> is now `g+1`, B's), instead of delivering A's value into B's receiver
> (cross-queue misdelivery). Claiming at the *current* gen would not distinguish
> incarnations across the shared pool — this was a real bug caught only by the live
> simulation, since `sync.Pool`'s P-affinity makes cross-queue reuse rare in a stress
> test (see `TestInboxCapturedGenStaleHintInert`, the deterministic guard). A
> per-queue pool would also fix it but sacrifices cross-queue inbox reuse; captured-gen
> keeps the shared pool.

### Transitions (each a generation-guarded CAS unless noted)

1. **register** (receiver, `PopFrontFunc`): obtain ib (`free` @ g); `CAS(g,free)→
   (g,waiting)`; push `inboxHint{ib, g}`; block on `ch` (select `ch` | ctx | …).
2. **deliver** (sender, `TryPushBack`): pop a hint `{ib, hg}`; `CAS(hg,waiting)→
   (hg,delivering)` — claim at the **captured** `hg`. Win → `ch <- value` (cap-1,
   uncontended; the claim made us exclusive) → delivered. Lose → stale (abandoned/
   reused → gen advanced past `hg`), in-flight, or contended; try next hint.
   **Senders never reclaim.**
3. **receive** (receiver): `<-ch` got the value; `CAS(g,delivering)→(g,free)` (no
   gen bump), then reclaim (Put) or — for a held/re-passing receiver — keep at
   `free` for the next register. Drained channel preserved (no realloc).
4. **abandon** (receiver): `CAS(g,waiting)→(g+1,free)` — the **sole** gen bump; the
   receiver then reclaims (Put) or re-registers (re-pass). **Lose** → a sender is
   `delivering`: a value is inbound, so the receiver drains it from `ch` (orphan),
   then `CAS(g,delivering)→(g,free)` + reclaim/reuse (subsumes today's orphan drain).
5. **re-pass** (receiver holding ib after abandon, e.g. `Queue` across retries): ib
   is already `free` @ g+1, so re-register via step 1's `CAS(g+1,free)→(g+1,waiting)`
   and re-push the hint. A transient double-membership is tolerated — a duplicate
   hint pops to a non-matching state and is skipped.

`Reset` (omnipool Resetter): store `free`, keep the generation as-is (NOT bumped —
abandon already did, where applicable) and keep the drained channel. Implementing
Reset also prevents omnipool's default whole-struct zeroing (which would nil the
channel).

## Safety arguments to discharge (in the prototype + model check)

1. **No double-delivery:** `CAS(waiting→delivering)` serialises senders — at most
   one wins per waiting period, so the cap-1 channel is never double-filled (a
   sender thus never blocks in the non-blocking `TryPushBack`).
2. **No lost value:** a `deliver` that wins always reaches a receiver — the
   still-blocked one via `<-ch`, or (if it left the select on ctx) the receiver's
   `abandon` CAS fails and it drains the orphan.
3. **No lost wakeup:** register-then-recheck (confirm) ordering is unchanged; the
   state machine only governs *who may deliver/reclaim*, not when a waiter parks.
4. **Stale-view inertness:** the gen bump on abandon makes a sender that observed a
   now-disowned registration fail its CAS; no other transition resurrects a stale
   view into a wrong delivery. This is the crux the reverted naive fix lacked, and
   the property the prototype must stress hardest.

## Validation plan (mirrors the outbox productionization)

1. **Prototype + proto-tests first** — a standalone state-machine prototype with
   `inboxpool_proto_test.go` / `inboxpool_reclaim_proto_test.go` exercising the
   transitions and the four safety properties under stress, mirroring
   `outboxpool_proto_test.go` / `outboxpool_reclaim_proto_test.go`. Do NOT edit the
   live `inboxonly.go`/`inbox.go` until the prototype is green.
2. **Productionize** into `inbox.go` + `inboxonly.go` (`TryPushBack` deliver/
   reclaim, `PopFrontFunc` register/abandon/re-pass), keeping `Waiters`/`Handoff`/
   `Queue` callers unchanged where possible.
3. **Gate:** rdvq unit + `saturation_test` (the case the naive fix hung) + the
   full streampool suite + a large `-race TestBySimulation` batch +
   `BenchmarkLauncherSkim` showing the inbox cluster gone (target ≈ 14 → low
   single digits).

## Status

PRODUCTIONIZED & VALIDATED (2026-06-29).

- Step 1 (prototype, `inboxpool_proto_test.go`): `TestInboxReclaim_Race` (two queues
  sharing one pool, churning receivers) + `TestInboxCapturedGenStaleHintInert` (the
  deterministic cross-queue guard — fails if `trySend` claims at the current gen
  instead of the captured one).
- Step 2 (live: `inbox.go`, `inboxonly.go`, with `Waiters`/`Handoff`/`Queue` callers
  reclaiming on every PopFrontFunc since it now always leaves the inbox free).
- Step 3 gate: full streampool suite green; rdvq `-race` incl. `saturation_test`;
  25/25 `TestBySimulation -race`; **`BenchmarkLauncherSkim` 38 → 5 allocs/op** (meta
  pooling 38→14, gen-stamped inbox 14→5). rdvq Queue benchmarks unchanged
  (performance-neutral). New `BenchmarkHandoffVsChan` quantifies Handoff vs an
  unbuffered channel.

Note: the design originally described the sender claiming at the inbox's *current*
generation; productionization corrected this to the **captured-gen hint** (see the
shared-pool note above) — the only way to keep the shared pool cross-queue-safe while
bumping the generation solely on abandon.

## Follow-up: reaping accumulated abandoned hints (2026-06-30)

The gen-stamped inbox recycles abandoned *inboxes*, but each registration still publishes
an `inboxHint` (an nbcq **node + value cell**) into `emptyInboxes`, and an abandoned
registration leaves its hint behind — inert (stale gen), but holding that node+value live
in the queue until some sender's `TryPushBack` front-pops past it. Under abort-heavy
contention (notably `blockAcquire`'s register-then-recheck loop, which aborts the instant
a permit frees during registration), abandons outrun notifies and these hints accumulate,
forcing every registration to allocate fresh node+value storage. This was GC- and
P-independent (`GOGC=off` and `GOMAXPROCS=1` both left it unchanged) — a
push-without-matching-pop accumulation, not `sync.Pool` churn.

Fix: `inboxOnlyQueue.reapStale`, called from the abandon branch of `PopFrontFunc` —
front-pop a bounded run of leading hints, drop the stale ones (`TryPopFront` recycles
their node+value) and re-push the first live one. Re-pushing a live hint is safe (at most
a redirected/deferred wake, never lost — see
`docs/decisions/waiter-set-notification.md`). Took streampool from ~7.3 → ~2.6
allocs/task; with `confirmFn`/`h.release` method-value caching, → ~1.1 (the per-task
closure floor). `TestWaitersReapPreservesLiveWaiter` + `BenchmarkWaitersAbandon` guard it.

The broader question this raised — whether the FIFO waiter ordering is even desirable, and
whether a caller-held or affinity-bucketed waiter set would be better — is recorded in
`docs/decisions/waiter-set-notification.md` (answer: keep the shared substrate + reap;
affinity bucketing deferred, measurement-gated).
