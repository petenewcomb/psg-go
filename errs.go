// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import "github.com/petenewcomb/streampool/internal/cerr"

const ErrTaskPanicked = cerr.Error("task panicked")
const ErrFunnelPanicked = cerr.Error("funnel panicked")
const ErrFunnelFlushPanicked = cerr.Error("funnel flush panicked")
const ErrFunnelFactoryPanicked = cerr.Error("funnel factory panicked")
const ErrFunnelFactoryReturnedNil = cerr.Error("funnel factory returned nil")
const ErrWaveDone = cerr.Error("wave done")

// ErrWeightExceedsCapacity is the per-unit refusal a weighted limiter
// ([NewWeightedSemaphore]) returns when a demand's weight is permanently larger than the
// limiter's (nonzero) ceiling — infeasible at any point, so the framework fails the
// dispatch rather than waiting forever. Wrapped with the offending weight and capacity;
// match with errors.Is. A PAUSED weighted limiter (ceiling 0) waits instead, since a
// raise can admit the demand.
const ErrWeightExceedsCapacity = cerr.Error("weight exceeds semaphore capacity")
