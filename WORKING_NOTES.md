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

**Current Status:** Design documented, ready to commit current state and begin refactoring.
