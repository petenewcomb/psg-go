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

**Upcoming Major Work - Reducer Concept:**
Planning to introduce reducer functionality to support fan-in patterns needed by the benchmarking application. This was originally planned for post-merge but is now needed for the benchapp development. The reducer will complement the existing combiner architecture by providing keyed aggregation capabilities.

**Branch Status:**
Not yet ready for merge - significant reducer architecture work pending. After reducer implementation, will need to update documentation and finalize API before merging to main.