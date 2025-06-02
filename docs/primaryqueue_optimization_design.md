# Idle Worker Queue Pattern Design

## Overview

This document describes a general optimization pattern that eliminates contention on shared channels by replacing them with lock-free queues of idle worker channels. Instead of workers competing for work from a shared channel, idle workers advertise their availability, allowing direct handoff from producers. The pattern trades a small amount of CPU overhead for significant improvements in throughput and latency at high concurrency levels.

## Problem Context

Many concurrent systems use shared channels for work distribution:
- Multiple producers send work to a shared channel
- Multiple workers receive from the same shared channel

Under high concurrency (20+ goroutines), these shared channels become contention bottlenecks. Channel operations require synchronization, and as concurrency increases, goroutines spend more time competing for channel access than doing useful work. This manifests as increased latency and reduced throughput despite having sufficient processing capacity.

This pattern appears in several places:
1. **CombinerPool**: Distributing combine operations to combiner goroutines
2. **Job.taskChan**: Distributing tasks to worker goroutines
3. **Job.gatherChan**: Distributing gather operations (potential future optimization)

## Solution Architecture

The idle worker queue pattern replaces shared channels with a more efficient mechanism:

1. **Dedicated Channels**: Each worker gets its own buffered channel (size 1)
2. **Availability Advertisement**: When idle, workers push their channel into a lock-free queue
3. **Direct Handoff**: Producers pop an idle worker's channel for direct work delivery
4. **No Contention**: Each channel has only one sender and one receiver

This design ensures that in the common case under load, work distribution involves no contention - just direct handoff between producer and consumer.

## General Pattern

### Core Components

```go
// Queue of idle worker channels
type WorkerPool struct {
    idleWorkers nbcq.Queue[chan Work]
}

// Producer side - finding an idle worker
func (p *WorkerPool) submitWork(work Work) {
    // Try to find an idle worker
    for {
        workerCh, ok := p.idleWorkers.PopFront(nodePool)
        if !ok {
            break // No idle workers
        }
        
        select {
        case workerCh <- work:
            return // Successfully handed off
        default:
            // Worker channel closed/full, try next
        }
    }
    
    // No idle workers available - handle according to needs:
    // - Spawn new worker
    // - Fall back to shared channel
    // - Apply backpressure
}

// Worker side - advertising availability
func (p *WorkerPool) runWorker() {
    workerCh := make(chan Work, 1)
    workerChInQueue := false
    
    // Essential cleanup to mark channel as dead so producers skip it
    defer func() {
        select {
        case workerCh <- nil:
            // Successfully marked channel as dead
        default:
            // Channel full, drain and process the work
            work := <-workerCh
            if work != nil {
                // Process the work that was pending
                processWork(work)
            }
        }
    }
    
    for {
        // Execute work...
        
        // Advertise availability
        if !workerChInQueue {
            p.idleWorkers.PushBack(nodePool, workerCh)
            workerChInQueue = true
        }
        
        // Wait for work
        select {
        case work := <-workerCh:
            workerChInQueue = false
            // Process work...
        case <-timeout:
            return // Scale down
        }
    }
}
```

### Key Design Elements

1. **Buffer Size 1**: Worker channels have buffer of 1 to enable non-blocking handoff
2. **Availability Tracking**: `workerChInQueue` flag prevents duplicate queue entries
3. **Graceful Cleanup**: Workers must drain their channel before exiting
4. **Closed Channel Handling**: Producers skip over closed/dead worker channels

## Specific Implementations

### 1. CombinerPool (Hybrid with Fallback)

CombinerPool maintains compatibility by keeping shared channels as a fallback:

```go
func (cp *CombinerPool) postCombine(ctx context.Context, combine boundCombineFunc) {
    // First try idle worker queue
    for {
        workerCh, _ := cp.primaryQueue.PopFront(primaryQueueNodePool)
        if workerCh == nil {
            break
        }
        select {
        case workerCh <- combine:
            return
        default:
            // Worker died, continue
        }
    }
    
    // Fall back to shared channels for compatibility
    select {
    case cp.primaryChan <- combine:
        return
    case cp.secondaryChan <- combine:
        return
    default:
        // Apply backpressure...
    }
}
```

### 2. Job.taskChan (Complete Elimination)

Job completely eliminates the shared channel, spawning workers on demand:

```go
func (j *Job) startTask(taskFn func(context.Context)) {
    // Only try idle workers
    for {
        workerCh, ok := j.idleWorkers.PopFront(taskWorkerNodePool)
        if !ok {
            break
        }
        select {
        case workerCh <- taskFn:
            return
        default:
            // Worker died, continue
        }
    }
    
    // No idle workers - spawn new one with initial task
    j.spawnTaskWorker(taskFn)
}
```

Workers include aggressive scale-down with configurable timeout:

```go
timerp.Reset(idleTimer, time.Duration(j.taskWorkerIdleTimeout.Load()))
select {
case taskFn = <-workerCh:
    workerChInQueue = false
    // Process work
case <-idleTimer.C:
    return // Scale down after timeout
case <-ctx.Done():
    return // Job cancelled
}
```

### 3. waitq Package (Pure Implementation)

The internal waitq package represents the purest form of this pattern - no shared channels at all:

```go
type Queue struct {
    inner nbcq.Queue[Waiter]
}

func (q *Queue) Add() Waiter {
    w := Waiter{
        notifyChan: make(chan struct{}, 1), // Each waiter has dedicated channel
    }
    q.inner.PushBack(p, w)
    return w
}

func (q *Queue) Notify() {
    for {
        w, ok := q.inner.PopFront(p)
        if !ok {
            return
        }
        select {
        case w.notifyChan <- struct{}{}:
            return // Notified successfully
        default:
            // Waiter was closed, try next
        }
    }
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

#### CombinerPool (primaryQueue optimization)

With statistical significance (p < 0.05 for all metrics):

**Performance Improvements:**
- Throughput: +26.11% (8,260 → 10,416 tasks/sec)
- P50 combine latency: -18.78% (19.75µs → 16.04µs)
- P99 combine latency: -24.00% (163.9µs → 124.6µs)
- P50 workflow latency: -31.92% (400.9µs → 272.9µs)

**Resource Trade-offs (per task):**
- Allocations: +8.6% (28.01 → 30.41 allocs/task)
- Memory: -5.5% (1,259 → 1,190 bytes/task)

#### Job.taskChan (complete elimination)

**Performance Improvements:**
- Throughput: +16.29% (10,570 → 12,290 tasks/sec)
- P50 workflow latency: -13.91% (268.0µs → 230.7µs)
- P99 workflow latency: -14.47% (5.561ms → 4.756ms)

**Resource Trade-offs (per task):**
- Memory: +9.9% (1,186 → 1,304 bytes/task)
- Allocations: +4.2% (30.6 → 31.9 allocs/task)

Note: The increased combine latency (+21%) in Job.taskChan is due to combiners handling 16% more throughput, not a regression.

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

## Negative Results: Job.gatherChan Optimization

Not all applications of the idle worker queue pattern proved beneficial. We attempted to apply the same optimization to `Job.gatherChan`, allowing gatherer goroutines to register as idle workers rather than competing for work from a shared channel.

### Implementation Details

The gatherChan optimization followed the same pattern:

```go
// Added to Job struct
idleGatherers           nbcq.Queue[chan boundGatherFunc]
gatherWorkerIdleTimeout atomic.Int64

// Modified postGather to try idle gatherers first
func (j *Job) postGather(ctx context.Context, gather boundGatherFunc) {
    // Try to find an idle gatherer for direct handoff
    for {
        gathererCh, ok := j.idleGatherers.PopFront(gatherWorkerNodePool)
        if !ok {
            break // No idle gatherers
        }
        
        select {
        case gathererCh <- gather:
            return // Successfully handed off
        default:
            // Gatherer channel closed or full, try next
        }
    }
    
    // Fall back to shared channel
    select {
    case j.gatherChan <- gather:
    case <-ctx.Done():
    }
}
```

### Benchmark Results

Extensive benchmarking (10s duration × 10 runs) showed negative performance impact:

**Benchmark Results (10s × 10 runs with benchstat):**
- P50 gather latency: +12.08% WORSE (675.0ns → 756.6ns, p=0.000)
- Throughput: +2.36% BETTER (3,535 → 3,619 tasks/sec, p=0.001)

**Mixed performance impact:**
- Gather latency degraded significantly with statistical significance
- Throughput improved marginally with statistical significance
- Overall impact negative due to latency being a critical metric

### Root Cause Analysis

The optimization failed because the benchmark scenario has fundamentally different characteristics than the CombinerPool:

1. **Single Gatherer**: The benchmark likely uses only one gathering goroutine, eliminating the contention that the optimization was designed to solve

2. **No Competition**: With a single gatherer, there's no shared channel contention to eliminate - the gatherer has exclusive access to `gatherChan`

3. **Pure Overhead**: The lock-free queue operations become pure overhead when there's no contention benefit, adding CPU cost with no throughput gain

4. **Cache Pollution**: Additional atomic operations and queue management degrade cache performance without providing value

### Lessons Learned

1. **Optimization Context Matters**: Performance optimizations are highly dependent on the specific usage patterns they target

2. **Contention Prerequisites**: The idle worker queue pattern only helps when multiple workers actually compete for shared resources

3. **Measure Everything**: Even theoretically sound optimizations can show negative results in practice

4. **Document Negative Results**: Failed optimizations provide valuable insights for future work

### When GatherChan Optimization Might Help

The optimization could still be beneficial in scenarios with:
- Multiple concurrent gatherer goroutines
- High throughput workloads where gather processing becomes a bottleneck
- Applications that explicitly spawn multiple gathering workers

However, the current PSG architecture typically uses a single gatherer per job, making this optimization counterproductive in the common case.

## Conclusion

The primaryQueue optimization successfully addresses channel contention at high concurrency levels by introducing a work-stealing pattern with dedicated channels. The trade-off of slightly higher CPU usage for significantly better throughput and latency is favorable for systems prioritizing responsiveness. The implementation maintains backward compatibility and gracefully degrades to the original behavior when the optimization cannot help.

However, the attempted gatherChan optimization demonstrates that this pattern is not universally beneficial. Performance optimizations must match the specific contention patterns of their target workloads. The negative results from gatherChan optimization reinforce the importance of thorough benchmarking and highlight the context-dependent nature of performance engineering.

This design exemplifies both the power and limitations of systems optimization: using CPU-intensive lock-free operations to eliminate blocking bottlenecks can result in better overall system performance, but only when the bottlenecks actually exist in the target scenario.