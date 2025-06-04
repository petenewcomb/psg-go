# RDVQ Performance Investigation

## Summary

Replaced the channel + idle queue pattern in `Job.gatherOne()` with a Rendezvous Queue (rdvq) abstraction. This refactoring provides:
- **2.5-3.5% performance improvement** in throughput
- Cleaner, more maintainable code
- Slightly more consistent performance characteristics

## Commits Compared

- **Before**: Based on commit `1f5de89` "Fix Job.gatherChan optimization with proper channel pooling"
  - Modified to remove redundant select clauses in `tryGatherOne` for fair comparison
- **After**: Current working directory (this commit)

## Background

The original implementation used:
- A Go channel (`gatherChan`) for queuing gather operations
- A separate queue (`idleGatherers`) tracking idle gather workers
- Manual coordination between producers and consumers

The rdvq abstraction encapsulates this pattern into a single data structure that handles the rendezvous between producers and consumers.

## Performance Investigation

### Initial Measurements

Initial benchmarks suggested a ~10% performance improvement, but this turned out to be misleading due to measurement artifacts.

### Key Findings

1. **CPU Frequency Scaling Effects**: Without fixed CPU frequency, benchmarks showed huge variance (281µs to 358µs for the same code).

2. **Profiling Overhead**: Running benchmarks with profiling enabled significantly skewed results, affecting the two implementations differently.

3. **Real Performance Improvement**: With proper controls (fixed CPU frequency, no profiling):
   - Before: ~578-584µs/op
   - After: ~563-566µs/op
   - **Improvement: 2.5-3.5%**

### Benchmark Methodology

Proper measurement required:
- Fixed CPU frequency (`cpupower frequency-set -g performance`)
- No profiling overhead
- Multiple runs to ensure consistency
- Statistical analysis with benchstat

### Code Comparison

The rdvq implementation:
- Performs the same algorithmic operations
- Has nearly identical allocation patterns (48 vs 49 allocs/op)
- Simply reorganizes the code structure

Key differences:
- Unified data structure vs separate channel + queue
- Encapsulated state management
- Possibly better compiler optimization opportunities

## Future Work

The source of the performance improvement remains unclear. Despite doing essentially the same work, rdvq is consistently faster. Investigating why should be future work:

1. **CPU profiling** to identify where cycles are saved
2. **Assembly analysis** to see if the compiler generates different code
3. **Cache analysis** to check for memory access pattern differences
4. **Micro-benchmarking** of individual operations

## Task Worker RDVQ Implementation

Following the success of gather queue RDVQ, we investigated applying the same pattern to task worker management.

### Background

The original `Job.idleWorkers` implementation used:
- `nbcq.Queue[chan func(context.Context)]` - a queue of idle worker channels
- Complex coordination logic for task handoff
- Multiple goroutines and channels for each worker

The new implementation uses:
- `rdvq.Queue[func(context.Context)]` - direct task function queuing
- Unified handoff mechanism via RDVQ
- Simpler worker lifecycle management

### Performance Results

Benchmarks show significant improvements across all key metrics:

| Metric | Before | After | Improvement |
|--------|--------|--------|-------------|
| **Overall Performance** | 538.2µs/task | 512.4µs/task | **4.79% faster** |
| **Throughput** | 10.57k tasks/sec | 11.23k tasks/sec | **6.22% increase** |
| **Gather Operations** | 1006.7µs latency | 955.6µs latency | **5.07% faster** |
| **Memory Usage** | 1010.0 B/task | 1005.4 B/task | **0.46% less** |

### Key Implementation Changes

1. **API Enhancement**: Added `TryPushBack()` method to RDVQ for non-blocking task handoff
2. **Simplified Worker Logic**: Workers use `PopFrontFunc()` with integrated timeout/cancellation handling
3. **Direct Task Handoff**: Eliminated intermediate channel layer

### Statistical Significance

All major improvements show p=0.000 with n=20 samples, confirming high reliability.

### Pattern Validation

The task worker RDVQ implementation demonstrates that the "idle receiver queue" pattern benefits significantly from RDVQ conversion. This validates the approach for future optimizations.

## Conclusion

The rdvq refactoring pattern has proven successful across multiple use cases:

### Gather Queue RDVQ
- 2.5-3.5% performance improvement
- Significantly cleaner code (~40 lines vs ~150 lines)

### Task Worker RDVQ  
- 4.79% performance improvement
- 6.22% throughput increase
- Cleaner worker management

Both implementations provide:
- Measurable performance improvements
- Encapsulation of complex synchronization patterns
- More maintainable and less error-prone code
- Validation of the RDVQ pattern for idle receiver queues

The consistent benefits across different use cases suggest that other "idle receiver queue" patterns in the codebase (such as waitq) would likely benefit from similar RDVQ conversion.