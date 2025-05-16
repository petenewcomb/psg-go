// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package nbcq_test

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/nbcq"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// Add a basic functional test to verify operations directly
func TestQueueBasicFunctionality(t *testing.T) {
	q := nbcq.Queue[int]{}
	q.Init()

	// Test empty queue
	_, ok := q.PopFront()
	require.False(t, ok)

	// Test adding and removing elements
	q.PushBack(1)
	q.PushBack(2)
	q.PushBack(3)

	val, ok := q.PopFront()
	require.True(t, ok)
	require.Equal(t, 1, val)

	val, ok = q.PopFront()
	require.True(t, ok)
	require.Equal(t, 2, val)

	val, ok = q.PopFront()
	require.True(t, ok)
	require.Equal(t, 3, val)

	_, ok = q.PopFront()
	require.False(t, ok)
}

// TestQueueWithRapid uses rapid state machine testing to verify queue
// correctness
func TestQueueWithRapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// The system under test
		q := nbcq.Queue[int]{}
		q.Init()

		// The model (reference implementation)
		var model []int

		t.Repeat(map[string]func(*rapid.T){
			// PushBack operation
			"pushBack": func(t *rapid.T) {
				// Generate a random value to push
				val := rapid.Int().Draw(t, "value")

				// Update actual implementation
				q.PushBack(val)

				// Update model
				model = append(model, val)
			},

			// PopFront operation
			"popFront": func(t *rapid.T) {
				// Skip if empty - nothing to pop
				if len(model) == 0 {
					t.Skip("Queue is empty, nothing to pop")
				}

				// Get expected value from model
				expected := model[0]
				model = model[1:]

				// Get actual value from queue
				val, ok := q.PopFront()

				// Verify the operation succeeded
				require.True(t, ok, "PopFront failed on non-empty queue")
				require.Equal(t, expected, val, "PopFront returned wrong value")
			},

			// Check invariants between actions
			"": func(t *rapid.T) {
				// If model is empty, verify queue behaves as empty
				if len(model) == 0 {
					_, ok := q.PopFront()
					require.False(t, ok, "PopFront should fail on empty queue")
				}
			},
		})
	})
}

func TestQueueConcurrency(t *testing.T) {
	var q nbcq.Queue[int]
	q.Init()
	chk := require.New(t)

	var numReaders = max(1, runtime.NumCPU()/2)
	var numWriters = max(1, runtime.NumCPU()/2)
	var iterations = 5_000_000
	if testing.Short() {
		iterations /= 5
	}

	// Tracking statistics for each reader and writer independently
	type readerStats struct {
		startTime  time.Time
		endTime    time.Time
		totalReads int
		minValue   int // Minimum value observed
		maxValue   int // Maximum value observed
	}

	type writerStats struct {
		startTime   time.Time
		endTime     time.Time
		totalWrites int
	}

	receivedValueMap := make([]*atomic.Int32, numWriters*iterations)
	for i := range receivedValueMap {
		receivedValueMap[i] = &atomic.Int32{}
	}

	// Pre-allocate slices for statistics with no need for synchronization
	readerData := make([]readerStats, numReaders)
	writerData := make([]writerStats, numWriters)

	// Initialize reader stats
	for i := range readerData {
		readerData[i].minValue = -1
		readerData[i].maxValue = -1
	}

	startTime := time.Now()

	var writerWg sync.WaitGroup
	writerWg.Add(numWriters)

	var readerWg sync.WaitGroup
	readerWg.Add(numReaders)

	var ready sync.WaitGroup
	ready.Add(numReaders + numWriters)

	// Channel to be closed when it's time for all the goroutines to begin work
	startCh := make(chan struct{})

	var writersDone atomic.Bool

	// Start readers
	for id := 0; id < numReaders; id++ {
		data := &readerData[id]
		go func() {
			defer func() {
				data.endTime = time.Now()
				readerWg.Done()
			}()

			ready.Done()
			<-startCh

			data.startTime = time.Now()

			for {
				v, ok := q.PopFront()
				if !ok {
					if writersDone.Load() {
						break
					}
					continue
				}
				// The writer explicitly adds one to the value that's pushed to
				// distinguish it from the zero value.
				if v == 0 {
					panic("v == 0")
				}
				v--
				data.totalReads++
				if data.minValue < 0 || v < data.minValue {
					data.minValue = v
				}
				data.maxValue = max(data.maxValue, v)
				receivedValueMap[v].Add(1)
			}
		}()
	}

	// Start writers
	for id := 0; id < numWriters; id++ {
		data := &writerData[id]
		go func() {
			defer func() {
				data.endTime = time.Now()
				writerWg.Done()
			}()

			ready.Done()
			<-startCh

			data.startTime = time.Now()

			rangeStart := id * iterations
			rangeEnd := rangeStart + iterations
			for v := rangeStart; v < rangeEnd; v++ {
				// Add one to the value that's pushed to distinguish it from the
				// zero value.
				q.PushBack(v + 1)
				data.totalWrites++
			}
		}()
	}

	ready.Wait()
	close(startCh)
	writerWg.Wait()
	writersDone.Store(true)
	readerWg.Wait()

	// Analyze the results after all goroutines have finished
	var latestReaderStart, earliestReaderEnd time.Time
	var latestWriterStart, earliestWriterEnd time.Time

	var maxReaderMinValue, minReaderMaxValue int
	for i, stats := range readerData {

		if i == 0 || stats.minValue > maxReaderMinValue {
			maxReaderMinValue = stats.minValue
		}
		if i == 0 || stats.maxValue > minReaderMaxValue {
			minReaderMaxValue = stats.maxValue
		}

		if stats.startTime.After(latestReaderStart) {
			latestReaderStart = stats.startTime
		}
		if earliestReaderEnd.IsZero() || stats.endTime.Before(earliestReaderEnd) {
			earliestReaderEnd = stats.endTime
		}
	}

	for _, stats := range writerData {
		if stats.startTime.After(latestWriterStart) {
			latestWriterStart = stats.startTime
		}
		if earliestWriterEnd.IsZero() || stats.endTime.Before(earliestWriterEnd) {
			earliestWriterEnd = stats.endTime
		}
	}

	// Verify timing overlaps to ensure contention
	t.Logf("Writers: all started by %v, first finished at %v",
		latestWriterStart.Sub(startTime), earliestWriterEnd.Sub(startTime))
	t.Logf("Readers: all started by %v, first finished at %v",
		latestReaderStart.Sub(startTime), earliestReaderEnd.Sub(startTime))

	// Either readers started before writers finished or writers started before readers finished
	latestStart := latestReaderStart
	if latestWriterStart.After(latestStart) {
		latestStart = latestWriterStart
	}
	earliestEnd := earliestReaderEnd
	if earliestWriterEnd.Before(earliestEnd) {
		earliestEnd = earliestWriterEnd
	}

	readerWriterTimeOverlap := earliestEnd.Sub(latestStart)
	chk.Greater(readerWriterTimeOverlap, time.Duration(0), "Readers and writers didn't operate concurrently")

	readerWriterValueOverlap := minReaderMaxValue - maxReaderMinValue
	chk.Greater(readerWriterValueOverlap, 0, "Readers did not receive fully overlapping value sets")

	t.Logf("Time overlap: all readers and all writers ran concurrently for %v", readerWriterTimeOverlap)

	// Calculate min/max/avg values observed per reader
	var minTotalReads, maxTotalReads, sumTotalReads int
	for i, stats := range readerData {
		sumTotalReads += stats.totalReads
		if i == 0 || stats.totalReads < minTotalReads {
			minTotalReads = stats.totalReads
		}
		if i == 0 || stats.totalReads > maxTotalReads {
			maxTotalReads = stats.totalReads
		}
	}

	var sumTotalWrites int
	for _, stats := range writerData {
		sumTotalWrites += stats.totalWrites
	}

	// Log statistics about values observed
	t.Logf("Min/Avg/Max values read per reader: %d/%.1f/%d",
		minTotalReads, float64(sumTotalReads)/float64(numReaders), maxTotalReads)

	chk.Equal(numWriters*iterations, sumTotalWrites)

	_, ok := q.PopFront()
	chk.False(ok)

	chk.Equal(numWriters*iterations, sumTotalReads)

	for i := range receivedValueMap {
		count := receivedValueMap[i].Load()
		chk.Equal(int32(1), count, "receivedValueMap[%d] = %d, expected 1", i, count)
	}
}
