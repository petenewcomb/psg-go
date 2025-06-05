# nextValues Buffer Investigation - ABANDONED

## ⚠️ DECISION: nextValues Buffer Removed

**Status**: ABANDONED - The nextValues buffer approach has been determined to be both slower and architecturally unsound.

**Reason**: 
1. **Performance Regression**: Introduced overhead rather than optimization
2. **Unfixable Correctness Issues**: Fundamental race conditions between buffer and channel-based consumers
3. **Infinite Loops**: Values can cycle indefinitely without being consumed
4. **Architectural Incompatibility**: Go's `select` cannot atomically check both channels and data structures

See the [RDVQ Performance Investigation](../../docs/rdvq_performance_investigation.md#nextvalues-buffer-investigation-and-abandonment) for full technical details.

---

## Historical Overview
This benchmark folder validates the fix for the abandonment bug introduced by the nextValues buffer refactoring. It compares the pre-nextValues working implementation against the fixed nextValues implementation.

## Background
The nextValues buffer was introduced to improve RDVQ performance, but it introduced a critical abandonment bug where values could be lost when receivers timed out. This bug was identified in `internal/rdvq/rdvq.go` in the cleanup logic.

## The Abandonment Bug
The bug occurred in the abandonment cleanup logic:

**Original buggy code:**
```go
// Channel is full, drain the pending work and requeue to be
// picked up by the next pop operation.
drainedValue := <-receiverCh
if result.OK {
    q.nextValues.PushBack(&p.valuePool, drainedValue)
} else {
    result.Value = drainedValue  // BUG: Trying to change the past
    result.OK = true            // BUG: BlockFunc already decided "I got nothing"
}
```

**Root cause:** Once a `BlockFunc` times out and returns `BlockResult{OK: false}`, it has already captured state (like `ctx.Err()`) and told the caller "I didn't get a value." You cannot retroactively change that decision.

**Fixed code:**
```go
// Always preserve drained values for the next receiver
drainedValue := <-receiverCh
q.nextValues.PushBack(&p.valuePool, drainedValue)
```

## Test Scenarios

### 1. Performance Benchmark
- **Workload**: `waiting/duration=10µs/flushPeriod=10µs/method=gatherOnly/combinerLimit=0`
- **Focus**: High-frequency timeout scenarios that trigger the abandonment bug
- **Expected**: Fixed version should match or exceed pre-nextValues performance

### 2. Stress Test Validation
- **Test**: `TestQueue_StressWithAbandonments`
- **Focus**: Value conservation under concurrent abandonment scenarios
- **Expected**: Perfect conservation (0 lost values) in fixed version

## Comparison Points

### Before (bed6a6d): Pre-nextValues Implementation
- ✅ **Perfect value conservation** - no abandonment bug
- ✅ **Immediate callback processing** - `PopFront(ctx, pool, processFn)`
- ✅ **Simple abandonment handling** - direct callback execution

### After: Fixed nextValues Implementation  
- ✅ **Fixed abandonment bug** - always preserve drained values
- 🔄 **nextValues buffer** - improved batching capabilities
- 🔄 **Return value API** - `PopFront(ctx, pool) (value, error)`

## Expected Results

With the abandonment bug fixed, we expect:

1. **Value Conservation**: Perfect conservation in stress tests (0 lost values)
2. **Performance Recovery**: Throughput should match or exceed pre-nextValues baseline
3. **Memory Efficiency**: Allocation patterns should be reasonable
4. **Latency Stability**: No excessive latency spikes from abandonment cycles

## Files in this Directory

- `bench.sh` - Benchmark script for before/after validation
- `bench_nextvalues_fixed_before_*.txt` - Pre-nextValues baseline results
- `bench_nextvalues_fixed_after_*.txt` - Fixed nextValues results
- `*_normalized.txt` - Normalized results (if applicable)

## Usage

```bash
# Benchmark pre-nextValues implementation (baseline)
./bench.sh before

# Benchmark fixed nextValues implementation
./bench.sh after
```

## Success Criteria

The fix is validated if:

1. **Stress test passes**: 0 values lost in abandonment stress test
2. **Performance restored**: Throughput within 5% of pre-nextValues baseline
3. **No regression**: Allocation patterns are reasonable
4. **Correctness maintained**: All functional tests pass

## Technical Context

This validation is critical because:

- The nextValues buffer was a major refactoring that affected core RDVQ behavior
- The abandonment bug caused silent data loss under concurrent load
- Performance regressions could invalidate the benefits of the nextValues optimization
- This establishes confidence in the layered RDVQ architecture for future work

## Next Steps

After validation:
- If performance is restored → nextValues refactoring is successful
- If performance gaps remain → investigate remaining bottlenecks
- Consider this as baseline for further RDVQ optimizations