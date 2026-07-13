// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package delayq_test

import (
	"math/rand/v2"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool/internal/delayq"
	"github.com/stretchr/testify/assert"
)

// testItem is the standard test fixture: a pointer-identity item that
// only tracks its heap position. The queue owns the deadline.
type testItem struct {
	id    int
	state delayq.ScheduledState
}

func (it *testItem) ScheduledState() *delayq.ScheduledState { return &it.state }

// Position is a test helper surfacing the queue-owned tri-state position.
func (it *testItem) Position() int { return it.state.Position() }

// epoch is a fixed reference point so deadlines in tests are easy to
// reason about regardless of wall time.
var epoch = time.Unix(1_700_000_000, 0)

func at(offsetMs int64) time.Time {
	return epoch.Add(time.Duration(offsetMs) * time.Millisecond)
}

func ids(items []*testItem) []int {
	out := make([]int, len(items))
	for i, it := range items {
		out[i] = it.id
	}
	return out
}

// assertTime compares two time.Time values by their instant rather than
// by struct equality, which would fail when one value carries the
// monotonic clock (e.g. derived from delayq's package epoch via
// epoch.Add) and the other does not (e.g. built with time.Unix).
func assertTime(t *testing.T, want, got time.Time) {
	t.Helper()
	if !want.Equal(got) {
		assert.Fail(t, "times differ", "want=%v got=%v", want, got)
	}
}

func TestEmpty(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	ready, next := q.Drain(time.Now(), nil)
	assert.Empty(t, ready)
	assert.True(t, next.IsZero())
}

func TestScheduleThenDrain(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	a := &testItem{id: 1}
	b := &testItem{id: 2}
	c := &testItem{id: 3}

	q.Schedule(a, at(10))
	q.Schedule(b, at(20))
	q.Schedule(c, at(5))

	// Drain at a time before the earliest deadline returns nothing
	// but reports the heap's earliest as next.
	ready, next := q.Drain(at(0), nil)
	assert.Empty(t, ready)
	assertTime(t, at(5), next)

	// Draining at the earliest deadline picks up c only.
	ready, next = q.Drain(at(5), nil)
	assert.Equal(t, []int{3}, ids(ready))
	assertTime(t, at(10), next)

	// Draining past the last deadline picks up everything remaining,
	// in deadline order, and leaves the queue empty.
	ready, next = q.Drain(at(100), nil)
	assert.Equal(t, []int{1, 2}, ids(ready))
	assert.True(t, next.IsZero())
}

func TestReusableReadySlice(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)
	q.Schedule(&testItem{id: 1}, at(10))

	pre := make([]*testItem, 0, 4)
	post, _ := q.Drain(at(20), pre)
	assert.Same(t, &pre[:1][0], &post[0])
}

func TestRescheduleUpdatesDeadline(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	a := &testItem{id: 1}
	q.Schedule(a, at(50))
	q.Drain(at(0), nil) // fold into heap; nothing expired

	// Now push the deadline earlier and re-Schedule. A subsequent
	// Drain at the new (earlier) time should release a.
	q.Schedule(a, at(10))

	ready, next := q.Drain(at(10), nil)
	assert.Equal(t, []int{1}, ids(ready))
	assert.True(t, next.IsZero())
}

func TestRescheduleMovesDeadlineLater(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	a := &testItem{id: 1}
	q.Schedule(a, at(10))
	q.Drain(at(0), nil)

	// Push the deadline further out; Drain at the original deadline
	// should not release a and should report the new deadline.
	q.Schedule(a, at(100))

	ready, next := q.Drain(at(10), nil)
	assert.Empty(t, ready)
	assertTime(t, at(100), next)

	ready, next = q.Drain(at(100), nil)
	assert.Equal(t, []int{1}, ids(ready))
	assert.True(t, next.IsZero())
}

func TestRemove(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	a := &testItem{id: 1}
	b := &testItem{id: 2}
	q.Schedule(a, at(10))
	q.Schedule(b, at(20))

	q.Remove(a)
	// A second Remove of the same item is a no-op.
	q.Remove(a)

	// Only b should drain.
	ready, next := q.Drain(at(100), nil)
	assert.Equal(t, []int{2}, ids(ready))
	assert.True(t, next.IsZero())
}

func TestRemoveNotInHeap(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	// Never Scheduled — Remove should be a safe no-op.
	q.Remove(&testItem{id: 1})

	ready, next := q.Drain(at(100), nil)
	assert.Empty(t, ready)
	assert.True(t, next.IsZero())
}

func TestWakeFiresOnLoweredDeadline(t *testing.T) {
	var wakes atomic.Int32
	var q delayq.Queue[*testItem]
	q.Init(func() { wakes.Add(1) })

	q.Schedule(&testItem{id: 1}, at(50))
	assert.Equal(t, int32(1), wakes.Load(), "first schedule lowers from noDeadline")

	// A higher deadline does not wake.
	q.Schedule(&testItem{id: 2}, at(100))
	assert.Equal(t, int32(1), wakes.Load())

	// A lower deadline does wake.
	q.Schedule(&testItem{id: 3}, at(10))
	assert.Equal(t, int32(2), wakes.Load())
}

func TestYieldWakes(t *testing.T) {
	var wakes atomic.Int32
	var q delayq.Queue[*testItem]
	q.Init(func() { wakes.Add(1) })

	q.Schedule(&testItem{id: 1}, at(1000))
	q.Drain(at(0), nil)
	assert.Equal(t, int32(1), wakes.Load())

	q.Yield()
	assert.Equal(t, int32(2), wakes.Load())
	// After Yield, the next worker calling Drain at the current wall
	// time will see the long-expired sentinel and the item will pop.
	ready, _ := q.Drain(time.Now(), nil)
	assert.Equal(t, []int{1}, ids(ready))
}

// TestDrainRaisesNextSafely confirms the invariant the comment in Drain
// promises: Drain reports the heap's new earliest as next, and a
// subsequent lower Schedule shows up on the following Drain.
func TestDrainRaisesNextSafely(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	q.Schedule(&testItem{id: 1}, at(100))
	q.Schedule(&testItem{id: 2}, at(200))

	ready, next := q.Drain(at(100), nil)
	assert.Equal(t, []int{1}, ids(ready))
	assertTime(t, at(200), next)

	q.Schedule(&testItem{id: 3}, at(150))
	ready, next = q.Drain(at(0), nil)
	assert.Empty(t, ready)
	assertTime(t, at(150), next)
}

func TestConcurrentSchedule(t *testing.T) {
	const N = 200
	var q delayq.Queue[*testItem]
	q.Init(nil)

	items := make([]*testItem, N)
	for i := range items {
		items[i] = &testItem{id: i}
	}

	var wg sync.WaitGroup
	wg.Add(N)
	for i := range items {
		go func(it *testItem, idx int) {
			defer wg.Done()
			q.Schedule(it, at(int64(idx+1)))
		}(items[i], i)
	}
	wg.Wait()

	// Drain at a far-future time should pop all items in deadline order.
	ready, next := q.Drain(at(int64(N+10)), nil)
	got := ids(ready)
	want := make([]int, N)
	for i := range want {
		want[i] = i
	}
	assert.Equal(t, want, got)
	assert.True(t, next.IsZero())
}

// TestConcurrentScheduleAndDrain runs many producers alongside a
// single drainer goroutine and confirms every scheduled item is
// eventually drained exactly once.
func TestConcurrentScheduleAndDrain(t *testing.T) {
	const Producers = 8
	const PerProducer = 200

	// Track wake-ups to confirm Schedule fires the wake hook at least
	// once during the run (not asserted exactly — Schedules may coalesce).
	var wakes atomic.Int32
	var q delayq.Queue[*testItem]
	q.Init(func() { wakes.Add(1) })

	items := make([]*testItem, Producers*PerProducer)
	deadlines := make([]time.Time, Producers*PerProducer)
	for i := range items {
		// Stagger deadlines slightly so the heap actually exercises
		// reordering, but keep them all in the past relative to the
		// drainer's Drain call so everything pops.
		items[i] = &testItem{id: i}
		deadlines[i] = at(int64(i))
	}

	// Shuffle so producer push order isn't deadline order. Test data only.
	//
	//nolint:gosec // deterministic shuffling for test coverage; not security-sensitive
	rng := rand.New(rand.NewPCG(0xDEADBEEF, 0xCAFEBABE))
	rng.Shuffle(len(items), func(a, b int) {
		items[a], items[b] = items[b], items[a]
		deadlines[a], deadlines[b] = deadlines[b], deadlines[a]
	})

	var producerWg sync.WaitGroup
	producerWg.Add(Producers)
	for p := 0; p < Producers; p++ {
		go func(slice []*testItem, dlSlice []time.Time) {
			defer producerWg.Done()
			for i, it := range slice {
				q.Schedule(it, dlSlice[i])
			}
		}(items[p*PerProducer:(p+1)*PerProducer], deadlines[p*PerProducer:(p+1)*PerProducer])
	}

	done := make(chan struct{})
	var drained []*testItem
	go func() {
		defer close(done)
		ready := make([]*testItem, 0, 64)
		for len(drained) < Producers*PerProducer {
			ready, _ = q.Drain(at(int64(len(items)*2)), ready[:0])
			drained = append(drained, ready...)
			// Tiny yield to let producers push more.
			time.Sleep(50 * time.Microsecond)
		}
	}()

	producerWg.Wait()
	<-done

	assert.Len(t, drained, Producers*PerProducer)
	// Every id appears exactly once.
	seen := make(map[int]int, len(drained))
	for _, it := range drained {
		seen[it.id]++
	}
	for i := 0; i < Producers*PerProducer; i++ {
		assert.Equal(t, 1, seen[i], "id %d should be drained exactly once", i)
	}
	assert.Positive(t, wakes.Load(), "Schedule should have woken the drainer at least once")
}

// TestDrainReturnsInDeadlineOrder asserts the heap ordering property:
// regardless of insertion order, Drain releases items by deadline.
func TestDrainReturnsInDeadlineOrder(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	deadlines := []int64{50, 10, 30, 20, 40}
	for i, ms := range deadlines {
		q.Schedule(&testItem{id: i}, at(ms))
	}

	ready, _ := q.Drain(at(100), nil)
	gotMs := make([]int64, len(ready))
	// Item exposes no deadline accessor, so derive expected ordering from
	// the test input order. Drained order should be ascending by deadline.
	idToDeadline := map[int]int64{}
	for i, ms := range deadlines {
		idToDeadline[i] = ms
	}
	for i, it := range ready {
		gotMs[i] = idToDeadline[it.id]
	}
	wantMs := append([]int64(nil), deadlines...)
	sort.Slice(wantMs, func(i, j int) bool { return wantMs[i] < wantMs[j] })
	assert.Equal(t, wantMs, gotMs)
}

// TestExpediteRemovesScheduledItem verifies that Expedite pulls a
// not-yet-due item out of the queue and that a later Drain no longer
// returns it.
func TestExpediteRemovesScheduledItem(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	a := &testItem{id: 1}
	b := &testItem{id: 2}
	q.Schedule(a, at(50))
	q.Schedule(b, at(60))

	got, ok := q.Expedite(a)
	assert.True(t, ok)
	assert.Equal(t, a, got)
	assert.Negative(t, a.Position(), "expedited item should report a removed (negative) position")

	// Draining well past both deadlines must return only b.
	ready, next := q.Drain(at(100), nil)
	assert.Equal(t, []int{2}, ids(ready))
	assert.True(t, next.IsZero())
}

// TestExpediteFoldsPendingSchedule verifies that Expedite folds a
// Schedule that has not yet reached the heap, so an item scheduled and
// immediately expedited is still found.
func TestExpediteFoldsPendingSchedule(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	a := &testItem{id: 1}
	q.Schedule(a, at(50)) // pending in the update nbcq, not yet folded

	got, ok := q.Expedite(a)
	assert.True(t, ok)
	assert.Equal(t, a, got)

	ready, next := q.Drain(at(100), nil)
	assert.Empty(t, ready)
	assert.True(t, next.IsZero())
}

// TestExpediteAlreadyGoneIsNoop verifies the benign no-op contract for
// items that were scheduled but have since left the queue: already
// drained, and already expedited.
func TestExpediteAlreadyGoneIsNoop(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	// Already drained.
	drained := &testItem{id: 2}
	q.Schedule(drained, at(10))
	ready, _ := q.Drain(at(100), nil)
	assert.Equal(t, []int{2}, ids(ready))
	_, ok := q.Expedite(drained)
	assert.False(t, ok)

	// Already expedited.
	once := &testItem{id: 3}
	q.Schedule(once, at(50))
	_, ok = q.Expedite(once)
	assert.True(t, ok)
	_, ok = q.Expedite(once)
	assert.False(t, ok)
}

// TestExpediteNeverScheduledPanics verifies the defensive contract: a
// never-scheduled item (Position zero) is a programming error.
func TestExpediteNeverScheduledPanics(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	never := &testItem{id: 1}
	assert.Zero(t, never.Position())
	assert.Panics(t, func() { q.Expedite(never) })
}

// TestExpediteUpdatesNextDeadline verifies that expediting the earliest
// item republishes the next-deadline reported by Drain.
func TestExpediteUpdatesNextDeadline(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)

	a := &testItem{id: 1}
	b := &testItem{id: 2}
	q.Schedule(a, at(10))
	q.Schedule(b, at(90))

	// Expedite the earliest; the queue's next due item is now b.
	_, ok := q.Expedite(a)
	assert.True(t, ok)

	ready, next := q.Drain(at(50), nil)
	assert.Empty(t, ready, "b is not yet due at t=50")
	assertTime(t, at(90), next)
}

// TestScheduleZeroPanics verifies the defensive contract: the zero Time
// is the queue's "none" sentinel, so scheduling with it is a programming
// error.
func TestScheduleZeroPanics(t *testing.T) {
	var q delayq.Queue[*testItem]
	q.Init(nil)
	assert.Panics(t, func() { q.Schedule(&testItem{id: 1}, time.Time{}) })
}
