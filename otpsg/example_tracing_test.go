// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg_test

import (
	"context"
	"fmt"
	"runtime"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/otpsg"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/trace"
)

// Example demonstrating how to use the otpsg tracing integration
func Example_tracing() {
	// Configure a simple stdout exporter for demonstration
	exporter, _ := stdouttrace.New(stdouttrace.WithPrettyPrint())
	tp := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	// Create a root context with a parent span
	ctx, rootSpan := otel.Tracer("example").Start(context.Background(), "process-request")
	defer rootSpan.End()

	// Create a PSG job
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()

	// Create task pools
	computePool := psg.NewTaskPool(job, runtime.NumCPU())
	ioPool := psg.NewTaskPool(job, 10)

	// Define a traced task for data loading
	loadDataTask := otpsg.TracedTask("load-data", func(ctx context.Context) ([]int, error) {
		// In a real app, this would load data from a database or file
		fmt.Println("Loading data...")
		return []int{1, 2, 3, 4, 5}, nil
	})

	// Define a traced task for data processing
	processDataTask := otpsg.TracedTask("process-data", func(ctx context.Context) (int, error) {
		// In a real app, this would do some CPU-intensive processing
		fmt.Println("Processing data...")
		return 42, nil
	})

	// Define a gather function that processes loaded data
	dataGather := otpsg.TracedGather("handle-loaded-data",
		func(ctx context.Context, data []int, err error) error {
			if err != nil {
				return err
			}

			fmt.Println("Handling loaded data:", data)

			// Launch a processing task for the loaded data
			processGather := otpsg.TracedGather("handle-processed-data",
				func(ctx context.Context, result int, err error) error {
					if err != nil {
						return err
					}
					fmt.Println("Final result:", result)
					return nil
				})

			return processGather.Scatter(ctx, computePool, processDataTask)
		})

	// Start the pipeline by loading data
	if err := dataGather.Scatter(ctx, ioPool, loadDataTask); err != nil {
		fmt.Println("Error:", err)
	}

	// Wait for all tasks to complete
	if err := job.CloseAndGatherAll(ctx); err != nil {
		fmt.Println("Error during gather:", err)
	}

	// Output:
	// Loading data...
	// Handling loaded data: [1 2 3 4 5]
	// Processing data...
	// Final result: 42
}

// Example demonstrating fully instrumented tasks
func Example_instrumentedTask() {
	// Set up tracing provider (simplified)
	exporter, _ := stdouttrace.New(stdouttrace.WithPrettyPrint())
	tp := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	// Create a PSG job
	ctx := context.Background()
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()
	pool := psg.NewTaskPool(job, 1)

	// Create fully instrumented task and gather
	task := otpsg.InstrumentedTask("calculate-sum",
		func(ctx context.Context) (int, error) {
			sum := 0
			for i := 1; i <= 10; i++ {
				sum += i
			}
			return sum, nil
		})

	gather := otpsg.InstrumentedGather("handle-sum",
		func(ctx context.Context, sum int, err error) error {
			fmt.Println("Sum:", sum)
			return nil
		})

	// Use convenience scatter function
	err := otpsg.InstrumentedScatter(ctx, pool, task, gather)
	if err != nil {
		fmt.Println("Error:", err)
	}

	// Wait for all tasks to complete
	if err := job.CloseAndGatherAll(ctx); err != nil {
		fmt.Println("Error during gather:", err)
	}

	// Output:
	// Sum: 55
}
