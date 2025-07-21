// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq_test

import (
	"sync"
	"testing"
	"time"

	"github.com/petenewcomb/psg-go/internal/rdvq"
	"github.com/stretchr/testify/assert"
)

func TestWaiters_BasicNotification(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	// No waiters - notification should be dropped
	waiters.Notify(nil)

	notified := make(chan bool, 1)
	waiterStarted := make(chan struct{})

	go func() {
		close(waiterStarted) // Signal that waiter is created

		var waiter rdvq.Waiter
		renotifyFn := waiters.WaitFuncWithOrphanHandler(
			&waiter,
			func() bool { return true },
			func(rdvq.RenotifyFunc) { panic("orphan notify") },
			func(ch <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
				// Block waiting for notification - no default case
				renotifyFn := <-ch
				return renotifyFn // Waiter was notified
			},
		)
		notified <- (renotifyFn != nil)
	}()

	// Wait for waiter to be created and start waiting
	<-waiterStarted
	time.Sleep(10 * time.Millisecond)

	// Send notification
	waiters.Notify(nil)

	// Should receive notification
	select {
	case result := <-notified:
		assert.True(t, result)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Notification not received")
	}
}

func TestWaiters_VerificationFunction(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	var waiter rdvq.Waiter
	selectCalled := false
	renotifyFn := waiters.WaitFuncWithOrphanHandler(
		&waiter,
		func() bool { return false },
		func(rdvq.RenotifyFunc) { panic("orphan notify") },
		func(ch <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
			selectCalled = true
			return nil
		},
	)

	// Verification returned false, so select function should not be called
	assert.False(t, selectCalled)
	assert.Nil(t, renotifyFn)
}

func TestWaiters_VerificationPreventsRace(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	workReady := false
	var mu sync.Mutex

	// Start the waiter in a goroutine
	waitResult := make(chan bool, 1)
	waiterStarted := make(chan struct{})

	go func() {
		close(waiterStarted)

		var waiter rdvq.Waiter
		renotifyFn := waiters.WaitFuncWithOrphanHandler(
			&waiter,
			func() bool {
				mu.Lock()
				defer mu.Unlock()
				return !workReady // Continue waiting only if no work ready
			},
			func(rdvq.RenotifyFunc) { panic("orphan notify") },
			func(ch <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
				// When verification succeeds, this should be called and block
				renotifyFn := <-ch
				return renotifyFn
			},
		)
		waitResult <- (renotifyFn != nil)
	}()

	// Wait for waiter to start
	<-waiterStarted
	time.Sleep(10 * time.Millisecond)

	// Send notification while no work is ready - should trigger select function
	waiters.Notify(func() {})

	// Waiter should receive notification and return SelectWaitSignaled
	select {
	case result := <-waitResult:
		assert.True(t, result) // Should return SelectWaitSignaled since waiter was notified
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Waiter did not respond to notification")
	}
}

func TestWaiters_VerificationPreventsFalseWait(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	workReady := true // Work is immediately ready

	var waiter rdvq.Waiter
	selectCalled := false
	renotifyFn := waiters.WaitFuncWithOrphanHandler(
		&waiter,
		func() bool {
			return !workReady // Should return false (don't wait)
		},
		func(rdvq.RenotifyFunc) { panic("orphan notify") },
		func(ch <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
			selectCalled = true
			return nil
		},
	)

	// Verification should have prevented waiting
	assert.False(t, selectCalled) // Select function should not be called
	assert.Nil(t, renotifyFn)     // Should return nil (not notified)
}

func TestWaiters_MultipleWaiters(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	numWaiters := 5
	notifications := make(chan int, numWaiters)

	// Start multiple waiters
	for i := 0; i < numWaiters; i++ {
		waiterID := i

		go func(id int) {
			var waiter rdvq.Waiter
			renotifyFn := waiters.WaitFuncWithOrphanHandler(
				&waiter,
				func() bool { return true },
				func(rdvq.RenotifyFunc) { panic("orphan notify") },
				func(ch <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
					select {
					case renotifyFn := <-ch:
						notifications <- id
						return renotifyFn
					case <-time.After(200 * time.Millisecond):
						return nil
					}
				},
			)
			if renotifyFn == nil {
				notifications <- -1 // Indicate timeout/abort
			}
		}(waiterID)
	}

	// Give waiters time to register
	time.Sleep(20 * time.Millisecond)

	// Send notifications one by one
	for i := 0; i < numWaiters; i++ {
		waiters.Notify(func() {})
	}

	// Collect notifications
	received := make(map[int]bool)
	for i := 0; i < numWaiters; i++ {
		select {
		case waiterID := <-notifications:
			assert.NotEqual(t, -1, waiterID, "Waiter timed out")
			assert.False(t, received[waiterID], "Waiter %d notified multiple times", waiterID)
			received[waiterID] = true
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("Did not receive notification %d", i)
		}
	}

	// All waiters should have been notified
	assert.Len(t, received, numWaiters)
}

func TestWaiters_NotifyAll(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	numWaiters := 3
	notifications := make(chan bool, numWaiters)

	// Start multiple waiters
	for i := 0; i < numWaiters; i++ {
		go func() {
			var waiter rdvq.Waiter
			renotifyFn := waiters.WaitFuncWithOrphanHandler(
				&waiter,
				func() bool { return true },
				func(rdvq.RenotifyFunc) { panic("orphan notify") },
				func(ch <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
					select {
					case renotifyFn := <-ch:
						notifications <- true
						return renotifyFn
					case <-time.After(200 * time.Millisecond):
						notifications <- false
						return nil
					}
				},
			)
			notifications <- (renotifyFn != nil)
		}()
	}

	// Give waiters time to register
	time.Sleep(20 * time.Millisecond)

	// Notify all at once
	waiters.NotifyAll()

	// All waiters should be notified
	for i := 0; i < numWaiters; i++ {
		select {
		case notified := <-notifications:
			assert.True(t, notified, "Waiter %d was not notified", i)
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("Waiter %d did not receive notification", i)
		}
	}
}

func TestWaiters_OrphanedNotifications(t *testing.T) {
	var waiters rdvq.Waiters
	waiters.Init()

	// Start waiting but abandon immediately
	go func() {
		var waiter rdvq.Waiter
		_ = waiters.WaitFuncWithOrphanHandler(
			&waiter,
			func() bool { return true },
			func(rdvq.RenotifyFunc) { panic("orphan notify") },
			func(ch <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
				// Abandon immediately
				return nil
			},
		)
	}()

	// Give waiter time to register and abandon
	time.Sleep(10 * time.Millisecond)

	// Send notification - should be orphaned
	waiters.Notify(func() {})

	notified := make(chan bool, 1)
	go func() {
		var waiter rdvq.Waiter
		renotifyFn := waiters.WaitFuncWithOrphanHandler(
			&waiter,
			func() bool { return true },
			func(rdvq.RenotifyFunc) { panic("orphan notify") },
			func(ch <-chan rdvq.RenotifyFunc) rdvq.RenotifyFunc {
				select {
				case renotifyFn := <-ch:
					notified <- true
					return renotifyFn
				case <-time.After(50 * time.Millisecond):
					notified <- false
					return nil
				}
			},
		)
		notified <- (renotifyFn != nil)
	}()

	// Give new waiter time to process orphaned notification
	time.Sleep(20 * time.Millisecond)

	// Send another notification for the new waiter
	waiters.Notify(func() {})

	// Should receive notification (either orphaned one or new one)
	select {
	case result := <-notified:
		assert.True(t, result)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("New waiter did not receive notification")
	}
}
