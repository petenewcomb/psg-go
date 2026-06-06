// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package delayq implements a deadline-ordered queue intended for a
// model where multiple worker goroutines collectively service
// deadline-driven work without any single goroutine owning the
// timer-watching duty. Callers Schedule each item to become ready at a
// given time, Drain returns whichever have become ready plus the next
// pending time, and a wake hook nudges parked workers when a newly
// scheduled time beats the current earliest.
//
// The time is supplied to [Queue.Schedule] as its "at" parameter
// (reading "schedule item at T") and owned by the queue, which stores it
// as the item's deadline — the time by which the queue must release the
// item to honor the request. The item type T need not expose a time of
// its own, so item state visible to user code cannot drift out of sync
// with the ordering the queue uses; the item is required only to track
// its heap position via the [Item] interface, which user code should
// treat as opaque queue bookkeeping.
//
// Typical use:
//
//	var q delayq.Queue[*myItem]
//	q.Init(wakeIdleWorker)
//
//	// Producer side
//	q.Schedule(item, at)
//
//	// Consumer side
//	ready, next := q.Drain(time.Now(), ready[:0])
//	for _, it := range ready { ... }
//	// Arm a timer for next (zero Time means the queue is empty).
//
// Choosing which worker drives the timer and how that role is handed
// off on worker exit is the caller's responsibility; [Queue.Yield]
// supports the latter.
package delayq

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petenewcomb/psg-go/internal/heap"
	"github.com/petenewcomb/psg-go/internal/nbcq"
)

// Item is the contract for an entry stored in a [Queue]. The Position
// / SetPosition pair is queue bookkeeping: implementations store an int
// field and surface it through these methods; only the queue ever
// mutates it. Its tri-state lets [Queue.Expedite] tell a never-scheduled
// item from one that was scheduled and has since drained:
//   - zero: never scheduled;
//   - positive: currently queued;
//   - negative: previously queued, since drained or removed.
type Item interface {
	Position() int
	SetPosition(int)
}

// noDeadline is the atomic sentinel for an empty queue.
const noDeadline int64 = math.MaxInt64

// epoch is the package-level reference point used to encode deadlines
// as monotonic-clock-relative int64 nanos in the next-deadline atomic.
// time.Time.Sub against this anchor strips wall-clock components when
// both deadlines came from time.Now(), giving the atomic a monotonic
// reading immune to NTP adjustments and similar wall-clock jumps.
var epoch = time.Now()

// nanosSinceEpoch encodes t as nanos since [epoch] for storage in the
// atomic. Returns [noDeadline] for the zero Time.
func nanosSinceEpoch(t time.Time) int64 {
	if t.IsZero() {
		return noDeadline
	}
	return t.Sub(epoch).Nanoseconds()
}

// timeFromNanos decodes nanos back into an absolute time.Time. Returns
// the zero Time for [noDeadline].
func timeFromNanos(nanos int64) time.Time {
	if nanos == noDeadline {
		return time.Time{}
	}
	return epoch.Add(time.Duration(nanos))
}

// entry is the by-value heap record pairing an item with its queue-
// managed deadline. Stored directly in the underlying heap slice; no
// per-entry pointer allocations. Less, Position, and SetPosition
// delegate the heap-position tracking to the embedded item — see the
// [Item] interface contract.
type entry[T Item] struct {
	item     T
	deadline time.Time
}

func (e entry[T]) Less(other entry[T]) bool { return e.deadline.Before(other.deadline) }
func (e entry[T]) Position() int            { return e.item.Position() }
func (e entry[T]) SetPosition(p int)        { e.item.SetPosition(p) }

// update records a Schedule or Remove request from outside the
// mutex. Drain folds these into the heap.
type update[T Item] struct {
	item     T
	deadline time.Time
	remove   bool
}

// Queue is a deadline-ordered queue of T values. The zero value is not
// usable; call [Queue.Init] before use.
type Queue[T Item] struct {
	mu      sync.Mutex
	h       heap.Heap[entry[T]]
	updates nbcq.Queue[update[T]]

	// nextDeadline is unix-nanos of the earliest known deadline.
	// [noDeadline] means empty / nothing scheduled.
	nextDeadline atomic.Int64

	// wake is invoked by Schedule when CAS-min lowers nextDeadline. It
	// is the caller's hook for nudging a parked worker (or arming a
	// timer) so the new earlier deadline is noticed. Nil disables the
	// wake call.
	wake func()
}

// Init prepares q for use. wake is invoked from [Queue.Schedule]
// whenever a newly scheduled deadline is earlier than any previously
// known one; pass nil if callers poll independently and do not need
// a wake notification.
func (q *Queue[T]) Init(wake func()) {
	q.updates.Init()
	q.nextDeadline.Store(noDeadline)
	q.wake = wake
}

// Schedule records that item should be returned by a [Queue.Drain] once
// now reaches at. Calling Schedule again on an item already in the queue
// replaces its time, so it doubles as reschedule. Safe for concurrent
// callers.
//
// The parameter is named for the call site ("schedule item at T"); the
// queue stores it as the item's deadline — the time by which the queue
// must release it to honor the request. at must be non-zero: the zero
// Time is the queue's "none" sentinel (an empty queue, a no-op Drain
// result), so scheduling with it is a programming error and panics. A
// caller wanting "ready now" passes time.Now(); a caller with no
// specific time supplies its own far-future placeholder.
func (q *Queue[T]) Schedule(item T, at time.Time) {
	if at.IsZero() {
		panic("delayq: Schedule with zero time")
	}
	q.updates.PushBack(update[T]{item: item, deadline: at})
	q.lowerDeadline(nanosSinceEpoch(at))
}

// Remove drops item from the queue. Use this when the caller intends
// to handle item out-of-band (e.g. flushing immediately because its
// deadline has already been reached) and wants to prevent it from
// being returned by a future Drain. Safe to call on an item that was
// never Scheduled or has already been drained. Safe for concurrent
// callers.
func (q *Queue[T]) Remove(item T) {
	q.updates.PushBack(update[T]{item: item, remove: true})
}

// lowerDeadline CAS-mins nextDeadline against d. If the atomic was
// actually lowered, the wake hook is called.
func (q *Queue[T]) lowerDeadline(d int64) {
	for {
		old := q.nextDeadline.Load()
		if d >= old {
			return
		}
		if q.nextDeadline.CompareAndSwap(old, d) {
			if q.wake != nil {
				q.wake()
			}
			return
		}
	}
}

// Drain returns all items whose deadline is at or before now,
// appended to the caller-supplied ready slice for the typical
// reuse-the-slice idiom, along with the earliest remaining deadline
// (the zero Time when the queue is empty). Drained items are no
// longer in the queue; the caller may [Queue.Schedule] them again.
// Safe for concurrent callers; one Drain at a time runs.
//
// An item Scheduled while a Drain is in progress may or may not be
// observed by that Drain — callers that need a strict happens-before
// guarantee should call Drain again.
func (q *Queue[T]) Drain(now time.Time, ready []T) (drained []T, next time.Time) {
	// Snapshot the atomic before mutating the heap. The post-Drain CAS
	// uses this snapshot so a concurrent Schedule (which lowers the
	// atomic past the snapshot) wins the race — its item is in the
	// nbcq and will be folded by the next Drain.
	pre := q.nextDeadline.Load()

	q.mu.Lock()
	defer q.mu.Unlock()

	q.foldUpdates()

	nowNanos := nanosSinceEpoch(now)

	for q.h.Len() > 0 {
		if nanosSinceEpoch(q.h.Peek().deadline) > nowNanos {
			break
		}
		ready = append(ready, q.h.Pop().item)
	}

	return ready, q.republishNext(pre)
}

// Expedite removes item from the queue and returns it so the caller can
// process it immediately, regardless of its deadline. It folds any
// pending Schedule/Remove updates first, so an item whose Schedule has
// not yet reached the heap is still found. Safe for concurrent callers;
// one heap mutation runs at a time.
//
// The outcome turns on item's tri-state position (see [Item]):
//   - currently queued: removed and returned as (item, true);
//   - previously queued, already drained or removed: a benign no-op,
//     returned as (zero, false) — the item is already on its way out;
//   - never scheduled: a programming error, so Expedite panics.
//
// Unlike [Queue.Remove] (which defers to the next Drain), Expedite acts
// synchronously: on return a found item is no longer in the heap, so the
// caller may hand it onward without risk of a later Drain also returning
// it.
func (q *Queue[T]) Expedite(item T) (T, bool) {
	pre := q.nextDeadline.Load()

	q.mu.Lock()
	defer q.mu.Unlock()

	q.foldUpdates()

	p := item.Position()
	if p == 0 {
		panic("delayq: Expedite of a never-scheduled item")
	}

	found := q.h.Remove(entry[T]{item: item})

	q.republishNext(pre)

	if !found {
		// p < 0: previously queued, already drained or removed.
		return *new(T), false
	}
	return item, true
}

// foldUpdates drains the pending-update nbcq into the heap. Must be
// called with q.mu held. heap.Push handles both fresh inserts and
// in-place updates: it reads the item's Position and either pushes a
// new entry or overwrites the slot at p-1 and calls Fix.
func (q *Queue[T]) foldUpdates() {
	for {
		op, ok := q.updates.TryPopFront()
		if !ok {
			break
		}
		if op.remove {
			q.h.Remove(entry[T]{item: op.item})
			continue
		}
		q.h.Push(entry[T]{item: op.item, deadline: op.deadline})
	}
}

// republishNext publishes the new earliest deadline via a single CAS
// against the pre-operation snapshot and returns it (the zero Time when
// the queue is empty). Must be called with q.mu held. If a concurrent
// Schedule changed the atomic while the heap was mutated, the CAS fails
// — Schedule's lower value stands, and its item (now in the updates
// nbcq) will surface on the next fold.
func (q *Queue[T]) republishNext(pre int64) time.Time {
	newNext := noDeadline
	if q.h.Len() > 0 {
		newNext = nanosSinceEpoch(q.h.Peek().deadline)
	}
	q.nextDeadline.CompareAndSwap(pre, newNext)
	return timeFromNanos(newNext)
}

// Yield fires the wake hook and forces the next [Queue.Drain] to
// observe an already-expired deadline regardless of the heap's actual
// state. A worker that has been driving the timer calls Yield as it
// exits so another worker takes over the role without waiting for the
// pending timer.
func (q *Queue[T]) Yield() {
	q.nextDeadline.Store(math.MinInt64)
	if q.wake != nil {
		q.wake()
	}
}
