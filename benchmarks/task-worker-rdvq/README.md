# Task Worker RDVQ Implementation Benchmarks

This directory contains benchmark results comparing the original `nbcq.Queue[chan func(context.Context)]` task worker implementation with the new `rdvq.Queue[func(context.Context)]` RDVQ-based implementation.

## Implementation Change

**Before**: `Job.idleWorkers` used `nbcq.Queue[chan func(context.Context)]` - a queue of idle worker channels
**After**: `Job.taskQueue` uses `rdvq.Queue[func(context.Context)]` - direct task function handoff via RDVQ

### Key Changes

1. **Task Handoff**: Instead of queuing worker channels and sending tasks through them, we now queue task functions directly and use RDVQ's optimized handoff mechanism.

2. **Worker Implementation**: Workers now use `rdvq.PopFrontFunc()` with custom blocking logic that handles timeouts and cancellation in a single select statement.

3. **API Enhancement**: Added `TryPushBack()` method to RDVQ for non-blocking task handoff attempts.

## Benchmark Results

### Performance Improvements (Per-Task Normalized)

| Metric | Before | After | Change |
|--------|--------|-------|--------|
| **Overall Performance** | 538.2µs/task | 512.4µs/task | **-4.79%** ✅ |
| **Throughput** | 10.57k tasks/sec | 11.23k tasks/sec | **+6.22%** ✅ |
| **Gather Duration** | 36.32µs | 33.93µs | **-6.58%** ✅ |
| **Gather Latency** | 1006.7µs | 955.6µs | **-5.07%** ✅ |
| **Workflow Duration** | 57.73µs | 55.85µs | **-3.25%** ✅ |
| **Workflow Latency** | 1016.0µs | 950.1µs | **-6.48%** ✅ |
| **Memory Usage** | 1010.0 B/task | 1005.4 B/task | **-0.46%** ✅ |
| **Allocations** | 28.96/task | 28.78/task | **-0.61%** ✅ |

### Trade-offs

| Metric | Before | After | Change |
|--------|--------|-------|--------|
| Combine Latency (p50) | 6.782µs | 7.590µs | +11.91% ⚠️ |
| Task Latency (p50) | 79.45µs | 82.63µs | +4.00% ⚠️ |

The latency increases are minimal in absolute terms (0.8µs and 3.2µs respectively) and are significantly outweighed by the overall performance gains.

## Files

- `bench_task_worker_rdvq_before_20250604T150000Z.txt` - Raw baseline results (original nbcq implementation)
- `bench_task_worker_rdvq_before_20250604T150000Z_normalized.txt` - Normalized baseline results
- `bench_task_worker_rdvq_after_20250604T150933Z.txt` - Raw RDVQ implementation results
- `bench_task_worker_rdvq_after_20250604T150933Z_normalized.txt` - Normalized RDVQ results
- `bench.sh` - Automated benchmark script with normalization

## Benchmark Command

```bash
./bench.sh before  # Run baseline benchmark
./bench.sh after   # Run RDVQ benchmark
```

## Statistical Significance

All major performance improvements show p=0.000 (highly significant with n=20 samples), confirming the reliability of these results.

## Conclusion

The RDVQ task worker implementation delivers substantial performance improvements across all key metrics:
- **4.79% faster execution per task**
- **6.22% higher throughput** 
- **5-6% improvements in gather operations**
- **Minor memory efficiency gains**

This validates the pattern of converting idle receiver queues to RDVQ, building on previous successes with combiner RDVQ implementation.