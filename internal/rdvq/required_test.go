// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/stretchr/testify/assert"
)

var p = &rdvq.Pool[int]{}

func TestRequired_BasicFunctionality(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
	ctx := context.Background()

	// Test TryPopFront on empty queue
	value, ok := q.TryPopFront(p)
	assert.False(t, ok)
	assert.Equal(t, 0, value) // zero value for int

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
	assert.True(t, ok)
	assert.Equal(t, 1, value)
	assert.Equal(t, 1, <-values)

	// Consume remaining values with PopFront
	// Note: PopFront may process multiple values due to timing
	var received []int
	for len(received) < 2 {
		err := q.PopFront(ctx, p, func(value int) {
			received = append(received, value)
		})
		assert.NoError(t, err)
	}

	// We should have received both values 2 and 3
	assert.Contains(t, received, 2)
	assert.Contains(t, received, 3)

	// All three values should already be available in the model
	assert.Equal(t, 2, <-values)
	assert.Equal(t, 3, <-values)

	// And the producer should be finished
	wg.Wait()

	// There should be no more values available
	value, ok = q.TryPopFront(p)
	assert.False(t, ok)
	assert.Equal(t, 0, value) // zero value
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
		assert.Error(t, err, "Should not receive value when cancelled")
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
		assert.NoError(t, err)
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
		assert.NoError(t, err)
		err = outbox.Wait(ctx, p)
		assert.NoError(t, err)
	}()
	defer wg.Wait()

	// Verify receipt
	select {
	case val := <-received:
		assert.Equal(t, 42, val)
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
			assert.Error(t, err)
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
		assert.NoError(t, err)
		err = outbox.Wait(ctx, p)
		assert.NoError(t, err)
	}()

	// New receiver should get the value
	var received []int
	err := q.PopFront(ctx, p, func(value int) {
		received = append(received, value)
	})
	assert.NoError(t, err)
	assert.Len(t, received, 1)
	assert.Equal(t, 99, received[0])
	wg.Wait()
}

func TestRequired_Concurrency(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
	ctx := context.Background()

	numReaders := max(1, runtime.GOMAXPROCS(-1)/2)
	numWriters := max(1, runtime.GOMAXPROCS(-1)/2)
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
			assert.NoError(t, err)
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
	assert.False(t, ok, "Queue should be empty after all values consumed")
	assert.Equal(t, 0, value) // zero value for int
}

func TestRequired_Stress(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)

	numPushers := runtime.GOMAXPROCS(-1)
	numPoppers := runtime.GOMAXPROCS(-1)
	numExcessPoppers := min(3, runtime.GOMAXPROCS(-1))
	duration := 10 * time.Second

	if testing.Short() {
		duration = 1 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		pushed    atomic.Int64
		tryPushed atomic.Int64
		refused   atomic.Int64
		canceled  atomic.Int64
		popped    atomic.Int64
		tryPopped atomic.Int64
		excess    atomic.Int64
		abandoned atomic.Int64
	)

	pushErrCh := make(chan error, 1)
	popErrCh := make(chan error, 1)

	pushOps := []func(context.Context, *rdvq.Outbox[int]){
		func(ctx context.Context, outbox *rdvq.Outbox[int]) {
			// Trying pusher
			if !q.TryPushBack(p, outbox, int(tryPushed.Add(1))) {
				refused.Add(1)
			}
		},
		func(ctx context.Context, outbox *rdvq.Outbox[int]) {
			// Normal pusher
			err := q.PushBack(ctx, p, outbox, int(pushed.Add(1)))
			switch {
			case err == nil:
			case errors.Is(err, context.Canceled) && ctx.Err() != nil:
				canceled.Add(1)
			default:
				select {
				case pushErrCh <- err:
				default:
				}
			}
		},
	}

	popOps := []func(context.Context){
		func(ctx context.Context) {
			// Normal popper
			err := q.PopFront(ctx, p, func(value int) {
				popped.Add(1)
			})
			switch {
			case err == nil:
			case errors.Is(err, context.Canceled) && ctx.Err() != nil:
			default:
				select {
				case popErrCh <- err:
				default:
				}
			}
		},
		func(ctx context.Context) {
			// Trying popper
			if _, ok := q.TryPopFront(p); ok {
				tryPopped.Add(1)
			}
		},
		func(ctx context.Context) {
			// Abandoning popper
			shortCtx, shortCancel := context.WithTimeout(ctx, 1*time.Nanosecond)
			err := q.PopFront(shortCtx, p, func(value int) {
				popped.Add(1)
			})
			switch {
			case err == nil:
			case errors.Is(err, context.DeadlineExceeded) && shortCtx.Err() != nil:
				abandoned.Add(1)
			case errors.Is(err, context.Canceled) && ctx.Err() != nil:
			default:
				select {
				case popErrCh <- err:
				default:
				}
			}
			shortCancel()
		},
	}

	var wg sync.WaitGroup

	for range numPoppers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Create a new context to reduce contention
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			for ctx.Err() == nil {
				//nolint:gosec // non-cryptographic use case
				popOps[rand.IntN(len(popOps))](ctx)
			}
		}()
	}

	// Start separate excess poppers, as they would otherwise block and slow
	// down the other poppers
	for range numExcessPoppers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Create a new context to reduce contention
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			for ctx.Err() == nil {
				_, err := q.PopFrontExcess(ctx, p)
				switch {
				case err == nil:
					excess.Add(1)
				case errors.Is(err, context.Canceled) && ctx.Err() != nil:
				default:
					select {
					case popErrCh <- err:
					default:
					}
				}
			}
		}()
	}

	for range numPushers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Create a new context to reduce contention
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			var outbox rdvq.Outbox[int]
			for ctx.Err() == nil {
				//nolint:gosec // non-cryptographic use case
				pushOps[rand.IntN(len(pushOps))](ctx, &outbox)
			}
		}()
	}

	// Let it run for a while
	time.Sleep(duration)
	cancel()
	wg.Wait()

	// Drain any remaining values
	var remaining int64
	for {
		_, ok := q.TryPopFront(p)
		if !ok {
			break
		}
		remaining++
	}

	t.Logf("Pushed: %d, TryPushed: %d, Refused: %d, Canceled: %d, Popped: %d, TryPopped: %d, Excess: %d, Abandoned: %d, Remaining: %d", pushed.Load(), tryPushed.Load(), refused.Load(), canceled.Load(), popped.Load(), tryPopped.Load(), excess.Load(), abandoned.Load(), remaining)

	// Error but context not cancelled - unexpected
	select {
	case err := <-pushErrCh:
		assert.NoError(t, err, "Unexpected error from PushBack")
	default:
	}

	select {
	case err := <-popErrCh:
		assert.NoError(t, err, "Unexpected error from PopFront")
	default:
	}

	actuallyPushed := pushed.Load() + tryPushed.Load() - refused.Load() - canceled.Load()
	actuallyPopped := popped.Load() + tryPopped.Load() + excess.Load() + remaining
	assert.Equal(t, actuallyPushed, actuallyPopped)
}

func TestRequired_TryPushBack(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
	ctx := context.Background()

	// TryPushBack should fail when no receivers are waiting
	var outbox rdvq.Outbox[int]
	success := q.TryPushBack(p, &outbox, 42)
	assert.True(t, success, "TryPushBack should succeed with empty outbox")
	success = q.TryPushBack(p, &outbox, 24)
	assert.False(t, success, "TryPushBack should fail with full outbox and no waiting receivers")

	// Start a receiver
	received := make(chan int)
	go func() {
		err := q.PopFront(ctx, p, func(value int) {
			received <- value
		})
		assert.NoError(t, err)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	assert.True(t, outbox.IsEmpty(p))

	// TryPushBack should succeed now
	success = q.TryPushBack(p, &outbox, 42)
	assert.True(t, success, "TryPushBack should succeed with waiting receiver")

	assert.False(t, outbox.IsEmpty(p))

	// Verify the value was received
	select {
	case val := <-received:
		assert.Equal(t, 42, val)
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
		assert.NoError(t, err)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	var outbox rdvq.Outbox[int]
	err := q.PushBack(ctx, p, &outbox, 100)
	assert.NoError(t, err)

	// Should receive immediately via direct delivery
	select {
	case val := <-received:
		assert.Equal(t, 100, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Direct delivery failed")
	}

	// Outbox should still be empty (direct delivery bypassed outbox)
	assert.True(t, outbox.IsEmpty(p))

	// Test Tier 2: Outbox buffering when no receivers waiting
	err = q.PushBack(ctx, p, &outbox, 200)
	assert.NoError(t, err)

	// Outbox should now contain the item
	assert.False(t, outbox.IsEmpty(p))

	// Test Tier 3: Shared channel when outbox is full
	done := make(chan struct{})
	go func() {
		defer close(done)
		// This should block on shared channel since outbox is full
		err := q.PushBack(ctx, p, &outbox, 300)
		assert.NoError(t, err)
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
		assert.NoError(t, err)

		// Second PopFront should get the shared channel item
		err = q.PopFront(ctx, p, func(value int) {
			received <- value
		})
		assert.NoError(t, err)
	}()

	// Should receive the outboxed item first
	select {
	case val := <-received:
		assert.Equal(t, 200, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Outbox delivery failed")
	}

	// Should receive the shared channel item second
	select {
	case val := <-received:
		assert.Equal(t, 300, val)
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
		assert.NoError(t, err)
	}()

	// Wait for receiver to start waiting
	<-receiverStarted
	time.Sleep(10 * time.Millisecond)

	// Send item to outbox - this should notify the waiting receiver
	var outbox rdvq.Outbox[int]
	err := q.PushBack(ctx, p, &outbox, 42)
	assert.NoError(t, err)

	// Receiver should be notified and drain the outbox
	select {
	case val := <-received:
		assert.Equal(t, 42, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Outbox notification failed")
	}

	// Outbox should be empty now
	assert.True(t, outbox.IsEmpty(p))
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
			assert.NoError(t, err)
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
				assert.NoError(t, err)
			}

			// Wait for outbox to be drained
			err := outbox.Wait(ctx, p)
			assert.NoError(t, err)
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
	assert.Len(t, receivedValues, numSenders*itemsPerSender)
	for senderID := 0; senderID < numSenders; senderID++ {
		for i := 0; i < itemsPerSender; i++ {
			expectedValue := senderID*1000 + i
			assert.True(t, receivedValues[expectedValue], "Missing value %d", expectedValue)
		}
	}
}

func TestRequired_TryPopFrontWithOutboxes(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)

	// TryPopFront should return immediately when no work available
	value, ok := q.TryPopFront(p)
	assert.False(t, ok)
	assert.Equal(t, 0, value) // zero value for int

	// Add item to outbox
	var outbox rdvq.Outbox[int]
	success := q.TryPushBack(p, &outbox, 42)
	assert.True(t, success) // Should go to outbox

	// TryPopFront should immediately drain the outbox
	value, ok = q.TryPopFront(p)
	assert.True(t, ok)
	assert.Equal(t, 42, value)

	// Outbox should be empty now
	assert.True(t, outbox.IsEmpty(p))
}

func TestRequired_OutboxWaitBehavior(t *testing.T) {
	var q rdvq.Required[int]
	q.Init(p)
	ctx := context.Background()

	var outbox rdvq.Outbox[int]

	// Wait on empty outbox should return immediately
	err := outbox.Wait(ctx, p)
	assert.NoError(t, err)

	// Add item to outbox
	err = q.PushBack(ctx, p, &outbox, 42)
	assert.NoError(t, err)
	assert.False(t, outbox.IsEmpty(p))

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
	assert.True(t, ok)
	assert.Equal(t, 42, value)

	// Wait should now complete
	select {
	case err := <-waitDone:
		assert.NoError(t, err)
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
			assert.NoError(t, err)
		}()

		// Wait for receiver to start
		<-receiverStarted

		// Send item immediately - there's a race between receiver checking
		// outboxes and starting to block
		var outbox rdvq.Outbox[int]
		err := q.PushBack(ctx, p, &outbox, i)
		assert.NoError(t, err)

		// Should always receive the value despite the race
		select {
		case val := <-received:
			assert.Equal(t, i, val)
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Race condition detected at iteration %d", i)
		}
	}
}
