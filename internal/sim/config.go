// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

var DefaultConfig = Config{
	Path: PathConfig{
		Count:  BiasedIntConfig{Min: 1, Med: 10, Max: 20},
		Length: BiasedIntConfig{Min: 1, Med: 3, Max: 5},
		//Duration: BiasedIntConfig{Min: 0, Med: 10*time.Millisecond, Max: 1 * time.Second},
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
