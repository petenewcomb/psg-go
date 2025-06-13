//go:build exclude

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

var intPool = &rdvq.Pool[int]{}

func TestQueue_BasicFunctionality(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init(intPool)
	ctx := context.Background()

	// Test TryPopFront on empty queue
	ok := q.TryPopFront(ctx, intPool, func(ctx context.Context, value int) {
		t.Error("Should not receive value from empty queue")
	})
	require.False(t, ok)

	// Test rendezvous pattern - producer blocks until consumer receives
	values := make(chan int, 3)
	var wg sync.WaitGroup

	// Start producer that will push 3 values
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 3; i++ {
			_ = q.PushBack(ctx, intPool, i)
			values <- i
		}
		close(values)
	}()

	// Consume first value with TryPopFront
	var val int
	// Give producer a moment to start
	time.Sleep(10 * time.Millisecond)
	ok = q.TryPopFront(ctx, intPool, func(ctx context.Context, value int) {
		val = value
	})
	require.True(t, ok)
	require.Equal(t, 1, val)
	require.Equal(t, 1, <-values)

	// Consume second value with PopFront
	var received int
	q.PopFront(ctx, intPool, func(ctx context.Context, value int) {
		received = value
	})
	require.Equal(t, 2, received)
	require.Equal(t, 2, <-values)

	// Consume third value with TryPopFront
	ok = q.TryPopFront(ctx, intPool, func(ctx context.Context, value int) {
		val = value
	})
	require.True(t, ok)
	require.Equal(t, 3, val)
	require.Equal(t, 3, <-values)

	wg.Wait()

	// Queue should be empty now
	ok = q.TryPopFront(ctx, intPool, func(ctx context.Context, value int) {
		t.Error("Should not receive value from empty queue")
	})
	require.False(t, ok)
}

func TestQueue_ContextCancellation(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init(intPool)

	// Test receiver cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Start a receiver that will block
	done := make(chan struct{})
	go func() {
		q.PopFront(ctx, intPool, func(ctx context.Context, value int) {
			t.Error("Should not receive value when cancelled")
		})
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
	var q rdvq.Queue[int]
	q.Init(intPool)
	ctx := context.Background()

	// Start receiver first
	received := make(chan int)
	go func() {
		q.PopFront(ctx, intPool, func(ctx context.Context, value int) {
			received <- value
		})
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	// Send value (do this in a goroutine since it will block until receiver processes it)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = q.PushBack(ctx, intPool, 42)
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

func TestQueue_AbandonedReceivers(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init(intPool)

	// Create multiple receivers that abandon their channels
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			q.PopFront(ctx, intPool, func(ctx context.Context, value int) {
				// This will block and then abandon
			})
		}()
		time.Sleep(5 * time.Millisecond)
		cancel()
	}

	// Give time for all receivers to register and abandon
	time.Sleep(50 * time.Millisecond)

	// Send a value - it should go to fallbackChan since all receivers are abandoned
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = q.PushBack(ctx, intPool, 99)
	}()

	// New receiver should get the value
	var received int
	q.PopFront(ctx, intPool, func(ctx context.Context, value int) {
		received = value
	})
	wg.Wait()
	require.Equal(t, 99, received)
}

func TestQueueConcurrency(t *testing.T) {
	var q rdvq.Queue[int]
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
				q.PopFront(readerCtx, intPool, func(ctx context.Context, val int) {
					receivedValueMap[val].Add(1)
					totalPopped.Add(1)
				})
				if readerCtx.Err() != nil {
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
				if q.PushBack(ctx, intPool, v) == nil {
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
	ok := q.TryPopFront(ctx, intPool, func(ctx context.Context, value int) {
		t.Error("Queue should be empty after all values consumed")
	})
	require.False(t, ok, "Queue should be empty after all values consumed")
}

func TestQueue_StressWithAbandonments(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init(intPool)

	const (
		numGoroutines = 100
		duration      = 2 * time.Second
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		pushed       atomic.Int64
		pushFailures atomic.Int64
		popped       atomic.Int64
		popAttempts  atomic.Int64
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
					if q.PushBack(ctx, intPool, int(pushed.Add(1))) != nil {
						pushFailures.Add(1)
					}

				case 1: // Normal popper
					q.PopFront(ctx, intPool, func(ctx context.Context, value int) {
						popped.Add(1)
					})

				case 2: // Abandoning popper
					shortCtx, shortCancel := context.WithTimeout(ctx, time.Microsecond)
					popAttempts.Add(1)
					q.PopFront(shortCtx, intPool, func(ctx context.Context, value int) {
						popped.Add(1)
					})
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

	t.Logf("Pushed: %d, Popped: %d, PushFailures: %d, PopAttempts: %d", pushed.Load(), popped.Load(), pushFailures.Load(), popAttempts.Load())

	// Drain any remaining values
	var remaining int64
	for {
		ok := q.TryPopFront(ctx, intPool, func(ctx context.Context, value int) {
			// Just counting, don't need the value
		})
		if !ok {
			break
		}
		remaining++
	}

	// Verify conservation: pushed = popped + remaining
	if pushed.Load()-pushFailures.Load() != popped.Load()+remaining {
		t.Errorf("Value conservation failed: pushed=%d, popped=%d",
			pushed.Load()-pushFailures.Load(), popped.Load()+remaining)
	}
}
