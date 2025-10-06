// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"fmt"
	"math"
	"math/bits"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/influxdata/tdigest"
	"github.com/petenewcomb/psg-go"
	"github.com/petenewcomb/psg-go/internal/omnipool"
	"github.com/petenewcomb/psg-go/internal/trace"
	"github.com/petenewcomb/psg-go/psgfn"
	"github.com/petenewcomb/psg-go/psgopt"
	"github.com/stretchr/testify/assert"
)

type passthroughTestCombiner[T any] struct {
	t     *testing.T
	value T
}

func (c *passthroughTestCombiner[T]) Combine(ctx context.Context, value T, err error) (time.Time, error) {
	assert.NoError(c.t, err)
	c.value = value
	return time.Now(), nil
}

func (c *passthroughTestCombiner[T]) Flush(ctx context.Context) (T, error) {
	return c.value, nil
}

//nolint:thelper // not a test helper, but a factory function for creating a test combiner
func newPassthroughTestCombinerFactory[T any](t *testing.T) func() psgfn.Combiner[T, T] {
	return func() psgfn.Combiner[T, T] {
		return &passthroughTestCombiner[T]{t: t}
	}
}

func TestCombinerScatterNilTaskPanic(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()
	taskPool := psg.NewTaskPool(job)

	chk.PanicsWithValue("task function must be non-nil", func() {
		// Create a gather
		gatherOp := psg.NewGatherOp(func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		})

		// Create a combiner taskPool
		combinerPool := psg.NewCombinerPool(job)

		// Create a combine operation
		combineOp := psg.NewCombineOp(
			gatherOp,
			combinerPool,
			newPassthroughTestCombinerFactory[int](t),
		)

		// Should panic with nil task function
		_ = combineOp.Scatter(
			ctx,
			taskPool,
			nil, // Nil Task should panic
		)
	})
}

func TestCombinerScatterNilGatherPanic(t *testing.T) {
	ctx := context.Background()
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()

	assert.PanicsWithValue(t, "gather function must be non-nil", func() {
		psg.NewGatherOp[int](nil)
	})
}

func TestCombinerTryScatterNilTaskPanic(t *testing.T) {
	ctx := context.Background()
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()
	taskPool := psg.NewTaskPool(job, psgopt.WithMaxConcurrency(1))

	assert.PanicsWithValue(t, "task function must be non-nil", func() {
		gatherOp := psg.NewGatherOp(
			func(ctx context.Context, result int, err error) error {
				return nil
			},
		)
		_, _ = gatherOp.TryScatter(
			ctx,
			time.Time{},
			taskPool,
			nil, // Nil Task should panic
		)
	})
}

func TestCombinerScatterFromTask(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()
	job := psg.NewJob(ctx)
	defer job.CancelAndWait()
	taskPool := psg.NewTaskPool(job)

	gatherOp := psg.NewGatherOp(
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	)
	combinerPool := psg.NewCombinerPool(job)
	combineOp := psg.NewCombineOp(
		gatherOp,
		combinerPool,
		newPassthroughTestCombinerFactory[int](t),
	)
	err := combineOp.Scatter(
		ctx,
		taskPool,
		func(ctx context.Context) (int, error) {
			chk.PanicsWithValue(
				"Scatter called from task context but allowed only by top-level, gather, or combine context",
				func() {
					innerGatherOp := psg.NewGatherOp(
						func(ctx context.Context, result int, err error) error {
							chk.NoError(err)
							chk.Fail("should not get here")
							return nil
						},
					)
					chk.NoError(innerGatherOp.Scatter(
						ctx,
						taskPool,
						func(ctx context.Context) (int, error) {
							chk.Fail("should not get here")
							return 0, nil
						},
					))
				},
			)
			return 0, nil
		},
	)
	chk.NoError(err)
	chk.NoError(job.CloseAndGatherAll(ctx))
}

func TestCombinerTaskCanScatterToSubJob(t *testing.T) {
	chk := assert.New(t)
	ctx := context.Background()

	// Create parent job with task pool
	parentJob := psg.NewJob(ctx)
	defer parentJob.CancelAndWait()
	parentTaskPool := psg.NewTaskPool(parentJob)

	// Variable to track execution flow
	subJobTaskRan := false

	gatherOp := psg.NewGatherOp(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	combinerPool := psg.NewCombinerPool(parentJob)
	combineOp := psg.NewCombineOp(
		gatherOp,
		combinerPool,
		newPassthroughTestCombinerFactory[bool](t),
	)
	err := combineOp.Scatter(
		ctx,
		parentTaskPool,
		func(ctx context.Context) (bool, error) {
			// Create a sub-job inside the task
			subJob := psg.NewJob(ctx)
			defer subJob.CancelAndWait()
			subTaskPool := psg.NewTaskPool(subJob)

			// This should succeed - scattering a task to the sub-job's task pool
			gatherOp := psg.NewGatherOp(
				func(ctx context.Context, result bool, err error) error {
					chk.NoError(err)
					chk.True(result)
					return nil
				},
			)
			err := gatherOp.Scatter(
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
	chk := assert.New(t)
	ctx := context.Background()

	// Create parent job with task pool
	parentJob := psg.NewJob(ctx)
	defer parentJob.CancelAndWait()
	parentTaskPool := psg.NewTaskPool(parentJob)

	gatherOp := psg.NewGatherOp(
		func(ctx context.Context, result bool, err error) error {
			chk.NoError(err)
			chk.True(result)
			return nil
		},
	)
	combinerPool := psg.NewCombinerPool(parentJob)
	combineOp := psg.NewCombineOp(
		gatherOp,
		combinerPool,
		newPassthroughTestCombinerFactory[bool](t),
	)
	err := combineOp.Scatter(
		ctx,
		parentTaskPool,
		func(ctx context.Context) (bool, error) {
			// This should panic - attempting to scatter to the parent job's task pool
			// while inside a task of that same job
			innerGatherOp := psg.NewGatherOp(
				func(ctx context.Context, result bool, err error) error {
					chk.Fail("Should not get here - parent task pool gather should not run")
					return nil
				},
			)
			chk.PanicsWithValue(
				"Scatter called from task context but allowed only by top-level, gather, or combine context",
				func() {
					_ = innerGatherOp.Scatter(
						ctx,
						parentTaskPool,
						func(ctx context.Context) (bool, error) {
							chk.Fail("Should not get here - parent task pool task should not run")
							return false, nil
						},
					)
				},
			)

			return true, nil
		},
	)

	chk.NoError(err)
	chk.NoError(parentJob.CloseAndGatherAll(ctx))
}

type benchmarkTaskResult struct {
	Time                      time.Time
	Depth                     int
	CombineSubtaskBudget      int
	GatherSubtaskBudget       int
	CumulativeNominalDuration time.Duration
	Latency                   time.Duration
}

type benchmarkTask struct {
	startTime                 time.Time
	depth                     int
	combineSubtaskBudget      int
	gatherSubtaskBudget       int
	cumulativeNominalDuration time.Duration
	executeFn                 psgfn.Task[benchmarkTaskResult]
}

var benchmarkTaskPool = omnipool.For[benchmarkTask]()

func newBenchmarkTaskFn(
	startTime time.Time,
	depth, combineSubtaskBudget, gatherSubtaskBudget int,
	cumulativeNominalDuration time.Duration,
) psgfn.Task[benchmarkTaskResult] {
	task := benchmarkTaskPool.Get()
	task.startTime = startTime
	task.depth = depth
	task.combineSubtaskBudget = combineSubtaskBudget
	task.gatherSubtaskBudget = gatherSubtaskBudget
	task.cumulativeNominalDuration = cumulativeNominalDuration
	return task.executeFn
}

func (t *benchmarkTask) Init() {
	t.depth = -1
	t.executeFn = t.execute
}

func (t *benchmarkTask) Reset() {
	t.startTime = time.Time{}
	t.depth = -1
	t.cumulativeNominalDuration = 0
}

func (t *benchmarkTask) execute(context.Context) (benchmarkTaskResult, error) {
	now := time.Now()
	res := benchmarkTaskResult{
		Time:                      now,
		Latency:                   now.Sub(t.startTime),
		Depth:                     t.depth,
		CombineSubtaskBudget:      t.combineSubtaskBudget,
		GatherSubtaskBudget:       t.gatherSubtaskBudget,
		CumulativeNominalDuration: t.cumulativeNominalDuration,
	}

	benchmarkTaskPool.Put(t)
	return res, nil
}

type benchmarkCombinedResult struct {
	Time                         time.Time
	MaxDepth                     int
	CombineSubtaskBudget         int
	GatherSubtaskBudget          int
	MaxCumulativeNominalDuration time.Duration
	Count                        int
	TaskLatenciesNs              tdigest.CentroidList
	LatenciesNs                  tdigest.CentroidList
	DurationsNs                  tdigest.CentroidList
	DurationSum                  time.Duration
	DurationCount                int
	WorkflowLatenciesNs          tdigest.CentroidList
	MaxConcurrency               int
}

type benchmarkCombiner struct {
	firstCombineTime       time.Duration // since epoch
	testStartTime          *atomic.Int64 // time.Duration since epoch
	testEndTime            *atomic.Int64 // time.Duration since epoch
	cumulativeCombinerTime *atomic.Int64 // time.Duration
	abandonedTaskCount     *atomic.Int64
	simulateWorkFrom       func(t time.Time, d time.Duration)
	workloadDuration       time.Duration
	flushPeriod            time.Duration
	scatter                func(ctx context.Context, deadline time.Time, target psg.TaskPoolOrJob,
		task psgfn.Task[benchmarkTaskResult]) (bool, error)
	target    psg.TaskPoolOrJob
	newTaskFn func(startTime time.Time, depth, combineSubtaskBudget, gatherSubtaskBudget int,
		cumulativeNominalDuration time.Duration) psgfn.Task[benchmarkTaskResult]
	idealCombinesPerGather int

	maxDepth                     int
	combineSubtaskBudget         int
	gatherSubtaskBudget          int
	maxCumulativeNominalDuration time.Duration
	count                        int
	taskLatenciesNs              *tdigest.TDigest
	durationsNs                  *tdigest.TDigest
	durationSum                  time.Duration
	latenciesNs                  *tdigest.TDigest
	workflowLatenciesNs          *tdigest.TDigest
	maxConcurrency               int
}

var benchmarkCombinerPool = omnipool.For[benchmarkCombiner]()

var epoch = time.Now()

func newBenchmarkCombiner(
	testStartTime *atomic.Int64,
	testEndTime *atomic.Int64,
	cumulativeCombinerTime *atomic.Int64,
	abandonedTaskCount *atomic.Int64,
	simulateWorkFrom func(t time.Time, d time.Duration),
	workloadDuration time.Duration,
	flushPeriod time.Duration,
	scatter func(ctx context.Context, deadline time.Time, target psg.TaskPoolOrJob,
		task psgfn.Task[benchmarkTaskResult]) (bool, error),
	target psg.TaskPoolOrJob,
	newTaskFn func(startTime time.Time, depth, combineSubtaskBudget, gatherSubtaskBudget int,
		cumulativeNominalDuration time.Duration) psgfn.Task[benchmarkTaskResult],
	idealCombinesPerGather int,
) *benchmarkCombiner {
	c := benchmarkCombinerPool.Get()
	c.firstCombineTime = time.Since(epoch)
	c.testStartTime = testStartTime
	c.testEndTime = testEndTime
	c.cumulativeCombinerTime = cumulativeCombinerTime
	c.abandonedTaskCount = abandonedTaskCount
	c.simulateWorkFrom = simulateWorkFrom
	c.workloadDuration = workloadDuration
	c.flushPeriod = flushPeriod
	c.scatter = scatter
	c.target = target
	c.newTaskFn = newTaskFn
	c.idealCombinesPerGather = idealCombinesPerGather
	return c
}

func (c *benchmarkCombiner) Combine(ctx context.Context, taskRes benchmarkTaskResult, err error) (time.Time, error) {
	combineStartTime := time.Now()
	latency := combineStartTime.Sub(taskRes.Time)

	// Front-load all measurement work before simulated work
	c.maxDepth = max(c.maxDepth, taskRes.Depth)
	c.maxCumulativeNominalDuration = max(c.maxCumulativeNominalDuration, taskRes.CumulativeNominalDuration)
	if c.count == 0 {
		c.taskLatenciesNs = newTDigest()
		c.latenciesNs = newTDigest()
		c.durationsNs = newTDigest()
		c.workflowLatenciesNs = newTDigest()
		c.maxConcurrency = max(c.maxConcurrency, int(c.cumulativeCombinerTime.Add(1)))
	}
	c.count++

	flushDeadline := combineStartTime
	if c.count < c.idealCombinesPerGather {
		flushDeadline = flushDeadline.Add(c.flushPeriod)
	}

	c.taskLatenciesNs.Add(float64(taskRes.Latency.Nanoseconds()), 1.0)
	c.latenciesNs.Add(float64(latency.Nanoseconds()), 1.0)
	// Workflow latency: scatter to combine start (queueing time)
	c.workflowLatenciesNs.Add(float64((taskRes.Latency + latency).Nanoseconds()), 1.0)

	c.simulateWorkFrom(combineStartTime, c.workloadDuration)

	// Don't include scatter time in work duration
	c.combineSubtaskBudget += taskRes.CombineSubtaskBudget
	c.gatherSubtaskBudget += taskRes.GatherSubtaskBudget
	switch {
	case c.combineSubtaskBudget < 0:
		panic("combineSubtaskBudget is negative")
	case c.gatherSubtaskBudget < 0:
		panic("gatherSubtaskBudget is negative")
	case c.combineSubtaskBudget > 0:
		scatters := bits.Len(uint(c.combineSubtaskBudget))
		c.combineSubtaskBudget -= scatters
		shares := scatters

		gatherSubtaskBudget := c.gatherSubtaskBudget
		gatherSubtaskBudgetPerScatter := 0
		if gatherSubtaskBudget > 0 {
			// Save some subtask budget for flushing
			shares++
			gatherSubtaskBudgetPerScatter = max(1, gatherSubtaskBudget/shares)
		}

		combineSubtaskBudget := c.combineSubtaskBudget
		combineSubtaskBudgetPerScatter := max(1, combineSubtaskBudget/shares)
		combineSubtaskBudget = min(combineSubtaskBudget, scatters*combineSubtaskBudgetPerScatter)
		c.combineSubtaskBudget -= combineSubtaskBudget

		if gatherSubtaskBudget > 0 {
			if c.combineSubtaskBudget > 0 {
				// If we have combine subtask budget to flush, we must also
				// reserve budget for at least one gather subtask
				gatherSubtaskBudget--
			}
			gatherSubtaskBudgetPerScatter = max(1, gatherSubtaskBudget/shares)
			gatherSubtaskBudget = min(gatherSubtaskBudget, scatters*gatherSubtaskBudgetPerScatter)
			c.gatherSubtaskBudget -= gatherSubtaskBudget
		}

		for range scatters {
			scatterCombineSubtaskBudget := min(combineSubtaskBudget, combineSubtaskBudgetPerScatter)
			combineSubtaskBudget -= scatterCombineSubtaskBudget
			scatterGatherSubtaskBudget := min(gatherSubtaskBudget, gatherSubtaskBudgetPerScatter)
			gatherSubtaskBudget -= scatterGatherSubtaskBudget
			for {
				deadline := time.Now().Add(c.workloadDuration)
				taskFn := c.newTaskFn(
					time.Now(),
					taskRes.Depth+1,
					scatterCombineSubtaskBudget,
					scatterGatherSubtaskBudget,
					taskRes.CumulativeNominalDuration+c.workloadDuration,
				)
				ok, err := c.scatter(ctx, deadline, c.target, taskFn)
				if !ok {
					c.abandonedTaskCount.Add(1)
					_, _ = taskFn(ctx) // let the task pool itself
				}
				if err != nil {
					return flushDeadline, err
				}
				if ok {
					break
				}
			}
		}
	}

	duration := time.Since(combineStartTime)
	c.durationsNs.Add(float64(duration.Nanoseconds()), 1.0)

	c.durationSum += duration

	return flushDeadline, err
}

func (c *benchmarkCombiner) Flush(ctx context.Context) (benchmarkCombinedResult, error) {
	now := time.Now()
	res := benchmarkCombinedResult{
		Time:                         now,
		MaxDepth:                     c.maxDepth,
		CombineSubtaskBudget:         c.combineSubtaskBudget,
		GatherSubtaskBudget:          c.gatherSubtaskBudget,
		MaxCumulativeNominalDuration: c.maxCumulativeNominalDuration + c.workloadDuration + c.flushPeriod,
		Count:                        c.count,
		TaskLatenciesNs:              copyCentroidList(c.taskLatenciesNs),
		LatenciesNs:                  copyCentroidList(c.latenciesNs),
		DurationsNs:                  copyCentroidList(c.durationsNs),
		DurationSum:                  c.durationSum,
		DurationCount:                c.count,
		WorkflowLatenciesNs:          copyCentroidList(c.workflowLatenciesNs),
		MaxConcurrency:               c.maxConcurrency,
	}

	poolTDigest(&c.taskLatenciesNs)
	poolTDigest(&c.latenciesNs)
	poolTDigest(&c.durationsNs)
	poolTDigest(&c.workflowLatenciesNs)

	testStartTime := time.Duration(c.testStartTime.Load())
	if testStartTime > 0 {
		testEndTime := time.Duration(c.testEndTime.Load())
		combinerStartTime := c.firstCombineTime
		if testEndTime == 0 || combinerStartTime < testEndTime {
			if combinerStartTime < testStartTime {
				combinerStartTime = testStartTime
			}
			combinerEndTime := testEndTime
			if combinerEndTime == 0 {
				combinerEndTime = time.Since(epoch)
			}
			c.cumulativeCombinerTime.Add(int64(combinerEndTime - combinerStartTime))
		}
	}
	benchmarkCombinerPool.Put(c)

	return res, nil
}

var tdigestPool = omnipool.For[tdigest.TDigest]()

func newTDigest() *tdigest.TDigest {
	return tdigestPool.Get()
}
func poolTDigest(t **tdigest.TDigest) {
	if *t != nil {
		(*t).Reset()
		tdigestPool.Put(*t)
		*t = nil
	}
}

var centroidListPool = omnipool.ForSlice(tdigest.CentroidList(nil))

func newCentroidList(c ...tdigest.Centroid) tdigest.CentroidList {
	cl := centroidListPool.Get()
	cl = append(cl, c...)
	return cl
}
func copyCentroidList(t *tdigest.TDigest) tdigest.CentroidList {
	cl := centroidListPool.Get()
	cl = t.Centroids(cl)
	return cl
}
func poolCentroidList(cl tdigest.CentroidList) {
	centroidListPool.Put(cl)
}

// BenchmarkCombinerThroughput measures the maximum throughput of processing
// a continuous stream of data with gather-only vs. combiner approaches
func BenchmarkCombinerThroughput(b *testing.B) {
	combinerLimits := []int{
		-1, // combine, unlimited
		0,  // gather-only
		1, 2, 3, 4,
	}
	availableCores := runtime.GOMAXPROCS(-1)
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
						ctx, cancel := context.WithCancelCause(context.Background())
						defer cancel(nil)

						job := psg.NewJob(ctx)
						defer func() {
							job.CancelAndWait()
						}()

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

						var testStartTime atomic.Int64          // time.Duration since epoch
						var testEndTime atomic.Int64            // time.Duration since epoch
						var cumulativeCombinerTime atomic.Int64 // time.Duration

						var abandonedTaskCount atomic.Int64
						maxDepth := 0
						var maxCumulativeNominalDuration time.Duration

						var newTaskFn func(startTime time.Time, depth, combineSubtaskBudget, gatherSubtaskBudget int,
							cumulativeNominalDuration time.Duration) psgfn.Task[benchmarkTaskResult]
						var scatter func(ctx context.Context, deadline time.Time, target psg.TaskPoolOrJob,
							task psgfn.Task[benchmarkTaskResult]) (bool, error)

						gatherFn := func(ctx context.Context, combineRes benchmarkCombinedResult, err error) error {
							if err != nil {
								return err
							}

							gatherStartTime := time.Now()
							gatherLatencyNs := float64(gatherStartTime.Sub(combineRes.Time).Nanoseconds())

							// Front-load all measurement work before simulated work
							gatherLatenciesNs.Add(gatherLatencyNs, 1.0)
							totalTasksGathered += combineRes.Count
							taskLatenciesNs.AddCentroidList(combineRes.TaskLatenciesNs)
							combineLatenciesNs.AddCentroidList(combineRes.LatenciesNs)
							combineDurationsNs.AddCentroidList(combineRes.DurationsNs)
							combineDurationSum += combineRes.DurationSum
							combineDurationCount += combineRes.DurationCount
							combineWorkflowLatenciesNs.AddCentroidList(combineRes.WorkflowLatenciesNs)

							maxDepth = max(maxDepth, combineRes.MaxDepth)
							maxCumulativeNominalDuration = max(maxCumulativeNominalDuration, combineRes.MaxCumulativeNominalDuration)

							// Add gather latency to workflow latencies to get scatter-to-gather-start time
							for i := range combineRes.WorkflowLatenciesNs {
								combineRes.WorkflowLatenciesNs[i].Mean += gatherLatencyNs
							}
							workflowLatenciesNs.AddCentroidList(combineRes.WorkflowLatenciesNs)

							combineCounts.Add(float64(combineRes.Count), 1.0)

							simulateWorkFrom(gatherStartTime, workloadDuration)

							combineSubtaskBudget := combineRes.CombineSubtaskBudget
							gatherSubtaskBudget := combineRes.GatherSubtaskBudget
							switch {
							case combineSubtaskBudget < 0:
								panic("combineSubtaskBudget is negative")
							case gatherSubtaskBudget < 0:
								panic("gatherSubtaskBudget is negative")
							case gatherSubtaskBudget == 0:
								if combineSubtaskBudget != 0 {
									panic("gatherSubtaskBudget is zero, but combineSubtaskBudget is non-zero")
								}
							case gatherSubtaskBudget > 0:
								scatters := bits.Len(uint(gatherSubtaskBudget))
								gatherSubtaskBudget -= scatters
								combineSubtaskBudgetPerScatter := max(1, combineSubtaskBudget/scatters)
								gatherSubtaskBudgetPerScatter := max(1, gatherSubtaskBudget/scatters)
								for range scatters {
									scatterCombineSubtaskBudget := min(combineSubtaskBudget, combineSubtaskBudgetPerScatter)
									scatterGatherSubtaskBudget := min(gatherSubtaskBudget, gatherSubtaskBudgetPerScatter)
									combineSubtaskBudget -= scatterCombineSubtaskBudget
									gatherSubtaskBudget -= scatterGatherSubtaskBudget
									for {
										deadline := time.Now().Add(workloadDuration)
										taskFn := newTaskFn(
											time.Now(),
											combineRes.MaxDepth+1,
											scatterCombineSubtaskBudget,
											scatterGatherSubtaskBudget,
											combineRes.MaxCumulativeNominalDuration+workloadDuration,
										)
										ok, err := scatter(ctx, deadline, job, taskFn)
										if !ok {
											abandonedTaskCount.Add(1)
											_, _ = taskFn(ctx) // let the task pool itself
										}
										if err != nil {
											return err
										}
										if ok {
											break
										}
									}
								}
							}

							gatherDuration := time.Since(gatherStartTime)
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

						gatherFnAdapter := func(ctx context.Context, taskRes benchmarkTaskResult, err error) error {
							now := time.Now()
							taskLatencyNsCentroid := tdigest.Centroid{
								Mean:   float64(now.Sub(taskRes.Time).Nanoseconds()),
								Weight: 1.0,
							}
							combinedRes := benchmarkCombinedResult{
								Time:                         now,
								MaxDepth:                     taskRes.Depth,
								MaxCumulativeNominalDuration: taskRes.CumulativeNominalDuration,
								Count:                        1,
								TaskLatenciesNs:              newCentroidList(taskLatencyNsCentroid),
								LatenciesNs:                  newCentroidList(taskLatencyNsCentroid),
								DurationsNs:                  newCentroidList(),
								WorkflowLatenciesNs:          newCentroidList(taskLatencyNsCentroid),
							}
							return gatherFn(ctx, combinedRes, err)
						}

						idealCombinesPerGather := int(math.Round(float64(flushPeriod) / float64(workloadDuration)))

						// Setup processing - either gather-only or with combiner
						if combinerLimit == 0 {
							scatter = func(ctx context.Context, deadline time.Time, target psg.TaskPoolOrJob,
								task psgfn.Task[benchmarkTaskResult]) (bool, error) {
								// Tests to make sure that NewGatherOp does not incur allocation overhead
								gatherOp := psg.NewGatherOp(gatherFnAdapter)
								if deadline.IsZero() {
									return true, gatherOp.Scatter(ctx, target, task)
								}
								return gatherOp.TryScatter(ctx, deadline, target, task)
							}
						} else {
							combinerPool := psg.NewCombinerPool(job, psgopt.WithMaxConcurrency(combinerLimit))
							combinerFactory := func() psgfn.Combiner[benchmarkTaskResult, benchmarkCombinedResult] {
								return newBenchmarkCombiner(
									&testStartTime,
									&testEndTime,
									&cumulativeCombinerTime,
									&abandonedTaskCount,
									simulateWorkFrom,
									workloadDuration,
									flushPeriod,
									scatter,
									job,
									newTaskFn,
									idealCombinesPerGather,
								)
							}

							gatherOp := psg.NewGatherOp(gatherFn)
							combineOp := psg.NewCombineOp(gatherOp, combinerPool, combinerFactory)
							defer combineOp.Close()

							scatter = func(ctx context.Context, deadline time.Time, target psg.TaskPoolOrJob,
								task psgfn.Task[benchmarkTaskResult]) (bool, error) {
								localCombineOp := combineOp
								if idealCombinesPerGather == 1 {
									// Tests to make sure that NewCombineOp does not incur allocation overhead
									localCombineOp = psg.NewCombineOp(gatherOp, combinerPool, combinerFactory)
									defer localCombineOp.Close()
								}
								if deadline.IsZero() {
									return true, localCombineOp.Scatter(ctx, target, task)
								}
								return localCombineOp.TryScatter(ctx, deadline, target, task)
							}
						}

						var totalTasksLaunched atomic.Int64
						newTaskFn = func(startTime time.Time, depth, combineSubtaskBudget, gatherSubtaskBudget int,
							cumulativeNominalDuration time.Duration) psgfn.Task[benchmarkTaskResult] {
							totalTasksLaunched.Add(1)
							return newBenchmarkTaskFn(startTime, depth, combineSubtaskBudget, gatherSubtaskBudget, cumulativeNominalDuration)
						}

						opTasksGatheredOrigin := totalTasksGathered
						totalTopLevelTasks := 0
						op := func() int {
							for {
								deadline := time.Now().Add(1 * time.Millisecond)
								taskFn := newTaskFn(time.Now(), 0, 12, 3, 0)
								ok, err := scatter(ctx, deadline, job, taskFn)
								if ok {
									totalTopLevelTasks++
								} else {
									abandonedTaskCount.Add(1)
									_, _ = taskFn(ctx) // let the task pool itself
								}
								if err != nil {
									b.Fatalf("Error: %v", err)
								}

								if totalTasksGathered != opTasksGatheredOrigin {
									tasksGathered := totalTasksGathered - opTasksGatheredOrigin
									opTasksGatheredOrigin = totalTasksGathered
									return tasksGathered
								}
							}
						}

						warmupStartTime := time.Now()
						for time.Since(warmupStartTime) < flushPeriod {
							op()
						}

						topLevelTasksOrigin := totalTopLevelTasks
						tasksGatheredOrigin := totalTasksGathered
						maxDepth = 0
						maxCumulativeNominalDuration = 0
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
						testStartTime.Store(int64(time.Since(epoch)))
						func() {
							defer trace.StartRegion(ctx, "BenchmarkCombinerThroughput.Loop").End()
							for b.Loop() {
								op()
							}
						}()
						testEndTime.Store(int64(time.Since(epoch)))

						// We purposefully do not run job.CloseAndGatherAll
						// before capturing results to avoid inflating
						// overallSum with data gathered outside the
						// benchmarking loop.

						topLevelTasks := float64(totalTopLevelTasks - topLevelTasksOrigin)
						tasksGathered := float64(totalTasksGathered - tasksGatheredOrigin)

						// Now call CloseAndGatherAll to make sure nothing was lost.
						assert.NoError(b, job.CloseAndGatherAll(ctx))
						assert.Equal(b, totalTasksLaunched.Load(), int64(totalTasksGathered)+abandonedTaskCount.Load())

						avgCombinerConcurrency := float64(cumulativeCombinerTime.Load()) /
							float64(testEndTime.Load()-testStartTime.Load())

						b.ReportAllocs()

						// Throughput - the primary metric for this benchmark
						throughput := tasksGathered / b.Elapsed().Seconds()
						b.ReportMetric(throughput, "tasks/sec")

						// Tasks per operation - needed to normalize allocs/op and B/op
						tasksPerOp := tasksGathered / float64(b.N)
						b.ReportMetric(tasksPerOp, "tasks/op")

						// Top Level tasks per operation
						b.ReportMetric(tasksGathered/topLevelTasks, "tasks/top-level-task")

						b.ReportMetric(float64(maxDepth), "max-depth")

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

						b.ReportMetric(float64(maxCumulativeNominalDuration.Nanoseconds()), "max-nominal-workflow-duration-ns")

						b.ReportMetric(workflowLatenciesNs.Quantile(0.99), "p99-workflow-latency-ns")
						b.ReportMetric(workflowLatenciesNs.Quantile(0.50), "p50-workflow-latency-ns")

						if combinerLimit != 0 {
							b.ReportMetric(avgCombinerConcurrency, "avg-combiner-concurrency")
						}
					})
				}
			}
		}
	}
}
