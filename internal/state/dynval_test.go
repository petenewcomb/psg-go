// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDynamicValue_ZeroValue(t *testing.T) {
	chk := require.New(t)
	var dv DynamicValue[int]

	// Load from zero value should return zero and not panic
	value, ch := dv.Load()
	chk.Equal(0, value)
	chk.NotNil(ch)
}

func TestDynamicValue_Store(t *testing.T) {
	chk := require.New(t)
	var dv DynamicValue[int]

	// Store sets the value
	dv.Store(42)
	value, _ := dv.Load()
	chk.Equal(42, value)

	// A second store updates the value
	dv.Store(100)
	value, _ = dv.Load()
	chk.Equal(100, value)
}

func TestDynamicValue_LoadNotification(t *testing.T) {
	chk := require.New(t)
	var dv DynamicValue[int]

	// Get the initial value and change channel
	_, changeCh1 := dv.Load()

	// Store a new value
	dv.Store(2)

	// The first change channel should be closed now
	select {
	case <-changeCh1:
		// Expected - channel was closed
	default:
		chk.Fail("Expected change channel to be closed after Store")
	}

	// Get the new value and a new change channel
	_, changeCh2 := dv.Load()

	// This channel should be open
	select {
	case <-changeCh2:
		chk.Fail("Did not expect new change channel to be closed yet")
	default:
		// Expected - channel is open
	}

	// Store another value
	dv.Store(3)

	// Now the second channel should be closed
	select {
	case <-changeCh2:
		// Expected - channel was closed
	default:
		chk.Fail("Expected second change channel to be closed after Store")
	}
}

func TestDynamicValue_Concurrent(t *testing.T) {
	chk := require.New(t)
	var dv DynamicValue[int]

	const numGoroutines = 10
	const numIterations = 100

	// Used to track received notifications
	var notificationsReceived atomic.Int32

	// Pre-allocate slices for tracking watchers of each value
	// We know the values will be 0 to numIterations
	valueWatchers := make([]atomic.Int32, numIterations+1)

	// Channel to signal completion to readers
	done := make(chan struct{})

	// Goroutines that watch for changes
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j <= numIterations; j++ {
				val, changeCh := dv.Load()

				// Register that we're watching for this value
				// This is safe because we know val is in range [0, numIterations]
				valueWatchers[val].Add(1)

				// Wait for change notification or completion signal
				select {
				case <-changeCh:
					notificationsReceived.Add(1)
				case <-done:
					return
				}
			}
		}(i)
	}

	// Wait a bit to let goroutines start
	time.Sleep(10 * time.Millisecond)

	// Writer goroutine
	for i := 1; i <= numIterations; i++ {
		dv.Store(i)

		// Sleep to ensure all reader goroutines have time to see each value
		time.Sleep(5 * time.Millisecond)
	}

	// Signal readers to stop
	close(done)

	// Wait for all reader goroutines to finish
	wg.Wait()

	// Each goroutine should have received numIterations notifications
	expectedNotifications := int32(numGoroutines * numIterations)
	chk.Equal(expectedNotifications, notificationsReceived.Load())

	// Verify that all values were seen by exactly numGoroutines goroutines
	// With the sleeps in place, all goroutines should see every value
	expected := int32(numGoroutines)
	chk.Equal(expected, valueWatchers[0].Load(), "Initial value (0) should be seen by all goroutines")
	for i := 1; i <= numIterations; i++ {
		chk.Equal(expected, valueWatchers[i].Load(), "Value %d should be seen by all goroutines", i)
	}
}

func TestDynamicValue_RapidChanges(t *testing.T) {
	chk := require.New(t)
	var dv DynamicValue[int]

	// Get the initial change channel
	_, initialChangeCh := dv.Load()

	// Rapidly changing the value multiple times
	for i := 1; i <= 1000; i++ {
		dv.Store(i)
	}

	// Ensure initial channel was closed
	select {
	case <-initialChangeCh:
		// Expected - channel was closed
	default:
		chk.Fail("Expected initial change channel to be closed after rapid changes")
	}

	// Get the final value
	finalValue, _ := dv.Load()
	chk.Equal(1000, finalValue)
}

func TestDynamicValue_StressRaceDetection(t *testing.T) {
	var dv DynamicValue[int]
	chk := require.New(t)

	const numReaders = 100
	const numWriters = 100
	const iterations = 10000

	// Tracking statistics for each reader and writer independently
	type readerStats struct {
		startTime  time.Time
		endTime    time.Time
		totalReads int
		values     map[int]bool // Track unique values observed
		minValue   int          // Minimum value observed
		maxValue   int          // Maximum value observed
	}

	type writerStats struct {
		startTime   time.Time
		endTime     time.Time
		totalWrites int
	}

	// Pre-allocate slices for statistics with no need for synchronization
	readerData := make([]readerStats, numReaders)
	writerData := make([]writerStats, numWriters)

	// Initialize reader stats
	for i := range readerData {
		readerData[i].values = make(map[int]bool)
		readerData[i].minValue = -1
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

	// Channel to signal readers that writers are done
	writersDone := make(chan struct{})

	// Start readers
	for id := 0; id < numReaders; id++ {
		go func() {
			data := &readerData[id]
			defer func() {
				data.endTime = time.Now()
				readerWg.Done()
			}()

			ready.Done()
			<-startCh

			data.startTime = time.Now()

			for i := 0; ; i++ {
				val, ch := dv.Load()
				data.totalReads++

				// Track observed values
				if val >= 0 {
					data.values[val] = true

					// Track min/max value
					if data.minValue == -1 || val < data.minValue {
						data.minValue = val
					}
					if val > data.maxValue {
						data.maxValue = val
					}
				}

				// Alternately wait on the channel or yield
				if i%2 == 0 {
					select {
					case <-ch:
					case <-writersDone:
						return
					default:
						runtime.Gosched() // Yield to other goroutines
						break
					}
				}
			}
		}()
	}

	// Start writers
	for id := 0; id < numWriters; id++ {
		go func() {
			data := &writerData[id]
			defer func() {
				data.endTime = time.Now()
				writerWg.Done()
			}()

			ready.Done()
			<-startCh

			data.startTime = time.Now()

			for i := 0; i < iterations; i++ {
				// Create deterministic value that we can trace back to this writer
				val := id*iterations + i + 1 // +1 to avoid zero
				dv.Store(val)
				data.totalWrites++
				runtime.Gosched() // Yield to other goroutines
			}

		}()
	}

	ready.Wait()
	close(startCh)
	writerWg.Wait()
	close(writersDone)
	readerWg.Wait()

	// Analyze the results after all goroutines have finished
	var latestReaderStart, earliestReaderEnd time.Time
	var latestWriterStart, earliestWriterEnd time.Time
	var totalReads, totalUniqueValues, totalWithZeroValues int

	for _, stats := range readerData {
		totalReads += stats.totalReads
		totalUniqueValues += len(stats.values)

		if stats.minValue == 0 {
			totalWithZeroValues++
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

		// Verify all writers wrote the expected number of values
		chk.Equal(iterations, stats.totalWrites, "Writer didn't write all values")
	}

	// Verify timing overlaps to ensure contention
	t.Logf("Readers: all started by %v, first finished at %v",
		latestReaderStart.Sub(startTime), earliestReaderEnd.Sub(startTime))
	t.Logf("Writers: all started by %v, first finished at %v",
		latestWriterStart.Sub(startTime), earliestWriterEnd.Sub(startTime))

	// Either readers started before writers finished or writers started before readers finished
	latestStart := latestReaderStart
	if latestWriterStart.After(latestStart) {
		latestStart = latestWriterStart
	}
	earliestEnd := earliestReaderEnd
	if earliestWriterEnd.Before(earliestEnd) {
		earliestEnd = earliestWriterEnd
	}

	readerWriterOverlap := earliestEnd.Sub(latestStart)
	chk.Greater(readerWriterOverlap, time.Duration(0), "Readers and writers didn't operate concurrently")

	// Calculate min/max/avg values observed per reader
	minObserved := len(readerData[0].values)
	maxObserved := 0
	for _, stats := range readerData {
		if len(stats.values) < minObserved {
			minObserved = len(stats.values)
		}
		if len(stats.values) > maxObserved {
			maxObserved = len(stats.values)
		}
	}

	// Log statistics about values observed
	t.Logf("Total reads: %d", totalReads)
	t.Logf("Min/Avg/Max unique values per reader: %d/%.1f/%d",
		minObserved, float64(totalUniqueValues)/float64(numReaders), maxObserved)
	t.Logf("Readers that saw initial zero value: %d of %d", totalWithZeroValues, numReaders)
	t.Logf("Time overlap: readers and writers all ran concurrently for %v", readerWriterOverlap)

	// Calculate percentage of total possible values that were observed collectively
	totalPossibleValues := numWriters * iterations
	observedValueCount := 0
	allObservedValues := make(map[int]bool)
	for _, stats := range readerData {
		for val := range stats.values {
			allObservedValues[val] = true
		}
	}
	observedValueCount = len(allObservedValues)

	t.Logf("Unique values observed by at least one reader: %d of %d possible (%.1f%%)",
		observedValueCount, totalPossibleValues,
		float64(observedValueCount)*100/float64(totalPossibleValues))

	// Verify readers observed a substantial portion of values
	// Each reader should see a meaningful percentage of the written values
	minExpectedValuesPerReader := iterations / 5 // At least 20% of iterations per reader
	chk.Greater(minObserved, minExpectedValuesPerReader,
		"Each reader should observe at least %d unique values, but minimum was %d",
		minExpectedValuesPerReader, minObserved)

	// Verify total unique values observed across all readers
	// With continuous reading, we should collectively observe a substantial portion
	minExpectedTotalValues := iterations * numWriters / 2 // At least 50% of all possible values
	chk.Greater(observedValueCount, minExpectedTotalValues,
		"All readers should collectively observe at least %d unique values (50%%), but observed %d (%.1f%%)",
		minExpectedTotalValues, observedValueCount,
		float64(observedValueCount)*100/float64(totalPossibleValues))
}

func TestDynamicValue_AliasedChannels(t *testing.T) {
	chk := require.New(t)
	var dv DynamicValue[int]

	// Get the value and change channel twice
	_, changeCh1 := dv.Load()
	_, changeCh2 := dv.Load()

	// Both should refer to the same channel
	chk.Equal(changeCh1, changeCh2, "Expected same change channel from multiple Load calls before Store")

	// Store a new value
	dv.Store(100)

	// Both channels should be closed now
	select {
	case <-changeCh1:
		// Expected
	default:
		chk.Fail("Expected first change channel to be closed after Store")
	}

	select {
	case <-changeCh2:
		// Expected
	default:
		chk.Fail("Expected second change channel to be closed after Store")
	}

	// Get two new change channels
	_, changeCh3 := dv.Load()
	_, changeCh4 := dv.Load()

	// Both should refer to the same channel
	chk.Equal(changeCh3, changeCh4, "Expected same change channel from multiple Load calls before Store")

	// And they should be different from the previous ones
	chk.NotEqual(changeCh1, changeCh3, "Expected different change channels after Store")
}

func TestDynamicValue_DifferentTypes(t *testing.T) {
	chk := require.New(t)

	// Test with string
	var dvString DynamicValue[string]

	val, ch := dvString.Load()
	chk.Equal("", val)

	dvString.Store("world")
	select {
	case <-ch:
		// Expected - channel was closed
	default:
		chk.Fail("Expected change channel to be closed after Store")
	}

	val, _ = dvString.Load()
	chk.Equal("world", val)

	// Test with a struct
	type testStruct struct {
		Field1 int
		Field2 string
	}

	var dvStruct DynamicValue[testStruct]

	valStruct, _ := dvStruct.Load()
	chk.Equal(0, valStruct.Field1)
	chk.Equal("", valStruct.Field2)

	dvStruct.Store(testStruct{Field1: 100, Field2: "world"})

	valStruct, _ = dvStruct.Load()
	chk.Equal(100, valStruct.Field1)
	chk.Equal("world", valStruct.Field2)
}
