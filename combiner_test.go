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
	"testing"
	"time"

	"github.com/influxdata/tdigest"
	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/internal/ema"
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

					b.Run(name, func(b *testing.B) {
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()

						job := psg.NewJob(ctx)
						defer job.CancelAndWait()
						taskPool := psg.NewTaskPool(job, -1)

						type taskResult struct {
							Time    time.Time
							Latency time.Duration
						}

						type combinedResult struct {
							Time                time.Time
							Count               int
							TaskLatenciesNs     *tdigest.CentroidList
							LatenciesNs         *tdigest.CentroidList
							DurationsNs         *tdigest.CentroidList
							WorkflowLatenciesNs *tdigest.CentroidList
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
						combineWorkflowLatenciesNs := tdigest.New()

						gatherLatenciesNs := tdigest.New()
						gatherDurationsNs := tdigest.New()

						workflowLatenciesNs := tdigest.New()
						workflowDurationsNs := tdigest.New()

						gatherFunc := func(ctx context.Context, combineRes combinedResult, err error) error {
							if err != nil {
								return err
							}

							workStartTime := time.Now()
							gatherLatencyNs := float64(workStartTime.Sub(combineRes.Time).Nanoseconds())
							gatherLatenciesNs.Add(gatherLatencyNs, 1.0)

							simulateWork(workloadDuration)

							now := time.Now()

							totalTasksGathered += combineRes.Count

							taskLatenciesNs.AddCentroidList(*combineRes.TaskLatenciesNs)
							combineLatenciesNs.AddCentroidList(*combineRes.LatenciesNs)
							combineDurationsNs.AddCentroidList(*combineRes.DurationsNs)
							combineWorkflowLatenciesNs.AddCentroidList(*combineRes.WorkflowLatenciesNs)

							gatherDurationNs := float64(now.Sub(workStartTime).Nanoseconds())
							gatherDurationsNs.Add(gatherDurationNs, 1.0)

							for i := range *combineRes.WorkflowLatenciesNs {
								(*combineRes.WorkflowLatenciesNs)[i].Mean += gatherLatencyNs
							}
							workflowLatenciesNs.AddCentroidList(*combineRes.WorkflowLatenciesNs)

							for i := range *combineRes.DurationsNs {
								(*combineRes.DurationsNs)[i].Mean += gatherDurationNs
							}
							workflowDurationsNs.AddCentroidList(*combineRes.DurationsNs)

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
								Count:               1,
								TaskLatenciesNs:     newCentroidList(taskLatencyNsCentroid),
								LatenciesNs:         newCentroidList(taskLatencyNsCentroid),
								DurationsNs:         newCentroidList(tdigest.Centroid{Mean: 0.0, Weight: 1.0}),
								WorkflowLatenciesNs: newCentroidList(taskLatencyNsCentroid),
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
								count := 0
								var taskLatenciesNs *tdigest.TDigest
								var durationsNs *tdigest.TDigest
								var latenciesNs *tdigest.TDigest
								var workflowLatenciesNs *tdigest.TDigest

								nextFlushTime := time.Now().Add(flushPeriod)
								flush := func(ctx context.Context, emit psg.CombinerEmitFunc[combinedResult]) {
									if count > 0 {
										res := combinedResult{
											Time:                time.Now(),
											Count:               count,
											TaskLatenciesNs:     copyCentroidList(taskLatenciesNs),
											LatenciesNs:         copyCentroidList(latenciesNs),
											DurationsNs:         copyCentroidList(durationsNs),
											WorkflowLatenciesNs: copyCentroidList(workflowLatenciesNs),
										}
										emit(ctx, res, nil)
									}

									count = 0
									poolTDigest(&taskLatenciesNs)
									poolTDigest(&latenciesNs)
									poolTDigest(&durationsNs)
									poolTDigest(&workflowLatenciesNs)

									nextFlushTime = time.Now().Add(flushPeriod)
								}

								return psg.FuncCombiner[taskResult, combinedResult]{
									CombineFunc: func(ctx context.Context, taskRes taskResult, err error, emit psg.CombinerEmitFunc[combinedResult]) {
										if err != nil {
											emit(ctx, combinedResult{}, err)
										}

										workStartTime := time.Now()
										latency := workStartTime.Sub(taskRes.Time)

										simulateWork(workloadDuration)

										now := time.Now()
										duration := now.Sub(workStartTime)

										if count == 0 {
											taskLatenciesNs = newTDigest()
											latenciesNs = newTDigest()
											durationsNs = newTDigest()
											workflowLatenciesNs = newTDigest()
										}
										count++
										taskLatenciesNs.Add(float64(taskRes.Latency.Nanoseconds()), 1.0)
										latenciesNs.Add(float64(latency.Nanoseconds()), 1.0)
										durationsNs.Add(float64(duration.Nanoseconds()), 1.0)
										workflowLatenciesNs.Add(float64((taskRes.Latency + latency).Nanoseconds()), 1.0)

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

						newTaskFunc := func(startTime time.Time) psg.TaskFunc[taskResult] {
							return func(context.Context) (taskResult, error) {
								now := time.Now()
								return taskResult{Time: now, Latency: now.Sub(startTime)}, nil
							}
						}

						totalTasksLaunched := 0
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
						taskLatenciesNs.Reset()
						combineLatenciesNs.Reset()
						combineDurationsNs.Reset()
						combineWorkflowLatenciesNs.Reset()
						gatherLatenciesNs.Reset()
						gatherDurationsNs.Reset()
						workflowLatenciesNs.Reset()
						workflowDurationsNs.Reset()
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

						b.ReportMetric(taskLatenciesNs.Quantile(0.99), "p99-task-latency-ns")
						b.ReportMetric(taskLatenciesNs.Quantile(0.50), "p50-task-latency-ns")

						b.ReportMetric(combineLatenciesNs.Quantile(0.99), "p99-combine-latency-ns")
						b.ReportMetric(combineLatenciesNs.Quantile(0.50), "p50-combine-latency-ns")
						b.ReportMetric(combineDurationsNs.Quantile(0.99), "p99-combine-duration-ns")
						b.ReportMetric(combineDurationsNs.Quantile(0.50), "p50-combine-duration-ns")
						b.ReportMetric(combineWorkflowLatenciesNs.Quantile(0.99), "p99-combine-workflow-latency-ns")
						b.ReportMetric(combineWorkflowLatenciesNs.Quantile(0.50), "p50-combine-workflow-latency-ns")

						b.ReportMetric(gatherLatenciesNs.Quantile(0.99), "p99-gather-latency-ns")
						b.ReportMetric(gatherLatenciesNs.Quantile(0.50), "p50-gather-latency-ns")
						b.ReportMetric(gatherDurationsNs.Quantile(0.99), "p99-gather-duration-ns")
						b.ReportMetric(gatherDurationsNs.Quantile(0.50), "p50-gather-duration-ns")

						b.ReportMetric(workflowLatenciesNs.Quantile(0.99), "p99-workflow-latency-ns")
						b.ReportMetric(workflowLatenciesNs.Quantile(0.50), "p50-workflow-latency-ns")
						b.ReportMetric(workflowDurationsNs.Quantile(0.99), "p99-workflow-duration-ns")
						b.ReportMetric(workflowDurationsNs.Quantile(0.50), "p50-workflow-duration-ns")
					})
				}
			}
		}
	}
}
