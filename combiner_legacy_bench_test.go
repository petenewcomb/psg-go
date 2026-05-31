//go:build psg_wave3_legacy_bench
// +build psg_wave3_legacy_bench

// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// This file holds the Wave-2-era benchmark suite for the combiner.
// Wave 3 reshaped Task / Skimmer / Combiner enough that the benchmark
// requires a deliberate redesign (see REFACTOR_PLAN.md: combiner-
// benchmark requirements session). To keep Wave 3 focused, the entire
// benchmark is gated behind the `psg_wave3_legacy_bench` build tag and
// is NOT compiled by default. Restore by either porting it to the new
// API or removing the build tag.

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

type benchmarkTaskResult struct {
	Time                      time.Time
	Depth                     int
	CombineSubtaskBudget      int
	SkimSubtaskBudget         int
	CumulativeNominalDuration time.Duration
	Latency                   time.Duration
}

type benchmarkTask struct {
	startTime                 time.Time
	depth                     int
	combineSubtaskBudget      int
	skimSubtaskBudget         int
	cumulativeNominalDuration time.Duration
	executeFn                 psgfn.Task[benchmarkTaskResult]
}

var benchmarkTaskPool = omnipool.For[benchmarkTask]()

func newBenchmarkTaskFn(
	startTime time.Time,
	depth, combineSubtaskBudget, skimSubtaskBudget int,
	cumulativeNominalDuration time.Duration,
) psgfn.Task[benchmarkTaskResult] {
	task := benchmarkTaskPool.Get()
	task.startTime = startTime
	task.depth = depth
	task.combineSubtaskBudget = combineSubtaskBudget
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
		CombineSubtaskBudget:      t.combineSubtaskBudget,
		SkimSubtaskBudget:         t.skimSubtaskBudget,
		CumulativeNominalDuration: t.cumulativeNominalDuration,
	}

	benchmarkTaskPool.Put(t)
	return res, nil
}

type benchmarkCombinedResult struct {
	Time                         time.Time
	MaxDepth                     int
	CombineSubtaskBudget         int
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
	newTaskFn func(startTime time.Time, depth, combineSubtaskBudget, skimSubtaskBudget int,
		cumulativeNominalDuration time.Duration) psgfn.Task[benchmarkTaskResult]
	idealCombinesPerSkim int

	// Downstream sink captured for Submit-on-Flush (Wave 2 reshape).
	skimmer psg.Skimmer[benchmarkCombinedResult]
	job     *psg.Pool

	maxDepth                     int
	combineSubtaskBudget         int
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
	newTaskFn func(startTime time.Time, depth, combineSubtaskBudget, skimSubtaskBudget int,
		cumulativeNominalDuration time.Duration) psgfn.Task[benchmarkTaskResult],
	idealCombinesPerSkim int,
	skimmer psg.Skimmer[benchmarkCombinedResult],
	job *psg.Pool,
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
	c.idealCombinesPerSkim = idealCombinesPerSkim
	c.skimmer = skimmer
	c.job = job
	return c
}

func (c *benchmarkCombiner) Accumulate(ctx context.Context, taskRes benchmarkTaskResult, err error) (time.Time, error) {
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
	if c.count < c.idealCombinesPerSkim {
		flushDeadline = flushDeadline.Add(c.flushPeriod)
	}

	c.taskLatenciesNs.Add(float64(taskRes.Latency.Nanoseconds()), 1.0)
	c.latenciesNs.Add(float64(latency.Nanoseconds()), 1.0)
	// Workflow latency: scatter to combine start (queueing time)
	c.workflowLatenciesNs.Add(float64((taskRes.Latency + latency).Nanoseconds()), 1.0)

	c.simulateWorkFrom(combineStartTime, c.workloadDuration)

	// Don't include scatter time in work duration
	c.combineSubtaskBudget += taskRes.CombineSubtaskBudget
	c.skimSubtaskBudget += taskRes.SkimSubtaskBudget
	switch {
	case c.combineSubtaskBudget < 0:
		panic("combineSubtaskBudget is negative")
	case c.skimSubtaskBudget < 0:
		panic("skimSubtaskBudget is negative")
	case c.combineSubtaskBudget > 0:
		scatters := bits.Len(uint(c.combineSubtaskBudget))
		c.combineSubtaskBudget -= scatters
		shares := scatters

		skimSubtaskBudget := c.skimSubtaskBudget
		skimSubtaskBudgetPerScatter := 0
		if skimSubtaskBudget > 0 {
			// Save some subtask budget for flushing
			shares++
			skimSubtaskBudgetPerScatter = max(1, skimSubtaskBudget/shares)
		}

		combineSubtaskBudget := c.combineSubtaskBudget
		combineSubtaskBudgetPerScatter := max(1, combineSubtaskBudget/shares)
		combineSubtaskBudget = min(combineSubtaskBudget, scatters*combineSubtaskBudgetPerScatter)
		c.combineSubtaskBudget -= combineSubtaskBudget

		if skimSubtaskBudget > 0 {
			if c.combineSubtaskBudget > 0 {
				// If we have combine subtask budget to flush, we must also
				// reserve budget for at least one skim subtask
				skimSubtaskBudget--
			}
			skimSubtaskBudgetPerScatter = max(1, skimSubtaskBudget/shares)
			skimSubtaskBudget = min(skimSubtaskBudget, scatters*skimSubtaskBudgetPerScatter)
			c.skimSubtaskBudget -= skimSubtaskBudget
		}

		for range scatters {
			scatterCombineSubtaskBudget := min(combineSubtaskBudget, combineSubtaskBudgetPerScatter)
			combineSubtaskBudget -= scatterCombineSubtaskBudget
			scatterSkimSubtaskBudget := min(skimSubtaskBudget, skimSubtaskBudgetPerScatter)
			skimSubtaskBudget -= scatterSkimSubtaskBudget
			for {
				deadline := time.Now().Add(c.workloadDuration)
				taskFn := c.newTaskFn(
					time.Now(),
					taskRes.Depth+1,
					scatterCombineSubtaskBudget,
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

	duration := time.Since(combineStartTime)
	c.durationsNs.Add(float64(duration.Nanoseconds()), 1.0)

	c.durationSum += duration

	return flushDeadline, err
}

func (c *benchmarkCombiner) Flush(ctx context.Context) error {
	now := time.Now()
	res := benchmarkCombinedResult{
		Time:                         now,
		MaxDepth:                     c.maxDepth,
		CombineSubtaskBudget:         c.combineSubtaskBudget,
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
	skimmer := c.skimmer
	job := c.job
	benchmarkCombinerPool.Put(c)

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

// BenchmarkCombinerThroughput measures the maximum throughput of processing
// a continuous stream of data with skim-only vs. combiner approaches
func BenchmarkCombinerThroughput(b *testing.B) {
	combinerLimits := []int{
		-1, // combine, unlimited
		0,  // skim-only
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
					// Only need to run skim-only once to cover all flush periods
					if combinerLimit == 0 && fpi > 0 {
						continue
					}

					var method string
					switch combinerLimit {
					case 0:
						method = "skimOnly"
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

						job := psg.New(ctx)
						defer func() {
							job.CancelAndWait()
						}()

						totalTasksSkimed := 0

						taskLatenciesNs := tdigest.New()

						combineLatenciesNs := tdigest.New()
						combineDurationsNs := tdigest.New()
						var combineDurationSum time.Duration
						combineDurationCount := 0
						combineWorkflowLatenciesNs := tdigest.New()
						combineCounts := tdigest.New()

						skimLatenciesNs := tdigest.New()
						skimDurationsNs := tdigest.New()
						var skimDurationSum time.Duration
						skimDurationCount := 0

						workflowLatenciesNs := tdigest.New()

						var testStartTime atomic.Int64          // time.Duration since epoch
						var testEndTime atomic.Int64            // time.Duration since epoch
						var cumulativeCombinerTime atomic.Int64 // time.Duration

						var abandonedTaskCount atomic.Int64
						maxDepth := 0
						var maxCumulativeNominalDuration time.Duration

						var newTaskFn func(startTime time.Time, depth, combineSubtaskBudget, skimSubtaskBudget int,
							cumulativeNominalDuration time.Duration) psgfn.Task[benchmarkTaskResult]
						var scatter func(ctx context.Context, deadline time.Time, target psg.TaskPoolOrJob,
							task psgfn.Task[benchmarkTaskResult]) (bool, error)

						skimFn := func(ctx context.Context, combineRes benchmarkCombinedResult, err error) error {
							if err != nil {
								return err
							}

							skimStartTime := time.Now()
							skimLatencyNs := float64(skimStartTime.Sub(combineRes.Time).Nanoseconds())

							// Front-load all measurement work before simulated work
							skimLatenciesNs.Add(skimLatencyNs, 1.0)
							totalTasksSkimed += combineRes.Count
							taskLatenciesNs.AddCentroidList(combineRes.TaskLatenciesNs)
							combineLatenciesNs.AddCentroidList(combineRes.LatenciesNs)
							combineDurationsNs.AddCentroidList(combineRes.DurationsNs)
							combineDurationSum += combineRes.DurationSum
							combineDurationCount += combineRes.DurationCount
							combineWorkflowLatenciesNs.AddCentroidList(combineRes.WorkflowLatenciesNs)

							maxDepth = max(maxDepth, combineRes.MaxDepth)
							maxCumulativeNominalDuration = max(maxCumulativeNominalDuration, combineRes.MaxCumulativeNominalDuration)

							// Add skim latency to workflow latencies to get scatter-to-skim-start time
							for i := range combineRes.WorkflowLatenciesNs {
								combineRes.WorkflowLatenciesNs[i].Mean += skimLatencyNs
							}
							workflowLatenciesNs.AddCentroidList(combineRes.WorkflowLatenciesNs)

							combineCounts.Add(float64(combineRes.Count), 1.0)

							simulateWorkFrom(skimStartTime, workloadDuration)

							combineSubtaskBudget := combineRes.CombineSubtaskBudget
							skimSubtaskBudget := combineRes.SkimSubtaskBudget
							switch {
							case combineSubtaskBudget < 0:
								panic("combineSubtaskBudget is negative")
							case skimSubtaskBudget < 0:
								panic("skimSubtaskBudget is negative")
							case skimSubtaskBudget == 0:
								if combineSubtaskBudget != 0 {
									panic("skimSubtaskBudget is zero, but combineSubtaskBudget is non-zero")
								}
							case skimSubtaskBudget > 0:
								scatters := bits.Len(uint(skimSubtaskBudget))
								skimSubtaskBudget -= scatters
								combineSubtaskBudgetPerScatter := max(1, combineSubtaskBudget/scatters)
								skimSubtaskBudgetPerScatter := max(1, skimSubtaskBudget/scatters)
								for range scatters {
									scatterCombineSubtaskBudget := min(combineSubtaskBudget, combineSubtaskBudgetPerScatter)
									scatterSkimSubtaskBudget := min(skimSubtaskBudget, skimSubtaskBudgetPerScatter)
									combineSubtaskBudget -= scatterCombineSubtaskBudget
									skimSubtaskBudget -= scatterSkimSubtaskBudget
									for {
										deadline := time.Now().Add(workloadDuration)
										taskFn := newTaskFn(
											time.Now(),
											combineRes.MaxDepth+1,
											scatterCombineSubtaskBudget,
											scatterSkimSubtaskBudget,
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

							skimDuration := time.Since(skimStartTime)
							skimDurationNs := float64(skimDuration.Nanoseconds())
							skimDurationsNs.Add(skimDurationNs, 1.0)
							skimDurationSum += skimDuration
							skimDurationCount++

							poolCentroidList(combineRes.TaskLatenciesNs)
							poolCentroidList(combineRes.LatenciesNs)
							poolCentroidList(combineRes.DurationsNs)
							poolCentroidList(combineRes.WorkflowLatenciesNs)

							return nil
						}

						skimFnAdapter := func(ctx context.Context, taskRes benchmarkTaskResult, err error) error {
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
							return skimFn(ctx, combinedRes, err)
						}

						idealCombinesPerSkim := int(math.Round(float64(flushPeriod) / float64(workloadDuration)))

						// Setup processing - either skim-only or with combiner
						if combinerLimit == 0 {
							scatter = func(ctx context.Context, deadline time.Time, target psg.TaskPoolOrJob,
								task psgfn.Task[benchmarkTaskResult]) (bool, error) {
								// Tests to make sure that NewSkimmer does not incur allocation overhead
								skimmer := psg.NewSkimmer(psgfn.HandlerFunc[benchmarkTaskResult](skimFnAdapter))
								if deadline.IsZero() {
									return true, skimmer.Start(ctx, target, task)
								}
								return skimmer.TryStart(ctx, deadline, target, task)
							}
						} else {
							combinerPool := psg.NewCombinerPool(job, psgopt.WithMaxConcurrency(combinerLimit))
							skimmer := psg.NewSkimmer(psgfn.HandlerFunc[benchmarkCombinedResult](skimFn))
							combinerFactory := func() psgfn.Accumulator[benchmarkTaskResult] {
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
									idealCombinesPerSkim,
									skimmer,
									job,
								)
							}

							combineOp := psg.NewCombiner(combinerPool, combinerFactory)
							defer combineOp.Close()

							scatter = func(ctx context.Context, deadline time.Time, target psg.TaskPoolOrJob,
								task psgfn.Task[benchmarkTaskResult]) (bool, error) {
								localCombineOp := combineOp
								if idealCombinesPerSkim == 1 {
									// Tests to make sure that NewCombiner does not incur allocation overhead
									localCombineOp = psg.NewCombiner(combinerPool, combinerFactory)
									defer localCombineOp.Close()
								}
								if deadline.IsZero() {
									return true, localCombineOp.Start(ctx, target, task)
								}
								return localCombineOp.TryStart(ctx, deadline, target, task)
							}
						}

						var totalTasksLaunched atomic.Int64
						newTaskFn = func(startTime time.Time, depth, combineSubtaskBudget, skimSubtaskBudget int,
							cumulativeNominalDuration time.Duration) psgfn.Task[benchmarkTaskResult] {
							totalTasksLaunched.Add(1)
							return newBenchmarkTaskFn(startTime, depth, combineSubtaskBudget, skimSubtaskBudget, cumulativeNominalDuration)
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
						combineLatenciesNs.Reset()
						combineDurationsNs.Reset()
						combineDurationSum = 0
						combineDurationCount = 0
						combineWorkflowLatenciesNs.Reset()
						skimLatenciesNs.Reset()
						skimDurationsNs.Reset()
						skimDurationSum = 0
						skimDurationCount = 0
						workflowLatenciesNs.Reset()
						testStartTime.Store(int64(time.Since(epoch)))
						func() {
							defer trace.StartRegion(ctx, "BenchmarkCombinerThroughput.Loop").End()
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

						avgCombinerConcurrency := float64(cumulativeCombinerTime.Load()) /
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

						b.ReportMetric(skimLatenciesNs.Quantile(0.99), "p99-skim-latency-ns")
						b.ReportMetric(skimLatenciesNs.Quantile(0.50), "p50-skim-latency-ns")
						b.ReportMetric(skimDurationsNs.Quantile(0.99), "p99-skim-duration-ns")
						b.ReportMetric(skimDurationsNs.Quantile(0.50), "p50-skim-duration-ns")

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
