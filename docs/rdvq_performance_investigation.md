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

## Conclusion

The rdvq refactoring is a good change:
- Provides measurable performance improvement (2.5-3.5%)
- Significantly cleaner code (~40 lines vs ~150 lines)
- Encapsulates a complex synchronization pattern
- More maintainable and less error-prone

Even without fully understanding the performance improvement, the code quality benefits alone justify the change.