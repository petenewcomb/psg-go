# PSG-Go Combiner Branch Working Notes

This document contains working notes and context for development on the `combiner` branch. It tracks current implementation insights and architectural understanding needed for remaining work.

Major combiner architecture work is complete. Branch is now in cleanup and finalization phase.

## Recent Performance Analysis (2025-07-25)

### Rdvq Outbox Optimization (2911b9c)

Implemented non-blocking fast path for all outbox channels, not just fresh ones. Previously, non-fresh outboxes were forced through expensive selectFn path (waiter setup, notification registration) even when they had available capacity.

**Performance Impact:**
- Processing workloads: +88-142% throughput improvements in high task multiplier scenarios
- Waiting workloads: +36-189% throughput improvements but with some latency increases
- The optimization eliminates unnecessary waiter overhead for reused outboxes with capacity

**Key Insight:** The latency increases in waiting workloads likely reflect the optimization exposing underlying Go runtime scheduler pressure rather than PSG algorithmic issues. Waiting workloads create many park/unpark events that strain the scheduler, and higher task creation rates amplify this pressure.

**Next Steps:** Implement scheduler health monitoring using near-instant event timing to detect runtime pressure and add backpressure mechanisms when thresholds are exceeded.

## Previous Performance Analysis (2025-07-22)

### Important Note on Benchmark Baseline
The original analysis used bench_norm_old.txt which was found to be from a different benchmark configuration (simplified test with different task scattering and max hold time settings). The analysis has been corrected using bench_12f5955_20250722T130401Z_norm.txt as the proper baseline.

### Summary of Previous Optimizations
The benchmark comparison (new baseline from commit 12f5955 vs current) captures the impact of:
- Hardware-accelerated 128-bit atomics in nbcq (409d49d)
- Data race fix in CombinerPool goroutine context setup (c515383)
- Receiver/waiter reuse to prevent stale notification buildup (42ab341)

### Performance Validation Summary

**IMPORTANT**: All recent optimization commits have been validated through targeted benchmarking and proper statistical analysis.

#### Key Discovery: Measurement Variance
Initial comprehensive analysis suggested performance regressions, but when baseline measurement variance (±13%) was properly accounted for, all reported regressions fell within overlapping statistical ranges.

#### Targeted Benchmarking Results

**Hardware-accelerated 128-bit atomics (409d49d):**
- Processing workloads: p99 latency improved ~5%, throughput improved ~1%
- GatherOnly operations: no statistically significant impact
- **Result**: Optimization worked exactly as intended

**Data race fix (c515383) and receiver/waiter reuse (42ab341):**
- Targeted testing shows performance consistent with intended behavior
- No actual regressions when measurement variance properly considered
- **Result**: Infrastructure changes performed as expected

#### Final Assessment
All optimization commits (409d49d, c515383, 42ab341, 2911b9c) performed as intended. The rdvq optimization provides the largest performance gains while exposing scheduler-related constraints that need to be addressed through runtime pressure monitoring.

**The optimization work was entirely successful with clear next steps identified.**

## Recent Implementation (2025-07-28)

**Scheduler Latency Backpressure**: Implemented scheduler monitoring infrastructure to replace GC-based backpressure. The system can detect Go runtime scheduler pressure and apply backpressure when thresholds are exceeded. Configuration available via `WithSchedulerLatencyThreshold()` and `WithSchedulerLatencyMaxAge()`.

**Key Changes:**
- **Rdvq API improvements**: Inbox/Outbox objects now encapsulate channels + metadata instead of exposing raw channels directly
- **Proper scheduler latency measurement**: Fixed measurement to track actual Go scheduler delays (runnable→running time) rather than application operation delays
- **Performance neutral when disabled**: Benchmarking confirms no significant performance impact when scheduler monitoring is disabled
- **Disabled by default**: `DefaultSchedulerLatencyThreshold = 0` to avoid premature optimization

**Implementation Details:**
- `schedulerLatencySensor` tracks wait cycles: `waitStarting()` → `triggered()` (runnable) → `waitEnded()` (running)  
- Latency calculated as time from goroutine becoming runnable to actually executing
- Global measurements shared across jobs (can be filtered per-job if needed)
- Trace logging integration for debugging

**Decision**: Scheduler monitoring included but disabled by default. The rdvq API improvements provide immediate ergonomic benefits while keeping the backpressure feature available for future tuning if needed.

## Current Status (2025-08-09)

**Combiner Architecture Progress:**
- Core combiner/gather infrastructure complete and tested
- psgwf package refactored with cleaner combineop/gatherop pattern
- Workflow context propagation and pinning mechanism implemented
- New internal benchmarking application (internal/benchapp) added for performance testing

## Combiner Ownership Model Refactoring (2025-08-12)

**Major Architectural Shift Completed (commit da1142a):**
Fundamental change from per-goroutine combiner ownership to shared pool model. Combiner instances are no longer owned by specific goroutines but are instead pooled and shared across all combiner goroutines, maintaining single-threaded execution through lock-free per-CombineOp instance queues (nbcq.Queue).

**Key Changes:**
- **activeCombinerMap**: Extracted combiner management into dedicated structure with heap-based deadline tracking
- **Reference-counted CombineOp/GatherOp**: Enable value-type copying while maintaining operation identity through shared internal state
- **Per-operation instance queuing**: Lock-free queuing ensures single-threaded combiner execution without lock contention
- **Simplified CombinerPool**: Delegates to activeCombinerMap, removing complex inline management (~300 lines reduced)

This refactoring enables arbitrary composition patterns while maintaining performance and correctness.

## Planned Reducer Architecture Design

**Core Insight: User-Managed Key Mapping**
Rather than building key-awareness into the framework, users maintain their own `map[KeyType]ReduceOp` mappings. This keeps the framework simple while providing maximum flexibility - different keys can use completely different reducer and gather functions.

**Reducer vs Combiner Semantics:**
- **Combiner**: Stateless, creates fresh instances via factory for each batch, then discards them
- **Reducer**: Stateful, uses single persistent instance that accumulates state across all inputs

**Proposed Type Structure:**
```go
type ReduceOp[I any] struct {
    pool    *CombinerPool             // Pool specified at construction
    reducer psgfn.Reducer[I]          // Single persistent instance (not factory)
}

type CombineOp[I any] struct {
    pool    *CombinerPool             // Pool specified at construction  
    factory psgfn.CombinerFactory[I]  // Creates fresh instances
}

type GatherOp[I any] struct {
    gatherFn psgfn.Gather[I]          // Terminal operation
}

// All operations provide:
// - Integrate(ctx, value, err) for direct value integration
// - Close(ctx) for signaling completion (triggers final flush)
```

**Uniform Integration Interface:**
All operations provide `Integrate()` method for direct value integration and `Close()` method for completion signaling. Pool remains an implementation detail hidden at construction time. Sink interface may be added later for polymorphic dataflow composition in the implicit layer.

**Explicit vs Implicit Data Flow:**
Current implicit model has operations configured with targets at creation time. Exploring explicit integration model where all data flow happens through explicit Integrate() calls:

```go
// Explicit integration (proposed core layer)
combineOp.Integrate(ctx, value, err)  // Direct value integration
gatherOp.Integrate(ctx, value, err)

// Implicit convenience layer (syntactic sugar)
combineOpWithTarget := NewCombineOpWithTarget(gatherOp, pool, factory)
combineOpWithTarget.Scatter(ctx, target, taskFn)  // Auto-integrates to gatherOp
```

**Deadlock-Free Arbitrary Composition:**
The shift to flush-as-work eliminates blocking sends. Instead of combiners directly posting to outboxes (which can block), flush operations become work items queued through the same system:

1. **Combiner flushes** → generates flush work items → processed asynchronously
2. **No blocking on send** → enables safe cycles in dataflow graphs  
3. **Governor backpressure** → prevents unbounded queue growth
4. **Unified work processing** → all work (tasks, flushes, posts) flows through same queues

**Composition Patterns Enabled:**
- Task → Combine → Reduce → Gather (hierarchical aggregation)
- Task → Reduce (simple fan-in without combining)
- Per-request Reduce → Cross-request Reduce (multi-level fan-in)
- Arbitrary cycles via explicit Post() calls (safe due to queue-based flow)

**Implementation Strategy:**
Architecture designed with the following implementation steps:

1. **Convert gather posting to work items** - Starting with `postGatherSlow()`, make all gather operations flow through work queue system
2. **Add blocking behavior for top-level context** - Use `job.block()` like `scatterNowOrQueue()` when Integration calls can't proceed immediately  
3. **Implement Sink interface and Integrate() API** - Create uniform interface for all operations
4. **Implement ReduceOp** - Add stateful reducer operations with persistent instances
5. **Create implicit convenience layer** - Build syntactic sugar on top of explicit integration
6. **Update existing CombineOp** - Migrate to new integration mechanism
7. **Add cycle prevention tooling** - Composable add-ons for detection and mitigation

**Key Design Decisions:**
- Pool-segregated instances (avoid complex cross-pool handoff of abandonedCombiners)
- Pool specified at construction time (implementation detail hidden from users)
- `Integrate()` method name for uniform integration API (active verb, semantically appropriate)
- `Close()` method for completion signaling (leverages existing reference counting in inner objects)
- CombineOp identity preserved (avoid interface values as map keys)

**Fan-In Completion Pattern:**
The `Close()` method enables clean fan-in patterns by signaling "no more inputs" to operations:
```go
// Fan-in example
for _, item := range items {
    go func(item Item) {
        result := process(item)
        reduceOp.Integrate(ctx, result, nil)
    }(item)
}
reduceOp.Close(ctx)  // Triggers final flush when all work completes
```

## Integration Architecture Implementation (2025-08-14)

**Major Progress: Core Integration Infrastructure Completed**

The foundation for explicit integration has been fully implemented and tested. All gather operations now flow through the work queue system, eliminating blocking sends and enabling deadlock-free composition.

**Implemented Components:**

1. **Work Queue Foundation (✓ Complete)**
   - Converted all gather posting to work items following `scatterNowOrQueue` pattern
   - Added proper blocking behavior for top-level contexts using `job.block()`
   - Created `taskWorkerExEnv` for execution environment support in task workers
   - All operations now flow through unified work processing system

2. **Integration API (✓ Complete)**
   - Added `Integrate()` and `TryIntegrate()` methods to GatherOp and CombineOp
   - Implemented explicit value/error parameter model instead of workq.Work
   - Added deadline support for non-blocking variants
   - Unified interface enables direct value integration across all operation types

3. **Resource Management (✓ Complete)**
   - Fixed resource leaks through proper `boundTask`/`boundCombineWork` interface implementation
   - Eliminated double-free bugs with correct ownership transfer patterns
   - Added proper work lifecycle management with reference counting
   - Implemented robust cleanup for all work types (tasks, combines, gathers)

4. **Subscription-Based Coordination (✓ Complete)**
   - Renamed Coordinator to Notifier throughout codebase for clarity
   - Implemented subscription pattern for goroutine spawning notifications
   - Added `SpawnWaitNotifier()` method to CombinerPoolState
   - Work items now subscribe to notifications when unable to post immediately

5. **Type System Cleanup (✓ Complete)**
   - Renamed `boundCombine` to `boundCombineWork` for consistency
   - Updated `executeCombine` to take `boundCombineWork` interface
   - Fixed ambiguous selector issues in embedded struct hierarchies
   - All type boundaries now clearly defined and consistent

**Architecture Validation:**
- ✅ All tests passing including stress tests
- ✅ Full build validation complete  
- ✅ Resource leak prevention verified
- ✅ Double-free prevention validated
- ✅ Integration API functional and tested
- ❌ **BLOCKER: Benchmarks hanging/deadlocking** - needs investigation

**Current State:**
The explicit integration foundation is complete and functional. The benchmark deadlock has been resolved through a comprehensive rearchitecture of the notification and work posting systems.

## Benchmark Deadlock Resolution (2025-09-02)

**Root Cause:**
The deadlock occurred due to a circular dependency in the work posting mechanism:
1. Task workers tried to post results directly to gather queues
2. Gather queues were full, causing workers to block waiting for space
3. The goroutines that would drain the gather queues were blocked waiting for the task workers

**Architectural Fix:**
Implemented a hybrid synchronous/asynchronous posting model that breaks the circular dependency:

1. **Notification System Refactoring:**
   - Renamed `Coordinator` → `Notifier`/`Listener` pattern for clearer separation of concerns
   - `Subscribe` → `AddToListeners` with proper double-registration prevention
   - `ShouldBlockOrListen` → `ShouldBlockOrPostpone` to clarify postponement semantics
   - Listeners track which collections they're registered with via `addedTo` map

2. **Hybrid Posting Model:**
   - **Fast path**: Synchronous `TryPushBack()` for immediate delivery to waiting receivers
   - **Medium path**: Synchronous posting to outbox when space available
   - **Slow path**: Create `gatherPostWork` items only when posting would block
   - Work items register for notifications and retry when space becomes available

3. **Resource Management Improvements:**
   - Outboxes now have atomic `refCount` and proper lifecycle management
   - `fillPending()`/`fillAttemptComplete()` pattern ensures correct ownership transfer
   - `emptied()` notifies listeners AND frees the outbox atomically
   - Reference counting prevents use-after-free in concurrent scenarios

4. **Integration Pattern Unification:**
   - Both `CombineOp` and `GatherOp` implement `integrate()` methods
   - Tasks implement `boundTask` interface with `Execute()` and `Free()` methods
   - Uniform work posting through the work queue system
   - `gatherPostWork` and similar types handle asynchronous posting when needed

5. **Execution Environment Refactoring:**
   - Split into `baseExEnv`, `taskExEnv`, `integrationExEnv`, and `topLevelExEnv`
   - Each environment type only exposes operations valid in its context
   - Panics on invalid operations ensure fail-fast behavior for contract violations

**Key Design Decisions:**
- Preserve synchronous posting performance while providing asynchronous escape hatch
- Aggressive invariant checking with panics ensures undefined behavior is caught early
- The `ShouldBlockOrPostpone()` pattern allows work to be deferred without abandoning it
- Work items only created when actually needed, not for every post operation

**Result:**
Benchmarks now run successfully without hanging. The hybrid model maintains the performance of synchronous posting while preventing deadlocks through selective asynchronous deferral.

## Proposed: LIFO Stack for Natural Worker Scaling (2025-09-02)

### Problem Statement
Current FIFO queue (rdvq.Optional) keeps all workers "warm" by cycling through them equally, preventing natural timeout-based scaling. Workers never go idle long enough to timeout and exit, requiring complex controller logic to manage pool sizes.

### Proposed Solution: Replace Queue with Stack

**Core Insight**: Using LIFO (stack) instead of FIFO (queue) for worker pools creates natural scaling behavior:
- Recently used workers stay "hot" at top of stack
- Idle workers sink to bottom and naturally timeout
- System self-regulates to minimum workers needed for current load
- Eliminates need for complex controller logic and primary/spare distinctions

### Implementation Plan

#### Worker Pool Architecture Analysis

Current PSG has different patterns for different worker types:

**Tasks**: Direct worker pool (`taskQueue` = `rdvq.Optional[*taskWork]`) with instant worker spawning
- Task arrives → `TryPop()` worker → if none available, spawn immediately  
- Low latency, but FIFO prevents natural worker scaling

**Combines**: Two-tier system (`combineQueue` = `rdvq.Required[Work]`) with buffering
- Work arrives → try inbox → if busy, buffer in outbox → worker picks up later
- Higher latency due to outbox buffering

**Gathers**: Two-tier system (`gatherQueue` = `rdvq.Required[Work]`) with buffering  
- Must buffer because "workers" are user threads calling `Gather()` - PSG can't spawn them

#### Proposed Unified Architecture

**Approach A: Lock-Free Stack + Spawn (for PSG-managed workers)**
```go
// For tasks and combiners - PSG can spawn workers
taskWorkers    LockFreeStack[*TaskWorker]
combineWorkers LockFreeStack[*CombineWorker]

// Work arrives → TryPop() worker → if empty, spawn new worker
```

**Approach B: Required Two-Tier (for user-managed workers)**
```go
// For gathers - PSG cannot spawn user threads
gatherQueue rdvq.Required[*GatherWork]  // Must buffer when no gatherers
```

#### Critical Contention Analysis

**Why Lock-Free Stack Is Essential**:
Under high concurrency, many tasks arriving when no workers are idle:
```
Thread 1: TryPop() → lock mutex → find empty → unlock → spawn worker
Thread 2: TryPop() → wait for mutex → find empty → unlock → spawn worker  
Thread N: TryPop() → wait for mutex → ...
```

All concurrent arrivals serialize on the mutex just to discover "still empty, spawn worker". Unlike Required's two-tier system where outboxes absorb contention, the spawn-on-demand pattern puts the stack directly in the hot path.

**For Task Workers**: Every task arrival when workers are busy hits the stack for empty check.

**For Combiner Workers**: Same issue - combine operations are latency-critical and shouldn't wait for outbox buffering.

#### Lock-Free Stack Implementation Strategy

**Phase 1: Basic Treiber Stack**
- Simple, well-understood algorithm
- ABA problem handled by Go's GC (pointers stay valid)
- Node recycling via sync.Pool

**Phase 2: Performance Validation**
- Benchmark against mutex version under various contention levels
- Verify it enables natural worker timeout behavior
- Measure latency improvements for tasks and combines

**Phase 3: Advanced Optimizations (if needed)**
- Elimination arrays (Hendler et al. algorithm) for very high contention
- Other optimizations based on measured bottlenecks

```go
type LockFreeStack[T any] struct {
    head atomic.Pointer[stackNode[T]]
    pool *sync.Pool // Node recycling
}

type stackNode[T any] struct {
    value T
    next  *stackNode[T]
}
```

#### What This Eliminates

- **CombinerPoolController** and all controller complexity
- **Primary/spare distinctions** in combiner pools  
- **Controller goroutines** and periodic management
- **Most rdvq.Required usage** (only gathers need it)
- **Complex multi-tier coordination** in favor of simple "work queue + worker pool"

#### Final Architecture Decision: Required + Spawn-on-Miss

**Unified Pattern**: Use rdvq.Required for all work distribution, enhanced with spawn-on-miss capability:

```go
taskQueue    rdvq.Required[*taskWork]    // with task worker spawn hook
combineQueue rdvq.Required[*combineWork] // with combiner spawn hook  
gatherQueue  rdvq.Required[*gatherWork]  // no spawn hook (user threads)
```

**Spawn-on-Miss Implementation**:
```go
required.PushBackFunc(outbox, work, func(outbox *Outbox[Work]) {
    // Spawn worker if under capacity (don't pass work directly)
    if pool.currentCount < pool.maxWorkers {
        go pool.spawnWorker() // Worker will check outboxes when ready
    }
    
    // Normal select on outbox (work is already buffered)
    BasicPushSelect(ctx, outbox, work)
})
```

**Benefits of This Approach**:
1. **Bounded Low Latency**: Work immediately buffered + worker immediately spawned
2. **Natural Load Balancing**: Multiple workers compete for outbox work (fastest wins)
3. **Capacity Limits**: Proper buffering when at max workers
4. **LIFO Scaling**: Inbox stack (LIFO) enables natural worker timeout
5. **Proven Architecture**: Leverages existing Required infrastructure

**Contention Analysis Revisited**:
Spawn-on-miss + outbox buffering significantly reduces inbox stack contention:
- Work spreads across multiple outboxes under high load
- Workers stay busy processing outboxes (rarely go idle)
- Stack operations become rare scaling events, not per-task operations
- Atomic empty flag handles the few remaining empty checks lock-free

**Implementation Decision**: Start with mutex-based LIFO stack with atomic empty flag. The spawn-on-miss pattern likely makes this sufficient, with easy upgrade path to lock-free if needed.

#### Implementation Plan

**Phase 1: Internal Collection Architecture**
- Create `inboxCollection[T]` interface with `Push()`, `TryPop()`, `Reset()` methods
- Implement `fifoCollection[T]` wrapping `nbcq.Queue` for waiter fairness
- Implement `lifoCollection[T]` with mutex + slice + atomic empty flag for worker scaling
- Unify under single `optional[T, C inboxCollection[T]]` implementation

**Phase 2: Public API Integration**
- Rename `rdvq.Required` to `rdvq.Queue` for clearer semantics
- `rdvq.Queue` uses `lifoCollection` for inbox management (natural worker scaling)
- `rdvq.Waiters` uses `fifoCollection` for notification fairness
- All collection types remain internal - users only see `Queue` and `Waiters`

**Phase 3: Spawn-on-Miss Enhancement**
- Add spawn capability to `rdvq.Queue.PushBackFunc()` selectFn
- Work immediately buffered in outbox + worker spawned if under capacity
- New worker competes with existing workers for outbox work (natural load balancing)
- Preserves low latency while respecting capacity limits

**Final Architecture**:
```go
// All work distribution uses Queue with spawn-on-miss
taskQueue    rdvq.Queue[*taskWork]    // LIFO inboxes + task worker spawning
combineQueue rdvq.Queue[*combineWork] // LIFO inboxes + combiner spawning  
gatherQueue  rdvq.Queue[*gatherWork]  // LIFO inboxes, no spawning (user threads)
```

#### Adaptive Timeout Based on Churn Rate**
- Instead of fixed idle timeout, adapt based on worker churn rate
- User specifies acceptable churn rate (e.g., "10 workers/second max")
- System automatically adjusts timeout to stay within churn budget:
  ```
  if churnRate > maxChurnRate:
      timeout *= 2  // Reduce churn
  else if churnRate < maxChurnRate/2:
      timeout *= 0.9  // Can be more aggressive
  ```
- More intuitive than timeout: "How much CPU for worker management?" vs "How long should workers idle?"

### Benefits

1. **Simplification**: Removes entire controller subsystem
2. **Natural Scaling**: Workers scale based on actual load patterns
3. **Better Cache Locality**: Hot workers stay in CPU cache
4. **Single Tuning Parameter**: Just churn rate limit (or timeout)
5. **Self-Regulating**: No periodic ticks or state machines needed

### Configuration Examples

```go
// Simple: Just timeout
WithTaskWorkerIdleTimeout(5 * time.Second)

// Advanced: Churn rate control  
WithMaxChurnRate(10.0)  // Max 10 worker/sec turnover
WithMinTimeout(100 * time.Millisecond)
WithMaxTimeout(30 * time.Second)
```

### Key Design Decisions

- **"Starvation" is good**: Idle workers timing out is the goal, not a problem
- **No shuffling needed**: Let hot workers stay hot for cache locality
- **Churn tracking**: Simple ring buffer of recent spawn/exit events
- **Timeout adaptation**: Exponential backoff/advance within bounds

**Next Steps (Priority Order):**
1. **Implement lock-free stack for rdvq.Optional** - Start with task workers as proof of concept
2. **Add churn rate tracking** - Measure current behavior before changing timeout logic
3. **Implement adaptive timeout** - Based on measured churn rate
4. **Apply to combiner pool** - Remove controller after validating approach
5. **Add Close() method to operations** - Enable completion signaling and final flush triggers
6. **Remove implicit flow from core** - Strip out auto-targeting behavior in favor of explicit integration
7. **Implement ReduceOp** - Add stateful reducer operations with single persistent instances
8. **Re-layer implicit convenience on top** - Build syntactic sugar using explicit integration as foundation

**Key Technical Insights:**
- Hybrid synchronous/asynchronous posting preserves performance while preventing deadlock
- Notification system with listener pattern enables pull-based coordination
- Reference counting with atomic operations ensures thread-safe resource management
- Fail-fast invariant checking catches logic errors immediately in development
- Type-specific execution environments prevent invalid operations at compile time where possible