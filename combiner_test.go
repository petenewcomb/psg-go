// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go"
	"github.com/stretchr/testify/require"
)

func TestCombinerScatterNilTaskFuncPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)
	defer job.CancelAndWait()

	chk.PanicsWithValue("task function must be non-nil", func() {
		gather := psg.NewCombiner(ctx, 1,
			func() psg.CombinerFunc[int, int] {
				return func(ctx context.Context, flush bool, value int, err error) (bool, int, error) {
					chk.NoError(err)
					return true, 0, nil
				}
			},
			func(ctx context.Context, result int, err error) error {
				chk.NoError(err)
				return nil
			},
		)
		_ = gather.Scatter(
			ctx,
			pool,
			nil, // Nil TaskFunc should panic
		)
	})
}

func TestCombinerScatterNilGatherFuncPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)
	defer job.CancelAndWait()

	chk.PanicsWithValue("gather function must be non-nil", func() {
		psg.NewGather[int](nil)
	})
}

func TestCombinerScatterPoolNotBoundPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create a pool but don't associate it with a job
	pool := psg.NewPool(1)

	chk.PanicsWithValue("pool not bound to a job", func() {
		gather := psg.NewGather(
			func(ctx context.Context, result int, err error) error {
				return nil
			},
		)
		_ = gather.Scatter(
			ctx,
			pool,
			func(ctx context.Context) (int, error) {
				return 0, nil
			},
		)
	})
}

func TestCombinerTryScatterNilTaskFuncPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)
	defer job.CancelAndWait()

	chk.PanicsWithValue("task function must be non-nil", func() {
		gather := psg.NewGather(
			func(ctx context.Context, result int, err error) error {
				return nil
			},
		)
		_, _ = gather.TryScatter(
			ctx,
			pool,
			nil, // Nil TaskFunc should panic
		)
	})
}

func TestCombinerTryScatterPoolNotBoundPanic(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()

	// Create a pool but don't associate it with a job
	pool := psg.NewPool(1)

	chk.PanicsWithValue("pool not bound to a job", func() {
		gather := psg.NewGather(
			func(ctx context.Context, result int, err error) error {
				return nil
			},
		)
		_, _ = gather.TryScatter(
			ctx,
			pool,
			func(ctx context.Context) (int, error) {
				return 0, nil
			},
		)
	})
}

func TestCombinerScatterFromTask(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)

	gather := psg.NewGather(
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	)
	err := gather.Scatter(
		ctx,
		pool,
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
					pool,
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

	// Create parent job with pool
	parentPool := psg.NewPool(1)
	parentJob := psg.NewJob(ctx, parentPool)
	defer parentJob.CancelAndWait()

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
		parentPool,
		func(ctx context.Context) (bool, error) {
			// Create a sub-job inside the task
			subPool := psg.NewPool(1)
			subJob := psg.NewJob(ctx, subPool)
			defer subJob.CancelAndWait()

			// This should succeed - scattering a task to the sub-job's pool
			gather := psg.NewGather(
				func(ctx context.Context, result bool, err error) error {
					chk.NoError(err)
					chk.True(result)
					return nil
				},
			)
			err := gather.Scatter(
				ctx,
				subPool,
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

	// Create parent job with pool
	parentPool := psg.NewPool(1)
	parentJob := psg.NewJob(ctx, parentPool)
	defer parentJob.CancelAndWait()

	gather := psg.NewGather(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	err := gather.Scatter(
		ctx,
		parentPool,
		func(ctx context.Context) (bool, error) {
			// This should panic - attempting to scatter to the parent job's pool
			// while inside a task of that same job
			innerGather := psg.NewGather(
				func(ctx context.Context, result bool, err error) error {
					chk.Fail("Should not get here - parent pool gather should not run")
					return nil
				},
			)
			chk.PanicsWithValue("Scatter called from within TaskFunc; move call to GatherFunc instead", func() {
				_ = innerGather.Scatter(
					ctx,
					parentPool,
					func(ctx context.Context) (bool, error) {
						chk.Fail("Should not get here - parent pool task should not run")
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
	// Run with different worker configurations
	usableCores := runtime.NumCPU() - 1 // reserve one for the main thread
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
				for _, combinerLimit := range []int{
					-1, // direct
					0,  // gather-only
					1, 2, 3, 4, 8,
				} {

					// Only need to run direct and gather-only once to cover all flush periods
					if combinerLimit < 1 && fpi > 0 {
						continue
					}

					if workload == "processing" && combinerLimit > usableCores {
						// Skip this configuration since the hardware is not capable
						// of running it without CPU contention
						break
					}

					var method string
					switch {
					case combinerLimit < 0:
						method = "direct"
					case combinerLimit == 0:
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

						pool := psg.NewPool(-1)
						job := psg.NewJob(ctx, pool)
						defer job.CancelAndWait()

						type aggregatedResult struct {
							Time        time.Time
							Count       int64
							DurationSum time.Duration
							LatencySum  time.Duration
							LatencyMax  time.Duration
						}
						var overallResult aggregatedResult
						gatherFunc := func(ctx context.Context, value aggregatedResult, err error) error {
							if err != nil {
								return err
							}
							workStartTime := time.Now()
							simulateWork(workloadDuration)
							overallResult.Time = time.Now()
							overallResult.Count += value.Count
							overallResult.DurationSum += value.DurationSum + overallResult.Time.Sub(workStartTime)
							timeSinceValue := overallResult.Time.Sub(value.Time)
							overallResult.LatencySum += time.Duration(value.Count)*timeSinceValue + value.LatencySum
							overallResult.LatencyMax = max(overallResult.LatencyMax, value.LatencyMax+timeSinceValue)
							return nil
						}

						gatherFuncAdapter := func(ctx context.Context, value time.Time, err error) error {
							aggRes := aggregatedResult{
								Time:  value,
								Count: 1,
							}
							return gatherFunc(ctx, aggRes, err)
						}

						// Setup processing - either gather-only or with combiner
						var scatter func(ctx context.Context, pool *psg.Pool, task psg.TaskFunc[time.Time]) error
						switch {
						case combinerLimit < 0:
							scatter = func(ctx context.Context, pool *psg.Pool, task psg.TaskFunc[time.Time]) error {
								value, err := task(ctx)
								return gatherFuncAdapter(ctx, value, err)
							}
						case combinerLimit == 0:
							scatter = psg.NewGather(gatherFuncAdapter).Scatter
						default:
							newCombinerFunc := func() psg.CombinerFunc[time.Time, aggregatedResult] {
								var combinedResult aggregatedResult
								nextFlushTime := time.Now().Add(flushPeriod)
								return func(ctx context.Context, flush bool, value time.Time, err error) (bool, aggregatedResult, error) {
									if err != nil {
										return true, aggregatedResult{}, err
									}

									if !flush {
										workStartTime := time.Now()
										simulateWork(workloadDuration)
										combinedResult.Time = time.Now()
										combinedResult.Count++
										combinedResult.DurationSum += combinedResult.Time.Sub(workStartTime)
										timeSinceValue := combinedResult.Time.Sub(value)
										combinedResult.LatencySum += timeSinceValue
										combinedResult.LatencyMax = max(combinedResult.LatencyMax, timeSinceValue)
									}

									now := time.Now()
									if flush || now.After(nextFlushTime) {
										resultToFlush := combinedResult
										combinedResult = aggregatedResult{}
										nextFlushTime = now.Add(flushPeriod)
										return true, resultToFlush, nil
									}

									return false, aggregatedResult{}, nil
								}
							}

							combiner := psg.NewCombiner(ctx, combinerLimit, newCombinerFunc, gatherFunc)
							scatter = func(ctx context.Context, pool *psg.Pool, task psg.TaskFunc[time.Time]) error {
								if err := combiner.Scatter(ctx, pool, task); err != nil {
									return err
								}
								return nil
							}
						}

						var tasksRun atomic.Int64
						taskFunc := func(context.Context) (time.Time, error) {
							tasksRun.Add(1)
							return time.Now(), nil
						}

						var tasksLaunched int64
						op := func() {
							if err := scatter(ctx, pool, taskFunc); err != nil {
								b.Fatalf("Error: %v", err)
							}
							tasksLaunched++
						}

						// Warmup
						warmupEnd := time.Now().Add(10 * workloadDuration)
						for time.Now().Before(warmupEnd) {
							op()
						}

						tasksLaunchedOrigin := tasksLaunched
						tasksRunOrigin := tasksRun.Load()
						overallResultOrigin := overallResult
						overallResult.LatencyMax = 0
						for b.Loop() {
							oldCount := overallResult.Count
							for overallResult.Count == oldCount {
								op()
							}
						}

						// We purposefully do not run job.CloseAndGatherAll
						// here, to avoid inflating overallSum with data
						// gathered outside the benchmarking loop.

						tasksLaunched -= tasksLaunchedOrigin
						tasksRun.Add(int64(-tasksRunOrigin))
						overallResult.Count -= overallResultOrigin.Count
						overallResult.DurationSum -= overallResultOrigin.DurationSum
						overallResult.LatencySum -= overallResultOrigin.LatencySum

						b.ReportAllocs()
						b.ReportMetric(float64(tasksLaunched)/float64(b.N), "launched/op")
						b.ReportMetric(float64(tasksLaunched-overallResult.Count)/float64(b.N), "canceled/op")
						b.ReportMetric(float64(tasksRun.Load())/float64(b.N), "run/op")
						b.ReportMetric(float64(int64(tasksLaunched)-tasksRun.Load())/float64(b.N), "dropped/op")
						b.ReportMetric(float64(overallResult.Count)/float64(b.N), "completed/op")
						b.ReportMetric(float64(overallResult.Count)/b.Elapsed().Seconds(), "completed/s")
						b.ReportMetric(float64(overallResult.LatencyMax.Nanoseconds()), "max-latency-ns")
						b.ReportMetric(float64(overallResult.LatencySum.Nanoseconds())/float64(b.N), "avg-latency-ns/op")
						b.ReportMetric(float64(overallResult.DurationSum.Nanoseconds())/float64(b.N), "avg-duration-ns/op")
					})
				}
			}
		}
	}
}
