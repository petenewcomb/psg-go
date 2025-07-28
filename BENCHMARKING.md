# PSG-Go Benchmarking Guide

This guide documents how to properly analyze PSG-Go benchmark results, particularly when comparing performance changes across commits.

## Running Benchmarks

### Basic Benchmark Execution

Use the provided `bench.sh` script to run the full benchmark suite:

```bash
# Run with default settings (6 iterations, 10h timeout)
./bench.sh

# Run with a descriptive tag
./bench.sh mytag

# Run with custom go test flags
./bench.sh mytag -bench=Combiner -count=10
```

The script will:
1. Verify CPU frequency scaling is set to 'performance' mode (required for consistent results)
2. Create a timestamped output file (e.g., `bench_20250722T010050Z.txt`)
3. Run the benchmarks and show progress in real-time
4. Automatically normalize the results using `internal/cmd/benchnorm`
5. Save normalized results with `_norm.txt` suffix
6. Generate a comparison report using `benchcmp` if a baseline exists (`bench_norm.txt`)
7. Display a quick summary of performance changes

### Normalizing Results Manually

If you need to normalize existing benchmark results:

```bash
# Check bench.sh to see the normalization command:
go run -C internal/cmd/benchnorm ./... < bench_raw.txt > bench_norm.txt
```

Always use the normalized (`*_norm.txt`) files for analysis, not the raw benchmark output.

### Using benchcmp for Automated Analysis

The `benchcmp` tool provides automated benchmark comparison following BENCHMARKING.md guidelines:

```bash
# Compare current results against baseline
internal/bin/benchcmp -baseline bench_norm.txt -current bench_feature_norm.txt

# Or use the auto-generated report from bench.sh
cat bench_feature_20250724T123456Z_report.txt
```

#### Establishing a Baseline

To enable automatic comparison reports:

```bash
# Run baseline benchmarks
./bench.sh baseline

# Set as comparison baseline  
cp bench_baseline_20250724T123456Z_norm.txt bench_norm.txt

# Future runs will automatically compare against this baseline
./bench.sh my-feature
```

#### Report Structure

`benchcmp` generates two complementary tables:

**1. Best-to-Best Performance Analysis**
- Compares optimal static combiner limits between baseline and current
- Shows throughput and latency changes for your code change
- Indicates latency threshold violations
- Grouped by flush/duration ratio (1x, 10x, 100x) for pattern analysis

**2. Unlimited Configuration Analysis**  
- Analyzes how static limits compare to unlimited configurations
- Shows ∞→∞ (unlimited to unlimited), B→∞ (baseline best to unlimited), C→∞ (current best to unlimited)
- Provides efficiency insights about combiner architecture
- Helps identify when concurrency controller needs updates

#### Key Features

- **Automatic best limit identification**: Follows BENCHMARKING.md rules (excludes unlimited -1)
- **Statistical significance testing**: Uses benchmath for proper p-value calculation  
- **P50 and P99 latency analysis**: Reports both median and tail latency with vs. ideal comparisons
- **Focused significance markers**: ! for strong bad effects only, ~ for non-significant changes
- **Consistent percentage differences**: Negative latency = improvement, positive = degradation
- **Clean visual grouping**: Ratio-based sorting reveals operational patterns
- **Threshold validation**: Flags latencies exceeding flush period + duration deadlines

## Key Principles

### 1. Focus on Peak Performance at Optimal Configurations

**DO:**
- Compare performance at the **best-performing static combiner limits** for each scenario
- Identify "best" by examining positive combiner limits (1, 2, 3, 4, 8, 12, 16, 24) - **unlimited (-1) is NOT a candidate for "best"**
- Choose the limit with the best combination of throughput and p99 latency
- Recognize that different workload/duration/flush combinations have different optimal limits
- Compare gatherOnly to gatherOnly separately
- Understand that regressions at suboptimal limits (too high or too low) are acceptable

**DON'T:**
- Consider unlimited (-1) as a candidate for "best static limit"
- Average or aggregate performance across all combiner limits
- Worry about performance at suboptimal combiner limit configurations
- Expect the ideal computed concurrency to match the actual best-performing limit

### 2. Understand the Benchmark Output Format

In PSG-Go benchmark output, **values come BEFORE labels**:
```
1 ideal-combiner-concurrency    # means ideal-combiner-concurrency = 1
100000 ideal-tasks/sec          # means ideal-tasks/sec = 100000
```

### 3. Critical Metrics to Compare

For each workload scenario, examine:
1. **Tasks/sec** (throughput) at the best static limit
2. **p99-workflow-latency** at the best static limit
3. **Unlimited (-1) performance** vs best static limit

### 4. Latency Evaluation Context

**A p99 latency is acceptable if it's less than the flush period + task duration:**
- p99 latency of 500µs with 1ms flush period + 100µs duration = ✅ GOOD (500µs < 1.1ms)
- p99 latency of 2ms with 1ms flush period + 100µs duration = ❌ BAD (2ms > 1.1ms)

This threshold accounts for the time needed for the final gather operation after the flush period expires. The flush period defines when combining stops, but tasks still need their duration to complete.

## Using benchstat Effectively

### Basic Filtering

Use benchstat's filter syntax to extract specific metrics:

```bash
# Compare specific scenario with just the metrics we care about
benchstat -filter '.name:CombinerThroughput /workload:processing /duration:10µs /flushPeriod:10µs .unit:(p99-workflow-latency-ns OR tasks/sec)' \
  -table '/workload,/duration,/flushPeriod' \
  bench_norm_old.txt bench_norm.txt
```

### Filter Syntax
- `.name:` - Match benchmark name
- `/key:value` - Match specific parameter values
- `.unit:` - Match specific metrics
- `OR` - Combine multiple conditions
- Wildcards (`*`) are supported in values

## Analysis Workflow

### 1. Identify Best Static Limits

For each workload/duration/flush combination:
1. Extract throughput (tasks/sec) for all **positive** combiner limits (1, 2, 3, 4, 8, 12, 16, 24)
2. Consider both throughput AND p99 latency to identify the best overall performer
3. Note this as the "best static limit" for that scenario
4. Remember: unlimited (-1) is NOT a candidate for "best" - it gets evaluated separately

Example:
```bash
# Extract all throughput values for a scenario (excluding unlimited)
benchstat -filter '.name:CombinerThroughput /workload:waiting /duration:10µs /flushPeriod:10µs /method:combine .unit:tasks/sec' \
  bench_norm.txt | grep "combinerLimit=" | grep -v "combinerLimit=-1" | \
  awk '{print $1, $2}' | sed 's/CombinerThroughput.*combinerLimit=//' | sed 's/-12//' | sort -k2 -rn
```

### 2. Compare Performance at Best Limits

**Pay attention to statistical significance AND measurement variance:**
- Only report performance changes when p < 0.05 (statistically significant) 
- When p ≥ 0.05, state "no statistically significant change" rather than claiming regression/improvement
- **CRITICAL**: Always check baseline measurement variance before concluding regressions exist - benchmarks can have significant variance (e.g., ±13% was observed in recent analysis)
- Verify that apparent differences fall outside overlapping measurement ranges before reporting performance changes
- Consider error margins when identifying "best" - overlapping ranges may not be meaningfully different

When best static limits differ between baseline and current:
- Check if the limits are actually statistically different within their error margins
- Compare performance at BOTH limits to understand the shift
- Report the best-to-best comparison (even if limits differ)

For each scenario's best static limit, compare:
- **Before vs After throughput** (tasks/sec) - note p-value and significance
- **Before vs After p99 latency** - note p-value and significance  
- Whether p99 latency meets the flush period + duration deadline

### 3. Evaluate Unlimited (-1) Configuration

Compare unlimited performance to the best static limit in each benchmark:
- **Baseline**: How does unlimited compare to baseline's best static limit?
- **Current**: How does unlimited compare to current's best static limit?
- Large discrepancies or degradation indicate the concurrency controller needs updating

### 4. Check Both Workload Types

Always analyze both:
- **Processing workloads** (CPU-bound work)
- **Waiting workloads** (blocking/sleeping tasks)

Optimizations that help one may hurt the other.

## Example Analysis

```bash
# 1. Extract data for key scenarios
for scenario in "10µs/10µs" "100µs/100µs" "100µs/1ms" "1ms/1ms" "1ms/10ms"; do
  duration=$(echo $scenario | cut -d'/' -f1)
  flush=$(echo $scenario | cut -d'/' -f2)
  
  echo "=== Processing $duration duration, $flush flush ==="
  benchstat -filter ".name:CombinerThroughput /workload:processing /duration:$duration /flushPeriod:$flush .unit:(p99-workflow-latency-ns OR tasks/sec)" \
    -table '/workload,/duration,/flushPeriod' \
    bench_norm_old.txt bench_norm.txt | grep -E "combinerLimit=(-1|1|2|3|4)-12|geomean"
done

# 2. Don't forget waiting workloads!
benchstat -filter '.name:CombinerThroughput /workload:waiting /duration:10µs /flushPeriod:10µs .unit:(p99-workflow-latency-ns OR tasks/sec)' \
  -table '/workload,/duration,/flushPeriod' \
  bench_norm_old.txt bench_norm.txt
```

## Interpreting Results

### Good Result Example
```
Processing 10µs/10µs:
- Baseline best: combinerLimit=2 (35.25k tasks/sec, 1.426ms p99)
- Current best: combinerLimit=2 (38.93k tasks/sec, 1.186ms p99)
- Result: +10.44% throughput, -16.85% latency ✓
- Unlimited: Performs comparably to best in both cases ✓
```

### Problematic Result Example  
```
Waiting 10µs/10µs:
- Baseline best: combinerLimit=3 (3.968k tasks/sec)
- Current best: combinerLimit=16 (3.739k tasks/sec) 
- Result: Best static limit shifted, ~10% throughput loss
- Unlimited: Was 1.4% better than best, now 5.7% worse ✗
```

## Common Pitfalls

1. **Don't include unlimited (-1) when identifying "best" static limits** - Unlimited is evaluated separately against the best
2. **Don't compare at arbitrary combiner limits** - Always find and compare at the best-performing limits  
3. **Don't aggregate/average across combiner limits** - Only best-to-best comparisons matter
4. **Don't panic about latencies under the flush period + duration threshold** - They're meeting their deadline
5. **Don't ignore waiting workloads** - They often reveal contention issues
6. **Don't confuse ideal-combiner-concurrency with actual best limit** - They often don't match
7. **Don't use raw bench.txt** - Use bench_norm.txt for normalized results
8. **Don't forget to compare gatherOnly separately** - It's a different operation with its own performance characteristics

## Measurement Methodology Lessons

### Measurement Overhead Compensation

**Key Discovery**: Measurement work itself can be a performance bottleneck, affecting benchmark results and workload fidelity.

#### Background: Reservoir Sampling vs TDigest Investigation

This lesson emerged from an investigation to replace tdigest-based latency measurement with reservoir sampling, motivated by concerns about tdigest pooling complexity and NaN values in combine workflow latency measurements.

**Reservoir Sampling Approach:**
- Fixed-size arrays with atomic operations distributed across combine/gather phases
- Simpler memory management (no pooling complexity)
- Statistical approximation of quantiles

**TDigest Approach:**  
- Pooled tdigest objects with measurement work concentrated in gather phase
- Exact quantile calculations
- More complex memory management

**Initial Results:**
- Reservoir sampling: +18.7% throughput improvement
- TDigest: Individual operations faster, but lower system throughput

**Root Cause Discovery:**
The performance difference wasn't due to the algorithm choice, but to **when measurement work was performed**:
- Reservoir sampling distributed atomic operations across the processing pipeline
- TDigest concentrated measurement work in the gather phase, creating bottlenecks

**Key Insight:** The distribution of measurement work matters more than the measurement algorithm itself.

#### The Problem
- Atomic operations for metrics collection added per-operation overhead
- CPU-intensive measurement work converted "waiting" workloads to CPU-bound workloads
- Measurement timing affected different implementations differently

#### The Solution: `simulateWorkFrom()` Pattern
```go
// Front-load measurement work, then adjust simulated work duration
func simulateWorkFrom(baseWork time.Duration, measurementStart time.Time) {
    measurementOverhead := time.Since(measurementStart)
    adjustedWork := max(1, baseWork - measurementOverhead)
    // Now simulate the adjusted work duration
}
```

#### Results from Applying Compensation

**Final Outcome: Enhanced TDigest Implementation**
After discovering that measurement timing was the key factor, the insights were applied to the original tdigest implementation:

| Metric | Original TDigest | Reservoir Sampling | Enhanced TDigest | Best Improvement |
|--------|------------------|-------------------|------------------|------------------|
| **Throughput** | 7,598 tasks/sec | 9,018 tasks/sec (+18.7%) | 9,822 tasks/sec | **+29.3%** |
| **Latency/op** | 569.4µs | 577.8µs | 556.0µs | **-2.3%** |
| **Memory/op** | 1,034 B | 1,112 B | 1,012 B | **-2.1%** |

**Key Findings:**
- **29% throughput improvement** by eliminating measurement bottleneck (exceeds reservoir sampling gains)
- **Preserved workload fidelity** - waiting workloads remain I/O-bound  
- **More accurate baselines** for comparing optimizations
- **Exact quantiles preserved** vs statistical approximation
- **Proven implementation** with measurement methodology improvements

**Algorithm Choice vs Implementation Quality:**
The investigation revealed that implementation quality (measurement timing, overhead compensation) matters more than algorithm choice (reservoir vs tdigest). The "losing" algorithm with better implementation methodology outperformed the "winning" algorithm with naive implementation.

### Workload Fidelity Considerations

**Waiting workloads** should remain I/O-bound:
- Use `time.Sleep(max(1, duration))` to ensure scheduler yielding
- Compensate for measurement overhead to preserve intended timing
- Avoid converting blocking workloads to CPU-bound through measurement artifacts

**Processing workloads** should remain CPU-bound:
- Measurement overhead is part of the CPU work and doesn't need compensation
- Focus on measurement efficiency rather than compensation

### Statistical Analysis Requirements

**Beyond p-values** - Consider measurement variance:
- Check if baseline measurements have significant variance (e.g., ±13%)
- Verify that apparent differences fall outside overlapping measurement ranges
- Use targeted benchmarking to isolate individual changes when comprehensive analysis is inconclusive

### Fair Comparison Requirements

**Eliminate unrelated code differences** before comparing approaches:

#### Example: "Extra Clauses" in Select Statements

When comparing RDVQ vs channel+idle-queue patterns, the original baseline had unnecessary select clauses:

```go
// Original baseline (unfair comparison):
select {
case gather := <-j.gatherChan:
    // process gather...
case <-j.state.Done():    // Unnecessary in non-blocking operation
    return false, nil
case <-ctx.Done():        // Unnecessary in non-blocking operation  
    return false, ctx.Err()
default:
    return false, nil
}

// Fair comparison baseline:
select {
case gather := <-j.gatherChan:
    // process gather...
default:
    return false, nil
}
```

**Impact**: Removing unnecessary clauses accounted for ~3.6% of the initially observed performance difference.

**Lesson**: Always ensure you're comparing functionally equivalent code. Unrelated differences can mask or exaggerate the actual impact of the optimization being tested.

## Red Flags in Results

- Waiting workload throughput regressions > 20% **that exceed measurement variance**
- P99 latencies exceeding flush periods (especially if worsening) **outside measurement error**
- Unlimited (-1) performing significantly worse than best static limits **beyond statistical noise**
- Consistent regressions across multiple scenarios **that fall outside measurement ranges**
- **Important**: Always verify that apparent issues are not measurement artifacts by checking baseline variance and conducting targeted testing

## Summary Checklist

- [ ] Identified best static combiner limit for each scenario
- [ ] Compared throughput at best limits (before vs after)
- [ ] Compared p99 latency at best limits (before vs after)
- [ ] Evaluated latencies against flush period thresholds
- [ ] Checked unlimited (-1) performance vs best static limits
- [ ] Analyzed both processing AND waiting workloads
- [ ] Used benchstat with proper filters for clean comparisons
- [ ] Considered whether regressions are in acceptable scenarios