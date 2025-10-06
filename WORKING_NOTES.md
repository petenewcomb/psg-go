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

## LIFO Stack Architecture Implementation (2025-09-06)

**COMPLETED: Full LIFO Stack Implementation (2025-09-08)**

Successfully completed the entire LIFO stack architecture implementation for natural worker scaling. All core components are implemented, tested, and lint-clean:

**Implementation Details:**

1. **Trait-based Generic Collection System (✓ Complete)**
   - Created `emptyInboxesTrait[T, C]` interface for unified collection abstraction
   - `inboxQueueTrait` provides FIFO behavior using `nbcq.Queue` for waiter fairness
   - `inboxStackTrait` provides LIFO behavior using mutex + slice + atomic empty flag for worker scaling
   - Generic `inboxOnlyQueue[T, C, CT]` implementation supports both collection types

2. **Collection Implementations (✓ Complete)**
   - **FIFO Collection**: Fast lock-free queue for notification fairness (waiters)
   - **LIFO Collection**: Mutex-protected stack with atomic empty flag for natural scaling (workers)
   - Atomic empty flag enables fast-path optimization to avoid lock contention
   - Both collections handle reset/cleanup properly for resource management

3. **API Restructuring (✓ Complete)**
   - Renamed `rdvq.Optional` → internal `inboxOnlyQueue` (no longer public)
   - Renamed `rdvq.Required` → `rdvq.Queue` for clearer semantics
   - `rdvq.Queue` uses LIFO `inboxStackQueue` for inbox management (natural worker scaling)
   - `rdvq.Waiters` uses FIFO `inboxQueueQueue` for notification fairness
   - Updated `nbcq.Queue.PopFront()` → `TryPopFront()` for consistency

4. **Comprehensive Testing (✓ Complete)**
   - Refactored tests to run on both FIFO and LIFO implementations
   - Single `TestInboxOnly()` function with sub-tests for each queue type  
   - All existing functionality preserved and validated for both collection types
   - Tests moved to same package (`rdvq`) to access internal types

**Architecture Benefits Achieved:**
- **Natural Worker Scaling**: LIFO behavior allows idle workers to naturally timeout 
- **Notification Fairness**: FIFO behavior ensures fair waiter processing
- **Performance Preservation**: Fast-path optimizations maintain performance
- **Zero Breaking Changes**: All existing public APIs work unchanged
- **Implementation Flexibility**: Trait system allows easy future collection types

**Current State:**
All LIFO stack infrastructure is complete and operational. The foundation is ready for the next phase:
- Task workers will naturally scale down using LIFO inbox behavior  
- Waiters maintain fair notification processing using FIFO behavior
- Controller complexity can be removed in favor of simple timeout-based scaling

**COMPLETED Implementation:**
1. ✅ **Updated job.go to use new rdvq.Queue API** - All references migrated successfully
2. ✅ **Fixed compilation issues** - All `PopFront()` → `TryPopFront()` calls updated  
3. ✅ **Added spawn-on-miss capability** - Enhanced `rdvq.Queue.PushBackFunc()` with worker spawning
4. ✅ **Applied to combiner pool** - Controller complexity removed, natural timeout-based scaling implemented
5. ✅ **Comprehensive testing** - Both FIFO and LIFO collections tested through unified test suite
6. ✅ **Documentation updated** - API docs reflect new architecture and consumer selection semantics
7. ✅ **Lint compliance** - All golangci-lint issues resolved

**Future Work (moved to TODO.md):**
- **Fix CombineOp.refInner related race** - See race.txt
- **Add Close() method to operations** - Enable completion signaling and final flush triggers
- **Remove implicit flow from core** - Strip out auto-targeting behavior in favor of explicit integration
- **Implement ReduceOp** - Add stateful reducer operations with single persistent instances
- **Re-layer implicit convenience on top** - Build syntactic sugar using explicit integration as foundation
- **Churn rate tracking** - Part of general observability improvements
- **Adaptive timeout** - Only if problems emerge with current approach

**Key Technical Insights:**
- Trait-based generics avoid interface overhead while providing abstraction
- Atomic flags enable fast-path optimizations in hot code paths
- LIFO vs FIFO choice affects system scaling behavior fundamentally
- Comprehensive test coverage essential when changing core infrastructure
- Hybrid synchronous/asynchronous posting preserves performance while preventing deadlock
- Notification system with listener pattern enables pull-based coordination
- Reference counting with atomic operations ensures thread-safe resource management
- Fail-fast invariant checking catches logic errors immediately in development
- Type-specific execution environments prevent invalid operations at compile time where possible

## CombineOp Race Condition Fix (2025-10-05)

### Problem Identified
The original `CombineOp` design had fundamental race conditions:
- Value type with mutable fields (`inner`, `innerPool`) that were accessed concurrently
- `combineOpMap` created contention bottleneck for high-volume per-request patterns
- Weak reference approach failed due to GC issues (no strong reference anchor)

### Root Cause Analysis
Attempting to optimize simultaneously for two wildly different use cases:
1. **Shared CombineOp**: One logical operation used across many goroutines (needs coordination)
2. **Per-request CombineOps**: Many independent operations (coordination is pure overhead)

The original design forced all cases through map coordination, pessimizing the common per-request pattern.

### Solution: User-Managed Lifecycle Pattern

Shift responsibility to users via explicit lifecycle management:

```go
type CombineOp[I, O any] struct {
    // Configuration
    id              combineOpID
    gatherOp        GatherOp[O]
    pool            *CombinerPool
    combinerFactory psgfn.CombinerFactory[I, O]
    
    // State management
    inner *combineOp[I, O]
    state combineOpState // uninitialized, initialized, closed
}

// Init: uninitialized → initialized
func (c *CombineOp[I, O]) Init(gatherOp, pool, factory) {
    if c.state != combineOpUninitialized {
        panic("invalid state")
    }
    // Get inner from pool, set config
    c.state = combineOpInitialized
}

// Close: initialized → closed
func (c *CombineOp[I, O]) Close() {
    if c.state != combineOpInitialized {
        if c.state == combineOpClosed {
            return // Idempotent
        }
        panic("not initialized")
    }
    // Unref inner, cleanup
    c.state = combineOpClosed
}

// Reset: uninitialized or closed → uninitialized
func (c *CombineOp[I, O]) Reset() {
    if c.state == combineOpInitialized {
        panic("must close before reset")
    }
    *c = CombineOp[I, O]{} // Clear to zero
}
```

### Key Design Decisions

1. **No more NewCombineOp** - Users manage lifecycle explicitly
2. **No combineOpMap** - Eliminates contention entirely
3. **User controls sharing** - Copy struct to share inner, manage refcounting carefully
4. **User controls pooling** - Can pool CombineOp structs if desired
5. **Strict state checking** - Panics on invalid transitions (programming errors)
6. **Reset is idempotent** - Safe to call multiple times when not initialized
7. **Close is idempotent** - Safe to call multiple times

### Usage Patterns

**Pattern 1: Per-request (independent)**
```go
var op CombineOp[Input, Output]
op.Init(gatherOp, pool, factory)
defer op.Close()
// Use op...
```

**Pattern 2: Pooled CombineOps**
```go
pool := &sync.Pool{
    New: func() any { return new(CombineOp[Input, Output]) },
}

op := pool.Get().(*CombineOp[Input, Output])
defer func() {
    op.Close()
    op.Reset()
    pool.Put(op)
}()
op.Init(gatherOp, combinerPool, factory)
// Use op...
```

**Pattern 3: Shared across goroutines**
```go
var sharedOp CombineOp[Input, Output]  
sharedOp.Init(gatherOp, pool, factory)
defer sharedOp.Close() // Called once!

// Copies share the inner pointer
for i := 0; i < 10; i++ {
    go func(op CombineOp[Input, Output]) {
        op.Scatter(...) // Uses shared inner
    }(sharedOp)
}
```

### Benefits

- **Zero contention** - No shared map, no locks on fast path
- **User control** - Explicit lifecycle management 
- **Flexibility** - Users choose sharing vs independence
- **Go idiomatic** - Follows standard Init/Close/Reset patterns
- **Omnipool compatible** - Reset() works with object pooling
- **Clear semantics** - State transitions are explicit and strict

### Evolution to Final Simplified Design (2025-10-06)

After implementing the Init/Close/Reset pattern and discovering it added significant complexity without proportional benefits, we simplified to a clean NewCombineOp + Close + Dup pattern.

#### Final Architecture

**Core Design:**
```go
type CombineOp[I, O any] struct {
    id    combineOpID
    inner *combineOp[I, O]  // Direct pointer to pooled inner object
}

// Construction: Creates and initializes in one step
func NewCombineOp[I, O](...) CombineOp[I, O] {
    innerPool := omnipool.For[combineOp[I, O]]()  // Type-keyed global pool
    inner := innerPool.Get()
    // Initialize inner with refCount = 1
    return CombineOp[I, O]{id: id, inner: inner}
}

// Duplication: Creates additional handle to same inner
func (c *CombineOp[I, O]) Dup() CombineOp[I, O] {
    inner := c.refInner()  // Increments refCount
    return CombineOp[I, O]{id: c.id, inner: inner}
}

// Cleanup: Required for proper pooling
func (c *CombineOp[I, O]) Close() {
    c.inner.unref(true)  // Decrements refCount, returns to pool when zero
}
```

**Key Simplifications:**
1. **Removed Init/Reset complexity** - No user-managed lifecycle states
2. **Removed innerPool caching** - Each NewCombineOp calls `omnipool.For` (but it's globally cached by type)
3. **Preserved value semantics** - CombineOp remains copyable
4. **No weak references** - Direct pointers with reference counting
5. **No combineOpMap** - Eliminated contention source entirely

#### Handle Tracking Problem and Solution

**The Problem:**
With Dup(), multiple CombineOp values share the same `inner` pointer but need independent Close() semantics:
```go
c1 := NewCombineOp(...)
c2 := c1.Dup()
c1.Close()  // Can't set inner.id = 0 yet, c2 still needs it
c2.Close()  // Now can set inner.id = 0
```

Original approach had race conditions between checking closed state and using the inner.

**The Solution: Map-Based Handle Tracking**

Track active handles using a map in the shared inner object:

```go
type combineOpHandleID int64

type combineOp[I, O any] struct {
    mu           sync.Mutex
    id           combineOpID
    
    // Separate refcounting for different purposes  
    handleIDs    map[combineOpHandleID]struct{}  // Active handle IDs
    nextHandleID combineOpHandleID               // Counter for handle IDs
    internalRefs int                              // Tasks, combiners, work items
    
    // ... rest of fields
}

type CombineOp[I, O any] struct {
    id       combineOpID
    handleID combineOpHandleID  // This specific handle's unique ID
    inner    *combineOp[I, O]
}

func NewCombineOp[I, O](...) CombineOp[I, O] {
    // ...
    handleID := inner.nextHandleID
    inner.nextHandleID++
    inner.handleIDs[handleID] = struct{}{}
    
    return CombineOp[I, O]{id: id, handleID: handleID, inner: inner}
}

func (c *CombineOp[I, O]) Dup() CombineOp[I, O] {
    c.inner.mu.Lock()
    if _, ok := c.inner.handleIDs[c.handleID]; !ok {
        panic(fmt.Sprintf("Dup() on closed handle %d", c.handleID))
    }
    handleID := c.inner.nextHandleID
    inner.nextHandleID++
    inner.handleIDs[handleID] = struct{}{}
    c.inner.mu.Unlock()
    
    return CombineOp[I, O]{id: c.id, handleID: handleID, inner: c.inner}
}

func (c *CombineOp[I, O]) Close() {
    c.inner.mu.Lock()
    if _, ok := c.inner.handleIDs[c.handleID]; !ok {
        panic(fmt.Sprintf("Double close on handle %d", c.handleID))
    }
    delete(c.inner.handleIDs, c.handleID)
    
    if len(c.inner.handleIDs) == 0 {
        c.inner.id = 0  // All handles closed - mark as closed
        // Don't clear the map - reuse it next time from pool!
    }
    // ... cleanup logic with separate refcounting
}
```

#### Benefits of Final Design

**Safety:**
- Detects double-close with clear error messages including handle ID
- Detects Dup() on closed handles  
- Detects use-after-close via inner.id = 0 check
- Handles value copies correctly (they can't close because handleID not in map)

**Performance:**
- No allocation for map per operation (reused from pool)
- No global coordination structures
- Type-keyed global pools via omnipool.For
- Fast path for single-handle operations

**Debuggability:**
- Clear error messages with specific handle IDs
- Unlimited number of Dups
- Map reused across pool cycles (amortized allocation cost)

**Potential Optimization (Added to TODO.md):**
Consider size threshold for handleIDs map to prevent pathologically large maps from staying in pool after operations that create thousands of Dups.

#### Usage Patterns

**Per-request pattern (most common):**
```go
c := NewCombineOp(gatherOp, pool, factory)
defer c.Close()
// Use c for this request...
```

**Async operations pattern:**
```go
c := NewCombineOp(gatherOp, pool, factory)
defer c.Close()

// Create handles for async operations
handle1 := c.Dup()
handle2 := c.Dup()

go func() {
    defer handle1.Close()
    handle1.Integrate(ctx, value1, err1)
}()

go func() {
    defer handle2.Close()  
    handle2.Integrate(ctx, value2, err2)
}()
```

**File descriptor semantics:**
The Dup()/Close() pattern follows familiar file descriptor duplication semantics - each handle must be closed independently, sharing the same underlying resource.

### Final Implementation Status (2025-10-06)

The map-based handle tracking has been fully implemented and committed. The solution successfully eliminates all race conditions while providing robust error detection and excellent debugging capabilities.

**Key Implementation Details:**
- Each CombineOp has a `handleID` from a global counter
- The inner `combineOp` maintains a `handleIDs` map tracking active handles
- `unref()` takes a handleID parameter (0 for internal refs, non-zero for Close)
- `refInner()` validates both that handles exist and the specific handle is valid
- The handle map is cleared and reused across pool cycles
- Reference counting is completely separate from handle tracking
- `needUnlock` pattern in `unref()` ensures mutex is unlocked even on panic

**Performance Considerations:**
- Map allocation is amortized across pool lifetime
- No global coordination structures (eliminated combineOpMap)
- Future optimization possible: atomic-only mode for high-concurrency scenarios (added to TODO.md)

**Current Architecture Benefits:**
- Zero contention for per-request patterns
- Detects all forms of handle misuse with clear error messages
- Maintains Go value semantics
- Clean separation between user-facing handles and internal references
- Robust pooling with proper cleanup

The implementation is production-ready with the race condition fully resolved.
