# Backpressure and Reentrancy Management

This document describes the implementation details of PSG's backpressure mechanisms and reentrancy management systems that enable reliable flow control and deadlock prevention in concurrent workflows.

## Overview

PSG's reliability depends on two critical implementation systems:
1. **Multi-level backpressure** that prevents resource exhaustion while maintaining throughput
2. **Work queueing for reentrancy** that enables nested operations without deadlocks

These systems work together to provide the structured concurrency guarantees that make PSG's programming model both safe and performant.

## Multi-Level Backpressure System

PSG implements a sophisticated multi-level backpressure system that prevents resource exhaustion while maintaining high throughput. The system operates at multiple levels simultaneously to provide comprehensive flow control.

### Backpressure Levels

#### 1. Task Pool Limits
**Purpose**: Prevent unbounded goroutine creation
**Mechanism**: Concurrency bounds enforce maximum parallel tasks per pool
**Behavior**: Scatter requests block when pool limits are exceeded

```go
// Conceptual implementation
func (tp *TaskPool) checkCapacity() error {
    if tp.inFlight >= tp.limit {
        return backpressureProvider.WaitForCapacity(tp)
    }
    return nil
}
```

**Key Properties**:
- Per-pool independent limits
- Blocks only the scattering goroutine, not the entire system
- Releases capacity as tasks complete

#### 2. Combiner Queue Pressure
**Purpose**: Prevent combiner pool overwhelming
**Mechanism**: Combiner pools provide backpressure when work queues exceed capacity
**Behavior**: Scatter requests yield or block when combiners are overwhelmed

**Implementation Strategy**:
- Monitor combiner work queue depths
- Apply backpressure before queues overflow
- Provide isolated backpressure environments per combiner pool

#### 3. Garbage Collection Pressure
**Purpose**: Prevent system-wide resource exhaustion
**Mechanism**: Monitor GC behavior and block scatters during collection stress
**Behavior**: System-wide scatter blocking when GC overhead is excessive

```go
// Conceptual GC monitoring
type GCMonitor struct {
    gcRatio atomic.Value // Recent GC overhead ratio
}

func (gm *GCMonitor) shouldBackpressure() bool {
    ratio := gm.gcRatio.Load().(float64)
    return ratio > maxGCRatio // e.g., 0.25 = 25% overhead
}
```

**Benefits**:
- Prevents memory exhaustion cascades
- Maintains system responsiveness during GC pressure
- Automatic recovery as GC pressure reduces

#### 4. Yielding Pressure
**Purpose**: Maintain system responsiveness and prevent queue buildup
**Mechanism**: Process completed work before scattering new work
**Behavior**: Proactive processing of up to N completed items before new scatters

### Backpressure Providers

PSG uses **contextual backpressure providers** that adapt behavior to the current execution context:

#### Default Provider (Gather-Based)
**Used by**: Top-level scatters and gather operations
**Behavior**:
- Yields completed gather operations before new scatters
- Applies TaskPool concurrency limits
- Monitors system GC pressure
- Blocks on resource exhaustion

```go
// Conceptual implementation
func (bp *defaultBackpressureProvider) applyBackpressure(ctx context.Context) error {
    // 1. Yield completed work
    bp.yieldCompletedWork(ctx, maxYieldCount)
    
    // 2. Check pool capacity
    if err := bp.checkPoolCapacity(ctx); err != nil {
        return err
    }
    
    // 3. Check GC pressure
    if bp.gcMonitor.shouldBackpressure() {
        return bp.waitForGCRelief(ctx)
    }
    
    return nil
}
```

#### Combiner Provider (Combine-Based)
**Used by**: Scatters within combiner functions
**Behavior**:
- Yields completed combine operations
- Provides isolated backpressure environment
- Prevents combiner queue overflow
- Independent of other backpressure providers

**Key Difference**: Combiner providers operate independently to prevent cascade blocking between different combiner pools.

### Flow Control Mechanisms

#### Yielding Strategy

**Pre-Scatter Yielding**: Process completed work before accepting new scatters
```go
// Conceptual yielding implementation
func yieldBeforeScatter(ctx context.Context, maxYield int) {
    for i := 0; i < maxYield; i++ {
        workItem := tryGetCompletedWork(ctx)
        if workItem == nil {
            break // No more completed work available
        }
        processWorkItem(workItem)
    }
}
```

**Benefits**:
- Prevents unbounded queue growth
- Maintains system responsiveness under load
- Reduces memory pressure from queued work
- Improves cache locality by processing related work together

**Tuning Parameters**:
- `maxYield`: Maximum items to process before scattering (typically 2)
- Balance between responsiveness and scatter latency

#### Blocking Strategy

**Adaptive Blocking**: System adjusts blocking behavior based on current conditions
```go
// Conceptual blocking implementation
func waitForCapacity(ctx context.Context, resource Resource) error {
    for !resource.hasCapacity() {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-resource.capacityAvailable():
            return nil
        case workItem := <-tryGetWork():
            // Process work to potentially create capacity
            processWorkItem(workItem)
        case <-time.After(backpressureTimeout):
            // Periodic capacity check
            continue
        }
    }
    return nil
}
```

**Adaptive Elements**:
- Work processing during blocking creates capacity
- Timeout-based periodic checks prevent stuck states
- Context cancellation for proper cleanup
- Resource-specific capacity signals

## Work Queueing and Reentrancy Management

### The Reentrancy Challenge

PSG supports **controlled reentrancy** - gather and combine functions can scatter new tasks. This creates several challenges:

1. **Stack Overflow**: Unbounded recursive execution depths
2. **Deadlocks**: Circular dependencies between operations  
3. **Liveness**: Blocked operations preventing overall progress
4. **Resource Exhaustion**: Unbounded resource consumption

### Work Queueing Solution

PSG addresses reentrancy through **work queueing** - nested operations create work items rather than executing immediately.

#### Work Queue Architecture

```go
// Conceptual work queue structure
type Job struct {
    workQueue    *nbcq.Queue[WorkItem]  // Non-blocking circular queue
    gatherQueue  *rdvq.Required[Gather] // 3-tier delivery system
    combineQueue *rdvq.Required[Combine] // 3-tier delivery system
}

type WorkItem struct {
    Type WorkItemType // Gather, Combine, Flush
    Data interface{}  // Type-specific payload
}
```

**Work Item Types**:
- **Gather Items**: Completed task results awaiting sequential processing
- **Combine Items**: Inputs awaiting combiner processing  
- **Flush Items**: Time-triggered combiner flush operations

#### Queue Properties

**Non-Blocking Circular Queue (NBCQ)**:
```go
type Queue[T] struct {
    buffer []T           // Circular buffer
    head   atomic.Uint64 // Head pointer
    tail   atomic.Uint64 // Tail pointer
    mask   uint64        // Size mask (size must be power of 2)
}
```

**Properties**:
- **Lock-free**: Uses atomic operations for coordination
- **Non-blocking**: Insertion never blocks to prevent deadlocks
- **Bounded**: Fixed-size circular buffer prevents unbounded growth
- **Thread-safe**: Concurrent access from multiple goroutines

**RDVQ (Rendezvous Queue) System**:
Advanced 3-tier delivery system for high-performance coordination:

1. **Direct Handoff**: Immediate sender-receiver coordination (fastest)
2. **Outbox Buffering**: Per-sender single-item overflow buffering
3. **Shared Channel**: Blocking delivery with backpressure signals

### Reentrancy Flow Control

#### Execution Phases

PSG processing follows a structured execution cycle:

```go
// Conceptual execution cycle
func processingCycle(job *Job) {
    for !job.isDone() {
        // Phase 1: Process queued work items
        processedWork := processWorkQueue(job.workQueue)
        
        // Phase 2: Handle gather/combine operations
        handledOps := processOperationQueues(job)
        
        // Phase 3: Process work generated by phases 1-2
        processGeneratedWork(job.workQueue)
        
        // Phase 4: Check termination conditions
        if !processedWork && !handledOps {
            break // No progress made, exit
        }
    }
}
```

**Phase Characteristics**:
1. **Work Processing**: Handles queued gather/combine/flush operations
2. **Operation Handling**: Processes new gather/combine requests
3. **Generated Work**: Handles work created by phase 1 processing
4. **Termination**: Exits when no progress is possible

#### Liveness Guarantees

**Finite Processing**: Each work item completes in bounded time
- Tasks have finite execution time bounds
- Gather operations process single results
- Combine operations have bounded complexity
- Flush operations complete finite aggregations

**Progress Guarantees**: System makes forward progress despite backpressure
- Work items are processed in finite time
- Yielding ensures work processing before new scatters
- Reference counting prevents resource leaks
- Proper termination detection prevents infinite loops

**Starvation Prevention**: 
- Round-robin processing between different work types
- Bounded queue sizes prevent any single work type from dominating
- Backpressure prevents resource exhaustion

### Deadlock Prevention Through Architecture 

#### Fundamental Constraints

**Task Scattering Prohibition**: Tasks cannot scatter new work
```go
// Tasks can only do this:
func taskFunction(ctx context.Context) (Result, error) {
    result := doWork()
    return result, nil  // Only emit results
}

// Tasks CANNOT do this:
func invalidTaskFunction(ctx context.Context) (Result, error) {
    gatherOp.Scatter(ctx, anotherTask) // FORBIDDEN - prevents deadlocks
    return result, nil
}
```

**Why This Prevents Deadlocks**:
- Eliminates circular task dependencies (Task A → Task B → Task A)
- Simplifies reference counting (tasks only consume, never create references)
- Enables deterministic shutdown (task completion always reduces total work)
- Makes dependency analysis tractable

#### Dependency Ordering

**Hierarchical Dependencies**: Clear ordering prevents cycles
1. **Tasks** complete before their results are processed (tasks → results)
2. **Gather operations** complete before new scatters (gather → scatter)  
3. **Combine operations** complete before flush operations (combine → flush)
4. **Flush operations** complete before job shutdown (flush → done)

**Validation Rules**: Context validation enforces constraints
- Task contexts cannot scatter (validation error)
- Closed jobs reject new scatters (validation error)
- Flushing combiners reject new combines (validation error)

#### Reference Counting System

**Work Reference Tracking**: All concurrent work participates in reference counting
```go
// Conceptual reference counting
func scatterTask(job *Job, task Task) {
    job.workRefs.Add(1)        // Increment before launch
    
    go func() {
        defer job.workRefs.Add(-1) // Decrement on completion
        
        result, err := task(ctx)
        emitResult(result, err)    // Only emit, never scatter
    }()
}
```

**Shutdown Coordination**:
```go
// Conceptual shutdown process
func (job *Job) Wait() {
    // 1. Prevent new top-level scatters
    job.Close()
    
    // 2. Wait for all work to complete
    for job.workRefs.Load() > 0 {
        processAvailableWork() // Help drain work queues
        time.Sleep(shortInterval)
    }
    
    // 3. Final cleanup
    job.cleanup()
}
```

## Performance Optimizations

### Lock-Free Data Structures

**Atomic Coordination**: Minimize lock contention through atomic operations
- RDVQ uses atomic state transitions for coordination
- Work queues use atomic head/tail pointers  
- Reference counting uses atomic increment/decrement
- State machines use atomic state updates

**Memory Ordering**: Careful attention to memory ordering guarantees
- Release semantics for publishing data
- Acquire semantics for consuming data
- Sequential consistency where required for correctness

### Cache Optimization

**Locality Improvements**: 
- Sequential work processing improves temporal locality
- Per-goroutine state isolation reduces false sharing
- Batched operations amortize coordination costs
- Prefetching strategies for predictable access patterns

**Memory Layout**: Optimize data structure layout
- Align frequently-accessed fields to cache lines
- Group related fields together
- Separate read-mostly from write-heavy data
- Use padding to prevent false sharing

### Adaptive Algorithms

**Dynamic Tuning**: System adapts to actual workload characteristics
- Queue sizes adjust based on observed usage patterns
- Backpressure thresholds adapt to system performance
- Goroutine pool sizes scale with workload demands
- Timeout values adjust based on latency observations

## Instrumentation and Observability

### Built-in Metrics

**Queue Metrics**:
- Queue depth histograms
- Enqueue/dequeue rates
- Overflow frequency
- Blocking duration distributions

**Backpressure Metrics**:
- Backpressure activation frequency
- Yield operation counts
- Blocking duration statistics
- Resource exhaustion events

**Performance Metrics**:
- Task execution time distributions
- Gather processing latency
- Combine operation throughput
- End-to-end workflow latency

### Debugging Support

**Structured Events**: Well-defined lifecycle events for tracing
- Task lifecycle (created, started, completed, failed)
- Queue operations (enqueue, dequeue, overflow)
- Backpressure events (activated, resolved)
- Resource events (allocated, released)

**State Inspection**: Runtime state visibility
- Current queue depths and contents
- Active task counts and states
- Backpressure provider states
- Resource utilization statistics

## Conclusion

PSG's backpressure and reentrancy management systems provide the foundation for reliable concurrent programming by:

1. **Preventing Resource Exhaustion**: Multi-level backpressure prevents system overload
2. **Enabling Safe Reentrancy**: Work queueing allows nested operations without deadlocks  
3. **Maintaining Liveness**: Structured processing cycles ensure forward progress
4. **Providing Performance**: Lock-free algorithms and adaptive tuning deliver high throughput

These implementation details enable the high-level programming model described in [programming-model.md](programming-model.md) while maintaining the safety and performance characteristics that make PSG suitable for production use.

The combination of principled architectural constraints (like task scattering prohibition) with sophisticated implementation techniques (like multi-tier queuing) demonstrates that high-performance concurrent systems can be both safe and performant when built on solid theoretical foundations.