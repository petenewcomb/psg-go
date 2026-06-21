# Adaptive Combiner Pool Sizing

## Problem Context

PSG's combiner pools face a fundamental resource allocation challenge: determining the optimal number of goroutines to maximize throughput while minimizing resource waste and latency. This problem is more complex than traditional capacity planning because of several unique characteristics of the PSG architecture.

### The Core Dilemma

Too few goroutines create a capacity bottleneck. Tasks queue up waiting for available combiners, spare channels become heavily utilized, and overall throughput suffers. The system is clearly under-provisioned, but how much should we scale up?

Too many goroutines create a different problem entirely. Excessive goroutines compete for shared resources - memory bandwidth, CPU cache lines, locks, and coordination overhead. Paradoxically, this contention can drive individual goroutine utilization higher while simultaneously reducing overall throughput. The system appears busy but is actually thrashing.

The optimal point lies somewhere between these extremes, but traditional capacity planning approaches fail to find it reliably.

### Why Traditional Approaches Fail

**Queueing theory assumes independence** between arrival rate and service capacity. PSG's backpressure mechanism violates this assumption - when the system reaches capacity, backpressure artificially constrains the arrival rate. You cannot measure true demand independently from current service capacity, making classical M/M/c models inapplicable.

**Simple threshold-based control** (spawn at 80% utilization, terminate at 20%) cannot distinguish between "high utilization because we need more capacity" and "high utilization because contention is making existing goroutines inefficient." Both scenarios show high utilization, but one requires scaling up while the other requires scaling down.

**Fixed scaling factors** (always double when scaling up) are too rigid. The optimal goroutine count varies dramatically based on workload characteristics, available CPU cores, memory bandwidth, and task complexity. A one-size-fits-all approach cannot adapt to this variability.

### The Spare Utilization Signal

PSG's architecture provides a unique observability window through spare channel utilization. In PSG's two-channel design, only one goroutine can elect itself as "spare," exclusively monitoring the spare channel for overflow work when primary channels are busy.

Spare utilization exhibits a characteristic U-shaped curve as goroutine count increases:

- **Under-provisioned** (few goroutines): Spare utilization approaches 80-100% because insufficient overall capacity forces the spare goroutine to handle disproportionate work
- **Well-provisioned** (optimal goroutines): Spare utilization moderates to 30-60% as the spare handles overflow without being overwhelmed
- **Over-provisioned** (excessive goroutines): Spare utilization initially drops to 20-40% due to work spreading across many goroutines, then rises again to 60-90% as contention effects dominate

This U-shape creates ambiguity - high spare utilization could indicate either end of the spectrum. The algorithm must combine utilization with throughput measurements to distinguish between these cases.

### Performance Knee Detection

Concurrent systems typically exhibit a characteristic throughput pattern: linear scaling up to some point, then diminishing returns as contention effects dominate. The "knee" - where linear scaling transitions to diminishing returns - represents the optimal operating point for many workloads.

However, detecting a true knee requires more than just observing throughput plateaus. The algorithm must identify changes in slope (throughput per additional goroutine) rather than absolute throughput levels. A premature knee detection based on limited data can cause the system to settle at severely suboptimal configurations.

## Mathematical Approach

The current implementation takes a sampling-based approach that builds a piecewise understanding of the performance landscape through focused exploration.

### Core Data Structure

The algorithm maintains a collection of performance samples, each containing:
- **Timestamp**: When the measurement was taken
- **Goroutine count**: The number of active combiner goroutines
- **Throughput**: Operations completed per unit time
- **Spare utilization**: Fraction of time the spare goroutine was busy

Samples are kept sorted by goroutine count and aged out based on a configurable retention period to adapt to workload changes.

### Valley Detection

The algorithm searches for the "utilization valley" - the lowest spare utilization point that indicates efficient resource allocation. Valley detection simply finds the sample with minimum spare utilization, with basic outlier handling to avoid being misled by measurement noise.

The valley represents the sweet spot where the system has sufficient capacity without excessive competition between goroutines.

### Knee Detection Through Return Analysis

Rather than complex curve fitting, knee detection focuses on the economic concept of marginal return. For each adjacent pair of samples, the algorithm calculates the "return rate" - how much additional throughput is gained per additional goroutine.

When the return rate falls below a configurable threshold (typically 1-2% return per goroutine), the algorithm declares a knee. This approach captures the essential insight that diminishing returns, not absolute throughput levels, define the optimal operating point.

### Decision Logic

The algorithm's recommendation logic follows a clear hierarchy:

1. **Valley-based optimization**: If a utilization valley exists with acceptable utilization levels, operate near the valley to minimize resource usage while maintaining good throughput

2. **Knee-based optimization**: If no suitable valley exists but a throughput knee is detected, operate at or slightly below the knee to maximize throughput efficiency

3. **Exploration modes**: When neither valley nor knee provides clear guidance:
   - If all samples show high utilization with good throughput growth: scale up aggressively (2.5x factor)
   - If samples show mixed signals: scale up conservatively (1.3x factor)  
   - If throughput growth is poor: scale down

### Scaling Strategies

**Aggressive scaling** applies when all measurements indicate under-provisioning - high utilization everywhere with good throughput returns. The 2.5x factor enables rapid exploration of higher goroutine counts.

**Conservative scaling** applies when signals are mixed or when approaching suspected optimal regions. The 1.3x factor provides steady exploration without overshooting.

**Gap filling** targets unexplored regions between existing samples to improve the resolution of the performance map in areas where patterns are unclear.

**Scale-down** occurs when throughput returns are poor, indicating the system has exceeded optimal capacity and contention effects dominate.

### Temporal Adaptation

The algorithm adapts to changing workloads through sample aging. Samples older than the retention period (typically 1 second) are discarded, allowing the performance model to evolve as conditions change. This prevents the algorithm from being anchored to obsolete performance characteristics.

## Implementation Lessons

### Premature Knee Detection

Early implementations suffered from declaring victory too soon. When testing only 1-2 goroutine counts, the algorithm would detect an apparent "knee" and stop exploration, often settling on severely suboptimal configurations (2 goroutines achieving 2000 ops/sec when 4+ could achieve 4000 ops/sec).

The lesson: **throughput thresholds without slope evidence are meaningless**. A true knee requires demonstrating that additional goroutines provide diminishing returns, not just that current throughput seems "good enough."

### Units and Precision

The data pipeline initially appeared to show zero throughput due to unit mismatches. EMAs tracked operations-per-nanosecond (tiny numbers like 0.000001946) while debug output formatted these as 0.0, creating the illusion of missing data.

The lesson: **careful attention to units and debug formatting precision is critical** for algorithm development and troubleshooting.

### Utilization vs. Throughput Priority

Early strategies prioritized reaching "reasonable" utilization levels over maximizing throughput, leading to local optimization where exploration stopped prematurely.

The lesson: **high utilization should drive continued exploration**, not termination. Utilization provides direction (scale up or down) but throughput optimization should remain the primary goal.

### Measurement Stability

Direct throughput and utilization measurements are noisy. The algorithm uses Exponential Moving Averages (EMAs) to smooth these signals, but critically, **scaling decisions only occur when measurements are stable** - when recent changes are small relative to the current values.

This stability requirement prevents oscillation and ensures the algorithm responds to genuine performance changes rather than measurement noise.

## Rejected Alternatives

### Complex Statistical Models

Approaches using regression analysis, confidence intervals, and sophisticated curve fitting were rejected as over-engineered for the problem domain. With only 6-10 samples typical for convergence, complex statistical methods become unreliable and add unnecessary computational overhead.

### Continuous Optimization

Gradient-based approaches that constantly perturb the system to measure sensitivity were rejected due to the high cost of continuous exploration. The focused exploration session approach minimizes time spent in suboptimal configurations.

### Machine Learning

ML approaches were considered but rejected as inappropriate for a general-purpose library. They would require training data, exhibit black-box behavior that's difficult to debug, and add complexity without clear benefits over the simpler mathematical approach.

### PID Controllers

Classical control theory approaches failed because utilization alone doesn't indicate the correct action. High utilization might require more goroutines (under-provisioning) or fewer goroutines (contention), making traditional feedback control inappropriate.

### Long-term Memory

Maintaining extensive historical performance data was rejected due to memory overhead and the risk of being anchored to obsolete workload patterns. The sample aging approach provides adaptation while keeping memory usage bounded.

## Empirical Validation

### Auto-discovery Effectiveness

Benchmark results consistently show that unlimited pools (using this algorithm) outperform manually-configured fixed limits. The algorithm discovers optimal concurrency levels that vary significantly across different workload characteristics - combinations of task duration, flush periods, and system resources that are difficult to predict manually.

### Workload Adaptation

Different workload types require substantially different optimal goroutine counts. A batch processing workload might peak at 2-3 goroutines, while a high-frequency trading scenario might need 12-16. The algorithm successfully adapts to these variations without manual tuning.

### Conservative Exploration Benefits

The algorithm's measured approach prevents the performance degradation seen in fixed-limit configurations that exceed optimal levels. By requiring evidence of diminishing returns before declaring convergence, it avoids the "more goroutines must be better" trap.

### Time-based vs. Count-based Flushing

The algorithm successfully handles the tension between latency bounds and throughput optimization. Short flush periods create apparent "performance regressions" that are actually intended behavior - the system prioritizes latency bounds over peak throughput. Long flush periods allow the algorithm to optimize for throughput within reasonable latency constraints.

## System Behavior

### Normal Operation

Under steady workloads, the algorithm typically converges within 6-10 measurement cycles, finds the optimal goroutine count, and then operates stably at that point. Utilization settles into the moderate range (30-60%) and throughput remains consistently high.

### Workload Changes

When workload characteristics change (different task types, varying arrival rates, system resource availability), the sample aging mechanism gradually discards obsolete data. The algorithm detects that current goroutine counts no longer provide optimal performance and initiates new exploration to adapt to the changed conditions.

### Resource Constraints

When system-wide resource limits are reached (CPU cores, memory bandwidth), the algorithm detects poor throughput returns and scales down rather than continuing to add goroutines that cannot be effectively utilized.

### Failure Modes

The algorithm can struggle with extremely noisy workloads where measurement stability is difficult to achieve, or with workloads that have multiple distinct performance peaks separated by valleys. In practice, these scenarios are rare, and the conservative exploration approach prevents severe performance degradation even when optimization is suboptimal.

## Conclusion

The adaptive combiner pool sizing algorithm represents a practical solution to a complex optimization problem. By focusing on the mathematical relationships between goroutine count, utilization, and throughput rather than attempting to predict optimal configurations, the algorithm provides robust performance across diverse workloads.

The key insight is recognizing that this is not a continuous optimization problem but rather a discrete exploration problem. The algorithm's job is to quickly map the performance landscape, identify the optimal operating point, and adapt when conditions change. This approach proves both more effective and more maintainable than attempting to predict optimal configurations through manual analysis or complex modeling.

The lessons learned - particularly around premature convergence, measurement precision, and the primacy of throughput optimization - provide valuable guidance for similar optimization problems in concurrent systems.