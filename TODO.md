# TODO

## Refactor status (2026-06)

The combiner branch has progressed substantially beyond its original scope. See `WORKING_NOTES.md` for the live status of the in-flight reshape. Highlights of what's complete on the branch as of `aab904c`:

- Thread A: Handler[T] unification + op trio rename (`Gather`→`Skim`, `Combiner`→`Funnel`, `TaskRunner`→`Launcher`) + Launcher arity collapse + Submit/SubmitErr/SubmitResult dispatch family.
- Thread B: wave-at-construction with nil-sentinel resolution + worker plumbing so nil-wave ops dispatch correctly from inside any op body + factory pass through psgwf / otpsg wrappers.
- psgfn folded into top-level psg; AccumulatorFactory is an interface with `Close() error`; full convenience constructor surface (`NewFn*` / `NewTask*` / `NewErr*`) per op; void-T type aliases.
- Thread C v0.1: `Forever` sentinel + dispatch `(bool, error)` refactor. Full Try* honoring deadlines deferred pending Pool/workq consolidation.

The next major piece is **Pool/workq consolidation** (merge TaskPool + FunnelPool into one Pool, rationalize workq integration). Thread C completion falls out of that pass.

The sections below are the original pre-refactor TODO. Many items are now stale or superseded; treat them as historical reference and consult WORKING_NOTES + CHANGELOG for current scope.

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
- Promote affinity between combiner goroutines and specific combiner instances to improve cache locality.

### API Enhancements
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
- Better establish the theoretical basis of notification conservation and find a way to measure and verify it
