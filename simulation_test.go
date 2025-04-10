// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/addrummond/heap"
)

type simulatedTask struct {
	executionDuration time.Duration
	children          []*simulatedTask
}

func (st *simulatedTask) Format(f fmt.State, verb rune) {
	if verb != 'v' {
		panic("unsupported verb")
	}
	st.formatInternal(f, "  ")
}

func (st *simulatedTask) formatInternal(f fmt.State, indent string) {
	fmt.Fprintf(f, "%s%p: %v\n", indent, st, st.executionDuration)
	for _, child := range st.children {
		child.formatInternal(f, indent+"  ")
	}
}

type simulatedResult struct {
	task                                 *simulatedTask
	concurrencyAtStart, concurrencyAfter int64
}

func biasedBool(p float64) *rapid.Generator[bool] {
	notOne := func(v float64) bool { return v != 1 }
	return rapid.Custom(func(t *rapid.T) bool {
		return rapid.Float64Range(0, 1).Filter(notOne).Draw(t, "p") < p
	})
}

type executionDatapoints struct {
	maxConcurrencies []int64
	overallDurations []time.Duration
}

func (d executionDatapoints) computeStatistics() executionStatistics {
	var statistics executionStatistics

	for i, maxConcurrency := range d.maxConcurrencies {
		if i == 0 || maxConcurrency < statistics.minMaxConcurrency {
			statistics.minMaxConcurrency = maxConcurrency
		}
		if i == 0 || maxConcurrency > statistics.maxMaxConcurrency {
			statistics.maxMaxConcurrency = maxConcurrency
		}
	}

	for i, overallDuration := range d.overallDurations {
		if i == 0 || overallDuration < statistics.minOverallDuration {
			statistics.minOverallDuration = overallDuration
		}
		if i == 0 || overallDuration > statistics.maxOverallDuration {
			statistics.maxOverallDuration = overallDuration
		}
	}
	return statistics
}

type executionStatistics struct {
	minMaxConcurrency, maxMaxConcurrency   int64
	minOverallDuration, maxOverallDuration time.Duration
}

func mergeExecutionStatistics(a, b executionStatistics) executionStatistics {
	return executionStatistics{
		minMaxConcurrency:  min(a.minMaxConcurrency, b.minMaxConcurrency),
		maxMaxConcurrency:  max(a.maxMaxConcurrency, b.maxMaxConcurrency),
		minOverallDuration: min(a.minOverallDuration, b.minOverallDuration),
		maxOverallDuration: max(a.maxOverallDuration, b.maxOverallDuration),
	}
}

func repeatedlyExecute(times int, execute func() executionDatapoints) executionDatapoints {
	var overallDatapoints executionDatapoints

	// Warm up to avoid measuring cold start effects
	_ = execute()
	_ = execute()
	_ = execute()

	for range times {
		datapoints := execute()
		overallDatapoints.maxConcurrencies = append(overallDatapoints.maxConcurrencies, datapoints.maxConcurrencies...)
		overallDatapoints.overallDurations = append(overallDatapoints.overallDurations, datapoints.overallDurations...)
	}
	return overallDatapoints
}

func simulateExecution(t *rapid.T, concurrencyLimit int, minJitter, maxJitter time.Duration, pendingTasks []*simulatedTask) executionDatapoints {

	// Clone to avoid modifying the input, since we'll use this as a mutable queue
	pendingTasks = slices.Clone(pendingTasks)

	var scheduledExecutions heap.Heap[simulatedExecution, heap.Min]
	var runningExecutions heap.Heap[simulatedExecution, heap.Min]
	durationSinceStart := time.Duration(0)
	maxConcurrency := 0

	// Keep track of the next end duration to simplify time advancement
	var nextEndDuration time.Duration
	hasNextEndDuration := false
	updateNextEndDuration := func(endDuration time.Duration, ok bool) {
		if ok && (!hasNextEndDuration || endDuration < nextEndDuration) {
			nextEndDuration = endDuration
			hasNextEndDuration = true
		}
	}

	schedulePendingTask := func() {
		// Pop from the end of the pending list to minimize buffer reallocation.
		i := len(pendingTasks) - 1
		nextTask := pendingTasks[i]
		t.Logf("%v scheduling %p", durationSinceStart, nextTask)
		pendingTasks = pendingTasks[:i]
		jitter := time.Duration(rapid.Int64Range(int64(minJitter), int64(maxJitter)).Draw(t, "jitter"))
		// Add the index to the jitter to bias execution order towards the order in which the pending tasks were added
		jitter += time.Duration(i)
		heap.PushOrderable(&scheduledExecutions, simulatedExecution{
			task:        nextTask,
			endDuration: durationSinceStart + jitter,
		})
	}

	// Reap a scheduled execution if one is ready to start
	reapScheduledExecution := func() bool {
		execution, ok := heap.Peek(&scheduledExecutions)
		if !ok || execution.endDuration > durationSinceStart {
			updateNextEndDuration(execution.endDuration, ok)
			return false
		}
		t.Logf("%v reaping scheduled execution: %p", durationSinceStart, execution.task)
		_, _ = heap.PopOrderable(&scheduledExecutions)
		heap.PushOrderable(&runningExecutions, simulatedExecution{
			task:        execution.task,
			endDuration: execution.task.executionDuration + durationSinceStart,
		})
		return true
	}

	// Reap a running execution if one is already complete
	reapRunningExecution := func() bool {
		execution, ok := heap.Peek(&runningExecutions)
		if !ok || execution.endDuration > durationSinceStart {
			updateNextEndDuration(execution.endDuration, ok)
			return false
		}
		t.Logf("%v reaping running execution: %p", durationSinceStart, execution.task)
		_, _ = heap.PopOrderable(&runningExecutions)
		pendingTasks = append(pendingTasks, execution.task.children...)
		return true
	}

	for {
		pendingCount := len(pendingTasks)
		scheduledCount := heap.Len(&scheduledExecutions)
		runningCount := heap.Len(&runningExecutions)
		concurrency := runningCount + scheduledCount
		if concurrency == 0 && pendingCount == 0 {
			t.Logf("%v done", durationSinceStart)
			break
		}

		hasNextEndDuration = false
		switch {
		case reapRunningExecution():
		case reapScheduledExecution():

		case concurrency >= concurrencyLimit || pendingCount == 0:
			// Advance durationSinceStart if we've hit the concurrency limit or
			// there are no pending tasks. Else schedule a pending task.
			t.Logf("%v advancing to %v", durationSinceStart, nextEndDuration)
			durationSinceStart = nextEndDuration

			// nextEndDuration must be valid because at least one of
			// runningCount or scheduledCount must be greater than zero
			require.True(t, hasNextEndDuration)

		case pendingCount > 0:
			schedulePendingTask()
			concurrency++
			if concurrency > maxConcurrency {
				maxConcurrency = concurrency
			}
		}
	}

	return executionDatapoints{
		maxConcurrencies: []int64{int64(maxConcurrency)},
		overallDurations: []time.Duration{durationSinceStart},
	}
}

type simulatedExecution struct {
	task        *simulatedTask
	endDuration time.Duration
}

func (a *simulatedExecution) Cmp(b *simulatedExecution) int {
	return cmp.Compare(a.endDuration, b.endDuration)
}

func buildSimulatedTasks(t *rapid.T, concurrencyLimit int) (taskCount int, maxTaskDuration time.Duration, rootTasks []*simulatedTask) {
	maxPaths := 1000
	maxPathLength := 10
	minExecutionDuration := 1 * time.Millisecond
	maxExecutionDuration := 10 * time.Millisecond

	newIntermediateChildProbability := rapid.Float64Range(0, 1).Draw(t, "newIntermediateChildProbability")

	createNewIntermediateChild := func() bool {
		return biasedBool(newIntermediateChildProbability).Draw(t, "createNewIntermediateChild")
	}

	var rootTask simulatedTask

	executionDurationBudget := 100 * time.Millisecond
	budgetedPaths := int(executionDurationBudget * time.Duration(concurrencyLimit) / maxExecutionDuration)
	pathCount := rapid.IntRange(1, min(maxPaths, budgetedPaths)).Draw(t, "pathCount")
	t.Logf("concurrencyLimit: %d", concurrencyLimit)
	t.Logf("pathCount: %d", pathCount)

	maxTaskDuration = minExecutionDuration
	for range pathCount {
		pathLength := rapid.IntRange(1, maxPathLength).Draw(t, "pathLength")
		parent := &rootTask
		for step := range pathLength {
			var child *simulatedTask
			if step == pathLength-1 || len(parent.children) == 0 || createNewIntermediateChild() {
				child = &simulatedTask{
					executionDuration: time.Duration(rapid.Int64Range(int64(minExecutionDuration), int64(maxExecutionDuration)).Draw(t, "executionDuration")),
				}
				if child.executionDuration > maxTaskDuration {
					maxTaskDuration = child.executionDuration
				}
				taskCount++
				parent.children = append(parent.children, child)
			} else {
				child = rapid.SampledFrom(parent.children).Draw(t, "child")
			}
			parent = child
		}
		pathCount++
	}
	t.Logf("pathCount: %d, taskCount: %d, tasks:\n%v", pathCount, taskCount, rootTask.children)

	return taskCount, maxTaskDuration, rootTask.children
}

func TestBySimulation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		concurrencyLimit := rapid.IntRange(1, 100).Draw(t, "concurrencyLimit")
		taskCount, maxTaskDuration, rootTasks := buildSimulatedTasks(t, concurrencyLimit)

		minJitterEstimationCount := 100
		maxJitterEstimationCount := 100
		trialCount := 10
		if testing.Short() {
			minJitterEstimationCount = 10
			maxJitterEstimationCount = 10
			trialCount = 1
		}

		simulationDatapointsMinJitter := repeatedlyExecute(minJitterEstimationCount, func() executionDatapoints {
			return simulateExecution(t, concurrencyLimit, 0, 0, rootTasks)
		})
		expectations := simulationDatapointsMinJitter.computeStatistics()

		simulationDatapointsMaxJitter := repeatedlyExecute(maxJitterEstimationCount, func() executionDatapoints {
			return simulateExecution(t, concurrencyLimit, 0, 15*time.Millisecond, rootTasks)
		})
		expectations = mergeExecutionStatistics(expectations, simulationDatapointsMaxJitter.computeStatistics())

		check := require.New(t)

		results := repeatedlyExecute(trialCount, func() executionDatapoints {
			var concurrency atomic.Int64

			taskExecutor := func(task *simulatedTask) psg.TaskFunc[simulatedResult] {
				return func(ctx context.Context) (simulatedResult, error) {
					res := simulatedResult{
						task:               task,
						concurrencyAtStart: concurrency.Add(1),
					}
					defer func() {
						res.concurrencyAfter = concurrency.Add(-1)
					}()
					select {
					case <-time.After(task.executionDuration):
						return res, nil
					case <-ctx.Done():
						return res, ctx.Err()
					}
				}
			}

			ctx := context.Background()
			pool := psg.NewPool(concurrencyLimit)
			job := psg.NewJob(ctx, pool)
			defer job.Cancel()

			gatheredCount := 0
			maxConcurrency := int64(0)
			var gather psg.GatherFunc[simulatedResult]
			gather = func(ctx context.Context, res simulatedResult, err error) error {
				gatheredCount++
				check.LessOrEqual(gatheredCount, taskCount)
				check.NoError(err)
				check.Greater(res.concurrencyAtStart, int64(0))
				check.LessOrEqual(res.concurrencyAtStart, int64(concurrencyLimit))
				check.GreaterOrEqual(res.concurrencyAfter, int64(0))
				check.Less(res.concurrencyAfter, int64(concurrencyLimit))
				if res.concurrencyAtStart > maxConcurrency {
					maxConcurrency = res.concurrencyAtStart
				}
				for _, child := range res.task.children {
					err := psg.Scatter(ctx, pool, taskExecutor(child), gather)
					check.NoError(err)
				}
				return nil
			}

			startTime := time.Now()
			for _, task := range rootTasks {
				err := psg.Scatter(ctx, pool, taskExecutor(task), gather)
				check.NoError(err)
			}

			err := job.GatherAll(ctx)
			overallDuration := time.Since(startTime)
			check.NoError(err)

			check.Equal(taskCount, gatheredCount)

			t.Logf("%d %d %d %v %v %v", int64(expectations.minMaxConcurrency), maxConcurrency, int64(expectations.maxMaxConcurrency),
				expectations.minOverallDuration, overallDuration, expectations.maxOverallDuration)

			return executionDatapoints{
				maxConcurrencies: []int64{int64(maxConcurrency)},
				overallDurations: []time.Duration{overallDuration},
			}
		}).computeStatistics()

		// Adjust to account for imperfect estimation due to limited simulation repetition
		minMaxConcurrencyTolerance := 0.2
		maxMaxConcurrencyTolerance := 0.2
		minDurationTolerance := 0.3
		maxDurationTolerance := 0.4
		if testing.Short() {
			maxDurationTolerance = 0.6
		}
		expectations.minMaxConcurrency = max(1, int64(math.Floor((1-minMaxConcurrencyTolerance)*float64(expectations.minMaxConcurrency))))
		expectations.maxMaxConcurrency = int64(math.Ceil((1 + maxMaxConcurrencyTolerance) * float64(expectations.maxMaxConcurrency)))
		expectations.minOverallDuration = max(maxTaskDuration, time.Duration(math.Floor((1-minDurationTolerance)*float64(expectations.minOverallDuration))))
		expectations.maxOverallDuration = time.Duration(math.Ceil((1 + maxDurationTolerance) * float64(expectations.maxOverallDuration)))

		t.Logf("results:      %d %d %v %v\n", results.minMaxConcurrency, results.maxMaxConcurrency, results.minOverallDuration, results.maxOverallDuration)
		t.Logf("expectations: %d %d %v %v\n", expectations.minMaxConcurrency, expectations.maxMaxConcurrency, expectations.minOverallDuration, expectations.maxOverallDuration)

		check.GreaterOrEqual(results.maxMaxConcurrency, expectations.minMaxConcurrency)
		check.LessOrEqual(results.maxMaxConcurrency, expectations.maxMaxConcurrency)
		check.GreaterOrEqual(results.minOverallDuration, expectations.minOverallDuration)
		check.LessOrEqual(results.maxOverallDuration, expectations.maxOverallDuration)
	})
}
