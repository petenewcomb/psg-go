//go:build psg_wave3_legacy_bench
// +build psg_wave3_legacy_bench

// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// This file holds the Wave-2-era benchmark suite for the funnel.
// Wave 3 reshaped streampool.Task / Skimmer / Funnel enough that the benchmark
// requires a deliberate redesign (see docs/plan/REFACTOR_PLAN.md: funnel-
// benchmark requirements session). To keep Wave 3 focused, the entire
// benchmark is gated behind the `psg_wave3_legacy_bench` build tag and
// is NOT compiled by default. Restore by either porting it to the new
// API or removing the build tag.

package streampool_test

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
	"github.com/petenewcomb/streampool"
	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/trace"

	"github.com/stretchr/testify/assert"
)

type benchmarkTaskResult struct {
	Time                      time.Time
	Depth                     int
	FunnelSubtaskBudget       int
	SkimSubtaskBudget         int
	CumulativeNominalDuration time.Duration
	Latency                   time.Duration
}

type benchmarkTask struct {
	startTime                 time.Time
	depth                     int
	funnelSubtaskBudget       int
	skimSubtaskBudget         int
	cumulativeNominalDuration time.Duration
	executeFn                 streampool.Task[benchmarkTaskResult]
}

var benchmarkTaskPool = omnipool.For[benchmarkTask]()

func newBenchmarkTaskFn(
	startTime time.Time,
	depth, funnelSubtaskBudget, skimSubtaskBudget int,
	cumulativeNominalDuration time.Duration,
) streampool.Task[benchmarkTaskResult] {
	task := benchmarkTaskPool.Get()
	task.startTime = startTime
	task.depth = depth
	task.funnelSubtaskBudget = funnelSubtaskBudget
	task.skimSubtaskBudget = skimSubtaskBudget
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
		FunnelSubtaskBudget:       t.funnelSubtaskBudget,
		SkimSubtaskBudget:         t.skimSubtaskBudget,
		CumulativeNominalDuration: t.cumulativeNominalDuration,
	}

	benchmarkTaskPool.Put(t)
	return res, nil
}

type benchmarkFunneldResult struct {
	Time                         time.Time
	MaxDepth                     int
	FunnelSubtaskBudget          int
	SkimSubtaskBudget            int
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

type benchmarkFunnel struct {
	firstFunnelTime      time.Duration // since epoch
	testStartTime        *atomic.Int64 // time.Duration since epoch
	testEndTime          *atomic.Int64 // time.Duration since epoch
	cumulativeFunnelTime *atomic.Int64 // time.Duration
	abandonedTaskCount   *atomic.Int64
	simulateWorkFrom     func(t time.Time, d time.Duration)
	workloadDuration     time.Duration
	flushPeriod          time.Duration
	scatter              func(ctx context.Context, deadline time.Time, target streampool.TaskPoolOrJob,
		task streampool.Task[benchmarkTaskResult]) (bool, error)
	target    streampool.TaskPoolOrJob
	newTaskFn func(startTime time.Time, depth, funnelSubtaskBudget, skimSubtaskBudget int,
		cumulativeNominalDuration time.Duration) streampool.Task[benchmarkTaskResult]
	idealFunnelsPerSkim int

	// Downstream sink captured for Submit-on-Flush (Wave 2 reshape).
	skimmer streampool.Skimmer[benchmarkFunneldResult]
	job     *streampool.Wave

	maxDepth                     int
	funnelSubtaskBudget          int
	skimSubtaskBudget            int
	maxCumulativeNominalDuration time.Duration
	count                        int
	taskLatenciesNs              *tdigest.TDigest
	durationsNs                  *tdigest.TDigest
	durationSum                  time.Duration
	latenciesNs                  *tdigest.TDigest
	workflowLatenciesNs          *tdigest.TDigest
	maxConcurrency               int
}

var benchmarkFunnelPool = omnipool.For[benchmarkFunnel]()

var epoch = time.Now()

func newBenchmarkFunnel(
	testStartTime *atomic.Int64,
	testEndTime *atomic.Int64,
	cumulativeFunnelTime *atomic.Int64,
	abandonedTaskCount *atomic.Int64,
	simulateWorkFrom func(t time.Time, d time.Duration),
	workloadDuration time.Duration,
	flushPeriod time.Duration,
	scatter func(ctx context.Context, deadline time.Time, target streampool.TaskPoolOrJob,
		task streampool.Task[benchmarkTaskResult]) (bool, error),
	target streampool.TaskPoolOrJob,
	newTaskFn func(startTime time.Time, depth, funnelSubtaskBudget, skimSubtaskBudget int,
		cumulativeNominalDuration time.Duration) streampool.Task[benchmarkTaskResult],
	idealFunnelsPerSkim int,
	skimmer streampool.Skimmer[benchmarkFunneldResult],
	job *streampool.Wave,
) *benchmarkFunnel {
	c := benchmarkFunnelPool.Get()
	c.firstFunnelTime = time.Since(epoch)
	c.testStartTime = testStartTime
	c.testEndTime = testEndTime
	c.cumulativeFunnelTime = cumulativeFunnelTime
	c.abandonedTaskCount = abandonedTaskCount
	c.simulateWorkFrom = simulateWorkFrom
	c.workloadDuration = workloadDuration
	c.flushPeriod = flushPeriod
	c.scatter = scatter
	c.target = target
	c.newTaskFn = newTaskFn
	c.idealFunnelsPerSkim = idealFunnelsPerSkim
	c.skimmer = skimmer
	c.job = job
	return c
}

func (c *benchmarkFunnel) Accumulate(ctx context.Context, taskRes benchmarkTaskResult, err error) (time.Time, error) {
	funnelStartTime := time.Now()
	latency := funnelStartTime.Sub(taskRes.Time)

	// Front-load all measurement work before simulated work
	c.maxDepth = max(c.maxDepth, taskRes.Depth)
	c.maxCumulativeNominalDuration = max(c.maxCumulativeNominalDuration, taskRes.CumulativeNominalDuration)
	if c.count == 0 {
		c.taskLatenciesNs = newTDigest()
		c.latenciesNs = newTDigest()
		c.durationsNs = newTDigest()
		c.workflowLatenciesNs = newTDigest()
		c.maxConcurrency = max(c.maxConcurrency, int(c.cumulativeFunnelTime.Add(1)))
	}
	c.count++

	flushDeadline := funnelStartTime
	if c.count < c.idealFunnelsPerSkim {
		flushDeadline = flushDeadline.Add(c.flushPeriod)
	}

	c.taskLatenciesNs.Add(float64(taskRes.Latency.Nanoseconds()), 1.0)
	c.latenciesNs.Add(float64(latency.Nanoseconds()), 1.0)
	// Workflow latency: scatter to funnel start (queueing time)
	c.workflowLatenciesNs.Add(float64((taskRes.Latency + latency).Nanoseconds()), 1.0)

	c.simulateWorkFrom(funnelStartTime, c.workloadDuration)

	// Don't include scatter time in work duration
	c.funnelSubtaskBudget += taskRes.FunnelSubtaskBudget
	c.skimSubtaskBudget += taskRes.SkimSubtaskBudget
	switch {
	case c.funnelSubtaskBudget < 0:
		panic("funnelSubtaskBudget is negative")
	case c.skimSubtaskBudget < 0:
		panic("skimSubtaskBudget is negative")
	case c.funnelSubtaskBudget > 0:
		scatters := bits.Len(uint(c.funnelSubtaskBudget))
		c.funnelSubtaskBudget -= scatters
		shares := scatters

		skimSubtaskBudget := c.skimSubtaskBudget
		skimSubtaskBudgetPerScatter := 0
		if skimSubtaskBudget > 0 {
			// Save some subtask budget for flushing
			shares++
			skimSubtaskBudgetPerScatter = max(1, skimSubtaskBudget/shares)
		}

		funnelSubtaskBudget := c.funnelSubtaskBudget
		funnelSubtaskBudgetPerScatter := max(1, funnelSubtaskBudget/shares)
		funnelSubtaskBudget = min(funnelSubtaskBudget, scatters*funnelSubtaskBudgetPerScatter)
		c.funnelSubtaskBudget -= funnelSubtaskBudget

		if skimSubtaskBudget > 0 {
			if c.funnelSubtaskBudget > 0 {
				// If we have funnel subtask budget to flush, we must also
				// reserve budget for at least one skim subtask
				skimSubtaskBudget--
			}
			skimSubtaskBudgetPerScatter = max(1, skimSubtaskBudget/shares)
			skimSubtaskBudget = min(skimSubtaskBudget, scatters*skimSubtaskBudgetPerScatter)
			c.skimSubtaskBudget -= skimSubtaskBudget
		}

		for range scatters {
			scatterFunnelSubtaskBudget := min(funnelSubtaskBudget, funnelSubtaskBudgetPerScatter)
			funnelSubtaskBudget -= scatterFunnelSubtaskBudget
			scatterSkimSubtaskBudget := min(skimSubtaskBudget, skimSubtaskBudgetPerScatter)
			skimSubtaskBudget -= scatterSkimSubtaskBudget
			for {
				deadline := time.Now().Add(c.workloadDuration)
				taskFn := c.newTaskFn(
					time.Now(),
					taskRes.Depth+1,
					scatterFunnelSubtaskBudget,
					scatterSkimSubtaskBudget,
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

	duration := time.Since(funnelStartTime)
	c.durationsNs.Add(float64(duration.Nanoseconds()), 1.0)

	c.durationSum += duration

	return flushDeadline, err
}

func (c *benchmarkFunnel) Flush(ctx context.Context) error {
	now := time.Now()
	res := benchmarkFunneldResult{
		Time:                         now,
		MaxDepth:                     c.maxDepth,
		FunnelSubtaskBudget:          c.funnelSubtaskBudget,
		SkimSubtaskBudget:            c.skimSubtaskBudget,
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
		funnelStartTime := c.firstFunnelTime
		if testEndTime == 0 || funnelStartTime < testEndTime {
			if funnelStartTime < testStartTime {
				funnelStartTime = testStartTime
			}
			funnelEndTime := testEndTime
			if funnelEndTime == 0 {
				funnelEndTime = time.Since(epoch)
			}
			c.cumulativeFunnelTime.Add(int64(funnelEndTime - funnelStartTime))
		}
	}
	skimmer := c.skimmer
	job := c.job
	benchmarkFunnelPool.Put(c)

	return skimmer.Submit(ctx, job, res, nil)
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

// BenchmarkFunnelThroughput measures the maximum throughput of processing
// a continuous stream of data with skim-only vs. funnel approaches
func BenchmarkFunnelThroughput(b *testing.B) {
	funnelLimits := []int{
		-1, // funnel, unlimited
		0,  // skim-only
		1, 2, 3, 4,
	}
	availableCores := runtime.GOMAXPROCS(-1)
	for {
		prevLimit := funnelLimits[len(funnelLimits)-1]
		if prevLimit >= availableCores {
			break
		}
		funnelLimits = append(funnelLimits, prevLimit*2)
	}
	funnelLimits = append(funnelLimits, availableCores, availableCores*2)
	slices.Sort(funnelLimits)
	funnelLimits = slices.Compact(funnelLimits)

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
				for _, funnelLimit := range funnelLimits {
					// Only need to run skim-only once to cover all flush periods
					if funnelLimit == 0 && fpi > 0 {
						continue
					}

					var method string
					switch funnelLimit {
					case 0:
						method = "skimOnly"
					default:
						method = "funnel"
					}
					name := fmt.Sprintf(
						"workload=%s/duration=%v/flushPeriod=%v/method=%s/funnelLimit=%d",
						workload,
						workloadDuration,
						flushPeriod,
						method,
						funnelLimit,
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

						job := streampool.New(ctx)
						defer func() {
							job.CancelAndWait()
						}()

						totalTasksSkimed := 0

						taskLatenciesNs := tdigest.New()

						funnelLatenciesNs := tdigest.New()
						funnelDurationsNs := tdigest.New()
						var funnelDurationSum time.Duration
						funnelDurationCount := 0
						funnelWorkflowLatenciesNs := tdigest.New()
						funnelCounts := tdigest.New()

						skimLatenciesNs := tdigest.New()
						skimDurationsNs := tdigest.New()
						var skimDurationSum time.Duration
						skimDurationCount := 0

						workflowLatenciesNs := tdigest.New()

						var testStartTime atomic.Int64        // time.Duration since epoch
						var testEndTime atomic.Int64          // time.Duration since epoch
						var cumulativeFunnelTime atomic.Int64 // time.Duration

						var abandonedTaskCount atomic.Int64
						maxDepth := 0
						var maxCumulativeNominalDuration time.Duration

						var newTaskFn func(startTime time.Time, depth, funnelSubtaskBudget, skimSubtaskBudget int,
							cumulativeNominalDuration time.Duration) streampool.Task[benchmarkTaskResult]
						var scatter func(ctx context.Context, deadline time.Time, target streampool.TaskPoolOrJob,
							task streampool.Task[benchmarkTaskResult]) (bool, error)

						skimFn := func(ctx context.Context, funnelRes benchmarkFunneldResult, err error) error {
							if err != nil {
								return err
							}

							skimStartTime := time.Now()
							skimLatencyNs := float64(skimStartTime.Sub(funnelRes.Time).Nanoseconds())

							// Front-load all measurement work before simulated work
							skimLatenciesNs.Add(skimLatencyNs, 1.0)
							totalTasksSkimed += funnelRes.Count
							taskLatenciesNs.AddCentroidList(funnelRes.TaskLatenciesNs)
							funnelLatenciesNs.AddCentroidList(funnelRes.LatenciesNs)
							funnelDurationsNs.AddCentroidList(funnelRes.DurationsNs)
							funnelDurationSum += funnelRes.DurationSum
							funnelDurationCount += funnelRes.DurationCount
							funnelWorkflowLatenciesNs.AddCentroidList(funnelRes.WorkflowLatenciesNs)

							maxDepth = max(maxDepth, funnelRes.MaxDepth)
							maxCumulativeNominalDuration = max(maxCumulativeNominalDuration, funnelRes.MaxCumulativeNominalDuration)

							// Add skim latency to workflow latencies to get scatter-to-skim-start time
							for i := range funnelRes.WorkflowLatenciesNs {
								funnelRes.WorkflowLatenciesNs[i].Mean += skimLatencyNs
							}
							workflowLatenciesNs.AddCentroidList(funnelRes.WorkflowLatenciesNs)

							funnelCounts.Add(float64(funnelRes.Count), 1.0)

							simulateWorkFrom(skimStartTime, workloadDuration)

							funnelSubtaskBudget := funnelRes.FunnelSubtaskBudget
							skimSubtaskBudget := funnelRes.SkimSubtaskBudget
							switch {
							case funnelSubtaskBudget < 0:
								panic("funnelSubtaskBudget is negative")
							case skimSubtaskBudget < 0:
								panic("skimSubtaskBudget is negative")
							case skimSubtaskBudget == 0:
								if funnelSubtaskBudget != 0 {
									panic("skimSubtaskBudget is zero, but funnelSubtaskBudget is non-zero")
								}
							case skimSubtaskBudget > 0:
								scatters := bits.Len(uint(skimSubtaskBudget))
								skimSubtaskBudget -= scatters
								funnelSubtaskBudgetPerScatter := max(1, funnelSubtaskBudget/scatters)
								skimSubtaskBudgetPerScatter := max(1, skimSubtaskBudget/scatters)
								for range scatters {
									scatterFunnelSubtaskBudget := min(funnelSubtaskBudget, funnelSubtaskBudgetPerScatter)
									scatterSkimSubtaskBudget := min(skimSubtaskBudget, skimSubtaskBudgetPerScatter)
									funnelSubtaskBudget -= scatterFunnelSubtaskBudget
									skimSubtaskBudget -= scatterSkimSubtaskBudget
									for {
										deadline := time.Now().Add(workloadDuration)
										taskFn := newTaskFn(
											time.Now(),
											funnelRes.MaxDepth+1,
											scatterFunnelSubtaskBudget,
											scatterSkimSubtaskBudget,
											funnelRes.MaxCumulativeNominalDuration+workloadDuration,
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

							skimDuration := time.Since(skimStartTime)
							skimDurationNs := float64(skimDuration.Nanoseconds())
							skimDurationsNs.Add(skimDurationNs, 1.0)
							skimDurationSum += skimDuration
							skimDurationCount++

							poolCentroidList(funnelRes.TaskLatenciesNs)
							poolCentroidList(funnelRes.LatenciesNs)
							poolCentroidList(funnelRes.DurationsNs)
							poolCentroidList(funnelRes.WorkflowLatenciesNs)

							return nil
						}

						skimFnAdapter := func(ctx context.Context, taskRes benchmarkTaskResult, err error) error {
							now := time.Now()
							taskLatencyNsCentroid := tdigest.Centroid{
								Mean:   float64(now.Sub(taskRes.Time).Nanoseconds()),
								Weight: 1.0,
							}
							funneldRes := benchmarkFunneldResult{
								Time:                         now,
								MaxDepth:                     taskRes.Depth,
								MaxCumulativeNominalDuration: taskRes.CumulativeNominalDuration,
								Count:                        1,
								TaskLatenciesNs:              newCentroidList(taskLatencyNsCentroid),
								LatenciesNs:                  newCentroidList(taskLatencyNsCentroid),
								DurationsNs:                  newCentroidList(),
								WorkflowLatenciesNs:          newCentroidList(taskLatencyNsCentroid),
							}
							return skimFn(ctx, funneldRes, err)
						}

						idealFunnelsPerSkim := int(math.Round(float64(flushPeriod) / float64(workloadDuration)))

						// Setup processing - either skim-only or with funnel
						if funnelLimit == 0 {
							scatter = func(ctx context.Context, deadline time.Time, target streampool.TaskPoolOrJob,
								task streampool.Task[benchmarkTaskResult]) (bool, error) {
								// Tests to make sure that NewSkimmer does not incur allocation overhead
								skimmer := streampool.NewSkimmer(wave, streampool.NewHandler(skimFnAdapter))
								if deadline.IsZero() {
									return true, skimmer.Start(ctx, target, task)
								}
								return skimmer.TryStart(ctx, deadline, target, task)
							}
						} else {
							funnelPool := wave
							skimmer := streampool.NewSkimmer(wave, streampool.NewHandler(skimFn))
							funnelFactory := func() streampool.Accumulator[benchmarkTaskResult] {
								return newBenchmarkFunnel(
									&testStartTime,
									&testEndTime,
									&cumulativeFunnelTime,
									&abandonedTaskCount,
									simulateWorkFrom,
									workloadDuration,
									flushPeriod,
									scatter,
									job,
									newTaskFn,
									idealFunnelsPerSkim,
									skimmer,
									job,
								)
							}

							funnelOp := streampool.NewFunnel(funnelPool, funnelFactory)
							defer funnelOp.Close()

							scatter = func(ctx context.Context, deadline time.Time, target streampool.TaskPoolOrJob,
								task streampool.Task[benchmarkTaskResult]) (bool, error) {
								localFunnelOp := funnelOp
								if idealFunnelsPerSkim == 1 {
									// Tests to make sure that NewFunnel does not incur allocation overhead
									localFunnelOp = streampool.NewFunnel(funnelPool, funnelFactory)
									defer localFunnelOp.Close()
								}
								if deadline.IsZero() {
									return true, localFunnelOp.Start(ctx, target, task)
								}
								return localFunnelOp.TryStart(ctx, deadline, target, task)
							}
						}

						var totalTasksLaunched atomic.Int64
						newTaskFn = func(startTime time.Time, depth, funnelSubtaskBudget, skimSubtaskBudget int,
							cumulativeNominalDuration time.Duration) streampool.Task[benchmarkTaskResult] {
							totalTasksLaunched.Add(1)
							return newBenchmarkTaskFn(startTime, depth, funnelSubtaskBudget, skimSubtaskBudget, cumulativeNominalDuration)
						}

						opTasksSkimedOrigin := totalTasksSkimed
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

								if totalTasksSkimed != opTasksSkimedOrigin {
									tasksSkimed := totalTasksSkimed - opTasksSkimedOrigin
									opTasksSkimedOrigin = totalTasksSkimed
									return tasksSkimed
								}
							}
						}

						// Run for a while so that garbage collection and
						// pooling can find the pattern
						warmupStartTime := time.Now()
						for time.Since(warmupStartTime) < 1*time.Second {
							op()
						}

						topLevelTasksOrigin := totalTopLevelTasks
						tasksSkimedOrigin := totalTasksSkimed
						maxDepth = 0
						maxCumulativeNominalDuration = 0
						taskLatenciesNs.Reset()
						funnelLatenciesNs.Reset()
						funnelDurationsNs.Reset()
						funnelDurationSum = 0
						funnelDurationCount = 0
						funnelWorkflowLatenciesNs.Reset()
						skimLatenciesNs.Reset()
						skimDurationsNs.Reset()
						skimDurationSum = 0
						skimDurationCount = 0
						workflowLatenciesNs.Reset()
						testStartTime.Store(int64(time.Since(epoch)))
						func() {
							defer trace.StartRegion(ctx, "BenchmarkFunnelThroughput.Loop").End()
							for b.Loop() {
								op()
							}
						}()
						testEndTime.Store(int64(time.Since(epoch)))

						// We purposefully do not run job.CloseAndSkimAll
						// before capturing results to avoid inflating
						// overallSum with data skimmed outside the
						// benchmarking loop.

						topLevelTasks := float64(totalTopLevelTasks - topLevelTasksOrigin)
						tasksSkimed := float64(totalTasksSkimed - tasksSkimedOrigin)

						// Now call CloseAndSkimAll to make sure nothing was lost.
						assert.NoError(b, job.CloseAndSkimAll(ctx))
						assert.Equal(b, totalTasksLaunched.Load(), int64(totalTasksSkimed)+abandonedTaskCount.Load())

						avgFunnelConcurrency := float64(cumulativeFunnelTime.Load()) /
							float64(testEndTime.Load()-testStartTime.Load())

						b.ReportAllocs()

						// Throughput - the primary metric for this benchmark
						throughput := tasksSkimed / b.Elapsed().Seconds()
						b.ReportMetric(throughput, "tasks/sec")

						// Tasks per operation - needed to normalize allocs/op and B/op
						tasksPerOp := tasksSkimed / float64(b.N)
						b.ReportMetric(tasksPerOp, "tasks/op")

						// Top Level tasks per operation
						b.ReportMetric(tasksSkimed/topLevelTasks, "tasks/top-level-task")

						b.ReportMetric(float64(maxDepth), "max-depth")

						b.ReportMetric(taskLatenciesNs.Quantile(0.99), "p99-task-latency-ns")
						b.ReportMetric(taskLatenciesNs.Quantile(0.50), "p50-task-latency-ns")

						if funnelLimit != 0 {
							b.ReportMetric(funnelLatenciesNs.Quantile(0.99), "p99-funnel-latency-ns")
							b.ReportMetric(funnelLatenciesNs.Quantile(0.50), "p50-funnel-latency-ns")
							b.ReportMetric(funnelDurationsNs.Quantile(0.99), "p99-funnel-duration-ns")
							b.ReportMetric(funnelDurationsNs.Quantile(0.50), "p50-funnel-duration-ns")
							b.ReportMetric(funnelWorkflowLatenciesNs.Quantile(0.99), "p99-funnel-workflow-latency-ns")
							b.ReportMetric(funnelWorkflowLatenciesNs.Quantile(0.50), "p50-funnel-workflow-latency-ns")
							b.ReportMetric(funnelCounts.Quantile(0.99), "p99-funnel-count")
							b.ReportMetric(funnelCounts.Quantile(0.50), "p50-funnel-count")
							b.ReportMetric(funnelCounts.Quantile(0.01), "p01-funnel-count")
						}

						b.ReportMetric(skimLatenciesNs.Quantile(0.99), "p99-skim-latency-ns")
						b.ReportMetric(skimLatenciesNs.Quantile(0.50), "p50-skim-latency-ns")
						b.ReportMetric(skimDurationsNs.Quantile(0.99), "p99-skim-duration-ns")
						b.ReportMetric(skimDurationsNs.Quantile(0.50), "p50-skim-duration-ns")

						b.ReportMetric(float64(maxCumulativeNominalDuration.Nanoseconds()), "max-nominal-workflow-duration-ns")

						b.ReportMetric(workflowLatenciesNs.Quantile(0.99), "p99-workflow-latency-ns")
						b.ReportMetric(workflowLatenciesNs.Quantile(0.50), "p50-workflow-latency-ns")

						if funnelLimit != 0 {
							b.ReportMetric(avgFunnelConcurrency, "avg-funnel-concurrency")
						}
					})
				}
			}
		}
	}
}
