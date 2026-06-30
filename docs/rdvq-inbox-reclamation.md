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

As with the outbox, the `emptyInboxes` collection holds **stale-tolerant hints**,
not authoritative membership: an inbox may sit on it while `free`/`waiting`/
`delivering`; a popping sender loads the current `(gen,state)` and CAS-validates,
so a duplicate hint or a hint to a since-abandoned incarnation is inert.

### Transitions (each a generation-guarded CAS unless noted)

1. **register** (receiver, `PopFrontFunc`): obtain ib (`free` @ g); `CAS(g,free)→
   (g,waiting)`; push ib as a hint; block on `ch` (select `ch` | ctx | …).
2. **deliver** (sender, `TryPushBack`): pop a hint; `load(g,st)`:
   - `st==waiting`: `CAS(g,waiting)→(g,delivering)`. Win → `ch <- value` (cap-1,
     uncontended; the claim made us exclusive) → delivered. Lose → stale/contended,
     try next hint. **Senders never reclaim.**
   - `st∈{free,delivering}`: stale or in-flight hint; skip.
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

DESIGN ONLY (2026-06-29). Not implemented. Next step: the standalone prototype +
proto-tests (step 1) before any change to live rdvq code.
