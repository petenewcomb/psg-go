// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/internal/ema"
	"github.com/petenewcomb/psg-go/internal/reservoir"
	"github.com/stretchr/testify/require"
)

func TestCombinerScatterNilTaskFuncPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()
	taskPool := psg.NewTaskPool(job, 1)

	chk.PanicsWithValue("task function must be non-nil", func() {
		// Create a gather
		gather := psg.NewGather(func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		})

		// Create a combiner taskPool
		combinerPool := psg.NewCombinerPool(job)

		// Create a combine operation
		combine := psg.NewCombine(
			gather,
			combinerPool,
			func() psg.Combiner[int, int] {
				return psg.FuncCombiner[int, int]{
					CombineFunc: func(ctx context.Context, value int, err error, emit psg.CombinerEmitFunc[int]) {
						chk.NoError(err)
						emit(ctx, 0, nil)
					},
					FlushFunc: func(ctx context.Context, emit psg.CombinerEmitFunc[int]) {
						// No-op in this test
					},
				}
			},
		)

		// Should panic with nil task function
		_ = combine.Scatter(
			ctx,
			taskPool,
			nil, // Nil TaskFunc should panic
		)
	})
}

func TestCombinerScatterNilGatherFuncPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()

	chk.PanicsWithValue("gather function must be non-nil", func() {
		psg.NewGather[int](nil)
	})
}

func TestCombinerTryScatterNilTaskFuncPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()
	taskPool := psg.NewTaskPool(job, 1)

	chk.PanicsWithValue("task function must be non-nil", func() {
		gather := psg.NewGather(
			func(ctx context.Context, result int, err error) error {
				return nil
			},
		)
		_, _ = gather.TryScatter(
			ctx,
			taskPool,
			nil, // Nil TaskFunc should panic
		)
	})
}

func TestCombinerScatterFromTask(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	job := psg.NewJob(ctx)
	taskPool := psg.NewTaskPool(job, 1)

	gather := psg.NewGather(
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	)
	err := gather.Scatter(
		ctx,
		taskPool,
		func(ctx context.Context) (int, error) {
			chk.PanicsWithValue("Scatter called from within TaskFunc; move call to GatherFunc instead", func() {
				innerGather := psg.NewGather(
					func(ctx context.Context, result int, err error) error {
						chk.NoError(err)
						chk.Fail("should not get here")
						return nil
					},
				)
				chk.NoError(innerGather.Scatter(
					ctx,
					taskPool,
					func(ctx context.Context) (int, error) {
						chk.Fail("should not get here")
						return 0, nil
					},
				))
			})
			return 0, nil
		},
	)
	chk.NoError(err)
	chk.NoError(job.CloseAndGatherAll(ctx))
}

func TestCombinerTaskCanScatterToSubJob(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create parent job with task pool
	parentJob := psg.NewJob(ctx)
	defer parentJob.CancelAndWait()
	parentTaskPool := psg.NewTaskPool(parentJob, 1)

	// Variable to track execution flow
	subJobTaskRan := false

	gather := psg.NewGather(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	err := gather.Scatter(
		ctx,
		parentTaskPool,
		func(ctx context.Context) (bool, error) {
			// Create a sub-job inside the task
			subJob := psg.NewJob(ctx)
			defer subJob.CancelAndWait()
			subTaskPool := psg.NewTaskPool(subJob, 1)

			// This should succeed - scattering a task to the sub-job's task pool
			gather := psg.NewGather(
				func(ctx context.Context, result bool, err error) error {
					chk.NoError(err)
					chk.True(result)
					return nil
				},
			)
			err := gather.Scatter(
				ctx,
				subTaskPool,
				func(ctx context.Context) (bool, error) {
					subJobTaskRan = true
					return true, nil
				},
			)
			chk.NoError(err)

			// Gather all results in the sub-job
			chk.NoError(subJob.CloseAndGatherAll(ctx))

			return true, nil
		},
	)

	chk.NoError(err)
	chk.NoError(parentJob.CloseAndGatherAll(ctx))

	// Verify the sub-job task executed successfully
	chk.True(subJobTaskRan, "The task in the sub-job should have run")
}

func TestCombinerTaskCannotScatterToParentJob(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create parent job with task pool
	parentJob := psg.NewJob(ctx)
	defer parentJob.CancelAndWait()
	parentTaskPool := psg.NewTaskPool(parentJob, 1)

	gather := psg.NewGather(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	err := gather.Scatter(
		ctx,
		parentTaskPool,
		func(ctx context.Context) (bool, error) {
			// This should panic - attempting to scatter to the parent job's task pool
			// while inside a task of that same job
			innerGather := psg.NewGather(
				func(ctx context.Context, result bool, err error) error {
					chk.Fail("Should not get here - parent task pool gather should not run")
					return nil
				},
			)
			chk.PanicsWithValue("Scatter called from within TaskFunc; move call to GatherFunc instead", func() {
				_ = innerGather.Scatter(
					ctx,
					parentTaskPool,
					func(ctx context.Context) (bool, error) {
						chk.Fail("Should not get here - parent task pool task should not run")
						return false, nil
					},
				)
			})

			return true, nil
		},
	)

	chk.NoError(err)
	chk.NoError(parentJob.CloseAndGatherAll(ctx))
}

// BenchmarkCombinerThroughput measures the maximum throughput of processing
// a continuous stream of data with gather-only vs. combiner approaches
func BenchmarkCombinerThroughput(b *testing.B) {
	combinerLimits := []int{
		-2, // direct
		-1, // combine, unlimited
		0,  // gather-only
		1, 2, 3, 4,
	}
	availableCores := runtime.NumCPU()
	for {
		prevLimit := combinerLimits[len(combinerLimits)-1]
		if prevLimit >= availableCores {
			break
		}
		combinerLimits = append(combinerLimits, prevLimit*2)
	}
	combinerLimits = append(combinerLimits, availableCores, availableCores*2)
	slices.Sort(combinerLimits)
	combinerLimits = slices.Compact(combinerLimits)

	// Run with different worker configurations
	for _, workload := range []string{"processing", "waiting"} {
		for _, workloadDuration := range []time.Duration{
			1 * time.Nanosecond,
			10 * time.Microsecond,
			100 * time.Microsecond,
			1 * time.Millisecond,
		} {
			for fpi, flushPeriod := range []time.Duration{
				workloadDuration,
				10 * workloadDuration,
				100 * workloadDuration,
			} {
				for _, combinerLimit := range combinerLimits {
					// Only need to run direct and gather-only once to cover all flush periods
					if (combinerLimit == -2 || combinerLimit == 0) && fpi > 0 {
						continue
					}

					var method string
					switch combinerLimit {
					case -2:
						method = "direct"
					case 0:
						method = "gatherOnly"
					default:
						method = "combine"
					}
					name := fmt.Sprintf(
						"workload=%s/duration=%v/flushPeriod=%v/method=%s/combinerLimit=%d",
						workload,
						workloadDuration,
						flushPeriod,
						method,
						combinerLimit,
					)

					burnCPU := func(d time.Duration) {
						deadline := time.Now().Add(d)
						x := 0.0
						for time.Now().Before(deadline) {
							x = math.Sqrt(x + 33)
						}
					}

					var simulateWork func(d time.Duration)
					switch workload {
					case "processing":
						simulateWork = burnCPU
					case "waiting":
						simulateWork = time.Sleep
					}

					simulateWorkFrom := func(t time.Time, d time.Duration) {
						d -= time.Since(t)
						if d > 0 {
							simulateWork(d)
						}
					}

					b.Run(name, func(b *testing.B) {
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()

						job := psg.NewJob(ctx)
						defer job.CancelAndWait()
						taskPool := psg.NewTaskPool(job, -1)

						type taskResult struct {
							Time        time.Time
							ScatterTime time.Time
						}

						type combinedResult struct {
							Time                time.Time
							Count               int64
							EarliestScatterTime time.Time
							LatestScatterTime   time.Time
							MedianScatterTime   time.Time
						}

						// Reservoir sampling for latency measurements
						const reservoirCapacity = 10000

						taskLatencies := make([]time.Duration, reservoirCapacity)
						taskLatencyCount := atomic.Int64{}

						combineLatencies := make([]time.Duration, reservoirCapacity)
						combineLatencyCount := atomic.Int64{}

						combineDurations := make([]time.Duration, reservoirCapacity)
						combineDurationCount := atomic.Int64{}

						combineWorkflowLatencies := make([]time.Duration, reservoirCapacity)
						combineWorkflowLatencyCount := atomic.Int64{}

						gatherLatencies := make([]time.Duration, reservoirCapacity)
						gatherLatencyCount := int64(0)

						gatherDurations := make([]time.Duration, reservoirCapacity)
						gatherDurationCount := int64(0)

						workflowMinLatencies := make([]time.Duration, reservoirCapacity)
						workflowMinLatencyCount := int64(0)

						workflowMedianLatencies := make([]time.Duration, reservoirCapacity)
						workflowMedianLatencyCount := int64(0)

						workflowMaxLatencies := make([]time.Duration, reservoirCapacity)
						workflowMaxLatencyCount := int64(0)

						totalTasksGathered := int64(0)

						gatherFunc := func(ctx context.Context, combineRes combinedResult, err error) error {
							if err != nil {
								return err
							}

							gatherStartTime := time.Now()

							// Reservoir sampling for gather latency (can do before work)
							gatherLatencyCount++
							reservoir.Add(gatherLatencies, gatherLatencyCount, gatherStartTime.Sub(combineRes.Time))

							// Do simulated work, adjusted for measurement overhead
							simulateWorkFrom(gatherStartTime, workloadDuration)
							workEndTime := time.Now()

							// Reservoir sampling for gather duration
							gatherDurationCount++
							reservoir.Add(gatherDurations, gatherDurationCount, workEndTime.Sub(gatherStartTime))

							// Calculate workflow latencies (min/median/max) - scatter to gather start
							// Min workflow latency (newest task)
							workflowMinLatencyCount++
							reservoir.Add(workflowMinLatencies, workflowMinLatencyCount, gatherStartTime.Sub(combineRes.LatestScatterTime))

							// Max workflow latency (oldest task)
							workflowMaxLatencyCount++
							reservoir.Add(workflowMaxLatencies, workflowMaxLatencyCount, gatherStartTime.Sub(combineRes.EarliestScatterTime))

							// Median workflow latency (representative task)
							workflowMedianLatencyCount++
							reservoir.Add(workflowMedianLatencies, workflowMedianLatencyCount, gatherStartTime.Sub(combineRes.MedianScatterTime))

							totalTasksGathered += combineRes.Count
							return nil
						}

						gatherFuncAdapter := func(ctx context.Context, task taskResult, err error) error {
							now := time.Now()

							// For gather-only and direct cases, create a simple combined result
							// with the scatter time for workflow latency calculation
							combined := combinedResult{
								Time:                now,
								Count:               1,
								EarliestScatterTime: task.ScatterTime,
								LatestScatterTime:   task.ScatterTime,
								MedianScatterTime:   task.ScatterTime,
							}
							return gatherFunc(ctx, combined, err)
						}

						// Setup processing - either gather-only or with combiner
						var scatter func(ctx context.Context, target psg.TaskPoolOrJob, task psg.TaskFunc[taskResult]) error
						switch combinerLimit {
						case -2:
							scatter = func(ctx context.Context, target psg.TaskPoolOrJob, task psg.TaskFunc[taskResult]) error {
								taskRes, err := task(ctx)
								return gatherFuncAdapter(ctx, taskRes, err)
							}
						case 0:
							scatter = psg.NewGather(gatherFuncAdapter).Scatter
						default:
							gather := psg.NewGather(gatherFunc)
							combinerPool := psg.NewCombinerPool(job)
							combinerPool.SetLimits(max(0, combinerLimit), combinerLimit)
							combine := psg.NewCombine(gather, combinerPool, func() psg.Combiner[taskResult, combinedResult] {
								count := int64(0)
								var earliestScatterTime, latestScatterTime time.Time
								scatterTimesSample := [8]time.Time{}
								nextFlushTime := time.Now().Add(flushPeriod)

								flush := func(ctx context.Context, emit psg.CombinerEmitFunc[combinedResult]) {
									if count > 0 {
										// Prepare sample
										sampleLen := reservoir.Len(scatterTimesSample[:], count)
										slices.SortFunc(scatterTimesSample[:sampleLen], func(a, b time.Time) int {
											return a.Compare(b)
										})

										res := combinedResult{
											Time:                time.Now(),
											Count:               count,
											EarliestScatterTime: earliestScatterTime,
											LatestScatterTime:   latestScatterTime,
											MedianScatterTime:   scatterTimesSample[sampleLen/2],
										}
										emit(ctx, res, nil)
									}
									count = 0
									nextFlushTime = time.Now().Add(flushPeriod)
								}

								return psg.FuncCombiner[taskResult, combinedResult]{
									CombineFunc: func(ctx context.Context, taskRes taskResult, err error, emit psg.CombinerEmitFunc[combinedResult]) {
										if err != nil {
											emit(ctx, combinedResult{}, err)
											return
										}

										combineStartTime := time.Now()

										// Track scatter times for workflow latency calculation
										scatterTime := taskRes.ScatterTime
										switch {
										case count == 0:
											// First task in this batch
											earliestScatterTime = scatterTime
											latestScatterTime = scatterTime
										case scatterTime.Before(earliestScatterTime):
											earliestScatterTime = scatterTime
										case scatterTime.After(latestScatterTime):
											latestScatterTime = scatterTime
										}

										// Reservoir sampling for scatter times (for median calculation)
										reservoir.AddFunc(scatterTimesSample[:], int64(count+1), func(sample []time.Time, index int) {
											sample[index] = scatterTime
										})

										// Sample combine latency (can do before work)
										combineCount := combineLatencyCount.Add(1)
										reservoir.AddFunc(combineLatencies, combineCount, func(sample []time.Duration, index int) {
											atomic.StoreInt64((*int64)(&sample[index]), int64(combineStartTime.Sub(taskRes.Time)))
										})

										// Do simulated work, adjusted for measurement overhead
										simulateWorkFrom(combineStartTime, workloadDuration)
										workEndTime := time.Now()

										durationCount := combineDurationCount.Add(1)
										reservoir.AddFunc(combineDurations, durationCount, func(sample []time.Duration, index int) {
											atomic.StoreInt64((*int64)(&sample[index]), int64(workEndTime.Sub(combineStartTime)))
										})

										// Sample combine workflow latency (scatter to combine start)
										workflowCount := combineWorkflowLatencyCount.Add(1)
										reservoir.AddFunc(combineWorkflowLatencies, workflowCount, func(sample []time.Duration, index int) {
											atomic.StoreInt64((*int64)(&sample[index]), int64(combineStartTime.Sub(taskRes.ScatterTime)))
										})

										count++
										if time.Now().After(nextFlushTime) {
											flush(ctx, emit)
										}
									},
									FlushFunc: flush,
								}
							})
							scatter = func(ctx context.Context, target psg.TaskPoolOrJob, task psg.TaskFunc[taskResult]) error {
								if err := combine.Scatter(ctx, target, task); err != nil {
									return err
								}
								return nil
							}
						}

						newTaskFunc := func(scatterTime time.Time) psg.TaskFunc[taskResult] {
							return func(context.Context) (taskResult, error) {
								res := taskResult{
									Time:        time.Now(),
									ScatterTime: scatterTime,
								}

								// Atomic reservoir sampling for task metrics
								count := taskLatencyCount.Add(1)
								reservoir.AddFunc(taskLatencies, count, func(sample []time.Duration, index int) {
									atomic.StoreInt64((*int64)(&sample[index]), int64(res.Time.Sub(res.ScatterTime)))
								})

								return res, nil
							}
						}

						totalTasksLaunched := int64(0)
						op := func() {
							if err := scatter(ctx, taskPool, newTaskFunc(time.Now())); err != nil {
								b.Fatalf("Error: %v", err)
							}
							totalTasksLaunched++
						}

						tau := ema.Tau(1000 * time.Millisecond)
						var avgLagRatio ema.State
						var avgLagRatioTrend ema.State
						avgLagRatio.EMA = 1.0
						avgLagRatioTrend.EMA = 1.0
						var avgThroughput ema.State
						var avgThroughputTrend ema.State
						//warmupEnd := time.Now().Add(100 * flushPeriod)
						//for time.Now().Before(warmupEnd) {
						lastReportTime := time.Now()
						lastUpdateTime := time.Now()
						for math.Abs(avgLagRatioTrend.Get()) > 0.1 || math.Abs(avgThroughputTrend.Get()) > 0.1 {
							oldCount := totalTasksGathered
							for totalTasksGathered == oldCount {
								op()
							}
							now := time.Now()
							elapsedTime := float64(now.Sub(lastUpdateTime))
							lastUpdateTime = now
							tasksGathered := float64(totalTasksGathered - oldCount)
							lagRatio := float64(totalTasksLaunched-totalTasksGathered) / tasksGathered
							previousAvgLagRatio := avgLagRatio.Get()
							avgLagRatio.Update(tau, lagRatio)
							avgLagRatioTrend.Update(tau, avgLagRatio.Get()-previousAvgLagRatio)
							previousAvgThroughput := avgThroughput.Get()
							avgThroughput.Update(tau, tasksGathered/elapsedTime)
							avgThroughputTrend.Update(tau, avgThroughput.Get()-previousAvgThroughput)
							if now.Sub(lastReportTime) > time.Second {
								//fmt.Println(lagRatio, avgLagRatio.Get(), avgLagRatioTrend.Get())
								lastReportTime = now
							}
						}

						//fmt.Println("starting test")
						tasksGatheredOrigin := totalTasksGathered
						// Reset counters for measurement phase
						taskLatencyCount.Store(0)
						combineLatencyCount.Store(0)
						combineDurationCount.Store(0)
						combineWorkflowLatencyCount.Store(0)
						gatherLatencyCount = 0
						gatherDurationCount = 0
						workflowMinLatencyCount = 0
						workflowMedianLatencyCount = 0
						workflowMaxLatencyCount = 0
						for b.Loop() {
							oldCount := totalTasksGathered
							for totalTasksGathered == oldCount {
								op()
							}
						}

						// We purposefully do not run job.CloseAndGatherAll
						// before capturing results to avoid inflating
						// overallSum with data gathered outside the
						// benchmarking loop.

						tasksGathered := totalTasksGathered - tasksGatheredOrigin

						//fmt.Println("ended test")

						// Now call CloseAndGatherAll to make sure nothing was lost.
						require.NoError(b, job.CloseAndGatherAll(ctx))
						require.Equal(b, totalTasksLaunched, totalTasksGathered)

						b.ReportAllocs()

						// Throughput - the primary metric for this benchmark
						b.ReportMetric(float64(tasksGathered)/b.Elapsed().Seconds(), "tasks/sec")

						// Tasks per operation - needed to normalize allocs/op and B/op
						tasksPerOp := float64(tasksGathered) / float64(b.N)
						b.ReportMetric(tasksPerOp, "tasks/op")

						// Finalize reservoirs in place for quantile reporting
						taskLatencyFinalCount := taskLatencyCount.Load()
						reservoir.Finalize(taskLatencies, taskLatencyFinalCount)
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(taskLatencies, taskLatencyFinalCount, 0.99)), "p99-task-latency-ns")
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(taskLatencies, taskLatencyFinalCount, 0.50)), "p50-task-latency-ns")

						combineLatencyFinalCount := combineLatencyCount.Load()
						reservoir.Finalize(combineLatencies, combineLatencyFinalCount)
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(combineLatencies, combineLatencyFinalCount, 0.99)), "p99-combine-latency-ns")
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(combineLatencies, combineLatencyFinalCount, 0.50)), "p50-combine-latency-ns")

						combineDurationFinalCount := combineDurationCount.Load()
						reservoir.Finalize(combineDurations, combineDurationFinalCount)
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(combineDurations, combineDurationFinalCount, 0.99)), "p99-combine-duration-ns")
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(combineDurations, combineDurationFinalCount, 0.50)), "p50-combine-duration-ns")

						combineWorkflowLatencyFinalCount := combineWorkflowLatencyCount.Load()
						reservoir.Finalize(combineWorkflowLatencies, combineWorkflowLatencyFinalCount)
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(combineWorkflowLatencies, combineWorkflowLatencyFinalCount, 0.99)), "p99-combine-workflow-latency-ns")
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(combineWorkflowLatencies, combineWorkflowLatencyFinalCount, 0.50)), "p50-combine-workflow-latency-ns")

						reservoir.Finalize(gatherLatencies, gatherLatencyCount)
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(gatherLatencies, gatherLatencyCount, 0.99)), "p99-gather-latency-ns")
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(gatherLatencies, gatherLatencyCount, 0.50)), "p50-gather-latency-ns")

						reservoir.Finalize(gatherDurations, gatherDurationCount)
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(gatherDurations, gatherDurationCount, 0.99)), "p99-gather-duration-ns")
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(gatherDurations, gatherDurationCount, 0.50)), "p50-gather-duration-ns")

						reservoir.Finalize(workflowMinLatencies, workflowMinLatencyCount)
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(workflowMinLatencies, workflowMinLatencyCount, 0.99)), "p99-workflow-min-latency-ns")
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(workflowMinLatencies, workflowMinLatencyCount, 0.50)), "p50-workflow-min-latency-ns")

						reservoir.Finalize(workflowMedianLatencies, workflowMedianLatencyCount)
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(workflowMedianLatencies, workflowMedianLatencyCount, 0.99)), "p99-workflow-median-latency-ns")
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(workflowMedianLatencies, workflowMedianLatencyCount, 0.50)), "p50-workflow-median-latency-ns")

						reservoir.Finalize(workflowMaxLatencies, workflowMaxLatencyCount)
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(workflowMaxLatencies, workflowMaxLatencyCount, 0.99)), "p99-workflow-max-latency-ns")
						b.ReportMetric(float64(reservoir.InterpolatedQuantile(workflowMaxLatencies, workflowMaxLatencyCount, 0.50)), "p50-workflow-max-latency-ns")
					})
				}
			}
		}
	}
}
