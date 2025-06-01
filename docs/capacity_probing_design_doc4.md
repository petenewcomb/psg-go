# Adaptive Capacity Probing Algorithm Design

## Overview

This document describes an adaptive algorithm for determining optimal capacity in systems with backpressure mechanisms, where the goal is to balance efficiency (low utilization of fallback resources) with throughput (maximum sustainable performance). The algorithm is designed to minimize the number of capacity probes required while reliably identifying key inflection points in both utilization and throughput behavior.

## Problem Context

The system operates in an environment where capacity can range from minimal (1 unit) to several hundred units, with no predetermined upper limit. A backpressure mechanism ensures that the input rate cannot exceed the system's sustainable throughput, providing stable measurements once a new capacity level stabilizes (typically within 10 milliseconds). However, workloads can vary significantly - some remain stable over time while others change frequently, requiring the algorithm to adapt through sample aging and expiration.

The core challenge is that higher capacity incurs costs beyond simple resource contention, including increased data production and decreased batching efficiency. This creates a multi-objective optimization problem where the cheapest solution (minimal capacity) may not provide adequate performance, while excessive capacity wastes resources even if it technically works.

## Measurement Architecture

The system monitors utilization of a "spillover" resource that serves as a fallback when primary capacity is insufficient. This creates a characteristic U-shaped utilization pattern: at low capacity, spillover utilization is high due to unsatisfied demand; at optimal capacity, spillover utilization is minimal as primary resources handle the load efficiently; at excessive capacity, spillover utilization may rise again due to coordination overhead and resource contention effects.

Throughput measurements typically follow a pattern of initial linear scaling with capacity, followed by diminishing returns as various bottlenecks emerge. The algorithm leverages these two measurement signals - utilization and throughput - to navigate toward optimal capacity configurations.

## Core Algorithm

The algorithm employs a decision tree based on pattern recognition in both utilization and throughput data. Rather than using complex confidence metrics or statistical analysis, it uses binary detection ("pattern found" or "pattern not found") combined with simple validation checks. This approach proves more robust with the limited sample sizes (typically 6-10 samples) required for practical convergence.

### Primary Decision Logic

The algorithm first attempts to detect a utilization valley (any sample with non-high utilization) and a throughput knee (transition from linear to sub-linear scaling). Based on these detections, it follows one of four paths:

```go
switch {
case valleyFound:
    // Fine-tune around the efficiency optimum
    return probeNearValley()
    
case allLinearThroughput:
    // Continue scaling to find system limits  
    return scaleUpAggressively()
    
case noLinearThroughput:
    if onlyOneSample {
        // Insufficient data for linearity testing
        return scaleUpAggressively() 
    } else {
        // Multiple samples show non-linear behavior
        return scaleDown()
    }
    
default: // knee found but no valley
    // Probe around the throughput transition
    return probeNearKnee()
}
```

This structure handles a critical edge case: when `linearEndIndex == 0` with only one sample, the algorithm cannot actually determine linearity (since linearity testing requires at least two samples plus the origin). In this case, it treats the situation as insufficient data rather than evidence of non-linear behavior, and continues scaling upward to gather more information.

### Valley Detection

Valley detection uses a binary threshold approach, identifying the first sample where spillover utilization falls below a configurable threshold (tunable parameter). This simple "first acceptable utilization" strategy proves more effective than sophisticated pattern matching with limited sample sizes:

```go
func findUtilizationValley() int {
    for i, sample := range samples {
        if sample.Utilization < highUtilThreshold {
            return i
        }
    }
    return len(samples) // No valley found
}
```

### Knee Detection Through Linear Analysis

Knee detection employs a sequential analysis approach, determining how far into the sample sequence throughput remains linear from the origin. The algorithm maintains running statistics of throughput-to-capacity ratios and uses normalized range as a linearity test:

```
normalizedRange = (maxRatio - minRatio) / meanRatio
```

When this normalized range exceeds a configurable tolerance (tunable parameter), linearity has broken down. The algorithm can skip a limited number of outlier samples (tunable parameter) to maintain robustness against measurement noise while remaining sensitive to genuine pattern changes.

This approach directly tests the line that would actually be used for prediction (origin to last sample) rather than computing abstract statistical fits, ensuring consistency between detection and application.

### Probing Strategies

The algorithm employs three distinct probing strategies based on the current situation:

**Aggressive scaling** multiplies the current highest capacity by a significant factor (tunable parameter, typically 2-3x) when clear directional signals indicate the need to cover ground quickly. This occurs when all samples show high utilization and throughput scaling remains linear.

**Conservative scaling** applies a smaller multiplication factor (tunable parameter, typically 1.3-1.5x) when refinement is needed or mixed signals suggest caution. This strategy helps avoid overshooting promising regions.

**Gap probing** targets the midpoint between existing samples when refining around detected valleys or knees. If the gap between consecutive samples provides room for only one additional sample, the algorithm returns the existing base sample rather than creating duplicates.

## Implementation Insights

The final implementation proved significantly simpler than initial designs through several key insights:

**Binary thresholds eliminate decision paralysis.** Rather than computing confidence scores or statistical significance measures, the algorithm uses simple pass/fail criteria. With limited sample sizes, this binary approach provides clearer decision boundaries and faster convergence.

**Utilization patterns drive strategy while throughput patterns guide direction.** High utilization indicates the need to search for relief, while throughput linearity determines whether to search up (linear scaling continues) or down (system limits reached).

**Gap analysis simplifies to geometric midpoints.** Instead of applying growth factors within constrained spaces, the algorithm simply targets the midpoint between existing samples. This provides good coverage while avoiding the complexity of constrained optimization.

**Edge cases absorb into main logic.** Cold start scenarios and insufficient data conditions integrate naturally into the primary decision tree rather than requiring separate handling paths.

## Operational Model

The algorithm operates as a continuous calibration loop rather than a one-time optimization. It runs periodically (timing determined by operational requirements), with older samples aging out as workload characteristics change. This model handles dynamic conditions naturally - workload changes, seasonal patterns, and system modifications all get reflected through sample renewal without requiring explicit change detection.

Each invocation of the algorithm answers a single question: "Given current knowledge, what capacity should we test next to improve our understanding?" The broader system manages sample lifecycle, scheduling, and stopping criteria based on operational constraints.

## Design Decisions and Alternatives

### Binary vs Confidence-Based Detection

The algorithm uses binary pattern detection rather than confidence scores or statistical significance testing. With typical sample sizes of 6-10 points, confidence calculations become unreliable and create ambiguous "medium confidence" scenarios that complicate decision-making. Binary detection with simple validation provides clearer decision boundaries and faster convergence.

### Range vs Standard Deviation for Linearity

Linearity testing uses normalized range (max-min)/mean rather than coefficient of variation or R-squared measures. While less statistically sophisticated, range calculation avoids square root operations and provides adequate sensitivity for detecting when linear scaling breaks down. The primary goal is practical pattern recognition rather than precise statistical modeling.

### Sequential vs Regression Analysis

The knee detection algorithm analyzes samples sequentially to find where linearity from origin breaks down, rather than performing regression analysis over the entire dataset. This approach tests against the actual line that would be used for prediction (origin to final sample) and avoids the conceptual disconnect between regression-based detection and practical application.

### Midpoint vs Growth Factor Gap Probing

Gap probing uses geometric midpoints rather than applying configurable growth factors within constrained spaces. This eliminates the complexity of constrained optimization while providing good sampling coverage. The midpoint strategy proves simple to implement and reason about while maintaining effective refinement capabilities.

## Tunable Parameters

The algorithm requires four primary tunable parameters:

- **Utilization threshold**: Defines the boundary between "high" and "acceptable" utilization for binary valley detection
- **Linearity tolerance**: Controls sensitivity of knee detection through normalized range comparison  
- **Aggressive scaling factor**: Multiplication factor for rapid capacity exploration (typically 2-3x)
- **Conservative scaling factor**: Multiplication factor for careful refinement (typically 1.3-1.5x)

Additional parameters control outlier handling (maximum skips in linearity testing) and operational constraints (sample retention policies), but these can often use fixed values derived from system characteristics rather than requiring tuning.

## Testing and Validation

The algorithm's effectiveness depends on its ability to converge quickly across diverse workload patterns while remaining robust to measurement noise and system variability. Key validation scenarios include:

- **Linear scaling workloads** that benefit from higher capacity
- **Saturated workloads** with clear throughput plateaus  
- **U-shaped utilization patterns** with distinct efficiency sweet spots
- **Noisy measurements** that might trigger false pattern detection
- **Dynamic workloads** where optimal capacity shifts over time

The binary decision structure and continuous operation model facilitate systematic testing of these scenarios through simulation and controlled production trials.

## Future Considerations

While the current algorithm handles the primary optimization scenarios effectively, several extensions might prove valuable for specialized environments:

**Multi-stage pattern recognition** could address systems with multiple distinct bottleneck phases, though such complexity should be weighed against the benefits of the current simple approach.

**Adaptive threshold tuning** might automatically adjust utilization and linearity thresholds based on observed system behavior, reducing the manual tuning burden.

**Integration with external signals** such as cost metrics or latency requirements could extend the optimization criteria beyond the current efficiency and throughput focus.

However, any such extensions should preserve the algorithm's core strengths: fast convergence, simple decision logic, and minimal parameter tuning requirements.