# rdvq outbox recovery: the empty-behind-full miss

This document records an investigation into a specific behavior of the rdvq
outbox pool's non-blocking push (`Queue.TryPushBack`), the design built to fix
it, and — at least as importantly — the **measurement methodology** that was
required to reach a trustworthy answer. The headline result is that the fix
(`emptyOutboxes`) cuts worst-case producer-emit latency by roughly a third in
psg's actual posting pattern; the headline lesson is that it took several wrong
benchmark models, each giving a confident *wrong* answer, before the right one
revealed it.

## The behavior

The destination-owned outbox pool keeps live outboxes on one lock-free queue
(`outboxes`) and the ones currently holding a value on another (`fullOutboxes`).
A non-blocking `TryPushBack`, finding no waiting receiver, must decide whether a
free (drained, empty) outbox is available to drop the value into.

The simple implementation checks only the **front** of `outboxes`: if it is a
drained outbox, use it; if it is full, requeue it to the back and refuse. That
refusal is a **false negative** whenever a free outbox sits *behind* a full one
— the "empty-behind-full miss." The producer thinks there is no capacity when in
fact there is.

The question: does that miss matter, and is it worth recovering?

Two designs were compared:

- **front-check + requeue** (the simple one): O(1), no extra structure. Its
  requeue rotates the queue, so over repeated attempts the free outbox surfaces.
- **`emptyOutboxes` recovery**: a third lock-free queue holding *hints* to
  drained outboxes. `TryPushBack` pops a hint and claims that outbox directly,
  no scan, no rotation. This needs a small per-outbox state machine
  (`empty → filling → full`) with generation-guarded CAS transitions so a stale
  hint (its slot refilled, or the outbox reused from the pool) can never act on
  the wrong incarnation. The generation is **monotonic** (bumped on reclaim,
  never reset) precisely so that a stale hint's claim CAS can only ever match the
  exact drained incarnation it was minted for — which makes cross-queue hint
  reuse through the shared `omnipool` safe.

(An earlier, rejected variant — a gated bounded *scan* of `outboxes` for the free
outbox — is preserved in git history as the `a3cbdbc` checkpoint. It recovered
the misses but its requeue churn manufactured new refusals; see "The journey.")

## What matters, and why max

The thing to optimize is the **tail**, and specifically the **max**, of per-item
*emit latency* — the time from a producer's first post attempt to a successful
post. Two reasons:

1. rdvq sits on a hot path crossed many times per workflow (task → funnel →
   skim, per job, nested through subjobs). Per-hop tail latency **compounds**
   end-to-end, so a per-hop effect too small to clear a single-queue noise floor
   can still dominate a workflow's worst case.
2. The worst case is what users feel. A median or even p99 improvement that
   leaves the max untouched is not worth complexity.

## The journey (and why the first answers were wrong)

The honest part. The first several measurements all concluded "recovery does not
help — front-check wins," and all of them were **artifacts of an unfaithful
benchmark**. Each correction was forced by a specific flaw.

1. **Tight-spin retry erases the cost.** The first harness had each producer
   retry a refused post in a `runtime.Gosched` tight loop. Under tight-spin a
   producer brute-forces any freed slot into use within microseconds — so the
   front-check's requeue rotates the queue fast and the free outbox never sits
   idle. The recovery had nothing to win because the thing it prevents (freed
   capacity going unused while items miss it) *cannot happen* under tight-spin.
   `benchstat` dutifully reported "no max difference (p≈0.98)."

2. **The metric split was missing.** `refuse/op` alone conflates the recoverable
   *miss* (a free outbox existed) with genuine backpressure (none did). Adding a
   `miss/op` probe (refusals taken while an empty existed) was needed to tell
   whether a design was failing to recover or correctly refusing.

3. **The drain distribution wasn't being measured.** The consumer was a
   heavy-tailed `time.Sleep` (Pareto, α≈1.05), but `time.Sleep` only guarantees a
   lower bound; under CPU contention the *short* draws overshoot ~3×. The
   achieved drain mean was ~1.36× the intended one, which meant the **load-factor
   labels were wrong by ~1.36×**: the "underload 0.8" rows were actually load
   ~1.1 — overloaded. Conclusions about the underload regime had been drawn from
   overloaded runs. The fix was to *measure* the achieved drain durations and the
   implied actual load, and sweep lower labeled loads to hit true underload.

4. **The retry model didn't match psg.** This was the decisive one. psg's actual
   posting (see "How psg posts," below) is **postpone-and-re-drive**: a refused
   item parks on the "outbox freed" wakeup and is re-driven **once per drain** —
   `outboxFreed.Notify` wakes exactly **one** waiter per freed slot. The tight
   spin (and a `cond.Broadcast` variant that woke *all* waiters) both let
   front-check rotate the queue many times per drain, erasing the cost. Only when
   the harness woke **one** waiter per freed slot (`sync.Cond.Signal`, modeling
   `outboxFreed.Notify`) did the real dynamic appear: front-check's woken
   producer keeps hitting the full front and re-parking for *more* drain cycles
   while the freed slot sits, where `emptyOutboxes` claims it on the first
   wakeup.

Getting the harness itself right also took work: a naive listener-park producer
deadlocked, because the one-shot rdvq `Listeners` is the wrong primitive for a
persistently-parking waiter (a leaked notify entry absorbs a wakeup meant for a
genuinely-parked producer). The benchmark uses a leak-free `sync.Cond` instead.

## The result

With the faithful wake-one-per-drain model (`BenchmarkQueueEmit`, `cond.Signal`),
`benchstat` over n=12, front-check → emptyOutboxes:

| metric | underload (actual ~0.68) | overload (actual ~1.97) |
|---|---|---|
| **emit-max** | **−34.3%** (p=0.002) | **−32.8%** (p=0.000) |
| emit-p99.9 | −10.6% (p=0.021) | −28.1% (p=0.000) |
| emit-p99 | +4.2% (noisy) | −21.5% (p=0.000) |
| `miss/op` | tiny both | recovered ~80% |

`emit-max` geomean **−33.5%**; `emit-p99.9` geomean **−19.8%**. Throughput
(`sec/op`) is within noise. So `emptyOutboxes` cuts worst-case emit latency by
about a third, significantly, in both regimes — and that per-hop reduction
compounds across a workflow.

The mechanism, stated plainly: when a drain frees an outbox and wakes one parked
producer, front-check makes that producer probe the *front* of `outboxes` — but
the freed outbox was marked empty in place, typically not at the front — so it
misses, re-parks, and waits another whole drain cycle, while the freed capacity
sits behind a full front. `emptyOutboxes` hands the woken producer the freed
outbox directly. Recovery converts a multi-drain-cycle wait into a single one.

## How psg posts (why the wake-one model is the faithful one)

Every outbox `TryPushBack` call site is the same shape — the `workq.Post` LISTEN
pattern (`job.go` task/skim, `funnelpool.go` funnel, workq `incoming`):

```go
for {
    if tryPost() { return true }                 // posted
    if !ex.ShouldBlockOrPostpone() { return false }
    if !meta.ShouldBlock() {                      // nested → postpone
        ex.AddToListeners(q.ListenersFor(…))      // subscribe to "outbox freed"
        if tryPost() { return true }              // retry once (close the race)
        return false                              // POSTPONE — re-driven on the wakeup
    }
    q.PushBackFunc(…)                             // top-level → block
}
```

All four sites post *the same single item* and, on a miss, either **postpone**
(subscribe to the outbox-freed wakeup, retry once, return — re-driven when an
outbox frees) or **block**. None ever tries a different item. So the relevant
model is "the same item is re-driven once per freed-outbox wakeup," which is
exactly what the `cond.Signal` harness reproduces. (A separate concern —
oldest-first work-queue ordering being perturbed by a miss — is real but soft:
it's an internal anti-starvation heuristic, transiently perturbed, not a
user-facing guarantee. The latency win is the load-bearing argument.)

## Methodology lessons

Generalizable beyond this change:

- **Model the real retry/wakeup discipline.** A synthetic "retry until success"
  can hide or invent costs depending on *how* it waits. Tight-spin and
  wake-all both erased a real opportunity cost that wake-one exposed. Match the
  production wakeup fan-out (here: one waiter per freed unit).
- **Measure what you assume.** The `time.Sleep` consumer wasn't delivering the
  intended distribution, and the load-factor labels were off by 1.36×. Always
  instrument the achieved workload, not just the requested one.
- **Split the metric.** `miss/op` (recoverable) vs genuine backpressure was the
  difference between "it didn't help" and "it recovered the misses but the model
  hid the payoff."
- **Max is the product metric; chase it explicitly,** and remember it compounds
  across hops. p50/throughput were flat throughout and would have said "no
  change."
- **Seal with `benchstat -count`.** Single runs flip-flopped on these
  heavy-tailed percentiles; only n≥12 made the ~⅓ max win legible and
  significant.

See [[feedback_bench_methodology]] (user memory) for the standing version of
these.
