// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/influxdata/tdigest"
	"github.com/petenewcomb/psg-go"
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
		gather := psg.NewGatherOp(func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		})

		// Create a combiner taskPool
		combinerPool := psg.NewCombinerPool(job)

		// Create a combine operation
		combine := psg.NewCombineOp(
			gather,
			combinerPool,
			func() psg.Combiner[int, int] {
				return psg.FuncCombiner[int, int]{
					CombineFn: func(ctx context.Context, value int, err error, emit psg.CombinerEmitFunc[int]) {
						chk.NoError(err)
						emit(ctx, 0, nil)
					},
					FlushFn: func(ctx context.Context, emit psg.CombinerEmitFunc[int]) {
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
		psg.NewGatherOp[int](nil)
	})
}

func TestCombinerTryScatterNilTaskFuncPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()
	taskPool := psg.NewTaskPool(job, 1)

	chk.PanicsWithValue("task function must be non-nil", func() {
		gather := psg.NewGatherOp(
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

	gather := psg.NewGatherOp(
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
				innerGather := psg.NewGatherOp(
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

	gather := psg.NewGatherOp(
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
			gather := psg.NewGatherOp(
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

	gather := psg.NewGatherOp(
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
			innerGather := psg.NewGatherOp(
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
					// Only need to run gather-only once to cover all flush periods
					if combinerLimit == 0 && fpi > 0 {
						continue
					}

					var method string
					switch combinerLimit {
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
						simulateWork = func(d time.Duration) {
							// Make sure we at least yield
							time.Sleep(max(1, d))
						}
					}

					simulateWorkFrom := func(t time.Time, d time.Duration) {
						adjusted := d - time.Since(t)
						simulateWork(max(0, adjusted))
					}

					b.Run(name, func(b *testing.B) {
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()

						job := psg.NewJob(ctx)
						defer func() {
							job.CancelAndWait()
						}()
						taskPool := psg.NewTaskPool(job, -1)

						type taskResult struct {
							Time    time.Time
							Depth   int
							Latency time.Duration
						}

						type combinedResult struct {
							Time                time.Time
							Depth               int
							Count               int
							TaskLatenciesNs     *tdigest.CentroidList
							LatenciesNs         *tdigest.CentroidList
							DurationsNs         *tdigest.CentroidList
							DurationSum         time.Duration
							DurationCount       int
							WorkflowLatenciesNs *tdigest.CentroidList
							MaxConcurrency      int
						}

						tdigestPool := sync.Pool{
							New: func() any {
								return tdigest.New()
							},
						}
						newTDigest := func() *tdigest.TDigest {
							return tdigestPool.Get().(*tdigest.TDigest)
						}
						poolTDigest := func(t **tdigest.TDigest) {
							if *t != nil {
								(*t).Reset()
								tdigestPool.Put(*t)
								*t = nil
							}
						}

						centroidListPool := sync.Pool{
							New: func() any {
								return &tdigest.CentroidList{}
							},
						}
						newCentroidList := func(c ...tdigest.Centroid) *tdigest.CentroidList {
							cl := centroidListPool.Get().(*tdigest.CentroidList)
							*cl = append(*cl, c...)
							return cl
						}
						copyCentroidList := func(t *tdigest.TDigest) *tdigest.CentroidList {
							cl := centroidListPool.Get().(*tdigest.CentroidList)
							*cl = t.Centroids(*cl)
							return cl
						}
						poolCentroidList := func(cl *tdigest.CentroidList) {
							*cl = (*cl)[:0]
							centroidListPool.Put(cl)
						}

						totalTasksGathered := 0

						taskLatenciesNs := tdigest.New()

						combineLatenciesNs := tdigest.New()
						combineDurationsNs := tdigest.New()
						var combineDurationSum time.Duration
						combineDurationCount := 0
						combineWorkflowLatenciesNs := tdigest.New()
						combineCounts := tdigest.New()

						gatherLatenciesNs := tdigest.New()
						gatherDurationsNs := tdigest.New()
						var gatherDurationSum time.Duration
						gatherDurationCount := 0

						workflowLatenciesNs := tdigest.New()

						var combinerConcurrency atomic.Int32
						maxCombinerConcurrency := 0

						var newTaskFn func(startTime time.Time, depth int) psg.TaskFunc[taskResult]
						var scatter func(ctx context.Context, target psg.TaskPoolOrJob, task psg.TaskFunc[taskResult]) error

						gatherFn := func(ctx context.Context, combineRes combinedResult, err error) error {
							if err != nil {
								return err
							}

							gatherStartTime := time.Now()
							gatherLatencyNs := float64(gatherStartTime.Sub(combineRes.Time).Nanoseconds())

							// Front-load all measurement work before simulated work
							gatherLatenciesNs.Add(gatherLatencyNs, 1.0)
							totalTasksGathered += combineRes.Count
							taskLatenciesNs.AddCentroidList(*combineRes.TaskLatenciesNs)
							combineLatenciesNs.AddCentroidList(*combineRes.LatenciesNs)
							combineDurationsNs.AddCentroidList(*combineRes.DurationsNs)
							combineDurationSum += combineRes.DurationSum
							combineDurationCount += combineRes.DurationCount
							combineWorkflowLatenciesNs.AddCentroidList(*combineRes.WorkflowLatenciesNs)
							maxCombinerConcurrency = max(maxCombinerConcurrency, combineRes.MaxConcurrency)

							// Add gather latency to workflow latencies to get scatter-to-gather-start time
							for i := range *combineRes.WorkflowLatenciesNs {
								(*combineRes.WorkflowLatenciesNs)[i].Mean += gatherLatencyNs
							}
							workflowLatenciesNs.AddCentroidList(*combineRes.WorkflowLatenciesNs)

							combineCounts.Add(float64(combineRes.Count), 1.0)

							simulateWorkFrom(gatherStartTime, workloadDuration)
							workEndTime := time.Now()

							// Don't include scatter time in work duration inflation
							for range max(0, 3-combineRes.Depth) {
								if err := scatter(ctx, taskPool, newTaskFn(time.Now(), combineRes.Depth+1)); err != nil {
									return err
								}
							}

							gatherDuration := workEndTime.Sub(gatherStartTime)
							gatherDurationNs := float64(gatherDuration.Nanoseconds())
							gatherDurationsNs.Add(gatherDurationNs, 1.0)
							gatherDurationSum += gatherDuration
							gatherDurationCount++

							poolCentroidList(combineRes.TaskLatenciesNs)
							poolCentroidList(combineRes.LatenciesNs)
							poolCentroidList(combineRes.DurationsNs)
							poolCentroidList(combineRes.WorkflowLatenciesNs)

							return nil
						}

						gatherFuncAdapter := func(ctx context.Context, task taskResult, err error) error {
							now := time.Now()
							taskLatencyNsCentroid := tdigest.Centroid{
								Mean:   float64(now.Sub(task.Time).Nanoseconds()),
								Weight: 1.0,
							}
							combined := combinedResult{
								Time:                now,
								Depth:               task.Depth,
								Count:               1,
								TaskLatenciesNs:     newCentroidList(taskLatencyNsCentroid),
								LatenciesNs:         newCentroidList(taskLatencyNsCentroid),
								DurationsNs:         newCentroidList(),
								WorkflowLatenciesNs: newCentroidList(taskLatencyNsCentroid),
							}
							return gatherFn(ctx, combined, err)
						}

						idealCombinesPerGather := int(math.Round(float64(flushPeriod) / float64(workloadDuration)))

						// Setup processing - either gather-only or with combiner
						if combinerLimit == 0 {
							scatter = psg.NewGatherOp(gatherFuncAdapter).Scatter
						} else {
							gather := psg.NewGatherOp(gatherFn)
							combinerPool := psg.NewCombinerPool(job)
							combinerPool.SetLimits(max(0, combinerLimit), combinerLimit)
							combine := psg.NewCombineOp(gather, combinerPool, func() psg.Combiner[taskResult, combinedResult] {
								maxDepth := 0
								count := 0
								var taskLatenciesNs *tdigest.TDigest
								var durationsNs *tdigest.TDigest
								var durationSum time.Duration
								var latenciesNs *tdigest.TDigest
								var workflowLatenciesNs *tdigest.TDigest
								maxConcurrency := 0

								flush := func(ctx context.Context, emit psg.CombinerEmitFunc[combinedResult]) {
									if count > 0 {
										res := combinedResult{
											Time:                time.Now(),
											Depth:               maxDepth,
											Count:               count,
											TaskLatenciesNs:     copyCentroidList(taskLatenciesNs),
											LatenciesNs:         copyCentroidList(latenciesNs),
											DurationsNs:         copyCentroidList(durationsNs),
											DurationSum:         durationSum,
											DurationCount:       count,
											WorkflowLatenciesNs: copyCentroidList(workflowLatenciesNs),
											MaxConcurrency:      maxConcurrency,
										}
										emit(ctx, res, nil)
										combinerConcurrency.Add(-1)
									}

									maxDepth = 0
									count = 0
									poolTDigest(&taskLatenciesNs)
									poolTDigest(&latenciesNs)
									poolTDigest(&durationsNs)
									durationSum = 0
									poolTDigest(&workflowLatenciesNs)
									maxConcurrency = 0
								}

								return psg.FuncCombiner[taskResult, combinedResult]{
									CombineFn: func(ctx context.Context, taskRes taskResult, err error, emit psg.CombinerEmitFunc[combinedResult]) {
										if err != nil {
											emit(ctx, combinedResult{}, err)
										}

										combineStartTime := time.Now()
										latency := combineStartTime.Sub(taskRes.Time)

										// Front-load all measurement work before simulated work
										maxDepth = max(maxDepth, taskRes.Depth)
										if count == 0 {
											taskLatenciesNs = newTDigest()
											latenciesNs = newTDigest()
											durationsNs = newTDigest()
											workflowLatenciesNs = newTDigest()
											maxConcurrency = max(maxConcurrency, int(combinerConcurrency.Add(1)))
										}
										count++
										taskLatenciesNs.Add(float64(taskRes.Latency.Nanoseconds()), 1.0)
										latenciesNs.Add(float64(latency.Nanoseconds()), 1.0)
										// Workflow latency: scatter to combine start (queueing time)
										workflowLatenciesNs.Add(float64((taskRes.Latency + latency).Nanoseconds()), 1.0)

										simulateWorkFrom(combineStartTime, workloadDuration)
										workEndTime := time.Now()

										// Don't include scatter time in work duration inflation
										for range max(0, min(1-count, 3-taskRes.Depth)) {
											if err := scatter(ctx, taskPool, newTaskFn(time.Now(), taskRes.Depth+1)); err != nil {
												emit(ctx, combinedResult{}, err)
												return
											}
										}

										duration := workEndTime.Sub(combineStartTime)
										durationsNs.Add(float64(duration.Nanoseconds()), 1.0)

										durationSum += duration

										if count >= idealCombinesPerGather {
											flush(ctx, emit)
										}
									},
									FlushFn: flush,
								}
							})
							combine.SetMaxHoldTime(flushPeriod)

							scatter = func(ctx context.Context, target psg.TaskPoolOrJob, task psg.TaskFunc[taskResult]) error {
								if err := combine.Scatter(ctx, target, task); err != nil {
									return err
								}
								return nil
							}
						}

						var totalTasksLaunched atomic.Int64
						newTaskFn = func(startTime time.Time, depth int) psg.TaskFunc[taskResult] {
							totalTasksLaunched.Add(1)
							return func(context.Context) (taskResult, error) {
								now := time.Now()
								return taskResult{Time: now, Latency: now.Sub(startTime), Depth: depth}, nil
							}
						}

						opTasksGatheredOrigin := totalTasksGathered
						op := func() int {
							for {
								if err := scatter(ctx, taskPool, newTaskFn(time.Now(), 0)); err != nil {
									b.Fatalf("Error: %v", err)
								}

								if totalTasksGathered != opTasksGatheredOrigin {
									tasksGathered := totalTasksGathered - opTasksGatheredOrigin
									opTasksGatheredOrigin = totalTasksGathered
									return tasksGathered
								}
							}
						}

						idealThroughput := 1 / float64(workloadDuration.Seconds())
						idealCombinerConcurrency := 0.0
						if combinerLimit != 0 {
							idealCombinerConcurrency = float64(combinerLimit)
							if combinerLimit == -1 || combinerLimit > idealCombinesPerGather {
								idealCombinerConcurrency = float64(idealCombinesPerGather)
							}
							if workload == "processing" && idealCombinerConcurrency >= float64(availableCores) {
								idealCombinerConcurrency = float64(availableCores*idealCombinesPerGather) / float64(1+idealCombinesPerGather)
							}

							idealCombinerThroughput := idealCombinerConcurrency / float64(workloadDuration.Seconds())
							idealGatherThroughput := float64(idealCombinesPerGather) / float64(workloadDuration.Seconds())
							idealThroughput = min(idealCombinerThroughput, idealGatherThroughput)
						}

						warmupStartTime := time.Now()
						for time.Since(warmupStartTime) < time.Second {
							op()
						}

						tasksGatheredOrigin := totalTasksGathered
						taskLatenciesNs.Reset()
						combineLatenciesNs.Reset()
						combineDurationsNs.Reset()
						combineDurationSum = 0
						combineDurationCount = 0
						combineWorkflowLatenciesNs.Reset()
						gatherLatenciesNs.Reset()
						gatherDurationsNs.Reset()
						gatherDurationSum = 0
						gatherDurationCount = 0
						workflowLatenciesNs.Reset()
						for b.Loop() {
							op()
						}

						// We purposefully do not run job.CloseAndGatherAll
						// before capturing results to avoid inflating
						// overallSum with data gathered outside the
						// benchmarking loop.

						finalCombinerConcurrency := combinerConcurrency.Load()
						tasksGathered := float64(totalTasksGathered - tasksGatheredOrigin)

						// Now call CloseAndGatherAll to make sure nothing was lost.
						require.NoError(b, job.CloseAndGatherAll(ctx))
						require.Equal(b, totalTasksLaunched.Load(), int64(totalTasksGathered))

						b.ReportAllocs()

						// Throughput - the primary metric for this benchmark
						throughput := tasksGathered / b.Elapsed().Seconds()
						b.ReportMetric(throughput, "tasks/sec")

						// Tasks per operation - needed to normalize allocs/op and B/op
						tasksPerOp := tasksGathered / float64(b.N)
						b.ReportMetric(tasksPerOp, "tasks/op")

						b.ReportMetric(taskLatenciesNs.Quantile(0.99), "p99-task-latency-ns")
						b.ReportMetric(taskLatenciesNs.Quantile(0.50), "p50-task-latency-ns")

						if combinerLimit != 0 {
							b.ReportMetric(combineLatenciesNs.Quantile(0.99), "p99-combine-latency-ns")
							b.ReportMetric(combineLatenciesNs.Quantile(0.50), "p50-combine-latency-ns")
							b.ReportMetric(combineDurationsNs.Quantile(0.99), "p99-combine-duration-ns")
							b.ReportMetric(combineDurationsNs.Quantile(0.50), "p50-combine-duration-ns")
							b.ReportMetric(combineWorkflowLatenciesNs.Quantile(0.99), "p99-combine-workflow-latency-ns")
							b.ReportMetric(combineWorkflowLatenciesNs.Quantile(0.50), "p50-combine-workflow-latency-ns")
							b.ReportMetric(combineCounts.Quantile(0.99), "p99-combine-count")
							b.ReportMetric(combineCounts.Quantile(0.50), "p50-combine-count")
							b.ReportMetric(combineCounts.Quantile(0.01), "p01-combine-count")
						}

						b.ReportMetric(gatherLatenciesNs.Quantile(0.99), "p99-gather-latency-ns")
						b.ReportMetric(gatherLatenciesNs.Quantile(0.50), "p50-gather-latency-ns")
						b.ReportMetric(gatherDurationsNs.Quantile(0.99), "p99-gather-duration-ns")
						b.ReportMetric(gatherDurationsNs.Quantile(0.50), "p50-gather-duration-ns")

						combineDurationInflation := float64(combineDurationSum)/float64(combineDurationCount)/float64(workloadDuration) - 1
						gatherDurationInflation := float64(gatherDurationSum)/float64(gatherDurationCount)/float64(workloadDuration) - 1
						overallDurationInflation := float64(combineDurationSum+gatherDurationSum)/float64(combineDurationCount+gatherDurationCount)/float64(workloadDuration) - 1
						if combinerLimit != 0 {
							b.ReportMetric(combineDurationInflation, "combine-duration-inflation")
						}
						b.ReportMetric(gatherDurationInflation, "gather-duration-inflation")
						b.ReportMetric(overallDurationInflation, "overall-duration-inflation")

						b.ReportMetric(workflowLatenciesNs.Quantile(0.99), "p99-workflow-latency-ns")
						b.ReportMetric(workflowLatenciesNs.Quantile(0.50), "p50-workflow-latency-ns")

						if combinerLimit != 0 {
							b.ReportMetric(float64(maxCombinerConcurrency), "max-combiner-concurrency")
							b.ReportMetric(float64(finalCombinerConcurrency), "final-combiner-concurrency")
							b.ReportMetric(idealCombinerConcurrency, "ideal-combiner-concurrency")
						}

						rectifiedThroughput := throughput * (1 + overallDurationInflation)
						idealThroughputRatio := rectifiedThroughput / idealThroughput
						b.ReportMetric(rectifiedThroughput, "rectified-tasks/sec")
						b.ReportMetric(idealThroughput, "ideal-tasks/sec")
						b.ReportMetric(idealThroughputRatio, "ideal-throughput-ratio")
					})
				}
			}
		}
	}
}
