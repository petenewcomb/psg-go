# TODO

## Combiner Branch Pre-Merge Tasks

### 1. Analyze and refactor job shutdown sequence
- [ ] Review the cleanup process when jobs complete or are canceled
- [ ] Ensure all resources are properly released
- [ ] Verify that flush/done channel signaling works correctly in all scenarios
- [ ] Check for potential race conditions during shutdown
- [ ] Consider simplifying the state transition logic in job.decrementInFlight and job.decrementCombiners
- [ ] Add debug logging (enabled via flag) to track state transitions for troubleshooting
- [ ] Ensure combiner tasks are properly cleaned up during cancelation

### 2. Flesh out test coverage for combiners
- [ ] Add more unit tests for Combiner functionality
- [ ] Include Combiner testing in the simulation test
- [ ] Test edge cases like empty combiners, very large combiner pools
- [ ] Verify proper integration with Job and Pool components
- [ ] Test cancellation during various combiner operations
- [ ] Add stress tests with high concurrency and rapid task creation/completion
- [ ] Test corner cases around combiner task lingering and timeout
- [ ] Ensure no goroutine leaks in any scenario

### 3. Documentation updates
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
- [ ] Benchmark Combiner vs regular Scatter/Gather operations
- [ ] Profile memory usage during heavy combiner operations
- [ ] Consider adding pool size metrics/stats
- [ ] Analyze whether buffer sizes on channels need tuning
- [ ] Consider making delay parameters configurable

### 5. API finalization
- [ ] Review Combiner constructor API for usability
- [ ] Consider adding helper methods for common combining operations
- [ ] Ensure consistent error handling across the API
- [ ] Consider if time.Duration fields should be exposed/configurable
- [ ] Add WithXxx option methods if appropriate
- [ ] Ensure all public types and methods have consistent naming

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