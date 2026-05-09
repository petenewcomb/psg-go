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
- **Re-evaluate demand-tracking and dedicated-spawner items below** in light of the rdvq BufferedFunc ordering fix (2026-05-09). The original livelock motivating "combiner worker demand tracking (CRITICAL)" and "v3 dedicated spawner goroutine" was rooted in the rdvq race, not the absence of those mechanisms. Pending benchmark confirmation.
- **Eliminate the orphan concept entirely**: Instead of creating orphans in RDVQ, check for a value in the inbox _before_ grabbing an outbox from the queue.  If a value exists in the inbox, call the renotifyFn returned by the outbox waiter and just return the inbox value.
- **Implement demand tracking for combiner workers** (CRITICAL - prevents livelock)
  - Task workers now have demand tracking (taskWorkerDemand counter) to prevent livelock
  - Combiner workers need similar mechanism to prevent circular blocking scenario:
    * All combiner workers blocked trying to post to gatherQueue
    * New combine work arrives and queues up in combineQueue
    * No workers available to process queued work (all blocked downstream)
    * Current unmetDemandFn only fires when workers are actively receiving, not when blocked
  - Add combinerWorkerDemand counter to CombinerPool (mirror taskWorkerDemand in Job)
  - Add demandRegistered field to combineWork (mirror taskWork.demandRegistered)
  - Increment demand in combinePostWork.Execute() when registering demand
  - Decrement demand in CombinerPool.goroutine() when worker receives work
  - Worker checks demand after receiving work and spawns if demand exists
  - See WORKING_NOTES.md "Combiner Worker Demand Tracking" for detailed analysis
- **Implement dedicated spawner goroutine pattern** (v3 design - improves responsiveness, eliminates contention)
  - Replace distributed spawn attempts (v2a) with single dedicated spawner goroutine per worker type
  - Spawner waits on rdvq.Waiters, wakes on demand notification, spawns at rate-limited intervals
  - Demanding code simplified: just increment counter + call Notify(), no spawn logic
  - Benefits: no contention, no thundering herd, clean rate limiting, single responsibility
  - Task workers: Add taskWorkerSpawner (rdvq.Waiters), runTaskWorkerSpawner() goroutine
  - Combiner workers: Same pattern with combinerWorkerSpawner
  - Default spawn delay: 100µs (~10k spawns/sec rate limit)
  - See WORKING_NOTES.md "Task Worker Demand-Based Spawning v3" for detailed design
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
