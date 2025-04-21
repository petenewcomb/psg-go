// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"sync/atomic"
)

// Supports a maximum of 2^31-1 in-flight tasks, and will panic on overflow or
// underflow.
type JobInFlightCounter struct {
	// Tracks tasks in the high 32 bits and gathers in the low 32 bits, but the
	// highest bit of each range is not used to store valid values. Because only
	// the task counter may be directly changed only by one at a time, and
	// because it is stored in the highest bits, the overall sign of the value
	// can be used to detect overflow and underflow. This is also true for
	// gather counter increments, since every gather increment is atomically
	// paired with a task decrement.
	v atomic.Int64
}

func (c *JobInFlightCounter) report(m string, v int64) {
	//fmt.Printf("%v: JobInFlightCounter(%p).%s: %016x (%d, %d)\n", time.Now(), c, m, uint64(v), int32(v>>32), int32(uint64(v)&(uint64(1)<<32-1)))
}

func (c *JobInFlightCounter) IncrementTasks() JobInFlightCounterStatus {
	newValue := c.v.Add(int64(1) << 32)
	c.report("IncrementTasks", newValue)
	if newValue < 0 {
		panic("overflow: too many tasks in flight")
	}
	return JobInFlightCounterStatus{v: newValue}
}

// Returns true if no more tasks are left in-flight.
func (c *JobInFlightCounter) DecrementTasks() JobInFlightCounterStatus {
	newValue := c.v.Add(-int64(1) << 32)
	c.report("DecrementTasks", newValue)
	if newValue < 0 {
		panic("underflow: there were no tasks in flight")
	}
	return JobInFlightCounterStatus{v: newValue}
}

// Returns true if no more tasks are left in-flight.
func (c *JobInFlightCounter) MoveTaskToGather() JobInFlightCounterStatus {
	newValue := c.v.Add(1 - int64(1)<<32)
	c.report("MoveTaskToGather", newValue)
	if newValue < 0 {
		panic("underflow: there were no tasks in flight")
	}
	return JobInFlightCounterStatus{v: newValue}
}

// Returns true if nothing is left in-flight.
func (c *JobInFlightCounter) DecrementGathers() JobInFlightCounterStatus {
	newValue := c.v.Add(-1)
	c.report("DecrementGathers", newValue)
	// If there were tasks but no gathers, subtracting 1 will flip all of the
	// low 32 bits to 1, including the "sign" bit.
	if newValue < 0 || newValue&(int64(1)<<31) != 0 {
		c.report("DecrementGathersPanic", newValue)
		panic("underflow: there were no gathers in flight")
	}
	return JobInFlightCounterStatus{v: newValue}
}

func (c *JobInFlightCounter) Status() JobInFlightCounterStatus {
	return JobInFlightCounterStatus{v: c.v.Load()}
}

type JobInFlightCounterStatus struct {
	v int64
}

func (s JobInFlightCounterStatus) HadTasksLeftInFlight() bool {
	return s.v > int64(1)<<32-1
}

func (s JobInFlightCounterStatus) HadGathersLeftInFlight() bool {
	return s.v&(int64(1)<<32-1) != 0
}

func (s JobInFlightCounterStatus) HadAnythingLeftInFlight() bool {
	return s.v != 0
}

func (s JobInFlightCounterStatus) HadNothingLeftInFlight() bool {
	return s.v == 0
}
