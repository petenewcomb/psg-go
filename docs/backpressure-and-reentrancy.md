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

## Governor: Coordinating Backpressure Across System Boundaries

### The Story Behind Cross-System Coordination

PSG's architecture creates an interesting challenge that doesn't exist in traditional queuing systems. When a scatter operation flows from a TaskPool into a CombinerPool, you're crossing a boundary between two different resource management systems. Each system has its own notification infrastructure, its own capacity constraints, and its own workers waiting for work.

The fundamental problem emerges when the downstream system (CombinerPool) becomes congested. In a traditional system, you might expect the upstream system to simply queue requests until the downstream system is ready. But PSG doesn't work that way—it's built around notification conservation and immediate resource utilization rather than queuing patterns.

When a CombinerPool becomes busy, new combine work hits what the code calls the "slow path" in `postCombineSlow()`. At this point, the work can't be immediately processed, but it also can't just disappear into a queue. The system needs to communicate back to the upstream scatter operations that they should pause and wait for downstream capacity to become available.

This is where the Governor comes in. It serves as a coordination mechanism that allows the downstream system to signal "I'm congested" in a way that the upstream system can understand and respond to appropriately.

### How the Governor Fits Into PSG's Architecture

The Governor doesn't replace any existing coordination—it extends PSG's existing `Waiters` infrastructure to work across system boundaries. When you look at the code in `governor.go`, you can see it's remarkably simple: just an atomic counter tracking how many downstream workers are waiting, combined with the existing `Waiters` system for upstream coordination.

The elegance is in how it integrates with the existing flow. When a scatter operation reaches `combineOp.go`, it gets wrapped by the Governor through `WrapUpstream()`. This wrapper uses the existing `Waiters.Wrap()` pattern that PSG already uses throughout the system, so no upstream code needs to change.

The downstream integration happens in the combiner pool's slow path. When work can't be immediately processed, the pool calls `IncrementDownstreamWaiters()` to signal congestion. When work eventually completes, it calls `DecrementDownstreamWaiters()`. When that counter hits zero, the Governor triggers `NotifyAll()` on its upstream waiters, waking any scatter operations that were paused waiting for downstream capacity.

### The Context Connection

What makes this particularly elegant is how it connects to PSG's context system. The Governor respects the context hierarchy through the `ShouldBlock()` method in `ctxmeta.go`. Top-level contexts participate in backpressure because they can safely wait, but task contexts return `nil` for their block function because they might be holding resources needed to resolve the very congestion they'd be waiting for.

This context awareness prevents the deadlock scenarios that could arise if every context type participated in backpressure. It's another example of how PSG's architectural constraints (like prohibiting task-to-task scattering) create the safety properties that allow sophisticated coordination mechanisms like the Governor to work reliably.

### Why This Matters for Notification Conservation

The Governor ensures that when resources become available in one system, that availability information flows correctly to the system that needs it. Without the Governor, you could have a situation where a CombinerPool becomes available again, but the TaskPool scatter operations that were waiting for that capacity never get notified to retry.

This bidirectional notification flow—downstream signaling congestion upstream, and upstream signaling capacity availability downstream—is what makes PSG's cross-system coordination work without losing the notification conservation properties that make the whole system reliable.

The Governor is essentially a translator between the resource accounting of different subsystems, ensuring that PSG's fundamental principle—that resource availability notifications never get lost—holds true even when work flows across multiple system boundaries.

## Notification Conservation and Cross-System Backpressure

### Core Theory

#### Notification Conservation Principle

**Fundamental Invariant**: Notifications should never disappear. They must keep flowing until either:
1. Someone can act on them (execute deferred work), or 
2. There's genuinely nothing left to wake up

This ensures that resource availability signals always reach work that can utilize those resources.

#### Cross-System Notification Cascading

PSG's architecture involves multiple notification systems:
- **Resource pools** (TaskPool, CombinerPool) that signal capacity availability
- **Work queues** (Accepted queues) that coordinate worker activity
- **Backpressure providers** that bridge between resource constraints and work execution

When a notification flows from one system to another, the receiving system becomes responsible for either:
1. **Consuming the notification** by executing relevant deferred work, or
2. **Propagating the notification** back upstream if it cannot consume it

#### Notification Consumption Semantics

A notification is considered "properly consumed" when it leads to **deferred work execution**. This is because:

1. **Deferred work represents previously blocked work** waiting for resources
2. **Every deferred work item must have a `readyFn`** to guarantee eventual execution
3. **Fresh work execution doesn't consume notifications** because fresh work wasn't waiting for the specific resource that became available

#### Eventual Consistency Through Cross-Resource Borrowing

The system allows "cross-resource borrowing" where:
- TaskPool A's notification might trigger execution of TaskPool B work
- This is safe because TaskPool B work must have its own `readyFn` that will eventually trigger and give TaskPool A work another opportunity
- The system converges through multiple notification rounds rather than requiring perfect resource matching

#### Implementation: Upstream Notifier Queues

Each `Accepted` work queue maintains an `upstream` notification queue:

```go
type Accepted struct {
    fresh    nbcq.Queue[WorkFunc]
    deferred nbcq.Queue[WorkFunc] 
    waiters  rdvq.Waiters
    upstream nbcq.Queue[WorkReadyFunc]  // Pooled renotify functions
}
```

**Worker Execution Logic**:
1. Worker wakes up via notification
2. Attempts to execute deferred work
3. If no deferred work executes AND worker was woken by upstream notification:
   - Push upstream notifier to `upstream` queue for other workers to process
4. If deferred work executes: notification properly consumed

**Cross-System Flow**:
1. **Resource becomes available** → ResourcePool.waiters.Notify()
2. **Watchers respond** → readyFn adds upstream notifier to work queue and wakes worker  
3. **Worker processes work** → if no deferred work executes, queues upstream notifier
4. **Any available worker** can pick up and execute upstream notifiers
5. **Upstream notifiers** continue the cascade back to the original resource pool

#### Benefits

**Notification Conservation**: No notifications get lost in cross-system handoffs

**Lower Latency**: Idle workers can process notification debt from busy workers

**Eventual Consistency**: System converges to optimal resource utilization through multiple rounds

**Scalability**: Pooled renotify queues prevent notification bottlenecks

**Composability**: Any number of systems can be chained while preserving notification flow

## Conclusion

PSG's backpressure and reentrancy management systems provide the foundation for reliable concurrent programming by:

1. **Preventing Resource Exhaustion**: Multi-level backpressure prevents system overload
2. **Enabling Safe Reentrancy**: Work queueing allows nested operations without deadlocks  
3. **Maintaining Liveness**: Structured processing cycles ensure forward progress
4. **Providing Performance**: Lock-free algorithms and adaptive tuning deliver high throughput
5. **Conserving Notifications**: Cross-system notification cascading ensures resource availability signals always reach work that can utilize them

These implementation details enable the high-level programming model described in [programming-model.md](programming-model.md) while maintaining the safety and performance characteristics that make PSG suitable for production use.

The combination of principled architectural constraints (like task scattering prohibition) with sophisticated implementation techniques (like multi-tier queuing and notification conservation) demonstrates that high-performance concurrent systems can be both safe and performant when built on solid theoretical foundations.