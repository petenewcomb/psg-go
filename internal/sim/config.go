// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package sim

var DefaultConfig = Config{
	Pool: defaultPoolConfig,
	Path: PathConfig{
		Count:  BiasedIntConfig{Min: 1, Med: 10, Max: 20},
		Length: BiasedIntConfig{Min: 1, Med: 3, Max: 5},
		//Duration: BiasedIntConfig{Min: 0, Med: 10*time.Millisecond, Max: 1 * time.Second},
	},
	Task:     defaultTaskConfig,
	Gather:   defaultGatherConfig,
	Combiner: defaultCombinerConfig,
	Combine:  defaultCombineConfig,
	Subjob: SubjobConfig{
		MaxDepth: 3,
	},
}

type Config struct {
	Pool     PoolConfig
	Path     PathConfig
	Task     TaskConfig
	Gather   GatherConfig
	Combiner CombinerConfig
	Combine  CombineConfig
	Subjob   SubjobConfig
}

type PathConfig struct {
	Count  BiasedIntConfig
	Length BiasedIntConfig
}
