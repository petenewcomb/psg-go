# RDVQ Investigation Benchmarks

These benchmarks were collected during the investigation of performance differences between the channel+idle-queue pattern and the rdvq (rendezvous queue) implementation.

## Code Versions

### "Before" - Channel + Idle Queue Pattern
- Base commit: `1f5de89` "Fix Job.gatherChan optimization with proper channel pooling"
- Modified to remove extra select clauses in `tryGatherOne` (see below)
- Uses `gatherChan` channel + `idleGatherers` queue pattern

### "After" - RDVQ Implementation  
- Current working directory state (to be committed)
- Replaces channel + idle queue with `rdvq.Queue`
- Functionally equivalent but reorganized code

## The "Extra Clauses" Modification

The original code at commit `1f5de89` had unnecessary select clauses in `tryGatherOne`:

```go
// Original tryGatherOne select statement:
select {
case gather := <-j.gatherChan:
    // process gather...
case <-j.state.Done():
    return false, nil
case <-ctx.Done():
    return false, ctx.Err()
default:
    return false, nil
}
```

For fair comparison, we removed the `j.state.Done()` and `ctx.Done()` cases since they're redundant in a non-blocking operation:

```go
// Modified tryGatherOne select statement (what we benchmarked):
select {
case gather := <-j.gatherChan:
    // process gather...
default:
    return false, nil
}
```

This modification accounted for ~3.6% of the initially observed performance difference.

## Files

### Before (channel + idle queue pattern, without extra clauses)
- `bench_with_rdvq_before_without_extra_clauses_fixedcpufreq_noprof.txt` - First run
- `bench_with_rdvq_before_without_extra_clauses_fixedcpufreq_noprof2.txt` - Second run
- `bench_with_rdvq_before_without_extra_clauses_fixedcpufreq_noprof3.txt` - Third run

### After (rdvq implementation)
- `bench_with_rdvq_after_fixedcpufreq_noprof1.txt` - First run
- `bench_with_rdvq_after_fixedcpufreq_noprof2.txt` - Second run
- `bench_with_rdvq_after_fixedcpufreq_noprof3.txt` - Third run

## Benchmark Configuration
- CPU frequency: Fixed (performance governor)
- Profiling: Disabled
- Benchmark: `BenchmarkCombinerThroughput`
- Workload: waiting/duration=10µs/flushPeriod=10µs/method=gatherOnly/combinerLimit=0

## Key Results
- Before: ~578-584µs/op
- After: ~563-566µs/op
- Improvement: 2.5-3.5%

See `docs/rdvq_performance_investigation.md` for full analysis.