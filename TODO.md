# TODO

## Refactor status (2026-06)

The combiner branch has progressed substantially beyond its original scope. See `WORKING_NOTES.md` for the live status of the in-flight reshape. Highlights of what's complete on the branch as of `aab904c`:

- Thread A: Handler[T] unification + op trio rename (`Gather`→`Skim`, `Combiner`→`Funnel`, `TaskRunner`→`Launcher`) + Launcher arity collapse + Submit/SubmitErr/SubmitResult dispatch family.
- Thread B: wave-at-construction with nil-sentinel resolution + worker plumbing so nil-wave ops dispatch correctly from inside any op body + factory pass through psgwf / otpsg wrappers.
- psgfn folded into top-level psg; AccumulatorFactory is an interface with `Close() error`; full convenience constructor surface (`NewFn*` / `NewTask*` / `NewErr*`) per op; void-T type aliases.
- Thread C v0.1: `Forever` sentinel + dispatch `(bool, error)` refactor. Full Try* honoring deadlines deferred pending Pool/workq consolidation.

The next major piece is **Pool/workq consolidation** (merge TaskPool + FunnelPool into one Pool, rationalize workq integration). Thread C completion falls out of that pass.

The sections below are the original pre-refactor TODO. Many items are now stale or superseded; treat them as historical reference and consult WORKING_NOTES + CHANGELOG for current scope.

## Limiter / livelock investigation follow-ups (2026-06-14)

- **`acquireOrWait` error-path latent permit leak (defensive — NOT the proven
  livelock cause).** In `acquireOrWait`'s block loop, `if err != nil { return
  false, err }` discards a permit that `confirmFn` may have already latched
  (`b.held`). If `blockFn` (`Pool.block`) ever returns a real error
  (`ErrJobDone` / ctx cancel) coincident with a `confirmFn` grant, the caller
  (`limiterScatterWork` / `funnelWork`) returns on the error without running the
  gated work and is requeued (not freed), so the handle's `Free` release
  backstop never runs and the HELD permit leaks. **This is NOT what fails the
  `TestBySimulation -race` gate** — disproven 2026-06-14: a log-only variant
  (postpone-on-latched-error disabled, branch instrumented) hung 4× with
  **zero** occurrences of the latched-error branch, so it never fires in the
  repro. But it's a real latent hazard; close it defensively (postpone the
  latched permit before returning the error) once the actual livelock fix
  lands. See WORKING_NOTES for live root-cause status.

- **Rename `ErrJobDone` → `ErrWaveDone`.** Legacy "Job" vocabulary; the
  user-facing sink is now a Wave. ~12 usages (errs.go, funnelpool.go,
  limiter.go, job.go doc comment). Fold into the broader Job→Pool/Wave naming
  reconciliation with the other deferred combiner-era renames.

## Combiner Branch Pre-Merge Tasks (original list — partially stale)

Items to complete before merging to main branch.

### 3. Documentation updates
- Complete review and update of doc comments for all new/modified public APIs
- Update README with information about the new combining architecture
- Add a Combiner example to the README Features section
- Create a playground example for the new combining architecture
- Ensure that examples do not use any internal packages (e.g. exmpclk)

### 5. API finalization
- Review and document thread-safety guarantees for remaining public APIs
- Add a way to force creation of a new work group
- Maybe remove psg prefixes from psg-go subfolders, but leave the prefixes in the package names?
- should combiner concurrency limits be specified per-combineop instead of or in addition to the combiner pool?

### Wave 3 follow-ups
- **Migrate the combiner throughput benchmark to the Wave 3 API.** The
  pre-Wave-3 benchmark in `combiner_test.go` was lifted out to
  `combiner_legacy_bench_test.go` behind the `psg_wave3_legacy_bench`
  build tag because the value-returning Task shape is gone. Per
  REFACTOR_PLAN.md (combiner-benchmark requirements session), this
  needs a dedicated design pass — what metrics we still want to track
  in the new model — before being brought back online.

### 6. Implementation improvements
- **Change `rdvq.RenotifyFunc` to a `Renotifier` interface** so the infrastructure can free pooled renotifier objects in all cases (not just when invoked). The remaining workaround is in `wrappedRenotify` (rdvq/notifier.go) which self-frees inside its renotify callback — works on invocation, leaks on replacement/discard. Less urgent now that the orphan task queue and `orphanedTaskRenotify` are gone. See WORKING_NOTES "Renotifier lifecycle". Files: `internal/rdvq/notifier.go`, `internal/rdvq/waiters.go`, all `Notify()` callsites.
- **Add deadline field to `taskPostWork`** and use it in the blocking post path. Currently `newTaskPostWork()` receives the parameter but doesn't store or use it; sibling scatter work types do. See WORKING_NOTES "Deadline propagation in taskPostWork".
- **Make sure that calls to Gosched are interleaved with checks for deadline/cancellation**
- **Consider refactoring `taskPostWork.Execute()` to reduce duplication with `workq.ExecuteOrWait`** — about 80 lines of similar try/subscribe/block logic. Has unique requirements (custom TryPushBack, demand-registration side effects, blocking via PushBackFunc + BasicPushSelect) so not trivial. Evaluate if a `TryPostBehavior` abstraction is worth the complexity. See WORKING_NOTES "ExecuteOrWait duplication".
- Improve detection of top-level vs. child tasks to prevent adding new top-level tasks after Close() (use ctxMeta to allow new scatters only to finish workflows already started)
- Refactor otpsg module to build on psgwf workflow context propagation instead of directly on core psg
- consider removing combiner goroutines' doneCh and dedicated goroutine now that select on it happens only in the slow path
- profile (memory, cpu, blocking) again after all the recent refactoring, see if there are any more obvious targets or low-hanging fruit
- review again for readability
- make sure all exported functions emit trace regions
- reorganize code within large files like job.go
- re-review tracing guidelines in DEVELOPMENT.md
- figure out what to do about trace.IsEnabled everywhere (if, how)
- review and understand processing and waiting aggregation throughput and speedup graphs
- enable cyclo and fix issues
- can we integrate taskWork into combineTask and gatherTask?
- rename Free to Recycle, add Recycler interface from which other things can derive
- Expose all user code integration points as interfaces with Recycle (Recycler), then add convenience functions that use pooled objects to wrap implementation-by-closure; perhaps reserve psgfn for the convenience functions and add a separate package for the integration interfaces?
- Make sure job.governor is really necessary
- Add Deadline to workq.Execution and make sure that it's set and respected everywhere, especially when blocking
- Abstract logic in *PostWork and perhaps make it extend from workq.ExecuteOrWait?
- Consider an addition to omnipool to codify the pattern in which a monotonic ID field is used to guard against reuse of a object that has already been pooled.  it's a form of weak reference.
- Always return context.Cause(ctx) instead of ctx.Err()

## Post-Merge Enhancements

Items that can be deferred to GitHub issues after the combiner branch is merged.

### Implementation improvements
- fix LockAndSetQueueFunc ugliness
- fix addWork ugliness
- fix inconsistencies between refcounting (and pooling) implementations: semantics re locking, naming, etc.
- consider whether any atomic.Int64s should instead be atomic.Int32 (e.g. InFlightCounter, concurrency tracking in sim/run.go)
- consider whether to use hierarchical timing wheels to avoid O(log n) heap overhead of go-native Timers, esp. for Flush.

### Performance Optimizations
- Explore operation affinity for worker goroutines to improve cache-line efficiency (generalizes the older "combiner goroutine ↔ combiner instance affinity" idea to all op types). Motivation sharpened by the ultrapool analysis (ARCHITECTURE_COMPARISON.md §6): its random shard pick deliberately trades cache locality for load spreading; psg could plausibly get both. Today rdvq's LIFO inbox stack gives *worker-temporal* locality (the most recently active worker picks up the next item) but is op-blind — work from different ops interleaves through the same queues. The affinity key already exists: every `workq.Work` carries a `GroupID` (`internal/workq/work.go`), and requeue ordering already compares groups (`accepted.go`); what's missing is a pickup policy that prefers same-group work per worker, with a fallback so affinity never starves throughput. There are several candidate affinity dimensions to weigh, not one: per-instance (the original funnel idea — accumulator state is the hottest win), per-op, per-group (`GroupID` is the key already plumbed), per-wave, and key-based (would compose with the "keyed combine and reduce" enhancement below). Dimensions can conflict with each other and with LIFO scale-down; choose by measurement, not principle. Sequence after the Pool/workq consolidation — the pickup paths are exactly what that pass restructures.
- Adapt psg to ultrapool's cross-library benchmark suite (`maurice2k/ultrapool` `benchmark/`) — ready-made fire-and-forget workloads with adapters for ants/pond/gammazero/fasthttp already written; supplies the cross-library numbers ARCHITECTURE_COMPARISON.md calls for before the README comparison table is published. Extend with latency percentiles (P99/max are primary; the suite measures throughput only). See POSITIONING_RESEARCH.md "Addendum: ultrapool".

### API Enhancements
- Evaluate an `iter.Seq`/`iter.Seq2` interop surface for result consumption (e.g., ranging over skimmed results). The 2026-06-12 competitor sweep (POSITIONING_RESEARCH.md "Competitive landscape sweep") found Go 1.23 iterators becoming the result-streaming substrate across new entrants (rill, firetiger-oss/concurrent, samber/lo) — the most likely leapfrog vector if psg lacks an interop story. Design direction (PN, 2026-06-12): a natural layer built over a Skimmer — range-over-func iterators are push-style (the loop body is the `yield` callback) and a Skimmer's handler is already a serialized push callback, so the adapter is nearly shape-preserving (handler → yield; `iter.Seq2[T, error]` for the error flow). The design crux is the early-termination contract: what breaking out of the range loop means for the wave (stop consuming vs. cancel vs. drain) must be pinned down explicitly.
- Consider adding helper methods for common combining operations (e.g., counting, grouping, mapping)
- Add keyed combine and reduce functionality
- Consider making it possible to positively close and release task and combiner pools without shutting down the overall job?
- Add generic hooks in core PSG for key lifecycle events
- Add metrics hooks for pool resource utilization (in-flight tasks, queue depth)
- Add hooks for job-level monitoring and statistics
- Create standard interfaces for instrumentation providers
- debug mode that runs everything in a way that makes logic easy to debug, ideally in a single goroutine
- consider publishing generally-useful internal packages as standalone projects
- consider adding environment variable-based configuration of PSG default tuning parameters 
- consider adding https://github.com/glycerine/gown annotations and supporting Gown analysis of application code

### Additional Tests and Examples
- Investigate and fix workflow cancellation test flakiness in psgwf (observed timing-dependent failures in example tests)
- Make sure that combiner pools scale down to zero
- Test automatic flushing behavior based on timeout settings somewhere other than just benchmarks
- Test TaskPool.SetOptions and CombinerPool.SetOptions functionality, especially dynamic pool resizing
- Ensure no goroutine leaks in any scenario
- Test and ensure correct ongoing behavior when user code recovers from panics that propagated through the framework
- Test edge cases with cross-job context propagation
- Add tests verifying proper shutdown sequence and resource cleanup
- Test multithreaded gathers

### Design Documentation
- Update and refine design docs to make them more readable and less AI-fueled dumps of bullet points
- Add documentation that compares rdvq.Waiters, workq.Watchers, and workq.Waiters with condition variables
- Better establish the theoretical basis of notification conservation and find a way to measure and verify it (the "Formal verification & foundations" items below are the concrete follow-through on this)

### Formal verification & foundations

Defer until after the Pool/workq consolidation lands — formal models rot against a moving design, and these protocols are exactly what that pass restructures. Ordered by ROI.

- **Write up the invariants + happens-before contracts as adversarial prose first** (cheapest, survives refactors better than a model, often finds the bug before any tool). Two highest-value targets:
  - rdvq's register → recheck (`confirmFn`) → block → notify path. Enumerate every interleaving of the race window flagged at `internal/rdvq/queue.go:339` (confirmFn consuming an outbox value while a parallel sender pushes) and argue no item is lost and no waiter blocks forever. Files: `internal/rdvq/queue.go`, `internal/rdvq/waiters.go`.
  - delayq's CAS-min `nextDeadline` with pre-snapshot republish — the "Schedule lowers the deadline between my snapshot and my CAS, so the loser re-surfaces on next Drain" argument. Files: `internal/delayq/delayq.go:172-185, 285-290`.
- **One small TLA+/PlusCal (or Spin/Promela) model of the wakeup protocol**, checking deadlock-freedom + "every pushed item is eventually received." Keep it abstract — model the protocol, not the Go. Be explicit that this proves the *design*, not the binary: translation fidelity and Go's happens-before/weak-memory semantics are the two gaps (default TLA+ assumes sequential consistency).

#### Ranked verification risk targets (where a proof is most likely to surface something or fail to close)

Findings from an adversarial read on 2026-06-06. NB: no concrete bug was proven — these are ranked *risk* assessments, ordered by where to spend verification budget first. Some overlap with the items above/below; treat this as the prioritized "where to look" index.

1. **`aptr.go` `np`/`a128` reconciliation — bespoke, off-paper, highest risk** (`internal/nbcq/aptr.go:45-82`). Two separate obligations, both resting only on a comment:
   - *Progress*: `Load` spins until `addr(np) == a128[0]` and relies on `updateNodePtr` driving `np` to converge to `a128`. Traced interleavings converge (live updater whose `newPair == a128` retries; stale updaters bail on `currentPair != newPair`), but this is exactly where a model checker should confirm no livelock / stuck-`np` interleaving exists. Don't trust it without one.
   - *GC keep-alive*: the invariant "a node held in `a128` only as a raw `uintptr` is also kept alive by `np` or a live `pointer[T]` local" is invisible to the type system; the `:71-72` comment is the entire proof. A refactor that returns from the break path while the node is reachable only via the uintptr is a use-after-free **the race detector will not catch** (reachability bug, not a data race). Existing item below ("Targeted `-race` stress on the aptr.go GC-shadow-pointer seam") is the cheap first cut; this is the case for going further.
2. **Renotify conservation discharge — most likely to fail a *no-deadlock* proof** (`internal/rdvq/notifier.go:49-56, 89-94`). `wrappedRenotify` only fires `wrappedFn` / returns to pool **when `renotify()` is actually invoked**. Benign reading = pool leak (already filed under "Implementation improvements"); malignant reading = if an enqueued listener entry can be discarded without invocation, a real wakeup is **lost** → deadlock. The "if I don't consume, I re-propagate" obligation is *conditional on invocation here*, not unconditional. Open question to settle: is the discard/replace path actually reachable? This is the author's own acknowledged soft spot (`WORKING_NOTES.md:736`) — strong signal.
3. **delayq `wake` ↔ park handshake — cross-module, adversarial-scheduler-sensitive** (`internal/delayq/delayq.go:179-181, 194-196, 285-291` + workq park side). delayq's CAS-min republish is internally clean, BUT in the Schedule-during-Drain race `republishNext` returns the heap min, which can be *later* than the deadline a concurrent `Schedule` just installed in the atomic; the caller arms its timer off that returned value. Correctness then depends entirely on `Schedule`'s `wake()` reaching a worker before it commits to sleeping on the stale-late timer. delayq explicitly punts this ("may or may not be observed… call Drain again"), so **the no-lost-wakeup obligation actually lives in workq, across the wake/park boundary** — classic notify-before-park, split across two modules. Also: `Yield`'s unconditional `Store(MinInt64)` (`:300`) clobbers a concurrent CAS-min — benign for its purpose but widens the interleaving space.
4. **ABA-tag axioms spanning multiple sites — won't "fail," but can't be claimed "proven" without stating them** (`internal/nbcq/nbcq.go:52-55, 75-78, 185-194`). The per-node `next.count` reuse trick deviates from the textbook freelist: `next.count` is **never reset** across pool reuse and the safety claim needs two explicit axioms a checker would force you to state — *never reset* and *never wraps (uint64)*. Both practically unbreakable, but "never reset" is one well-meaning `Reset()` cleanup away from silent breakage. **Action: add guard comments at all four sites** (Init, Reset, D19, and the omnipool-reuse assumption) tying them together so the invariant isn't accidentally severed; and write any eventual theorem as "correct assuming no 64-bit wrap."
5. **rdvq `PopFrontFunc` at-most-one-value invariant — provable, but has a track record** (`internal/rdvq/queue.go:312, 318, 336-342`). The `panic` guards assert `ok` is set at most once across `confirmFn` / `processOrphanFn` / `selectFn`; the waitInbox-before-stack-inbox registration flip is what makes double-delivery impossible. Probably correct as written, but this is the exact spot that already produced a real dropped-notification bug (`WORKING_NOTES.md:59-61`) — delicate enough to model-check rather than trust. (Overlaps with the adversarial-prose item above.)
- **Make the "notification conservation" claim defensible** (currently the boldest unproven foundation; see `docs/backpressure-and-reentrancy.md:469-544`). The doc states it as one invariant but it's really a conjunction of three, and only the first is argued:
  1. *No loss* (safety) — an actionable wakeup is never dropped. The conservation primitive is `if !w.Notify(rf) { rf() }` (`internal/rdvq/waiters.go:93-94`) plus the stranded-renotify handler (`:90-96`).
  2. *No inflation* (safety) — wakeups don't multiply without progress (avoid renotify storms / livelock). Note `NotifyAll`'s mint loop (`waiters.go:145`) and `NoopRenotify` coalescing mean this is NOT a literally conserved quantity.
  3. *Termination / convergence* (liveness) — the cross-system cascade actually stops. **This is the missing piece**: the doc asserts convergence but gives no well-founded variant. "Cross-resource borrowing" (A's notify runs B's work, B's readyFn re-triggers A) is the livelock-prone topology. Find a measure that strictly decreases per round (candidates: total outstanding postponed work; outstanding-token count bounded by waiter count).
  - Scope split: the single-`rdvq.Waiters` version is local and provable; the cross-system version is a *per-node proof obligation* (each integrator must guarantee "if I don't consume, I re-propagate"), only as strong as the weakest integrator. State that contract explicitly so Governor/pools/future systems can be checked against it.
  - Known soft spots / historical counterexamples to address: the fixed drained-as-orphan + outbox-wait lost-notification bug (`WORKING_NOTES.md:61`) and the `wrappedRenotify` leak-on-replace/discard (`WORKING_NOTES.md:736`, conservation violation in the other direction).
- **Fix stale conservation doc before proving anything.** `docs/backpressure-and-reentrancy.md:512-517` documents an `Accepted{ deferred, upstream nbcq.Queue[WorkReadyFunc] }` that no longer exists — current code is `fresh / postponed / waiters / listener / scheduled / unmetDemandFn` (`accepted.go`), and `upstream` moved into `Governor` (`governor.go:16`). The claim can't be proven against a spec that doesn't match the code.
- **Full write-up: the conservation trust boundary as a foundational principle.** API_DESIGN.md principle 7 is only a summary. The full treatment is more fundamental and deserves its own design doc: the conservation invariant is inductive over participating nodes; `internal`-ness closes the induction (clause 1: no user-authored nodes); the bracketed-leaf rule protects users (clause 2: every user callback carries zero propagation duty and is fully bracketed by its surrounding node regardless of blocking/panic/reentry/spawn). Show how the existing reentrancy constraints (task-scatter prohibition, `ctxmeta.ShouldBlock` nil for task contexts, queued-not-recursive skim) are instances of clause 2, and derive the bracketed-leaf test that every future plug-in/hook must pass. The trust boundary is goroutine participation, not module ownership — so otpsg/psgwf hooks are in scope too.
- **Full reconciliation of all documentation against current code** (`doc.go` and everything under `docs/`). The stale `Accepted` struct in `backpressure-and-reentrancy.md` is one symptom; the combiner-branch reshape (op-trio rename, Handler unification, psgfn fold-in, wave-at-construction, pending Pool/workq consolidation) has almost certainly left other docs describing the pre-refactor architecture. Audit each doc for terminology drift (Gather/Combiner/TaskRunner → Skim/Funnel/Launcher), struct/field names, and removed concepts (orphan task queue, etc.). Best done as one pass after the Pool/workq consolidation lands, so docs aren't reconciled twice.
- **Targeted `-race` stress on the aptr.go GC-shadow-pointer seam** (`Load` retry loop + `updateNodePtr` reconciliation, `internal/nbcq/aptr.go:45-82`). This is the one part of nbcq *not* covered by published Michael-Scott linearizability proofs — it's Go-specific glue keeping a GC-visible pointer consistent with the uint128 tagged pointer. Do NOT re-prove MS itself; the version-counter+double-width-CAS ABA scheme is textbook and already proven.
- Note (not a task): "no contention" is a *performance* property — it belongs to benchmarking/profiling (bench.txt, charts), not to a proof. Lock-/obstruction-freedom is the closest provable analogue and is a progress guarantee, not a contention bound.
