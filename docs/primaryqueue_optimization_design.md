# PrimaryQueue Optimization Design

## Overview

This document describes an optimization to the CombinerPool work distribution mechanism that reduces contention on shared channels by introducing a lock-free queue of dedicated worker channels. The optimization trades a small amount of CPU overhead for significant improvements in throughput and latency at high concurrency levels.

## Problem Context

The CombinerPool manages a dynamic pool of goroutines that execute combine operations. Work distribution previously relied entirely on two shared channels:
- `primaryChan`: The main channel for distributing work
- `secondaryChan`: A spillover channel for when the primary is busy

Under high concurrency (24+ goroutines), these shared channels become contention bottlenecks. Multiple goroutines compete to send work on the producer side, while multiple combiner goroutines compete to receive work on the consumer side. This contention manifests as increased latency and reduced throughput despite having sufficient processing capacity.

## Solution Architecture

The primaryQueue optimization introduces a work-stealing pattern using a lock-free queue:

1. **Dedicated Channels**: Each combiner goroutine gets its own buffered channel (size 1)
2. **Availability Advertisement**: When idle, combiners push their channel into a lock-free queue
3. **Direct Handoff**: Work distributors first try to pop an idle combiner's channel for direct work delivery
4. **Fallback Path**: If no idle combiners are available, fall back to the shared channels

This design ensures that in the common case under load, work distribution bypasses the contended shared channels entirely.

### Implementation Details

```go
// Per-pool lock-free queue of available combiner channels
primaryQueue nbcq.Queue[chan<- boundCombineFunc]

// Per-combiner dedicated channel and tracking
primaryQueueCh := make(chan boundCombineFunc, 1)
primaryQueueChInQueue := false

// Work distribution first tries direct handoff
func (cp *CombinerPool) postCombine(ctx context.Context, combine boundCombineFunc) {
    // Try to find an idle combiner
    for {
        primaryQueueCh, _ := cp.primaryQueue.PopFront(primaryQueueNodePool)
        if primaryQueueCh == nil {
            break
        }
        select {
        case primaryQueueCh <- combine:
            return
        default:
            // Channel not ready, try next
        }
    }
    
    // Fall back to shared channels
    // ... existing implementation
}
```

Key design decisions:

1. **Buffer Size 1**: Minimizes memory overhead while allowing non-blocking send attempts
2. **Lock-Free Queue**: Uses the Michael & Scott non-blocking concurrent queue algorithm for scalability
3. **Availability Tracking**: The `primaryQueueChInQueue` flag prevents duplicate queue entries
4. **Graceful Degradation**: Falls back to shared channels when no idle combiners are available

## Performance Analysis

### Benchmark Configuration

Testing used a high-contention scenario:
- Workload: waiting (I/O simulation)
- Task duration: 10µs
- Flush period: 1ms
- Combiner limit: 24 goroutines
- Benchmark duration: 10s per run, 10 runs total

### Results Summary

With statistical significance (p < 0.05 for all metrics):

**Throughput Improvement:**
- Tasks completed per second: +26.11% (8,260 → 10,416 tasks/s)

**Latency Reductions:**
- P50 combine latency: -18.78% (19.75µs → 16.04µs)
- P99 combine latency: -24.00% (163.9µs → 124.6µs)
- P50 workflow latency: -31.92% (400.9µs → 272.9µs)
- P99 workflow latency: -8.00% (5.946ms → 5.470ms)

**Resource Usage (per completed task):**
- Allocations: +8.6% (28.01 → 30.41 allocs/task)
- Memory bytes: -5.5% (1,259 → 1,190 bytes/task)

### Key Benchmark Results

```
                                     │ bench_extended_before.txt │       bench_extended_after.txt       │
                                     │        completed/s        │ completed/s   vs base                │
CombinerThroughput/.../limit=24-12           8.260k ± 5%   10.416k ± 9%  +26.11% (p=0.000 n=10)

                                     │ bench_extended_before.txt │       bench_extended_after.txt       │
                                     │  p50-combine-latency-sec  │ p50-combine-latency-sec  vs base     │
CombinerThroughput/.../limit=24-12            19.75µ ± 8%               16.04µ ± 8%  -18.78% (p=0.001 n=10)

                                     │ bench_extended_before.txt │       bench_extended_after.txt       │
                                     │  p99-combine-latency-sec  │ p99-combine-latency-sec  vs base     │
CombinerThroughput/.../limit=24-12           163.9µ ± 11%               124.6µ ± 6%  -24.00% (p=0.000 n=10)
```

### CPU Profile Analysis

Profiling revealed the mechanism behind the improvements:

**postCombine CPU usage:**
- Before: 11.04% of total CPU time
- After: 5.12% of total CPU time
- Relative improvement: 54% reduction

This dramatic reduction in work distribution overhead translates directly to the observed throughput and latency improvements.

## Trade-offs and Considerations

### Benefits

1. **Reduced Contention**: Eliminates shared channel bottleneck in the common case
2. **Better Cache Locality**: Each combiner primarily interacts with its own channel
3. **Fair Distribution**: FIFO queue ordering ensures fairness
4. **Lower Latency**: Direct handoff reduces queuing delays
5. **Memory Efficiency**: Despite more allocations, uses less memory per task

### Costs

1. **CPU Overhead**: Lock-free queue operations use atomic CAS loops
2. **Code Complexity**: Additional state tracking and queue management
3. **Memory Allocations**: Queue nodes and extra channels increase allocation count

### When This Optimization Helps

The primaryQueue optimization is most beneficial when:
- High goroutine counts (20+) create channel contention
- Workload has consistent high throughput requirements
- Latency reduction justifies slightly higher CPU usage
- System has CPU headroom to trade for better responsiveness

## Implementation Insights

Several key insights emerged during implementation:

1. **Race Condition Prevention**: The `primaryQueueChInQueue` flag is critical to prevent advertising a channel as available while it's processing work

2. **Channel Draining**: When a combiner becomes secondary or exits, it must properly drain and remove its channel from circulation

3. **Default Case Handling**: The `default` case in `postCombine` when popping channels handles the edge case where a combiner isn't immediately ready

4. **Work Queuing**: The internal `workQueue` allows combiners to accept work immediately and process it asynchronously, maximizing availability

## Future Considerations

Potential enhancements to explore:

1. **Adaptive Enablement**: Automatically enable/disable based on measured contention levels
2. **Queue Metrics**: Track hit rate vs fallback rate to validate effectiveness
3. **Channel Pool**: Pre-allocate channels to reduce allocation overhead
4. **Dynamic Sizing**: Adjust channel buffer size based on workload characteristics

## Conclusion

The primaryQueue optimization successfully addresses channel contention at high concurrency levels by introducing a work-stealing pattern with dedicated channels. The trade-off of slightly higher CPU usage for significantly better throughput and latency is favorable for systems prioritizing responsiveness. The implementation maintains backward compatibility and gracefully degrades to the original behavior when the optimization cannot help.

This design exemplifies a classic systems optimization: using CPU-intensive lock-free operations to eliminate blocking bottlenecks, resulting in better overall system performance despite higher per-operation cost.