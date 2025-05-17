// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import "github.com/petenewcomb/psg-go/internal/cerr"

const ErrTaskPanicked = cerr.Error("task panicked")
const ErrCombinePanicked = cerr.Error("combine panicked")
const ErrCombinerFlushPanicked = cerr.Error("combiner flush panicked")
const ErrCombinerFactoryPanicked = cerr.Error("combiner factory panicked")
const ErrCombinerFactoryReturnedNil = cerr.Error("combiner factory returned nil")
const ErrJobDone = cerr.Error("job done")
