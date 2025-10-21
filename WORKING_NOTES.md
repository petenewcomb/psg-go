# PSG-Go Combiner Branch Working Notes

This document contains working notes and context for development on the `combiner` branch.

Major combiner architecture work is complete. Branch is now in cleanup and finalization phase.

## Architecture Highlights (Completed)

**Core Infrastructure:**
- LIFO stack architecture for natural worker scaling (eliminates controller complexity)
- Leakguard package for safe resource handle management with finalizer-based leak detection
- Demand-based worker spawning with token tracking for precise spawn chaining
- Hardware-accelerated 128-bit atomics in nbcq for improved performance
- Orphan task buffering with notification infrastructure

**Key Design Patterns:**
- Reference-counted CombineOp/GatherOp handles with Dup()/Close() semantics
- Pool-segregated combiner instances to avoid complex cross-pool handoff
- Trait-based generic collection system (FIFO for fairness, LIFO for scaling)
- Subscription-based coordination with Notifier/Listener pattern

## Task Worker Demand-Based Spawning (2025-10-19)

**Problem Solved:**

Fixed livelock in `taskPostWork.Execute()` where blocking path would call spawn-on-demand, but if spawn failed due to concurrency limit, the code would block indefinitely with no retry opportunity.

**Solution: Token-Based Demand Tracking**

Implemented demand tracking where:
- Tasks register demand tokens when they can't immediately post
- Spawning workers check for unmet demand on completion and chain spawns
- Demand is cancelled when tasks successfully post or give up
- Orphaned tasks wrapped with demand tracking for consistent handling

**Key Components:**

1. **Demand Token Queue** (`unmetTaskWorkerDemand nbcq.Queue[*unmetDemandToken]`):
   - Tracks tasks waiting for workers with unique token IDs
   - Each token has `stillNeeded` flag for safe cancellation across retries
   - Tokens pooled via omnipool for zero allocation after warmup

2. **unmetDemand Wrapper Struct**:
   ```go
   type unmetDemand struct {
       token *unmetDemandToken
       id    unmetDemandID
   }

   func (d *unmetDemand) Cancel() {
       if d.token == nil { return }
       d.token.mu.Lock()
       defer d.token.mu.Unlock()
       if d.token.id == d.id {
           d.token.stillNeeded = false
       }
   }
   ```

3. **Spawn Chaining**:
   - When worker secures a task, checks demand queue
   - Spawns another worker if there's unmet demand
   - Chains spawns to satisfy all waiting tasks
   - Skips cancelled tokens (task already posted or gave up)

**Benefits:**
- Eliminates livelock - tasks no longer wait indefinitely when spawn capacity becomes available
- Precise demand tracking with safe cancellation
- Spawn chaining ensures all waiting tasks eventually get workers
- Zero allocation after warmup

## Scatter Work Persistence Refactoring (2025-10-19)

**Problem Identified - Demand Signal Livelock:**

The current scatter architecture creates and destroys `taskPostWork` objects on every scatter operation. This causes **correctness issues** with the demand-based worker spawning because work items remain alive across retries but recreate their underlying post work each time:

**The Flow:**
1. `combineScatterWork` is created, calls `target.scatter()`
2. `scatter()` creates NEW `taskPostWork` from pool
3. `taskPostWork.Execute()` registers `unmetDemand` if posting fails and work needs postponement
4. `taskPostWork.Execute()` returns (not posted)
5. `taskPostWork.Free()` is called immediately, which **cancels the demand** via `defer demand.Cancel()`
6. `scatter()` returns (not started)
7. **`combineScatterWork` stays alive** - it goes into workq as postponed work (NOT back to pool)
8. Later, workq retries: `combineScatterWork.Execute()` is called AGAIN
9. This calls `scatter()` again, which creates a **NEW `taskPostWork`** from pool
10. **LIVELOCK**: The old demand signal was cancelled in step 5, new one hasn't been created yet

The demand tokens are being created and destroyed on each retry attempt, even though the parent work object (`combineScatterWork`) remains alive across all retry attempts.

**Current Architecture:**

```
combineScatterWork (stays alive across retries)
  → Execute() called attempt #1
    → calls target.scatter()
      → creates NEW taskPostWork from pool
      → taskPostWork.Execute() [registers demand token #1]
      → taskPostWork.Free() [cancels demand token #1]  ← DEMAND LOST!
    → returns (not started)

  → Execute() called attempt #2 (retry)
    → calls target.scatter()
      → creates NEW taskPostWork from pool (demand token #1 is gone!)
      → taskPostWork.Execute() [might register demand token #2]
      → taskPostWork.Free() [cancels demand token #2]  ← DEMAND LOST AGAIN!
    → returns (not started)
```

**Key Insight:**

The parent work objects (`combineScatterWork`, `gatherScatterWork`) already persist across retries by design - they stay in the workq postponed queue. We need the underlying `taskPostWork` to persist with the same lifetime.

**Solution: Embed by Value**

If we embed the scatter work hierarchy using struct composition, the `taskPostWork` lives and dies with its parent:

```
combineScatterWork (stays alive until success) {
    taskPoolScatterWork (embedded by value) {
        taskScatterWork (embedded by value) {
            demand unmetDemand  // PERSISTS until combineScatterWork freed!
        }
    }
}
```

When `combineScatterWork.Execute()` is called multiple times (retries), the same embedded `taskScatterWork` is reused, and **the demand signal persists across all attempts**!

### Proposed Design

**Type Hierarchy:**

```go
// Layer 1: Base task scatter work (renamed from taskPostWork)
type taskScatterWork struct {
    jobWork
    job      *Job
    taskWork *taskWork
    demand   unmetDemand  // Persists across Execute() calls!
}

func (w *taskScatterWork) Init() {
    // One-time initialization - no arguments
}

// No Reset() needed - pool uses default zero value reset

func newTaskScatterWork(job *Job, group workq.GroupID) *taskScatterWork {
    w := taskScatterWorkPool.Get()
    w.jobWork.Init(group, job)
    w.job = job
    return w
}

func (w *taskScatterWork) SetTask(taskFn boundTask, completedFn func()) {
    if w.taskWork == nil {
        w.taskWork = w.job.newTaskWork(w.Group(), taskFn, completedFn)
    } else {
        w.taskWork.task = taskFn
        w.taskWork.completedFn = completedFn
    }
}

func (w *taskScatterWork) Execute(ctx, ex) error {
    registerDemand := func() {
        if w.demand.token == nil {
            w.demand = w.job.registerTaskWorkerDemand()
        }
    }
    // Existing execute logic...
}

func (w *taskScatterWork) Free() {
    // Free sub-objects before putting to pool
    w.demand.Cancel()
    if w.taskWork != nil {
        w.taskWork.Free(w.job)
    }
    w.Close(w.job)
    taskScatterWorkPool.Put(w)  // Pool calls Reset
}

// Layer 2: TaskPool wrapper adds in-flight tracking
type taskPoolScatterWork struct {
    taskScatterWork      // embedded by value
    pool                *TaskPool
    inFlightIncremented bool
}

func (w *taskPoolScatterWork) Init() {
    w.taskScatterWork.Init()
}

func newTaskPoolScatterWork(pool *TaskPool, group workq.GroupID) *taskPoolScatterWork {
    w := taskPoolScatterWorkPool.Get()
    w.pool = pool
    w.taskScatterWork.jobWork.Init(group, pool.job)
    w.taskScatterWork.job = pool.job
    return w
}

func (w *taskPoolScatterWork) Execute(ctx, ex, deadline) error {
    wb := workq.WaitBehavior{
        BlockBehavior: w.pool.job.protoBB,
        ShouldWait: func() bool { return w.pool.scatterShouldWait(w) },
    }

    defer func() {
        if !ex.Started() && w.inFlightIncremented {
            w.pool.decrementInFlight()
            w.inFlightIncremented = false
        }
    }()

    return workq.ExecuteOrWait(ctx, ex, deadline, &w.pool.notifier, wb,
        func(ctx context.Context, ex workq.Execution) error {
            return w.taskScatterWork.Execute(ctx, ex)
        })
}

func (w *taskPoolScatterWork) Free() {
    w.taskScatterWork.Free()
    taskPoolScatterWorkPool.Put(w)
}

// Layer 3: Combine/Gather wrappers
type combineScatterWork struct {
    jobWork
    pool     *CombinerPool
    deadline time.Time
    target   TaskPoolOrJob
    work     taskPoolScatterWork  // embedded by value - persists!
    task     boundTask
}

func (w *combineScatterWork) Init() {
    w.work.Init()
}

func newCombineScatterWork(
    pool *CombinerPool,
    group workq.GroupID,
    deadline time.Time,
    target TaskPoolOrJob,
    task boundTask,
) *combineScatterWork {
    w := combineScatterWorkPool.Get()
    w.jobWork.Init(group, pool.job)
    w.pool = pool
    w.deadline = deadline
    w.target = target
    w.task = task

    // Configure embedded work based on target type
    if tp, ok := target.(*TaskPool); ok {
        w.work.pool = tp
        w.work.taskScatterWork.job = tp.job
        w.work.taskScatterWork.jobWork.Init(group, tp.job)
    } else {
        j := target.(*Job)
        w.work.taskScatterWork.job = j
        w.work.taskScatterWork.jobWork.Init(group, j)
    }

    return w
}

func (w *combineScatterWork) Execute(ctx, ex) error {
    w.work.taskScatterWork.SetTask(w.task, nil)

    workFn := func(ctx context.Context, ex workq.Execution) error {
        return w.work.Execute(ctx, ex, w.deadline)
    }

    defer func() {
        if ex.Started() {
            w.task = nil
        }
    }()

    bb := w.pool.job.protoBB
    if bb.ShouldBlock(ctx) != nil {
        jobGovernedWorkFn := func(ctx context.Context, ex workq.Execution) error {
            return w.pool.job.governor.Execute(ctx, ex, w.deadline, bb, workFn)
        }
        return w.pool.governor.Execute(ctx, ex, w.deadline, bb, jobGovernedWorkFn)
    }
    return workFn(ctx, ex)
}

func (w *combineScatterWork) Free() {
    w.work.Free()
    if w.task != nil {
        w.task.Free()
    }
    w.Close(w.pool.job)
    combineScatterWorkPool.Put(w)
}
```

**API Changes:**

1. **TaskPoolOrJob interface** - Remove `scatter()` method:
   ```go
   // OLD
   type TaskPoolOrJob interface {
       getJob() *Job
       scatter(ctx, group, ex, deadline, *taskPoolScatterWork, boundTask) error
   }

   // NEW
   type TaskPoolOrJob interface {
       getJob() *Job
   }
   ```

2. **Rename types**:
   - `taskPostWork` → `taskScatterWork`
   - Remove `*taskPoolScatterWork` argument from interfaces

**Benefits:**

1. **Fixes Livelock** - Demand signals persist across retry attempts
2. **Zero allocation for embedded structs** - No get/put during retries
3. **Better cache locality** - Embedded structs keep data together
4. **Cleaner API** - No passing pointers through interface
5. **Simplified interface** - `TaskPoolOrJob` no longer needs `scatter()` method
6. **Correct lifetimes** - Demand lives exactly as long as work attempting the scatter

**Implementation Plan:**

1. Rename `taskPostWork` → `taskScatterWork` throughout codebase
2. Move `demand unmetDemand` field into `taskScatterWork` struct
3. Remove `defer demand.Cancel()` from `Execute()`, move to `Free()`
4. Add `SetTask()` method to `taskScatterWork`
5. Refactor `taskPoolScatterWork` to embed `taskScatterWork`
6. Add `Execute()` and `Free()` methods to `taskPoolScatterWork`
7. Remove `scatter()` method from `TaskPoolOrJob` interface
8. Update `combineScatterWork` to embed `taskPoolScatterWork` by value
9. Update `combineScatterWork` constructor and methods
10. Update `gatherScatterWork` to follow same pattern
11. Update all call sites
12. Verify demand tokens persist across retries
13. Run tests and benchmarks

**Files Affected:**
- `job.go` - Core taskScatterWork implementation
- `scatter.go` - TaskPoolOrJob interface
- `taskpool.go` - taskPoolScatterWork embedding
- `combineop.go` - combineScatterWork embedding
- `gatherop.go` - gatherScatterWork embedding
- `combiner_test.go` - Test code

**Current Status:** ~~Design documented, ready to commit current state and begin refactoring.~~

**PIVOT (2025-10-20):** Abandoned embedding approach in favor of composition/delegation pattern (see below).

## Scatter Work Refactoring - Composition/Delegation Pattern (2025-10-20)

**Decision:** After exploring the embedding approach above, pivoted to a cleaner composition/delegation pattern using the `workq.Work` interface. This avoids the complexity of embedded struct pooling while still achieving persistent demand signals.

**Implementation:**

All scatter work types now use composition via the `workq.Work` interface instead of struct embedding:

```go
// Base interface for scatter targets
type TaskPoolOrJob interface {
    getJob() *Job
    newScatterWork(group workq.GroupID, deadline time.Time, task boundTask) workq.Work
}

// Each layer wraps the previous using interface delegation:

// Layer 1: taskWork + taskPostWork (job.go)
type taskPostWork struct {
    jobWork
    job    *Job
    task   *taskWork
    demand unmetDemand  // Persists across Execute() retries!
}

func (j *Job) newScatterWork(group, deadline, task) workq.Work {
    taskWork := j.newTaskWork(group, task, nil)
    return j.newTaskPostWork(group, deadline, taskWork)
}

// Layer 2: TaskPool concurrency limiting (taskpool.go)
type taskPoolScatterWork struct {
    workq.Work              // Delegates to wrapped work
    pool                *TaskPool
    deadline            time.Time
    inFlightIncremented bool
}

func (p *TaskPool) newScatterWork(group, deadline, task) workq.Work {
    taskWork := p.job.newTaskWork(group, task, p.decrementInFlightFn)
    taskPostWork := p.job.newTaskPostWork(group, deadline, taskWork)
    return newTaskPoolScatterWork(p, deadline, taskPostWork)
}

func (w *taskPoolScatterWork) Execute(ctx, ex) error {
    // Uses ExecuteOrWait with ShouldWait callback
    return workq.ExecuteOrWait(ctx, ex, w.deadline, &w.pool.notifier, wb,
        func(ctx context.Context, ex workq.Execution) error {
            return w.Work.Execute(ctx, ex)  // Delegate
        })
}

// Layer 3: Governor backpressure (combineop.go, gatherop.go)
type combineScatterWork struct {
    workq.Work      // Delegates to wrapped work
    pool     *CombinerPool
    deadline time.Time
}

func (w *combineScatterWork) Execute(ctx, ex) error {
    workFn := w.Work.Execute  // Delegate to wrapped work
    bb := w.pool.job.protoBB
    if bb.ShouldBlock(ctx) != nil {
        // Double governor control (job + pool)
        jobGovernedWorkFn := func(ctx, ex) error {
            return w.pool.job.governor.Execute(ctx, ex, w.deadline, bb, workFn)
        }
        return w.pool.governor.Execute(ctx, ex, w.deadline, bb, jobGovernedWorkFn)
    }
    return workFn(ctx, ex)
}
```

**Key Layering:**

1. **taskWork**: Actual task execution wrapper
2. **taskPostWork**: Posts task to queue with demand tracking
3. **taskPoolScatterWork**: Adds TaskPool concurrency limiting
4. **combineScatterWork/gatherScatterWork**: Adds governor backpressure control

Each layer wraps the previous via `workq.Work` interface delegation. The workq framework keeps work items alive across retries, so demand signals in `taskPostWork` persist properly.

**Benefits:**
- Clean interface delegation instead of complex embedding
- Demand signals persist correctly (work items stay alive during retries)
- Each layer has single responsibility
- Object pooling works cleanly (one pool per type)
- Deadline propagates through all layers

## Deadline and ExecuteOrWait Refactoring Analysis (2025-10-20)

**Issue 1: taskPostWork Deadline Not Used**

Currently `taskPostWork.newTaskPostWork()` receives a `deadline` parameter but doesn't store or use it. All other scatter work types store and use their deadlines:

- **taskPoolScatterWork** (taskpool.go:148): Passes deadline to `ExecuteOrWait`
- **combineScatterWork** (combineop.go:577,579): Passes deadline to `governor.Execute`
- **gatherScatterWork** (gatherop.go:324): Passes deadline to `governor.Execute`
- **taskPostWork** (job.go:1148): Receives deadline but doesn't store/use it ❌

**Fix needed:** Add `deadline time.Time` field to `taskPostWork` struct and use it appropriately in the blocking post logic (likely pass to `BasicPushSelect` via context with deadline).

**Issue 2: Duplicate Wait/Block Logic**

Multiple places implement similar wait/block/postpone patterns:

1. **workq.ExecuteOrWait** (internal/workq/wait.go:28-78): Generic implementation
   - Checks `ShouldWait()` callback in loop
   - Non-blocking path: subscribe to listeners, return for postponement
   - Blocking path: calls `BlockFunc` with confirm callback
   - Clean abstraction via `WaitBehavior` struct

2. **workq.Governor.Execute** (internal/workq/governor.go:35-44): Thin wrapper
   ```go
   func (g *Governor) Execute(ctx, ex, deadline, behavior, workFn) error {
       wb := WaitBehavior{
           BlockBehavior: behavior,
           ShouldWait:    g.upstreamShouldWaitFn,
       }
       return ExecuteOrWait(ctx, ex, deadline, &g.upstream, wb, workFn)
   }
   ```

3. **taskPostWork.Execute** (job.go:1018-1099): Custom implementation
   - 80+ lines of similar logic
   - Manually implements try/subscribe/block pattern
   - Calls `ex.ShouldBlockOrPostpone()`, `meta.ShouldBlock()`, `ex.AddToListeners()`
   - Custom queue posting with `TryPushBack`, `PushBackFunc`, `BasicPushSelect`
   - Demand registration logic interwoven

**Refactoring Opportunity:**

The core wait/block/postpone pattern could potentially be extracted. However, `taskPostWork` has unique requirements:

- **Custom "try" operation**: `TryPushBack()` instead of simple boolean check
- **Side effects on try**: Registers demand callback via `bufferedFn` parameter
- **Demand tracking**: Needs to register demand token when post fails
- **Custom blocking**: Uses `PushBackFunc` + `BasicPushSelect` instead of standard notifier wait

**Possible approaches:**

1. **Short term (pragmatic)**: Keep current logic but add deadline field and use it in `BasicPushSelect`

2. **Medium term (refactor)**: Extract common pattern but with more flexibility than current `ExecuteOrWait`:
   ```go
   // Enhanced callback structure
   type TryPostBehavior struct {
       TryPost   func() bool           // Returns true if succeeded
       OnBuffered func()                // Called when post buffered but worker needed
       OnFailed   func()                // Called when try fails (for demand registration)
   }

   // Could wrap in ExecuteOrWait-style function
   func ExecuteOrWaitForPost(ctx, ex, deadline, notifier, behavior, blockFn) error
   ```

3. **Long term (generalize)**: Make `ExecuteOrWait` more flexible to handle the queue posting pattern

**Recommendation:**
- Start with #1: Add deadline field and fix immediate issue
- Consider #2 if similar patterns emerge elsewhere
- The duplication is real but may not be worth over-abstracting yet

**Files Affected:**
- job.go (taskPostWork deadline field and usage)
- Potentially internal/workq/wait.go if pursuing refactoring

## Orphan Renotify Allocation Leak (2025-10-20)

**Problem:** The `orphanedTaskRenotify` fix for the nil pointer panic introduces an allocation leak similar to earlier issues.

**Current Implementation:**

```go
type orphanedTaskRenotify struct {
    id         orphanedTaskID
    work       *orphanedTaskWork
    RenotifyFn rdvq.RenotifyFunc  // Just a func(), no Free capability
}

// Usage in job.go:996-1002:
orphan := newOrphanedTaskWork(j, orphanedTask)
orphanRenotify := newOrphanedTaskRenotify(orphan)
j.orphanedTasks.PushBack(orphan)
j.orphanWaiters.Notify(orphanRenotify.RenotifyFn)  // ← Only function is passed
// orphanRenotify object is never freed! ❌
```

**Root Cause:**

1. `orphanRenotify` is allocated from pool
2. Only the `RenotifyFn` closure is passed to `Notify()`
3. The waiters/notifier system only knows about the function, not the object
4. When renotify is consumed/discarded, there's no way to call `orphanRenotify.Free()`
5. The `orphanedTaskRenotify` object leaks permanently

**Solution: Renotifier Interface**

Change from `RenotifyFunc` function type to `Renotifier` interface:

```go
// OLD (rdvq package)
type RenotifyFunc func()

// NEW
type Renotifier interface {
    Renotify()
    Free()
}
```

Then the waiters/notifier infrastructure can:
- Call `Renotify()` when notification is delivered
- Call `Free()` when renotifier is consumed, replaced, or discarded
- Allow pooled objects to be properly reclaimed

**Implementation Impact:**

This requires changes throughout the rdvq notification system:
- `rdvq.Notifier.Notify()` signature changes from `func(RenotifyFunc)` to `func(Renotifier)`
- `rdvq.Waiters` needs to track and free renotifiers
- All callsites need to adapt to interface instead of function
- Backward compatibility: provide convenience wrapper for function-based renotifiers

**Files Affected:**
- internal/rdvq/notifier.go - Change Notify signature
- internal/rdvq/waiters.go - Add renotifier lifecycle management
- job.go - Update orphanedTaskRenotify to implement interface
- All callsites of Notify() throughout codebase

**Workaround (Short-term):**

Applied self-freeing pattern to `orphanedTaskRenotify.renotify()` (job.go:861-864):
```go
func (r *orphanedTaskRenotify) renotify() {
    r.work.registerDemand(r.id)
    r.Free()  // Free self after invocation
}
```

This matches the pattern already used in `wrappedRenotify.renotify()` (internal/rdvq/notifier.go:89-94).

**Note:** Both `orphanedTaskRenotify` and `wrappedRenotify` should be migrated to the Renotifier interface in the long-term refactoring. This will allow infrastructure to free unused renotifiers even when they're replaced/discarded without being invoked.

## Benchmark Deadlock Issue (2025-10-20)

**Status:** After fixing nil pointer panic and allocation leaks, benchmarks still experiencing intermittent deadlocks.

**Evidence:** bench_20251020T105451Z.txt shows benchmark running successfully for ~463 seconds, then hanging with 5 goroutines stuck in select for 5 minutes before timeout.

### Complete Goroutine State Analysis

**Application Goroutines (11 total):**

| Goroutine | State | Role | What it's doing |
|-----------|-------|------|-----------------|
| 1 | chan receive (7m) | Test framework | Waiting for benchmark to complete |
| 18 | chan receive (5m) | Benchmark runner | Waiting for test iteration |
| **19415** | **runnable** | **Benchmark test** | **Freeing work after TryScatter** |
| **19152** | **runnable** | **Combiner pool worker** | **Freeing work after combine execution** |
| **19151** | **runnable** | **Combiner pool worker** | **TryScatter → TryPushBack (in trace log)** |
| 19150 | select (5m) | Task worker | **STUCK** trying to post combine result |
| 19416 | select (5m) | Task worker | **STUCK** trying to post combine result |
| 19434 | select (5m) | Task worker | **STUCK** trying to post combine result |
| 19417 | select (5m) | Sentinel | Waiting on job.Done() or ctx.Done() |
| 19153 | select (5m) | Sentinel | Waiting on job.Done() or ctx.Done() |

**Critical Observation: Runnable Goroutines**

Three goroutines are **runnable** but haven't made progress in 5+ minutes:
- **19415**: Benchmark test cleaning up after failed TryScatter
- **19152**: Combiner worker in `InFlightCounter.Decrement` cleanup
- **19151**: Combiner worker attempting `TryPushBack` in task queue (currently in trace logging)

This is **not a pure deadlock** - it's a **livelock**. The runnable goroutines should be making progress but aren't being scheduled or are spinning ineffectively.

### Deadlock Pattern

**Three task workers stuck in identical state:**

```
Task Worker (19150, 19416, 19434)
  → combineTask.Execute (completed)
  → combineOp.integrate (posting result)
  → combinePostWork.Execute
  → PushBackFunc on combiner work queue
  → select (waiting on outbox) ← STUCK HERE
```

All three are trying to push `combineWork` results back to the combiner pool work queue but the outbox never becomes available.

**Meanwhile, combiner workers are trying to scatter tasks:**

```
Combiner Worker (19151 - runnable)
  → Combine() called user combiner
  → TryScatter (cascading new tasks)
  → combineScatterWork.Execute
  → taskPostWork.Execute
  → TryPushBack on task queue ← Currently here in trace logging
```

### Root Cause Hypothesis

**Circular dependency deadlock:**

1. **Task workers** (19150, 19416, 19434) have completed combine tasks
2. They're trying to **post results** to combiner work queue
3. **Combiner workers** (19151, 19152) are running
4. They're trying to **scatter new tasks** back to task queue
5. But the task queue might be full/blocked
6. And the combiner queue can't accept results because workers are busy scattering
7. **Circular wait**: Task workers wait for combiner queue space, combiner workers wait for task queue space

**Why runnable goroutines don't help:**
- Goroutine 19151 is trying `TryPushBack` (non-blocking) but likely failing repeatedly
- Goroutines 19152, 19415 are cleaning up, but don't resolve the circular dependency
- No notification is being generated to break the cycle

**Notification Conservation Violation:**

The combiner work queue outbox should notify waiting task workers when space becomes available. But if:
1. All combiner workers are stuck trying to scatter
2. And scatter operations are using `TryPushBack` (non-blocking)
3. Then no combiner worker ever finishes and pulls from the queue
4. So no outbox space notification is ever sent
5. Task workers wait forever

### Why It's Intermittent

This requires a perfect storm:
1. Multiple task workers complete combines simultaneously
2. All try to integrate results at same time (filling combiner queue)
3. Meanwhile, active combiners all try to scatter tasks
4. Task queue is near capacity
5. TryScatter fails, but doesn't unblock the circular wait

After thousands of successful iterations, this specific queue state alignment occurs.

### Potential Fixes

1. **Break the scatter→post→scatter cycle**:
   - Don't allow scattering during integrate/post operations
   - Or use separate queues for different operation types

2. **Add timeout to PushBackFunc in combinePostWork**:
   - Use deadline parameter (currently unused)
   - Fail with error instead of hanging forever

3. **Make TryScatter more aggressive about freeing resources**:
   - Ensure failed TryScatter cleans up immediately
   - Don't hold any locks or queue positions across retry attempts

4. **Demand-based spawning for combiner workers**:
   - Similar to task worker spawning
   - Spawn new combiner worker when integrate operations block
