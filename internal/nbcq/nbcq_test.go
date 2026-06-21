// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package nbcq_test

import (
	"context"
	"math/rand/v2"
	"runtime"
	"runtime/trace"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool/internal/nbcq"
	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

// Add a basic functional test to verify operations directly
func TestQueueBasicFunctionality(t *testing.T) {
	q := nbcq.Queue[int]{}
	q.Init()

	// Test empty queue
	_, ok := q.TryPopFront()
	assert.False(t, ok)

	// Test adding and removing elements
	q.PushBack(1)
	q.PushBack(2)
	q.PushBack(3)

	val, ok := q.TryPopFront()
	assert.True(t, ok)
	assert.Equal(t, 1, val)

	val, ok = q.TryPopFront()
	assert.True(t, ok)
	assert.Equal(t, 2, val)

	val, ok = q.TryPopFront()
	assert.True(t, ok)
	assert.Equal(t, 3, val)

	_, ok = q.TryPopFront()
	assert.False(t, ok)
}

// TestQueueWithRapid uses rapid state machine testing to verify queue
// correctness
func TestQueueWithRapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		traceRegion := "TestQueueWithRapid"
		defer trace.StartRegion(context.Background(), traceRegion).End()

		// The systems under test
		queues := make([]*nbcq.Queue[int], rapid.IntRange(1, 100).Draw(t, "queues"))

		// The models (reference implementation)
		models := make([][]int, len(queues))

		getQM := func() (*nbcq.Queue[int], *[]int) {
			i := rapid.IntRange(0, len(queues)-1).Draw(t, "value")
			q := queues[i]
			if q == nil {
				q = &nbcq.Queue[int]{}
				q.Init()
				queues[i] = q
			}
			return q, &models[i]
		}

		checkQM := func(q *nbcq.Queue[int], m *[]int) {
			// If model is empty, verify queue behaves as empty
			if len(*m) == 0 {
				_, ok := q.TryPopFront()
				assert.False(t, ok, "PopFront should fail on empty queue")
			}
		}

		t.Repeat(map[string]func(*rapid.T){
			// PushBack operation
			"pushBack": func(t *rapid.T) {
				q, m := getQM()

				// Generate a random value to push
				val := rapid.Int().Draw(t, "value")

				// Update actual implementation
				q.PushBack(val)

				// Update model
				*m = append(*m, val)

				checkQM(q, m)
			},

			// PopFront operation
			"popFront": func(t *rapid.T) {
				q, m := getQM()

				// Skip if empty - nothing to pop
				if len(*m) == 0 {
					t.Skip("Queue is empty, nothing to pop")
				}

				// Get expected value from model
				expected := (*m)[0]
				*m = (*m)[1:]

				// Get actual value from queue
				val, ok := q.TryPopFront()

				// Verify the operation succeeded
				assert.True(t, ok, "PopFront failed on non-empty queue")
				assert.Equal(t, expected, val, "PopFront returned wrong value")

				checkQM(q, m)
			},
		})
	})
}

func TestQueueConcurrency(t *testing.T) {
	activeQueues := make([]atomic.Pointer[nbcq.Queue[int]], 3)
	for i := range activeQueues {
		q := &nbcq.Queue[int]{}
		q.Init()
		activeQueues[i].Store(q)
	}
	chk := assert.New(t)

	var oldQueueMu sync.Mutex
	var oldQueues []*nbcq.Queue[int]
	stashOldQueue := func(q *nbcq.Queue[int]) {
		oldQueueMu.Lock()
		defer oldQueueMu.Unlock()
		oldQueues = append(oldQueues, q)
	}
	popOldQueue := func() *nbcq.Queue[int] {
		oldQueueMu.Lock()
		defer oldQueueMu.Unlock()
		if len(oldQueues) == 0 {
			return nil
		}
		q := oldQueues[len(oldQueues)-1]
		oldQueues = oldQueues[:len(oldQueues)-1]
		return q
	}

	var numReaders = max(1, runtime.GOMAXPROCS(-1)/2)
	var numWriters = max(1, runtime.GOMAXPROCS(-1)/2)
	var iterations = 5_000_000
	if testing.Short() {
		iterations /= 10
	}
	if raceEnabled {
		iterations /= 10
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

			pop := func(q *nbcq.Queue[int]) bool {
				v, ok := q.TryPopFront()
				if !ok {
					return false
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
				return true
			}

			for {
				i := rand.IntN(len(activeQueues))                      //nolint:gosec // not a crypto use case
				if rand.IntN(100*(id+1)) == 0 && !writersDone.Load() { //nolint:gosec // not a crypto use case
					newQ := &nbcq.Queue[int]{}
					newQ.Init()
					oldQ := activeQueues[i].Swap(newQ)
					stashOldQueue(oldQ)
				} else {
					q := activeQueues[i].Load()
					if !pop(q) && writersDone.Load() {
						break
					}
				}
			}

			for i := range activeQueues {
				q := activeQueues[i].Load()
				for pop(q) {
				}
			}
			for q := popOldQueue(); q != nil; q = popOldQueue() {
				for pop(q) {
				}
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
				q := activeQueues[rand.IntN(len(activeQueues))].Load() //nolint:gosec // not a crypto use case
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
	chk.Positive(readerWriterValueOverlap, "Readers did not receive fully overlapping value sets")

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

	for i := range activeQueues {
		q := activeQueues[i].Load()
		_, ok := q.TryPopFront()
		chk.False(ok)
	}
	for _, q := range oldQueues {
		_, ok := q.TryPopFront()
		chk.False(ok)
	}

	chk.Equal(numWriters*iterations, sumTotalReads)

	for i := range receivedValueMap {
		count := receivedValueMap[i].Load()
		chk.Equal(int32(1), count, "receivedValueMap[%d] = %d, expected 1", i, count)
	}
}
