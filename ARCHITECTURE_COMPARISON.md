# Architecture Comparison: Steady-State Contention & Allocations

Source-based architectural analysis of how psg-go compares against the
closest competitors on two performance axes used in `README-proposed.md`:
**steady-state contention** and **per-task allocations**.

This is design-level analysis, not benchmark data. The ratings in the
README comparison table are grounded in the architectural choices each
library makes on its hot paths (sync primitives, queue structures, object
pooling). Before the README is published, these ratings should be backed
by an actual cross-library benchmark suite that measures both contention
behavior under load and `allocs/op` in steady state.

## Methodology

Performed 2026-05-23 by a research subagent. All claims are grounded in
direct source reading with `file:line` or GitHub URL citations. No
analysis is based on README marketing claims.

Sources examined:
- `golang.org/x/sync/errgroup` — Go source tree
- `sourcegraph/conc` (`pool/pool.go`)
- `panjf2000/ants` (`pool.go`, `ants.go`, `worker.go`)
- `alitto/pond` (`pool.go`, `internal/linkedbuffer/linkedbuffer.go`)
- `cmitsakis/workerpool-go` (`main.go`)
- psg-go internal packages on disk: `nbcq`, `rdvq`, `omnipool`, `workq`

---

## Per-library findings

### 1. `golang.org/x/sync/errgroup`

Source: https://cs.opensource.google/go/x/sync/+/master:errgroup/errgroup.go

**Submit path (`Go`)** — sync primitives & allocations:
- If `SetLimit` was called: a buffered semaphore channel send (`g.sem <- token{}`).
- Always: `g.wg.Add(1)` (atomic), then `go func() { ... f() ... }()`.
- The closure captures `g` and `f`, allocating a closure record on the
  heap. `go func()` itself heap-allocates a fresh goroutine stack per
  task. There is **no worker reuse** — every task = one new goroutine.

**Work pickup path** — none; the goroutine is the worker, dedicated to
one task.

**Result delivery** — error reporting only:

```go
if err := f(); err != nil {
    g.errOnce.Do(func() {
        g.err = err
        if g.cancel != nil { g.cancel(g.err) }
    })
}
```

`sync.Once` ensures only the first error is retained. No queue, no
per-task allocation on the success path beyond `g.wg.Done()`.

**Overall contention rating: LOW.** The only shared state in the
steady-state hot path is `sync.WaitGroup` and (optionally) a buffered
semaphore channel — both lightweight, lock-free under normal conditions.
Concurrent submitters contend only for `wg.Add` (atomic add) and the
semaphore channel send (which is a runtime mutex on the chan).

**Overall steady-state allocation rating: HIGH.** Goroutine creation per
task (one stack), one closure allocation per `Go` call (captures `g`,
`f`). No object pooling. This is the structural cost of "one goroutine
per task" — fine for coarse tasks, expensive for fine ones.

---

### 2. `sourcegraph/conc` (pool/pool.go)

Source: https://raw.githubusercontent.com/sourcegraph/conc/main/pool/pool.go

**Submit path (`Go`)** — pool.go:38–69:

```go
select {
case p.tasks <- f:
    // A goroutine was available to handle the task.
default:
    p.handle.Go(func() { p.worker(f) })
}
```

- Sync primitive: **unbuffered `chan func()`** (`p.tasks = make(chan func())`
  at line 102).
- If a worker is parked on `<-p.tasks`, the send succeeds (one channel
  handoff, runtime mutex on the chan).
- If not, a new goroutine is spawned. The pool grows lazily up to
  `limiter` cap; goroutines never exit until `Wait()` closes `p.tasks`.
- `sync.Once` (`p.initOnce`) guards channel creation.

**Work pickup path** — `worker`, line 154:

```go
for f := range p.tasks { f() }
```

Each worker receives directly from the same unbuffered `p.tasks` channel.
Under N concurrent submitters + M idle workers, the Go runtime's channel
implementation serializes through the channel's hchan mutex.

**Result delivery** — base `Pool` has no result channel;
`ResultPool`/`ErrorPool`/`ContextPool` collect into slices guarded by
their own mutexes (separate files). The base `Pool` collects nothing per
task.

**Overall contention rating: MEDIUM.** One unbuffered channel funnels
every submission. The hchan mutex is contended by N submitters + M
receivers in steady state; this is the well-known M:N channel contention
pattern. Lower than mutex-based dispatch only because the hchan mutex is
briefly held and supports direct-handoff fast paths.

**Overall steady-state allocation rating: LOW.** Tasks are `func()`
values — passed by value through the channel, no wrapping struct. The
submit-time closure `func() { p.worker(f) }` only allocates on *worker
spawn*, not steady-state submission. **However**, the user-supplied
`func()` itself typically captures variables, forcing a closure
allocation on every `Go(...)` callsite — but that's a property of the
caller's code, not the pool.

---

### 3. `panjf2000/ants`

Source: https://raw.githubusercontent.com/panjf2000/ants/master/pool.go,
ants.go, worker.go

**Submit path (`Pool.Submit`)** — pool.go:32–42 calls `retrieveWorker()`,
then `w.inputFunc(task)`. `retrieveWorker` in ants.go:~485 onward:

```go
func (p *poolCommon) retrieveWorker() (w worker, err error) {
    p.lock.Lock()
retry:
    if w = p.workers.detach(); w != nil { p.lock.Unlock(); return }
    if capacity := p.Cap(); capacity == -1 || capacity > p.Running() {
        w = p.workerCache.Get().(worker)
        w.run()
        p.lock.Unlock()
        return
    }
    // ...
    p.addWaiting(1)
    p.cond.Wait()
    p.addWaiting(-1)
    // ...
    goto retry
}
```

- Sync primitive: a **spin lock** (`syncx.NewSpinLock()` at ants.go:~219
  — `lock: syncx.NewSpinLock()`) protecting `p.workers` (a stack or loop
  queue) and a `*sync.Cond` (`p.cond = sync.NewCond(p.lock)`).
- Every `Submit` acquires the spin lock; pop a worker from the stack
  (LIFO via `workerStack.detach`, worker_stack.go:53), or spawn one from
  `workerCache` (a `sync.Pool`), or `cond.Wait` if at capacity.
- `inputFunc` then sends to the worker's per-worker buffered chan
  (capacity `workerChanCap` = 1 if GOMAXPROCS>1, ants.go:88–103).

**Work pickup path** — worker.go:55–66:

```go
for fn := range w.task {
    if fn == nil { return }
    fn()
    if ok := w.pool.revertWorker(w); !ok { return }
}
```

Worker blocks on its own dedicated `task chan func()` (buffered to 1). No
shared dispatch channel — every worker has its own. The pickup is
contention-free per worker.

**Revert path (`revertWorker`)** — ants.go:~570:

```go
p.lock.Lock()
// ...
p.workers.insert(worker)
p.cond.Signal()
p.lock.Unlock()
```

Returning a worker to the stack takes the same spin lock and signals one
cond waiter.

**Result delivery** — none. ants is fire-and-forget; the user must
arrange result delivery externally.

**Overall contention rating: MEDIUM-HIGH.** **Every** `Submit` and
**every** task completion takes the same spin lock (`p.lock`). Under N
concurrent submitters + M concurrent completions, all 2(N+M) operations
serialize on one spin lock. Spin locks are cheap when uncontended but
degrade under contention (and worker_stack push/pop is non-trivial
work). The `cond.Signal`/`cond.Broadcast` semantics also imply lock
holders wake other goroutines while holding the lock.

**Overall steady-state allocation rating: LOW (near-zero on the hot
path).** `sync.Pool` (`workerCache`) recycles `goWorker` structs. Tasks
pass as raw `func()` values through per-worker channels — no envelope
struct. The single allocation hazard is the user-supplied closure
itself.

---

### 4. `alitto/pond`

Source: https://raw.githubusercontent.com/alitto/pond/master/pool.go,
internal/linkedbuffer/linkedbuffer.go

**Submit path (`pool.Submit` → `wrapAndSubmit` → `submit` → `trySubmit`)**
— pool.go:316–451:

```go
func (p *pool) trySubmit(task any) error {
    p.mutex.Lock()
    // ...
    if int(p.workerCount.Load()) >= p.maxConcurrency {
        // ...
        p.tasks.Write(task)
        p.mutex.Unlock()
        return nil
    }
    p.workerCount.Add(1)
    p.workerWaitGroup.Add(1)
    if queueEnabled && tasksLen > 0 {
        p.tasks.Write(task)
        task, _ = p.tasks.Read()
    }
    p.mutex.Unlock()
    p.launchWorker(task)
    p.notifySubmitWaiter()
    return nil
}
```

- Sync primitive: **`sync.Mutex` (`p.mutex`)** protects `p.tasks` (a
  `LinkedBuffer[any]`), `p.workerCount` reads in critical sections, and
  the closed/stopping logic.
- `wrapTask` (pool.go:347) calls `future.NewFuture(ctx)` per submission —
  that creates a future struct + resolve closure. Then `wrapTask[struct{},
  func(error)](task, resolve, ctx, p.panicRecovery)` allocates another
  closure.
- LinkedBuffer's `Write`/`Read` themselves are not lock-free — `trySubmit`
  holds `p.mutex` across them; the buffer's internal atomic counters are
  bookkeeping, not synchronization for the slot data.

**Work pickup path** — `worker` (pool.go:259) calls `p.readTask` in a
loop:

```go
func (p *pool) readTask() (task any, err error) {
    p.mutex.Lock()
    if p.tasks.Len() == 0 { ... }
    // ...
    task, _ = p.tasks.Read()
    p.mutex.Unlock()
    p.notifySubmitWaiter()
    return
}
```

Every pickup takes the same `p.mutex`. So **submit and pickup contend on
a single mutex.**

**Result delivery** — through the per-task `Future`, which is allocated
per task. `future.NewFuture` (in `internal/future/`) creates a struct
holding a result channel/state. `resolve(err)` writes to it.

**Overall contention rating: HIGH.** One `sync.Mutex` serializes all
submits and all pickups across all workers. Under N submitters + M
workers, this is the most heavily contended hot path of the five
libraries. The blocking-submit path also waits on `p.submitWaiters` (a
capacity-1 chan) which serializes wake-ups.

**Overall steady-state allocation rating: MEDIUM-HIGH.** Each `Submit`
allocates: (a) a `future.Future`, (b) the `resolve` closure, (c) the
wrapped task closure (`wrapTask` at pool.go:355), and (d) a slot in
`LinkedBuffer` (amortized — buffer is reused, but each task occupies a
slot the buffer doesn't pool individually). The `Go(task func()) error`
form skips the future, dropping (a)–(b), but still has (c)+(d).

---

### 5. `cmitsakis/workerpool-go`

Source: https://raw.githubusercontent.com/cmitsakis/workerpool-go/master/main.go

**Submit path** — submission goes into `p.jobsNew chan I` (capacity 2,
main.go:175 — `p.jobsNew = make(chan I, 2)`). Confirmed:

```go
p.jobsNew = make(chan I, 2)
p.jobsQueue = make(chan Job[I], p.maxActiveWorkers)
p.jobsDone = make(chan Result[I, O], p.maxActiveWorkers)
```

A user calls `Submit(payload)` (defined later in the file), which
performs `p.jobsNew <- payload`. With capacity 2, after two pending
submissions the submitter blocks until the central `loop` goroutine
drains.

**Central `loop` goroutine** (main.go:185+) — a single goroutine
multiplexes `select { case <-p.jobsNew: ...; case <-p.jobsDone: ... }`,
forwards new payloads to `p.jobsQueue` (cap=maxActiveWorkers), and
forwards results to `p.Results`. It also runs the EMA-based load tracking
and emits enable/disable signals to workers via
`enableWorker`/`disableWorker`.

**Work pickup path** — workers receive from `p.jobsQueue` (the shared,
bounded chan). All workers contend on this channel's hchan mutex.

**Result delivery** — workers write `p.jobsDone <- Result{...}`; central
`loop` reads `p.jobsDone` and writes to `p.Results`. The result path
involves **two channel hops** (worker → jobsDone → loop → Results) and
the `Result[I,O]` struct embeds the original `Job[I]`.

`stoppedWorkersMu sync.Mutex // protects: stoppedWorkers and
sleepingWorkers` (main.go:73) covers the scaling bookkeeping, not the hot
path.

**Overall contention rating: MEDIUM (with a structural bottleneck).** The
hot path is all channels — no shared mutex. But the central `loop`
goroutine is a **single-goroutine funnel**: every submission, every
result, and every scaling decision flows through one select. With
`jobsNew` capacity 2, the loop is the throughput ceiling regardless of
how many workers exist. Channel-based contention is lower per-operation
than mutex contention, but the serialization through `loop` is
unavoidable.

**Overall steady-state allocation rating: LOW-MEDIUM.** `Job[I]` and
`Result[I,O]` are concrete structs passed by value through channels — no
per-task heap allocation from the framework if `I`/`O` are value types.
The `loop` goroutine allocates nothing per task. The result-channel hop
does cause `Result[I,O]` to be copied twice. No object pooling, but no
closures either — payloads are raw values, not `func()`.

---

### 6. psg-go (this library)

Source: `/home/peter/src/psg-go/` — `job.go`, `taskpool.go`,
`internal/nbcq/nbcq.go`, `internal/rdvq/queue.go`,
`internal/omnipool/struct.go`, `internal/workq/accepted.go`.

**Submit path (`Scatter` → `meta.ExecuteNowOrQueue` →
`taskPostWork.Execute` → `taskQueue.TryPushBack`/`PushBackFunc`)**:

The submit flow constructs work items entirely from pools:

```go
// gatherop.go:202
w := g.workPool.Get()
// taskpool.go:125
w := taskPoolScatterWorkPool.Get()
// job.go:68
w := taskWorkPool.Get()
// job.go:997
w := taskPostWorkPool.Get()
```

All these are `omnipool.Pool[T]` — wrappers over `sync.Pool`
(internal/omnipool/struct.go:64–130) with `Reset`-on-Put. Steady-state
submission reuses every wrapper struct.

Posting hits `rdvq.Queue.PushBackFunc` (internal/rdvq/queue.go:97):

```go
// First try to deliver to a waiting inbox
if q.inboxStackQueue.TryPushBack(value) {
    return
}
outbox := outboxFor(sender, q)
// ...
select {
case outbox.ch <- value:
    outbox.filled()
    // ...
    q.fullOutboxes.PushBack(outbox)
    q.outboxWaiters.Notify(nil)
    return
default:
}
```

- The `outbox` is **per-sender-goroutine** (`internal/rdvq/sender.go` —
  `Sender.outboxMap` is private to one goroutine), with a `chan T` of
  capacity 1.
- If no receiver is waiting, the value goes into that *private* outbox
  channel — zero cross-sender contention.
- The outbox is then enqueued onto `q.fullOutboxes`, which is
  `nbcq.Queue[*outbox[T]]` — a Michael-Scott lock-free queue using
  **128-bit atomic CAS** for the head/tail pointer-with-count pairs
  (nbcq.go:90–135):

  ```go
  if tail.ptr.next.CompareAndSwap(next, pointer[T]{ptr: node, count: next.count + 1}) {
      // ...
      q.tail.CompareAndSwap(tail, pointer[T]{ptr: node, count: tail.count + 1})
  ```

**Work pickup path (`runTasks` → `taskQueue.PopFrontFunc`)** — job.go:822
and rdvq/queue.go:300:

- Workers first try a non-blocking `TryPopFront` (which pops from
  `fullOutboxes` via NBCQ CAS).
- On miss, they register as an inbox waiter (LIFO stack —
  `inboxStackQueue`, which provides natural worker scaling: most-recently
  -active receivers get next item, others time out idle).
- Selects on inbox channel + outbox-wait channel + idle timer + ctx.Done
  — no shared mutex.

**Result delivery (`Gather`/`GatherAll` → `workQueue.ExecuteOne` →
`gatherQueue.PopFrontFunc`)**:

- Same rdvq pattern. `Job.gatherQueue workq.Pending` is
  `rdvq.Queue[workq.Work]` (workq/pending.go:11).
- The `workq.Accepted` queue for retries uses two `nbcq.Queue[Work]`
  (fresh and postponed, accepted.go:27–32) — both lock-free.
- `gatherWork[T]` is allocated from a typed `omnipool.Pool[gatherWork[T]]`
  (gatherop.go:202).

**Single mutex on the hot path**: `Job.taskWorkerMu` (job.go:52), but it
only guards `latestTaskWorkerIdleExit` — touched on worker idle-timeout
exit (`tryTaskWorkerIdleExit`, job.go:1064), not on submit or pickup.

**Overall contention rating: LOW.** The hot path uses (a) per-sender
private outbox channels (no cross-sender contention on the buffering
step), (b) a Michael-Scott lock-free queue with 128-bit CAS for the
cross-sender ordering, and (c) lock-free atomic counters for
inFlight/demand. The closest thing to a global lock on the
submit/pickup path is the runtime hchan mutex on the per-sender outbox
channel (capacity 1) — but that channel has only one sender (the owning
goroutine), so it is structurally uncontended in steady state.

**Overall steady-state allocation rating: NEAR-ZERO.** Every per-task
wrapper (`taskWork`, `taskPostWork`, `gatherWork[T]`, `taskPoolScatterWork`,
`gatherScatterWork`) is pooled via `omnipool` with `Reset`-on-Put. NBCQ
nodes and value-pointer cells are pooled too (nbcq.go:62 —
`nodePool *omnipool.Pool[node[T]]`, `valuePool *omnipool.Pool[T]`). The
remaining per-task heap allocation is whatever the user closure
captures, which is outside the library's control. **Caveat: this hot-path
zero-alloc story assumes `omnipool` pools are warm; the first few
submissions of a session pay normal allocation costs.**

---

## Comparative table summary

| Library                  | Steady-state contention | Per-task allocations |
|--------------------------|-------------------------|----------------------|
| errgroup                 | Low                     | High (new goroutine per task) |
| sourcegraph/conc pool    | Medium                  | Low |
| panjf2000/ants           | Medium-High             | Low |
| alitto/pond              | High                    | Medium-High (future + closures + buffer slot) |
| cmitsakis/workerpool-go  | Medium                  | Low-Medium |
| psg-go                   | Low                     | Near-zero |

---

## Footnote-ready single-sentence summaries

**errgroup**
- *Contention:* "Submit is essentially `wg.Add(1); go f()` plus an optional
  buffered-chan semaphore — no shared dispatch structure to contend on
  (errgroup.go ~70–100)."
- *Allocations:* "Allocates a fresh goroutine stack and a closure capturing
  `g` and `f` on every `Go` call; no worker reuse (errgroup.go:78–98)."

**sourcegraph/conc pool**
- *Contention:* "All submitters and all workers contend on a single
  unbuffered `chan func()` (`p.tasks = make(chan func())`, pool.go:103);
  contention is the Go runtime's hchan mutex."
- *Allocations:* "Tasks pass as raw `func()` values through the channel
  with no wrapping struct; workers are spawned lazily and reused via
  `chan func()` (pool.go:154)."

**panjf2000/ants**
- *Contention:* "Every Submit and every task completion takes a single
  spin lock `p.lock` (ants.go:172) plus a `*sync.Cond` to gate the worker
  stack/queue."
- *Allocations:* "Worker structs are pooled via `sync.Pool`
  (`workerCache`, pool.go:53–58); tasks pass as raw `func()` through
  per-worker `chan func()` buffered to 1 (worker.go:46)."

**alitto/pond**
- *Contention:* "Every Submit and every `readTask` takes one `sync.Mutex`
  (`p.mutex`, pool.go:142, 321, 469) that guards a `LinkedBuffer[any]`
  task queue."
- *Allocations:* "Each Submit allocates a `future.Future`, a `resolve`
  closure, and a `wrapTask` closure, plus a slot in the linked buffer
  (pool.go:347–357)."

**cmitsakis/workerpool-go**
- *Contention:* "Submit writes into a `chan I` of capacity 2
  (`p.jobsNew = make(chan I, 2)`, main.go:175); a single central `loop`
  goroutine multiplexes jobsNew/jobsDone and is the throughput
  bottleneck."
- *Allocations:* "`Job[I]` and `Result[I,O]` are concrete struct values
  copied through channels — no per-task heap envelope from the framework,
  no object pooling either."

**psg-go**
- *Contention:* "Hot path is a Michael-Scott lock-free queue using 128-bit
  atomic CAS (`nbcq.Queue`, nbcq.go:90–135) over per-sender private
  outbox channels — no shared mutex on Submit/pickup/result paths."
- *Allocations:* "Every per-task wrapper (`taskWork`, `taskPostWork`,
  `gatherWork[T]`, NBCQ nodes, value-pointers) is recycled through
  `omnipool` (`sync.Pool` + Reset) — `omnipool.For[T]()` shared per type
  (job.go:122, gatherop.go:39, nbcq.go:63)."

---

## Honest caveats

**Where the analysis is least confident:**

1. **ants's spin lock.** A spin lock is fast under low contention; rated
   medium-high based on the structural fact that every submit *and* every
   completion takes it. Whether that's actually contended in practice
   depends on workload (submission rate, task duration). A benchmark
   might reveal that the lock is held so briefly that contention is
   invisible up to high core counts. The rating reflects worst-case
   behavior, not measured behavior.

2. **conc pool's medium rating.** An unbuffered channel with the Go
   runtime's direct-handoff fast path can be very efficient — under
   perfectly balanced N submitters + N idle workers, it's nearly as cheap
   as the lock-free queue, because hchan can hand off without going
   through its mutex's slow path. Under unbalanced load (M ≠ N workers)
   it degrades. The "medium" rating bakes in this variability.

3. **pond's pool-mutex isn't always the bottleneck.** Under workloads
   where tasks are long and submission is bursty-then-idle, the mutex is
   rarely held. The "high" rating is a worst-case characterization for
   steady high-throughput submit; it's not an indictment of the library
   for its target workloads (which appear to be coarser-grained tasks
   where the future-allocation cost is amortized over real work).

4. **workerpool-go's central-loop bottleneck.** Rated contention "medium"
   because channels are cheaper than locks per-op, but the structural
   funnel through one loop goroutine + the `jobsNew` capacity-2 cap is a
   hard ceiling. For the use case the library targets (auto-scaling
   pipelines), this is by design — backpressure comes through that
   funnel — but it does mean throughput cannot exceed what one goroutine
   can multiplex.

5. **psg-go's "near-zero" allocation rating is hot-path only.** Cold
   start, pool churn from sync.Pool's GC-driven flushes, and the
   (acknowledged in WORKING_NOTES.md) `wrappedRenotify` self-freeing edge
   case can produce occasional allocations. Long-running benchmarks
   should see near-zero `allocs/op`; short or bursty benchmarks may not.

**Where benchmark data would meaningfully change the picture:**

- For ants vs. psg-go on the contention axis: spin-lock-of-tiny-critical-
  section can outperform lock-free CAS-loop under low contention. A real
  measurement might reverse the ordering at low core counts.
- For pond on tiny tasks: if `future.NewFuture` and the closures are
  stack-allocatable in Go's current escape analysis (they probably are
  not, but if so), the "medium-high" allocation rating would drop.
- For conc vs. psg-go: an unbuffered chan with direct handoff vs. NBCQ +
  outbox is a workload-dependent tradeoff — bursty submitters favor
  psg-go's outbox buffering; perfectly synchronized M:N handoff favors
  conc.
