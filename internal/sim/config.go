// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

//nolint:mnd // default configuration
var DefaultConfig = Config{
	Deterministic: false,
	Path: PathConfig{
		Count:  BiasedIntConfig{Min: 1, Med: 15, Max: 30},
		Length: BiasedIntConfig{Min: 1, Med: 3, Max: 5},
	},
	TaskLimiter:   defaultLimiterConfig,
	FunnelLimiter: defaultLimiterConfig,
	TaskRunner:    defaultTaskRunnerConfig,
	Skimmer:       defaultSkimmerConfig,
	Funnel:        defaultFunnelConfig,
	Subjob:        SubjobConfig{MaxDepth: 3},
}

// Config controls plan generation. The Deterministic flag forces all
// probabilistic Step settings (Prob, SelfTime distribution width,
// ReturnErrorProb) to their deterministic-equivalent values so the
// generator produces plans whose execution counts are exactly bounded.
// Probabilistic mode (Deterministic = false) keeps distributions and
// probabilities, giving richer race-exposure but only Max-bounded
// assertions.
type Config struct {
	Deterministic bool
	Path          PathConfig
	TaskLimiter   LimiterConfig
	FunnelLimiter LimiterConfig
	TaskRunner    TaskRunnerConfig
	Skimmer       SkimmerConfig
	Funnel        FunnelConfig
	Subjob        SubjobConfig
}

type PathConfig struct {
	Count  BiasedIntConfig
	Length BiasedIntConfig
}
