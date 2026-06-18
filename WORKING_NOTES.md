# PSG-Go Combiner Branch Working Notes

This document contains working notes and context for development on the `combiner` branch.

**►► START HERE (active work): the Worker-pool + workq CONSOLIDATION.** The rdvq
work below (outbox recovery → reclamation → gen-stamped-hint bug fix → vestigial
`Sender`/`Receiver`/`Waiter` removal, all committed and green) is DONE — it was in
service of the consolidation (those handles were per-worker `E` state being
untangled). Goal: collapse the three live producer paths (task/skim/funnel) onto
the single unified `workq.Post` → `workq.Queue`, driven by `worker.Pool`/`Worker`,
until there is just `workq.Queue.incoming`.

**Resume at: the global-substrate activation (the corrected model — NOT the
per-job "cp-5 FunnelPool→worker.Pool cutover," which is RETRACTED; see "CORRECTION
(2026-06-16, PN)" below).** Authoritative plan = **"Next (corrected)"** /
**"wave-5b ctx model — CONVERGED"** below: one global `defaultPool` + one shared
`workq.Queue` + a context-free unified `E` already exist and compile but are
DORMANT (nothing `Post`s yet). Remaining sequence: (1) wire the wave-5b ctx model
into `worker.Pool` (stop-chan → `poolCtx`, expose it); (2) build the new per-`Wave`
substrate (`waveCtx`, per-wave governor + in-flight counter + skim queue +
Acquire/Release the global pool + per-wave flusher); (3) wire uniform `submit`
(non-top-level → `Post`; top-level → governor gate → `Post`; per-wave execCtx
borrow); (4) collapse the task/funnel/skim producers onto `defaultPool.Post` /
skim-queue `Post`; (5) delete `taskExEnv`/`cpWorker`/cpstate + the per-job pools +
legacy `Pool`/`Wave` binding. The channel-vs-rdvq queue-impl question is DEFERRED
until then (PN: by then we'll know what that one queue actually needs).

**SEQUENCING (PN): DESIGN REVIEW FIRST — ✓ COMPLETE (2026-06-17).** Written design
+ all open questions resolved with PN in **`docs/global-substrate-activation.md`**
(grounds the converged model in the current code: the `Pool`=job naming trap, the
global/per-wave split, the wave-5b ctx model, producer collapse onto `Post`, the
per-wave flusher, deletion list, checkpoint sequence §8, and the resolved Q1–Q6 +
dispatch-layering crux in §9–§10). Key decisions: jobstate is per-Wave; keep
top-level ceremony (`suspend`/`reclaim` + `wait`/`yield` "old-before-new") and
`exEnv.ExecuteNowOrQueue` (subwave inline cases); skim shares the wave governor (no
skim-first); limiter wiring transitional.

**PROGRESS:** **CP1 DONE + COMMITTED (`6019c6d`)** — `worker.Pool` `stop chan` →
`poolCtx`/cancel, exposed via `PoolCtx()` for waves to derive `waveCtx`; dormant,
green (build/vet/`-short`/lint0/`TestBySimulation -race`).

**►► RESUME HERE (next session, FRESH CONTEXT recommended): the CP2+CP3 cut.** CP2
does NOT separate from CP3 — they fuse at the **ctxMeta seam** (the execCtx-shell
carries a reusable `*ctxMeta`, but ctxMeta creation is `Pool`(job)-bound:
`ctxMetaMap` cache + `j.ctx` AfterFunc + `ctxMeta.job *Pool`; the shell replaces
that caching and `ctxMeta.job`→wave/lifecycle). `poolCtx` was the only cleanly
dormant piece. **First thing next session: settle the "ctxMeta in the per-Wave
model" decision** (design doc §8 step 2 FINDING + KEY CP3 DECISION: two meta
lifecycles — user-dispatch cached `topLevelExEnv`, worker-exec shell `workerExEnv`;
re-target `job`/`parentJobs`/`heldRequest`-parent-walk off job identity onto
wave/lifecycle). Then build: per-Wave `jobstate` + governor + skim `Queue` +
in-flight + execCtx-shell pool (model `funnelInstanceQueue`, omnipool+nbcq) +
flusher; wire `submit` + execCtx borrow; collapse the 3 producers onto `Post`;
unified-E body entry (funnelop.go:801); delete legacy substrate (cpstate/cpWorker/
taskExEnv/FunnelPool/per-job pools/`*PostWork`). Validate (funnel+task+skim +
`-race` + sim). The notes below + the design doc are the anchor.

## ►► rdvq outbox recovery — LANDED FINDING + productionization (rdvq thread, DONE)

Full writeup: `docs/rdvq-outbox-recovery.md` (the investigation + the
methodology journey — keep it, the lessons are general). The **benchmark is the
scheme-pin**: `internal/rdvq/queue_bench_test.go` `BenchmarkQueueEmit` + the
`RDVQ_FRONT_ONLY=1` A/B toggle is the executable justification + regression
guard. The doc records *why*; the benchmark proves it and re-proves it.

**Finding (counterintuitive, hard-won):** the non-blocking `TryPushBack`
"empty-behind-full miss" (front-check refuses while a free outbox sits behind a
full one) IS worth recovering. The `emptyOutboxes` hint-queue design cuts
worst-case emit latency by **~⅓** (`emit-max` geomean −33.5%, p99.9 −19.8%,
significant n=12), and that compounds across workflow hops. This reverses
several earlier "front-check wins / red herring" conclusions, which were
artifacts of unfaithful benchmark retry models (tight-spin / wake-all both let
front-check brute-force-rotate, erasing the opportunity cost). The faithful
model wakes **one** waiter per freed slot (`cond.Signal` ≡ `outboxFreed.Notify`)
— matching psg's actual postpone/re-drive — and only then does the cost appear.
See the doc for the four corrections (instrumentation, achieved-distribution,
load-factor mislabel, retry-model fidelity).

**State:** the recovery PROTOTYPE is **committed** (47975c2) — `empty/filling/full`
state machine (`outbox.go`, monotonic gen for cross-queue hint safety),
`emptyOutboxes` hint queue, `nbcq.Empty()`. On top of it, **reclamation (item 1)
is now implemented in the working tree (uncommitted)** per the converged design
in `docs/rdvq-outbox-reclamation.md`, which also strips the bench-only
instrumentation (item 3) and drops the `RDVQ_FRONT_ONLY` toggle + front-only
branch (item 2). Validated green: rdvq full `-race` (162s, incl. the new
`TestTryPushBackSaturation` regression); `TestBySimulation` `-race` + deep
`rapid.checks=500`.

**⚠ CRITICAL BUG found + fixed during productionization (gen-stamped hints).**
The `emptyOutboxes` hint was a bare `*outbox`, and the hint-claim read the
outbox's CURRENT generation (`g,_ := ob.loadState(); ob.claimEmpty(g)`) — so the
monotonic-gen guard the whole design relies on NEVER FIRED. A hint outliving its
incarnation (outbox reclaimed → `Reset` → back in the pool) read the pool
generation, `claimEmpty` succeeded, and a producer filled an outbox WHILE IT SAT
IN THE FREE LIST → double presence → never-drained channel inside "non-blocking"
`TryPushBack` → deadlock. Latent pre-reclamation (a hinted outbox was always live
on `outboxes`); reclamation exposed it. **Fix:** hints are now `outboxHint{ob,
gen}` stamped at mark-empty; claim is `ob.claimEmpty(hint.gen)` at the MINTED gen.
Only `emptyOutboxes` needs this — `outboxes`/`fullOutboxes` membership IS the
liveness marker (popped before pooling), so their popped entries are always live.
No new alloc: nbcq already pools the stored value (`valuePool.Clone`/`Put`).
Found by saturation repro + a lock-free op-history ring (runtime/trace perturbed
the timing and HID it — it's a logical/ABA race, not a data race). Regression
guard: `TestTryPushBackSaturation` (saturated TryPushBack+PopFront, -race).

**Reclamation design (converged — see `docs/rdvq-outbox-reclamation.md`):** the
key insight is that reclamation can ONLY happen at an `outboxes` front-pop (nbcq
has no interior removal; a dangling entry left behind is a double-presence
corruption hazard), and `borrowToFill` runs only under backpressure (no slack to
reclaim). The resolution: **piggyback an O(1) reclaim probe on a successful
`emptyOutboxes` hint-claim** — a successful claim IS the "we have slack" signal,
so it fires exactly when reclaim is wanted and auto-backs-off under pressure.
After delivering, pop one front of `outboxes`; if `empty` + `claimEmpty` wins,
`Put` it (immediate reclaim; `Reset` bumps gen → stale hint inert); else push
back. **Skip-self** (the just-claimed outbox), bounded to two pops by holding it
off-queue. **No counter, no floor, zero new global state** — the probe
self-regulates (front usually full at high utilization → no reclaim; usually
empty when over-provisioned → reclaim). Idle set → 0; bursts re-`Get` from the
warm `sync.Pool`. `reclaimOutbox` mirrors the Checkpoint-2 `reclaimInbox`.

**Productionization plan (remaining):**
1. ✓ **Reclamation / scale-down** — DONE (working tree). Hint-claim probe +
   `reclaimOutbox`; immediate-Put, skip-self, no counter.
2. ✓ **Toggle dropped** — the `RDVQ_FRONT_ONLY` var + the front-only `else` branch
   in `TryPushBack` are removed; the hint-claim recover path is now the single
   unconditional production path. The A/B counterfactual is preserved in git
   (≤47975c2) + `docs/rdvq-outbox-recovery.md`. (PN: "keeping the benchmark is
   sufficient to pin the scheme unless a truly better one is found.")
3. ✓ **Strip bench-only instrumentation** (`empties`/`missRefusals` + `miss/op`) —
   DONE alongside item 1.
4. ✓ **Validated hard:** `go vet ./...`; rdvq full `-race` (incl.
   `TestTryPushBackSaturation`); `TestBySimulation` `-race` + deep
   `rapid.checks=500`; `BenchmarkOutboxHintCycle -benchmem` = **0 allocs/op**
   (gen-stamped hint is alloc-neutral; nbcq pools the value). TODO (deferred):
   extend the sim's outbox-accounting invariant to assert the single-`outboxes`-
   entry invariant.
5. **Commit** the productionized design (NOT yet committed). Keep
   `BenchmarkQueueEmit` + `BenchmarkEmitVsChan` as the long-term guards.

**Floor: measured and REMOVED.** An `outboxFloor`/`liveOutboxes` warm-reserve knob
was prototyped and A/B'd on the fixed code. It cuts the fastdrain refuse rate
3–7× and improves throughput 29–45% — but makes the **tail** (the primary metric)
worse (conc-512 fastdrain `emit-p99.9` 23→30 ms; conc-64 heavydrain `emit-max`
23→36 ms), is neutral/harmful under backpressure, and needs a concurrency-tracking
target to be production-useful. Net negative under tail-primary priorities →
removed. The no-floor probe is the shipped design.

**Deferred:** the `retiring` 4th state (full-outbox relief accelerator at the
requeue points) — documented in `docs/rdvq-outbox-reclamation.md` but NOT built;
add only if a bench shows relief-regime shrink lag matters.

**Benchmarks added:** `BenchmarkEmitVsChan` (saturated head-to-head: `rdvq` vs
`chan-nb` [non-blocking select + identical cond-retry postpone harness] vs
`chan-block` [blocking lower bound], swept conc {8,64,512} × drain {fast,heavy});
`BenchmarkOutboxHintCycle` / `BenchmarkChanCycle` (single-threaded zero-contention
base cost); `TestTryPushBackSaturation` (saturation regression guard).

## ►► rdvq overhead — separate performance investigation (NOT this branch's scope)

The head-to-head benchmarks surfaced a real, broad finding that is independent of
the reclamation work and deserves its own effort. **rdvq's emit path is ~27× a
plain channel at the zero-contention base** (`BenchmarkOutboxHintCycle` 1257 ns/op
vs `BenchmarkChanCycle` 46 ns/op, both 0 alloc) — the two-tier inbox/outbox + hint
mint/claim + reclaim probe machinery is intrinsically heavy. Under saturation it
is ~4–6× `chan-nb` on throughput, driven by a **much higher refuse rate**: rdvq
grows the outbox set only when `outboxes` is *completely empty* (otherwise it
refuses rather than allocating), so it runs with a far smaller effective buffer
than a `nProducers`-slot channel and refuses into the expensive cond-retry/
postpone path far more (conc-512 fastdrain: rdvq 38% refuse vs chan-nb 0.5%). Two
threads for a future investigation: (a) the **base per-op cost** of the machinery;
(b) the **"grow only when empty" buffer-sizing policy** that under-buffers and
over-refuses (the floor band-aided this but hurt the tail — the real fix is the
policy). NB the fair baseline is `chan-nb` (non-blocking, same postpone cost), not
`chan-block` (blocking parks the producer — which psg's architecture forbids).

This sits ON TOP of the `Sender`/`Receiver`/`Waiter` gut (checkpoints 1+2,
committed) and Checkpoint 3 (✓ vestigial types removed — `a18bbb7`).

## ►► rdvq integration (Checkpoint 3 — ✓ DONE, `a18bbb7`)

Decided sequencing (PN): **gut internals first, defer type/signature removal.**
Land the destination-owned pool while keeping `Sender`/`Receiver`/`Waiter` on
every signature (as vestigial `struct{}` params), one seam at a time, each a
green checkpoint; the eventual deletion of the types + params is then a purely
mechanical pass (checkpoint 3, next).

**✓ Checkpoint 1 — `Sender` gutted (DONE, green this commit).** rdvq core
rewritten to the destination-owned **outbox pool**: `outboxes` (borrow source) +
`fullOutboxes` (drain source) + `outboxPool` (`omnipool.Pool`) + gen-CAS reclaim
(one atomic `state` word per outbox), replacing the per-`Sender` outbox map,
per-outbox refcount, and per-outbox listeners. Per-outbox listeners collapsed
to ONE queue-level `outboxFreed Listeners` ("an outbox freed" wakeup), fired by
the draining receiver when `markReclaimable` sticks. `Sender` is now `struct{}`
with a no-op `Release`; its param is `_`-ignored in `PushBackFunc`/`TryPushBack`/
`ListenersFor`. Signatures unchanged → zero ripple, whole module still builds.
Validated: rdvq short+`-race`+full-stress (141s `-race`); `TestBySimulation`
short+full+`-race`+1000-check deep sweep (114s) all green. Design + the
non-blocking/conservation reconciliation written up in the "Checkpoint 1" note
under "rdvq Sender redesign" below.

**✓ Checkpoint 2 — `Receiver` AND `Waiter` gutted (DONE, green this commit).**
Scope was larger than "just Receiver": `Receiver` embeds `outboxWaiter Waiter`,
and `Waiter` is used standalone across the wait-side (`meta.Waiter()`, spawn/
work/block/limiter waiters), so gutting one forced gutting both (PN chose the
unified gut). One mechanism does it: the **inbox pool moved into
`inboxOnlyQueue`** (`borrowInbox`/`reclaimInbox`), and `inboxOnlyQueue.PopFrontFunc`
gained a `clean bool` return (drained + out-of-collection ⇒ safe to reclaim).
The **caller** still owns the borrow (so cross-iteration reuse + the
reuse-without-requeue marker path are preserved): `Queue.PopFrontFunc` borrows a
data inbox **lazily** (only when about to wait — fast path borrows nothing) and
reclaims iff clean; `Waiters.WaitFunc` borrows a wait-inbox per call, reclaims
iff clean. Abandoned inboxes stay in the LIFO stack / FIFO with their marker and
are drained by a sender/notifier then GC'd — exactly today's behavior, just not
map-held. `Receiver` and `Waiter` are now `struct{}` with no-op `Release`; their
params are `_`-ignored (`PopFrontFunc` passes `nil` to `outboxWaiters.WaitFunc`).
Ordering is provably preserved (the pool changes inbox-struct identity, not the
`inboxStack`/`inboxQueue` push/pop order or delivery). Signatures unchanged →
zero ripple. Validated: rdvq short+`-race`+full-stress (141s `-race`);
`TestBySimulation` short+full+`-race`+1000-check deep sweep (92s) all green;
whole module vets clean. Design in the "Checkpoint 2" note under "rdvq Sender
redesign" below.

*Known pre-existing flake (NOT this change):* `Example_observable` /
`Example_funnel` use `exmpclk` — an "imperfect" real-`time.Sleep` clock quantized
to 10ms — so under heavy `go test ./...` load a drifting sleep can shift the
quantized event-log order and fail the `// Output:` match (~1-2%). Results stay
correct; only the timing log moves. 250+ isolated runs of the change passed.

**✓ Checkpoint 3 — DONE (committed `a18bbb7`).** Deleted `Sender`/`Receiver`/
`Waiter` and stripped the no-op params from every signature and call site
(`PushBack*`/`PopFront*`/`ListenersFor`/`WaitFunc`/`Wait`; `workq.Post`; the
`workq.ExecEnv` interface; the psg exEnv providers + producers; the build-excluded
`dispatch.go` sketch; the `workq.Waiter` alias). Purely mechanical, behavior-
preserving; `doc.go` rewritten to the destination-owned pooled `Queue`. Validated:
build/vet, `-short ./...`, golangci-lint, and `-race` on rdvq + workq +
`TestBySimulation`, all green. (Also landed this session, on top of cp 1+2: the
rdvq outbox **reclamation** + the **gen-stamped-hint** use-after-reclaim bug fix —
commits `424e301`, `c8559de`; see the "start here" section above.)

**⇒ Consequence for the consolidation (the reason this mattered):** the
`Sender`/`Receiver`/`Waiter` handles were a chunk of the per-worker `E`/`ExecEnv`
state being untangled for the cutover. With them gone, `workq.ExecEnv` is now
`interface{}` (empty), `Worker.pull` no longer threads `state.Waiter()`, and the
unified funnel `E` (cp-4) no longer needs to satisfy any Sender/Receiver/Waiter
contract — it only needs the main-package `executionEnvironment` methods
(Lock/Unlock, group/queue stacks, `ExecuteNowOrQueue`). So the cp-4/cp-5 notes
below that reference `state.Waiter()` / "E satisfies ExecEnv via S/R/W" are now
SIMPLER than written. **Next consolidation step: cp-5 (the FunnelPool→worker.Pool
combined cutover), "ready to implement" — see "cp-5 cutover" below.** Best started
with FRESH CONTEXT against these notes (a major implementation phase).

Prototype proofs (`outboxpool_proto_test.go`, `outboxpool_reclaim_proto_test.go`,
`outboxpool_compare_test.go`) are superseded by the shipped implementation —
candidates for removal at a cleanup pass.

**State:** all foundation + designs committed, tree green. Foundation =
`workq.Queue`/`Worker[E]` (`c31d497`), `worker.Pool[E]` embedding the queue
(`0193aab`,`29aa0fd`), global `defaultPool`/`psg.Wait`/`workerExEnv` (`f361c24`),
wave-5b ctx model (`f584735`), rdvq design (`ce83a46`), outbox pool landed
(`4aed0fa`), inbox pool landed (this commit). rdvq is now handle-free internally:
`Sender`/`Receiver`/`Waiter` are all vestigial `struct{}`.

## Worker pool + workq consolidation — converged design (2026-06-14)

Reproducing the teardown deadlock (sim TEMP config: `Permits=1`,
`Inherit.Probability=1`, `Subjob.CancelProb=1`) showed the cancellation/drain
tangle is rooted in `Pool` conflating the **worker substrate** with **batch
lifecycle**, and that the real fix is the documented Pool/workq consolidation
(REFACTOR_PLAN Wave 4+5), brought forward. A long design thread (with PN)
converged on three internal building blocks; **the only public surface is
`psg.Wait()`**.

### `internal/worker.Pool[E]` (drafted)
Fungible, **context-free**, uncapped, demand-driven goroutine-lifecycle manager.
`NewPool(factory func() E)` — no ctx, no settings, no exported type. Workers
**persist across waves** (idle-scale-to-zero, fixed internal timeout, no
jitter/throttle — each idle worker independently times out and exits; no
synchronized re-arm stampede, so the legacy jitter+throttle are gone). Lifecycle:
`Acquire`/`Release` refcount (one per in-flight Wave) + `Wait` = graceful
quiesce+join (stop workers when refs hit 0 *iff* a Wait is outstanding; immediate
if refs==0; reusable after; `stop` captured per-worker at spawn). `psg.Wait()` =
`defaultPool.Wait()` (→ `streampool.Wait`/`DefaultPool.Wait` if an exported pool
ever returns). Each pool goroutine instantiates a `workq.Worker[E]` and drives it
in a loop; spawn rides the Queue's `unmetDemandFn`. Lives in `internal/worker/`
(mutual dep with Wave is gone — units bake in their own wave logic). Drafted in
`internal/worker/pool.go` + main `pool.go` (Wait glue); builds. **FIX NEEDED:
the factory must build the unified task/funnel exEnv, not `taskExEnv`** (see
"one E each").

### `internal/workq.Queue`
The combined work engine: **hides `Pending`** (incoming handoff) **and `Accepted`**
(fresh/postponed priority + scheduled/timed work + `unmetDemandFn`). Producer:
`Post(...)` — collapses the 3 near-identical `*PostWork` escalations (try →
`ShouldBlockOrPostpone` → listen/block → governor-notify → `Starting`). Consumer:
`DriveOne(ctx, *Worker[E])` — merges `ExecuteOne`/`TryExecuteOne`
(`drainScheduled → fresh → postponed → pull-from-incoming`). Timed:
`Schedule`/`Reschedule`/`ClaimForFlush` (hide `Remove`/`Expedite` — no callers).

### `internal/workq.Worker[E]`
The driver bound to a `*Queue`, holding the per-worker exEnv `E`.
`DriveOne`/`DriveUntilDrained`. Encapsulates the wait/notify ceremony
(`BlockFunc`/`WaitBehavior`/`blockConfirmer`/`AddToListeners`) and **one canonical
select** replacing `cpWorker.popSelect` + `Pool.skimSelect`, parameterized by
Receiver/idle?/done/deadline. **Block-and-help = a nested `Worker.DriveOne` on the
help-domain Queue** — dissolves `Pool.block` + `reclaimRequest`'s bespoke help
loop.

### Two engines, one E each
- **Task/funnel engine**: one shared `Queue`, driven by **`worker.Pool`
  goroutines**. **One unified `E`** — tasks and funnels are both `workq.Work` run
  by the same workers, so `taskExEnv` + `cpWorker` collapse into one
  integration-style exEnv (Sender + Receiver + group stack). `taskExEnv` existed
  only because task workers didn't run `ExecuteOne`.
- **Skim engine**: a separate `Queue`, driven by **user `Skim`/`SkimAll`
  goroutines** (drive-until-drained), with its own `E` (top-level exEnv). NOT
  `worker.Pool`. (Skimmers are the serial drain / backpressure source.)
- `workq.Worker[E]` is generic (two instantiations); `worker.Pool` is the
  single-E task/funnel one.

### Per-wave (not in the pool)
Work is tagged by wave. Per-wave: **cancellation** (`waveCtx`; the work's
`Execute` borrows a `WithCancel(waveCtx)` exec ctx → per-wave cancel reaches the
running body — the original wave-5b fix); **in-flight counter** (drain-completion;
a wave is done when its count hits 0, draining the shared Queue); **governor**
(admission — see the backpressure model below).

### Design principle: structural knobs only, minimal WIP (PN, 2026-06-15)
The framework exposes **no operational dials** — no idle timeout, no
max-goroutines, no jitter, no buffer sizes. The user's only levers are
**structural**: *topology* (which ops; which ops move together → a Wave; which
ops are interdependent → a shared scheduler) and *capacity* (a limiter's permit
count / rate). The framework derives everything operational from that structure
(when to spawn, drain, buffer, admit, prioritize). The buffer is fixed at **1**
(minimal WIP); input rate is automatically constrained to the throughput of the
**narrowest bottleneck** via backpressure to top-level admission. The wave
boundary and scheduler-sharing topology ARE the user's execution hints —
architectural, not operational. (This is why we keep deleting knobs.)

### Backpressure & admission model (settled, 2026-06-15)
- **Minimal WIP / buffer-1.** Every backpressure source is a buffer-1 on-deck
  slot. SATURATED = that slot is already full (one unsatisfiable item queued).
  Uniform across skimmers and limiter-**schedulers** — the saturation lives on
  the *scheduler's* on-deck candidate, not on the permits (all-permits-held is
  healthy, not saturated).
- **Limiting is post-admission, everywhere.** The permit is acquired at the
  **worker** (acquire-or-postpone in `Work.Execute`), never as an admission gate.
  A non-top-level submit (a body holding permit P_A) that had to acquire a permit
  before acceptance would block *holding P_A* → hold-and-wait deadlock (the
  documented suspend/resume livelock); it MUST be able to exit and release. Bonus:
  admit-then-limit gives the scheduler + work queue the full candidate set →
  better prioritization. The postponed candidate IS the scheduler's on-deck item.
- **Deadlock-freedom invariant: non-top-level submits are NEVER gated** —
  accepted unconditionally. Only **top-level** submits (user goroutine, holds no
  permit) are gated. Safe AND sufficient: in-flight / non-top-level work always
  flows, so every saturation is self-clearing (the gate only delays new top-level
  intake, never the work that relieves the pressure). So aggregating "any source
  saturated → gate" can over-throttle but **cannot deadlock**.
- **Routing (governor = the transmitter).** Each Wave owns one governor that
  **aggregates** the saturation of every source it feeds — its skimmers (direct)
  and its ops' limiters' schedulers (indirect: scheduler ⇽ limiter ⇽ op ⇽ wave).
  A top-level submit waits while any registered source is on-deck-full
  (`gov.Execute` == `ExecuteOrWait` on the governor). Wave-scoping is correct **by
  construction**, not coarse: the user draws the wave boundary to mean "moves as a
  unit"; finer independence = more waves / separate schedulers.
- **Shared schedulers = declared interdependence.** A scheduler shared across
  limiters (the `Inherit` case) intentionally couples **both** concurrency and
  backpressure — minimal-WIP, narrowest shared bottleneck paces input.
  ("Shared cap, independent backpressure" is deliberately unexpressible.)
  Independent ops use separate schedulers. A shared scheduler registers on the
  governor of each wave it is associated with (no-op where a wave has no
  top-level admission — e.g. a subwave fed only by non-top-level submits).

### Structural vocabulary
- **Resource** (semaphore / rate): pure capacity accounting; the one place a
  number lives ("what," not "how").
- **Scheduler**: the unit of backpressure-AND-scheduling coupling; user-facing
  and shareable. Sharing = interdependence. Carries the on-deck saturation signal.
- **Limiter**: binds scheduler + resource onto ops (`WithLimits`).
- **Wave**: the unit that admits / drains / cancels together; owns the governor
  that aggregates its sources and gates its top-level admission.

### Dispatch (settled)
ONE uniform `submit` for every op (no task/funnel/skim distinction). Non-top-level
= `Queue.Post` (handoff) unconditionally; top-level = wave-governor admission gate
(on-deck aggregate) then `Post`. The op's limiter is acquired post-admission at
the worker. Sketch: `dispatch.go`.

### What collapses (the slimming)
- `Pending` + `Accepted` → `Queue`.
- `ExecuteOne` + `TryExecuteOne` → `Queue.DriveOne`.
- `AddWorkFunc`/`TryAddWorkFunc` + `cpWorker.AddWork` + `Pool.addWork`/
  `addWorkWhileMaybeBlocking` → internalized in `DriveOne`.
- `cpWorker.popSelect` + `Pool.skimSelect` → one canonical `Queue` select.
- `taskPostWork` + `funnelPostWork` + `skimPostWork` → `Queue.Post`.
- `taskExEnv` + `cpWorker` → one unified worker exEnv.
- `BlockFunc`/`WaitBehavior`/`blockConfirmer`/`AddToListeners` → hidden behind
  `Worker`/`Queue`.

### Open questions (flagged, not yet decided)
1. **Kill the `postWork` layer?** Fold limiter-gate-in-`Execute` (→ postpone on
   reject) + the handoff escalation into `Queue.Post`, or is the `postWork`
   separation load-bearing for the full-handoff block case?
2. **Governor**: `Post` takes the wave's governor as a param (per-call
   backpressure) — confirm.
3. `AddWorkFunc`/`TryAddWorkFunc` likely dissolve into `DriveOne`'s blocking mode.

### Status / next
**Sketches landed** (first-cut, not hardened — for shape review only; carry
marked TODOs / conceptual accessors):
- `internal/workq/queue.go` — `Queue` (composes+hides `incoming Pending` +
  `accepted Accepted`): `Post` = pure handoff via `ExecuteOrWait`; `driveOne`;
  scheduled methods. `unmetDemandFn` = the one condition signal.
- `internal/workq/worker.go` — `Worker[E ExecEnv]`: `DriveOne`/`DriveUntilDrained`,
  the ONE canonical `selectWork`, `pull` (addWorkFn collapse), `Help` (=nested
  drive). TODOs: `handoffNotifier`, `blockBehaviorFrom`, `workWaitCh`, native
  `driveOne`.
- `dispatch.go` — uniform `submit(ctx, meta, ex, q, wave, w, deadline)`:
  non-top-level = unconditional `Post`; top-level = `wave.governor().Execute` then
  `Post`; q routed by op type (pool work queue vs wave skim queue). Plus
  `runUnderLimiter` — the post-admission limiter gate (head of `Work.Execute`).
- `internal/worker/pool.go` + `pool.go` — `worker.Pool[E]` lifecycle + `Wait()`
  (still over a raw `rdvq.Queue[Unit]`; to be rebuilt on `workq.Worker[E]`).

**Build order (dependency-first) — a major new implementation phase, best with
fresh context against these notes:**
1. Harden `workq.Queue` + `Worker[E]` to compile with stable interfaces: native
   `driveOne` (absorb/compose `Accepted`); `handoffNotifier` + `BlockBehavior`
   plumbing; the canonical select's `workWaitCh`.
2. Rebuild `worker.Pool[E]` to spawn goroutines that each construct a
   `workq.Worker[E]` on the shared task/funnel `Queue` and `DriveOne` in a loop
   (replacing the raw `rdvq.Queue[Unit]` draft). Fix `E` to the unified
   task/funnel exEnv (`taskExEnv` + `cpWorker` collapsed).
3. Build `Wave` around it: owns `waveCtx`, the per-wave governor, the skim
   `Queue`, the in-flight counter; `Acquire`/`Release` the pool; `Wave.Skim`
   drives a `Worker[E_skim]` on the skim `Queue` (`DriveUntilDrained`).
4. Wire `submit` + per-wave cancellation (work borrows the wave exec ctx) + the
   scheduler on-deck→governor registration.

Validator throughout: the committed sim TEMP config above (revert before each
commit). Exploration maps that grounded this (workq surface, the two driver
loops, the producer patterns) were captured via sub-agents this session.

### Implementation session 2026-06-16: green baseline + first-seam scoping

**Sequencing decision (PN): incremental in-place**, NOT the parallel build-up the
build order above literally describes. Each step makes a new block the *real* one
a legacy consumer uses, as a no-op/simplification, staying green (full suite)
throughout — per the refactoring principle "upgrade the foundation first; each
foundational step a simplification or no-op."

**Green baseline restored.** The draft sketches broke the build; fixed minimally:
- `queue.go:80` — `q.unmetDemandFn` (`RenotifyFunc`) → `rdvq.BufferedFunc(...)`
  conversion in `Post`'s `TryPushBack`. Semantically right: an item that had to
  buffer (no immediate taker) IS the demand signal, so firing `unmetDemandFn` as
  the handoff `bufferedFn` is correct.
- `dispatch.go` — build-excluded (`//go:build ignore`). It is the step-4 *wiring*
  sketch (references `Wave.governor()`/`blockBehaviorFor`, not yet built); dead
  code that broke the root package. Drop the tag when wiring `submit`.
Result: `go build ./...`, `go vet ./...`, `go test -short ./...` all green (modulo
the known psgwf `Example_clientTimeout` timing flake — passes 5/5 on re-run).
`workq.Queue`/`Worker[E]`, `worker.Pool[E]`, `psg.Wait` now compile but are dead
(unadopted).

**First-seam analysis — `FunnelPool` is the exact `workq.Queue` template.** Its
field trio maps 1:1: `funnelQueue workq.Pending` = `Queue.incoming`;
`workQueue workq.Accepted` = `Queue.accepted`; `cp.unmetDemandFn` =
`Queue.unmetDemandFn`. `funnelPostWork.Execute` (producer) = `Queue.Post`;
`workQueue.ExecuteOne(ctx, cpWorker.AddWork)` (consumer) = `Queue.driveOne` + the
pull; `cpWorker.popSelect` = `Worker.selectWork`.

**The entanglement that scopes the first seam.** A clean in-place adoption is NOT
a field-swap, because legacy `funnelPostWork.Execute` and `cpWorker` interleave
concerns that the new design moves *out*:
- **spawn** — `maybeSpawn`/`ShouldSpawn{First,}Goroutine`/`SpawnNotifier`
  (cpstate machine) → `worker.Pool` demand counter (`Enqueue`→`demand++` +
  `bufferedFn`). The legacy `tryPost` already passes `maybeSpawn` as the
  `bufferedFn`, mirroring `Queue.Post`'s `unmetDemandFn`.
- **governor/backpressure** — `governor.Waiting` inside `Execute` → the `submit`
  admission gate (top-level only).
- **idle/done/flush** — `cpWorker` idle-jitter + `cpstate.TryIdleExit`,
  `doneCh`/`doneErr`, `nextJobFlushCh`/`flushAll` → `worker.Pool` (fixed idle,
  no jitter; stop channel) + a preserved end-of-work flush hook.
Because the *simplification* of `Post`/`AddWork` depends on those concerns having
moved, there is no behavior-identical AND simplifying micro-seam: the
simplification IS the relocation. So the realistic plan is a sequence whose first
step is a structural foothold, then inward migrations, each green.

**Two refinements to bake in while hardening (confirmed against legacy):**
1. `Queue.Post` should take `BlockBehavior` as a *parameter* (as legacy
   `Governor.Execute` does), not derive it via the panicking `blockBehaviorFrom(ex)`.
2. The unified worker `E` is `integrationExEnv` (sender + receiver + group/queue
   stacks); `taskExEnv`'s "no receiver, single group" specialization disappears
   because pool workers always have a receiver now.

**Deeper grounding overturns "funnel is the easy first target" (2026-06-16).**
Reading the three producers + `cpstate` more carefully:
- **`ExecuteOrWait` doesn't fit the handoff.** `rdvq.Notifier` embeds
  `Listeners`/`Waiters` *by value*, but the per-sender drain listeners live
  *inside the outbox* (`ListenersFor(sender)` → `*Listeners`). The legacy
  producers pass that `*Listeners` straight to `AddToListeners`; ExecuteOrWait
  subscribes to its own `&notifier.Listeners`. So the clean `Queue.Post`-via-
  `ExecuteOrWait` sketch can't reuse per-sender drain listeners. `Post` must be a
  faithful hand-rolled port of the shared producer loop (tryPost → listen via
  `ListenersFor` → block via `PushBackFunc`+`BasicPushSelect`), NOT ExecuteOrWait-
  based. `ExecuteOrWait` is used only by `Governor.Execute` today; it stays the
  limiter/governor-gate mechanism, not the handoff.
- **The three producers share one loop skeleton**, differing in: bufferedFn
  (task `registerDemand`; skim `nil`; funnel `maybeSpawn`), a `waiting()` hook
  (skim/funnel `work.Waiting(governor)`), and the block-path select (skim/task
  plain `BasicPushSelect`; **funnel a custom `spawnWaitCh` select**).
- **Funnel is the HARDEST target, not the easiest.** Its block-path
  `spawnWaitCh` select + `SpawnNotifier.Listeners` subscription implement
  cap-aware elastic scaling: `ShouldSpawnGoroutine` is bounded by funnel
  `MaxConcurrency`, and `SpawnNotifier` wakes blocked producers when a spawn slot
  frees. This coupling is load-bearing while `MaxConcurrency` is finite — and it
  is exactly what `worker.Pool`'s uncapped demand model replaces. So a *clean*
  funnel `Post` (skim/task-style, spawn via `bufferedFn` only) is unsafe until
  `worker.Pool` owns funnel goroutines. **Funnel-wholesale entangles with
  `worker.Pool` adoption.**

**Honest meta-conclusion:** no seam here is small. The queues, the three
producers, the spawn lifecycle, and the governor are mutually coupled *by design*
— which is why the build order above chose parallel build-up (build the new stack,
cut over once) over incremental in-place. Incremental in-place is possible but
needs throwaway scaffolding (hooks/accessors) at each seam that approaches the
cost of the cutover. The cleanest in-place producer seam is **skim+task** (the two
`BasicPushSelect` producers, no spawn-notifier coupling), leaving funnel until
`worker.Pool` lands.

**DECISION (PN, 2026-06-16): (C) funnel→`worker.Pool` combined seam.** Migrate
`FunnelPool`'s goroutine substrate to `worker.Pool[E]` AND collapse its producer to
`Queue.Post` together, *replacing* the cpstate spawn machinery
(`ShouldSpawn*`/`SpawnNotifier`/`MaxConcurrency`) with `worker.Pool`'s uncapped
demand model — not preserving it. The funnel `Post` becomes the clean skim/task-
style handoff (spawn via `bufferedFn`=pool demand; no `spawnWaitCh` select).

**Checkpoint plan (each lands green; foundation first):**
1. **Harden `workq.Queue` — DONE (2026-06-16, uncommitted).** Faithful hand-rolled
   `Post(ctx, ex, sender, shouldBlock, w, onWait)` (tryPost → listen via
   `incoming.ListenersFor(sender)` → block via `incoming.PushBackFunc`+
   `BasicPushSelect`; `bufferedFn`+about-to-wait `fireDemand` = `unmetDemandFn`;
   `onWait` hook for governor downstream-registration). The `panic("TODO")` stubs
   (`handoffNotifier`/`blockBehaviorFrom`) are GONE — `Post` is NOT ExecuteOrWait-
   based (the per-sender `Listeners`-by-value snag). `driveOne` stays unexported
   (Worker, same package, calls it directly + accesses `q.incoming`); scheduled
   methods kept. `queue_test.go` pins the handoff (buffered+demand, direct
   rendezvous) — `-race` ×3 green; workq + root `-short` + lint all green. NOTE:
   `Post` has no caller yet (validated at checkpoint 5); `deadline` param dropped
   (handoff uses ctx, not a deadline) — re-add if submit-wiring needs it.
2. **Harden `workq.Worker[E]` — DONE (2026-06-16, uncommitted).** Real
   `selectWork` now takes `workWaitCh` directly; `pull` wraps `PopFrontFunc`
   inside `waiters.WaitFunc(state.Waiter(), confirmWaitFn, …)` (modeled on
   `cpWorker.AddWork`) to supply it — that was the missing plumbing. Added
   `Waiter() *rdvq.Waiter` to `ExecEnv`. idle/stop → `w.exit` → `ErrEndOfWork`
   so the driver loop stops. `execCtx` still returns `w.ctx` (driveCtx and the
   worker ctx coincide until Wave per-wave cancellation lands — cp 5+). Smoke
   test `TestWorker_DriveOne_ExecutesPostedWork` (minimal `testExecEnv` over real
   rdvq) drives Queue+Worker end-to-end; `-race` green, lint 0. The funnel
   integration (cp 5) is the load validator.
3. **Rebuild `worker.Pool[E]` — DONE (2026-06-16, uncommitted).** Replaced the
   draft's separate `rdvq.Queue[Unit[E]]`+`Enqueue` model (it predated the
   "drive the shared `workq.Queue`" decision). `Pool[E workq.ExecEnv]` now holds
   `queue *workq.Queue` + `newState func() (E, context.Context, context.CancelFunc)`
   (the main package owns the `ctxMeta` wiring → factory returns the worker ctx;
   keeps `internal/worker` main-package-independent). `runWorker` builds a
   `workq.NewWorker(queue, state, ctx, WithStop, WithIdleExit)` and loops
   `DriveOne`. **Spawn model (counter-free — avoids the documented demand-counter
   drift):** `DemandFunc()` (wired to `Queue.Init`'s `unmetDemandFn`) =
   `trySpawnWorker` triggers the first spawn; the **spawn chain** ramps — a
   freshly spawned worker whose first `DriveOne` returns `err==nil` (found+ran
   work) spawns a successor, one that returns `err!=nil` (idle/stop) does not, so
   the chain length tracks the backlog and self-terminates. `spawnConcurrencyLimit`
   bounds simultaneous spawns. Lifecycle (Acquire/Release/Wait/stop/rearm) kept
   verbatim. Generic + compiles standalone; load-validated at cp-5. The `pool.go`
   psg glue (default pool + `psg.Wait`) is build-excluded until cp-5 (its old
   `NewPool(func() taskExEnv)` signature is superseded).
4. **Unified funnel `E`** — design grounded (ctxmeta.go:329). `*integrationExEnv`
   already satisfies `workq.ExecEnv` (Sender/Receiver/Waiter via baseExEnv +
   receiver). But `E` must ALSO satisfy the main-package `executionEnvironment`
   iface (ctxmeta.go:233): Lock/Unlock, Group stack (PushGroup/PopGroup),
   QueueFunc stack, `ExecuteNowOrQueue`. So `E` ≈ `cpWorker` MINUS its lifecycle
   fields (idleTimer/doneCh/doneErr/idleTimerCh/nextJobFlushCh/followupFn/
   workRenotifyFn/newWork/err — all now owned by `worker.Pool`+`Worker`), i.e.
   `integrationExEnv` + `cp *FunnelPool` backref + Lock/Unlock + `ExecuteNowOrQueue`
   (→ needs a `Queue.ExecuteNowOrQueue`, surfacing `accepted`'s). OPEN: where the
   funnel end-of-work **flush** (`flushAll`/`nextJobFlushCh`) and **completion**
   tracking (`executeFunnel`'s `IncrementCompleted`, being deleted with cpstate)
   land — likely a preserved per-Pool flush hook + folding `executeFunnel`'s
   wrapper into `funnelWork.Execute`. THIS is why cp-4 isn't cleanly additive: it
   entangles with cp-5's "what moves out of cpWorker."
5. **Cut `FunnelPool` over**: `funnelQueue`+`workQueue` → `cp.queue workq.Queue`;
   `spawnNewGoroutine`+`cpstate` spawn+`goroutine()`+`cpWorker` → `worker.Pool[E]`+
   `Worker`; `funnelPostWork.Execute` → `cp.queue.Post`; preserve end-of-work
   flush + governor (transitional, until submit-wiring). Delete dead cpstate spawn
   machinery.
6. **Validate**: funnel suite + `-race` + sim green.

### cp-5 cutover — detailed design (grounded 2026-06-16, ready to implement)

Foundation done (cp-1/2/3, committed `c31d497`+`0193aab`). Added (uncommitted,
green): `Queue.ExecuteNowOrQueue` (delegates to `accepted`; the synchronous-
dispatch entry the unified `E` needs). The cutover lands as ONE commit (the
unified `E` is unexported+unused until wired → can't be a separate green commit:
`unused` lint). Pieces, with the verbatim seams:

- **Unified `E` (`funnelExEnv`)** — model on `topLevelExEnv` (ctxmeta.go:387):
  `struct { integrationExEnv; cp *FunnelPool }` + no-op `Lock`/`Unlock` (per the
  old `cpWorker`, NOT a mutex) + `ExecuteNowOrQueue → cp.queue.ExecuteNowOrQueue`
  + `executeFunnel(ctx, bc) { bc.Funnel(ctx, ee.Sender()) }` (drops
  `IncrementCompleted` — cpstate metric, write-only, unread). `integrationExEnv`
  already supplies Group/QueueFunc stacks + Receiver/Sender/Waiter, so this
  satisfies BOTH `workq.ExecEnv` and the main-package `executionEnvironment`.
- **`funnelWork.executeInner` (funnelop.go:804)**: `meta.executionEnvironment.(*cpWorker)`
  → `.(*funnelExEnv)`; `cw.executeFunnel` stays. `funnelInstance.funnel`
  (funnelop.go:561) `&c.op.funnelPool.workQueue` → `&cp.queue` (Schedule/Reschedule/
  ClaimForFlush move to `Queue`, already present).
- **Producer rewrite (`funnelPostWork.Execute`, funnelpool.go:241)** → collapses
  to `cp.queue.Post(ctx, ex, meta.Sender(), meta.ShouldBlock(), w.work, onWait)`
  where `onWait = func(){ w.work.Waiting(&cp.governor) }`. **Drops** the
  spawn-notifier block-select + `ShouldSpawn*` (spawn now rides `Post`'s
  `unmetDemandFn` = `pool.DemandFunc()`). Keeps the governor. On `posted`:
  `ex.Starting()` (Post does it) + `w.work = nil`.
- **`FunnelPool` fields**: `funnelQueue Pending` + `workQueue Accepted` →
  `queue workq.Queue`; add `pool *worker.Pool[*funnelExEnv]`; KEEP `governor`,
  `inFlight`, `job`; DELETE `state cpstate.FunnelPoolState`, `unmetDemandFn`.
- **`NewFunnelPool`**: `cp.pool = worker.NewPool(&cp.queue, cp.newWorkerState)`;
  `cp.queue.Init(cp.pool.DemandFunc())`; `governor.Init()`. `newWorkerState()
  (*funnelExEnv, ctx, cancel)` mirrors `goroutine()` lines 138-151:
  `WithCancel(j.ctx)`, `ensureCtxMeta` with `executionEnvironment=E`,
  `parent=nil` (fresh permit-root). DELETE `goroutine()`/`spawnNewGoroutine()`.
- **Pool lifecycle**: the FunnelPool must `cp.pool.Acquire()` (job start) /
  `Release()` + `Wait()` (job teardown) — find where the legacy job waited on
  `cp.job.wg` for funnel goroutines and route to `pool.Wait()`.

**THE HARD PART — end-of-work flush relocation.** Deadline-driven flushes already
work through the generic `Worker` (`selectWork`'s `deadlineCh` → `driveOne`
re-drains scheduled → runs the flush). What's lost is the JOB-END force-flush of
not-yet-due instances: legacy wove it into `cpWorker` (`nextJobFlushCh` →
`flushAll` → `DrainAllScheduled` + `forceFlush` each). The generic `Worker` has no
such case (correctly — it's funnel-specific). Relocate via **a dedicated per-
FunnelPool flusher goroutine** that watches `cp.job.state.FlushChan()` and runs
`flushAll` with its OWN `funnelExEnv`/sender (coordinates with workers via the
existing `ClaimForFlush` arbitration). Preferred over a `SetFlushListener`
callback, which would run flush (may emit downstream + need backpressure) on the
arbitrary `noMoreWork()` goroutine — deadlock-risky. UNSETTLED until validated;
this is the riskiest part of the cutover. Also re-confirm whether `cp.inFlight` is
still needed (legacy used it only for the worker end-of-work confirm, which
worker.Pool's idle-exit replaces; job Done is gated by jobstate barrier refs, not
the pool) — likely removable, verify.

**Options fallout.** The design drops operational dials, but
`WithMaxConcurrency`/`WithIdleTimeout`/`WithIdleJitter` are used by tests:
`maxholdtime_test.go:36` `WithMaxConcurrency(1)` relies on SERIAL execution;
`funnel_legacy_bench_test.go:602`; `example_funnel_test.go:54`
`WithIdleTimeout(-1)`. Keeping `SetOptions` as a no-op compiles them but BREAKS
maxholdtime's serialization assumption → it must migrate to a limiter
(`WithLimits`, the design's real concurrency lever). Budget this as part of cp-5.

**Validate**: funnel suite + `-race` + sim (`sim-trace-debugging` skill on hang).

### Flusher prototype — VALIDATED design (2026-06-16)

The riskiest piece, settled against the lifecycle code before the mechanical
cutover. The end-of-work flush moves OFF the worker loop to a dedicated per-pool
flusher goroutine. Two risks checked:

- **Sender lifetime — NO hazard.** The instance does NOT capture a sender at
  allocation: `funnelInstance.allocate` takes a sender only for a panic-path
  `emitErr`, and `emitErr` IGNORES it (`_ = sender`, funnelop.go:530) — errors go
  through `op.errSink`; result emission is the user body calling `Submit` with the
  **ctx**, which resolves the sender from the executing goroutine's exEnv at flush
  time. So the flusher uses its OWN `funnelExEnv`/ctx (via `newWorkerState`) and
  `forceFlush(flusherCtx, flusherSender)` is safe even though the allocating
  worker has long exited.
- **Lifecycle (jobstate/state.go).** `Closed→Flushing` when `inFlightWork→0`
  (`noMoreWork` rotates `nextFlushChan` + closes the old → `FlushChan` edge);
  `Flushing→Done` when `totalReferences→0`. A funnel instance holds ONE reference
  from `allocate` until `flush` (`IncrementReference`/`DecrementReference`), and
  references — unlike work — do NOT gate `Closed→Flushing` (so an idle live
  instance lets the job flush). `flushAll` = `cp.queue.DrainAllScheduled` +
  `forceFlush` each; idempotent (`ClaimForFlush` vs the deadline-driven `Execute`,
  and `flush` no-ops when `accumulator==nil`).

Prototype (drop into FunnelPool):
```
flusher goroutine (started in NewFunnelPool, BEFORE any work — reads the first
FlushChan on the constructing goroutine so no first-cycle miss):
  state, ctx, cancel := cp.newWorkerState(); defer cancel(); defer state.Release()
  done := cp.job.state.Done()
  for {
    flushCh := cp.job.state.FlushChan()      // re-subscribe each cycle (rotated)
    select {
    case <-flushCh: cp.flushAll(ctx, state.Sender())
    case <-done:    return
    }
  }
flushAll(ctx, sender): for _, w := range cp.queue.DrainAllScheduled(nil) {
    w.(scheduledFlusher).forceFlush(ctx, sender) }
```
Deadline-driven flushes still run through the generic Worker (`selectWork`'s
`deadlineCh`); the flusher only force-flushes not-yet-due instances at job-end.
**One edge to confirm in validation:** a self-recursive funnel (flush emits back
into the SAME pool, re-populating its scheduled queue across multiple Flushing
cycles) — the FlushChan re-read could pass a cycle. Within one pool this is rare
(flush emits downstream, not to self); cross-pool work rides the shared job
FlushChan so other pools' edges also wake this flusher. If the sim hangs on a
recursive case, add a buffered-signal backstop. (Cannot use `SetFlushListener` —
that's the user's `WithFlushListener` slot.)

### CORRECTION (2026-06-16, PN) — the "funnel cutover" was mis-scoped

The whole per-job `FunnelPool`-owns-a-`worker.Pool` framing below is WRONG and is
retracted. Per the converged design (lines 19-34, 54-72) and `pool.go`'s seam:
- **ONE global `defaultPool`** held at package level. It OWNS the wg.
  `Acquire`/`Release` = one ref per in-flight Wave; `Wait` stops workers when
  refs→0 iff a Wait is outstanding. **`psg.Wait() = defaultPool.Wait()`** is the
  public surface AND the "stop remaining goroutines once no wave references the
  pool" logic. `worker.Pool` (cp-3) ALREADY implements exactly this — so the
  `j.wg` coordinator goroutine I posited is unnecessary and contradicts the model.
- **The unified E is CONTEXT-FREE** — Sender + Receiver + group stack, no
  `cp *FunnelPool` / job backref. (My `funnelExEnv{cp}` was wrong.) Per-job/per-
  wave context rides the WORK item; the worker runs each body under the work's
  borrowed wave-exec ctx (wave-5b), stamping its E in.
- **ONE shared task/funnel `Queue`**, tagged PER-WAVE. End-of-work flush + drain
  + cancellation + governor are **per-wave** ("Per-wave (not in the pool)"), NOT
  per-pool. So the flusher is per-wave (scoped to that wave's scheduled instances
  + its in-flight drain), not the per-FunnelPool goroutine I drafted.

**Consequence:** the funnel engine does NOT cut over in isolation. It folds onto
the global pool + shared queue + the per-wave Wave (build-order steps 3-4:
`waveCtx`, per-wave in-flight counter, governor, `Acquire`/`Release` the global
pool; then wire `submit` + per-wave ctx borrowing). The validated flusher LOGIC
(idempotent `DrainAllScheduled`+`forceFlush`, sender-via-ctx, FlushChan/Done loop)
still holds — it just lives per-wave and the join is `psg.Wait`/Acquire-Release,
not a per-pool coordinator.

**Next (corrected):** wire the global substrate.
- `worker.Pool` now **embeds the shared `workq.Queue` UNEXPORTED** (alias
  `sharedQueue`): the global pool and the queue are 1:1, so `NewPool(factory)`
  owns + Inits the queue internally (`p.Init(p.trySpawnWorker)`), only the
  producer surface (`Post`/scheduled/`ExecuteNowOrQueue`) promotes, and the
  consuming side stays unexported in workq — done, green. (Dropped the external
  `*Queue` param + `DemandFunc`.)
- **DONE (green, dormant):** `pool.go` un-excluded — `var defaultPool =
  worker.NewPool(newWorkerState)` + `func Wait() { defaultPool.Wait() }`. The
  **context-free unified E** `workerExEnv` (just `integrationExEnv` + no-op
  Lock/Unlock + `ExecuteNowOrQueue` → `defaultPool` promoted; `var _
  executionEnvironment = (*workerExEnv)(nil)`) — NO job/cp backref. Producers will
  `defaultPool.Post`. Dormant (nothing Posts yet → no demand → no workers), so
  `newWorkerState`'s placeholder ctx is unexercised; `taskExEnv`/`cpWorker` still
  live for the legacy paths until cut over.
- **REMAINING:** the wave-5b ctx model (converged below), then `Wave` + wire
  `submit`; funnel + task producers collapse onto `defaultPool.Post`, then delete
  `taskExEnv`/`cpWorker`/cpstate and the per-job pools.

### wave-5b ctx model — CONVERGED (PN, 2026-06-16)

**Three contexts, by ancestry `poolCtx → waveCtx → execCtx`:**
- **`poolCtx`** — the global pool's context. Cancels ONLY when `Wait()` has been
  called AND refs hit zero (definitive teardown; distinct from idle-scale-to-zero).
  Replaces `worker.Pool`'s `stop chan` (cancel under the same `refs==0 && waiting`
  condition); the pool EXPOSES it so waves derive from it.
- **`waveCtx = WithCancel(poolCtx)`** (a subwave = `WithCancel(parentWave.waveCtx)`
  for wave-tree cancel ancestry). Per-wave cancel + global teardown both via
  stdlib ancestry, no custom hook.
- **`execCtx = WithCancel(waveCtx)`**, pooled per-wave (nbcq, prior art
  `funnelInstanceQueue`), reused (reuse-not-cancel). **The ctxMeta lives on the
  execCtx — never on the worker.** Distinct per-shell done channels avoid the
  shared-`waveCtx.Done()` park-lock contention.

**Worker:** holds `E` (NOT a ctxMeta) + a `poolCtx`-derived context used ONLY for
the idle-side cancellation case in `selectWork`. Bodies never run under it. The
fungible worker hands its `E` to the psg `work.Execute` (which knows the wave and
does the borrow) via a generic `workq` channel — a ctx value under a workq key or
a field on `Execution`, NOT ctxMeta. `work.Execute` borrows an execCtx shell from
its wave, stamps `E` (+ group/heldRequest) into `shell.meta` for the borrow, runs
the body under `shell.execCtx`, returns the shell. Borrow/return drives the
per-wave in-flight counter.

**E placement = A (per-worker), settled.** `Sender`/`Receiver` stay bound to the
worker goroutine — the fungible buffering substrate whose capacity scales with
worker count (= system parallelism). Stamped into the borrowed ctxMeta as a
pointer; shells stay lightweight (no rdvq state). Rejected B (E embedded
per-shell/per-wave): heavier shells, more Sender/Receiver instances + reset churn,
buffering coupled to wave structure; its only win (wave-scoped buffer cleanup) is
moot — see below.

**Cross-wave handoff is decoupled, by rdvq design (PN).** A full outbox is owned
by the DOWNSTREAM queue's `fullOutboxes` (outbox refcount), not by the `Sender`.
So abandoning a full outbox is safe: it's already queued, and the eventual
dequeuer empties + frees it. Therefore a **send completes when the item is
handed-off-or-buffered, not when consumed** → the **per-wave in-flight decrements
at send-completion**, so the sending wave drains and its `CancelAndWait` returns
WITHOUT waiting for the downstream receiver. (This is why per-worker Senders can
be abandoned freely, and why stale cross-wave delivery is a non-issue — if a
cancelled wave's buffered item is later dequeued, its borrowed shell is already
cancelled → the body aborts, accounted by borrow/return + `Free`.)

**`worker.Pool` deltas implied:** `stop chan` → `poolCtx`/cancel; expose
`poolCtx`; `newWorkerState` factory simplifies to building just `E` (the pool owns
the worker's idle ctx, captured from `poolCtx` at spawn).

### rdvq Sender redesign — destination-owned outbox pool (PN, 2026-06-16)

Came out of the E/Sender thread: in the global fungible pool, the per-goroutine
`Sender.outboxMap` accumulates stale outboxes for many wave-scoped destinations
(long-lived worker, short-lived destinations). Fix by moving outbox ownership to
the destination (the `Queue`), eliminating the `Sender` map AND the per-outbox
refcount.

**Mechanism — three per-Queue outbox queues** (replacing `Sender`-cached outboxes
+ refcount): `emptyOutboxes`, `maybeFullOutboxes`, `fullOutboxes`. **Invariant:**
every outbox is on exactly one of empty/maybeFull when not checked out by a
`Push`; `full` is an independent membership ("holds a value, awaiting a
receiver"). `Push` borrows: prefer `empty`, fall back to `maybeFull` (which may
still be full → the cap-1 fill BLOCKS = pacing), else allocate. After a buffered
fill: publish to `full` + `maybeFull`. A receiver drains from `full` and
**leaves the outbox on `maybeFull`** (now drained) — it never touches the borrow
side, so the two sides share state only through the cap-1 channel (race-free).
Allocation happens only when both empty+maybeFull are empty ⇒ every outbox is
checked out ⇒ **pool capped at #concurrent borrowers (≤ #goroutines)**.

**What this IS (PN):** rdvq becomes a **zero-contention buffered channel whose
buffer size is 1:1 with the peak concurrency it actually experiences** —
N distinct cap-1 channels reached lock-free (nbcq), self-sizing to exactly the
concurrency seen (no dial), shrinking back via idle outboxes on `maybeFull`.
Minimal-WIP made structural: WIP ≡ actual peak parallelism.

**API simplification:** `PushBack`/`TryPushBack`/`PushBackFunc` drop the `Sender`
param; per-outbox `refcount` + `listeners` collapse (the listeners → ONE
queue-level "an outbox freed" wakeup that postponing producers subscribe to,
fired by the receiver on drain); the `Sender` type largely dissolves. `selectFn`
variants stay. INBOXES unchanged — an inbox is pure rendezvous (no buffered value
outliving the call), and the per-`Receiver` inbox map has no staleness (a worker
receives from ~one queue), so the asymmetry is principled.

**PROVEN (prototype, `outboxpool_proto_test.go`, `-race` ×3):** the empty/
maybeFull/full borrow pool moved 160k values through **exactly 8 outboxes for 8
pushers** (3 drainers, so the full/pacing path was hammered), every value received
exactly once, zero races — bound tight, pacing holds.

**Sequencing:** land this rdvq change BEFORE wiring `E` into Wave/submit (it
dissolves the `Sender` that `E` would otherwise be built around). Ripple: rip
`Sender` out of `PushBack`/`ListenersFor` and every `*.Sender()` emit site (workq
`Post` + the psg producers) — that's the big integration the proto defers.

**"Cheat" fast-path — MEASURED (hardened), REJECTED (2026-06-16).** Considered
promoting drained outboxes onto a separate `emptyOutboxes` queue (prioritized
borrow) to avoid a borrow blocking on a still-full `maybeFull` front while an
empty sits behind it. It breaks the single-borrow-queue invariant (an outbox is
then on `empty` + a stale `maybeFull` entry), so it needs a per-outbox claim CAS +
two membership flags + a looser bound. Measurement went through several stages, each fixing a flaw PN caught (the
journey IS the lesson — don't trust a measurement until metric, workload, and
topology are all realistic):
- *throughput* (firehose, zero work): inconclusive, block count noise-dominated.
- *single max block*: looked like cheat cut the tail 3–4× — MISLEADING (one noisy
  sample).
- *too-short work / wrong ratio* (busySpin 200ns/200µs, pushers<<drainers): cheat
  won p50 but lost the tail — but the work was a single atomic-op's worth, and
  many-drainers absorbed everything.
- *FINAL — realistic* (`outboxpool_compare_test.go`): drain handler =
  heavy-tailed **blocking I/O** (`time.Sleep`, Pareto p50 1.7ms / p99 37ms /
  p99.9 213ms / max 1s), across the topologies that matter (P==D fungible-
  balanced; P>>D fan-in overload), push-latency percentiles ×4 runs each:
  - **P==D balanced** (cheat's best case — buffer oscillates, empties exist):
    cheat wins **p50** (~5.5 vs 7.7µs) but LOSES the tail every run — p99 ~4.5 vs
    ~3.6ms, p99.9 ~6.6 vs ~5.6ms.
  - **P>>D overload** (sustained backlog → no empties → cheat ≡ clean): IDENTICAL
    — p50 ~28ms, p99 ~72ms, p99.9 ~75–117ms both.
**Principled reason** the cheat can't win the tail: the tail is **consumer-
driven** (a slow handler backs up the pool; BOTH eat that equally); the cheat's
only lever is *which* outbox a borrow grabs — it can't make consumers faster — and
its machinery (claim CAS + skip-stale loops) adds variance that lands IN the tail.
Also notable: the structure **absorbs** the I/O tail — with many fungible
consumers + FIFO drain, a producer waits for the NEXT free consumer, not a
specific slow one, so a 1s handler tail shows up as a ~ms push tail.
**SHIP CLEAN** — wins or ties the tail (the priority metric) in every realistic
regime, simpler (no claim/flags), tight self-sizing bound. The cheat's only win
is a µs-scale median in one regime — not the priority. (Lesson, from PN: harden
the measurement — realistic metric + heavy-tailed blocking-I/O + right P:D ratios
reversed the conclusion more than once. See [[feedback_bench_methodology]].)

**Reclamation — race-safe, VALIDATED (2026-06-16).** The clean two-queue core
never shrinks: a concurrency spike pins peak-concurrency outboxes on `outboxes`
forever, and the count is **goroutine-bound** (a goroutine parked on a full
outbox costs no core, so it's NOT ≤ GOMAXPROCS — it's however many pile up on a
slow destination), and the shared task/funnel queue is long-lived. Crucially the
per-`Sender` design we're replacing scaled down FOR FREE (`Sender.Release` on
goroutine exit), so the destination-owned pool would be a memory REGRESSION on
long-lived destinations without reclamation — i.e. reclamation is load-bearing,
not a refinement. **Protocol:** fold a generation counter + the reclaimable bit
into ONE atomic word per outbox (`state = gen<<1 | reclaimable`). Fill bumps gen
+ clears reclaimable; drain captures gen `g`, receives, then `CAS (g,0)→(g,1)` —
which sticks ONLY if no refill bumped gen, so a refilled (full) outbox can never
be left reclaimable (kills the use-after-reclaim, the whole hazard). Borrow
prefers a full outbox (block-fill = pace) and discards reclaimable empties to a
**`sync.Pool`** — whose own GC-clearing IS the scale-to-zero (cheap reuse hot,
release cold). Validated `outboxpool_reclaim_proto_test.go`: `-race` clean, every
value received exactly once (no use-after-reclaim), scales to `circulating=1` at
idle; the `sync.Pool` churn (~1.3MB/run of small structs) is negligible — an early
"too aggressive" worry was a mis-read of a big-looking allocation count
(`sync.Pool` is fast).

**Both `Sender` AND `Receiver` go (PN).** `Sender` (per-goroutine outbox map) →
the destination-owned outbox pool above (functional: fixes staleness, adds
scale-down). `Receiver` (per-goroutine inbox map) → symmetric destination-owned
inbox borrow, leaving rdvq a **handle-free channel API** (`Push(v)`/`Pop()`, no
sender/receiver params). The Receiver case is WEAKER (no staleness — a worker
receives from ~one queue; the inbox is pure rendezvous, no buffering/reclamation),
so it's cleanliness not necessity — when implementing, confirm the per-receive
inbox borrow stays cheap (hot path) and the LIFO waiting-inbox order (worker
scale-down) is preserved. End state: the unified `E` holds neither.

**FINAL outbox-pool design (settled, ready to integrate):** two queues —
`outboxes` (everything; borrow source) + `fullOutboxes` (has-a-value; drain
source); no per-sender map, no refcount, no per-outbox listeners. Borrow pops
`outboxes`, prefers full (block-fill = pacing = minimal-WIP), reclaims empties to
`sync.Pool`. Fill sends, bumps gen + clears reclaimable, publishes to both. Drain
pops `fullOutboxes`, receives, gen-guarded CAS-marks reclaimable. One atomic word
per outbox. = a zero-contention buffered channel sized to peak concurrency, that
backpressures by blocking (not buffer inflation) and scales back down.

**CHECKPOINT 1 — landed (the non-blocking + freed-wakeup reconciliation, PN
chose path A 2026-06-16).** The settled design above is the *blocking* push
(`PushBackFunc`/`PushBack`); the prototype's `push()` always succeeds (allocates
on exhaustion). But `Post` also needs (a) a `TryPushBack` that can *fail* — that
failure is the backpressure signal that triggers LISTEN/postpone — and (b)
`ListenersFor` collapsed to a queue-level "outbox freed" wakeup. Neither is in
the prototype. Reconciliation as implemented (`internal/rdvq/outbox.go`,
`queue.go`):
- **Two borrow helpers, not one.** `borrowToFill` (blocking path) = the
  prototype scan verbatim: prefer full (block-fill = pace), reclaim drained
  extras keeping one fallback, allocate/recycle on exhaustion; always returns an
  outbox. `tryBorrowEmpty` (non-blocking path) pops at most one: reclaimable →
  drop-and-go; pool exhausted → fresh empty (a new slot is not backlog, so
  admit); **front is full → re-push it and refuse** (no slack). So the SAME pool
  serves both: blocking prefers full to pace; non-blocking prefers/requires
  empty to stay non-blocking. This is NOT the rejected "cheat" (that was a
  separate `emptyOutboxes` queue + claim-CAS for the *blocking* tail); here
  prefer-empty is *required* for non-blocking semantics, with no extra
  machinery.
- **`TryPushBack` no longer routes through `PushBackFunc`** (the old code shared
  it via a non-blocking selectFn). They diverge at the borrow, so they're now
  separate methods sharing `publishFilled`.
- **Empty-behind-full false negative is accepted** (documented in
  `tryBorrowEmpty`): a non-blocking borrow that hits a full front refuses even
  if an empty sits behind it. Safe (refuse → postpone, never deadlock),
  consistent with "ship clean / don't optimize empty-behind-full," and
  self-corrects: `Post`'s subscribe-then-retry re-pops the (now re-queued) front
  and finds the empty.
- **Queue-level `outboxFreed Listeners`** replaces per-outbox `listeners`.
  `ListenersFor` returns `&q.outboxFreed`; ALL postponed producers subscribe to
  it; a drain fires `outboxFreed.Notify(nil)` **only when `markReclaimable`
  sticks** (a slot truly opened — a failed CAS = a blocked producer refilled, so
  nothing was freed and no wakeup is owed). Stale listeners (dead executions)
  are skipped lazily by `Listeners.Notify`'s pop-until-true loop, so no
  free()-time `NotifyAll` drain is needed any more.
- **Conservation argument (the livelock risk I flagged):** each freed slot →
  one `outboxFreed` notify → wakes one postponed producer; if a *different*
  fresh `TryPushBack` steals that slot first, the woken producer's retry fails
  and it re-subscribes — but that thief's own eventual drain fires another
  freed, so notifications track freed slots one-for-one. `Post`'s
  subscribe-before-retry closes the lost-wakeup race (same discipline as the old
  per-outbox path). **Validated:** `-race` + 1000-check `TestBySimulation` deep
  sweep clean (114s), so the conservation holds across randomized concurrency.
- **Known benign spurious notify:** on the paced block-fill path the unblocking
  drain marks reclaimable (fires freed) a hair before the blocked producer's
  refill `bumpGen` clears it; harmless churn (woken producer retries, finds the
  slot taken, re-postpones), not a livelock (a value did move = progress).

**CHECKPOINT 2 — landed (destination-owned inbox borrow; gut Receiver AND
Waiter, PN chose unified gut 2026-06-16).** The inbox side is the symmetric
counterpart to the outbox pool, but WEAKER (an inbox is pure rendezvous — no
buffered value, no reclamation hazard), so the goal is cleanliness/handle-free,
not a staleness fix. Scope discovery: `Receiver` embeds `outboxWaiter Waiter`
AND `Waiter` is used standalone everywhere (`meta.Waiter()`, spawn/work/block/
limiter waiters), so gutting `Receiver` forces gutting `Waiter`. One mechanism
handles both, since the data inbox (`Queue` embeds `inboxStackQueue`) and the
wait inbox (`Waiters` embeds `inboxQueueQueue`) are the same
`inboxOnlyQueue.PopFrontFunc`:
- **Inbox pool on `inboxOnlyQueue`** (`inboxPool *omnipool.Pool[inbox[T]]` +
  `borrowInbox`/`reclaimInbox`); a pooled inbox keeps its (empty) channel so
  reuse re-allocates nothing.
- **`PopFrontFunc` gains a `clean bool` return:** true when the inbox ends
  drained AND out of the empty-inboxes collection (received directly, or an
  orphan was drained) ⇒ caller may reclaim/reuse; false when abandoned (marker
  left, inbox still in the collection) ⇒ caller must NOT reclaim. The method
  body is otherwise unchanged (the delicate marker/abandonment protocol is
  untouched).
- **Caller owns the borrow** (NOT pushed inside PopFrontFunc) — this is what
  preserves cross-iteration reuse and the reuse-without-requeue marker-drain
  path that prevents marker pile-up in the retry loop. `Queue.PopFrontFunc`
  borrows the data inbox **lazily inside the WaitFunc selectFn** (so a
  confirmFn-grab on the fast path borrows nothing), reuses it across the retry
  loop, and a `defer` reclaims iff the last `clean` was true. `Waiters.WaitFunc`
  borrows a wait-inbox per call, reclaims iff clean.
- **Why the abandonment leak is fine (= today's behavior):** an abandoned inbox
  is left in the LIFO stack / FIFO with its zero-value marker; a later sender
  (`TryPushBack`) or notifier (`Notify`) drains the marker, removing it, and it
  is GC'd. Before, the per-goroutine map held it for reuse; now it is simply not
  pooled. At most one abandoned inbox per PopFront/WaitFunc call (cross-iteration
  reuse keeps it to one), same as before. FIFO cleanup is actually favorable
  (abandoned wait-inboxes sit at the front, drained first by the next `Notify`).
- **Ordering preserved by construction:** the pool changes which inbox *struct*
  is reused, not the `inboxStack`(LIFO)/`inboxQueue`(FIFO) push/pop order or
  which receiver a sender hands off to — so delivery order and worker scale-down
  are unchanged. (Confirms the `Example_observable` flake is the real clock, not
  this change.)
- **Hot path stays cheap:** a looping worker that receives via direct handoff
  exits clean every time → reclaim + reborrow cycles the SAME inbox through the
  pool (zero steady-state alloc), replacing the old per-goroutine map lookup with
  a comparable/cheaper pool op.
- **Validated:** rdvq short+`-race`+full-stress (141s `-race`);
  `TestBySimulation` short+full+`-race`+1000-check sweep (92s) all green; module
  vets clean. `Receiver`/`Waiter` → `struct{}` + no-op `Release`; params kept
  and `_`-ignored. `inboxOnlyQueue.PopFront` (ctx test helper) still takes an
  `ib` and ignores the new bool.

**POOL BACKING — `omnipool`, not naked `sync.Pool` (PN flagged, corrected).**
Checkpoints 1+2 first transcribed the prototype's naked `sync.Pool`; switched to
`omnipool.Pool` (the codebase's pooling abstraction, used by `nbcq`) for both
the outbox pool (`outboxPool *omnipool.Pool[outbox[T]]`, cached in `Queue`) and
the inbox pool (`inboxPool *omnipool.Pool[inbox[T]]`, cached in `inboxOnlyQueue`),
set via `omnipool.For[…]()` in `Init` (nbcq's pattern). `outbox`/`inbox` now
implement `omnipool.Initer` (`Init` allocates the cap-1 channel) + `Resetter`
(`Reset` clears state/`wasEmptied` but KEEPS the drained channel — also stops
omnipool's default whole-struct zeroing from nil-ing it). Why omnipool: the
hot-path cost worry was wrong — `Pool[T].Get` reflects once in `For[T]()`
(cached `hasInit`/`hasReset` bools), so per-call `Get`/`Put` ≈ naked
`sync.Pool`; consistency wins; and global-by-type sharing is safe (a reclaimed
outbox/inbox is element-type-generic, not Queue-specific) and improves reuse.
The "destination-owned" principle still holds via the per-`Queue`
`outboxes`/`fullOutboxes` (the WIP-bounding state); only the spare-struct
free-list is shared. Re-validated to the same gates (rdvq 143s `-race`; 1000-
check sim `-race`).

--- superseded framing below (kept for the verbatim seams only) ---

**Next:** the mechanical cutover (one focused push), in order:
(1) `cpworker.go` → replace `cpWorker` with `funnelExEnv` (+ keep `scheduledFlusher`,
`executeFunnel`= `bc.Funnel(ctx, Sender())`); (2) `funnelpool.go` → FunnelPool
fields (`queue`+`pool`, drop `state`/`funnelQueue`/`workQueue`/`unmetDemandFn`),
`NewFunnelPool` (+ `newWorkerState`, start flusher), `flushAll`, lifecycle
(Acquire/Release/Wait), rewrite `funnelPostWork.Execute`→`Post`, delete
`goroutine()`/`spawnNewGoroutine`; (3) `funnelop.go` → `executeInner` cast →
`*funnelExEnv`, `funnel()` `workQueue`→`queue`; (4) delete cpstate usage; (5)
options: `SetOptions` no-op + migrate `maxholdtime_test` to a limiter; (6) build-
fix + funnel suite + `-race` + sim.

## Residual reclaim busy-spin — FIXED (2026-06-14)

The last known limiter livelock is resolved. Under max contention (`Permits=1`,
`Inherit` prob 1), `reclaimRequest` could busy-spin on a permit it had already
reclaimed because `addWorkWhileMaybeBlocking` returned a **stale `psResult`**
(`outbox`) on iterations where `skimSelect` was short-circuited, keeping
`PopFrontFunc`'s loop from reaching its empty-exit. Fix: declare `psResult`
**inside** the per-iteration selectFn closure (one-line scoping change in
`job.go`). Trace-proven (selectFn returned `outbox` 117k× while `skimSelect`
entered 0×); validated rt6 250 / rt7 264+ iters with 0 hangs (baseline hung at
iter 44, 150). Full write-up: `REVIEW_FINDINGS.md` Finding 13. This is the
residual that Finding 10's skim-gather ban did not reach.

## Cancellation/teardown coverage — sim expansion + two bugs found (2026-06-14, IN PROGRESS)

### Why
The sim only ever drove the *success* path: injected handler errors were
immediately retried, and it asserted only *peak* concurrency (`observed ≤
permits`), never *quiescent* balance. The entire error/cancellation/teardown
column was both un-triggered and un-detected. Goal: exercise the **class** of
rarely-hit error/cancel/teardown paths, not one instance.

### Principles (PN)
- **Observable-only, no white-box probes.** Conservation violations must surface
  as the two black-box signals that already exist: over-release → `observed >
  permits`; under-release/leak → hang (timeout). A leaked permit is only
  observable if the limiter keeps being demanded after the leak, so disruptions
  must land on ops whose limiter is **shared with surviving work** (cross-subjob
  `Inherit`).
- **No runtime entropy.** Every decision is baked into the Plan at generation
  time (rapid draws); the only live nondeterminism is goroutine scheduling. The
  existing runtime already honors this (`drawDuration`→Med, `rollProb`→`p>=1`,
  `shouldReturnError`→baked 0/1) — the "replace with a real RNG" comments are
  exactly what we are NOT doing.
- **Disruption matrix:** `{cancellation, submitted-error-propagated,
  internally-generated-error} × {any nesting level} × {retried | abandoned}`.
  Today only `{internal error} × {always retried}` is covered.

### Harness landed (this commit)
- `run.go`: centralized disruption classification (`classify`/`disposition`):
  expected handler errors → retry; cancellation / `ErrJobDone` → abandon;
  else → fail. Behavior-preserving until a disruption is injected.
- Plan-baked, **structural** cancellation: `SubjobConfig.CancelProb` →
  `Plan.CancelTriggerRunnerID`; the designated launcher's body calls
  `controller.cancel` when it runs (holding its permit, siblings likely blocked
  acquiring). `Plan.MinSkimmerInvocations` drops to 0 for cancelled subplans.
- To reproduce the bugs below: set `TaskLimiter/FunnelLimiter.Permits={1}`,
  `…Inherit={Probability:1}`, `Subjob.CancelProb=1` in `simulation_test.go`
  (the TEMP block, reverted for the committed state).

### Two bugs found (framework fix NOT yet committed — see below)
1. **Over-admission on subwave cancel.** `reclaimRequest` keyed its abandon on
   the **help-domain** ctx (the subwave). When only the subwave is cancelled and
   the parent is live, the reclaim abandoned (`held=false`, confirmed by probe:
   18 abandons coincident with `observed 2 > permits 1`), so the parent resumed
   its body **UNPERMITTED**. (limiter.go ctx.Err branch.)
2. **Teardown deadlock.** `Pool.CancelAndWait` was `Cancel(); wg.Wait()` with no
   drain. A task blocked posting into a skim queue (hold-through, holding a
   permit) never unwinds once its consumer is gone → `wg.Wait` blocks forever.
   Dump: 3 goroutines in `reclaimRequest`, `CancelAndWait` waiting on them.

### Root cause (after a long design thread — discard-drain was a DETOUR)
Both bugs are symptoms of one thing: **cancelling a subwave does not propagate
cancellation to the goroutines actually running that wave's work.** We chased a
"discard-drain" (consume the pipes without running user code) to unblock
producers, got it partly working (over-admission gone, ~2/10 hangs remained),
then a dump showed the real wall: with **a separate Pool per wave** (today every
`NewWave` with no `WithPool` does `New(parent)`, `ownsPool=true`), a teardown
drive on pool A *cannot reach* a permit-holding producer blocked one pool over.
That is a cross-pool coordination gap, not a draining deficiency.

**Verified key fact:** the hold-through post is `rdvq.BasicPushSelect`, which
`select`s on `ctx.Done()` — so a parked producer **aborts and releases its
permit the moment its ctx is cancelled**. *Draining is not required at all.* The
deadlock is simply "cancel never reached the producer." So the whole
discard-drain / tagging / queue-of-queues branch is unnecessary.

### Converged design — per-wave cancellation propagation (this is wave-5b)
The real need is two primitives, NOT a drain:
1. **Per-wave cancellation that reaches every goroutine running the wave's
   work** (so their `BasicPushSelect` posts abort → permits released).
2. **A per-wave in-flight counter** to detect when the subtree has quiesced
   (`CancelAndWait` waits on it; the wave's drain returns `ErrJobDone`).

Mechanism (decided 2026-06-14, PN — defer the fancy version):
- The Wave holds `waveCtx = WithCancel(poolCtx)` and an **nbcq pool of
  per-execution contexts**, each `WithCancel(waveCtx)` **with its own mutable
  `ctxMeta`** (`WithValue(WithCancel(waveCtx), …)` derived once, re-stamped per
  borrow). Workers **must run user functions under the borrowed wave-execution
  ctx, not their own goroutine ctx** — that is what makes per-wave cancel reach
  user code.
- **Distinct done channels** (one per pooled ctx) avoid the shared-channel
  park-lock contention that a single `waveCtx.Done()` would reintroduce;
  **nbcq borrow/return** is the lock-free reuse and amortizes the one-time
  `WithCancel(waveCtx)` children-map registration. Prior art to mirror:
  `funnelInstanceQueue` (`funnelop.go:316`) is the same nbcq reuse-cache pattern
  (`TryPopFront`/`PushBack`, "spent shells linger" drain at :371); the trim TODO
  applies to both.
- Cancel fans out via plain stdlib ancestry (`poolCtx`→`waveCtx`→exec ctx);
  no custom hook needed yet.
- The **per-wave in-flight counter rides the same borrow/return** — one
  mechanism gives cancellation-scoping AND completion-detection.
- **Reuse-not-cancel invariant:** return ≠ cancel; only wave-cancel closes the
  pooled ctxs, after which the wave is done so they are never reused closed.

This is **wave-5b** (per-wave tagged cancel/drain), the consolidation's second
half — note the Wave/Pool API decoupling (`WithPool`) already exists; what's
missing is per-wave cancellation that addresses one wave's work. The reclaim
"never abandon / no unpermitted resume" correctness folds in here (it was
coupled to a working per-wave teardown, so it could not land standalone).

Deferred (revisit with **Flows**): a joined-context adapter and a framework-
native, alloc-free / mutex-free `AfterFunc`-equivalent hook (modeled on how
`ctxMeta` is a preallocated reused value) — only needed if Flows must merge two
*genuinely independent* (non-ancestor) cancellation scopes. See TODO.md.

### Status / next
Harness + design committed. All framework-fix attempts (discard-drain,
always-reclaim, cancel+close, drive, skim discard gate) are **reverted** — they
were a detour. **Next: implement wave-5b** — per-wave `waveCtx` + nbcq pool of
wave-execution contexts (+ in-flight counter), workers execute user work under
the borrowed wave ctx. The committed sim harness (`Subjob.CancelProb` + shared
limiters) is the validator: it currently reproduces the two bugs and must go
green.

## Limiter suspend/resume — DESIGN SETTLED + REVIEWED, ready to implement (2026-06-11)

**The design is finalized and written up in `docs/limiter-suspend-resume.md` —
that note is the source of truth — and has passed a full design review:
`REVIEW_FINDINGS.md` (all 9 findings resolved, 2026-06-10/11) is the review
record, with every resolution folded back into the note.** This section is the
working summary; the older "ROOT-CAUSED" section below is accurate *background*
on the bug, but its fix sketch (a `heldPermit` carried as a ctx value) is
**superseded** by the handle/scheduler/resource design in the note.

**The bug (confirmed):** intermittent `TestBySimulation -race` busy-spin
livelock — a concurrency permit held across a blocking skim (a body driving a
subwave via `CloseAndSkimAll`) starves a sibling unit of the same op that needs
the same permit. Pre-existing; orthogonal to the funnel work. Repro: **non-short**
`-race` (short mode suppresses it — it needs deep paths/subwaves). Signature: ~20
goroutines, **zero** mutex/sema waiters, spinning in `ExecuteOrWait`/`sim.Run`.
Baseline measured this tree: **3 hangs / 30 iterations** (12 checks each, 360
non-short `-race` checks). Build the loop with
`go test -c -race -o /tmp/psg.race.test .` then run
`/tmp/psg.race.test -test.run '^TestBySimulation$' -rapid.checks=N -test.timeout=…`
in a loop, treating a `-timeout` panic as a hang.

**The fix (design):** a concurrency permit gates *active computation*, not
*blocked-waiting*. **Two-class park rule:** suspend at delegation-shaped waits
(gathers + block-and-help — both have deadlock witnesses in the note); hold
through pure capacity waits (blocking posts — the held permit IS backpressure
propagation; safe by "a permit wait never occupies bounded queue capacity").
Mechanism:
- A limiter is driven by the framework through a **request handle** (state
  machine `PENDING → HELD ⇄ SUSPENDED → DONE` plus `HELD ⇄ POSTPONED` for
  granted-then-yielded pre-body grants) with `tryAcquire`/`postpone`/`suspend`/
  `tryResume`/idempotent `release` + phase-routed `notifier` (never nil).
  Illegal transitions **panic**; capacity-returning transitions (suspend,
  postpone, HELD-release) notify, state-discarding ones don't.
- `limiterImpl` is implemented by a **scheduler** (sealed, few): a *direct*
  scheduler over one resource now; *ordered*/*prioritized* later. The scheduler
  owns all lifecycle/routing/discipline.
- A **resource** (open extension point) is pure accounting: `demand(applicant)` /
  `tryAcquire(amount)` / `release(amount)` / `suspendable()` /
  `setCapacityChangedFn(func(delta int))` (bind-time growth hook; channel
  rejected — unconsumed-ping hole). The semaphore is a ~10-line resource.
- `ExecuteOrWait` splits into **routing + a shared `blockingAcquire`**;
  **reclaim is help-shaped** (plain-wait reclaim deadlocks — witness in the
  note); help domain = the pool whose skim context the goroutine occupies.
- Externally-serialized permit scoping via `ctxMeta.parent` + **fresh-root
  worker contexts** (explicitly severed at worker-context creation) + always-on
  `assert(meta.parent == nil)` at handle-stamp time. **Skimmers stay
  limiter-free** — load-bearing for the hold-through class (skim handlers are
  the drain; drain must stay permit-free).

**User-facing API delta (now):** `NewSemaphore(nil, n)` (scheduler is the
mandatory first arg, nil = self-scheduled; only nil supported yet). The
Semaphore now caps **active** concurrency, not in-flight — needs a CHANGELOG
entry + a doc update on `Semaphore`. Everything else (scheduler/resource
constructors, exported `Resource`, `NewLimiter`) is additive/deferred.

**Implementation plan — execution order #1, #2, #3, #5, #4, #6** (each
checkpoint independently green; the hang baseline persists until #4 lands —
expected):
- **#1 — DONE (`c96db84`).** Limiter core: `resource` iface (with
  `setCapacityChangedFn` hook) + semaphore resource + direct scheduler + the
  handle (full state machine incl. POSTPONED; illegal transitions panic).
  Unit tests pin: notify discipline (wake exactly once; discards silent);
  no double-credit; legality panics; suspend-frees-slot; growth wakes
  postponed listeners. **Deviations:** (a) the `ExecuteOrWait`
  routing/`blockingAcquire` split is deferred to #3 — it has no consumer
  until the gates drive handles, and landing it dead would only draw lint;
  (b) a legacy `tryAcquire`/`release`/`notifier` shim remains on
  `limiterImpl` so the existing `limiterScatterWork`/`funnelWork` gates stay
  behavior-identical until #3 deletes it (legacy release now notifies
  unconditionally per the new HELD-release rule; differs from the old
  under-limit check only while draining a `SetMaxConcurrency` shrink —
  benign extra wakes); (c) request handles are not pooled yet — add
  pooling in #3 when the gates own the lifecycle.
- **#2 — DONE (`2397419`).** `ctxMeta` permit scoping: `parent` link (set to
  `sourceMeta` at creation in `ensureCtxMeta`) + `heldRequest` field (stamped
  in #3); task/funnel worker contexts explicitly sever `parent` at creation
  (subjob `j.ctx` carries the dispatching body's foreign-pool meta);
  `currentHeldRequest` walks parent, stops at first stamped handle. Tests pin
  walk semantics + chain topology through real flows (task/subwave/skim/
  funnel). Note: psgwf `Example_clientTimeout` flaked once during
  validation — the known pre-existing timing flake (TODO.md), passes 7/7 on
  re-run; not related.
- **#3 — DONE (`2f27619`).** Gates drive request handles; legacy shim +
  `limiterCompletedFn` deleted. `acquireOrWait` (+ pooled `requestBlocker`
  latch) is the routing/blockingAcquire split, in `limiter.go`. Task path:
  request created at dispatch (applicant = `launcherWork`), gate in
  `limiterScatterWork` (`postpone()` on granted-but-not-started), handle
  travels in `taskWork` (stamp in Execute w/ root assert; release at
  completion via `completedFn`; idempotent backstop + recycle in Free).
  Funnel path: lazy request in `funnelWork` (persists across postponed
  retries), stamp in executeInner, release at body end, backstop in Free.
  Handles pooled (`directRequestPool`; single recycle point = owning work's
  Free). End-to-end test pins subwave-finds-body-handle via parent walk.
  Baseline hang observed 1/3 full-suite runs — expected until #4.
- **#5 — DONE (`bbe4211`).** Sim active-concurrency measurement
  (`limiterTracker` threaded down the Func walk; Subjob step drops/restores
  the body's contribution — under-counts pre-brackets, so green) +
  cross-subjob limiter sharing (`Limiter.InheritFromParent` /
  `LimiterConfig.Inherit`; controller aliases the parent's `psg.Limiter` AND
  tracker; child skips the assertion the owning ancestor performs).
  **Deviation:** the generator's default `Inherit` probability ships as **0**
  and must be **flipped to ~0.25 in the #4 commit** — enabling it before the
  brackets land makes the shared-limiter deadlock witnesses reachable and
  would make even the `-short` suite (pre-commit gate) hang-prone. Machinery
  is fully tested meanwhile (controller-aliasing unit test + hand-built
  two-level inherited-limiter plan at permits=2, contention-free).
- **#4 — DONE.** Suspend-class brackets at the skim methods (`Pool.Skim`,
  `Pool.SkimAll`), the block-and-help wait (`Pool.block`), and the top-level
  dispatch episode (`ctxMeta.ExecuteNowOrQueue`). Help-shaped `reclaim` with
  `suspendForEpisode`; `postpone()` round-trip. Fixed the original livelock AND
  (after design review — REVIEW_FINDINGS Finding 10) a newly-surfaced
  cross-subjob-sharing deadlock whose **root cause was a skim handler driving a
  subwave** (monopolizing the sole serial skim driver). **Resolution: disallow
  blocking gathers from skim handlers** (`ctxMeta.vetNotNestedInSkim`,
  parent-chain walk) — subwork from a skim handler goes to a funnel (preferred,
  it's a map-reduce) or a launched task (both demand-driven, no sole driver).
  With that, the committed brackets + plain-wait reclaim suffice (no
  accept-defer, no help-outward). `Inherit` flipped to 0.25 (cross-subjob
  sharing ON). Validated: full suite ×5 green, `-race` short clean (bar the
  known leakguard test race), non-short `-race` loop **0 hangs/0 fails / 30**
  (was 3/30).
- **Funnel limiter semantics settled (Finding 11):** `WithLimits` on a funnel =
  **accumulate (intake) concurrency**; **Flush is drain-side and limiter-free**
  (generalizes Finding 8: skim handlers + funnel flushes are drain-side →
  limiter-free; launcher tasks + funnel accumulates are intake → limited). No
  `WithFlushLimits` (bound flush-triggered work by flushing to a downstream
  limited launcher/funnel); no separate instance-count limiter (instances
  bounded by accumulate concurrency). Sim measures accumulate concurrency only
  (flush/accumulate overlap is intended pipelining, not over-admission).
- **#6 — DONE for the implemented scope** (`go vet`, `-short`, lint 0,
  non-short `-race` loop 0/30). The leakguard `TestLogLeakStructured` race is
  pre-existing/test-only (documented above).
- **FUTURE (Pool-consolidation track) — generalize the governor (Finding 12).**
  Skimmers are the system's sole serialization point / backpressure source;
  launchers and funnels are elastic (scale goroutines/instances subject to
  limiters). The governor is **not a pool mechanism — it's a per-wave admission
  gate**: while a wave's downstream skim is blocked, that wave admits no new
  work (`Start`/`Submit` gated), *regardless of pool goroutine availability*.
  Gate admission, not spawning: the pool is fungible, so (a) goroutines grown
  for a healthy wave would otherwise run a saturated wave's admitted work, and
  (b) the saturated wave must not launch into them in the first place. The
  existing `launcherScatterWork.Execute`→`job.governor` IS a launch-gate; the
  generalization is just (1) make the flag **per-wave** (Wave-owned, not
  per-pool) and (2) apply the same gate to **funnel intake** (`Submit`), which
  has none today. Spawn (`trySpawnTaskWorker`/`maybeSpawn`) stays pure
  demand-driven — not a backpressure point. Goroutine and instance/memory
  bounding then fall out for free (a gated wave generates no demand), so no
  separate spawn-brake and no instance limiter even for unlimited ops. Per-wave
  matters because the consolidation puts many waves on one pool (pool-wide would
  throttle healthy waves); only *looks* pool-level today because `NewWave` mints
  its own pool (pool ≈ wave). `cp.governor` is currently write-only (funnel
  intake has no launch-gate). Deadlock-free because the skimmer always drains
  (Finding 10). Detail in REVIEW_FINDINGS Finding 12.

**Key invariants to preserve:** externally-serialized handles (never touched
concurrently; HB edges via queues/notifiers — listed in the note); the
two-class park rule (suspend at delegation waits, hold through capacity
waits — the *classification* is the contract, not a site list); stamped
handles only ever HELD/SUSPENDED; skimmers limiter-free; active-concurrency
measurement in the sim. See the note's "Rejected alternatives" for dead-ends
already explored (flat tokens, selectFn inversion, 2PC reservation,
counted-suspend barrier, uniform-suspend-everywhere, plain-wait reclaim,
capacity-growth channel).

## TestBySimulation `-race` hang ROOT-CAUSED: limiter held across a blocking gather (2026-06-07)

**This is the actual gate-blocking bug** — pre-existing, in core backpressure/limiter code,
**orthogonal to the 1c-ii funnel rewrite** that this session started on. The 1c-ii work was
aimed at a theorized funnel `c.mu`/`SetPosition` deadlock that is **not** what fails the gate.

**Symptom:** intermittent `TestBySimulation` hang. Across ~20 captured hangs (baseline + WIP,
race + non-race) there were **zero mutex/sema waiters** — it is a **busy-spin livelock**, not a
blocking deadlock. Baseline (old code) reproduces it, so it is pre-existing.

**Root cause (trace-confirmed):** an op (a Funnel instance, or a Launcher task) holds a `limit=1`
concurrency **limiter permit** while it is **blocked in a gather** (`Skim`/`SkimAll`/
`CloseAndSkimAll` — e.g. a Funnel `Accumulate` that synchronously runs a subjob via
`runSubjob → sim.Run → Wave.CloseAndSkimAll`). Other work **in the same (sub)job** that needs the
same single-permit limiter can never acquire it (the holder is parked, not releasing), so the
backpressure **block-and-help** path (`Pool.block → ExecuteOne → addWorkWhileMaybeBlocking →
rdvq.Waiters.WaitFunc`) spins forever (the rdvq item counter climbed to ~920k in the captured
trace). The sim builds **fresh** limiters per (sub)job (`sim.Run` → new controller + `NewWave` +
`NewSemaphore`), so this is within-one-(sub)job contention, telescoped by recursion — NOT
cross-job reuse. Decisive confirmation: making all limiters unlimited → **0 hangs / 250** (vs
~1/25 with limiters).

**THE FIX (agreed with PN): a limiter suspend/resume protocol.** A permit gates *active
computation*, not *blocked-waiting*. The framework suspends the held permit at **every point
where the holder blocks waiting on other work** — gathers AND the block-and-help wait on a
backpressured Submit — and reacquires (blocking) on return. The **limiter arbitrates** what
suspend/resume mean:
- `limiterImpl` gains `suspend() bool` + `resume(ctx) error` (distinct from `tryAcquire`/`release`
  — you can't reuse acquire/release because for a rate limiter `resume`/reacquire would wrongly
  *re-pay the rate*).
- **Semaphore:** `suspend` = give back the concurrency slot (`inFlight--`, notify); `resume` =
  block until a slot is free (`inFlight++`, wait on the notifier). (Re-takes a *concurrency* slot,
  not a new admission.)
- **Rate limiter:** `suspend`/`resume` = **no-op** (admission paid once at start; nothing held
  during the op; no deadlock to dissolve, nothing to re-pay).
- **Combined:** suspend the concurrency dimension, leave the rate dimension.

**Implementation sketch:**
- A `*heldPermit{ impl limiterImpl; suspended bool; mu }` carried as a **ctx value** (NOT
  `ctxMeta` — it must survive the subjob's `NewWave` so the subjob's `CloseAndSkimAll` sees the
  parent's hold). Set when an op runs its body under a limiter (task dispatch + `funnelWork.Execute`).
- `Skim`/`SkimAll`/`CloseAndSkimAll` (and the block-and-help wait point): on entry, if a
  `heldPermit` is present and not already suspended, `suspend()` it; `defer` a **blocking**
  `resume(ctx)`. The `suspended` flag makes nested gathers no-ops (re-entrant safe).
- `completedFn`/`funnelWork` release at op-completion must consult the `heldPermit` so a
  cancellation mid-gather (suspended) does not double-release.

**Why it telescopes over arbitrarily-nested subjobs:** a subjob is only ever entered *through* a
gather (`CloseAndSkimAll`). Level *k*'s op holds `Lₖ`; to run level *k+1* it must gather → that
suspends `Lₖ` for the whole child → level *k+1* holds/suspends `Lₖ₊₁` at *its* gathers, etc. So no
`Lₖ` is ever held across the wait for level *k+1* at any depth. Any subjob code is either (a) on a
different goroutine with its own holds/gathers, or (b) synchronous on the parent's goroutine —
which is *by definition* inside the parent's gather (permit already suspended). No third case.

**Invariant to preserve:** *the only points where a permit-holder blocks-waiting or synchronously
runs other work on its own goroutine are the gather calls (and block-and-help submit points,
which fold under the same suspend rule).* Today block-and-help drains the **skim** queue (gated by
*different* limiters than the holder's scatter-side permit), and funnels postpone rather than
block-and-help — so a holder never synchronously runs work needing its own permit, and
gather-suspend alone is already sufficient. Suspending at the submit block-and-help point too is
the airtight/uniform rule (and frees the slot during the wait — strictly better concurrency).

**Rejected alternative:** "demote subjob submits to non-top-level so they postpone." Wrong lever
— the cause is the *held permit across a wait*, not the *top-level label*; a subjob's
`controller.Run` is a legitimate driver that should block-and-help; and it tangles with the
dispatch contract (the `ctxMeta.ExecuteNowOrQueue` `AddToListeners` panic-stub requires
`blockFn != nil` when `ShouldBlock()` — nilling `shouldBlock` for subjobs panics; tried, reverted).

**Sim trace-debugging toolkit (used to find this; has stale markers to fix):**
`PSGTRACEINTERNALS= go test -trace=/tmp/trace.out -rapid.checks=1` (loop until a `-timeout` hang
leaves a usable trace), then `internal/sim/analyze-sim-trace.sh` → `fmttrace`
(`internal/cmd/fmttrace`, separate module) → plan/started/completed/`incomplete.txt`;
`internal/cmd/fmttrace/{find,extract}-goroutine*.sh` to drill into a goroutine. **Stale (combiner
rename):** `extract-sim-trace.sh` greps `sim.Run: Test plan:` but the sim now logs the plan as
`%v` (`Plan#N…`); `extract-sim-completed.sh` greps `step M/M: done` but the sim logs `… ends at`.
Fix either the scripts or restore the markers in `internal/sim/run.go`. (Worth a reusable
debugging skill.)


## Architecture Highlights (Completed)

**Core Infrastructure:**
- LIFO stack architecture for natural worker scaling (eliminates controller complexity)
- Leakguard package for safe resource handle management with finalizer-based leak detection
- Demand-based worker spawning with explicit `taskWorkerDemand` counter
- Hardware-accelerated 128-bit atomics in nbcq for improved performance
- rdvq channel-based selectFn API: user code receives raw channels and returns small result types; `Inbox`, `Outbox`, `WaitInbox` are unexported

**Key Design Patterns:**
- Reference-counted CombineOp/GatherOp handles with Dup()/Close() semantics
- Pool-segregated combiner instances to avoid complex cross-pool handoff
- Trait-based generic collection system (FIFO for fairness, LIFO for scaling)
- Subscription-based coordination with Notifier/Listener pattern
- `taskPostWork`/`combinePostWork`/`gatherPostWork` use composition via `workq.Work` interface delegation; the workq framework keeps work items alive across retries so demand state persists.

## rdvq BufferedFunc Ordering Fix (2026-05-09)

The intermittent `unbalanced decrement detected` panic in `TestBySimulation` and the long-standing benchmark livelock both traced to a race in `rdvq.Queue.PushBackFunc`: `bufferedFn` ran *after* `q.fullOutboxes.PushBack` and `Notify`, so a receiver could pick up and recycle the buffered value before `bufferedFn` fired. `taskPostWork.bufferedFn = registerDemand` mutates per-message state (`taskWork.demandRegistered`) and the global demand counter; when it ran late on a recycled `taskWork`, the demand counter drifted negative, `IsZero()` started lying, and spawning stalled.

**Fix:** Move `bufferedFn()` before `q.fullOutboxes.PushBack(outbox)` in both PushBackFunc paths in `internal/rdvq/queue.go`. Documented contract on `BufferedFunc`: it runs synchronously and completes before the value can be observed by any receiver. Pinned with `TestQueue_BufferedFuncOrdering` (fast and slow paths). Companion psg changes: `taskWork.demandRegistered` now `atomic.Bool` (was a non-atomic bool touched from multiple goroutines), decrement-in-`Free()` for work freed before pickup, `Reset()` panic as invariant check.

**Implications for previously-planned work:**

- The "Benchmark Deadlock Issue" analysis (originally diagnosed as a circular task↔combiner queue dependency) was a misdiagnosis — the actual cause was the demand-counter corruption above. With the rdvq fix, that hypothesis no longer needs investigation.
- The "Task Worker Demand-Based Spawning v3 (dedicated spawner goroutine)" design was motivated by a livelock that turns out to be the same bug. The current `trySpawnTaskWorker`-from-demanding-sites model is sufficient now that the counter is honest.
- The "Combiner Worker Demand Tracking" plan was framed as CRITICAL because of the same misdiagnosed livelock. The existing two-trigger model — `maybeSpawn` (posting side) plus `unmetDemandFn` (receiving side, via `workQueue.ExecuteOne`) — covers the spawn decision points. The remaining "all combiners blocked on gatherQueue" scenario produces correct backpressure-by-design, not livelock.

## Verified-clean review (2026-05-09)

After the rdvq BufferedFunc ordering fix, did a once-over for analogous issues:

- **`internal/cpstate/state.go`** — `spawnedGoroutineCount` is reservation-coupled: every successful `ShouldSpawn{First,}Goroutine()` is paired with a `GoroutineExiting()` (or `GoroutineExiting → GoroutineRestarted` for end-of-work confirmation). Retry loops in `Should*` handle concurrent reservation contention correctly. Cancel/shutdown branch in `combinerpool.go` deliberately skips `GoroutineExiting()` (the comment is accurate — accounting doesn't matter once the job is shutting down). No drift potential analogous to `taskWorkerDemand`.
- **`gatherQueue` notification path** — `gatherPostWork.Execute` passes `bufferedFn = nil` for both `TryPushBack` and `PushBackFunc`, so the rdvq race never affected this path. Combiners blocked on `BasicPushSelect` for gather posting wake via standard buffered-channel mechanics when a gather receiver drains the outbox; no separate notification needed. Receive side (`gatherSelect`) uses standard rdvq waiter patterns.

## rdvq API cleanup and orphan elimination (2026-05-09 / 2026-05-10)

The rdvq receive/wait/push APIs were reshaped so user code interacts only with channels and small result types — no more `Inbox`, `Outbox`, or `WaitInbox` structs in user signatures, and no more `Emptied()` / `Filled()` bookkeeping calls. All three of those types are now unexported.

Final shapes (user-visible):

```go
type PopSelectFunc[T any] = func(inboxCh <-chan T, outboxWaitCh <-chan RenotifyFunc) PopSelectResult[T]
type PushSelectFunc[T any] = func(outboxCh chan<- T) bool
type WaitSelectFunc       = func(waitCh <-chan RenotifyFunc) RenotifyFunc

func (q *Queue[T]) PopFront(ctx, receiver) (T, error)
func (q *Queue[T]) PopFrontFunc(receiver, selectFn) (T, bool)
```

`PopSelectResult` has unexported fields and mutator methods (`InboxEmptied`, `OutboxReady`) that panic on misuse (nil renotifyFn, double-call, both-set). Idiomatic selectFn pattern: declare a named `result` return value, call `result.InboxEmptied(v)` / `result.OutboxReady(rf)` in the matching select case, bare-return everywhere — including the "neither fired" case.

The orphan-elimination plan (a long-standing TODO) became feasible during this cleanup: a registration-order flip inside `Queue.PopFrontFunc` (waitInbox before stack-inbox) made the dual-fire race that motivated psg's orphan task queue structurally impossible. With that, `Job.orphanedTasks`, `Job.orphanWaiters`, `orphanedTaskWork`, `orphanedTaskRenotify`, and `Job.tryGetOrphanedTask` were all removed. The runTasks selectFn collapsed from a nested orphanWaiters-wrapped wait to a flat select on inbox + outboxWait + idleTimer + ctx.

Companion fix: in `Queue.PopFrontFunc`, when post-selectFn cleanup drains a value as orphan AND `selectFn` had also picked up an outbox-wait notification, the renotifyFn is now forwarded (previously dropped — a "lost notification" bug, separate from but exposed during the orphan work).

## Sender shutdown notifies pending listeners (2026-05-25)

### The symptom

A CombinerPool worker goroutine on its exit path (the `defer worker.Release()` at `combinerpool.go:130` of `CombinerPool.goroutine`) cascades through `integrationExEnv.Release` → `baseExEnv.Release` → `Sender.Release` → `outbox.free` → `omnipool.PutCustom` → `outboxTrait.Reset` → `Listeners.Reset`. The Reset panicked if `Listeners.q` was non-empty.

Reliably exposed (~25% of test runs) by the new sim's submit-via-start pattern, which creates higher scatter pressure on CombinerPool outboxes than the old sim did.

### What the leftover listeners actually mean

The listeners on a Sender's outbox are work items that wanted to send a value *through this Sender* but couldn't (outbox was full). They subscribed for a "your outbox drained, try again" notification.

When the Sender shuts down, those work items still want to send — they just can't via this Sender anymore. The correct response is to notify them so they retry on a different Sender (i.e., another goroutine's worker). Leaving them subscribed to a dead Sender would silently abandon their work (or, worse, attach those subscriptions to the *next* user of the recycled outbox).

The `Listeners.Reset` panic correctly enforces an invariant for putting an outbox back into the pool: the listeners queue must be empty, because a recycled outbox carrying stale subscriptions would leak notifications into a context that doesn't own them. The panic did its job — it surfaced abandoned subscriptions that needed handling before recycle. The fix isn't to relax the invariant; it's to satisfy it by draining the subscriptions appropriately before Reset runs.

`outbox.free()` now calls `ob.listeners.NotifyAll()` when refcount reaches 0, before returning the outbox to the omnipool. Each listener wrapper's `notify` fires with `NoopRenotify`, the workq re-execute path picks the work up on the next available worker, the wrapper is returned to its pool, and the listeners queue is empty by the time `omnipool.PutCustom` invokes `outboxTrait.Reset`. The invariant holds; no panic.

### How the leftover got there in the first place

The work registers the listener at `combinerpool.go:294-307`:
```go
if !meta.ShouldBlock() {
    ex.AddToListeners(w.pool.combineQueue.ListenersFor(meta.Sender()))
    ex.AddToListeners(&w.pool.state.SpawnNotifier().Listeners)

    posted := tryPost()
    if !posted {
        waiting()
    }
    return posted, nil
}
```

The subscribe-then-retry is intentionally optimistic: subscribe first (so we don't miss a drain that happens between attempts), then retry. Either branch can leave the listener queued:

- `posted=true`: the retry succeeded. The work is done, but the listener subscription stays in the queue as a deliberate leftover.
- `posted=false`: the work is parked. The listener stays, waiting for a future emptied() to fire it.

Couldn't we just remove the listener when the retry succeeds? No — `outbox.listeners` is backed by `nbcq.Queue` (Michael-Scott lock-free queue), which supports only enqueue/dequeue. There's no mechanism to remove an arbitrary entry. So the leftover-on-success is structural, not a missing cleanup.

In normal operation the leftover is harmless: a future `emptied()` calls `Notify(nil)` which pops the wrapper and invokes its `notify`; the workq sees the work has either already completed (no-op) or is ready to retry (it retries); the wrapper goes back to its pool. The Reset panic fires only when `outbox.free()` decrements refcount to 0 *before* any subsequent `emptied()` consumed the leftover. The new sim's higher scatter pressure makes outboxes fill-and-release more quickly, widening the timing window where leftovers survive until free.

### How the retry actually lands on another Sender (verified)

The wiring is end-to-end asynchronous and lands correctly:

1. **`accepted.go:47`**: `q.listener.Notify = q.waiters.Notify`. The workq queue's Listener, when fired, just calls `q.waiters.Notify`.

2. **`waiters.go:125-134`**: `Waiters.Notify` is `w.q.TryPushBack(renotifyFn)` — atomic enqueue to the waiters' nbcq queue. No synchronous callbacks, no use-after-free risk during the call chain inside `outbox.free()`.

3. **`accepted.go:344-369` `WaitForNew`**: a blocked worker is waiting on the workq's `waiters`. When the renotifyFn lands in the waiters queue, that worker wakes up, returns from WaitForNew, and re-enters the work-execution loop.

4. **`accepted.go:407-417` `execute`**: a `combinePostWork.Execute` that returned `posted=false` (didn't call `ex.Starting()`) is marked postponed and kept in `c.q.postponed`. The next worker to pull from postponed re-runs `Execute` with **its own** ctx and `meta.Sender()` — a different Sender whose outbox may not be full.

So when a Sender shuts down with pending listener subscriptions, NotifyAll drains them; each wrapper signals the workq's waiters; some other worker picks up the postponed work and retries on its own Sender. Work isn't lost.

For the `posted=true` leftover case (the work already completed), the notification still fires, but the workq has nothing to do — the postponed queue doesn't contain that work item anymore, so the woken worker re-checks, finds nothing extra to run, and returns to waiting. Harmless.

### Files involved

- `internal/rdvq/outbox.go` — the fix (NotifyAll on refcount=0)
- `internal/rdvq/listeners.go` — Reset's strictness vs. Sender-shutdown semantics
- `combinerpool.go:294-307` — the subscribe-then-retry pattern that produces leftovers
- `internal/workq/accepted.go:47, 426-428, 344-369, 407-417` — the listener-to-waiters wiring and the postponed-work retry path
- `internal/rdvq/waiters.go:125-134` — Notify is just an atomic enqueue

## Pre-existing race in TestLogLeakStructured (observed 2026-05-26)

`internal/leakguard/structured_log_test.go:50` reads `bytes.Buffer.Len()` from the test goroutine while a GC-triggered finalizer goroutine is still writing to the same buffer via `slog.JSONHandler.Handle`. Reproduces under `go test -race ./internal/leakguard/` on this commit AND on the prior commit (verified by stashing Wave 2 changes and running the test), so it predates Wave 2 — captured here so a future investigator doesn't burn cycles re-confirming.

### Stack of the race

- **Read** (goroutine 9, main test): `bytes.(*Buffer).Len()` from `TestLogLeakStructured` at `structured_log_test.go:50`.
- **Write** (goroutine 18, finalizer): `bytes.(*Buffer).grow()` → `bytes.(*Buffer).Write()` → `log/slog.(*commonHandler).handle()` → `log/slog.(*JSONHandler).Handle()` → `log/slog.LogAttrs()` → `leakguard.LogLeak()` at `leakguard.go:130` → triggered from the finalizer closure registered in `handle.Init` at `leakguard.go:314`.

The test buffer is shared between the test (which constructs a `slog.Handler` writing into it, then asserts on contents) and the leak-logging finalizer (which fires whenever a `leakguard.Handle` is GC'd without `Close`).

### Why it races

The test deliberately drops a handle to trigger the leak path, then reads the buffer to verify the leak was logged. The test currently has no synchronization that waits for the finalizer's `slog` write to complete before the read — it relies on `runtime.GC()` returning after finalizers, but `runtime.GC()` only guarantees the finalizers have *started*, not that they have finished. The race detector catches the un-synchronized buffer access.

### Plausible fixes

- **Buffer with mutex.** Wrap the buffer in a small `mu sync.Mutex` and have both the slog handler and the test's reader acquire it. Cheap; isolates the test from finalizer timing.
- **Channel handshake.** Have `LogLeak` send on a channel after the slog write completes, test reads after receiving. More explicit but couples test to LogLeak internals.
- **Avoid finalizer in the test entirely.** Construct the leak condition via a direct call to whatever LogLeak does at line 130, no finalizer. Removes the race surface but also reduces what the test is verifying.

The mutex approach is the cleanest. Note: this is a test-only race; production `LogLeak` callers don't share a buffer with a reader, so no production fix needed.

### Files involved

- `internal/leakguard/structured_log_test.go:43-50` — the test that races
- `internal/leakguard/leakguard.go:130` — `LogLeak` (the writer side)
- `internal/leakguard/leakguard.go:314` — finalizer registration in `Init`

## Thread A: Handler[T] unification + op trio rename (2026-05-31 / 2026-06-01)

Substantial reshape on the `combiner` branch. **Status: largely complete.** All committed work is on origin; tests green; build green.

### Thread A: complete (landed in commit order)

1. `0509241` **psgfn**: add `Handler[T]` interface, `HandlerFunc[T]` / `ErrHandler` adapters, `NewAccumulator` constructor — additive.
2. `9ebc5cd` **psg**: migrate `Gatherer` to take `psgfn.Handler[T]`.
3. `24553ee` **Op trio step 1**: `Gather`/`Gatherer` → `Skim`/`Skimmer`. Wave methods, internal types, files renamed.
4. `942620f` **Op trio step 2**: `Combiner`/`Combine` → `Funnel`. `CombinerPool` → `FunnelPool` (transitional).
5. `91872d0` **Op trio step 3**: `TaskRunner` → `Launcher`.
6. `cac52fc` **Step 3 (arity collapse)**: single `Launcher[T]` takes `psgfn.Handler[T]`; Launcher0/Launcher2 removed; per-arity Task interfaces removed; `psgfn.Task` named func adapter with short-circuit-on-err.
7. `bdca508` **Step 4 (Submit family)**: `Submit(v)` / `SubmitErr(err)` / `SubmitResult(v, err)` plus Try variants plus `Start`/`TryStart` sugars; same family across Launcher / Skimmer / Funnel.

API_DESIGN.md contains the trio-rename rationale and a `considered & rejected` entry.

### Thread B: complete (B.1 + B.2 + worker plumbing + factory pass)

`bd3ff60` B.1: wave moves to constructor (required).
`8bf2541` B.2: nil-OK at construction; resolution via existing `ctxMeta.wave`.
`c187379` Worker plumbing: nil-wave dispatch works from inside ALL three op body types — task bodies (taskWork carries dispatching wave), Funnel Accumulate/Flush bodies (funnelWork carries it), and Skim handler bodies (via fixing `ensureCtxMeta` to preserve `wave` across same-job ctx transitions). Sim alternates explicit-vs-nil wave for both Skimmers and Launchers, exercising both code paths every run.
`32c0c62` Factory pass: psgwf and otpsg wrappers accept nil wave; `otpsg.Scatter` drops the wave param (resolves from ctx).

### psgfn fold + AccumulatorFactory + full convenience surface

`e853a2a` Big consolidation commit:
- **psgfn package deleted**; all types moved to top-level `psg` (Handler, HandlerFunc, Accumulator, FuncAccumulator, NewAccumulator, NewHandler).
- **AccumulatorFactory is now an interface** with `NewAccumulator() Accumulator[T]` + `Close() error`. `funnelOp.unref()` calls `factory.Close()` on the last-reference cleanup path; Close errors route through the framework err sink.
- **Adapter parallel set**: for each interface (Handler, Accumulator, AccumulatorFactory), there are both generic and err-only flavors. `FuncErrAccumulator` and `FuncErrAccumulatorFactory` store fns in struct fields directly — zero framework-added closures for the err-only path.
- **Op constructor progression** per type: `NewLauncher` (interface, alloc-free hot path) → `NewFnLauncher` (closure, T inferred) → `NewTaskLauncher` (no-arg) → `NewErrLauncher` (err-only). Same shape for Skimmer (minus Task) and Funnel.
- **Type aliases** for every void-T case: `Task`, `ErrHandler`, `ErrAccumulator`, `ErrAccumulatorFactory`, `TaskLauncher`, `ErrLauncher`, `ErrSkimmer`, `ErrFunnel` — all aliases for the `*[struct{}]` instantiations, named for intent.

### Threads B and C status

- **Thread B**: complete.
- **Thread C v0.1 landed**: `Forever` sentinel added (`time.Date(9999, 1, 1, ...)` UTC); Submit / SubmitErr / SubmitResult on Launcher pass it through to the dispatch path so the intent reads as "block until success." `Launcher.dispatch` returns `(bool, error)` so the Try* family no longer needs the TODO sentinel-error path. `Pool.block` treats `Forever` the same as zero (no timer) — block until cancellation / notification. Zero-deadline semantic in `TryExecuteNow` was already "attempt once" via the unset `ex.AddToListeners` (the blocking layer skips listener registration when AddToListeners is nil, so contended dispatch returns `(false, nil)` after one attempt). Doc-aligned; no behavior change for zero.

### Thread C — blocked on Pool/workq consolidation

The remaining Thread C work (Try* honoring non-zero non-Forever deadlines via bounded-wait blocking) can't land cleanly until the underlying inconsistencies in the Pool + workq integration are resolved. The investigation surfaced three:

1. **Work-type deadline propagation is uneven.** `limiterScatterWork`, `launcherScatterWork`, `funnelWork`: pass `w.deadline` to `ExecuteOrWait` / `governor.Execute`; the timer reaches `Pool.block`. `taskPostWork` receives a deadline parameter, doesn't store it, calls `BasicPushSelect` which only watches `ctx.Done()` and `outboxCh` — no timer. (Pre-existing open issue: "Deadline propagation in taskPostWork.")

2. **Dispatch entry points use different "should block" signaling.** `ExecuteNowOrQueue` sets `ex.AddToListeners` to a panicking func to enable the `ShouldBlockOrPostpone` path. `TryExecuteNow` leaves it nil, so the entire blocking loop in `ExecuteOrWait` is bypassed regardless of deadline. Naively setting `AddToListeners` in `TryExecuteNow` causes hangs (see #3).

3. **`errBlockWaitSignaled` conflates timer-fired with notification-received.** `Pool.block` converts both to `nil` before returning. `ExecuteOrWait` can't distinguish "deadline reached, give up" from "got a signal, re-check condition" — re-enters `blockFn` with an already-expired deadline, the new timer fires at 0ns, tight loop.

The structural fix overlaps with the destination doc's **Pool consolidation** (merge TaskPool + FunnelPool into one Pool, rationalize the workq integration). Doing Thread C now would mean wrestling the same inconsistencies twice. Defer Thread C until after the pool consolidation pass; it will likely fall out naturally once `taskPostWork`, the `AddToListeners` signaling, and the `errBlockWaitSignaled` conflation are unified.

### Wrap-pattern migration (landed `aab904c`)

All `psg.NewLauncher(wave, psg.NewTask(fn))` / `psg.NewSkimmer(wave, psg.NewHandler(fn))` / `psg.NewSkimmer(wave, psg.NewErrHandler(fn))` wrap patterns in tests/examples migrated to the convenience constructors (`NewTaskLauncher` / `NewFnSkimmer` / `NewErrSkimmer`). Two intentional stragglers left: `psgwf/scatter.go` and `internal/sim/run.go` both have `body := psg.NewTask(...)` as a named intermediate variable — the Task value is constructed for downstream framework use rather than wrapped inline, so the convenience form doesn't fit.

### Naming-pass deferred items (still relevant)

- `CombinerPool` → `FunnelPool` retained; goes away when Pool consolidates per the destination doc.
- `psgwf.GenericTaskRunner` (and related psgwf wrappers) still use legacy names; rename or retire with the broader psgwf migration.
- chartgen's bench-data parser still reads the historical metric name `combinerLimit`; legacy benchmark file emits `funnelLimit`. Re-align when `bench.txt` is regenerated post-rename.

## Pool consolidation — foundational analysis (2026-06-06)

Design pass for the TaskPool (`Pool`, job.go) + FunnelPool merge. **Settled
direction** (confirmed with PN): the two pools become **one demand-driven,
uncapped worker pool**; per-op `Limiter` is the *only* concurrency control.
Approach: upgrade the foundational internal packages (`workq` / `delayq` /
`*state`) into usefully-abstracted shared building blocks *first*, so the pool
usage simplifies and collapses naturally — wrestle each seam once, in the
foundation, where each integration change should be a simplification or no-op.
Checkpoint at stable (green) states along the way.

### The three foundational seams (why the merge is hard today)

1. **Worker-state accounting is implemented twice, in two styles, with
   different spawn policies.** Funnel pool uses `internal/cpstate.FunnelPoolState`
   (`spawnedGoroutineCount`/`liveGoroutineCount`, `ShouldSpawn{First,}Goroutine`
   capped at `maxConcurrency`, `TryIdleExit`, idle timeout/jitter,
   `spawnNotifier`). Task pool open-codes the same concerns inline in job.go
   (`taskWorkerDemand`, `taskWorkersSpawning`, `taskWorkerIdleTimeout/Jitter`,
   `latestTaskWorkerIdleExit`, `trySpawnTaskWorker`). Policies *differ*: task =
   demand-driven, uncapped, **scale-up aggressively** via a self-propagating
   spawn chain (job.go:801-808: a worker that secures its first task spawns the
   next iff demand remains); funnel = **minimize goroutines** (each goroutine is
   a separate accumulator instance → more partial aggregates to flush;
   funnelpool.go:30) and caps at `maxConcurrency`.

2. **Blocking path conflates two signals.** `Pool.skimSelect` sets
   `errBlockWaitSignaled` for BOTH "block-deadline timer fired" (give up) and
   "block-wait notification arrived" (re-check) — job.go:483-488 — and
   `Pool.block` flattens both to nil (job.go:332). `ExecuteOrWait` can't
   distinguish them. (Thread-C blocker #3.)

3. **Two "should-block" signaling conventions + dropped deadline.**
   `ExecuteNowOrQueue` sets a panicking `AddToListeners`; `TryExecuteNow` leaves
   it nil (`execution.go:24` `ShouldBlockOrPostpone`). And `newTaskPostWork`
   takes a `deadline` it never stores/uses (job.go:1012); the blocking post uses
   `rdvq.BasicPushSelect` (job.go:951) which watches only ctx + outbox, no timer.
   (Thread-C blockers #1, #2.)

### Funnel dual-concurrency finding (key)

FunnelPool has **two independent** concurrency controls today:
- pool-wide goroutine cap (`cpstate.maxConcurrency` via `ShouldSpawnGoroutine`,
  funnelpool.go:254,323) — the legacy CombinerPool limit; and
- per-op `Limiter` (`funnelWork.Execute`, funnelop.go:750-772), independent of
  goroutine count.
The destination keeps only the per-op Limiter, so the goroutine cap is deleted.
maxConcurrency is already *loosely* enforced (the receiving-side `unmetDemandFn`
spawn bypasses it), default is -1 (unlimited), so deletion is low-risk on
enforcement — but see the spawn-policy subtleties below.

### Subtleties uncovered (don't re-derive these)

- **`unmetDemandFn` effectively never fires for the funnel pool.** It triggers
  only when `workAddedCount > 1` within one `Accepted.ExecuteOne`
  (accepted.go:315), but `cpWorker.AddWork` queues at most one item per call.
  So funnel scale-up is driven *entirely* by the posting-side
  `ShouldSpawnGoroutine`, NOT by `unmetDemandFn`. ⇒ "delete the cap and lean on
  unmetDemandFn" would pin the pool at one goroutine. A real replacement spawn
  policy is required.
- With the **default unlimited cap, `ShouldSpawnGoroutine` always returns
  true**, so every *buffered* post currently spawns a goroutine. Deletion must
  pair with a deliberate spawn policy, not bare removal.
- **`spawnedGoroutineCount` vs `liveGoroutineCount` are inconsistent across the
  two spawn paths** (posting-side increments `spawnedGoroutineCount`;
  `unmetDemandFn`/`spawnNewGoroutine` increments only `liveGoroutineCount` via
  `GoroutineStarted`), reconciled by `GoroutineRestarted`. Confusing-by-accident;
  the consolidation should collapse to one clean source of truth.
- Funnel goroutine accounting serves **two roles**: (a) the cap [DELETE], and
  (b) **last-goroutine detection** — `GoroutineExiting()==true` drives the
  end-of-work `confirmEndOfWork` dance + final `flushAll` (funnelpool.go:188-215)
  [PRESERVE].

### Flush ownership constraint (PN, 2026-06-06)

**Flush mechanics must end up living with `Wave`, not `Pool` or `Funnel`.**
Flush is a per-batch-of-work concern, so in the three-type model it belongs to
Wave (batch lifecycle + drain), not the fungible worker Pool and not scattered
across the Funnel op. Today flush is split across the wrong owners:

- `FunnelPool.flushQ` (`delayq` of pending deadlines) + the deadline-timer
  driving in `cpWorker` (`flushToNextDeadline`/`flushAll`, cpworker.go) live on
  the *funnel pool*.
- End-of-work flush coordination — `jobstate.JobState.RegisterFlusher()`,
  `nextFlushChan`, `flushListener`, the `Closed→Flushing→Done` transitions —
  lives on the *Pool's state*.

Destination: a worker *drives* a flush but does NOT *own* it; the Flushing-stage
coordination and `flushListener` move to **Wave**; the Funnel op only *registers*
deadlines. Consistent with REFACTOR_PLAN Wave 5 ("migrate op-ownership + drain
machinery from Pool to Wave").

#### Refined decision (PN concurred, 2026-06-06): timed work in workq

The `flushQ` itself is best modeled as a **generic "timed work" facility built
into `workq`, instantiated per-pool** — NOT a bespoke per-Wave queue. Rationale:
draining is a worker-level task and workers span Waves, so one pool-level merged
delay queue (a single worker drives one timer for the soonest deadline across
all Waves) is correct and strictly more efficient than per-Wave queues (which
force a worker to select across N timers). A flush is just *a unit of work that
becomes ready at deadline T*; `workq` already selects fresh → postponed →
wait-with-timer, so "ready at T" is a natural third source folded into the same
`ExecuteOne` wait/select (the worker's select already watches the idle timer —
adding a next-deadline timer is incremental).

**Why early / why it matters for the merge:** the bespoke flush machinery in
`cpWorker` (`flushDeadlineTimer`, `flushToNextDeadline`, the extra select cases)
is the single biggest reason the funnel worker loop differs from the task worker
loop. Making timed work native to `workq` dissolves that specialness — both
loops just run `ExecuteOne`, delay queue transparent — shrinking the eventual
worker-loop merge surface. Hence this becomes checkpoint 1.

**Ownership = mechanism vs policy:**
- `workq` (per-pool) owns the *mechanism*: schedule a `Work` ready at T, arm one
  timer, promote due items to fresh work. Generic, not flush-specific (task-side
  backoff/retry could reuse it later). Keep this concern cleanly separated —
  composed into the worker's wait, NOT tangled into Accepted's fresh/postponed
  logic — to avoid scope-creeping workq.
- **Wave** owns the *policy/lifecycle*: it scheduled its accumulators' flushes so
  it holds references to them; **force-flush = expedite/`Remove` its own
  entries**; **drain barrier** = Wave is Done only when its flushes have fired;
  `WithFlushListener` (today a Pool option via `JobState`, psgopt/job.go:89)
  relocates here.
- **Funnel op** just schedules "flush this accumulator at T" against its Wave.

**Verified enablers:**
- Flush is idempotent — `funnelInstance.flush` has an "already flushed, ignore"
  guard under `c.mu` (funnelop.go:609-614) — so Wave force-flush racing a natural
  deadline firing is safe (no double-emit).
- `delayq.Remove(item)` is O(log n) — each `Item` tracks its heap position
  (delayq.go:149) — so a Wave expedites/cancels its entries directly via held
  references, no scan, no new per-Wave index. Sight-line: this drops to **O(1)**
  if `delayq` shifts from a binary heap to a timing wheel (TODO.md item — "avoid
  O(log n) heap overhead … esp. for Flush"). See "delayq optimization target"
  below. Coding the timed-work facility against `delayq`'s interface
  (`Schedule`/`Remove`/`Drain→(ready,next)`/`wake`/`Item`), not heap internals,
  keeps that swap a `delayq`-plus-`Item`-helper change.

  **delayq optimization target (derived with PN, 2026-06-06):** a *bucketed,
  tickless timing wheel*. The ordering structure holds **buckets, not individual
  items**, so its cardinality is decoupled from timer count (a million timers in
  one window = one bucket); the finest bucket width is set at **scheduler noise
  (~10ms)**, which makes the quantization lossless in practice — Go timer /
  goroutine / OS jitter already sit above that floor, so the "exact firing" a
  per-item heap preserves is illusory precision below the noise floor. Timer ops
  become uniformly O(1) (compute slot, append/unlink a list node). Two variants:
  *heap-of-buckets* (Kafka-style: next bucket via heap root, O(log B) on
  bucket create/destroy) or *wheel + occupancy bitmap* (next-non-empty via
  find-first-set, **true O(1) reschedule** — preferred for our flush churn,
  where every `Accumulate` pushes the deadline out). Hierarchy gives unbounded
  range. This is the standard high-perf tickless timer (Kafka hierarchical
  timing wheel + delay-queue-of-buckets; tickless cousin of Netty
  `HashedWheelTimer`). Stays behind the `delayq` interface; only the `Item`
  helper's internals change (slot/list-node instead of heap position). Optional
  worst-case refinement (likely unnecessary for flushes, since worker
  parallelism, not the heap, bounds flush throughput): partial bucket admission
  to hard-cap the ordered set. **Baseline for now stays the current binary
  heap** — all of the above is the deferred path behind the stable interface.
- `delayq.Init(wake func())` already has a wake hook — the seam to re-arm a
  worker's timer when the next deadline lowers.

**Likely bonus simplification:** a pending timed item *is* outstanding work, so
the Wave's normal drain accounting can subsume it, collapsing the end-of-work
`flushAll` (far-future `now`) + `RegisterFlusher`/`nextFlushChan` channel dance
into "expedite my timed entries, then drain as usual."

**Risk to validate:** converting flush from out-of-band worker-driven calls
(`cpWorker.flushToNextDeadline`) into a first-class `Work` item flowing through
`Accepted` (fresh/postponed/governor) changes contention/ordering. Arguably
better (flushes become governed, first-class work) but must be proven against
`TestBySimulation` (`-short`, full, and `-race`).

### Sequenced checkpoints (plan, revised 2026-06-06)

1. **Timed work in workq** (flush → workq mechanism + Wave policy) — add a
   generic per-pool "work ready at deadline T" facility to `workq`, folded into
   the `ExecuteOne` wait/select; migrate funnel flush onto it. Relocates flush
   ownership per the "Flush ownership constraint" section: mechanism in
   workq/Pool, policy (force-flush, drain barrier, `WithFlushListener`) on Wave.
   Dissolves the `cpWorker` flush-timer divergence (biggest worker-loop
   difference), so it's the highest-leverage merge-enabler. Validate against
   `TestBySimulation` (`-short`, full, `-race`).
2. **workq block-path rationalization** — split `errBlockWaitSignaled` into
   distinct deadline-reached vs notify-received signals; unify the
   `AddToListeners` should-block convention; thread `taskPostWork`'s dropped
   deadline. Behavior-neutral (only currently-unreachable deadline paths
   change). Dissolves all three Thread-C blockers. *(Bounded; sim-covered.)*
3. **Delete funnel `maxConcurrency`** — convert funnel spawning to a deliberate
   demand-driven policy (NOT bare removal; see subtleties), delete the cap +
   `spawnNotifier`/spawn-slot-wait machinery + `ShouldSpawnGoroutine`, preserve
   live-goroutine/last-goroutine accounting + idle-exit. Update
   `maxholdtime_test.go` (uses `WithMaxConcurrency(1)` to force one goroutine —
   re-express via a per-op Semaphore Limiter or natural light-load behavior).
   `funnel_legacy_bench_test.go` is build-tagged `psg_wave3_legacy_bench` (not
   in normal builds). Remove `psgopt.WithMaxConcurrency` + `opts.MaxConcurrency`.
4. **Worker-state unification** — with both pools demand-driven/uncapped, extract
   the shared spawn (demand counter + bounded spawning counter + chain-on-secure
   + idle-exit throttle) into one building block both pools use; collapse
   `cpstate` + the task-pool inline state onto it.
5. **Posting-path convergence** — `taskPostWork`/`funnelPostWork` onto a shared
   `ExecuteOrWait`-based shape; then the actual Pool/FunnelPool merge falls out.

Checkpoints 1–3 are largely independent and can be sequenced by appetite;
4–5 depend on the demand-driven convergence from 3.

#### Checkpoint 1 design (settled with PN, 2026-06-06)

**`delayq` is subsumed *inside* the workq executable-work queue (`Accepted`),
not wrapped beside it.** Public surface added: exactly `Schedule(w, deadline)`
and `Remove(w)`. Everything else is internal to `Accepted`: draining due items
into the fresh source within `ExecuteOne`, the deadline timer, and wiring
`delayq.wake → q.waiters.Notify`.

- **Handle is the `Work` itself** — no separate handle object, no allocation, no
  pooling. Schedulable work = **option A**: a `ScheduledWork` interface
  (`Work` + `Position() int` + `SetPosition(int)`); the heap position lives in
  the work object via a tiny embeddable helper (mirrors how `WorkItem` supplies
  `ID`/`Group`/`Free`). This is the same pattern `funnelInstance` already uses
  (`flushHeapPos`, funnelop.go:432,445), generalized.
- **No `Reschedule` method.** `delayq.Schedule` is idempotent-replace
  (delayq.go:138), and because the handle is the stable work object,
  `Schedule(w, laterDeadline)` *is* reschedule. The funnel needs this on every
  `Accumulate`; it falls out for free. Surface stays exactly Schedule + Remove.
- Once a timed item is **drained** into fresh it is stored as plain `Work`; its
  `Item`-ness is dormant until rescheduled. `delayq.Remove` is safe on
  already-drained/never-scheduled items, so a Wave force-flushing an entry that
  just fired naturally is a harmless no-op (consistent with the idempotent-flush
  guard).
- **Timer (internal):** wire `delayq.wake → q.waiters.Notify` so a *sooner*
  newly-scheduled deadline nudges a parked worker. For a deadline simply
  arriving, thread the current `next` deadline through the internal
  `AddWorkFunc` contract so the worker's existing select watches a
  workq-provided timer (efficient pooled timer, no goroutine-per-block); on fire
  the worker re-enters `ExecuteOne` and drains due items. `AddWorkFunc` is
  workq-internal, so the public surface is unaffected. (Fully inverting select
  ownership into workq is the eventual merge end state, out of scope here.)

#### Checkpoint 1 sub-steps (each independently green)

- **1a — DONE (`357c39a`).** `internal/workq`: `ScheduledWork` interface +
  embeddable `Scheduled` helper; `delayq` subsumed into `Accepted` (internal
  field + public `Schedule`/`Remove`; due-drain folded into
  `ExecuteOne`/`TryExecuteOne` via `controller.drainTimed`; `wake → waiters`).
  Additive — no psg caller schedules timed work yet; full suite + `-race`
  green; workq unit tests cover immediate-due / remove / in-place reschedule /
  future-deadline parked-worker wake. **Deviation from the plan:** the
  next-deadline wake uses an internal `time.AfterFunc` armed in `WaitForNew`
  (keeps 1a entirely within workq, zero psg changes) rather than threading the
  deadline through `AddWorkFunc`. That threading (to drop the per-fire
  goroutine by using the worker's own pooled-timer select) is **deferred to 1b**,
  where `cpWorker`'s select is reworked anyway — see the `TODO(checkpoint-1b)`
  in `accepted.go` `WaitForNew`.
- **1b-i — DONE (`942c3aa`).** Threaded the next-deadline through `AddWorkFunc`
  (trailing `timedCh <-chan time.Time`); `WaitForNew` arms a pooled timer and
  passes its channel, replacing 1a's `AfterFunc`. Implementers accept it;
  inert until 1b-ii (skim never schedules timed work; funnel flush still on
  `cp.flushQ`). Behavior-neutral; suite + `-race` green.
- **Rename DONE (`18295ab`).** `halfBoundFunnel` → `funnelInstance` (the
  "half-bound" term was a remnant of the dropped `Combiner[I,O]` output type).
- **1b-ii — DONE (`b9dbf2d`).** `funnelInstance` implements
  `workq.ScheduledWork`; funnel flush routes through `cp.workQueue.Schedule`/
  `Remove`; retired `cp.flushQ`, `cpWorker.flushToNextDeadline`/flush timer, the
  `funnelFlusher` interface (funnelmap.go deleted), and `FunnelPool` Yield. Net
  −25 lines. **Key learning:** an async end-of-work sweep (flushAll draining to
  `fresh` for ExecuteOne to run) *lost flushes* when the last goroutine exited
  before executing them — the sim caught it (variable undercounts). Reverted to a
  **synchronous** flushAll: `Accepted.DrainAllTimed(dst)` returns all pending
  timed items and flushAll flushes each via the instance's `Flush` (flush+unref),
  preserving the known-good end-of-work dance. Deadline-driven flush stays async
  (drainTimed→fresh→Execute). `funnelInstance` keeps both `Flush` (combined,
  end-of-work) and `Execute`+`Free` (split, deadline path); an instance is
  drained exactly once so only one path runs per instance. Full suite + full sim
  + `-race` (funnel/skim/maxholdtime/workq/sim) green; lint 0. Minor leftover:
  `funnelOp.instanceCount` is now write-only (InstanceCount() removed) — harmless,
  lint-clean; drop in a later cleanup.
  Original worked-out details (kept for reference):
  - `funnelInstance` already has `Position`/`SetPosition` (was for `funnelFlusher`'s
    `delayq.Item`); workQueue's internal delayq calls them identically, so the
    `queued`/`SetPosition(0)`-flips-`queued` coordination + lock ordering
    (delayq.mu → c.mu) carry over unchanged.
  - Add `Work` methods: **`ID()` must use `workq.NewWorkID()`** (a fresh field
    set at allocate), NOT `funnelInstanceID` — the latter is a separate counter
    and could collide with a `WorkItem` ID, tripping `requeueBuffer`'s
    "unexpected equal IDs" panic. `Group()` = `earliestGroup`. **`Execute`** =
    `ex.Starting()` then flush, acquiring the worker's `Sender` from the ctx
    exec env exactly like `funnelWork.executeInner` (`meta.executionEnvironment.(*cpWorker).Sender()`).
    **`Free`** = `Unref` (drop the ref held while queued). So the existing
    refcount lifecycle maps: Execute=flush, Free=Unref; do NOT embed `WorkItem`
    (its `IncrementWork`/`DecrementWork` would double-count against job state).
  - `funnel()` schedules into `cp.workQueue` (future) / keeps inline flush for
    already-past deadline; `Remove` on re-flush.
  - `cpWorker`: drop the `flushDeadlineTimerCh` driving + `flushToNextDeadline`;
    wire the 1b-i `timedCh` param into `popSelect` (re-drain on fire). Due
    flushes now surface via workq `drainTimed` → fresh → `funnelInstance.Execute`.
  - **End-of-work sweep**: `flushAll` must force ALL pending timed work out of
    `workQueue.timed` regardless of deadline. Add a workq capability (e.g.
    `Accepted.DrainAllTimed`, building on `delayq.Yield`/a far-future Drain) and
    invoke it on the `nextJobFlushCh` signal. Keep `RegisterFlusher`/
    `nextFlushChan` drain-barrier in job-state for 1b-ii (Wave relocation is 1c).
    No-deadline instances still get a far-future placeholder so the sweep finds
    them.
  - Drop the `flushQ` param from `funnelWork.Funnel` / `boundFunnelWork`.
  - Validate `TestBySimulation` (`-short`, full, `-race`) + `TestMaxHoldTime*` +
    funnel/skim, and goroutine/no-leak behavior at end-of-work.
- **1c-i — DONE.** Per-instance flush-barrier reference (design (i)),
  implemented as its own green checkpoint independent of the force mechanism.
  `jobstate`: `RegisterFlusher` (per-goroutine ref + channel) replaced by
  `IncrementReference`/`DecrementReference` (bare `totalReferences` ±, the
  latter advancing to Done on last) plus a no-ref `FlushChan()` (current
  rotating flush-signal channel). `funnelWork.Funnel` new-accumulator branch
  acquires one ref next to `op.ref()`; `funnelInstance.flush()` releases it via
  `defer` after `accumulator.Flush` (panic-safe; ordered so an emitting flush's
  Submit takes its work ref before the instance ref drops). `cpWorker`
  subscribes via `FlushChan()` (no ref, no unregister); dropped the
  `unregisterAsJobFlusher` field + all unregister calls. Synchronous `flushAll`
  + `funnelInstance.Flush` (uppercase) + `timedFlusher` retained for 1c-ii.
  Barrier is now purely per-instance: `Done ⟺ Closed ∧ totalReferences==0`
  where outstanding = work refs (via IncrementWork) + live-instance refs.
  Flushing-stage entry still driven by `inFlightWork→0` (instance refs don't
  touch inFlightWork), so a live-but-idle instance can't keep the job out of
  Flushing. Validated: full short suite + workq + `TestBySimulation`
  (`-short -race -count=3`) + full non-short `TestBySimulation -race` (45s) +
  funnel/skim/maxholdtime; lint 0.
- **1c-ii — NEXT.** See "**1c-ii CONSOLIDATED DESIGN (2026-06-07)**" below
  for the agreed plan (three orthogonal concerns: heap-position under
  `delayq.mu`; atomic `admitted`; per-instance lifetime ref + op-liveness-
  at-flush). It supersedes the earlier scaffolding sketch and the
  "1c implementation design — REFINED" section. The earlier framing
  (live-set + `Accepted.Expedite` + `queueFresh` promotion + a foundation
  `Expedite` already committed in `6df4218`) was developed *before* the
  pre-existing deadlock was discovered/bisected; the consolidated design
  reframes it around the deadlock fix.
### Pre-existing latent deadlock: instance lock held across user callback (BISECTED 2026-06-06)

`TestBySimulation` (full, `-race`) hangs intermittently (~30–50% of full
runs) — a **pre-existing, long-standing** deadlock, NOT a regression from
the 1c work. `git bisect` (good=`b6641cc`, bad=`33a6cd9`, hang=10-min
timeout) pins the first hang to **`63a4d57` "sim: rewrite for
Pool/Wave/Flow vocabulary"** — a **test-only** commit (`internal/sim/*` +
`example_combiner_test.go`, zero production code). So the rewritten sim
merely began *exercising* a latent production bug; the bug itself is
older still.

**Root cause:** `funnelInstance.funnel()`/`flush()` hold the per-instance
`c.mu` across the user `Accumulate`/`Flush` callback. When that callback
re-enters the framework and blocks — `Submit`/`Start` backpressure, or a
nested `CloseAndGatherAll`/`CloseAndSkimAll` draining a sub-job — `c.mu`
stays held; meanwhile a concurrent `delayq.Drain` holding `delayq.mu`
calls `SetPosition`→`c.mu` on that instance and blocks, and the sub-job's
progress needs that drain, so the wait is circular. Baseline `665fd1f`
dump: goroutine in `Accumulate`→`CloseAndSkimAll` holds the instance
`c.mu` 8 min; `funnelInstance.SetPosition` blocked on it 8 min. The
`SetPosition`-on-`c.mu` shape is *this* era's manifestation (delayq
flush); pre-delayq it manifested differently, but the lock-across-user-
callback core is the same and predates the `Job→Pool` rename.

**Implication:** the full-race sim was never reliably green (prior
"green" claims rested on too few samples — 1–2 lucky runs). It cannot be
a green gate for the 1c work until this is fixed. Candidate fixes (PN's
design domain — concurrency-critical): don't hold `c.mu` across the user
callback, or split a position-bookkeeping lock from the accumulator lock.

**Separately — a 1c-i flush-orphan hang (distinct, FIXED in WIP):** the
1c-i per-instance barrier ref turned an *orphaned* end-of-work flush into
a *hang* (12-goroutine signature, vs the 34-goroutine mutex-deadlock
above). Cause: `cpWorker.flushAll`'s `if nextJobFlushCh==nil return false`
guard (load-bearing only for the retired per-goroutine
`unregisterAsJobFlusher`) let an unsubscribed last goroutine exit without
flushing live instances → their barrier refs never drop → Done hangs.
WIP fix: `flushAll` always drains the shared scheduled queue and flushes
(any worker can; each flush drops the instance's own barrier ref).

**Uncommitted WIP (not yet committed):**
(a) `ScheduledWorkItem` embed refactor — `funnelInstance` embeds
`workq.ScheduledWorkItem` (WorkItem+Scheduled), dropping hand-rolled
`workID`/`flushGroup`/`flushHeapPos`/`id`+`funnelInstanceID`; overrides
`SetPosition`/`Free`, adds `Execute`. (b) the `flushAll` orphan-hang fix.
Both pass `-short`+targeted+workq/delayq unit tests + lint; they cannot
be validated against the full-race sim until the pre-existing deadlock is
resolved.

### 1c-ii CONSOLIDATED DESIGN (settled with PN, 2026-06-07) — supersedes the older "1c implementation design — REFINED" and "1c-ii — NEXT" notes below

> **TODO (REVIEW, 2026-06-07):** The 1c-ii implementation (committed:
> `ScheduledState` interface swap in delayq/workq, synchronous
> `Reschedule`/`ClaimForFlush`, and the funnelInstance lifetime rewrite —
> no per-instance refcount, op-liveness-at-flush, R1/R2) is considered
> independently valid and necessary, but **PN has not fully reviewed it**
> and it **may be more complex than necessary** — revisit for
> simplification. Note: it was implemented to fix a theorized funnel
> `c.mu`/`SetPosition` deadlock that turned out NOT to be what fails the
> `TestBySimulation -race` gate (see the limiter-held-across-gather
> root-cause section at the top of this file). So it is **unvalidated
> against a green gate** — the gate can't go green until the limiter
> suspend/resume fix lands. Re-validate (full `-race` sim, many runs)
> once that fix is in.

This is the agreed design. It resolves the pre-existing deadlock *and*
the flush/lifetime tangle by replacing the overloaded `queued` flag with
**three orthogonal concerns**, and it removes the far-future-placeholder
hack and a latent op-leak along the way.

**The three things `queued` was conflating (PN's decomposition):**

- **(a) heap membership + position** — touched *only* under `delayq.mu`
  (the heap's own lock). `SetPosition` becomes pure position bookkeeping
  and **never takes `c.mu`**. *This is the deadlock fix*: a `delayq.Drain`
  can no longer wait on a `c.mu` held across user `Accumulate`/`Flush`, so
  user code can never freeze the delay queue. No leaf lock needed.
- **(b) work-queue admission** — an `atomic.Bool` owned by
  `workq.Accepted`. `Schedule` sets it; `Expedite`/`forceAll` read it.
  This is the admission check that keeps `Expedite` from being a backdoor:
  `Expedite` only *re-prioritizes already-admitted* work, never injects
  new work past the governor. PN's rule: **`c.mu` (or rather the writer)
  gates *changes* to admitted, reads are lock-free.**
- **(c) instance liveness + object lifetime** — see the lifetime model
  below; the single per-instance ref, taken at creation, plus op-liveness
  dropped at flush.

**`ScheduledState`** (new): a struct `{ admitted atomic.Bool; position int }`
encapsulating (a)+(b) with the fields unexported. `ScheduledWork` /
`delayq.Item` expose `ScheduledState() *ScheduledState` *instead of*
`Position()/SetPosition(int)`; the embeddable `Scheduled`/`ScheduledWorkItem`
just hold one. `position` is delayq-owned (only mutated under `delayq.mu`);
`admitted` is atomic. The work type embeds it and never touches the fields
directly, so it *structurally cannot* hold a heap lock across user code.

**`Schedule(w, at)` with `at == 0` = indefinite (admitted, not heaped).**
Reverses the zero-`at` panic added in `2759116`. `Schedule` always sets
`admitted=true`; a non-zero `at` also creates the heap entry, a zero `at`
does not. This is the proper replacement for the `maxFlushAllSkew`/
`drainAllSkew` 24h placeholder — no fake deadlines, no heap churn for
no-deadline accumulators, smaller heap (= smaller contention surface).
`Expedite(w)`: read `admitted` (panic if never admitted); if it has a heap
entry, remove it; push to `fresh`. `forceAll` = `Expedite` over the live
set, heaped or indefinite alike. The far-future placeholder, `DrainAllScheduled`,
and the whole-queue end-of-work sweep all go away.

**(c) lifetime model — the spine.** Today `queued` doubles as the
"heap-membership ref held?" gate, the ref-drop is deferred to `Free`, and
`funnelInstance.free()` couples object-recycle with `op.instanceCount--` +
`op.unref()` at the *discard-pop*. That coupling is the bug: a flushed
**spent shell** lingers in `instanceQueue` (an nbcq — no mid-queue removal)
holding the creation `op.ref()`, so the op's refcount never reaches 0 and
the op **silently leaks** (`factory.Close()` never runs); nothing is
guaranteed to pop it after the last `flushAll`. New model:
- **One ref per instance, taken right after the factory call** (creation).
  No conditional schedule-time `Ref`, no per-heap-entry ref → nothing for a
  reschedule-vs-drain race to double-drop. The `queued` flag disappears.
- **Op-liveness (`instanceCount` + `op.unref()`) drops at flush**, not at
  discard-pop — flush is when the instance stops being a live accumulator.
  So the op can reach teardown even with spent shells still cached.
- **Object recycle (`pool.Put`)** happens on reuse-pop *or* at teardown.
- **`funnelOp.unref()` (op teardown) DRAINS `instanceQueue`** — pop all,
  assert each `accumulator == nil` (spent), drop them — instead of
  asserting the queue empty. The queue is a reuse *cache* the op cleans up,
  not a refcount the world must drain.
- **Reschedule check under `delayq.mu`** so no heap entry lingers: funnel's
  reschedule, after `Accumulate` returns, takes `delayq.mu` and re-adds
  only if the instance is still schedulable; if it was already drained, it
  skips the re-add (the accumulated data flushes via the pending `Execute`).
  This is safe now precisely because (a) makes `c.mu → delayq.mu` the only
  cross-order (nothing takes `delayq.mu → c.mu`), so it's acyclic. (The
  alternative — a generation/`ID` weak-ref guard on pooled-instance reuse,
  per the omnipool TODO — is the fallback if the synchronous reschedule's
  `delayq.mu` contention proves costly; reschedule-check is the default.)

**Naming (this session):** `funnelInstance.funnel()` → `accumulate()`;
`*Work.Funnel`/`boundFunnelWork.Funnel` → `Dispatch`. Defer the broader
combiner-era renames (`executeFunnel`, the `cp`/`cw`/`c*` "combiner"
prefixes, etc.) to a final name-reconciliation pass.

**Already in WIP toward this:** the `ScheduledWorkItem` embed (a step to
`ScheduledState`) and the `flushAll` orphan-hang fix. Both stay; the embed
evolves into `ScheduledState`.

**Validation gate:** full `TestBySimulation -race`, run *many* times (the
bug is intermittent), must be **green** — this is the first time it
legitimately can be, since the design fixes the pre-existing deadlock. Plus
`-short -race`, maxholdtime/funnel/skim, and no-leak at end-of-work. Use
the reduced-variability sim config + `PSGTRACEINTERNALS` if anything
regresses.

#### concern (c) RESOLVED — lifetime model pinned down (2026-06-07)

Tracing the code to start sub-step 1 surfaced two facts that revise the
plan:

- **Sub-steps 1, 2 and 4 are inseparable.** Swapping `delayq.Item` to
  `ScheduledState() *ScheduledState` makes the heap write `state.position`
  directly under `delayq.mu`, which structurally removes
  `funnelInstance.SetPosition` — but that override's `if p<=0 { queued=false }`
  is the *only* drain-signal clearing `queued` today, and `queued`'s clean
  removal *is* the new lifetime model. So the interface swap, the deadlock
  fix, and the lifetime model land as **one** change; none is green alone.
- **The validation gate is flaky until the deadlock is fixed**, so trust the
  `-race` sim only *after* the combined change lands.

**Parties holding an instance pointer:** A = in `instanceQueue` (reuse
cache); C = being processed by a `funnelWork` (Accumulate, holds `c.mu`);
B = a `delayq` heap entry; D = drained-from-heap, running
`funnelInstance.Execute` (deadline flush, holds `c.mu`). A and C are one
lineage (an instance is in the queue *or* being processed, never both); B
and D are the delayq lineage.

**Two rules make the model safe without a per-instance refcount:**
- **R1** — C never flushes/reschedules an instance whose `Position ==
  removed(-1)` (D already drained it); C accumulates and pushes back **live**,
  letting D's pending `Execute` flush the freshly-accumulated data. Because
  C's synchronous `Remove` and D's `Drain` serialize on `delayq.mu`, exactly
  one of them claims the flush — never both.
- **R2** — D captures `op := c.op` under `c.mu` and **never touches the
  instance object after `c.mu.Unlock`** (only op-level atomics). An owner
  reuse-pop can then recycle the spent shell concurrently with D finishing,
  race-free.

**Answers to the four questions:**
1. **`refCount` does NOT survive** — the per-instance `refCount` integer and
   the `queued` flag are both eliminated. Liveness = `accumulator != nil`;
   the design's "one ref" is the op-level `op.ref()` taken at creation,
   dropped at flush (op-liveness).
2. **Recycle (`pool.Put`) is owner-only, exactly once:** reuse-pop (spent
   shell from `instanceQueue`) or teardown (`funnelOp.unref` drains
   `instanceQueue`). Single nbcq popper guarantees once. D never recycles.
3. **The Drain-pop vs re-funnel double-unref is unreachable** in the new
   model: synchronous delayq ops give C-remove/D-drain mutual exclusion, R1
   makes C defer when D won, and with no per-instance refcount there is
   nothing to double-drop.
4. **Reschedule-check** = a synchronous `delayq` method (`Reschedule(item,
   at) (added bool)` + admit/remove variants) run under `delayq.mu` while
   holding `c.mu`; folds updates, reads the authoritative tri-state
   `Position` (0 = admitted/never-heaped, >0 = heaped, -1 = drained), re-adds
   only if not drained. Sole cross-order `c.mu→delayq.mu`, acyclic.

**Revised sub-step order:**
1. **Combined foundation + deadlock fix + lifetime** (was 1+2+4): introduce
   `ScheduledState` and swap the interface; remove `funnelInstance.SetPosition`
   (position is `delayq.mu`-only — the deadlock fix); retire `queued` and the
   per-instance `refCount`; one creation `op.ref()` dropped at flush;
   synchronous `Reschedule`/`Remove` under `delayq.mu` with R1/R2;
   `funnelOp.unref` drains `instanceQueue`. **Gate: full `-race` sim green.**
2. (b) `admitted` atomic + `Schedule(w,0)` indefinite + `Expedite` admission
   check; reverse the zero-`at` panic; delete the placeholder + skew consts.
3. live set + `forceAll` (Expedite over the set); retire synchronous
   `flushAll`/`Flush`/`scheduledFlusher`/`DrainAllScheduled`.
4. naming: `accumulate()` / `*Work.Dispatch`.

**Original sub-step order (superseded by the above; kept for history):**
1. `ScheduledState` + `ScheduledState()` interface swap (workq/delayq/heap);
   embed in `funnelInstance` (extends the WIP embed). Behavior-neutral.
2. (a) `SetPosition`/position → `delayq.mu`-only; drop `queued`'s
   drain-signal role. *Deadlock fix* — validate hard against full `-race`.
3. (b) `admitted` atomic + `Schedule(w,0)` indefinite + `Expedite` admission
   check; reverse the zero-`at` panic; delete the placeholder + skew consts.
4. (c) lifetime: one creation ref, op-liveness-at-flush, `funnelOp.unref`
   drains `instanceQueue`, reschedule-check under `delayq.mu`. Retire the
   `queued` flag entirely.
5. live set + `forceAll` (Expedite over the set); retire synchronous
   `flushAll`/`Flush`/`scheduledFlusher`/`DrainAllScheduled`.
6. naming: `accumulate()` / `*Work.Dispatch`.

- **1c** — Relocate flush *policy* to Wave AND parallelize the end-of-work sweep.
  Two coupled deliverables:

  1. **Unified drain barrier: a pending timed item *is* outstanding work.**
     Replace the bespoke `confirmEndOfWork` dance + `RegisterFlusher`/
     `nextFlushChan` with a single accounting where outstanding = regular work +
     pending timed (flush) items; `Done` ⟺ outstanding == 0. This is the
     keystone: it makes `Done` wait for every flush regardless of which worker
     runs it, and ensures **no worker exits while forced flushes remain**.

  2. **Parallel end-of-work sweep (retire the serial `flushAll`).** The current
     synchronous `flushAll` serially calls an *unbounded* number of user
     `accumulator.Flush` functions on one goroutine — a tail-latency landmine
     (pre-existing; 1b-ii preserved it). With barrier (1) in place, the
     async-to-`fresh` direction reverted in 1b-ii becomes *safe*: at quiescence
     of regular work (Wave Closed, no in-flight regular work), **`forceAll`
     Expedites the Wave's pending instances** (queues each as ready now — see the
     Schedule/Expedite split below) so they run through the normal parallel
     ready→`Execute` path, fanned out across the whole pool instead of
     serialized. The deadline-driven path is already parallel; this brings the
     forced sweep in line.

  **Multi-cycle flushing is intrinsic and stays.** A flush can emit downstream
  and create new funnel input (cross-hop / recursive), which creates new
  accumulator state needing its own flush. So flushing is a fixpoint: force →
  flush → maybe new work → drain → force again, until outstanding (work +
  pending flushes) hits zero. 1c doesn't remove this; it makes it fall out of
  the unified barrier (refcount→0) rather than the re-confirm loop. Cycles are
  bounded by dataflow depth; terminates iff the user dataflow terminates (same
  as today).

  Plus the ownership move: `WithFlushListener` → Wave; force-flush ownership
  (Remove + run handles) on Wave. The synchronous `flushAll` (b9dbf2d) is the
  correct *interim*; 1c is the destination.

  **1c implementation design — REFINED, fresh-session-ready (PN, 2026-06-06).**
  **[SUPERSEDED 2026-06-07 by "1c-ii CONSOLIDATED DESIGN" above — kept for
  history. This predates the pre-existing-deadlock discovery; its live-set/
  `Expedite`/`queueFresh`/per-instance-barrier framing is reframed there
  around the deadlock fix and the three-orthogonal-concerns decomposition.]**
  Decision (a): land the correct per-instance barrier + force-via-normal-path
  now; full sweep parallelism arrives with checkpoint 2/3 demand-driven
  spawning. Structure-for-parallel is the goal. The first 1c attempt was reset
  (back to `b9dbf2d` code) because three nuances reshaped it mid-flight; they're
  all captured below so a fresh session can implement it in one clean pass.

  **(i) Per-instance-lifetime barrier reference.** A `funnelInstance` holds one
  job/Wave reference for its whole life as a live accumulator: creation →
  flush. Acquire in `funnelWork.Funnel`'s new-instance branch (next to
  `op.ref()`): `job.state.IncrementReference()`. Release in
  `funnelInstance.flush()` after the real `accumulator.Flush` returns, via
  `defer` (so a panicking Flush still releases). NOT in `free()` — `free()` is
  lazy (a spent instance lingers in instanceQueue until a next pop that may
  never come → would hang). `flush()`'s `accumulator==nil` early-return makes
  the real flush (and the release) run exactly once. This replaces
  `RegisterFlusher`'s per-goroutine ref entirely; barrier is purely per-instance:
  `Done ⟺ Closed ∧ totalReferences==0` (work refs via IncrementWork +
  live-instance refs). Ordering: the flush's emit acquires its work ref *inside*
  `accumulator.Flush` (Submit→IncrementWork) before the instance ref releases,
  so totalReferences can't transiently hit zero across an emitting flush. On
  cancel refs leak, but doneChan isn't the cancel sync point — consistent with
  today's flusher refs. (jobstate: add `IncrementReference`/`DecrementReference`;
  make the flush signal a no-ref `FlushChan()`; drop `RegisterFlusher`.)

  **(ii) Wave-held live-instance set (force enumeration).** `FunnelPool` holds
  the set of its live (unflushed) instances — exactly the funnels it may force
  at end-of-work. Add at creation, remove in `flush()`. Reasons (PN): avoids
  scanning every Wave's funnels to flush a few, and keeps `workq` ignorant of
  the Wave/grouping concept. Generics wrinkle: `funnelInstance[T]` is generic but
  `FunnelPool` isn't, so the set holds the non-generic `workq.ScheduledWork`
  (start with `map[ScheduledWork]struct{}`+mutex; an intrusive list is the later
  allocation optimization — but note the lock-order/lifecycle care: snapshot or
  hold the set lock across the force loop, and force only calls Expedite which
  takes no instance lock).

  **(iii) Schedule vs Expedite — the backpressure split (KEY).** Two distinct
  timed-queue operations with different rules:
  - **`Schedule` (admit NEW timed work)** can grow outstanding work, so it MUST
    be backpressure-controlled: available only *within the controlled ExecuteOne
    flow*, never an exported `Accepted` method. Exporting it is a backdoor —
    arbitrary code could inject unbounded work past the governor. Grant it like
    `queueFn` (a capability, not a public method).
  - **`Expedite` (queue an already-scheduled item as ready NOW)** — it does NOT
    lower the item's deadline (that would still wait for a drain pass). It
    *removes* the item from the timed queue and hands it to the **ready (fresh)
    queue immediately**, so the next worker runs it. Admits nothing new (the
    item already passed admission at `Schedule`) ⇒ backpressure-neutral ⇒ safe to
    call anywhere, including `forceAll` from the dance (outside `AddWork`). May be
    a public method.
  - **`Expedite` PANICS if the item was never scheduled / already done** — a
    defensive contract assertion, not a silent no-op. Subtlety: a worker can
    *concurrently drain* the item (timed→ready via `drainTimed`) between
    `forceAll` selecting it and `Expedite` running; that's benign (it's already
    on its way) and must NOT double-enqueue or panic. So distinguish: never
    scheduled / already flushed → **panic**; scheduled-but-just-drained → no-op.
    Needs an atomic check-and-move against the timed structure plus a per-instance
    "scheduled" indicator (the currently-scheduled set membership, not mere
    heap-presence). Respect the `delayq.mu → c.mu` order (`Expedite` not called
    under the instance lock; `forceAll` not holding the set lock while calling a
    delayq op — drain removes under `delayq.mu→c.mu`, so that ordering would
    cycle).
  - **Ready-enqueue must carry the same spawn/bookkeeping** the controller's
    `drainTimed` promotion uses (push via `queueFresh` so `workAddedCount` →
    `unmetDemandFn` spawns a worker → parallel sweep). Reconciling that with
    `forceAll` running *outside* the controller (no live `queueFresh`) is an open
    implementation point — e.g. `Expedite` fires the pool's spawn notify itself,
    or hands ready items off through a controlled path.
  - `forceAll` = `Expedite(c)` over the Wave's currently-scheduled set.
  - delayq roles: `Schedule` = add/replace (admission); `Remove` =
    drop-if-present. `Expedite` is an `Accepted`-level op = confirm-scheduled
    (panic if never) + `delayq.Remove` + ready-enqueue — NOT a delayq deadline
    mutation.

  **(iv) Schedule-capability context (the forceAll-not-in-AddWork nuance).**
  `forceAll` is reached from TWO contexts: the `confirmEndOfWork` dance (loop
  body, *outside* `AddWork`) and the popSelect signal-followup (*inside*
  `AddWork`). Expedite is safe in both (no admission). But `Schedule` (admission)
  must be within the controlled flow — and funnel flush-scheduling happens during
  the funnel work's *Execute* (accumulate→deadline), which is within `ExecuteOne`
  but NOT literally `AddWork`. So grant the Schedule capability for the
  controlled `ExecuteOne` flow (AddWork + Execute, e.g. via the `Execution` /
  exec-env), not as a bare `AddWorkFunc` parameter (which wouldn't reach Execute
  or the dance). The capability itself is stateless (Schedule just enqueues to
  the timed delayq; backpressure is the governor wrapping `ExecuteOne`).

  **(v) Promote timed→fresh through `queueFresh` (parallelization key + the real
  backdoor).** `drainTimed` must promote due items via the controller's
  `queueFresh` (which bumps `workAddedCount` → fires `unmetDemandFn` to spawn a
  worker), NOT a direct `q.fresh.PushBack`. The direct push was the actual
  admission backdoor: it bypassed the spawn-trigger bookkeeping, so forced
  flushes would never spawn workers — defeating decision-(a)'s parallel sweep.

  **(vi) Lost-wakeup race — `shouldStillWait` must re-check timed.**
  `Waiters.Notify` drops the wake if no inbox is parked (`TryPushBack`→false,
  verified), so a `Schedule`/`Expedite` that races a worker entering its wait
  loses its notification. The confirm-protocol backstop (`shouldStillWait`)
  currently re-checks only fresh/postponed — so timed work added in that window
  is missed → potential hang. Fix: `shouldStillWait` also re-drains timed (due
  items → fresh, caught by its existing `TryAccepted`) and aborts the wait if the
  next deadline is now sooner than what the wait's timer was armed for (add
  `controller.armedTimedDeadline`, set in `WaitForNew`). Mirrors the fresh-work
  backstop.

  **(vii) Retire** synchronous `cpWorker.flushAll` + `funnelInstance.Flush`
  (uppercase combined). Single flush path = `Execute`(flush)+`Free`(unref);
  `flush()` also removes from the live set + releases the barrier ref.
  flush-Execute is not a poolWork → doesn't touch inFlightWork; the per-instance
  ref holds the barrier.

  **(viii) Goroutine loop / triggers.** Keep the `confirmEndOfWork` dance and the
  job flush signal (`noMoreWork` already re-fires it each time `inFlightWork`
  returns to zero → multi-cycle works). Swap actions: dance → `forceAll`
  (Expedite each live instance) then `continue` to pump the now-due flushes;
  signal-followup → `forceAll`; `executeFunnel` subscribes via the no-ref
  `FlushChan()`.

  **Validation:** `TestBySimulation` (`-short`, full, `-race`) is the safety net
  for the barrier/force — it caught the 1b-ii async-sweep lost-flush bug. Also
  `TestMaxHoldTime*`, funnel/skim, end-of-work no-leak.

**Open design question for checkpoint 3**: the post-cap-deletion funnel spawn
policy. Funnel wants *minimum* goroutines, so it can't adopt the task pool's
aggressive chain verbatim. Candidate: keep "ensure ≥1 goroutine on post" (the
`ShouldSpawnFirstGoroutine` role) as the floor, and add a genuine excess-work
scale-up trigger (since `unmetDemandFn` doesn't fire) — e.g. spawn when a post
can't hand off AND queue depth exceeds live goroutines. Needs validation against
`TestBySimulation` (run both `-short` and full, plus `-race`).

### Next session pickup (in rough priority order)

1. **Pool / workq consolidation** — see "Pool consolidation — foundational
   analysis (2026-06-06)" above. Checkpoint 1 progress: **1a, 1b-i, 1b-ii,
   rename, and 1c-i (per-instance barrier ref) are DONE** (committed). 1c-ii
   foundation (`6df4218`) + `delayUntil→at`/`timed→scheduled` rename (`2759116`)
   committed. **NEXT = finish 1c-ii per the "1c-ii CONSOLIDATED DESIGN
   (2026-06-07)" section above** — the design that fixes the **pre-existing
   deadlock** (bisected to `63a4d57`, a test-only sim commit) via three
   orthogonal concerns: heap-position under `delayq.mu`; atomic `admitted`
   (+ `Schedule(w,0)` indefinite, reversing the zero-`at` panic); per-instance
   lifetime ref with op-liveness-dropped-at-flush + `funnelOp.unref` draining
   `instanceQueue`; then live-set/`forceAll`; then `accumulate()`/`Dispatch`
   renames. **CAUTION:** the full `-race` sim was never reliably green
   (intermittent pre-existing hang); it becomes the gate only after the
   deadlock fix. Uncommitted WIP in the tree (`ScheduledWorkItem` embed +
   `flushAll` orphan-hang fix) folds into sub-steps 1 and 5.
   After 1c: checkpoint 2 (delete funnel `maxConcurrency` → demand-driven) then
   3/4/5.
2. **Thread C completion** — Try* honoring non-zero non-Forever deadlines via bounded-wait. Falls out of the consolidation; pick up the `Forever` sentinel and `dispatch (bool, error)` foundation from `5dc49c7`.
3. **psgwf legacy-name retirement** — `psgwf.GenericTaskRunner` and friends still use pre-rename vocabulary. Done as a stand-alone pass or rolled into a broader psgwf migration.
4. **bench.txt regeneration + chartgen alignment** — re-run benchmarks under the new metric names (`funnelLimit` instead of `combinerLimit`), then update chartgen to parse the new names. Required before the legacy bench file can come back online for chart generation.
5. **CombinerPool retirement** — once Pool consolidation lands, the CombinerPool→FunnelPool transitional name can go away. Stand-alone follow-up if not folded into the consolidation pass.

## Open issues

### Deadline propagation in taskPostWork

`taskPostWork.newTaskPostWork()` receives a `deadline` parameter but doesn't store or use it. Sibling scatter work types (`taskPoolScatterWork`, `combineScatterWork`, `gatherScatterWork`) store and use theirs. Should add a `deadline time.Time` field and pass it to `BasicPushSelect` via context.

### Renotifier lifecycle (`wrappedRenotify` only now)

`rdvq.RenotifyFunc` is a bare `func()` with no `Free()`. After the orphan elimination, the only remaining workaround instance is `wrappedRenotify` in `internal/rdvq/notifier.go`, which self-frees inside its renotify callback — works only if the renotifier is invoked, leaks if it's replaced or discarded. Long-term: change `RenotifyFunc` to a `Renotifier` interface with `Renotify()` and `Free()` so the rdvq infrastructure can free unused renotifiers in all cases. Less urgent now that `orphanedTaskRenotify` is gone — only the rdvq-internal one remains.

Files affected: `internal/rdvq/notifier.go`, `internal/rdvq/waiters.go`, all `Notify()` callsites.

### ExecuteOrWait duplication

`taskPostWork.Execute` implements ~80 lines of try/subscribe/block logic that overlaps with `workq.ExecuteOrWait` and `workq.Governor.Execute`. It has unique requirements (custom `TryPushBack`, demand-registration side effects, blocking via `PushBackFunc` + `BasicPushSelect`) so it isn't a trivial extraction. Possibly worth a `TryPostBehavior` abstraction if other places grow similar shape, but not urgent.
