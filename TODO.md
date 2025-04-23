# TODO

## Combiner Branch Pre-Merge Tasks

### 1. Analyze and refactor job shutdown sequence
- [ ] Review the cleanup process when jobs complete or are canceled
- [ ] Ensure all resources are properly released
- [ ] Verify that flush/done channel signaling works correctly in all scenarios
- [ ] Check for potential race conditions during shutdown

### 2. Flesh out test coverage for combiners
- [ ] Add more unit tests for Combiner functionality
- [ ] Include Combiner testing in the simulation test
- [ ] Test edge cases like empty combiners, very large combiner pools
- [ ] Verify proper integration with Job and Pool components

### 3. Documentation updates
- [ ] Add or update doc comments for all new/modified public APIs
- [ ] Update README with information about the Combiner feature
- [ ] Update CHANGELOG to document the new features and fixes
- [ ] Ensure example code properly demonstrates Combiner usage

## Implementation Notes

### Backpressure Modes
The implementation now uses three distinct backpressure modes:
- `backpressureDecline`: Return without launching when capacity is full (used for TryScatter)
- `backpressureGather`: Block and gather results to make room (used for Scatter)
- `backpressureWaiter`: Block until notified by a waiter (used for Combiner)

This design prevents deadlocks when the same entity is both launching tasks and processing results.