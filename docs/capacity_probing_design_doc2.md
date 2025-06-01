# Adaptive Capacity Probing Algorithm Design

## Overview

This document describes an adaptive algorithm for determining optimal capacity in systems with backpressure, where we need to balance efficiency (low utilization) and throughput (maximum sustainable performance). The algorithm minimizes the number of capacity probes required while confidently identifying characteristic inflection points in both utilization and throughput curves.

## Problem Statement

### System Characteristics
- **Capacity range**: 1 to several hundred units (no specific upper limit)
- **Backpressure mechanism**: Ensures input rate cannot exceed sustainable throughput
- **Measurement stability**: ~10ms to achieve stability at new capacity level
- **Workload variability**: Some workloads stable, others change frequently
- **Cost considerations**: Higher capacity incurs costs beyond simple contention (increased data production, decreased batching efficiency)

### Goals
- **Minimize probe count**: Converge quickly (6-10 total samples for most workloads)
- **Handle noise**: Robust against measurement variability
- **Adapt to change**: Handle workload changes via sample aging/expiration
- **Simple and explainable**: Avoid complex heuristics requiring tuning

## Key Concepts and Definitions

### Utilization Patterns
- **Spillover utilization**: Measurement of fallback resource used only when primary capacity unavailable
- **Expected U-shape**: 
  - Left side (low capacity): High utilization due to unsatisfied demand
  - Bottom (sweet spot): Low utilization, primary capacity handles most load
  - Right side (high capacity): Rising utilization due to resource contention/coordination overhead

### Throughput Patterns
- **Expected pattern**: Linear scaling → diminishing returns (knee)
- **Linear**: Samples align within tolerance (consistent slope)
- **Linear from origin**: Linear AND line passes through (0,0)
- **Valid knee**: Inflection between linear-from-origin samples and diminishing returns

### Measurement Approach
- **Binary detection**: "Detected or not" rather than confidence levels
- **Temporal decay**: Recent samples weighted more heavily
- **Outlier tolerance**: Drop single worst outlier when needed

## Decision Flow Algorithm

### High-Level Flow
1. **Data quality check**: Handle noise, ensure minimum samples
2. **Knee detection**: Sequential analysis to find throughput limits
3. **Valley detection**: Find utilization efficiency sweet spot
4. **Recommendation**: Choose between valley (efficiency) and knee (throughput)

### Detailed Decision Tree

```
Current Samples
    ↓
Noise too high or < 3 samples?
    ├─ Yes → Add samples in sparse regions
    └─ No → Valid throughput knee detected?
        ├─ Yes → Valid utilization valley detected?
        │   ├─ Yes → Valley capacity ≤ Knee capacity?
        │   │   ├─ Yes → Valley utilization acceptable?
        │   │   │   ├─ Yes → Recommend valley capacity
        │   │   │   └─ No → Recommend just below knee
        │   │   └─ No → Sample between (resolve conflict)
        │   └─ No → Samples below knee capacity?
        │       ├─ Yes → Valley likely shallow/absent → Recommend knee
        │       └─ No → Sample between low capacity and knee
        └─ No → All samples linear from origin?
            ├─ Yes → Utilization high for all samples?
            │   ├─ Yes → Probe higher aggressively
            │   └─ No → Probe higher conservatively  
            └─ No → Samples linear but not from origin?
                ├─ Yes → Search for valley in current range
                └─ No → Add samples (treat as noise)
```

## Core Algorithms

### Valley Detection

**Simple approach with outlier handling:**

```go
func findUtilizationValley(samples []Sample) int {
    if len(samples) < 2 {
        return 0
    }
    
    // Sort by utilization to find lowest values
    sorted := make([]Sample, len(samples))
    copy(sorted, samples)
    sort.Slice(sorted, func(i, j int) bool {
        return sorted[i].Utilization < sorted[j].Utilization
    })
    
    // Use second-lowest if lowest is dramatically different
    target := sorted[0]
    if len(sorted) > 2 && sorted[0].Utilization < sorted[1].Utilization*0.5 {
        target = sorted[1] // Drop potential outlier
    }
    
    // Find index in original samples
    for i, sample := range samples {
        if sample.Utilization == target.Utilization {
            return i
        }
    }
    return 0
}
```

### Knee Detection (Sequential Analysis)

**Find how far linear-from-origin extends, then check for knee behavior:**

```go
func detectValidKnee(samples []Sample) int {
    linearEnd := findLinearFromOriginEnd(samples, 0.2, 1)
    
    if linearEnd == -1 || linearEnd == len(samples)-1 {
        return -1 // No knee detected
    }
    
    preSamples := samples[:linearEnd+1]
    postSamples := samples[linearEnd:]
    
    if showsKneeBehavior(postSamples, preSamples) {
        return linearEnd
    }
    
    return -1
}

func showsKneeBehavior(postSamples, preSamples []Sample) bool {
    // Check if post-samples are below origin line or noisier
    originSlope := getOriginSlope(preSamples)
    
    belowLineCount := 0
    for _, sample := range postSamples {
        expected := originSlope * sample.Capacity
        if sample.Throughput < expected {
            belowLineCount++
        }
    }
    
    // Majority below line = knee behavior
    return float64(belowLineCount)/float64(len(postSamples)) > 0.5
}
```

### Linear-from-Origin Detection

**Incremental algorithm with outlier skipping:**

```go
// findLinearFromOriginEnd determines how far into the sample sequence
// the throughput remains linear from origin (throughput = slope * capacity).
//
// Uses an incremental algorithm that maintains running statistics:
// - sumRatios/count tracks the current mean ratio (throughput/capacity)
// - minRatio/maxRatio track the historical range of ratios seen
// - Linearity test: normalized range (max-min)/mean < tolerance
//
// ALGORITHM TRADEOFF: minRatio and maxRatio reflect historical extremes
// across all accepted samples, while meanRatio is recalculated each iteration.
// This means we're asking "how much spread have we seen historically, relative
// to our current best estimate of the true ratio?" Early outliers contribute
// to the range forever but get diluted in the mean as more samples are added.
//
// This asymmetry is acceptable because:
// 1. We're detecting when linear scaling breaks down, not computing precise statistics
// 2. The approach is sensitive to deviations while remaining O(n) efficient
//
// OUTLIER HANDLING: Can skip up to maxSkips samples that would break linearity,
// allowing robustness against measurement noise while remaining sensitive to
// real system behavior changes.
//
// Returns the index of the last sample that maintains linearity from origin.
// Returns len(samples)-1 if all samples are linear (possibly with skips).
func findLinearFromOriginEnd(samples []Sample, tolerance float64, maxSkips int) int {
    if len(samples) < 2 {
        return len(samples) - 1
    }
    
    var sumRatios float64
    var count int
    minRatio := math.Inf(1)
    maxRatio := math.Inf(-1)
    skipsUsed := 0
    lastGoodIndex := -1
    
    for i, sample := range samples {
        ratio := sample.Throughput / sample.Capacity
        
        // Test adding this ratio
        testSum := sumRatios + ratio
        testCount := count + 1
        testMin := math.Min(minRatio, ratio)
        testMax := math.Max(maxRatio, ratio)
        
        if testCount >= 2 {
            meanRatio := testSum / float64(testCount)
            normalizedRange := (testMax - testMin) / meanRatio
            
            if normalizedRange > tolerance {
                if skipsUsed < maxSkips {
                    skipsUsed++
                    continue
                }
                return lastGoodIndex
            }
        }
        
        // Point is good
        sumRatios = testSum
        count = testCount
        minRatio = testMin
        maxRatio = testMax
        lastGoodIndex = i
    }
    
    return len(samples) - 1
}
```

## Probing Strategies

### Aggressive Probing
- **When**: All samples linear from origin + high utilization everywhere
- **Factor**: 2.5x capacity increase
- **Rationale**: System clearly under-provisioned, continue exponential scaling

### Conservative Probing  
- **When**: Linear from origin but some utilization improvement seen
- **Factor**: 1.3x capacity increase  
- **Rationale**: Avoid overshooting improvement, maintain discovery rate

### Gap Filling
- **When**: Non-linear data without clear patterns
- **Strategy**: Sample sparse regions in existing capacity range
- **Rationale**: Pattern exists but insufficient resolution to detect

## Design Decisions and Tradeoffs

### Binary Detection vs Confidence
- **Choice**: Binary "detected/not detected" with simple validation
- **Rationale**: Few samples (6-10) make confidence calculations unreliable
- **Benefit**: Clear yes/no decisions, fast convergence

### Range vs Standard Deviation
- **Choice**: Use min/max range instead of standard deviation
- **Rationale**: Simpler computation, avoids square roots
- **Tradeoff**: Less statistically rigorous but adequate for linearity testing

### Origin Intersection Priority
- **Choice**: Test linear-from-origin before general knee detection
- **Rationale**: If scaling linearly from origin, knee doesn't exist yet
- **Benefit**: Eliminates impossible conditions, clearer decision flow

### Outlier Handling
- **Choice**: "Drop worst outlier" approach with skip tolerance
- **Rationale**: Simple, doesn't require statistical parameters
- **Implementation**: Maximum 1 skip allowed in linear sequences

### Temporal Management
- **Choice**: Rely on external sample expiration rather than internal aging
- **Rationale**: Keeps algorithm stateless, handles workload changes externally
- **Assumption**: Old samples pruned before algorithm runs

## Alternatives Considered and Rejected

### Confidence-Based vs Binary Detection

**Alternative**: Use confidence scores (0-1) for valley and knee detection, with iterative refinement until confidence exceeds thresholds.

**Rejected because**:
- With only 6-10 samples, statistical confidence calculations become unreliable
- "Medium confidence" creates decision paralysis - unclear what action to take
- Complex confidence building loops add unnecessary complexity
- Binary approach with simple validation is more robust with sparse data

### Complex State Machine vs Decision Tree

**Alternative**: Maintain state across probe iterations, tracking whether we're "exploring", "refining valley", "refining knee", etc.

**Rejected because**:
- User requested stateless approach: "given the data we have, what should we do next?"
- State machines imply process continuity, but workloads can change between probes
- Harder to reason about and debug
- Simple decision tree based on current evidence is more robust

### Standard Deviation vs Range for Linearity Testing

**Alternative**: Use coefficient of variation (std dev / mean) for linearity testing instead of normalized range.

**Rejected because**:
- Requires square root computation (more expensive)
- Not significantly more accurate for our use case
- Range is simpler to understand and debug
- Both approaches adequate for detecting when linear scaling breaks down

### Linear Regression vs Ratio-Based Linearity Testing

**Alternative**: Use R² from linear regression through origin to test linearity.

**Rejected because**:
- Creates disconnect between linearity test and actual usage
- We test against regression line but use origin-to-last line for decisions  
- Ratio method tests against the exact line we'd actually use
- Regression adds complexity without clear benefit

### Outlier Correction vs Detection

**Alternative**: "Correct" outlier samples by projecting them onto the best-fit line, then use corrected values for downstream analysis.

**Rejected because**:
- Artificial corrections interfere with knee detection algorithms
- Could mask real system behavior changes that we need to detect
- Creates false precision in reported results
- Outliers near knee might be the signal we're looking for, not noise

### Complex Temporal Weighting vs Sample Expiration

**Alternative**: Implement decay functions, sliding windows, and adaptive confidence scoring based on sample age.

**Rejected because**:
- User specifically wanted simple, tuning-free approach
- Too many magic numbers and heuristic thresholds
- External sample expiration handles workload changes more cleanly
- Keeps algorithm stateless and easier to test

### Origin Intersection as Separate Decision Point

**Alternative**: Keep origin intersection check as separate decision branch in flowchart for "non-linear but no knee" cases.

**Rejected because**:
- Creates logical inconsistency: non-linear data should have inflection points
- Simpler to embed origin intersection test within knee validation
- "Non-linear but no valid knee" better treated as data quality issue
- Reduces decision tree complexity

### Multiple Probing Strategies  

**Alternative**: Implement aggressive, moderate, and conservative probing strategies.

**Rejected because**:
- Three strategies created unclear decision criteria
- "Moderate" strategy had no clear use case after simplification
- Binary choice (aggressive/conservative) covers the actual decision space
- Fewer strategies means fewer parameters to tune

### Separate vs Integrated Pattern Analysis

**Alternative**: Analyze utilization and throughput curves completely independently, then combine results.

**Rejected because**:
- Valley and knee should be correlated in well-behaved systems
- Integrated analysis can use one pattern to validate the other
- Sequential approach (find knee first, then valley within that range) provides natural bounds
- Separate analysis would require complex conflict resolution

### Curved Inflection Point Detection

**Alternative**: Use sophisticated curve fitting (splines, polynomials) to detect smooth transitions.

**Rejected because**:
- Too complex for limited sample sizes
- Hard to distinguish real transitions from noise with few points
- Sequential linear analysis is simpler and more robust
- Binary "knee exists or doesn't" matches our decision needs

### Adaptive Sampling Density

**Alternative**: Use mathematical optimization to determine optimal probe placement.

**Rejected because**:
- Over-engineering for practical problem constraints
- Exponential scaling + binary search provides good coverage
- Mathematical optimization requires assumptions about curve shapes
- Simple heuristics converge faster in practice

## Open Questions and Future Work

1. **Valley acceptability threshold**: What utilization level is "too high"?
2. **Noise detection criteria**: How to quantify "too noisy" for initial check?
3. **Chaos detection**: When to give up and reset with fresh samples?
4. **Multi-stage patterns**: Handling systems with multiple bottleneck phases
5. **Integration testing**: Validation against real workload patterns

## Implementation Notes

- **Language**: Go
- **Complexity**: All core algorithms O(n) where n = number of samples
- **Memory**: O(1) for algorithms, O(n) for sample storage
- **Dependencies**: Standard math library only
- **Testing**: Unit tests for each algorithm component plus integration scenarios