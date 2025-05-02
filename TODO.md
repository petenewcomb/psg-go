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
- [ ] Investigate using Pool to limit combiner tasks instead of Combiner having its own limit
- [ ] Consider pros/cons of combiners and regular tasks sharing a resource pool
- [ ] Evaluate how this would affect backpressure behavior
- [ ] Assess impact on API simplicity and usage patterns

### 1.2. Explore other refactoring for implementation clarity 
- [ ] make Pool only about resource limiting
- [ ] clean up messy internal gatherOne interface

### 2. Flesh out test coverage for combiners
- [x] Create Example_combiner test to demonstrate and document combiner behavior
- [x] Create benchmarks comparing combiner vs. direct task approach with different batch sizes
- [x] Benchmark with increasing numbers of tasks to identify optimal combiner settings
- [ ] Add more unit tests for Combiner functionality
- [ ] Include Combiner testing in the simulation test framework
- [ ] Test edge cases like empty combiners, very large combiner pools
- [ ] Verify proper integration with Job and Pool components
- [ ] Test cancellation during various combiner operations
- [ ] Add stress tests with high concurrency and rapid task creation/completion
- [ ] Test corner cases around combiner task lingering and timeout
- [ ] Test Pool.SetLimit functionality with combiners, especially dynamic pool resizing
- [ ] Ensure no goroutine leaks in any scenario
- [ ] Test memory usage patterns for large combiner workloads

### 3. Documentation updates
- [x] Add Example_combiner test showing real-world use case with result aggregation
- [x] Add job shutdown observability hooks
- [ ] Add or update doc comments for all new/modified public APIs
- [ ] Update README with information about the Combiner feature
- [ ] Add a Combiner example to the README Features section
- [ ] Update CHANGELOG to document:
  - [ ] Addition of Combiner type and functionality
  - [ ] Implementation of backpressure modes
  - [ ] Fix for deadlock in circular task dependencies
  - [ ] Improvements to notification system
- [ ] Ensure example code properly demonstrates Combiner usage
- [ ] Document the configuration options (spawnDelay, linger, etc.)
- [ ] Create a playground example for the Combiner

### 4. Performance optimization
- [ ] Test automatic scaling of combiner task count based on workload
- [ ] Implement and test SetLimit function for Combiner (similar to Pool.SetLimit)
- [ ] Analyze whether buffer sizes on channels need tuning
- [ ] Consider making delay parameters (spawnDelay, linger) configurable
- [ ] Profile memory usage during heavy combiner operations
- [ ] Add metrics/stats for monitoring combiner efficiency and utilization
- [x] Benchmark performance with various combiner configurations

### 5. API finalization
- [x] Improve JobState interface with RegisterFlusher pattern
- [ ] Consider what happens if the same Combiner is used to scatter tasks across pools from multiple different jobs, as is possible with Gather
- [ ] Review Combiner constructor API for usability 
- [ ] Consider adding helper methods for common combining operations (e.g., counting, grouping, mapping)
- [ ] Ensure consistent error handling across the API, especially when tasks fail
- [ ] Expose configuration options for spawnDelay and linger parameters
- [ ] Add WithXxx option methods if appropriate
- [ ] Ensure all public types and methods have consistent naming
- [ ] Review and document thread-safety guarantees for all public APIs
- [ ] Test and document behavior when tasks passed to combiners return errors
- [ ] Should "Combiner" be "Combine" (noun->verb) to parallel "Gather" (which a verb; the noun form would be Gatherer) 
- [ ] Should "Combine/Combiner" be built from a "Gather" and therefore not require a GatherFunc arg itself?

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

## Implementation Notes

### Backpressure Modes
The implementation now uses three distinct backpressure modes:
- `backpressureDecline`: Return without launching when capacity is full (used for TryScatter)
- `backpressureGather`: Block and gather results to make room (used for Scatter)
- `backpressureWaiter`: Block until notified by a waiter (used for Combiner)

This design prevents deadlocks when the same entity is both launching tasks and processing results.

### Waiters Notification System
A key part of the implementation is the waiters notification system:
- `waitersChannel` in Pool tracks tasks waiting for capacity
- `registerWaiter` and `notifyWaiter` manage the notification process
- Buffered notification channels prevent missed signals
- This approach decouples capacity signals from result gathering
