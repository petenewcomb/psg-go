# PSG Programming Model

This document describes the core programming model of PSG (Parallel Scatter-Gather), focusing on the user-facing concepts, design philosophy, and fundamental patterns that enable reliable concurrent workflows.

## Problem Statement: The Challenge of Concurrent Workflows

Modern applications require complex workflows that leverage concurrency and parallelism for performance, but concurrent programming introduces numerous hazards that make reliable implementation extremely difficult. PSG addresses these fundamental challenges:

### User Requirements: Complex Concurrent Workflows

**Performance Demands**:
- High-throughput processing of large datasets
- Low-latency response to dynamic workloads  
- Efficient resource utilization across multiple cores
- Scalability from single-machine to distributed systems

**Workflow Complexity**:
- Multi-stage processing pipelines with dependencies
- Fan-out/fan-in patterns for parallel processing
- Incremental result aggregation and streaming
- Dynamic workload adaptation and load balancing

**Operational Requirements**:
- Predictable behavior under varying loads
- Graceful degradation and error recovery
- Observable system behavior for debugging and optimization
- Configurable performance characteristics

### The Perils of Concurrent Programming

PSG specifically addresses the abundant hazards that make concurrent programming treacherous:

#### Race Conditions and Data Races
**The Problem**: Unsynchronized access to shared data leads to:
- Corrupt state and inconsistent results
- Intermittent failures that are difficult to reproduce
- Subtle bugs that manifest only under specific timing conditions
- Performance degradation from excessive synchronization

**PSG's Solution**: 
- Sequential gather processing eliminates most shared state
- Clear ownership models for mutable state
- Structured concurrency boundaries
- Deterministic execution order within sequential phases

#### Deadlocks and Livelocks
**The Problem**: Circular dependencies and resource contention cause:
- Complete system freezes requiring restarts
- Subtle dependency cycles that emerge under load
- Complex lock ordering requirements
- Difficulty reasoning about system-wide dependencies

**PSG's Solution**:
- Fundamental architectural constraint: tasks cannot scatter (eliminates primary deadlock source)
- Clear dependency ordering in system design
- Structured lifecycle management

#### Liveness and Starvation Issues
**The Problem**: Poor scheduling and resource allocation leads to:
- Some tasks never receiving CPU time
- System responsiveness degradation under load
- Unfair resource distribution
- Cascade failures from resource exhaustion

**PSG's Solution**:
- Adaptive resource management
- Built-in flow control mechanisms
- Fair scheduling and resource distribution

#### Performance Fragility and Operational Complexity
**The Problem**: Concurrent systems often exhibit:
- Performance cliffs and unpredictable behavior
- Complex configuration requiring deep expertise
- Difficult debugging and observability
- Fragile error handling and recovery

**PSG's Solution**:
- Predictable performance characteristics
- Self-tuning adaptive mechanisms
- Built-in observability hooks
- Structured error handling and cancellation

### PSG's Design Philosophy: Structured Concurrency

PSG embodies the principle of **structured concurrency** - the idea that concurrent programs should have clear, hierarchical structure similar to structured programming's approach to control flow. Just as structured programming eliminated goto statements and spaghetti code, structured concurrency eliminates the chaos of ad-hoc thread management and coordination.

**Core Principles**:

1. **Clear Boundaries**: Parallel and sequential execution phases are explicitly delineated
2. **Hierarchical Composition**: Complex workflows compose from simpler scatter-gather-combine primitives  
3. **Deterministic Coordination**: Within sequential phases, execution order is predictable and reproducible
4. **Resource Accountability**: All concurrent work is tracked and properly cleaned up
5. **Failure Isolation**: Errors are contained and propagated through well-defined boundaries

**Benefits for Users**:
- **Reasoning**: Developers can understand and predict system behavior
- **Testing**: Deterministic behavior enables reliable automated testing
- **Debugging**: Clear structure makes issues easier to isolate and reproduce
- **Maintenance**: System behavior remains predictable as code evolves
- **Performance**: Structured approach enables systematic optimization

This structured approach transforms concurrent programming from a specialist skill requiring deep expertise into a more accessible programming model that delivers both performance and reliability.

## Core Programming Model

### Fundamental Concepts

PSG implements a **scatter-gather-combine** programming model that enables efficient parallel computation through three primary operations:

1. **Scattering**: Distributing work items across available goroutines
2. **Gathering**: Sequentially processing completed task results  
3. **Combining**: Incrementally accumulating related results before emission

The model addresses the fundamental tension between parallelism (for performance) and coordination (for correctness) by providing structured concurrency primitives that maintain both efficiency and predictable behavior.

### Core Abstractions

**Job**: The root execution environment that provides:
- Lifecycle management (Open → Closed → Flushing → Done)
- Work coordination and proper shutdown
- Context propagation with job-specific values
- Centralized result collection and processing

**Task**: A unit of work defined as `func(context.Context) (T, error)` that:
- Executes in parallel with other tasks
- Returns results that flow to gather or combine operations
- Inherits job context and configuration
- Cannot scatter new work (fundamental deadlock prevention)

**Gather Operation**: Sequential processing of task results that:
- Maintains result ordering when required
- Enables incremental computation patterns
- Supports controlled reentrancy (scattering new work during processing)
- Provides structured error handling

**Combine Operation**: Incremental accumulation of related inputs that:
- Groups inputs by key or other criteria
- Applies user-defined combining logic
- Manages time-based and count-based flushing
- Emits aggregated results to downstream consumers

### Programming Model Benefits

1. **Structured Concurrency**: Clear boundaries between parallel and sequential execution phases
2. **Composability**: Operations can be nested and chained naturally
3. **Automatic Flow Control**: Built-in backpressure prevents resource exhaustion
4. **Error Handling**: Structured error propagation through the computation graph
5. **Testability**: Deterministic behavior despite internal concurrency

## Programming Patterns

### Basic Scatter-Gather Pattern

The fundamental pattern distributes work across many tasks and gathers results sequentially:

```go
// Conceptual example
job := psg.NewJob(ctx)
gatherOp := psg.NewGatherOp(job, gatherFunc)

// Scatter phase: distribute work
for _, workItem := range workItems {
    gatherOp.Scatter(ctx, taskFunc(workItem))
}

// Gather phase: process results
job.Close()  // No more scatters
job.Wait()   // Wait for completion
```

**Characteristics**:
- Tasks execute in parallel
- Results are processed sequentially in completion order
- Clear separation between parallel and sequential phases

### Incremental Combine Pattern

For aggregating related results before emission:

```go
// Conceptual example
combinerPool := psg.NewCombinerPool(job, combinerFactory)
combineOp := psg.NewCombineOp(combinerPool, keyFunc)

// Scatter phase: distribute work that produces key-value pairs
for _, workItem := range workItems {
    combineOp.Scatter(ctx, taskFunc(workItem))
}

// Combine phase: automatic aggregation by key
// Results are incrementally combined and emitted when ready
```

**Characteristics**:
- Results are grouped by key or other criteria
- Combining happens incrementally as results arrive
- Automatic flushing based on time or completion
- Efficient for streaming aggregation patterns

### Nested Workflows

Gather and combine functions can scatter new work, enabling complex workflows:

```go
// Conceptual example
gatherFunc := func(ctx context.Context, result T, err error) error {
    // Process the result
    processedData := process(result)
    
    // Scatter follow-up work based on the result
    if needsFollowUp(processedData) {
        anotherGatherOp.Scatter(ctx, followUpTask(processedData))
    }
    
    return nil
}
```

**Characteristics**:
- Dynamic workflow generation based on intermediate results
- Controlled reentrancy through work queueing
- Maintains structured concurrency guarantees

## Key Design Constraints

### Task Isolation: No Scattering from Tasks

**Fundamental Rule**: Tasks can only emit results - they cannot scatter new work.

**Why This Matters**:
- Prevents deadlocks from circular task dependencies
- Simplifies resource counting and cleanup
- Enables deterministic shutdown coordination
- Makes system behavior predictable and testable

**Where Scattering IS Allowed**:
- Gather functions (sequential processing of results)
- Combine functions (incremental accumulation)
- Flush functions (periodic emission of combined results)
- Top-level application code

### Sequential Processing Guarantee

**Within Gather Operations**: Results are processed one at a time in a single goroutine.

**Benefits**:
- Eliminates race conditions on shared state
- Enables simple, thread-safe gather function implementation
- Provides deterministic execution order
- Simplifies debugging and testing

**Performance Consideration**: Gather functions should be efficient since they're sequential bottlenecks.

### Resource Accountability

**All Work is Tracked**: Every scattered task is counted and must complete before job shutdown.

**Guarantees**:
- No goroutine leaks
- Proper resource cleanup
- Coordinated shutdown across all components
- Clear completion semantics

## Error Handling and Cancellation

### Structured Error Propagation

**Task Errors**: Propagated to gather functions with the task result
**Gather Errors**: Can be accumulated and handled at job level
**Context Cancellation**: Propagates through all scattered work
**Panic Recovery**: Tasks panics are recovered and converted to errors

### Cancellation Semantics

**Context-Based**: Standard Go context cancellation throughout
**Graceful Shutdown**: In-flight work completes, new work is rejected
**Resource Cleanup**: Proper cleanup guaranteed even during cancellation

## Configuration and Tuning

### High-Level Configuration

PSG provides intuitive configuration options that affect system behavior:

**Concurrency Limits**: Control parallel execution bounds
**Timeout Settings**: Configure combiner hold times and flush intervals
**Pool Sizing**: Adjust resource allocation for different workload patterns

### Self-Tuning Behavior

Many aspects of PSG adapt automatically:
- Goroutine pool sizing based on actual workload
- Backpressure thresholds based on system performance
- Queue sizing based on observed usage patterns

## Observability and Debugging

### Built-in Instrumentation Hooks

PSG provides structured events for monitoring:
- Task lifecycle events (started, completed, failed)
- Queue depth and throughput metrics
- Resource utilization statistics
- Performance bottleneck identification

### Deterministic Behavior

**Within Sequential Phases**: Execution order is predictable
**Testing Support**: Deterministic behavior enables reliable automated testing
**Debugging Support**: Clear structure makes issues easier to isolate

## Conclusion

The PSG programming model provides a structured approach to concurrent workflows that combines the performance benefits of parallelism with the safety and predictability needed for production systems. By embracing structured concurrency principles and providing clear abstractions for scatter-gather-combine patterns, PSG enables developers to build complex concurrent applications without the typical hazards of concurrent programming.

The model's strength lies in its ability to hide the complexity of coordination, backpressure, and resource management while exposing simple, composable primitives that naturally express parallel computation patterns.

## Comparison with Existing Approaches

### Structured Concurrency in the Industry

PSG's approach aligns with the broader **structured concurrency** movement in programming languages and frameworks, while providing Go-specific innovations:

#### Formal Structured Concurrency

**Industry Definition**: Structured concurrency ensures that concurrent operations have clear, hierarchical structure where:
- All spawned tasks complete before their parent scope exits
- Cancellation propagates automatically through the hierarchy
- Resource cleanup happens deterministically
- Error handling follows structured patterns

**Examples in Other Languages**:
- **Nurseries** in Python's Trio library
- **Structured Concurrency** in Java's Project Loom  
- **Async/await** with structured scoping in Swift and Kotlin
- **Green threads** with supervision trees in Erlang/OTP
- **Structured concurrency** in modern C++ with co-routines

**PSG's Implementation**:
- **Jobs** provide the structured scope (nursery equivalent)
- **Work reference counting** ensures all tasks complete before job completion
- **Context propagation** handles cancellation and cleanup automatically
- **Sequential gather processing** provides deterministic coordination points

#### Key Differentiators from Industry Approaches

**Dynamic Task Spawning**: Unlike traditional structured concurrency which prohibits task spawning from within tasks, PSG allows controlled reentrancy through gather/combine functions while maintaining structured guarantees.

**Multi-Level Resource Management**: PSG extends structured concurrency with TaskPools and CombinerPools that provide independent resource boundaries within the overall structured scope.

**Incremental Processing**: Traditional structured concurrency waits for all tasks to complete before proceeding. PSG processes results incrementally while maintaining structured guarantees.

### Comparison with Go Ecosystem

#### creachadair/taskgroup Package

**Project**: github.com/creachadair/taskgroup

This is the closest existing library to PSG in the Go ecosystem, providing structured concurrency with advanced features.

**Similarities**:
- Structured task group management with automatic synchronization
- Error collection and propagation from multiple concurrent tasks
- Concurrency limiting capabilities
- Support for result gathering from background tasks
- Context-aware design for cancellation

**Key Differences from PSG**:
- **Result Processing**: taskgroup collects all results before processing; PSG processes incrementally
- **Task Spawning**: taskgroup doesn't support dynamic task spawning from within tasks
- **Resource Pools**: PSG provides multiple independent TaskPools with different limits
- **Combiner Pattern**: PSG supports stateful aggregation with automatic flushing
- **Work Queueing**: PSG enables controlled reentrancy through work queueing

**Example Comparison**:
```go
// taskgroup approach
g := taskgroup.New(nil).Limit(10) // Single global limit
tasks := []taskgroup.Task{
    func() error { return doWork(1) },
    func() error { return doWork(2) },
    func() error { return doWork(3) },
}
err := g.Go(tasks...).Wait() // Batch execution, wait for all

// PSG approach  
job := psg.NewJob(ctx)
pool := psg.NewTaskPool(job).WithLimit(10) // Pool-specific limit
gather := psg.NewGather(func(ctx context.Context, result int, err error) error {
    if err == nil {
        processResult(result) // Incremental processing
        // Can spawn new tasks based on result
        if needsFollowUp(result) {
            gather.Scatter(ctx, pool, followUpTask(result))
        }
    }
    return err
})

for i := 1; i <= 3; i++ {
    gather.Scatter(ctx, pool, func(ctx context.Context) (int, error) {
        return doWork(i)
    })
}
```

**taskgroup's Strengths**:
- Simpler API for basic concurrent task execution
- Excellent error handling and filtering capabilities
- Mature, well-tested library
- Minimal overhead for straightforward use cases

**PSG's Advantages Over taskgroup**:
- Incremental result processing (streaming vs. batch)
- Dynamic workflow generation through reentrancy
- Multiple resource pools with independent limits
- Stateful aggregation through combiners
- Type-safe generic interfaces

#### Go's errgroup Package

**errgroup Approach**:
```go
g, ctx := errgroup.WithContext(ctx)
results := make([]string, 3) // Pre-allocated to avoid races
for i := range 3 {
    g.Go(func() error {
        res, err := doWork(ctx, i)
        if err != nil {
            return err
        }
        results[i] = res // Requires careful indexing
        return nil
    })
}
err := g.Wait()
```

**PSG Approach**:
```go
job := psg.NewJob(ctx)
defer job.CancelAndWait()

var results []string // Safe dynamic slice
gather := psg.NewGather(func(ctx context.Context, result string, err error) error {
    if err == nil {
        results = append(results, result) // Sequential processing
    }
    return err
})

for i := range 3 {
    gather.Scatter(ctx, job, func(ctx context.Context) (string, error) {
        return doWork(ctx, i)
    })
}

err := job.CloseAndGatherAll(ctx)
```

**Key Differences**:
- **Result Handling**: PSG eliminates data races through sequential gather processing
- **Dynamic Operations**: PSG supports task spawning from gather functions
- **Type Safety**: PSG uses generics for compile-time type safety
- **Resource Management**: PSG provides multiple concurrency pools

#### Native Go Concurrency Patterns

**Traditional Go Pattern**:
```go
// Manual worker pool implementation
jobs := make(chan Work, 100)
results := make(chan Result, 100)

// Start workers
for i := 0; i < numWorkers; i++ {
    go worker(jobs, results)
}

// Send work
go func() {
    for _, work := range workItems {
        jobs <- work
    }
    close(jobs)
}()

// Collect results
var collected []Result
for i := 0; i < len(workItems); i++ {
    collected = append(collected, <-results)
}
```

**PSG's Advantages**:
- **Simplified API**: No manual channel management
- **Automatic Resource Management**: TaskPools handle worker lifecycle
- **Error Handling**: Built-in error propagation and aggregation
- **Cancellation**: Automatic context-based cancellation
- **Type Safety**: Generic interfaces eliminate type assertions

#### Reactive Extensions Family

**Examples**: RxJS (JavaScript), RxJava (Java), RxSwift (Swift), RxGo (Go), ReactiveX ecosystem

The Reactive Extensions family provides powerful abstractions for handling asynchronous data streams with operators for transformation, composition, and error handling.

**Similarities to PSG**:
- Support for scatter-gather patterns through operators like `forkJoin` and `combineLatest`
- Handle asynchronous data streams and error propagation
- Pipeline composition capabilities and incremental processing
- Built-in backpressure management
- Functional composition of complex workflows

**Example Patterns**:
```javascript
// RxJS scatter-gather
forkJoin({
  task1: service1$,
  task2: service2$,
  task3: service3$
}).subscribe({
  next: (results) => console.log(results),
  error: (err) => console.error(err)
});

// RxJS streaming aggregation
source$.pipe(
  mergeMap(item => processItem(item)),
  scan((acc, result) => combineResults(acc, result)),
  debounceTime(100)
).subscribe(aggregatedResult => emit(aggregatedResult));
```

**Key Differences from PSG**:
- **Programming Paradigm**: Stream-based reactive paradigm vs. imperative task-based execution
- **Operator Semantics**: Fixed operator library vs. flexible user-defined gather/combine functions
- **Learning Curve**: Requires understanding reactive concepts vs. familiar imperative patterns
- **Concurrency Control**: Less direct control over resource limits and goroutine management
- **Type System**: Varies by language implementation; PSG leverages Go's type system specifically
- **Error Handling**: Stream-based error propagation vs. structured error collection

**PSG's Advantages Over Reactive Approaches**:
- **Familiar Patterns**: Uses imperative programming that's more familiar to most developers
- **Direct Resource Control**: Explicit concurrency limits and resource pool management
- **Simpler Mental Model**: Clear separation between parallel and sequential phases
- **Language Integration**: Designed specifically for Go's concurrency model and idioms

**Reactive Extensions' Advantages**:
- **Mature Ecosystem**: Well-established with extensive operator libraries
- **Cross-Language**: Consistent patterns across multiple programming languages
- **Sophisticated Operators**: Rich set of pre-built operators for complex stream processing
- **Time-Based Operations**: Excellent support for time-windowing and temporal operations

#### Pipeline and Dataflow Libraries

**Examples**: TPL Dataflow (.NET), Java's CompletableFuture, Python's asyncio, Akka Streams (Scala), Node.js Streams

These libraries focus on data pipeline construction with stage-based processing and built-in backpressure management.

**Similarities to PSG**:
- **Data Pipeline Construction**: Stage-based processing with data flow between components
- **Concurrent Execution**: Parallel processing across pipeline stages
- **Backpressure Management**: Built-in flow control to prevent overwhelming downstream stages
- **Composition**: Ability to compose complex pipelines from simpler components

**Example (TPL Dataflow)**:
```csharp
var processBlock = new TransformBlock<Input, Output>(
    input => ProcessData(input),
    new ExecutionDataflowBlockOptions { MaxDegreeOfParallelism = 4 });

var batchBlock = new BatchBlock<Output>(10);
processBlock.LinkTo(batchBlock);

var aggregateBlock = new ActionBlock<Output[]>(
    batch => AggregateBatch(batch));
batchBlock.LinkTo(aggregateBlock);
```

**Key Differences from PSG**:
- **Pipeline Structure**: Fixed pipeline topology vs. dynamic task spawning
- **Flexibility**: PSG allows recursive task creation and heterogeneous task management
- **Programming Model**: Block-based architecture vs. function-based scatter-gather
- **Error Handling**: PSG provides more flexible error handling and result aggregation
- **Resource Management**: PSG supports multiple independent resource pools

#### Async/Await and Future-Based Models

**Examples**: JavaScript Promises, Python asyncio, C# async/await, Rust's async/await, Scala Futures

These models provide asynchronous programming abstractions with composition capabilities.

**Similarities to PSG**:
- **Asynchronous Execution**: Non-blocking task execution with result handling
- **Composition**: Ability to compose complex asynchronous workflows
- **Error Propagation**: Structured error handling through the async chain
- **Cancellation**: Support for operation cancellation

**Key Differences from PSG**:
- **Programming Model**: Promise/Future chaining vs. scatter-gather coordination
- **Concurrency Control**: Limited direct control over resource allocation
- **Result Processing**: Typically batch-oriented vs. incremental processing
- **Reentrancy**: Less structured support for dynamic workflow generation

### Unique Features of PSG

#### Pipelined Processing
Unlike traditional scatter-gather that waits for all tasks to complete, PSG processes results incrementally as they arrive, enabling streaming aggregation patterns.

#### Controlled Reentrancy
PSG allows gather and combine functions to spawn new tasks within the same job, enabling recursive processing while maintaining structured guarantees through work queueing.

#### Multi-Resource Management
Different task types can use independent concurrency pools (TaskPools) with separate limits, enabling fine-grained resource control within a structured scope.

#### Combiner Pattern
Stateful aggregation with automatic flushing enables efficient batch processing and windowing operations that traditional structured concurrency doesn't directly support.

#### Zero Dependencies
Pure Go implementation with no external dependencies, making it lightweight and easy to adopt in any Go project.

### Use Case Differentiation

**PSG is Ideal For**:
- In-process concurrent task management with complex dependencies
- Applications requiring incremental result processing
- Scenarios with mixed resource constraints (I/O vs. compute)
- Recursive operations like web crawling or tree traversal
- Type-safe concurrent programming in Go
- Streaming aggregation and data processing pipelines

**PSG is NOT For**:
- Distributed task execution across machines
- Persistent workflow management with retry capabilities
- Visual workflow construction and monitoring
- Cross-system message-based integration

### Comparison with Distributed Systems

While these systems operate at a different architectural level than PSG, they share conceptual similarities and provided inspiration for PSG's design. PSG focuses solely on in-process concurrency, but its patterns support the requirements of distributed systems: workflow isolation, backpressure propagation, and careful stewardship of highly concurrent, failure-prone, and latency-sensitive operations.

#### DAG Workflow Engines

**Examples**: Apache Airflow, Argo Workflows, Dagster, Prefect, Temporal

**Key Similarities**:
- Task dependency management and parallel execution
- Error handling and retry logic
- Structured workflow composition

**Key Differences**:
- **Scope**: Cross-process/cross-machine orchestration vs. in-process concurrency
- **Persistence**: Persistent state and workflow visualization vs. ephemeral execution
- **Infrastructure**: Heavy platform requirements vs. lightweight library
- **Focus**: Long-running, scheduled workflows vs. real-time processing
- **Deployment**: Complex deployment requirements vs. simple library integration

#### Actor Frameworks

**Examples**: Akka (Scala), Erlang/OTP, Orleans

**Key Similarities**:
- Message-based task distribution
- Error supervision and recovery patterns
- Concurrent execution model with isolation

**Key Differences**:
- **Programming Model**: Actor model with message passing vs. direct function execution
- **Distribution**: Distributed by design vs. local concurrency focus
- **Complexity**: Complex deployment and operational requirements vs. simple library
- **Learning Curve**: Requires understanding actor model vs. familiar imperative patterns

#### Message Queue Systems

**Examples**: RabbitMQ, Apache Kafka, AWS SQS

**Key Similarities**:
- Scatter-gather messaging patterns
- Work distribution across consumers
- Backpressure and flow control mechanisms

**Key Differences**:
- **Communication**: Network-based vs. in-memory communication
- **Persistence**: Durability and persistence concerns vs. ephemeral processing
- **Overhead**: Network serialization overhead vs. direct function calls
- **Operational Complexity**: Separate infrastructure vs. embedded library

#### Enterprise Integration Patterns

**Examples**: Apache Camel, Spring Integration, MuleSoft

**Key Similarities**:
- Scatter-gather pattern implementation
- Message routing and aggregation
- Pipeline composition capabilities

**Key Differences**:
- **Architecture**: Enterprise service bus vs. library-based approach
- **Integration**: Cross-system integration focus vs. in-process workflows
- **Configuration**: Configuration-heavy XML/YAML vs. code-first API
- **Type Safety**: Runtime configuration vs. compile-time type safety

### Additional Go Libraries

#### conc Package

**Project**: github.com/sourcegraph/conc

**Similarities**:
- Safer concurrency abstractions for Go
- Error handling improvements over raw goroutines
- Context propagation and cancellation support

**Differences from PSG**:
- **Focus**: General safety improvements vs. specific scatter-gather patterns
- **Features**: No built-in result gathering or aggregation capabilities
- **Scope**: Limited support for dynamic task spawning and complex workflows
- **Patterns**: Focuses on making existing patterns safer vs. introducing new patterns

## Conclusion

The PSG programming model provides a structured approach to concurrent workflows that combines the performance benefits of parallelism with the safety and predictability needed for production systems. By embracing structured concurrency principles and providing clear abstractions for scatter-gather-combine patterns, PSG enables developers to build complex concurrent applications without the typical hazards of concurrent programming.

PSG's unique position in the Go ecosystem comes from its combination of structured concurrency guarantees, incremental processing capabilities, and Go-native design. It fills the gap between simple concurrency utilities like errgroup and complex reactive frameworks, providing a practical solution for sophisticated in-process concurrent workflows.

The model's strength lies in its ability to hide the complexity of coordination, backpressure, and resource management while exposing simple, composable primitives that naturally express parallel computation patterns.

---

*For implementation details on backpressure mechanisms, work queueing, and reentrancy management, see [backpressure-and-reentrancy.md](backpressure-and-reentrancy.md).*

*For specific performance optimizations, see the focused design documents in this directory.*