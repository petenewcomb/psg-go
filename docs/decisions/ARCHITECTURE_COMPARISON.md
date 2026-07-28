# Architecture Comparison: Steady-State Contention & Allocations

> Decision/evidence doc. "psg-go" below is the current code; "streampool" is the
> repositioned target name (see `docs/decisions/API_DESIGN.md`) — the source-level
> analysis is of the current `psg-go` internals and holds under either name.
>
> The illustrative `streampool` code snippets in this doc predate the locked surface
> (they still show `NewWave`, `NewLauncher(wave)`, `WithLimit`, `CancelAndWait`).
> They are kept as contention/allocation illustrations only — for the current surface
> see `API_DESIGN.md` and `surface-lineage.md`; do not read them as current API.

Source-based architectural analysis of how the library compares against the
closest competitors on two performance axes used in the README comparison table:
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
- `maurice2k/ultrapool` (`ultrapool.go` — entire library; local clone at
  commit `1164638`, 2026-05-12) — **entry added 2026-06-12**, after the
  original pass
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
workers, this is the most heavily contended hot path of the six
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

### 6. `maurice2k/ultrapool` (added 2026-06-12)

Source: `ultrapool.go` — the entire library is one 498-line file. Line
numbers refer to a local clone at commit `1164638` (2026-05-12). See
POSITIONING_RESEARCH.md "Addendum: ultrapool" for adoption context and
its vendor benchmark suite.

**Submit path (`AddTask`)** — ultrapool.go:234–244:

```go
shard := wp.shards[randInt()%wp.numShards]
return shard.dispatch(task)
```

- Shard selection is **random per call**: `randInt()` does a `sync.Pool`
  Get/Put around a splitMix64 step (ultrapool.go:493–498). No producer
  affinity — consecutive submissions from one goroutine scatter across
  shards (default shard count GOMAXPROCS/2 clamped to [2, 48],
  ultrapool.go:64–78).
- `dispatch` (ultrapool.go:287–322) takes the shard's
  `tqLock sync.RWMutex` **read lock on every dispatch** — it fences the
  channel send against `Stop`'s `close(taskQueue)` (ultrapool.go:203–212).
  Then: an atomic `stopped` check, a non-blocking send to the shard's
  buffered `chan T` (default capacity 1024, ultrapool.go:58), and — when
  backlog is visible after the send (`len() > 0`, ultrapool.go:301) —
  `trySpawnWorker`.
- `trySpawnWorker` (ultrapool.go:328–361) reserves a per-shard worker
  slot via CAS loop, then a global slot via a second CAS loop (or an
  unconditional atomic add when uncapped), then `go shard.workerLoop()`.
  Once the shard is at its worker cap the call collapses to one atomic
  load.
- On a full buffer: spawn + one retry, else `ErrPoolOverload`
  (ultrapool.go:310–321). `AddTaskWithBlocking` (ultrapool.go:247–278)
  handles overload by retry-looping on a **single global capacity-1
  `notify` channel shared by all waiters across all shards**; a waiter
  that gets in re-arms the notification for the next waiter (chained
  baton, ultrapool.go:257–263), and workers re-arm it on going idle and
  on exit (`notifyWaiter`, ultrapool.go:396–397, 448, 454–462).

**Work pickup path (`workerLoop`)** — ultrapool.go:375–452:

- An inner loop drains the shard channel via non-blocking receive; on
  empty, floor workers (≤ `shardMinWorkers`, default 2) park in a plain
  blocking receive (ultrapool.go:401–408); above-floor workers select on
  the channel plus a reused idle timer and retire after 1s idle via a
  CAS-guarded decrement that never drops the shard below its floor
  (ultrapool.go:430–441).
- Per task, pickup is one buffered-channel receive. All workers and
  dispatchers of a shard share that channel's hchan mutex; the shard
  count divides this contention.

**Result delivery** — none. The handler is fixed at pool construction
(`NewWorkerPool(func(T))`, ultrapool.go:24, 81); tasks are data values,
strictly fire-and-forget. Notably this also means there is **no per-task
user closure** — the usual caller-side allocation hazard of
`func()`-based pools is structurally absent. (No recursive-submission
story either: with bounded queues and worker caps, handlers that block in
`AddTaskWithBlocking` can exhaust the pool like any bounded pool.)

**Overall contention rating: LOW-MEDIUM.** Sharding genuinely spreads
load: per dispatch, the shared touches are one RWMutex read-lock (an
atomic RMW on the shard's reader count), one buffered-channel send
(hchan mutex), and under visible backlog one or two more atomic ops in
`trySpawnWorker` — all on per-shard state randomly spread over up to 48
shards. Structurally better than ants/pond (global locks) and conc
(single channel); but unlike psg-go's per-sender private outboxes, each
shard's cache lines are shared by every dispatcher and worker that lands
there, and the random (rather than affine) shard pick guarantees
cross-core traffic on those lines. The blocking-submit path's global
one-at-a-time waiter baton is a serialization point, but only engages at
overload.

**Overall steady-state allocation rating: NEAR-ZERO (framework).** A
submitted task is a `T` copied into a channel buffer — no envelope
struct, no future, no closure; the shard-pick PRNG is `sync.Pool`-
recycled (ultrapool.go:485–491). Two edge caveats: workers are
goroutines, not pooled objects, so oscillating load churns goroutine
stacks through the spawn-at-backlog / retire-at-1s-idle cycle; and each
above-floor worker lazily allocates one reused `time.Timer`
(ultrapool.go:411–415).

**Code-quality observations** (relevant to weighing its benchmark
claims): `wp.started` is read unsynchronized in `AddTask`
(ultrapool.go:235) while written under `wp.mutex` in `Start`
(ultrapool.go:188) — a data race if submission overlaps startup (benign
in the typical start-then-submit usage, but a race-detector hit waiting
to happen); and `dispatch` opens with a dead `if len(...) > 0` block
whose body is commented out (ultrapool.go:288–290). One maintainer,
recently rewritten (v2), essentially unadopted — high analysis
confidence (whole file read), low field-testing confidence.

---

### 7. psg-go (this library)

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
| maurice2k/ultrapool      | Low-Medium              | Near-zero (framework) |
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

**maurice2k/ultrapool**
- *Contention:* "Dispatch takes a per-shard RWMutex read-lock plus one
  buffered-channel send, randomly spread over up to 48 shards
  (ultrapool.go:242, 292, 300); no global lock on the submit/pickup path,
  but shard structures are shared by all dispatchers/workers landing
  there."
- *Allocations:* "Tasks are `T` values copied into a per-shard buffered
  channel with a handler fixed at construction — no per-task closure,
  envelope, or future (ultrapool.go:24, 81, 300); workers are unpooled
  goroutines that retire after 1s idle."

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

5. **ultrapool's rating is for the steady state its design optimizes;
   its edges are weaker.** Under stable load it is genuinely lean (value
   copy + channel send). Under oscillating load, the spawn-at-backlog /
   retire-after-1s cycle churns goroutine stacks, and the random shard
   pick trades cache locality for load spreading — both invisible in
   sustained-throughput benchmarks (which its vendor suite emphasizes)
   and visible in bursty ones. Its "beats raw goroutines" claim is
   architecturally plausible for sub-microsecond tasks (warm workers
   receiving values vs. fresh goroutine stacks) but unverified here.

6. **psg-go's "near-zero" allocation rating is hot-path only.** Cold
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

---

## Comparison with concurrency programming models

*(Relocated from the programming-model guide; programming-model-level comparisons live here.)*

### Structured Concurrency in the Industry

streampool's approach aligns with the broader **structured concurrency** movement in programming languages and frameworks, while providing Go-specific innovations:

#### Formal Structured Concurrency

**Industry Definition**: Structured concurrency ensures that concurrent operations have clear, hierarchical structure where:
- All spawned tasks complete before their parent scope exits
- Cancellation propagates automatically through the hierarchy
- Resource cleanup happens deterministically
- Error handling follows structured patterns

**Examples in Other Languages**:
- **Nurseries** in Python's Trio library
- **Structured Concurrency** in Java's Project Loom  
- **Async/await** with structured scoping in Swift and Kotlin
- **Green threads** with supervision trees in Erlang/OTP
- **Structured concurrency** in modern C++ with co-routines

**streampool's Implementation**:
- **Waves** provide the structured scope (nursery equivalent)
- **Work reference counting** ensures all tasks complete before wave completion
- **Context propagation** handles cancellation and cleanup automatically
- **Sequential skim processing** provides deterministic coordination points

#### Key Differentiators from Industry Approaches

**Dynamic Task Spawning**: Unlike traditional structured concurrency which prohibits spawning from within tasks, streampool lets any body submit and drive sub-waves it owns while maintaining structured guarantees — the only restriction is that a body cannot skim a wave it is part of.

**Multi-Level Resource Management**: streampool extends structured concurrency with Launchers and Funnels that provide independent resource boundaries within the overall structured scope.

**Incremental Processing**: Traditional structured concurrency waits for all tasks to complete before proceeding. streampool processes results incrementally while maintaining structured guarantees.

### Comparison with Go Ecosystem

#### creachadair/taskgroup Package

**Project**: github.com/creachadair/taskgroup

This is the closest existing library to streampool in the Go ecosystem, providing structured concurrency with advanced features.

**Similarities**:
- Structured task group management with automatic synchronization
- Error collection and propagation from multiple concurrent tasks
- Concurrency limiting capabilities
- Support for result gathering from background tasks
- Context-aware design for cancellation

**Key Differences from streampool**:
- **Result Processing**: taskgroup collects all results before processing; streampool processes incrementally
- **Task Spawning**: taskgroup doesn't support dynamic task spawning from within tasks
- **Resource Pools**: streampool provides multiple independent Launchers with different limits
- **Funnel Pattern**: streampool supports stateful aggregation with automatic flushing
- **Work Queueing**: streampool enables controlled reentrancy through work queueing

**Example Comparison**:
```go
// taskgroup approach
g := taskgroup.New(nil).Limit(10) // Single global limit
tasks := []taskgroup.Task{
    func() error { return doWork(1) },
    func() error { return doWork(2) },
    func() error { return doWork(3) },
}
err := g.Go(tasks...).Wait() // Batch execution, wait for all

// streampool approach  
wave := streampool.NewWave(ctx)
launcher := streampool.NewLauncher(wave).WithLimit(10) // Launcher-specific limit
skimmer := streampool.NewSkimmer(func(ctx context.Context, result int, err error) error {
    if err == nil {
        processResult(result) // Incremental processing
        // Can spawn new tasks based on result
        if needsFollowUp(result) {
            skimmer.Submit(ctx, launcher, followUpTask(result))
        }
    }
    return err
})

for i := 1; i <= 3; i++ {
    skimmer.Submit(ctx, launcher, func(ctx context.Context) (int, error) {
        return doWork(i)
    })
}
```

**taskgroup's Strengths**:
- Simpler API for basic concurrent task execution
- Excellent error handling and filtering capabilities
- Mature, well-tested library
- Minimal overhead for straightforward use cases

**streampool's Advantages Over taskgroup**:
- Incremental result processing (streaming vs. batch)
- Dynamic workflow generation through reentrancy
- Multiple resource pools with independent limits
- Stateful aggregation through funnels
- Type-safe generic interfaces

#### Go's errgroup Package

**errgroup Approach**:
```go
g, ctx := errgroup.WithContext(ctx)
results := make([]string, 3) // Pre-allocated to avoid races
for i := range 3 {
    g.Go(func() error {
        res, err := doWork(ctx, i)
        if err != nil {
            return err
        }
        results[i] = res // Requires careful indexing
        return nil
    })
}
err := g.Wait()
```

**streampool Approach**:
```go
wave := streampool.NewWave(ctx)
defer wave.CancelAndWait()

var results []string // Safe dynamic slice
skimmer := streampool.NewSkimmer(func(ctx context.Context, result string, err error) error {
    if err == nil {
        results = append(results, result) // Sequential processing
    }
    return err
})

for i := range 3 {
    skimmer.Submit(ctx, wave, func(ctx context.Context) (string, error) {
        return doWork(ctx, i)
    })
}

err := wave.CloseAndSkimAll(ctx)
```

**Key Differences**:
- **Result Handling**: streampool eliminates data races through sequential skim processing
- **Dynamic Operations**: streampool supports task spawning from skim functions
- **Type Safety**: streampool uses generics for compile-time type safety
- **Resource Management**: streampool provides multiple concurrency pools

#### Native Go Concurrency Patterns

**Traditional Go Pattern**:
```go
// Manual worker pool implementation
jobs := make(chan Work, 100)
results := make(chan Result, 100)

// Start workers
for i := 0; i < numWorkers; i++ {
    go worker(jobs, results)
}

// Send work
go func() {
    for _, work := range workItems {
        jobs <- work
    }
    close(jobs)
}()

// Collect results
var collected []Result
for i := 0; i < len(workItems); i++ {
    collected = append(collected, <-results)
}
```

**streampool's Advantages**:
- **Simplified API**: No manual channel management
- **Automatic Resource Management**: Launchers handle worker lifecycle
- **Error Handling**: Built-in error propagation and aggregation
- **Cancellation**: Automatic context-based cancellation
- **Type Safety**: Generic interfaces eliminate type assertions

#### Reactive Extensions Family

**Examples**: RxJS (JavaScript), RxJava (Java), RxSwift (Swift), RxGo (Go), ReactiveX ecosystem

The Reactive Extensions family provides powerful abstractions for handling asynchronous data streams with operators for transformation, composition, and error handling.

**Similarities to streampool**:
- Support for scatter-gather patterns through operators like `forkJoin` and `combineLatest`
- Handle asynchronous data streams and error propagation
- Pipeline composition capabilities and incremental processing
- Built-in backpressure management
- Functional composition of complex workflows

**Example Patterns**:
```javascript
// RxJS scatter-gather
forkJoin({
  task1: service1$,
  task2: service2$,
  task3: service3$
}).subscribe({
  next: (results) => console.log(results),
  error: (err) => console.error(err)
});

// RxJS streaming aggregation
source$.pipe(
  mergeMap(item => processItem(item)),
  scan((acc, result) => combineResults(acc, result)),
  debounceTime(100)
).subscribe(aggregatedResult => emit(aggregatedResult));
```

**Key Differences from streampool**:
- **Programming Paradigm**: Stream-based reactive paradigm vs. imperative task-based execution
- **Operator Semantics**: Fixed operator library vs. flexible user-defined skim/funnel functions
- **Learning Curve**: Requires understanding reactive concepts vs. familiar imperative patterns
- **Concurrency Control**: Less direct control over resource limits and goroutine management
- **Type System**: Varies by language implementation; streampool leverages Go's type system specifically
- **Error Handling**: Stream-based error propagation vs. structured error collection

**streampool's Advantages Over Reactive Approaches**:
- **Familiar Patterns**: Uses imperative programming that's more familiar to most developers
- **Direct Resource Control**: Explicit concurrency limits and resource pool management
- **Simpler Mental Model**: Clear separation between parallel and sequential phases
- **Language Integration**: Designed specifically for Go's concurrency model and idioms

**Reactive Extensions' Advantages**:
- **Mature Ecosystem**: Well-established with extensive operator libraries
- **Cross-Language**: Consistent patterns across multiple programming languages
- **Sophisticated Operators**: Rich set of pre-built operators for complex stream processing
- **Time-Based Operations**: Excellent support for time-windowing and temporal operations

#### Pipeline and Dataflow Libraries

**Examples**: TPL Dataflow (.NET), Java's CompletableFuture, Python's asyncio, Akka Streams (Scala), Node.js Streams

These libraries focus on data pipeline construction with stage-based processing and built-in backpressure management.

**Similarities to streampool**:
- **Data Pipeline Construction**: Stage-based processing with data flow between components
- **Concurrent Execution**: Parallel processing across pipeline stages
- **Backpressure Management**: Built-in flow control to prevent overwhelming downstream stages
- **Composition**: Ability to compose complex pipelines from simpler components

**Example (TPL Dataflow)**:
```csharp
var processBlock = new TransformBlock<Input, Output>(
    input => ProcessData(input),
    new ExecutionDataflowBlockOptions { MaxDegreeOfParallelism = 4 });

var batchBlock = new BatchBlock<Output>(10);
processBlock.LinkTo(batchBlock);

var aggregateBlock = new ActionBlock<Output[]>(
    batch => AggregateBatch(batch));
batchBlock.LinkTo(aggregateBlock);
```

**Key Differences from streampool**:
- **Pipeline Structure**: Fixed pipeline topology vs. dynamic task spawning
- **Flexibility**: streampool allows recursive task creation and heterogeneous task management
- **Programming Model**: Block-based architecture vs. function-based scatter-gather
- **Error Handling**: streampool provides more flexible error handling and result aggregation
- **Resource Management**: streampool supports multiple independent resource pools

#### Async/Await and Future-Based Models

**Examples**: JavaScript Promises, Python asyncio, C# async/await, Rust's async/await, Scala Futures

These models provide asynchronous programming abstractions with composition capabilities.

**Similarities to streampool**:
- **Asynchronous Execution**: Non-blocking task execution with result handling
- **Composition**: Ability to compose complex asynchronous workflows
- **Error Propagation**: Structured error handling through the async chain
- **Cancellation**: Support for operation cancellation

**Key Differences from streampool**:
- **Programming Model**: Promise/Future chaining vs. scatter-gather coordination
- **Concurrency Control**: Limited direct control over resource allocation
- **Result Processing**: Typically batch-oriented vs. incremental processing
- **Reentrancy**: Less structured support for dynamic workflow generation

### Unique Features of streampool

#### Pipelined Processing
Unlike traditional scatter-gather that waits for all tasks to complete, streampool processes results incrementally as they arrive, enabling streaming aggregation patterns.

#### Controlled Reentrancy
Any body may submit more work and drive sub-waves it owns, enabling recursive
processing while preserving structured-concurrency guarantees. The one structural
rule is that a body may not skim a wave it is part of (its own or an ancestor).

#### Multi-Resource Management
Per-op concurrency is expressed with composable Limiters (a shared limiter for a
collective cap, several on one op with AND semantics), enabling fine-grained
resource control within a structured scope without separate pool types.

#### Funnel Pattern
Stateful aggregation with automatic flushing enables efficient batch processing and windowing operations that traditional structured concurrency doesn't directly support.

#### Zero Dependencies
Pure Go implementation with no external dependencies, making it lightweight and easy to adopt in any Go project.

### Use Case Differentiation

**streampool is Ideal For**:
- In-process concurrent task management with complex dependencies
- Applications requiring incremental result processing
- Scenarios with mixed resource constraints (I/O vs. compute)
- Recursive operations like web crawling or tree traversal
- Type-safe concurrent programming in Go
- Streaming aggregation and data processing pipelines

**streampool is NOT For**:
- Distributed task execution across machines
- Persistent workflow management with retry capabilities
- Visual workflow construction and monitoring
- Cross-system message-based integration

### Comparison with Distributed Systems

While these systems operate at a different architectural level than streampool, they share conceptual similarities and provided inspiration for streampool's design. streampool focuses solely on in-process concurrency, but its patterns support the requirements of distributed systems: workflow isolation, backpressure propagation, and careful stewardship of highly concurrent, failure-prone, and latency-sensitive operations.

#### DAG Workflow Engines

**Examples**: Apache Airflow, Argo Workflows, Dagster, Prefect, Temporal

**Key Similarities**:
- Task dependency management and parallel execution
- Error handling and retry logic
- Structured workflow composition

**Key Differences**:
- **Scope**: Cross-process/cross-machine orchestration vs. in-process concurrency
- **Persistence**: Persistent state and workflow visualization vs. ephemeral execution
- **Infrastructure**: Heavy platform requirements vs. lightweight library
- **Focus**: Long-running, scheduled workflows vs. real-time processing
- **Deployment**: Complex deployment requirements vs. simple library integration

#### Actor Frameworks

**Examples**: Akka (Scala), Erlang/OTP, Orleans

**Key Similarities**:
- Message-based task distribution
- Error supervision and recovery patterns
- Concurrent execution model with isolation

**Key Differences**:
- **Programming Model**: Actor model with message passing vs. direct function execution
- **Distribution**: Distributed by design vs. local concurrency focus
- **Complexity**: Complex deployment and operational requirements vs. simple library
- **Learning Curve**: Requires understanding actor model vs. familiar imperative patterns

#### Message Queue Systems

**Examples**: RabbitMQ, Apache Kafka, AWS SQS

**Key Similarities**:
- Scatter-gather messaging patterns
- Work distribution across consumers
- Backpressure and flow control mechanisms

**Key Differences**:
- **Communication**: Network-based vs. in-memory communication
- **Persistence**: Durability and persistence concerns vs. ephemeral processing
- **Overhead**: Network serialization overhead vs. direct function calls
- **Operational Complexity**: Separate infrastructure vs. embedded library

#### Enterprise Integration Patterns

**Examples**: Apache Camel, Spring Integration, MuleSoft

**Key Similarities**:
- Scatter-gather pattern implementation
- Message routing and aggregation
- Pipeline composition capabilities

**Key Differences**:
- **Architecture**: Enterprise service bus vs. library-based approach
- **Integration**: Cross-system integration focus vs. in-process workflows
- **Configuration**: Configuration-heavy XML/YAML vs. code-first API
- **Type Safety**: Runtime configuration vs. compile-time type safety

### Additional Go Libraries

#### conc Package

**Project**: github.com/sourcegraph/conc

**Similarities**:
- Safer concurrency abstractions for Go
- Error handling improvements over raw goroutines
- Context propagation and cancellation support

**Differences from streampool**:
- **Focus**: General safety improvements vs. specific scatter-gather patterns
- **Features**: No built-in result gathering or aggregation capabilities
- **Scope**: Limited support for dynamic task spawning and complex workflows
- **Patterns**: Focuses on making existing patterns safer vs. introducing new patterns

### Summary

The streampool programming model provides a structured approach to concurrent workflows that combines the performance benefits of parallelism with the safety and predictability needed for production systems. By embracing structured concurrency principles and providing clear abstractions for fan-out, streaming aggregation, and result handling, streampool enables developers to build complex concurrent applications without the typical hazards of concurrent programming.

streampool's unique position in the Go ecosystem comes from its combination of structured concurrency guarantees, incremental processing capabilities, and Go-native design. It fills the gap between simple concurrency utilities like errgroup and complex reactive frameworks, providing a practical solution for sophisticated in-process concurrent workflows.

The model's strength lies in its ability to hide the complexity of coordination, backpressure, and resource management while exposing simple, composable primitives that naturally express parallel computation patterns.
