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
	"github.com/petenewcomb/psg-go/internal/trace"
	"github.com/stretchr/testify/assert"
)

func TestQueue_BasicFunctionality(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init()
	ctx := context.Background()

	// Test TryPopFront on empty queue
	value, ok := q.TryPopFront()
	assert.False(t, ok)
	assert.Equal(t, 0, value) // zero value for int

	// Test rendezvous pattern - producer blocks until consumer receives
	values := make(chan int, 3)
	var wg sync.WaitGroup

	// Start producer that will push 3 values
	var sender rdvq.Sender
	defer sender.Reset() // Free after all values are consumed
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 3; i++ {
			_ = q.PushBack(ctx, &sender, i, nil)
			values <- i
		}
		close(values)
	}()

	// Give producer a moment to start
	time.Sleep(10 * time.Millisecond)

	// Consume first value with TryPopFront
	value, ok = q.TryPopFront()
	assert.True(t, ok)
	assert.Equal(t, 1, value)
	assert.Equal(t, 1, <-values)

	// Consume remaining values with PopFront
	// Note: PopFront may process multiple values due to timing
	var receiver rdvq.Receiver
	var received []int
	for len(received) < 2 {
		err := q.PopFront(ctx, &receiver, func(value int) {
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
	value, ok = q.TryPopFront()
	assert.False(t, ok)
	assert.Equal(t, 0, value) // zero value
}

func TestQueue_ContextCancellation(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init()

	// Test receiver cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Start a receiver that will block
	done := make(chan struct{})
	go func() {
		var receiver rdvq.Receiver
		err := q.PopFront(ctx, &receiver, func(value int) {
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

func TestQueue_ReceiverThenSender(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init()
	ctx := context.Background()

	// Start receiver first
	received := make(chan int)
	go func() {
		var receiver rdvq.Receiver
		err := q.PopFront(ctx, &receiver, func(value int) {
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
		var sender rdvq.Sender
		defer sender.Reset()
		err := q.PushBack(ctx, &sender, 42, nil)
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

func TestQueue_AbandonedReceivers(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init()

	// Create multiple receivers that abandon their channels
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			var receiver rdvq.Receiver
			// This will block and then abandon
			err := q.PopFront(ctx, &receiver, func(value int) {
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
		var sender rdvq.Sender
		defer sender.Reset()
		err := q.PushBack(ctx, &sender, 99, nil)
		assert.NoError(t, err)
	}()

	// New receiver should get the value
	var receiver rdvq.Receiver
	var received []int
	err := q.PopFront(ctx, &receiver, func(value int) {
		received = append(received, value)
	})
	assert.NoError(t, err)
	assert.Len(t, received, 1)
	assert.Equal(t, 99, received[0])
	wg.Wait()
}

func TestQueue_Concurrency(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init()
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

			var receiver rdvq.Receiver
			for {
				err := q.PopFront(readerCtx, &receiver, func(val int) {
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

			var sender rdvq.Sender
			defer sender.Reset()
			rangeStart := writerID * iterations
			rangeEnd := rangeStart + iterations
			for v := rangeStart; v < rangeEnd; v++ {
				if err := q.PushBack(ctx, &sender, v, nil); err == nil {
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
	value, ok := q.TryPopFront()
	assert.False(t, ok, "Queue should be empty after all values consumed")
	assert.Equal(t, 0, value) // zero value for int
}

func TestQueue_Stress(t *testing.T) {
	traceRegion := "TestQueue_Stress"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	var q rdvq.Queue[int]
	q.Init()

	numPushers := runtime.GOMAXPROCS(-1)
	numPoppers := runtime.GOMAXPROCS(-1)
	duration := 10 * time.Second

	if testing.Short() {
		duration = 1 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		values       atomic.Int64
		pushed       atomic.Int64
		tryPushed    atomic.Int64
		pushRefused  atomic.Int64
		pushCanceled atomic.Int64
		popped       atomic.Int64
		tryPopped    atomic.Int64
		abandoned    atomic.Int64
	)

	pushErrCh := make(chan error, 1)
	popErrCh := make(chan error, 1)

	pushOps := []func(context.Context, *rdvq.Sender){
		func(ctx context.Context, sender *rdvq.Sender) {
			// Trying pusher
			tryPushed.Add(1)
			value := values.Add(1)
			if q.TryPushBack(sender, int(value), nil) {
				trace.Logf(ctx, traceRegion, "TryPushBack value=%d", value)
			} else {
				pushRefused.Add(1)
			}
		},
		func(ctx context.Context, sender *rdvq.Sender) {
			// Normal pusher
			pushed.Add(1)
			value := values.Add(1)
			err := q.PushBack(ctx, sender, int(value), nil)
			switch {
			case err == nil:
				trace.Logf(ctx, traceRegion, "PushBack value=%d", value)
			case errors.Is(err, context.Canceled) && ctx.Err() != nil:
				pushCanceled.Add(1)
			default:
				select {
				case pushErrCh <- err:
				default:
				}
			}
		},
	}

	popOps := []func(context.Context, *rdvq.Receiver){
		func(ctx context.Context, receiver *rdvq.Receiver) {
			// Normal popper
			err := q.PopFront(ctx, receiver, func(value int) {
				trace.Logf(ctx, traceRegion, "PopFront value=%d", value)
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
		func(ctx context.Context, receiver *rdvq.Receiver) {
			// Trying popper
			if value, ok := q.TryPopFront(); ok {
				trace.Logf(ctx, traceRegion, "TryPopFront value=%d", value)
				tryPopped.Add(1)
			}
		},
		func(ctx context.Context, receiver *rdvq.Receiver) {
			// Abandoning popper
			shortCtx, shortCancel := context.WithTimeout(ctx, 1*time.Nanosecond)
			err := q.PopFront(shortCtx, receiver, func(value int) {
				trace.Logf(ctx, traceRegion, "AbandoningPopFront value=%d", value)
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

			var receiver rdvq.Receiver
			for ctx.Err() == nil {
				//nolint:gosec // non-cryptographic use case
				popOps[rand.IntN(len(popOps))](ctx, &receiver)
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

			var sender rdvq.Sender
			defer sender.Reset()
			for ctx.Err() == nil {
				//nolint:gosec // non-cryptographic use case
				pushOps[rand.IntN(len(pushOps))](ctx, &sender)
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
		value, ok := q.TryPopFront()
		if !ok {
			break
		}
		trace.Logf(ctx, traceRegion, "TryPopFront value=%d", value)
		remaining++
	}

	//nolint:lll // doesn't make sense to break up more
	t.Logf("Pushed: %d, TryPushed: %d, PushRefused: %d, PushCanceled: %d, Popped: %d, TryPopped: %d, Abandoned: %d, Remaining: %d",
		pushed.Load(), tryPushed.Load(), pushRefused.Load(), pushCanceled.Load(), popped.Load(), tryPopped.Load(), abandoned.Load(), remaining)

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

	actuallyPushed := pushed.Load() + tryPushed.Load() - pushRefused.Load() - pushCanceled.Load()
	actuallyPopped := popped.Load() + tryPopped.Load() + remaining
	assert.Equal(t, actuallyPushed, actuallyPopped)
}

func TestQueue_TryPushBack(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init()
	ctx := context.Background()

	// TryPushBack should succeed when outbox is empty
	var sender rdvq.Sender
	defer sender.Reset()
	success := q.TryPushBack(&sender, 42, nil)
	assert.True(t, success, "TryPushBack should succeed with empty outbox")
	success = q.TryPushBack(&sender, 24, nil)
	assert.False(t, success, "TryPushBack should fail with full outbox and no waiting receivers")

	// Start a receiver
	received := make(chan int)
	go func() {
		var receiver rdvq.Receiver
		err := q.PopFront(ctx, &receiver, func(value int) {
			received <- value
		})
		assert.NoError(t, err)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	// TryPushBack should succeed now
	success = q.TryPushBack(&sender, 42, nil)
	assert.True(t, success, "TryPushBack should succeed with waiting receiver")

	// Verify the value was received
	select {
	case val := <-received:
		assert.Equal(t, 42, val)
	case <-time.After(time.Second):
		t.Fatal("Receiver did not receive value")
	}
}

// TestQueue_BufferedFuncOrdering pins the documented ordering guarantee for
// BufferedFunc: it must run synchronously and complete before any receiver can
// observe the buffered value via the queue.
func TestQueue_BufferedFuncOrdering(t *testing.T) {
	t.Run("fast path: empty outbox", func(t *testing.T) {
		var q rdvq.Queue[int]
		q.Init()

		var sender rdvq.Sender
		defer sender.Reset()

		var sawValueDuringBufferedFn bool
		bufferedFn := func() {
			if _, ok := q.TryPopFront(); ok {
				sawValueDuringBufferedFn = true
			}
		}

		ok := q.TryPushBack(&sender, 42, bufferedFn)
		assert.True(t, ok, "TryPushBack should succeed with empty outbox")
		assert.False(t, sawValueDuringBufferedFn,
			"value must not be observable via TryPopFront while bufferedFn runs")

		val, ok := q.TryPopFront()
		assert.True(t, ok)
		assert.Equal(t, 42, val)
	})

	t.Run("slow path: outbox full when selectFn runs", func(t *testing.T) {
		var q rdvq.Queue[int]
		q.Init()

		var sender rdvq.Sender
		defer sender.Reset()

		// Fill the outbox so the next send takes the slow path.
		ok := q.TryPushBack(&sender, 1, nil)
		assert.True(t, ok)

		var sawValueDuringBufferedFn bool
		bufferedFn := func() {
			if _, ok := q.TryPopFront(); ok {
				sawValueDuringBufferedFn = true
			}
		}

		// Custom selectFn drains the previous value to free outbox.ch, then
		// sends the new value, exercising the slow path synchronously.
		q.PushBackFunc(&sender, 2, bufferedFn, func(outbox *rdvq.Outbox[int]) {
			drained, ok := q.TryPopFront()
			assert.True(t, ok)
			assert.Equal(t, 1, drained)
			outbox.Ch() <- 2
			outbox.Filled()
		})

		assert.False(t, sawValueDuringBufferedFn,
			"value must not be observable via TryPopFront while bufferedFn runs")

		val, ok := q.TryPopFront()
		assert.True(t, ok)
		assert.Equal(t, 2, val)
	})
}

func TestQueue_ThreeTierDelivery(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init()
	ctx := context.Background()

	// Test Tier 1: Direct delivery to waiting receiver
	received := make(chan int, 1)
	go func() {
		var receiver rdvq.Receiver
		err := q.PopFront(ctx, &receiver, func(value int) {
			received <- value
		})
		assert.NoError(t, err)
	}()

	// Give receiver time to register
	time.Sleep(10 * time.Millisecond)

	var sender rdvq.Sender
	defer sender.Reset()
	err := q.PushBack(ctx, &sender, 100, nil)
	assert.NoError(t, err)

	// Should receive immediately via direct delivery
	select {
	case val := <-received:
		assert.Equal(t, 100, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Direct delivery failed")
	}

	// Test Tier 2: Outbox buffering when no receivers waiting
	err = q.PushBack(ctx, &sender, 200, nil)
	assert.NoError(t, err)

	// Test Tier 3: Shared channel when outbox is full
	done := make(chan struct{})
	go func() {
		defer close(done)
		// This should block on shared channel since outbox is full
		err := q.PushBack(ctx, &sender, 300, nil)
		assert.NoError(t, err)
	}()

	// Give sender time to start blocking
	time.Sleep(10 * time.Millisecond)

	// Start receiver to unblock the sender - need two PopFront calls:
	// one for the outboxed item (200) and one for the shared channel item (300)
	go func() {
		var receiver rdvq.Receiver

		// First PopFront should get the outboxed item
		err := q.PopFront(ctx, &receiver, func(value int) {
			received <- value
		})
		assert.NoError(t, err)

		// Second PopFront should get the shared channel item
		err = q.PopFront(ctx, &receiver, func(value int) {
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

func TestQueue_OutboxNotification(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init()
	ctx := context.Background()

	// Start a receiver that will block waiting for work
	received := make(chan int, 1)
	receiverStarted := make(chan struct{})
	go func() {
		close(receiverStarted)
		var receiver rdvq.Receiver
		err := q.PopFront(ctx, &receiver, func(value int) {
			received <- value
		})
		assert.NoError(t, err)
	}()

	// Wait for receiver to start waiting
	<-receiverStarted
	time.Sleep(10 * time.Millisecond)

	// Send item to outbox - this should notify the waiting receiver
	var sender rdvq.Sender
	defer sender.Reset()
	err := q.PushBack(ctx, &sender, 42, nil)
	assert.NoError(t, err)

	// Receiver should be notified and drain the outbox
	select {
	case val := <-received:
		assert.Equal(t, 42, val)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Outbox notification failed")
	}
}

func TestQueue_MultipleSendersWithSeparateOutboxes(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init()
	ctx := context.Background()

	numSenders := 5
	itemsPerSender := 10

	// Track received values
	received := make(chan int, numSenders*itemsPerSender)

	// Start receiver
	go func() {
		var receiver rdvq.Receiver
		for i := 0; i < numSenders*itemsPerSender; i++ {
			err := q.PopFront(ctx, &receiver, func(value int) {
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
			var sender rdvq.Sender
			defer sender.Reset()
			for i := 0; i < itemsPerSender; i++ {
				value := id*1000 + i // Unique value per sender
				err := q.PushBack(ctx, &sender, value, nil)
				assert.NoError(t, err)
			}
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

func TestQueue_TryPopFrontWithOutboxes(t *testing.T) {
	var q rdvq.Queue[int]
	q.Init()

	// TryPopFront should return immediately when no work available
	value, ok := q.TryPopFront()
	assert.False(t, ok)
	assert.Equal(t, 0, value) // zero value for int

	// Add item to outbox
	var sender rdvq.Sender
	defer sender.Reset()
	success := q.TryPushBack(&sender, 42, nil)
	assert.True(t, success) // Should go to outbox

	// TryPopFront should immediately drain the outbox
	value, ok = q.TryPopFront()
	assert.True(t, ok)
	assert.Equal(t, 42, value)
}
