// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package psg provides an API for launching (scattering) tasks and aggregating
// (skimming) their results. It separates these stages so that the tasks can
// run concurrently while aggregation remains sequential. This reduces the
// wall-clock time required for an overall operation (job) as compared to
// executing the tasks serially, without adding synchronization complexity to
// the aggregation logic.
//
// Since tasks require resources to execute, and those resources are limited,
// psg also provides a way to model pools of resources as limits on the number
// of tasks allowed to run at the same time. Different classes of task, for
// instance compute-bound or I/O-bound, can be executed in the context of
// different pools and therefore subject to different concurrency constraints.
//
// Non-trivial tasks often involve different stages that use different kinds of
// resources, for instance I/O to retrieve a chunk of data followed by compute
// to process it. The psg package therefore allows skim functions to launch
// new tasks into the same or different pools all within the context of the same
// job, creating incremental pipelines that are free of unnecessary
// synchronization barriers between stages and that neither over- nor
// under-utilize multiple distinct groups of resources.
//
// # Context Usage
//
// The psg library relies heavily on Go's context.Context for cancellation,
// deadline propagation, and tracking relationships between components. Proper
// context handling is essential for correct operation:
//
// # Context Propagation in User Code
//
//  1. Task Functions: The context passed to Task should be respected for
//     cancellation. User code should propagate this context to any operations
//     performed within the task, rather than creating new, disconnected
//     contexts. While the job will still function if a task ignores
//     cancellation, such tasks may continue running until they complete
//     naturally, even after the job has been canceled.
//
//  2. Skim Functions: The context passed to Skim is the context from
//     the calling Start or Skim* method, not the job's context. User code
//     should propagate this context, especially when calling Start to create
//     new tasks.
//
//  3. Combiner Functions: Both Combine and Flush methods receive contexts that
//     should be respected for cancellation and passed to the emit function.
//
// # Cancellation Behavior
//
// When a job is canceled via Pool.Cancel or its context is canceled:
//
//  1. All running Tasks receive context cancellation but will run until
//     they return. The library will correctly clean up once they complete.
//
//  2. Combiners will be flushed to ensure no data is lost.
//
//  3. Pool.CancelAndWait guarantees that all task goroutines exit before
//     returning.
//
// # Context Safety Features
//
// The library implements safeguards to prevent common errors:
//
//  1. Reentrancy Protection: Context values are used to detect and prevent
//     dangerous calling patterns. For example, calling Start from within a
//     Task of the same job will panic with a helpful error message.
//
//  2. Skim Queuing: Skim operations are queued rather than processed
//     recursively to prevent stack overflow when skim functions launch new
//     tasks.
//
// # Potential Issues with Improper Context Handling
//
// If user code fails to properly propagate contexts:
//
//  1. Cancellation signals may not reach all operations, potentially causing
//     delayed cleanup.
//
//  2. Reentrancy detection might fail, potentially allowing calls that could
//     lead to deadlocks.
//
//  3. Backpressure mechanisms may not function correctly, potentially leading
//     to resource exhaustion.
//
//  4. Skim operations might process in incorrect order or recursively rather
//     than sequentially.
//
// # Best Practices
//
//  1. Always use the provided context in Task, Skim, and Combiner
//     methods.
//
//  2. Follow the pattern of checking ctx.Done() regularly in long-running tasks.
//
//  3. Don't create new root contexts (e.g., context.Background()) within
//     functions that receive a context from the library.
//
//  4. Launch new tasks from Skim or Combiner methods, not from Task.
//
//  5. For tasks that need to create internal concurrency, consider creating a
//     sub-job within the task rather than calling Start directly.
package psg

//go:generate go build -C internal/cmd/benchnorm -o ../../bin/benchnorm
//go:generate go build -C internal/cmd/benchcmp -o ../../bin/benchcmp
//go:generate go build -C internal/cmd/chartgen -o ../../bin/chartgen
//go:generate go build -C internal/cmd/fmttrace -o ../../bin/fmttrace
//go:generate internal/bin/chartgen -o docs/charts bench.txt
