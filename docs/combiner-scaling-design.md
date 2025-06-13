# Dynamic Goroutine Scaling for Combiner Pools: Design Document

## Executive Summary

This document describes a control algorithm for dynamically scaling the number of goroutines in PSG's combiner pool to maximize throughput while minimizing latency and resource usage. The algorithm uses a focused exploration approach that identifies performance plateaus and operates at the minimum goroutine count needed to achieve near-optimal throughput.

## Problem Statement

PSG's combiner pools face a fundamental optimization challenge. Too few goroutines result in high utilization, increased wait times, and reduced throughput. Too many goroutines lead to resource waste, increased contention, smaller batch sizes, and more frequent flushes that can actually degrade performance.

The system must find the optimal operating point despite several challenging constraints. First, backpressure coupling means that arrival rate depends on service capacity, violating the independence assumptions of classical queueing theory. Second, performance is non-monotonic - adding goroutines can either improve or degrade latency depending on whether the bottleneck is capacity or contention. Third, workloads vary over time, requiring continuous adaptation. Fourth, individual measurements are noisy and unreliable. Finally, each configuration change has a cost in terms of disrupted batching efficiency.

## Key Insights

### Secondary Utilization as a Demand Signal

The PSG framework employs a two-channel design with primary and secondary channels for posting combiner work. Only one goroutine at a time can elect itself as "secondary," meaning it exclusively monitors the secondary channel. This design provides a natural demand indicator, though the relationship between secondary utilization and optimal provisioning is more complex than initially apparent.

#### The Secondary Utilization Curve

Secondary utilization exhibits a characteristic U-shaped relationship with goroutine count that reflects the competing effects of capacity and contention:

**Under-provisioned systems (high secondary utilization from insufficient capacity):** When there are too few goroutines relative to the workload, the secondary goroutine becomes heavily utilized (approaching 80-100% utilization). This occurs because insufficient overall capacity forces the secondary goroutine to process a disproportionate share of work. The system genuinely lacks the goroutines needed to handle the workload efficiently.

**Optimally-provisioned systems (moderate secondary utilization):** At the performance knee - the minimum goroutine count achieving near-maximum throughput - secondary utilization typically falls in the moderate range (30-60%). The secondary goroutine is productively engaged but not overwhelmed, indicating the system has sufficient capacity to handle the workload efficiently without excess contention. This represents the sweet spot of resource allocation.

**Moderately over-provisioned systems (low secondary utilization from work distribution):** When goroutine count slightly exceeds optimal levels, secondary utilization initially drops (20-40%). Work distribution across many goroutines leaves each individual goroutine, including the secondary, with less to do. The system has adequate capacity without significant contention penalties.

**Heavily over-provisioned systems (high secondary utilization from contention):** When goroutine count significantly exceeds optimal levels, secondary utilization rises again (60-90%) due to resource contention effects. Excessive goroutines compete for locks, cache lines, memory bandwidth, and CPU cores. This contention creates inefficient work patterns where goroutines spend significant time waiting for contested resources, driving up individual utilization despite having nominally "excess" capacity.

#### Implications for Scaling Decisions

This U-shaped utilization curve creates a fundamental ambiguity: high secondary utilization could indicate either under-provisioning (left side of the U) or over-provisioning with contention (right side of the U). The algorithm cannot rely on utilization alone to make scaling decisions.

This complexity necessitates the throughput-centric approach described in the next section. By measuring both utilization and throughput, the algorithm can distinguish between "high utilization because we need more goroutines" versus "high utilization because contention is making existing goroutines inefficient." When high utilization coincides with increasing throughput, more goroutines help. When high utilization coincides with declining throughput, fewer goroutines are needed.

The moderate utilization zone (30-60%) provides the most reliable signal for optimal provisioning, but even this must be validated against throughput measurements to ensure the system is truly operating at its performance knee rather than in a local efficiency valley.

### Performance Knee Detection Through Slope Analysis

Performance in concurrent systems typically follows a characteristic pattern where throughput initially increases linearly with additional goroutines, then experiences diminishing returns, eventually plateauing or even declining due to contention. The optimal operating point - the "knee" - is where the performance curve transitions from linear scaling to diminishing returns.

**Key insight: A true performance knee requires evidence of slope changes, not just a throughput threshold.** The algorithm must identify where the throughput-per-goroutine ratio decreases significantly, indicating the transition from capacity-limited to contention-limited performance.

The knee detection algorithm works by:

1. **Modeling performance as piecewise-linear segments** with different slopes and utilization characteristics
2. **Requiring multiple segments** with demonstrably different slopes before declaring a knee
3. **Identifying slope transitions** where throughput growth rate decreases significantly
4. **Validating utilization characteristics** ensuring the knee represents balanced resource allocation (moderate utilization) rather than under-provisioning (high utilization) or over-provisioning (low utilization)

This approach avoids premature convergence on false knees when insufficient exploration has been performed. Operating at the true knee provides predictable performance while minimizing resource usage.

### Focused Exploration Sessions

Traditional approaches to performance optimization often maintain long-term models of system behavior, attempting to learn and predict optimal configurations. However, this approach suffers from several drawbacks in the PSG context. Workload patterns may change, making historical data less relevant. Maintaining extensive history requires memory and computational overhead. Most importantly, the cost of exploration - time spent in sub-optimal configurations - must be minimized.

Instead, this design employs focused exploration sessions. When high utilization indicates potential under-provisioning, the system conducts a brief, systematic exploration of different goroutine counts. It quickly identifies the performance plateau, sets the optimal configuration, and then discards the exploration data. This approach minimizes both the time spent in sub-optimal configurations and the memory overhead of tracking performance history.

### Stability Through Trended EMAs

Direct measurements of throughput and utilization are noisy, making it difficult to distinguish genuine performance changes from temporary fluctuations. The algorithm addresses this by using Exponential Moving Averages (EMAs) with trend tracking. The EMA provides smooth measurements despite noise, while the trend component enables stability detection - when the trend is less than a small proportion of the value, the measurement is considered stable.

Critically, scaling decisions are only made when measurements are stable. This prevents oscillation and ensures that the system responds to genuine performance changes rather than transient spikes. The measurement window is configurable, allowing operators to trade off between responsiveness and stability.

## Algorithm Design

The algorithm maintains two primary components: **real-time performance tracking** and **adaptive performance curve modeling**.

### Performance Curve Modeling

The core insight is modeling the performance landscape as a collection of **piecewise-linear segments**, each characterized by:

- **Throughput slope** (ops/sec per goroutine)
- **Utilization category** (Low: <40%, Nominal: 40-60%, High: ≥60%)  
- **Goroutine count range** where the linear relationship holds
- **Temporal validity** to handle workload changes

Each segment represents a regime where performance scales linearly with goroutine count. Segments with different slopes indicate different scaling characteristics - high slopes suggest efficient scaling, while low or negative slopes indicate diminishing returns or contention.

### Real-time Tracking and Stability

The algorithm continuously monitors throughput and secondary utilization using exponential moving averages (EMAs) with trend tracking. Scaling decisions are made only when measurements are stable (trend < 10% of value) to prevent oscillation from noise.

When stable measurements are available, the algorithm:

1. **Updates the size controller** with the latest sample, either extending existing segments or creating new ones based on consistency with linear models
2. **Analyzes the performance samples** for knee detection and exploration opportunities  
3. **Makes scaling recommendations** based on current utilization and curve topology

### Knee Detection and Exploration Strategy

The algorithm employs a **conservative knee detection** approach that requires strong evidence before declaring convergence:

- **Multiple segments required**: A knee can only exist where two segments with different slopes meet
- **Slope differential threshold**: Significant decrease in throughput growth rate between segments
- **Utilization validation**: The knee should exhibit moderate utilization, not high utilization (indicating under-provisioning) or low utilization (indicating over-provisioning)
- **Exploration beyond knee**: Even when a knee is detected, continue exploration if current utilization remains high

When no confident knee is found, the algorithm employs **utilization-driven exploration**:

- **High utilization (≥60%)**: Explore higher goroutine counts to find capacity limits
- **Low utilization (<40%)**: Explore lower counts to find efficiency improvements  
- **Nominal utilization (40-60%)**: Continue systematic exploration to map the performance curve

### Temporal Adaptation

Size controllers have configurable retention periods to handle workload changes. Segments not refreshed within the retention window are discarded, allowing the algorithm to adapt to new conditions without being anchored to obsolete data.

## Implementation Lessons

### Premature Knee Detection

Initial implementations suffered from **premature convergence** where the algorithm would detect a "knee" based on limited data (e.g., testing only 1-2 goroutine counts) and stop exploration. This led to significant performance losses where the algorithm would settle on 2 goroutines achieving ~2000 ops/sec when 4+ goroutines could achieve ~4000 ops/sec.

**Root cause**: Using throughput thresholds (e.g., "95% of maximum observed") without requiring evidence of diminishing returns. A single linear segment can appear to have a "knee" at any point when insufficient exploration has occurred.

**Solution**: Require multiple segments with demonstrably different slopes before declaring any knee. High utilization should override knee detection and trigger continued exploration.

### Data Pipeline Precision Issues

The algorithm initially appeared to receive zero throughput values due to **units and precision mismatches** in the data pipeline. EMAs tracked throughput in operations-per-nanosecond (very small numbers like 0.000001946), but debug output formatted these as 0.0, creating the false impression of missing data.

**Root cause**: Debug formatting with insufficient precision and misaligned expectations about numerical scales.

**Solution**: Careful attention to units throughout the pipeline and debug output that displays values in human-readable formats (ops/sec) while maintaining precision in internal calculations.

### Utilization vs. Throughput Priority

Early exploration strategies prioritized utilization signals over throughput optimization, leading to **local optimization** where the algorithm would stop exploring once utilization appeared "reasonable" even when much higher throughput was achievable.

**Root cause**: Over-reliance on utilization thresholds without sufficient exploration to map the full performance landscape.

**Solution**: Treat high utilization as a strong signal to continue exploration regardless of other indicators. Utilization provides direction (scale up/down) but should not terminate exploration prematurely.

## Rejected Alternatives

Throughout the design process, numerous alternative approaches were considered and ultimately rejected.

Classical queueing theory, including M/M/c models, initially seemed applicable to the capacity planning problem. However, PSG's backpressure mechanism fundamentally violates the independence assumption of queueing theory. When the system is at capacity, backpressure artificially limits the arrival rate, making it impossible to measure true demand independently from service capacity. This coupling renders traditional queueing formulas inapplicable.

Simple threshold-based control was attractive for its simplicity - spawn goroutines when utilization exceeds 70%, remove them below 30%. However, this approach fails to distinguish between high utilization due to genuine under-provisioning versus high utilization due to contention. In the latter case, adding more goroutines worsens performance, leading to a positive feedback loop of degradation.

Continuous gradient tracking, where the system constantly measures the derivative of throughput with respect to goroutine count, was rejected due to the high cost of continuous exploration. This approach requires constantly perturbing the system to measure gradients, spending significant time in sub-optimal configurations. Moreover, the gradient is noisy and doesn't directly identify the plateau point.

Maintaining long-term performance history with sophisticated statistical analysis was considered but rejected due to complexity and overhead. This approach would require storing extensive historical data, calculating medians and percentiles, and potentially tracking higher-order patterns. The memory overhead and computational complexity were not justified by marginal improvements in decision quality, especially given that workload patterns may change unpredictably over time, making historical data less relevant.

PID controllers and other control theory approaches were evaluated but found unsuitable. The fundamental issue is that utilization alone doesn't indicate the correct action - high utilization might require more goroutines or might indicate contention where fewer goroutines would help. Additionally, the integral term in PID control can cause significant overshoot in this context.

Machine learning approaches were briefly considered but quickly rejected as overkill for this problem. They would require extensive training data, exhibit black-box behavior that's difficult to debug, and add unnecessary complexity to a system where explainability is crucial.

Fixed scaling multiples (always double when scaling up, halve when scaling down) were rejected for being too aggressive near the optimum and too rigid to adapt to different system scales. This approach can overshoot badly and doesn't account for the non-linear nature of the performance curve.

Many of the above were also rejected as being inappropriate for the general-purpose and lightweight nature of the PSG library. Users should rarely if ever need to tune or train PSG operational parameters to use it effectively.

## Implementation Considerations

Measurement quality is paramount to the algorithm's success. The implementation must ensure decisions are only made on stable measurements, using EMAs to filter noise effectively. Multiple samples should be required before declaring a plateau detected, preventing premature convergence on sub-optimal configurations.

Change management requires careful attention to prevent oscillation and minimize disruption. The system enforces minimum time between configuration changes and considers hysteresis to prevent rapid switching between configurations. The cost of disruption is implicitly accounted for by requiring significant utilization pressure before initiating exploration.

Edge cases must be handled gracefully. The system must respond appropriately to dynamic limit changes, ensure at least one goroutine remains active at all times, and reset exploration state when configuration parameters change. The algorithm must also handle the case where the concurrency limit prevents reaching the optimal configuration.

Observability is crucial for production systems. The implementation should log exploration sessions and their findings, track time spent at each configuration, and monitor the accuracy of plateau detection. This data enables operators to understand system behavior and tune parameters if needed.

## Benefits and Trade-offs

This design provides fast convergence to optimal configurations through focused exploration. The minimal memory footprint and simple logic make it suitable for production systems. The algorithm automatically adapts to workload changes while maintaining stability through measurement requirements. Most importantly, the clear relationship between inputs and decisions makes the system behavior explainable and debuggable.

The primary trade-off is the time spent in exploration, though this is minimized through the focused session approach. The algorithm also requires several measurement windows to detect stability, which can delay initial scaling response. However, these trade-offs are acceptable given the benefits of turn-key, stable, and predictable performance.

## Empirical Validation

### Time-based Flushing Trade-offs

PSG supports both count-based flushing (flush when batch reaches a configurable size) and time-based flushing (flush after a configurable maximum hold time). Benchmark results demonstrate the fundamental trade-off between latency bounds and throughput efficiency:

**Short flush periods** (e.g., 10µs, 1ms) cause time-based limits to trigger before count-based limits, resulting in smaller batches and higher per-operation overhead. This creates apparent "performance regressions" that are actually the intended behavior - the system is prioritizing latency bounds over peak throughput.

**Long flush periods** (e.g., 100ms) allow count-based limits to trigger first, enabling larger batches and higher throughput efficiency while still providing reasonable latency bounds.

This behavior validates the design principle that latency and throughput are competing objectives that must be balanced based on application requirements.

### Unlimited Pool Effectiveness

Benchmark evidence demonstrates that unlimited combiner pools consistently outperform manually-configured fixed limits when the system has sufficient resources to optimize effectively. For example, in processing workloads with moderate to long flush periods, unlimited pools show 20-40% latency improvements compared to the variable performance of fixed-limit configurations.

This effectiveness stems from the automatic discovery of optimal concurrency levels that vary significantly across different workload characteristics. Manual prediction of these optimal values proves difficult due to non-obvious relationships between task duration, flush periods, available CPU resources, and gather capacity.

The unlimited pool approach provides particular value for gather-limited workloads where the bottleneck lies in result processing rather than task execution. In these scenarios, fixed limits can create artificial capacity constraints while unlimited pools naturally discover the optimal balance between combiner concurrency and gather throughput.

### Algorithm Design Validation

The benchmark results confirm several key aspects of the perfCurves algorithm design:

**Auto-discovery effectiveness**: Unlimited pools find better configurations than manual tuning across diverse workloads, validating the algorithm's exploration and convergence strategies.

**Workload adaptation**: Different combinations of task duration, flush periods, and workload types require substantially different optimal concurrency levels, confirming the need for dynamic adaptation rather than static configuration.

**Conservative exploration benefits**: The algorithm's measured approach to scaling prevents the performance degradation visible in some fixed-limit configurations that exceed optimal concurrency levels.

## Conclusion

This design provides a practical solution to the combiner pool scaling problem through focused exploration of the performance landscape rather than relying on theoretical models or manual configuration. The algorithm's ability to automatically discover optimal concurrency levels eliminates the guesswork inherent in capacity planning for diverse and variable workloads.

The empirical validation demonstrates that unlimited pools deliver measurable performance benefits over manual tuning while providing the flexibility to adapt to changing conditions. The trade-offs between different flushing strategies are well-characterized and align with the intended design objectives.

The key insight is recognizing that the optimization problem is not continuous throughput maximization but rather finding and maintaining the minimum resource configuration that achieves application-appropriate performance characteristics. This approach proves both more effective and more maintainable than attempting to predict optimal configurations through manual analysis.
