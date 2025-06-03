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

The gatherChan optimization followed the same pattern (full implementation at commit [a997889](https://github.com/petenewcomb/psg-go/commit/a99788984ddb51822c97d551e48cf2d82eddc5c0)):

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

**Channel Pooling Improvement:**
The initial negative results led to investigation of the channel allocation overhead.
Adding channel pooling with `sync.Pool` eliminated the per-call allocation:

```go
var gathererChannelPool = sync.Pool{
    New: func() interface{} {
        return make(chan boundGatherFunc, 1)
    },
}

// In gatherOne():
gathererCh := gathererChannelPool.Get().(chan boundGatherFunc)
defer func() {
    // Return to pool with proper race condition handling
    gathererChannelPool.Put(gathererCh)
}
```

**Final Results with Proper Channel Pooling (15s × 15 runs with benchstat):**
- P50 gather latency: -5.34% BETTER (707.6ns → 669.8ns, p=0.000) ✓
- P99 gather latency: -6.66% BETTER (1.803µs → 1.683µs, p=0.000) ✓
- Throughput: neutral (3,140 → 3,094 tasks/sec, p=0.233)
- Memory: neutral (same 50 allocs/op)

**Key takeaway:** Proper implementation with channel pooling turned a 12% degradation into a 5-6% improvement!

### Root Cause Analysis

Initial results suggested the optimization failed due to:

1. **Single Gatherer**: The benchmark uses only one gathering goroutine
2. **Channel Allocation Overhead**: Per-call channel creation dominated costs
3. **Implementation Issues**: Race conditions in channel pooling logic

However, with proper channel pooling, the optimization **actually succeeds**:

1. **Reduced Latency**: Direct handoff eliminates queuing delays even with single gatherer
2. **Lock-free Benefits**: The nbcq queue provides more efficient operations than channel locks
3. **Channel Pooling Critical**: Eliminating allocation overhead was key to success
4. **Pattern Still Applicable**: Benefits exist even without multiple gatherers competing

### Lessons Learned

1. **Implementation Details Matter**: Proper channel pooling turned a -12% degradation into a +5-6% improvement

2. **Benchmark Methodology Critical**: Baseline drift between sessions can mask true effects

3. **Premature Conclusions Dangerous**: Initial "negative" results were due to implementation flaws, not fundamental issues

4. **Lock-free Can Beat Channels**: Even with single consumer, lock-free queues can outperform Go channels

5. **Document the Journey**: The evolution from negative to positive results teaches valuable lessons

### When to Apply This Pattern

The idle worker queue pattern with channel pooling provides benefits when:
- **Any level of concurrency** exists (even single consumer benefits from reduced latency)
- **Hot paths** where allocation overhead matters
- **Lock-free operations** can replace channel synchronization
- **Direct handoff** can eliminate queuing delays

The pattern provides even more benefits with:
- Multiple concurrent workers competing for tasks
- High throughput workloads where every nanosecond counts
- Systems where latency reduction is more important than throughput

## UBCQ Abstraction Attempt

Following the successful implementation of the idle worker queue pattern, we attempted to create a reusable abstraction called UBCQ (Unbounded Blocking Concurrent Queue) to replace the pattern-specific implementations. The UBCQ package provides a cleaner API with methods like `PushBack`, `PopFront`, and `PopFrontFunc` (which accepts a custom blocking function to avoid creating goroutines for context cancellation).

The full implementation and analysis can be found in commit [226f766](https://github.com/petenewcomb/psg-go/commit/226f766b6e069a9055904ed22486a5b792978b10).

### UBCQ Integration Results

We integrated UBCQ to replace `Job.gatherChan` and `Job.idleGatherers`. The implementation was straightforward:
- Replaced the channel and idle queue with a single `ubcq.Queue[boundGatherFunc]`
- Simplified `postGather` to just call `gatherQueue.PushBack()`
- Used `PopFrontFunc` in `gatherOne` to handle multiple cancellation conditions

However, benchmark results (20s × 20 runs) showed significant regressions:

**Performance Impact:**
- **Throughput**: -18.51% (3,779 → 3,079 tasks/sec)
- **Operation time**: +23.78% (524.6µs → 649.3µs)
- **p50 gather latency**: no significant change (718.5ns → 728.7ns)
- **p99 gather latency**: +2.78% (1.725µs → 1.773µs)
- **Memory per task**: +3.0% (949 → 979 bytes/task)
- **Allocations per task**: +15.3% (25.2 → 29.0 allocs/task)

### Analysis

The UBCQ abstraction introduces overhead through:
1. **Additional indirection**: The generic queue interface adds method calls and interface conversions
2. **Memory allocations**: The abstraction requires additional allocations for queue management
3. **Loss of optimization opportunities**: The hand-tuned implementation could make assumptions that the generic abstraction cannot

This demonstrates an important trade-off in systems programming: abstractions that improve code maintainability and reusability can come at a significant performance cost. For high-performance critical paths like gather operations, the hand-optimized implementation remains superior.

## Conclusion

The primaryQueue optimization successfully addresses channel contention at high concurrency levels by introducing a work-stealing pattern with dedicated channels. The trade-off of slightly higher CPU usage for significantly better throughput and latency is favorable for systems prioritizing responsiveness. The implementation maintains backward compatibility and gracefully degrades to the original behavior when the optimization cannot help.

The evolution of the gatherChan optimization from initial negative results to eventual success (after proper channel pooling) demonstrates the importance of implementation details. Even well-designed patterns can fail without careful attention to allocation overhead and race conditions.

The UBCQ abstraction attempt further illustrates that while generic, reusable components are valuable for code maintainability, they may not be suitable for performance-critical paths. The ~18% throughput reduction when using UBCQ validates the decision to maintain hand-optimized implementations for core PSG operations.

This design journey exemplifies both the power and limitations of systems optimization:
- Lock-free operations can eliminate blocking bottlenecks, but only when those bottlenecks actually exist
- Implementation details matter as much as algorithmic design
- Generic abstractions, while cleaner, may sacrifice too much performance for critical paths
- Thorough benchmarking with realistic parameters is essential for making informed decisions