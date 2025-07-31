// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package jobstate

import (
	"context"
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/trace"
)

// lifecycleStage represents the possible stages in a job's lifecycle
type lifecycleStage int32

//go:generate go run golang.org/x/tools/cmd/stringer@v0.35.0 -type=lifecycleStage -linecomment
const (
	// stageOpen indicates that the job is accepting new tasks
	stageOpen lifecycleStage = iota // Open
	// stageClosed indicates that the job is closed for new tasks but
	// existing tasks continue to run
	stageClosed // Closed
	// stageFlushing indicates that all tasks have completed and the job
	// is waiting for combiners to finish
	stageFlushing // Flushing
	// stageDone indicates that the job is completely done, all tasks and
	// combiners have completed
	stageDone // Done
)

// JobState encapsulates the state management for a scatter-gather job
type JobState struct {
	currentStage    atomic.Int32    // Contains a lifecycleStage value
	inFlightWork    InFlightCounter // Tracks only executing work
	totalReferences InFlightCounter // Tracks both work and combiners
	nextFlushChan   atomic.Value    // Stores chan struct{} for flush signals
	doneChan        chan struct{}
	flushListener   atomic.Value // Stores func() callback for flush events
}

// Init initializes an uninitialized JobState to the Open stage, and must be
// called exactly once before any other methods. An Init method is provided
// instead of a New function because JobState is expected to be an embedded
// field of Job.
//
//nolint:contextcheck // background context used only for tracing
func (js *JobState) Init() {
	traceRegion := "JobState.Init"
	trace.Logf(context.Background(), traceRegion,
		"JobState=%p, inFlightWork=%p, totalReferences=%p",
		js, &js.inFlightWork, &js.totalReferences)

	js.currentStage.Store(int32(stageOpen))
	js.nextFlushChan.Store(make(chan struct{}))
	js.doneChan = make(chan struct{})
}

// IncrementWork increments both the work counter and total references counter
//
//nolint:contextcheck // background context used only for tracing
func (js *JobState) IncrementWork() {
	traceRegion := "JobState.IncrementWork"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "JobState=%p", js)
	js.totalReferences.Increment()
	js.inFlightWork.Increment()
}

// DecrementWork decrements the work counter and attempts stage transitions if needed
//
//nolint:contextcheck // background context used only for tracing
func (js *JobState) DecrementWork() {
	traceRegion := "JobState.DecrementWork"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "JobState=%p", js)

	noMoreWork := js.inFlightWork.Decrement()

	// Decrement the total references count and check if it hit zero. If noMoreWork is true,
	// js.noMoreWork will handle the transition logic. But if noMoreWork is false
	// and the total references counter hit zero, we need to call js.noMoreReferences directly to
	// handle the race condition where flushers complete between the decrement and
	// the IsZero() check in noMoreWork().
	noMoreReferences := js.totalReferences.Decrement()

	if noMoreWork {
		// Last work just completed.
		js.noMoreWork()
	} else if noMoreReferences {
		// Total references counter hit zero but work counter didn't - this means flushers completed
		js.noMoreReferences()
	}
}

//nolint:contextcheck // background context used only for tracing
func (js *JobState) RegisterFlusher() (nextFlush <-chan struct{}, unregister func()) {
	traceRegion := "JobState.RegisterFlusher"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "JobState=%p", js)

	js.totalReferences.Increment()

	return js.nextFlushChan.Load().(chan struct{}), func() {
		// Check if all references are done for Flushing → Done transition
		if js.totalReferences.Decrement() {
			// Last reference just completed (work or combiner)
			js.noMoreReferences()
		}
	}
}

// Close attempts to transition from Open to Closed.
//
//nolint:contextcheck // background context used only for tracing
func (js *JobState) Close() {
	traceRegion := "JobState.Close"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "JobState=%p", js)

	var swapped bool
	trace.WithRegion(context.Background(), traceRegion+".CompareAndSwap(open, closed)", func() {
		swapped = js.currentStage.CompareAndSwap(int32(stageOpen), int32(stageClosed))
	})
	if swapped {
		// Successfully changed from Open to Closed
		if js.inFlightWork.IsZero() {
			js.noMoreWork()
		}
	}
}

// Done returns the channel that will be closed when the job transitions to Done
func (js *JobState) Done() <-chan struct{} {
	return js.doneChan
}

// SetFlushListener sets the function to be called when all work has completed
// and the job is waiting for combiners to emit their results. Pass nil to remove
// any existing listener.
func (js *JobState) SetFlushListener(fn func()) {
	js.flushListener.Store(fn)
}

// PanicIfDone panics if the job is in the done stage
func (js *JobState) PanicIfDone() {
	if lifecycleStage(js.currentStage.Load()) == stageDone {
		panic("job is closed and no longer running")
	}
}

// noMoreWork attempts to transition from Closed to Flushing, and will also
// advance to Done by calling noMoreReferences if appropriate
//
//nolint:contextcheck // background context used only for tracing
func (js *JobState) noMoreWork() {
	traceRegion := "JobState.noMoreWork"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "JobState=%p", js)

	currentStage := lifecycleStage(js.currentStage.Load())

	// Try to transition from Closed to Flushing if needed
	if currentStage == stageClosed {
		var swapped bool
		trace.WithRegion(context.Background(), traceRegion+".CompareAndSwap(closed, flushing)", func() {
			swapped = js.currentStage.CompareAndSwap(int32(stageClosed), int32(stageFlushing))
		})
		if swapped {
			currentStage = stageFlushing
		}
	}

	// If totalReferences is zero, there is nothing left to do.
	if js.totalReferences.IsZero() {
		js.noMoreReferences()
		return
	}

	// Handle flush channel for flushing state
	if currentStage == stageFlushing {
		// Call the flushListener callback if set (before closing the channel)
		if fn, ok := js.flushListener.Load().(func()); ok && fn != nil {
			trace.WithRegion(context.Background(), traceRegion+".flushListener", fn)
		}

		// Create new channel and swap with old one
		newCh := make(chan struct{})
		oldCh := js.nextFlushChan.Swap(newCh).(chan struct{})
		// Close old channel after replacing it
		close(oldCh)
	}
}

// noMoreReferences attempts to transition from Flushing to Done
//
//nolint:contextcheck // background context used only for tracing
func (js *JobState) noMoreReferences() {
	traceRegion := "JobState.noMoreReferences"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "JobState=%p", js)

	var swapped bool
	trace.WithRegion(context.Background(), traceRegion+".CompareAndSwap(flushing, done)", func() {
		swapped = js.currentStage.CompareAndSwap(int32(stageFlushing), int32(stageDone))
	})
	if swapped {
		// Successfully changed from Flushing to Done
		trace.WithRegion(context.Background(), traceRegion+".close(js.doneChan)", func() {
			close(js.doneChan)
		})
	}
}
