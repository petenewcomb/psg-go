// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg_test

import (
	"context"
	"fmt"
	"io"

	"github.com/petenewcomb/streampool"
	"github.com/petenewcomb/streampool/otpsg"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Example_tracing shows the v2 flow-native tracing model: one span whose
// lifetime IS the flow. Traced starts the span and hands back the FlowOptions
// that (a) ride the span down every dispatch chain for correlation and (b) end
// the span exactly once at the flow's true end — after all work, including the
// async processing task launched from inside a skim, has drained.
func Example_tracing() {
	// Send span JSON to io.Discard so only the business fmt.Println lines land
	// on stdout for the Output check below.
	exporter, _ := stdouttrace.New(
		stdouttrace.WithWriter(io.Discard),
		stdouttrace.WithPrettyPrint(),
	)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)
	defer func() {
		_ = tp.Shutdown(context.Background())
	}()

	// Start the flow span. flow carries both the correlation value and the
	// end-at-true-end follow-up; span is set active on ctx.
	ctx, flow := otpsg.Traced(context.Background(), "process-request")
	span := trace.SpanFromContext(ctx)

	wave := streampool.NewWave()

	body := func(ctx context.Context) error {
		// Terminal sink for the processed result.
		resultSkimmer := streampool.NewFnSkimmer(
			func(ctx context.Context, result int, err error) error {
				if err != nil {
					return err
				}
				fmt.Println("Final result:", result)
				return nil
			}).In(wave)

		// Skim of the loaded data: launches an async processing task.
		dataSkimmer := streampool.NewFnSkimmer(
			func(ctx context.Context, data []int, err error) error {
				if err != nil {
					return err
				}
				fmt.Println("Handling loaded data:", data)

				processTask := streampool.NewTaskLauncher(func(ctx context.Context) error {
					// Correlate republishes the flow span as active so this
					// child span parents under it — the async body inherits the
					// flow rider, not otel's active-span.
					_, child := otel.Tracer("otpsg").Start(otpsg.Correlate(ctx), "process-data")
					defer child.End()

					fmt.Println("Processing data...")
					return resultSkimmer.Submit(ctx, 42)
				})
				return processTask.In(wave).Start(ctx)
			}).In(wave)

		// Loader task kicks off the pipeline.
		loadTask := streampool.NewTaskLauncher(func(ctx context.Context) error {
			fmt.Println("Loading data...")
			return dataSkimmer.Submit(ctx, []int{1, 2, 3, 4, 5})
		})
		if err := loadTask.In(wave).Start(ctx); err != nil {
			return err
		}
		return wave.CloseAndSkimAll(ctx)
	}

	if err := streampool.WithFlow(ctx, body, flow...); err != nil {
		fmt.Println("Error:", err)
	}

	// The follow-up ended the span at the flow's true end — no defer needed.
	fmt.Println("Span recording after flow:", span.IsRecording())

	// Output:
	// Loading data...
	// Handling loaded data: [1 2 3 4 5]
	// Processing data...
	// Final result: 42
	// Span recording after flow: false
}

// Example_instrumentedTask shows the metrics+logging op decorators wired onto a
// plain launcher/skimmer pipeline. Instrumented* adds no tracing — a span would
// be applied at the flow level via Traced + WithFlow (see Example_tracing).
func Example_instrumentedTask() {
	ctx := context.Background()
	wave := streampool.NewWave()

	task := otpsg.InstrumentedTask("calculate-sum",
		func(ctx context.Context) (int, error) {
			sum := 0
			for i := 1; i <= 10; i++ {
				sum += i
			}
			return sum, nil
		})

	skimmer := streampool.NewSkimmer(otpsg.InstrumentedSkim("handle-sum",
		func(ctx context.Context, sum int, err error) error {
			if err != nil {
				return err
			}
			fmt.Println("Sum:", sum)
			return nil
		})).In(wave)

	runner := streampool.NewTaskLauncher(func(ctx context.Context) error {
		result, err := task(ctx)
		return skimmer.SubmitResult(ctx, result, err)
	})

	if err := runner.In(wave).Start(ctx); err != nil {
		fmt.Println("Error:", err)
	}
	if err := wave.CloseAndSkimAll(ctx); err != nil {
		fmt.Println("Error during skim:", err)
	}

	// Output:
	// Sum: 55
}
