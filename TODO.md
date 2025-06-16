# TODO

## Combiner Branch Pre-Merge Tasks

Items that must be completed before merging to main branch.

### 2. Testing
- [ ] Test corner cases around combiner timeouts (idleTimeout, minHoldTime, maxHoldTime)
- [ ] Test automatic flushing behavior based on timeout settings
- [ ] Test TaskPool.SetLimit and CombinerPool.SetLimit functionality, especially dynamic pool resizing
- [ ] Ensure no goroutine leaks in any scenario
- [ ] Add comprehensive tests for the new heap implementation
- [ ] Test job-binding of CombinerPool, including invalid cases
- [ ] Test panic recovery in combiners (simulate panics in Combine and Flush)
- [ ] Test edge cases with cross-job context propagation
- [ ] Test behavior when combiner factory panics
- [ ] Test cleanup behavior with mixed TaskPool and CombinerPool operations
- [ ] Add tests verifying proper shutdown sequence and resource cleanup
- [ ] Test and document behavior when tasks passed to combiners return errors
- [ ] test running gather scatters from combiners and vice versa in combiner benchmark

### 3. Documentation updates
- [ ] Complete review and update of doc comments for all new/modified public APIs
- [ ] Update README with information about the new combining architecture
- [ ] Add a Combiner example to the README Features section
- [ ] Create a playground example for the new combining architecture
- [ ] Clearly show the reentrancy effect of scattering or gathering within combiners and gather functions, esp. given that combiners may also be flushed
- [ ] Add documentation that compares waitq with condition variables

### 4. Performance optimization
- [ ] Test automatic scaling of combiner task count based on workload
- [ ] Add tests for SetLimit functionality for both TaskPool and CombinerPool
- [ ] Add goroutine affinity to combiners to minimize the number of combiner instances and therefore also combiner-output gathers.  This will reduce memory overhead and improve scaling characteristics.  The key challenge will be to measure per-combiner utilization of goroutines and bin-pack them accordingly, though a first cut might just move heavy-hitters to their own dedicated goroutines.
- [ ] Consider allowing (secondary) combiner goroutines to time out only after any pending time-based flushes have completed.  The scary thing here is that the goroutine management behavior can then be derailed by a combiner's minHoldTime setting, preventing timely scale-down of goroutines.  This concern might be addressed by leveraging an aspect of affinity: each combiner could have a different notion of "secondary".

### 5. API finalization
- [ ] Review and document thread-safety guarantees for remaining public APIs
- [ ] consider removing "One" from (Try)?(Gather|Combine)One, since they may gather or combine more than one 
- [ ] use Options-style configuration at least for CombinerPool

### 6. Implementation improvements
- [ ] Simplify and clarify context propagation and checking (review includesJob and newTaskContext, shift to leveraging vettedContext)
- [ ] Improve detection of top-level vs. child tasks to prevent adding new top-level tasks after Close() (use inGather/combinerBackpressureProvider to allow new scatters only to finish workflows already started)
- [ ] Review race conditions during job shutdown and combiner flushing
- [ ] Review potential deadlocks during cleanup, especially with combiners
- [ ] Make sure we're always selecting on the minimum number of channels at a time 
- [ ] Refactor otpsg module to build on psgwf workflow context propagation instead of directly on core psg
- [ ] Consider changing JobState.(In|De)crementTasks to JobState.(In|De)crementWork or similar, since it's also used to count pending scatters and gathers (I think)
- [ ] Replace queuing parameter of boundCombineFunc with pendingCombine wrapper struct that includes a releaseWaiters boolean flag
- [ ] Reconsider Min/MaxGatherCount on sim.Plan -- it seems that implicit combine flushes will not result in gathers anyway

## Post-Merge Enhancements

Items that can be deferred to GitHub issues after the combiner branch is merged.

### Performance Optimizations
- [ ] Test corner cases around combiner timeouts (idleTimeout, minHoldTime, maxHoldTime)
- [ ] Test automatic flushing behavior based on timeout settings
- [ ] Test automatic scaling of combiner task count based on workload
- [ ] Add goroutine affinity to combiners to minimize the number of combiner instances and therefore also combiner-output gathers.  This will reduce memory overhead and improve scaling characteristics.  The key challenge will be to measure per-combiner utilization of goroutines and bin-pack them accordingly, though a first cut might just move heavy-hitters to their own dedicated goroutines.
- [ ] Consider allowing (secondary) combiner goroutines to time out only after any pending time-based flushes have completed.  The scary thing here is that the goroutine management behavior can then be derailed by a combiner's minHoldTime setting, preventing timely scale-down of goroutines.  This concern might be addressed by leveraging an aspect of affinity: each combiner could have a different notion of "secondary".
- [ ] Actually hook up gcok to do something useful, and find a way for there to be only one instance of the monitor.

### API Enhancements
- [ ] Consider adding helper methods for common combining operations (e.g., counting, grouping, mapping)
- [ ] Consider making it possible to "shut down" task and combiner pools without shutting down the overall job?
- [ ] Add generic hooks in core PSG for key lifecycle events
- [ ] Add metrics hooks for pool resource utilization (in-flight tasks, queue depth)
- [ ] Add hooks for job-level monitoring and statistics
- [ ] Create standard interfaces for instrumentation providers
- [ ] debug mode that runs everything in a single goroutine in a way that makes logic easy to debug
- [ ] consider removing "One" from (Try)?(Gather|Combine)One, since they may gather or combine more than one 
- [ ] use Options-style configuration at least for CombinerPool

### 5. API enhancements
- [ ] Consider adding helper methods for common combining operations (e.g., counting, grouping, mapping)
- [ ] Consider making it possible to "shut down" task and combiner pools without shutting down the overall job?
- [ ] Hooks and instrumentation:
  - [ ] Add generic hooks in core PSG for key lifecycle events
  - [ ] Add metrics hooks for pool resource utilization (in-flight tasks, queue depth)
  - [ ] Add hooks for job-level monitoring and statistics
  - [ ] Create standard interfaces for instrumentation providers
- [ ] debug mode that runs everything in a single goroutine in a way that makes logic easy to debug

### Additional Tests and Examples
- [ ] Test edge cases with cross-job context propagation
- [ ] Ensure no goroutine leaks in any scenario
- [ ] Add comprehensive tests for the new heap implementation
- [ ] Clearly show the reentrancy effect of scattering or gathering within combiners and gather functions, esp. given that combiners may also be flushed
- [ ] Make sure that combiner pools scale down to zero
- [ ] Add tests for SetLimit functionality for both TaskPool and CombinerPool (not combiner-specific)

### Design Documentation
- [ ] Add an overall design doc that covers the user-facing design of psg.  this would have a more theoretical bent as opposed to the practical focus of what's in doc.go.  This doc would focus on overall theory not specific implementation.
- [ ] Add a design doc for the implementation of backpressure mechanisms, detailing the interplay of recursion and reentrancy.

### Items Needing Further Investigation
- [ ] Verify cross-job Gather safety similar to Combine cross-job safety (may not be relevant since Gather doesn't bind to jobs like CombinerPool does)
