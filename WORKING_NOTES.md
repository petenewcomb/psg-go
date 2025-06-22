# PSG-Go Combiner Branch Working Notes

This document contains working notes and context for development on the `combiner` branch. It tracks current status, key insights, implementation plans, and architectural understanding gained during development.

## Current Status
- **Branch**: `combiner`
- **Core deadlock**: ✅ Fixed - combiners now process flush-generated work before exit
- **Performance**: ✅ Major improvements (12.6% geomean across benchmarks)
- **Debug cleanup**: ✅ Production-ready code, ~300 lines of debug output removed
- **Naming consistency**: ✅ Completed (secondary → spare, committed in a6e25d7)
- **Work reference tracking**: ✅ Fixed flush work imbalance with queueFlush() (committed in 5e1017d)
- **Scale-to-zero**: ✅ Root cause identified - benchmark MinConcurrency setting prevents goroutine exit
- **Gather liveness**: 🚧 High tasks/op issue identified, solution planned
- **Next**: 🚧 Implement gather liveness optimization to fix responsiveness

## Key Insights from Previous Work

### Work Reference Tracking Issue ✅ Resolved
**Problem**: Flush operations were queued without proper work reference tracking
- `queueWork(flushAll)` and `queueWork(nextBCToFlush.FlushFn)` didn't increment job work count
- Flush execution calls `emit()` which does increment work references
- Created imbalance preventing proper job shutdown coordination

**Solution**: Implemented `queueFlush()` function (committed in 5e1017d)
- Tracks flush operations as legitimate work that must complete before job finish
- Balances work references: increment when queued, decrement when complete
- Fixes work reference imbalances that contributed to goroutine hang issues

### Scale-to-Zero Investigation ✅ Diagnostic Available
**Issue**: Spare goroutines spinning indefinitely instead of exiting when idle
**Root Cause**: Benchmark test forces minimum concurrency equal to combiner limit
- `WithConcurrencyBounds(max(0, combinerLimit), combinerLimit)` prevents scale-to-zero
- Controller can't allow goroutines to exit due to minimum concurrency requirement

**Verification Plan**: Temporarily change benchmark to `WithConcurrencyBounds(0, combinerLimit)`
- If hangs disappear: confirms forced minimum concurrency was the root cause
- If hangs persist: indicates deeper work reference or coordination issues remain
- Diagnostic tool to separate scale-to-zero problems from other hang causes

## Key Architectural Insights

### Combiner Lifecycle Management
- **Critical**: Combiners must process flush-generated work before exit
- **Work Item Lifecycle**: IncrementWork() → postGather() → queueGather() → DecrementWork()
- **Exit Flow**: Main loop → flushAll() → processWork() → exit
- **Deadlock Prevention**: Initialize `lastIDProcessed = workCounter` to prevent infinite loops

### RDVQ Infrastructure
- Complete lock-free queue system in internal/rdvq/
- Two-tier delivery: direct → outbox → shared channel
- Outbox management per goroutine via outboxmap.go
- Replaced old internal/waitq/ package

### Performance Characteristics
- Lower combiner limits (1-8) perform optimally: 20-50% improvements
- High combiner limits (12-24) show significant slowdowns and instability
- Short flush periods benefit most from lock-free improvements

## Implementation Plan

### Phase 1: Gather Liveness Optimization 🚧 (In Progress)
**Problem**: High tasks/op values (24-3399 vs normal 16-20) indicate poor liveness - gather goroutines drain work queues too aggressively instead of staying responsive.

**Root Cause**: `processOutstandingWork()` drains entire work queue in one gather operation, causing batching behavior that hurts latency.

**Solution Plan**:
- **Top-level gathers** (not in gather context): Process exactly one work item OR get one gather from queue
  - If work queue has items: process one work item, return immediately  
  - If no work: get one gather from gather queue (and maybe process it)
  - Limits batching, improves responsiveness

- **Backpressure gathers** (already in gather context): Only handle gather queue operations
  - Get 1-2 gathers max from gather queue (handle orphans)
  - Queue those gathers as work items (don't process directly)
  - Never process work items during backpressure
  - Stay focused on gather → work queue transfer

**Implementation Status**: Plan defined, ready for implementation

### Phase 2: Instrumentation Infrastructure (Deferred)
**Next Steps**:
- Design CombinerPoolSnapshot struct with metrics
- Implement WithDetailedStateEvents option and callback mechanism
- Hook instrumentation into lifecycle methods (GoroutineStarted, GoroutineExited, updateStats)
- Replace hardcoded cpDebug with instrumentation-based debug

### Phase 3: Zero-Scaling Tests (Deferred)
- Verify combiner pools scale to zero goroutines when idle (min concurrency = 0)
- Event-driven verification using instrumentation API
- Multiple workload patterns: burst/idle, gradual ramp-down, intermittent work

## Key Files
- `combinerpool.go` - Main combiner pool logic, lifecycle management
- `internal/cpstate/state.go` - Pool state management, controller logic
- `internal/rdvq/` - Lock-free queue infrastructure
- `internal/jobstate/` - Work counting and job state management