// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgopt

import (
	"github.com/petenewcomb/psg-go/internal/opts"
)

// TaskPoolOption is a configuration option that can be applied to TaskPool.
//
// Available TaskPool configuration options:
//   - [WithMaxConcurrency] - Sets maximum number of concurrent tasks
type TaskPoolOption = opts.TaskPoolOption
