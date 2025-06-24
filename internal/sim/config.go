// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

//nolint:mnd // default configuration
var DefaultConfig = Config{
	Path: PathConfig{
		Count:  BiasedIntConfig{Min: 1, Med: 15, Max: 30},
		Length: BiasedIntConfig{Min: 1, Med: 3, Max: 5},
	},
	Task:    defaultTaskConfig,
	Gather:  defaultGatherConfig,
	Combine: defaultCombineConfig,
	Subjob: SubjobConfig{
		MaxDepth: 3,
	},
	TaskPool:     defaultTaskPoolConfig,
	CombinerPool: defaultCombinerPoolConfig,
}

type Config struct {
	Path         PathConfig
	Task         TaskConfig
	Gather       GatherConfig
	Combine      CombineConfig
	Subjob       SubjobConfig
	TaskPool     TaskPoolConfig
	CombinerPool CombinerPoolConfig
}

type PathConfig struct {
	Count  BiasedIntConfig
	Length BiasedIntConfig
}
