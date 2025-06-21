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
	value, ok := q.TryPopFront(p)
	require.False(t, ok)
	require.Equal(t, 0, value) // zero value for int

	// Test rendezvous pattern - producer blocks until consumer receives
	values := make(chan int, 3)
	var wg sync.WaitGroup

	// Start producer that will push 3 values
	wg.Add(1)
	go func() {
		defer wg.Done()
		var outbox rdvq.Outbox[int]
		for i := 1; i <= 3; i++ {
			_ = q.PushBack(ctx, p, &outbox, i)
			values <- i
		}
		_ = outbox.Wait(ctx, p)
		close(values)
	}()

	// Give producer a moment to start
	time.Sleep(10 * time.Millisecond)

	// Consume first value with TryPopFront
	value, ok = q.TryPopFront(p)
	require.True(t, ok)
	require.Equal(t, 1, value)
	require.Equal(t, 1, <-values)

	// Consume second value with PopFront
	// Note: PopFront may process both an outbox value and an orphaned inbox value
	var received []int
	err := q.PopFront(ctx, p, func(value int) {
		received = append(received, value)
	})
	require.NoError(t, err)
	require.Len(t, received, 2)
	require.Contains(t, received, 2)
	require.Contains(t, received, 3)
	require.Equal(t, 2, <-values)
	require.Equal(t, 3, <-values)

	// All values should have been consumed by PopFront
	value, ok = q.TryPopFront(p)
	require.False(t, ok)
	require.Equal(t, 0, value) // zero value

	wg.Wait()

	// Queue should be empty now
	value, ok = q.TryPopFront(p)
	require.False(t, ok)
	require.Equal(t, 0, value) // zero value for int
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
		var outbox rdvq.Outbox[int]
		err := q.PushBack(ctx, p, &outbox, 42)
		require.NoError(t, err)
		err = outbox.Wait(ctx, p)
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
		var outbox rdvq.Outbox[int]
		err := q.PushBack(ctx, p, &outbox, 99)
		require.NoError(t, err)
		err = outbox.Wait(ctx, p)
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

			var outbox rdvq.Outbox[int]
			rangeStart := writerID * iterations
			rangeEnd := rangeStart + iterations
			for v := rangeStart; v < rangeEnd; v++ {
				if err := q.PushBack(ctx, p, &outbox, v); err == nil {
					totalPushed.Add(1)
				}
			}
			err := outbox.Wait(ctx, p)
			require.NoError(t, err)
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
	value, ok := q.TryPopFront(p)
	require.False(t, ok, "Queue should be empty after all values consumed")
	require.Equal(t, 0, value) // zero value for int
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

			var outbox rdvq.Outbox[int]
			for ctx.Err() == nil {
				switch id % 3 {
				case 0: // Pusher
					if q.PushBack(ctx, p, &outbox, int(pushed.Add(1))) != nil {
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
		_, ok := q.TryPopFront(p)
		if !ok {
			break
		}
		remaining++
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
	var outbox rdvq.Outbox[int]
	success := q.TryPushBack(p, &outbox, 42)
	require.True(t, success, "TryPushBack should succeed with empty outbox")
	success = q.TryPushBack(p, &outbox, 24)
	require.False(t, success, "TryPushBack should fail with full outbox and no waiting receivers")

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

	require.True(t, outbox.IsEmpty(p))

	// TryPushBack should succeed now
	success = q.TryPushBack(p, &outbox, 42)
	require.True(t, success, "TryPushBack should succeed with waiting receiver")

	require.False(t, outbox.IsEmpty(p))

	// Verify the value was received
	select {
	case val := <-received:
		require.Equal(t, 42, val)
	case <-time.After(time.Second):
		t.Fatal("Receiver did not receive value")
	}
}

func TestRequired_ThreeTierDelivery(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
	ctx := context.Background()

	// Test Tier 1: Direct delivery to waiting receiver
	received := make(chan int, 1)
	go func() {
		err := q.PopFront(ctx, p, func(value int) {
			received <- value
		})
		require.NoError(t, err)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	var outbox rdvq.Outbox[int]
	err := q.PushBack(ctx, p, &outbox, 100)
	require.NoError(t, err)

	// Should receive immediately via direct delivery
	select {
	case val := <-received:
		require.Equal(t, 100, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Direct delivery failed")
	}

	// Outbox should still be empty (direct delivery bypassed outbox)
	require.True(t, outbox.IsEmpty(p))

	// Test Tier 2: Outbox buffering when no receivers waiting
	err = q.PushBack(ctx, p, &outbox, 200)
	require.NoError(t, err)

	// Outbox should now contain the item
	require.False(t, outbox.IsEmpty(p))

	// Test Tier 3: Shared channel when outbox is full
	done := make(chan struct{})
	go func() {
		defer close(done)
		// This should block on shared channel since outbox is full
		err := q.PushBack(ctx, p, &outbox, 300)
		require.NoError(t, err)
	}()

	// Give sender time to start blocking
	time.Sleep(10 * time.Millisecond)

	// Start receiver to unblock the sender - need two PopFront calls:
	// one for the outboxed item (200) and one for the shared channel item (300)
	go func() {
		// First PopFront should get the outboxed item
		err := q.PopFront(ctx, p, func(value int) {
			received <- value
		})
		require.NoError(t, err)

		// Second PopFront should get the shared channel item
		err = q.PopFront(ctx, p, func(value int) {
			received <- value
		})
		require.NoError(t, err)
	}()

	// Should receive the outboxed item first
	select {
	case val := <-received:
		require.Equal(t, 200, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Outbox delivery failed")
	}

	// Should receive the shared channel item second
	select {
	case val := <-received:
		require.Equal(t, 300, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Shared channel delivery failed")
	}

	// Wait for the blocking sender to complete
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Sender did not complete")
	}
}

func TestRequired_OutboxNotification(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
	ctx := context.Background()

	// Start a receiver that will block waiting for work
	received := make(chan int, 1)
	receiverStarted := make(chan struct{})
	go func() {
		close(receiverStarted)
		err := q.PopFront(ctx, p, func(value int) {
			received <- value
		})
		require.NoError(t, err)
	}()

	// Wait for receiver to start waiting
	<-receiverStarted
	time.Sleep(10 * time.Millisecond)

	// Send item to outbox - this should notify the waiting receiver
	var outbox rdvq.Outbox[int]
	err := q.PushBack(ctx, p, &outbox, 42)
	require.NoError(t, err)

	// Receiver should be notified and drain the outbox
	select {
	case val := <-received:
		require.Equal(t, 42, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Outbox notification failed")
	}

	// Outbox should be empty now
	require.True(t, outbox.IsEmpty(p))
}

func TestRequired_MultipleSendersWithSeparateOutboxes(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
	ctx := context.Background()

	numSenders := 5
	itemsPerSender := 10

	// Track received values
	received := make(chan int, numSenders*itemsPerSender)

	// Start receiver
	go func() {
		for i := 0; i < numSenders*itemsPerSender; i++ {
			err := q.PopFront(ctx, p, func(value int) {
				received <- value
			})
			require.NoError(t, err)
		}
	}()

	// Start multiple senders, each with their own outbox
	var wg sync.WaitGroup
	for senderID := 0; senderID < numSenders; senderID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			var outbox rdvq.Outbox[int]

			for i := 0; i < itemsPerSender; i++ {
				value := id*1000 + i // Unique value per sender
				err := q.PushBack(ctx, p, &outbox, value)
				require.NoError(t, err)
			}

			// Wait for outbox to be drained
			err := outbox.Wait(ctx, p)
			require.NoError(t, err)
		}(senderID)
	}

	wg.Wait()

	// Verify all values were received
	receivedValues := make(map[int]bool)
	for i := 0; i < numSenders*itemsPerSender; i++ {
		select {
		case val := <-received:
			receivedValues[val] = true
		case <-time.After(time.Second):
			t.Fatal("Not all values received")
		}
	}

	// Verify we got all expected values
	require.Len(t, receivedValues, numSenders*itemsPerSender)
	for senderID := 0; senderID < numSenders; senderID++ {
		for i := 0; i < itemsPerSender; i++ {
			expectedValue := senderID*1000 + i
			require.True(t, receivedValues[expectedValue], "Missing value %d", expectedValue)
		}
	}
}

func TestRequired_TryPopFrontWithOutboxes(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)

	// TryPopFront should return immediately when no work available
	value, ok := q.TryPopFront(p)
	require.False(t, ok)
	require.Equal(t, 0, value) // zero value for int

	// Add item to outbox
	var outbox rdvq.Outbox[int]
	success := q.TryPushBack(p, &outbox, 42)
	require.True(t, success) // Should go to outbox

	// TryPopFront should immediately drain the outbox
	value, ok = q.TryPopFront(p)
	require.True(t, ok)
	require.Equal(t, 42, value)

	// Outbox should be empty now
	require.True(t, outbox.IsEmpty(p))
}

func TestRequired_OutboxWaitBehavior(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
	ctx := context.Background()

	var outbox rdvq.Outbox[int]

	// Wait on empty outbox should return immediately
	err := outbox.Wait(ctx, p)
	require.NoError(t, err)

	// Add item to outbox
	err = q.PushBack(ctx, p, &outbox, 42)
	require.NoError(t, err)
	require.False(t, outbox.IsEmpty(p))

	// Wait should block until outbox is drained
	waitDone := make(chan error)
	go func() {
		waitDone <- outbox.Wait(ctx, p)
	}()

	// Give wait time to start blocking
	time.Sleep(10 * time.Millisecond)

	select {
	case <-waitDone:
		t.Fatal("Wait returned too early")
	default:
		// Good, still blocking
	}

	// Drain the outbox
	value, ok := q.TryPopFront(p)
	require.True(t, ok)
	require.Equal(t, 42, value)

	// Wait should now complete
	select {
	case err := <-waitDone:
		require.NoError(t, err)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Wait did not complete after outbox was drained")
	}
}

func TestRequired_RaceConditionPrevention(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
	ctx := context.Background()

	// This test verifies that the waiter verification system prevents
	// race conditions between outbox checking and blocking

	iterations := 1000
	if testing.Short() {
		iterations = 100
	}

	for i := 0; i < iterations; i++ {
		received := make(chan int, 1)
		receiverStarted := make(chan struct{})

		// Start receiver
		go func() {
			close(receiverStarted)
			err := q.PopFront(ctx, p, func(value int) {
				received <- value
			})
			require.NoError(t, err)
		}()

		// Wait for receiver to start
		<-receiverStarted

		// Send item immediately - there's a race between receiver checking
		// outboxes and starting to block
		var outbox rdvq.Outbox[int]
		err := q.PushBack(ctx, p, &outbox, i)
		require.NoError(t, err)

		// Should always receive the value despite the race
		select {
		case val := <-received:
			require.Equal(t, i, val)
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Race condition detected at iteration %d", i)
		}
	}
}
