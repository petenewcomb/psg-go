# Reservoir Sampling Benchmarking Refactor

## Overview

This directory contains benchmark data and analysis from an experiment to replace tdigest-based latency measurement with reservoir sampling in `combiner_test.go`. The experiment was motivated by concerns about tdigest pooling complexity and the discovery of NaN values in combine workflow latency measurements.

## What We Did

### 1. Replaced tdigest with Reservoir Sampling
- **Before**: Used pooled tdigest objects for latency quantile calculations, with measurement work concentrated in the gather phase
- **After**: Used fixed-size reservoir sampling arrays with atomic operations distributed across combine/gather phases

### 2. Fixed Workflow Latency Measurement
- **Problem**: Combine workflow latency showed NaN values (introduced during refactoring, not an inherent tdigest issue)
- **Solution**: Implemented proper min/median/max workflow latency tracking using small embedded reservoir for median calculation

### 3. Measurement Overhead Compensation
- **Problem**: Atomic reservoir operations added significant per-operation overhead
- **Solution**: Front-loaded measurement work and used `simulateWorkFrom()` to adjust simulated work duration accordingly

### 4. Semantic Change: Workflow Latency Definition
- **Before**: End-to-end time from scatter to final result completion
- **After**: Queueing time from scatter to processing start (combine/gather start)
- **Rationale**: Cleaner separation between queueing delays vs processing time

## Results

### Performance Impact
| Metric | Tdigest → Reservoir | Improvement |
|--------|-------------------|-------------|
| **Throughput** | 7,598 → 9,018 tasks/sec | **+18.7%** |
| **Latency per op** | 569.4µs → 577.8µs | **-1.5%** |
| **Memory (B/op)** | 1,034 → 1,112 bytes | **-7.5%** |
| **Allocations** | 33.32 → 33.20 allocs/op | **+0.4%** |

### Latency Characteristics
| Metric | Before | After | Change |
|--------|--------|-------|--------|
| **p50 Combine Latency** | 5.624µs | 6.305µs | **-12.1%** |
| **p99 Combine Latency** | 237.1µs | 297.3µs | **-25.4%** |
| **p50 Task Latency** | 79.10µs | 81.39µs | **-2.9%** |
| **Combine Workflow Latency** | NaN | 99.63µs p50 | **Fixed!** |

### New Metrics Available
With reservoir sampling, we gained granular workflow latency tracking:
- **Min workflow latency**: 1.400ms p50 (newest task in batch)
- **Median workflow latency**: 2.192ms p50 (representative task)  
- **Max workflow latency**: 2.503ms p50 (oldest task in batch)

## Trade-offs Analysis

### ✅ Benefits
1. **Major throughput improvement** (+18.7%)
2. **Fixed memory usage** (no pool management complexity)
3. **Fixed combine workflow latency** (was NaN)
4. **Granular workflow latency ranges** (min/median/max per batch)
5. **Cleaner conceptual separation** between queueing and processing time

### ❌ Costs  
1. **Individual operation latencies worse** due to atomic overhead
2. **Workflow latency semantics changed** from end-to-end to queueing-only
3. **Statistical approximation** vs exact quantiles
4. **Workload type pollution**: Measurement compensation trades wait time for CPU time in "waiting" workloads
5. **Memory improvements marginal** (only ~7% despite fixed allocation)

## Benchmark Data Files

- `bench_reservoir_benchmarks_before_*`: Tdigest baseline results
- `bench_reservoir_benchmarks_after_*`: Final reservoir sampling results
- `bench.sh`: Benchmark execution script with CPU frequency validation

## Key Insights

1. **Throughput improvement likely due to measurement distribution**, not fundamental algorithmic advantage
2. **NaN issue was introduced during refactoring**, not an inherent tdigest problem  
3. **Measurement compensation problematic for "waiting" workloads** - converts I/O-bound to CPU-bound characteristics
4. **End-to-end workflow latency more representative** of user experience than queueing-only metrics

## Conclusion

While reservoir sampling achieved significant throughput improvements and fixed the combine workflow latency measurement, the trade-offs are substantial:

- **Performance**: Individual operations slower, but system throughput higher
- **Accuracy**: Statistical approximation vs exact quantiles  
- **Semantics**: Less representative workflow latency metrics
- **Workload fidelity**: Measurement compensation alters workload characteristics

The experiment was valuable for understanding measurement overhead impacts and exploring alternative approaches. However, the original tdigest approach was working correctly and may be preferable for benchmark fidelity.

## Next Steps

1. **Commit current implementation** to preserve the exploration
2. **Revert to tdigest version** for production benchmarks
3. **Consider selective improvements** like measurement overhead insights
4. **Keep reservoir package** for potential future use cases

---

*This analysis demonstrates the importance of careful measurement methodology in performance benchmarking and the subtle ways that measurement techniques can influence both results and system behavior.*