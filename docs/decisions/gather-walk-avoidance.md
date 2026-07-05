# Bounding the gather walk: seq-gated skips and a tree-as-index

> Decision record (2026-07-05, design session with PN). Companion to
> `weighted-acquisition.md` (the demand queue, overdraft, and the weighted/plain
> limiter split) and `permit-core.md` (the forest). **Status: designed; the
> `enqueue` w=1 gate is implemented; the seq/index scheme is a designed follow-up,
> sequenced with the weighted-acquisition surface work.**

## The problem, framed for a library

`internal/permits` does two kinds of forest walk on the acquire-miss path, each
`O(caches)` and each taking per-`cacheList` mutexes:

- **`searchList`** — the steal walk (find a borrowable victim), in `acquireInto`.
- **`walkCounts`** — the overdraft proof (any `inUse`? any borrowable?), in
  `headGather` after a gather miss.

This is a **library, not a service**: we cannot measure a "representative"
workload, because every workload is reachable. The deliverable is therefore a
**bound on the worst case** — specifically, on how much walk work a demand can be
made to do relative to real progress (actual capacity changes). Anything that lets
walk-count outrun capacity-change-count is a worst-case defect to close
structurally, not an efficiency nicety to measure.

## The redundancy

Trace a contended `w = 1` acquire that must wait (saturated pool, empty head slot):

1. `Acquire` → up-walk miss → `acquireInto(c, 1)` → **searchList #1** (nil) → `enqueue`.
2. `enqueue` → instant head → `headGather` → `acquireInto` → **searchList #2** (nil),
   then `walkCounts` (another walk).
3. `AcquireWait` confirm → `Acquire` → `queuedAcquire` → `headGather` →
   **searchList #3** (nil), then `walkCounts` again → park.

≈ five full-forest walks before the first park, and nothing material changes between
them (the `NewChild` body cache `enqueue` creates is empty — never a victim). Of the
`searchList` calls: #1 (fast-path steal) and #3 (register-then-confirm) are
load-bearing and irreducible; **#2 is pure redundancy for w=1** — reached only after
#1 just failed with no change, and #3 re-does it anyway. (#2 is the *first* gather
for w ≥ 2, whose fast path does not gather, so it stays.)

**Implemented (the `enqueue` w=1 gate):** `enqueue` calls `headGather` inline only
for `uw >= 2`; a w = 1 instant head returns the miss and lets the caller's
register-then-confirm recheck drive the single as-head gather. Removes #2 and its
`walkCounts`, standalone, independent of everything below.

The rest of this record bounds the *remaining* walks (#1, #3, and every re-drive).

## The scheme: a seqlock plus three cached facts

A monotonic pool `changeSeq`, bumped by every event that can make a previously-nil
walk succeed. A walk reads `changeSeq` **before** it walks; a nil result records the
pre-walk seq; a later walk **skips** while the recorded seq still equals `changeSeq`.
Standard seqlock: a change *during* a walk bumps to a higher seq, so the next walk
re-runs — no false skip. Because creating capacity always bumps, `changeSeq == S`
guarantees nothing was created since the walk that recorded `S`; the cached "empty"
is therefore still valid. Writes may be sloppy (a stale-low cached seq only forces
an extra walk, never a missed permit).

### The bump set (and why `deposit` is NOT in it)

Bump `changeSeq` (and, for the index below, stamp the cache chain) on exactly the
events that create capacity a **waiting other** could use:

- `counts.release` (inUse↓ → borrowable↑)
- destroy-drain and `SetMaxConcurrency` raise (Resource free pool grows)

**Not** `checkout` / `acquireLocal` / `stealOut` (they remove or don't add
borrowable), and — the subtle one — **not `deposit`.** A gatherer's partial-gather
deposits into its *own* home; under the head-of-line barrier that hoard is usable
only by the head's own `acquireInto` loop (everyone else is gated; descendants never
gather), so it is not "capacity a waiting other could use." If `deposit` bumped, a
weighted head would invalidate its own `notEnoughSeq` (below) every gather and never
skip — self-defeating. The hoard re-syncs at barrier exit: occupy on satisfy (inUse,
not borrowable), drain-with-bump on invalidate. **This no-bump rule leans on the
barrier gating other stealers** — a real coupling, and part of the completeness
invariant below.

### Layer 1 — pool `nothingBorrowableSeq` (coarse, shared)

The seq at which a full walk last confirmed **zero borrowable anywhere**. When
`changeSeq == nothingBorrowableSeq`, no steal helps *anyone*, weight-independent — so
the fast-path steal (#1) and every w = 1 waiter skip the walk and go straight to
miss/enqueue. One acquirer establishes it; all others ride it. This is the dominant
plain-semaphore / saturated case, made free.

Exclude nuance: the cached fact is "nothing *anywhere*" (exclude nothing), which is
safe for every caller (global-empty ⟹ empty for any exclude). A w ≥ 2 demand whose
own home holds sub-weight borrowable may take one extra walk (its steal excludes
home and finds nothing) — harmless, never a wrong skip. For w = 1 it cannot happen
(`acquireLocal` already took any home borrowable).

### Layer 2 — per-`Demand` `notEnoughSeq` (fine, private, weight-aware)

The seq at which *this* demand's gather last came up short of *its* `w`. When there
IS borrowable (Layer 1 does not fire) but a specific weighted head keeps failing to
assemble `w` amid churn, the head skips its own re-walks while `changeSeq` is
unchanged. Stored as a **seq, not an amount** — deliberately, to avoid maintaining a
global available-*amount* counter (which would be a hot-path contended write). The
weight is the demand's stamped `w` (stable per episode; a re-presentation with a
different weight panics), so the fact is naturally keyed.

### Layer 3 — per-`Cache` stamp index (weighted pools only) = the borrowable index

Each cache carries a dirty stamp. An admitting event stamps its cache and propagates
**up the parent chain**. A walk prunes: descend into a subtree only if its stamp
exceeds `nothingBorrowableSeq`; a subtree stamped `≤ nothingBorrowableSeq` has gained
no borrowable since we last confirmed it empty (any gain would have stamped its root
higher), so skip it whole. This turns the gather walk from `O(caches)` into
`O(changed paths)` — it is the "index" for where borrowable might be, realized by
reusing the tree instead of a separate heap, folding straight into the existing
top-down walk. (Removal — a steal — needs no stamp: it cannot turn none into some.)

**Propagation short-circuits**, so it is not `O(depth)` every time: stamping upward
may stop at the first ancestor already stamped `> nothingBorrowableSeq` (it and its
ancestors are already "dirty enough" to be descended; re-stamping changes no walk
decision). After a full-empty walk raises `nothingBorrowableSeq`, the first admitting
event re-propagates to the root; every one after that short-circuits high until the
next full-empty confirmation. So `O(depth)` occasionally, `O(short)` under churn.

**Weighted pools only.** The `O(depth)` propagation is pure cost for a plain/w = 1
pool, which never has the repeated wide-forest gather this solves — Layer 1 catches
saturation, and a lone borrowable is found near the cold front by camping. So the
index is maintained only when the pool is weight-capable (the same bit as the
`weighted-acquisition.md` constructor split: `overdraftPolicy != nil` / the weighted
resource type). Plain pools pay zero per-node cost.

## What it buys (the bound)

- **Redundant walks → gone.** With the barrier there is one gatherer (the head), so
  walk work is bounded by **real capacity changes**, pool-wide — not by wakes,
  retries, chain-probe tails, or the `headGather` race loop. That is the worst-case
  guarantee a library owes; walk-count no longer outruns progress.
- **Weighted gather: `O(W · changed-paths)`** instead of `O(W · caches)`, via the
  Layer-3 index.

## The cost: a completeness invariant (the footgun)

Every capacity-admitting event must bump `changeSeq` (Layers 1–2) **and** stamp its
chain (Layer 3). A missed bump or a missed stamp is a **silent wedge** — a
borrowable permit that a skip/prune never finds — the nastiest failure class
(intermittent hang, not a loud panic). Mitigations: concentrate the bump+stamp in
the two or three `counts` methods (`release`, and the Resource-return sites) plus the
raise hook, and add an assertion/oracle that ties "some borrowable exists" to
"`changeSeq` advanced past the last empty seq" in the model. This invariant is the
price of the bound; it is worth paying *because* it is a bound and not a measured
optimization, but it must be guarded deliberately.

## Layering summary (all gated on the weight-capability bit)

| pool | Layer 1 (pool `nothingSeq`) | Layer 2 (demand `notEnoughSeq`) | Layer 3 (per-cache index) |
|------|:---:|:---:|:---:|
| plain / w = 1 | yes | — | — |
| weighted     | yes | yes | yes |

Plain pools stay on `changeSeq` + `nothingBorrowableSeq` + front-camping and pay no
per-demand or per-node cost. Weighted pools add the private `notEnoughSeq` and the
tree index, paying `O(short)`-amortized propagation to bound the wide-forest gather.

## Sequencing

- **Landed now:** the `enqueue` w = 1 gate (removes redundant walk #2).
- **Follow-up (with the weighted-acquisition surface / split):** `changeSeq` +
  `nothingBorrowableSeq` (both pool tiers), then `notEnoughSeq` and the per-cache
  index for weighted pools. Each rests on the one bump/stamp completeness invariant;
  land with the assertion/oracle, not without.
- **Prerequisite analysis:** enumerate every wake/re-drive site and show, for each,
  "fires only on a real capacity change" (safe) or "can fire redundantly" (a source
  the seq gate must cover). That enumeration is what proves the bound rather than
  assuming it.
