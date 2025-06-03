// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package ubcq_test

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/ubcq"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

var intPool = &ubcq.Pool[int]{}

func TestQueue_BasicFunctionality(t *testing.T) {
	var q ubcq.Queue[int]
	q.Init(intPool)
	ctx := context.Background()

	// Test TryPopFront on empty queue
	_, ok := q.TryPopFront(intPool)
	require.False(t, ok)

	// Test adding and removing elements
	q.PushBack(intPool, 1)
	q.PushBack(intPool, 2)
	q.PushBack(intPool, 3)

	// Test TryPopFront
	val, ok := q.TryPopFront(intPool)
	require.True(t, ok)
	require.Equal(t, 1, val)

	// Test PopFront with context
	val, err := q.PopFront(ctx, intPool)
	require.NoError(t, err)
	require.Equal(t, 2, val)

	val, ok = q.TryPopFront(intPool)
	require.True(t, ok)
	require.Equal(t, 3, val)

	// Queue should be empty now
	_, ok = q.TryPopFront(intPool)
	require.False(t, ok)
}

func TestQueue_ContextCancellation(t *testing.T) {
	var q ubcq.Queue[int]
	q.Init(intPool)

	// Test receiver cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Start a receiver that will block
	done := make(chan struct{})
	go func() {
		_, err := q.PopFront(ctx, intPool)
		require.ErrorIs(t, err, context.Canceled)
		close(done)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for receiver to finish
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Receiver did not respond to context cancellation")
	}
}

func TestQueue_ReceiverThenSender(t *testing.T) {
	var q ubcq.Queue[int]
	q.Init(intPool)
	ctx := context.Background()

	// Start receiver first
	received := make(chan int)
	go func() {
		val, err := q.PopFront(ctx, intPool)
		if err != nil {
			t.Errorf("PopFront failed: %v", err)
			return
		}
		received <- val
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	// Send value
	q.PushBack(intPool, 42)

	// Verify receipt
	select {
	case val := <-received:
		require.Equal(t, 42, val)
	case <-time.After(time.Second):
		t.Fatal("Receiver did not receive value")
	}
}

func TestQueue_AbandonedReceivers(t *testing.T) {
	var q ubcq.Queue[int]
	q.Init(intPool)

	// Create multiple receivers that abandon their channels
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			_, _ = q.PopFront(ctx, intPool) // This will block and then abandon
		}()
		time.Sleep(5 * time.Millisecond)
		cancel()
	}

	// Give time for all receivers to register and abandon
	time.Sleep(50 * time.Millisecond)

	// Send a value - it should go to pendingValues since all receivers are abandoned
	q.PushBack(intPool, 99)

	// New receiver should get the value
	ctx := context.Background()
	val, err := q.PopFront(ctx, intPool)
	require.NoError(t, err)
	require.Equal(t, 99, val)
}

// TestQueueWithRapid uses property-based testing to verify correctness
func TestQueueWithRapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// The system under test
		var q ubcq.Queue[int]
		q.Init(intPool)
		ctx := context.Background()

		// The model (reference implementation)
		var model []int

		t.Repeat(map[string]func(*rapid.T){
			// PushBack operation
			"pushBack": func(t *rapid.T) {
				val := rapid.Int().Draw(t, "value")

				// Update actual implementation
				q.PushBack(intPool, val)

				// Update model
				model = append(model, val)
			},

			// TryPopFront operation
			"tryPopFront": func(t *rapid.T) {
				// Get actual value from queue
				val, ok := q.TryPopFront(intPool)

				if len(model) == 0 {
					// Should fail on empty queue
					require.False(t, ok, "TryPopFront succeeded on empty queue")
				} else {
					// Should succeed and return first value
					require.True(t, ok, "TryPopFront failed on non-empty queue")
					expected := model[0]
					model = model[1:]
					require.Equal(t, expected, val, "TryPopFront returned wrong value")
				}
			},

			// PopFront operation (blocking)
			"popFront": func(t *rapid.T) {
				if len(model) == 0 {
					t.Skip("Queue is empty, PopFront would block")
				}

				// Get expected value from model
				expected := model[0]
				model = model[1:]

				// Get actual value from queue
				val, err := q.PopFront(ctx, intPool)
				require.NoError(t, err)
				require.Equal(t, expected, val, "PopFront returned wrong value")
			},
		})
	})
}

func TestQueueConcurrency(t *testing.T) {
	var q ubcq.Queue[int]
	q.Init(intPool)
	ctx := context.Background()

	numReaders := max(1, runtime.NumCPU()/2)
	numWriters := max(1, runtime.NumCPU()/2)
	iterations := 100_000
	if testing.Short() {
		iterations /= 10
	}

	// Track which values were received
	receivedValueMap := make([]*atomic.Int32, numWriters*iterations)
	for i := range receivedValueMap {
		receivedValueMap[i] = &atomic.Int32{}
	}

	var readerWg, writerWg sync.WaitGroup
	readerWg.Add(numReaders)
	writerWg.Add(numWriters)

	startCh := make(chan struct{})
	var totalPushed, totalPopped atomic.Int64

	// Create cancellable context for readers
	readerCtx, cancelReaders := context.WithCancel(ctx)
	defer cancelReaders()

	// Start readers
	for id := 0; id < numReaders; id++ {
		go func() {
			defer readerWg.Done()
			<-startCh

			for {
				val, err := q.PopFront(readerCtx, intPool)
				if err != nil {
					return // Context cancelled
				}

				receivedValueMap[val].Add(1)
				if totalPopped.Add(1) >= int64(numWriters*iterations) {
					return
				}
			}
		}()
	}

	// Start writers
	for id := 0; id < numWriters; id++ {
		go func(writerID int) {
			defer writerWg.Done()
			<-startCh

			rangeStart := writerID * iterations
			rangeEnd := rangeStart + iterations
			for v := rangeStart; v < rangeEnd; v++ {
				q.PushBack(intPool, v)
				totalPushed.Add(1)
			}
		}(id)
	}

	// Start all goroutines
	close(startCh)

	// Wait for all writes to complete
	writerWg.Wait()

	// Wait for all values to be consumed or timeout
	deadline := time.Now().Add(5 * time.Second)
	for totalPopped.Load() < int64(numWriters*iterations) {
		if time.Now().After(deadline) {
			t.Fatalf("Timeout: only %d of %d values consumed", totalPopped.Load(), numWriters*iterations)
		}
		time.Sleep(time.Millisecond)
	}

	// Cancel reader context and wait for them to exit
	cancelReaders()
	readerWg.Wait()

	// Verify all values were received exactly once
	for i := 0; i < numWriters*iterations; i++ {
		count := receivedValueMap[i].Load()
		if count != 1 {
			t.Errorf("Value %d received %d times, expected 1", i, count)
		}
	}

	// Queue should be empty
	_, ok := q.TryPopFront(intPool)
	require.False(t, ok, "Queue should be empty after all values consumed")
}

func TestQueue_StressWithAbandonments(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	var q ubcq.Queue[int]
	q.Init(intPool)

	const (
		numGoroutines = 100
		duration      = 2 * time.Second
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		pushed    atomic.Int64
		popped    atomic.Int64
		abandoned atomic.Int64
	)

	// Start goroutines that randomly push, pop, or abandon
	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for ctx.Err() == nil {
				switch id % 3 {
				case 0: // Pusher
					q.PushBack(intPool, int(pushed.Add(1)))

				case 1: // Normal popper
					_, err := q.PopFront(ctx, intPool)
					if err == nil {
						popped.Add(1)
					}

				case 2: // Abandoning popper
					shortCtx, shortCancel := context.WithTimeout(ctx, time.Microsecond)
					_, err := q.PopFront(shortCtx, intPool)
					shortCancel()
					if err != nil {
						abandoned.Add(1)
					} else {
						popped.Add(1)
					}
				}

				// Small delay to make the test more realistic
				time.Sleep(time.Microsecond * time.Duration(id%10))
			}
		}(i)
	}

	// Let it run for a while
	time.Sleep(duration)
	cancel()
	wg.Wait()

	t.Logf("Pushed: %d, Popped: %d, Abandoned: %d", pushed.Load(), popped.Load(), abandoned.Load())

	// Drain any remaining values
	var remaining int64
	for {
		_, ok := q.TryPopFront(intPool)
		if !ok {
			break
		}
		remaining++
	}

	// Verify conservation: pushed = popped + remaining
	if pushed.Load() != popped.Load()+remaining {
		t.Errorf("Value conservation failed: pushed=%d, popped=%d, remaining=%d",
			pushed.Load(), popped.Load(), remaining)
	}
}
