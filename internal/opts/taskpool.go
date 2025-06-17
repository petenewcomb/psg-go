// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package opts

// TaskPoolOption is a configuration option that can be applied to TaskPool.
type TaskPoolOption interface {
	applyToTaskPool(c taskPoolConfig)
}

type taskPoolConfig interface {
	SetMaxConcurrency(limit int)
}

func ApplyToTaskPool(c taskPoolConfig, options ...TaskPoolOption) {
	for _, opt := range options {
		opt.applyToTaskPool(c)
	}
}
