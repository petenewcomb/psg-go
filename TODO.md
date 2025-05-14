# TODO

## Combiner Branch Pre-Merge Tasks

### 1. Analyze and refactor job shutdown sequence
- [x] Review the cleanup process when jobs complete or are canceled
- [x] Ensure all resources are properly released
- [x] Verify that flush/done channel signaling works correctly in all scenarios
- [x] Check for potential race conditions during shutdown
- [x] ~~Consider simplifying the state transition logic in job.decrementInFlight and job.decrementCombiners~~ Implemented new state machine with atomic operations and combined counter approach
- [x] Refactored job state management into internal/state package
- [x] Add debug logging (enabled via flag) to track state transitions for troubleshooting
- [x] Ensure combiner tasks are properly cleaned up during cancellation 
- [x] Implement RegisterFlusher mechanism with proper cleanup via unregister function

### 1.1. Explore using Pool for combiner task limiting
- [x] Investigate using Pool to limit combiner tasks instead of Combiner having its own limit
- [x] Consider pros/cons of combiners and regular tasks sharing a resource pool
- [x] Evaluate how this would affect backpressure behavior
- [x] Assess impact on API simplicity and usage patterns
- Conclusion: combiners and tasks must not share a resource pool as there's no way to reliably prevent deadlock

### 1.2. Explore other refactoring for implementation clarity
- [x] make Pool only about resource limiting (renamed to TaskPool)
- [x] clean up messy internal gatherOne interface
- [x] separate Pool-ness out from Combiner, allowing multiple combiners to share a concurrency limit (implemented CombinerPool)
- [x] initial refactoring of backpressure modes into abstracted provider pattern

### 2. Flesh out test coverage for combiners
- [x] Create Example_combiner test to demonstrate and document combiner behavior
- [x] Create benchmarks comparing combiner vs. direct task approach with different batch sizes
- [x] Benchmark with increasing numbers of tasks to identify optimal combiner settings
- [x] Add more unit tests for Combiner functionality
- [x] Include Combiner testing in the simulation test framework
- [x] Test edge cases like empty combiners, very large combiner pools
- [x] Verify proper integration with Job and Pool components
- [x] Test cancellation during various combiner operations
- [x] Add stress tests with high concurrency and rapid task creation/completion
- [ ] Test corner cases around combiner timeouts (idleTimeout, minHoldTime, maxHoldTime)
- [ ] Test automatic flushing behavior based on timeout settings
- [ ] Test TaskPool.SetLimit and CombinerPool.SetLimit functionality, especially dynamic pool resizing
- [ ] Ensure no goroutine leaks in any scenario
- [ ] Add comprehensive tests for the new heap implementation
- [x] Test memory usage patterns for large combiner workloads

### 2.1. Post-CombinerPool Refactoring Tests
- [ ] Test job-binding of CombinerPool, including invalid cases
- [ ] Test panic recovery in combiners (simulate panics in Combine and Flush)
- [ ] Test edge cases with cross-job context propagation
- [ ] Test behavior when combiner factory panics
- [ ] Test cleanup behavior with mixed TaskPool and CombinerPool operations
- [ ] Add tests verifying proper shutdown sequence and resource cleanup

### 3. Documentation updates
- [x] Add Example_combiner test showing real-world use case with result aggregation
- [x] Add job shutdown observability hooks
- [ ] Complete review and update of doc comments for all new/modified public APIs
- [x] Document the new Combiner interface and FuncCombiner implementation
- [x] Document the Combine type and its relationship with Gather
- [x] Document CombinerPool and renamed TaskPool
- [x] Document the configuration options (idleTimeout, minHoldTime, maxHoldTime, spawnDelay)
- [x] Update documentation for configuration methods with standardized language
- [ ] Update README with information about the new combining architecture
- [ ] Add a Combiner example to the README Features section
- [ ] Update CHANGELOG to document:
  - [ ] Refactoring of Combiner into interface
  - [ ] Addition of CombinerPool type
  - [ ] Renaming of Pool to TaskPool
  - [ ] Implementation of time-based flushing with min/max hold times
  - [ ] Implementation of callback-based emission pattern
  - [ ] Implementation of backpressure modes
  - [ ] Fix for deadlock in circular task dependencies
  - [ ] Improvements to notification system
- [x] Ensure example code properly demonstrates new Combiner interface and Combine type
- [ ] Create a playground example for the new combining architecture

### 4. Performance optimization
- [ ] Test automatic scaling of combiner task count based on workload
- [x] Implement SetLimit function for both TaskPool and CombinerPool
- [ ] Add tests for SetLimit functionality for both TaskPool and CombinerPool
- [x] Analyze whether buffer sizes on channels need tuning
- [x] Make delay parameters configurable (implemented idleTimeout, minHoldTime, maxHoldTime)
- [x] Review and potentially adjust or remove spawnDelay parameter (implemented SetSpawnDelay)
- [x] Profile memory usage during heavy combiner operations
- [x] Add metrics/stats for monitoring combiner efficiency and utilization
- [x] Benchmark performance with various combiner configurations

### 5. API finalization
- [x] Improve JobState interface with RegisterFlusher pattern
- [x] Consider what happens if the same Combine is used to scatter tasks across pools from multiple different jobs, as is possible with Gather (resolved by binding CombinerPool to Job)
- [x] Refactor Combiner into interface and separate CombinerPool (renamed Pool to TaskPool)
- [x] Implement callback-based emission pattern instead of boolean returns
- [x] Create FuncCombiner for simpler functional implementations
- [x] Implement Combine type to link Gather with Combiner instance
- [ ] Consider adding helper methods for common combining operations (e.g., counting, grouping, mapping)
- [x] Ensure consistent error handling across the API, especially when tasks fail
- [x] Expose configuration options for delay parameters (idleTimeout, minHoldTime, maxHoldTime, spawnDelay)
- [x] Examine further refactoring opportunities (decided to keep boundCombiner.CombineFunc and FlushFunc as is)
- [x] Ensure all public types and methods have consistent naming
- [x] Update documentation for configuration methods with standardized language
- [ ] Review and document thread-safety guarantees for remaining public APIs
- [ ] Test and document behavior when tasks passed to combiners return errors
- [x] Rename Combiner to reflect verb form (similar to Gather)
- [x] Build Combine from a Gather instance to create natural task→combine→gather flow
- [x] Make task pools dynamically addable to a job (i.e., by taking a job as an argument to NewTaskPool) and remove the pools argument from NewJob
- [ ] Consider making it possible to "shut down" task and combiner pools without shutting down the overall job?

### 5.1. Post-CombinerPool Refactoring Enhancements
- [ ] Simplify and clarify context propagation and checking (review includesJob and newTaskContext)
- [ ] Standardize field naming between TaskPool and CombinerPool for consistency (e.g., liveCount vs. liveGoroutineCount)
- [ ] Verify cross-job Gather safety similar to Combine cross-job safety
- [ ] Add panic recovery for combiner factory creation
- [ ] Review race conditions during job shutdown and combiner flushing
- [ ] Ensure zero-value safety for CombinerPool (panic or validation)
- [ ] Review potential deadlocks during cleanup, especially with combiners
- [ ] Update NewCombinerPool documentation to clarify Job binding

### 6. Context propagation improvements
- [ ] Modify gather/combiner functions to receive the task's context instead of the job context
- [ ] Ensure task contexts (passed to Scatter) are automatically canceled when the job context is canceled
- [ ] Support OpenTelemetry trace propagation through the task-gather chain
- [ ] Enable task-specific cancelation without affecting other tasks
- [ ] Allow task-specific data to flow naturally via context without changing interfaces
- [ ] Add examples demonstrating context propagation use cases for all library components
- [ ] Test context propagation with timeouts, cancelation, and values
- [ ] Improve detection of top-level vs. child tasks to prevent adding new top-level tasks after Close()
- [ ] Document best practices for context usage throughout the library
