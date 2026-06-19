// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import "github.com/petenewcomb/psg-go/internal/cerr"

const ErrTaskPanicked = cerr.Error("task panicked")
const ErrFunnelPanicked = cerr.Error("funnel panicked")
const ErrFunnelFlushPanicked = cerr.Error("funnel flush panicked")
const ErrFunnelFactoryPanicked = cerr.Error("funnel factory panicked")
const ErrFunnelFactoryReturnedNil = cerr.Error("funnel factory returned nil")
const ErrWaveDone = cerr.Error("wave done")
