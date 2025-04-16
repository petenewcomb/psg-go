// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

/*
// AdjustExpectations adjusts execution statistics to account for simulation uncertainty.
func AdjustExpectations(expectations ExecutionStatistics, plan *Plan, isShortTest bool) ExecutionStatistics {
	// Adjust to account for imperfect estimation due to limited simulation repetition
	minMaxConcurrencyTolerance := 0.2
	maxMaxConcurrencyTolerance := 0.2
	minDurationTolerance := 0.3
	maxDurationTolerance := 0.4
	if isShortTest {
		maxDurationTolerance = 0.6
	}
	expectations.MinMaxConcurrency = max(1, int64(math.Floor((1-minMaxConcurrencyTolerance)*float64(expectations.MinMaxConcurrency))))
	expectations.MaxMaxConcurrency = int64(math.Ceil((1 + maxMaxConcurrencyTolerance) * float64(expectations.MaxMaxConcurrency)))
	expectations.MinOverallDuration = max(plan.MaxPathDuration, time.Duration(math.Floor((1-minDurationTolerance)*float64(expectations.MinOverallDuration))))
	expectations.MaxOverallDuration = time.Duration(math.Ceil((1 + maxDurationTolerance) * float64(expectations.MaxOverallDuration)))
	return expectations
}

// ComputeStatistics calculates statistics from execution datapoints.
func (d *ExecutionDatapoints) ComputeStatistics() *ExecutionStatistics {
	statistics := &ExecutionStatistics{}

	for i, maxConcurrency := range d.MaxConcurrencies {
		if i == 0 || maxConcurrency < statistics.MinMaxConcurrency {
			statistics.MinMaxConcurrency = maxConcurrency
		}
		if i == 0 || maxConcurrency > statistics.MaxMaxConcurrency {
			statistics.MaxMaxConcurrency = maxConcurrency
		}
	}

	for i, overallDuration := range d.OverallDurations {
		if i == 0 || overallDuration < statistics.MinOverallDuration {
			statistics.MinOverallDuration = overallDuration
		}
		if i == 0 || overallDuration > statistics.MaxOverallDuration {
			statistics.MaxOverallDuration = overallDuration
		}
	}
	return statistics
}

// ExecutionStatistics contains statistics about task execution.
type ExecutionStatistics struct {
	MinMaxConcurrency, MaxMaxConcurrency   int64
	MinOverallDuration, MaxOverallDuration time.Duration
}

// MergeExecutionStatistics combines two sets of execution statistics.
func MergeExecutionStatistics(a, b ExecutionStatistics) ExecutionStatistics {
	return ExecutionStatistics{
		MinMaxConcurrency:  min(a.MinMaxConcurrency, b.MinMaxConcurrency),
		MaxMaxConcurrency:  max(a.MaxMaxConcurrency, b.MaxMaxConcurrency),
		MinOverallDuration: min(a.MinOverallDuration, b.MinOverallDuration),
		MaxOverallDuration: max(a.MaxOverallDuration, b.MaxOverallDuration),
	}
}

// RepeatedlyExecute runs an execution function multiple times and aggregates the results.
func RepeatedlyExecute(times int, execute func() ExecutionDatapoints) ExecutionDatapoints {
	var overallDatapoints ExecutionDatapoints

	// Warm up to avoid measuring cold start effects
	_ = execute()
	_ = execute()
	_ = execute()

	for range times {
		datapoints := execute()
		overallDatapoints.MaxConcurrencies = append(overallDatapoints.MaxConcurrencies, datapoints.MaxConcurrencies...)
		overallDatapoints.OverallDurations = append(overallDatapoints.OverallDurations, datapoints.OverallDurations...)
	}
	return overallDatapoints
}
*/
