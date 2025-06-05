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

func TestQueue_BasicFunctionality(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init(p)
	ctx := context.Background()

	// Test TryPopFront on empty queue
	_, ok := q.TryPopFront(p)
	require.False(t, ok)

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
	val, ok := q.TryPopFront(p)
	require.True(t, ok)
	require.Equal(t, 1, val)
	require.Equal(t, 1, <-values)

	// Consume second value with PopFront
	val, err := q.PopFront(ctx, p)
	require.NoError(t, err)
	require.Equal(t, 2, val)
	require.Equal(t, 2, <-values)

	// Consume third value with TryPopFront
	val, ok = q.TryPopFront(p)
	require.True(t, ok)
	require.Equal(t, 3, val)
	require.Equal(t, 3, <-values)

	wg.Wait()

	// Queue should be empty now
	_, ok = q.TryPopFront(p)
	require.False(t, ok)
}

func TestQueue_ContextCancellation(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init(p)

	// Test receiver cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Start a receiver that will block
	done := make(chan struct{})
	go func() {
		_, err := q.PopFront(ctx, p)
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

func TestQueue_ReceiverThenSender(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init(p)
	ctx := context.Background()

	// Start receiver first
	received := make(chan int)
	go func() {
		if value, err := q.PopFront(ctx, p); err == nil {
			received <- value
		}
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	// Send value (do this in a goroutine since it will block until receiver processes it)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = q.PushBack(ctx, p, 42)
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
	q.Init(p)

	// Create multiple receivers that abandon their channels
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			// This will block and then abandon
			_, err := q.PopFront(ctx, p)
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
		_ = q.PushBack(ctx, p, 99)
	}()

	// New receiver should get the value
	received, err := q.PopFront(ctx, p)
	require.NoError(t, err)
	require.Equal(t, 99, received)
	wg.Wait()
}

func TestQueueConcurrency(t *testing.T) {
	var q rdvq.Queue[int]
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
				val, err := q.PopFront(readerCtx, p)
				if err != nil {
					return // Context cancelled
				}
				receivedValueMap[val].Add(1)
				totalPopped.Add(1)
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
				if err := q.PushBack(ctx, p, v); err != nil {
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
	_, ok := q.TryPopFront(p)
	require.False(t, ok, "Queue should be empty after all values consumed")
}

func TestQueue_StressWithAbandonments(t *testing.T) {
	var q rdvq.Queue[int]
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
					if _, err := q.PopFront(ctx, p); err == nil {
						popped.Add(1)
					}

				case 2: // Abandoning popper
					shortCtx, shortCancel := context.WithTimeout(ctx, time.Microsecond)
					if _, err := q.PopFront(shortCtx, p); err == nil {
						popped.Add(1)
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
		if _, ok := q.TryPopFront(p); !ok {
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
