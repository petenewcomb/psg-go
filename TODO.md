# TODO

## Combiner Branch Pre-Merge Tasks

Items to complete before merging to main branch.

### 3. Documentation updates
- [ ] Complete review and update of doc comments for all new/modified public APIs
- [ ] Update README with information about the new combining architecture
- [ ] Add a Combiner example to the README Features section
- [ ] Create a playground example for the new combining architecture

### 5. API finalization
- [ ] Review and document thread-safety guarantees for remaining public APIs

### 6. Implementation improvements
- [ ] Improve detection of top-level vs. child tasks to prevent adding new top-level tasks after Close() (use ctxMeta to allow new scatters only to finish workflows already started)
- [ ] Refactor otpsg module to build on psgwf workflow context propagation instead of directly on core psg
- [ ] consider whether any atomic.Int64s should instead be atomic.Int32 (e.g. InFlightCounter, concurrency tracking in sim/run.go)
- [ ] make sure that rdvq.Optional methods aren't inappropriately leaking through to Waiters or Required 
- [ ] consider removing combiner goroutines' doneCh and dedicated goroutine now that select on it happens only in the slow path
- [ ] profile (memory, cpu, blocking) again after all the recent refactoring, see if there are any more obvious targets or low-hanging fruit
- [ ] review again for readability
- [ ] reduce potential build-up of stale notify functions (add monitor-style bounding of waiters for rdvq waiters, etc.)

## Post-Merge Enhancements

Items that can be deferred to GitHub issues after the combiner branch is merged.

### Performance Optimizations
- [ ] Find a way for there to be only one instance of the GC monitor that can serve multiple jobs.
- [ ] Add goroutine affinity to combiners to minimize the number of combiner instances and therefore also combiner-output gathers.  This will reduce memory overhead and improve scaling characteristics.  The key challenge will be to measure per-combiner utilization of goroutines and bin-pack them accordingly, though a first cut might just move heavy-hitters to their own dedicated goroutines.
- [ ] Consider allowing (secondary) combiner goroutines to time out only after any pending time-based flushes have completed.  The scary thing here is that the goroutine management behavior can then be derailed by a combiner's minHoldTime setting, preventing timely scale-down of goroutines.  This concern might be addressed by leveraging an aspect of affinity: each combiner could have a different notion of "secondary".

### API Enhancements
- [ ] Consider adding helper methods for common combining operations (e.g., counting, grouping, mapping)
- [ ] Add keyed combine and reduce functionality
- [ ] Consider making it possible to positively close and release task and combiner pools without shutting down the overall job?
- [ ] Add generic hooks in core PSG for key lifecycle events
- [ ] Add metrics hooks for pool resource utilization (in-flight tasks, queue depth)
- [ ] Add hooks for job-level monitoring and statistics
- [ ] Create standard interfaces for instrumentation providers
- [ ] debug mode that runs everything in a way that makes logic easy to debug, ideally in a single goroutine
- [ ] consider publishing generally-useful internal packages as standalone projects
- [ ] consider adding environment variable-based configuration of PSG default tuning parameters 

### Additional Tests and Examples
- [ ] Make sure that combiner pools scale down to zero
- [ ] Test corner cases around combiner timeouts (idleTimeout, minHoldTime, maxHoldTime)
- [ ] Test automatic flushing behavior based on timeout settings
- [ ] Test TaskPool.SetOptions and CombinerPool.SetOptions functionality, especially dynamic pool resizing
- [ ] Ensure no goroutine leaks in any scenario
- [ ] Add comprehensive tests for the heap implementation
- [ ] Test job-binding of CombinerPool, including invalid cases
- [ ] Test panic recovery in combiners (simulate panics in Combine and Flush)
- [ ] Test edge cases with cross-job context propagation
- [ ] Test behavior when combiner factory panics
- [ ] Add tests verifying proper shutdown sequence and resource cleanup
- [ ] Thoroughly test multithreaded gathers
- [ ] Verify cross-job Gather safety similar to Combine cross-job safety (may not be relevant since Gather doesn't bind to jobs like CombinerPool does)
- [ ] Add integration tests with actual TaskPool to verify cross-system notification flow (may be covered by existing simulation/benchmark tests)

### Design Documentation
- [ ] Update and refine design docs to make them more readable and less AI-fueled dumps of bullet points
- [ ] Add documentation that compares rdvq.Waiters, workq.Watchers, and workq.Waiters with condition variables
- [ ] Better establish the theoretical basis of notification conservation and find a way to measure and verify it
