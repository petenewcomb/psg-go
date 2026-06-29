# streampool/bench — head-to-head framework comparisons

An **isolated module** for comparing streampool against other Go concurrency /
worker-pool frameworks and stdlib baselines. It is separate from the main
`streampool` module on purpose: the third-party frameworks it pulls in (ants,
pond, conc, …) — and their licenses — stay out of streampool's own dependency
graph. Add whatever deps a comparison needs here without touching the root module.

## What it measures, and why

Go pool libraries (ants, pond, tunny) compete almost entirely on **throughput**
(tasks/sec) and **memory** (B/op, allocs/op, peak goroutines). None benchmark
**tail latency under heavy-tailed blocking work** — which is precisely the
operational pain ("a slow body backs up the dispatcher and amplifies the tail")
that streampool's dispatch/execution split is built to avoid. So this suite
reports both turfs through one apples-to-apples harness:

- **Competitors' turf (legibility):** tasks/sec, allocs/task, B/task, peak goroutines.
- **Our turf (the moat):** p50/p99/p99.9 of **dispatch latency** (enqueue → body
  start) and **end-to-end latency** (enqueue → body done), under a heavy-tailed
  lognormal blocking workload, swept across **P:D** regimes (P producer goroutines
  offering load vs body-concurrency cap D), from underload to heavy overload.

### The lineup (`dispatcher` interface)

| System | Role |
|---|---|
| `unbounded` | goroutine per task, no cap — the memory/goroutine-explosion control |
| `chan-semaphore` | buffered-channel semaphore + goroutine per task |
| `naive-pool` | fixed worker pool draining a channel — the **"dispatcher-pinned / no split"** control |
| `streampool` | a Wave + limiter-capped Launcher; bodies run on the executor (the split) |

External frameworks register in the same `dispatchers` slice via the `dispatcher`
interface (`start` / `submit` / `drain` / `stop`).

## Running

```sh
cd bench
# Full matrix (workload × P:D regime × system):
go test -run '^$' -bench BenchmarkDispatch -benchtime=1x -timeout=20m .
# One slice:
go test -run '^$' -bench 'BenchmarkDispatch/workload=heavytail/regime=overload' -benchtime=1x .
```

`-benchtime=1x` is intentional: each case runs a fixed warmup + measurement window
internally (see `harness_test.go`) and reports everything via `ReportMetric`, so Go's
own iteration count stays at 1.

## Status & findings

**First cut (2026-06-29).** Harness + stdlib baselines + streampool, validated. A
representative point (heavytail, balanced, P=D=8 on 8 cores):

| System | tasks/sec | allocs/task | p99-e2e | peak-goroutines |
|---|---|---|---|---|
| unbounded | 1.67M* | 3.1 | 79ms | **133,286** |
| chan-semaphore | 6.6k | 3.0 | 13.0ms | 20 |
| naive-pool | 6.7k | 1.0 | 14.3ms | 20 |
| streampool | 6.6k | **36.7** | **12.2ms** | 22 |

\* unbounded's throughput is spawn-rate, not useful work — it accumulates 133k
sleeping goroutines, the explosion bounded pools prevent.

Read: streampool matches/beats the bounded baselines on the e2e tail while bounding
goroutines, at a real **~37 allocs/task** cost vs naive-pool's 1.

### Known limitation / next steps

This flat "submit N independent blocking tasks" workload is throughput-bound by D,
so all bounded systems converge — it does **not** yet isolate the split's unique
edge. The split wins specifically where the dispatcher must stay responsive while
bodies block:

1. **Nested dispatch** — bodies that submit sub-work. A naive fixed pool
   *deadlocks* here (all workers blocked submitting to a full pool); streampool
   does not. (A correctness + latency demo.)
2. **Funnel + flush** — exercises CP-B1b (flush bodies on the executor); measures
   accumulate/flush/skim latency decomposition like the legacy combiner bench.
3. **Mixed load** — a latency-sensitive probe stream sharing the wave with
   heavy-tailed bodies, measuring the probe's dispatch tail.
4. **External frameworks** — ants / pond / conc in the lineup (task-only; they have
   no scatter-gather/funnel equivalent).
