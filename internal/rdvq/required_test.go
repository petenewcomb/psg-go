// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq_test

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/stretchr/testify/require"
)

var p = &rdvq.Pool[int]{}

func TestRequired_BasicFunctionality(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
	ctx := context.Background()

	// Test TryPopFront on empty queue
	var received []int
	q.TryPopFront(p, func(value int) {
		received = append(received, value)
	})
	require.Empty(t, received)

	// Test rendezvous pattern - producer blocks until consumer receives
	values := make(chan int, 3)
	var wg sync.WaitGroup

	// Start producer that will push 3 values
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 3; i++ {
			_ = q.PushBack(ctx, p, i)
			values <- i
		}
		close(values)
	}()

	// Give producer a moment to start
	time.Sleep(10 * time.Millisecond)

	// Consume first value with TryPopFront
	received = nil
	q.TryPopFront(p, func(value int) {
		received = append(received, value)
	})
	require.Len(t, received, 1)
	require.Equal(t, 1, received[0])
	require.Equal(t, 1, <-values)

	// Consume second value with PopFront
	received = nil
	err := q.PopFront(ctx, p, func(value int) {
		received = append(received, value)
	})
	require.NoError(t, err)
	require.Len(t, received, 1)
	require.Equal(t, 2, received[0])
	require.Equal(t, 2, <-values)

	// Consume third value with TryPopFront
	received = nil
	q.TryPopFront(p, func(value int) {
		received = append(received, value)
	})
	require.Len(t, received, 1)
	require.Equal(t, 3, received[0])
	require.Equal(t, 3, <-values)

	wg.Wait()

	// Queue should be empty now
	received = nil
	q.TryPopFront(p, func(value int) {
		received = append(received, value)
	})
	require.Empty(t, received)
}

func TestRequired_ContextCancellation(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)

	// Test receiver cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Start a receiver that will block
	done := make(chan struct{})
	go func() {
		err := q.PopFront(ctx, p, func(value int) {
			t.Error("Should not receive value when cancelled")
		})
		require.Error(t, err, "Should not receive value when cancelled")
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

func TestRequired_ReceiverThenSender(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
	ctx := context.Background()

	// Start receiver first
	received := make(chan int)
	go func() {
		err := q.PopFront(ctx, p, func(value int) {
			received <- value
		})
		require.NoError(t, err)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	// Send value (do this in a goroutine since it will block until receiver processes it)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := q.PushBack(ctx, p, 42)
		require.NoError(t, err)
	}()
	defer wg.Wait()

	// Verify receipt
	select {
	case val := <-received:
		require.Equal(t, 42, val)
	case <-time.After(time.Second):
		t.Fatal("Receiver did not receive value")
	}
}

func TestRequired_AbandonedReceivers(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)

	// Create multiple receivers that abandon their channels
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			// This will block and then abandon
			err := q.PopFront(ctx, p, func(value int) {
				t.Error("Should not receive value when cancelled")
			})
			require.Error(t, err)
		}()
		time.Sleep(5 * time.Millisecond)
		cancel()
	}

	// Give time for all receivers to register and abandon
	time.Sleep(50 * time.Millisecond)

	// Send a value - it should go to sharedChan since all receivers are abandoned
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := q.PushBack(ctx, p, 99)
		require.NoError(t, err)
	}()

	// New receiver should get the value
	var received []int
	err := q.PopFront(ctx, p, func(value int) {
		received = append(received, value)
	})
	require.NoError(t, err)
	require.Len(t, received, 1)
	require.Equal(t, 99, received[0])
	wg.Wait()
}

func TestRequired_Concurrency(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
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
				err := q.PopFront(readerCtx, p, func(val int) {
					receivedValueMap[val].Add(1)
					totalPopped.Add(1)
				})
				if err != nil {
					return // Context cancelled
				}
				if totalPopped.Load() >= int64(numWriters*iterations) {
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
				if err := q.PushBack(ctx, p, v); err == nil {
					totalPushed.Add(1)
				}
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
	var remaining []int
	q.TryPopFront(p, func(value int) {
		remaining = append(remaining, value)
	})
	require.Empty(t, remaining, "Queue should be empty after all values consumed")
}

func TestRequired_StressWithAbandonments(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)

	const (
		numGoroutines = 100
		duration      = 2 * time.Second
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		pushed        atomic.Int64
		pushFailures  atomic.Int64
		popped        atomic.Int64
		popsAbandoned atomic.Int64
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
					if q.PushBack(ctx, p, int(pushed.Add(1))) != nil {
						pushFailures.Add(1)
					}

				case 1: // Normal popper
					err := q.PopFront(ctx, p, func(value int) {
						popped.Add(1)
					})
					if err != nil && ctx.Err() == nil {
						// Error but context not cancelled - unexpected
						t.Errorf("Unexpected PopFront error: %v", err)
					}

				case 2: // Abandoning popper
					shortCtx, shortCancel := context.WithTimeout(ctx, time.Microsecond)
					err := q.PopFront(shortCtx, p, func(value int) {
						popped.Add(1)
					})
					if err == nil {
						// Successfully got a value
					} else {
						popsAbandoned.Add(1)
					}
					shortCancel()
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

	t.Logf("Pushed: %d, Popped: %d, PushFailures: %d, PopsAbandoned: %d", pushed.Load(), popped.Load(), pushFailures.Load(), popsAbandoned.Load())

	// Drain any remaining values
	var remaining int64
	for {
		found := false
		q.TryPopFront(p, func(value int) {
			remaining++
			found = true
		})
		if !found {
			break
		}
	}

	// Verify conservation: pushed = popped + remaining
	if pushed.Load()-pushFailures.Load() != popped.Load()+remaining {
		t.Errorf("Value conservation failed: pushed=%d, popped=%d, remaining=%d",
			pushed.Load()-pushFailures.Load(), popped.Load(), remaining)
	}
}

func TestRequired_TryPushBack(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
	ctx := context.Background()

	// TryPushBack should fail when no receivers are waiting
	success := q.TryPushBack(p, 42)
	require.False(t, success, "TryPushBack should fail with no waiting receivers")

	// Start a receiver
	received := make(chan int)
	go func() {
		err := q.PopFront(ctx, p, func(value int) {
			received <- value
		})
		require.NoError(t, err)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	// TryPushBack should succeed now
	success = q.TryPushBack(p, 42)
	require.True(t, success, "TryPushBack should succeed with waiting receiver")

	// Verify the value was received
	select {
	case val := <-received:
		require.Equal(t, 42, val)
	case <-time.After(time.Second):
		t.Fatal("Receiver did not receive value")
	}
}
