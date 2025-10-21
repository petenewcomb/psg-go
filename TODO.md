# TODO

## Combiner Branch Pre-Merge Tasks

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

### 6. Implementation improvements
- **Investigate and fix intermittent benchmark deadlock** (CRITICAL)
  - Benchmarks hang after ~463 seconds with 5 goroutines stuck in select for 5+ minutes
  - Key goroutine stuck in `combinePostWork.Execute` → `PushBackFunc` waiting on outbox
  - Likely notification conservation violation or demand coordination race condition
  - See bench_20251020T105451Z.txt and WORKING_NOTES.md "Benchmark Deadlock Issue"
  - May be related to orphan renotify changes or demand token lifecycle
  - Need trace logging and systematic review of notification paths
- **Change RenotifyFunc to Renotifier interface for proper lifecycle management**
  - RenotifyFunc is just `func()` with no Free capability
  - Current workaround: both `orphanedTaskRenotify` and `wrappedRenotify` free themselves in their renotify callbacks
  - This only works when renotifiers are invoked; doesn't handle replacement/discard cases
  - Need Renotifier interface with `Renotify()` and `Free()` methods
  - Allows rdvq infrastructure to properly reclaim pooled renotifier objects in all scenarios
  - Targets: `orphanedTaskRenotify` (job.go:839), `wrappedRenotify` (internal/rdvq/notifier.go:72)
  - See WORKING_NOTES.md "Orphan Renotify Allocation Leak" for detailed analysis
  - Files affected: internal/rdvq/notifier.go, internal/rdvq/waiters.go, job.go, all Notify() callsites
- **Add deadline field to taskPostWork and use it in blocking post operations** (job.go:1011-1118)
  - Currently `newTaskPostWork()` receives deadline parameter but doesn't store or use it
  - All other scatter work types (taskPoolScatterWork, combineScatterWork, gatherScatterWork) properly store and use their deadlines
  - Should add `deadline time.Time` field to struct and pass to BasicPushSelect via context with deadline
- **Consider refactoring taskPostWork.Execute() to reduce duplication with workq.ExecuteOrWait pattern** (job.go:1018-1099)
  - 80+ lines implement similar wait/block/postpone logic to workq.ExecuteOrWait
  - However, has unique requirements: custom TryPushBack, demand tracking, PushBackFunc+BasicPushSelect
  - Evaluate whether common pattern can be extracted without over-abstracting
  - Possible approaches: keep as-is, extract TryPostBehavior pattern, or generalize ExecuteOrWait
  - See WORKING_NOTES.md "Deadline and ExecuteOrWait Refactoring Analysis" for detailed analysis
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

## Post-Merge Enhancements

Items that can be deferred to GitHub issues after the combiner branch is merged.

### Implementation improvements
- fix LockAndSetQueueFunc ugliness
- fix addWork ugliness
- fix inconsistencies between refcounting (and pooling) implementations: semantics re locking, naming, etc.
- consider whether any atomic.Int64s should instead be atomic.Int32 (e.g. InFlightCounter, concurrency tracking in sim/run.go)

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
