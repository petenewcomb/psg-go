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
		wasNotified := false
		waiters.WaitFuncWithOrphanHandler(
			&waiter,
			func() bool { return true },
			func(rdvq.RenotifyFunc) bool { panic("orphan notify") },
			func(waitInbox *rdvq.WaitInbox) {
				// Block waiting for notification - no default case
				ch := waitInbox.Ch()
				<-ch
				waitInbox.Emptied()
				wasNotified = true
			},
		)
		notified <- wasNotified
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
	waiters.WaitFuncWithOrphanHandler(
		&waiter,
		func() bool { return false },
		func(rdvq.RenotifyFunc) bool { panic("orphan notify") },
		func(waitInbox *rdvq.WaitInbox) {
			selectCalled = true
		},
	)

	// Verification returned false, so select function should not be called
	assert.False(t, selectCalled)
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
		wasNotified := false
		waiters.WaitFuncWithOrphanHandler(
			&waiter,
			func() bool {
				mu.Lock()
				defer mu.Unlock()
				return !workReady // Continue waiting only if no work ready
			},
			func(rdvq.RenotifyFunc) bool { panic("orphan notify") },
			func(waitInbox *rdvq.WaitInbox) {
				// When verification succeeds, this should be called and block
				ch := waitInbox.Ch()
				<-ch
				waitInbox.Emptied()
				wasNotified = true
			},
		)
		waitResult <- wasNotified
	}()

	// Wait for waiter to start
	<-waiterStarted
	time.Sleep(10 * time.Millisecond)

	// Send notification while no work is ready - should trigger select function
	waiters.Notify(nil)

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
	waiters.WaitFuncWithOrphanHandler(
		&waiter,
		func() bool {
			return !workReady // Should return false (don't wait)
		},
		func(rdvq.RenotifyFunc) bool { panic("orphan notify") },
		func(waitInbox *rdvq.WaitInbox) {
			selectCalled = true
		},
	)

	// Verification should have prevented waiting
	assert.False(t, selectCalled) // Select function should not be called
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
			waiters.WaitFuncWithOrphanHandler(
				&waiter,
				func() bool { return true },
				func(rdvq.RenotifyFunc) bool { panic("orphan notify") },
				func(waitInbox *rdvq.WaitInbox) {
					ch := waitInbox.Ch()
					select {
					case <-ch:
						waitInbox.Emptied()
						notifications <- id
					case <-time.After(200 * time.Millisecond):
						// timeout - don't call Notified
						notifications <- -1 // Indicate timeout/abort
					}
				},
			)
		}(waiterID)
	}

	// Give waiters time to register
	time.Sleep(20 * time.Millisecond)

	// Send notifications one by one
	for i := 0; i < numWaiters; i++ {
		waiters.Notify(nil)
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
			waiters.WaitFuncWithOrphanHandler(
				&waiter,
				func() bool { return true },
				func(rdvq.RenotifyFunc) bool { panic("orphan notify") },
				func(waitInbox *rdvq.WaitInbox) {
					ch := waitInbox.Ch()
					select {
					case <-ch:
						waitInbox.Emptied()
						notifications <- true
					case <-time.After(200 * time.Millisecond):
						notifications <- false
					}
				},
			)
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
		waiters.WaitFuncWithOrphanHandler(
			&waiter,
			func() bool { return true },
			func(rdvq.RenotifyFunc) bool { panic("orphan notify") },
			func(waitInbox *rdvq.WaitInbox) {
				// Abandon immediately - don't wait on channel
			},
		)
	}()

	// Give waiter time to register and abandon
	time.Sleep(10 * time.Millisecond)

	// Send notification - should be orphaned
	waiters.Notify(nil)

	notified := make(chan bool, 1)
	go func() {
		var waiter rdvq.Waiter
		waiters.WaitFuncWithOrphanHandler(
			&waiter,
			func() bool { return true },
			func(rdvq.RenotifyFunc) bool { panic("orphan notify") },
			func(waitInbox *rdvq.WaitInbox) {
				ch := waitInbox.Ch()
				select {
				case <-ch:
					waitInbox.Emptied()
					notified <- true
				case <-time.After(50 * time.Millisecond):
					notified <- false
				}
			},
		)
	}()

	// Give new waiter time to process orphaned notification
	time.Sleep(20 * time.Millisecond)

	// Send another notification for the new waiter
	waiters.Notify(nil)

	// Should receive notification (either orphaned one or new one)
	select {
	case result := <-notified:
		assert.True(t, result)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("New waiter did not receive notification")
	}
}
