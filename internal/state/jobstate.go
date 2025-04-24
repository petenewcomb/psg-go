// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"sync/atomic"
)

// lifecycleStage represents the possible stages in a job's lifecycle
type lifecycleStage int32

const (
	// stageOpen indicates that the job is accepting new tasks
	stageOpen lifecycleStage = iota
	// stageClosed indicates that the job is closed for new tasks but
	// existing tasks continue to run
	stageClosed
	// stageFlushing indicates that all tasks have completed and the job
	// is waiting for combiners to finish
	stageFlushing
	// stageDone indicates that the job is completely done, all tasks and
	// combiners have completed
	stageDone
)

// JobState encapsulates the state management for a scatter-gather job
type JobState struct {
	currentStage  atomic.Int32    // Contains a lifecycleStage value
	inFlightTasks InFlightCounter // Tracks only executing tasks
	inFlightTotal InFlightCounter // Tracks both tasks and combiners
	nextFlushChan atomic.Value    // Stores chan struct{} for flush signals
	doneChan      chan struct{}
}

// Init initializes an uninitialized JobState to the Open stage, and must be
// called exactly once before any other methods. An Init method is provided
// instead of a New function because JobState is expected to be an embedded
// field of Job.
func (js *JobState) Init() {
	js.currentStage.Store(int32(stageOpen))
	js.nextFlushChan.Store(make(chan struct{}))
	js.doneChan = make(chan struct{})
}

// IncrementTasks increments both the task counter and total counter
func (js *JobState) IncrementTasks() {
	js.inFlightTotal.Increment()
	js.inFlightTasks.Increment()
}

// IncrementCombiners increments only the total counter
func (js *JobState) IncrementCombiners() {
	js.inFlightTotal.Increment()
}

// DecrementTasks decrements the task counter and attempts stage transitions if needed
func (js *JobState) DecrementTasks() {
	// First, decrement task counter and attempt Closed → Flushing transition if
	// there are no more remaining.
	if js.inFlightTasks.Decrement() {
		// Last task just completed
		js.noMoreTasks()
	}

	// Then, decrement total counter and attempt Flushing → Done transition if
	// there is no work remaining (no tasks or combiners)
	if js.inFlightTotal.Decrement() {
		// Last piece of work just completed (task or combiner)
		js.noMoreWork()
	}
}

// DecrementCombiners decrements only the total counter
func (js *JobState) DecrementCombiners() {
	// Check if all work is done for Flushing → Done transition
	if js.inFlightTotal.Decrement() {
		// Last piece of work just completed (task or combiner)
		js.noMoreWork()
	}
}

// Close attempts to transition from Open to Closed.
func (js *JobState) Close() {
	if js.currentStage.CompareAndSwap(int32(stageOpen), int32(stageClosed)) {
		// Successfully changed from Open to Closed
		if js.inFlightTasks.IsZero() {
			js.noMoreTasks()
		}
	}
}

// NextFlush returns a channel that will be closed the next time the number of tasks
// reaches zero after Close() has been called. This channel should be captured and stored
// by combiners at creation time, as a new channel will be provided each time tasks
// complete during the flushing phase.
func (js *JobState) NextFlush() <-chan struct{} {
	return js.nextFlushChan.Load().(chan struct{})
}

// Done returns the channel that will be closed when the job transitions to Done
func (js *JobState) Done() <-chan struct{} {
	return js.doneChan
}

// PanicIfDone panics if the job is in the done stage
func (js *JobState) PanicIfDone() {
	if lifecycleStage(js.currentStage.Load()) == stageDone {
		panic("job is closed and no longer running")
	}
}

// noMoreTasks attempts to transition from Closed to Flushing, and will also
// advance to Done if appropriate
func (js *JobState) noMoreTasks() {
	currentStage := lifecycleStage(js.currentStage.Load())

	// Try to transition from Closed to Flushing if needed
	if currentStage == stageClosed {
		if js.currentStage.CompareAndSwap(int32(stageClosed), int32(stageFlushing)) {
			currentStage = stageFlushing
		}
	}

	// Handle flush channel for flushing state
	if currentStage == stageFlushing {
		// Create new channel and swap with old one
		newFlushCh := make(chan struct{})
		oldFlushCh := js.nextFlushChan.Swap(newFlushCh).(chan struct{})
		// Close old channel after replacing it
		close(oldFlushCh)
	}

	// If inFlightTotal is zero, there is nothing left to do.
	if js.inFlightTotal.IsZero() {
		js.noMoreWork()
	}
}

// noMoreWork attempts to transition from Flushing to Done
func (js *JobState) noMoreWork() {
	if js.currentStage.CompareAndSwap(int32(stageFlushing), int32(stageDone)) {
		// Successfully changed from Flushing to Done
		close(js.doneChan)
	}
}
