// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package delayq implements a deadline-ordered queue intended for a
// model where multiple worker goroutines collectively service
// deadline-driven work without any single goroutine owning the
// timer-watching duty. Callers Schedule items with deadlines, Drain
// returns whichever have expired plus the next pending deadline, and
// a wake hook nudges parked workers when a newly scheduled deadline
// beats the current earliest.
//
// The deadline is supplied to [Queue.Schedule] and owned by the queue;
// the item type T need not expose a deadline of its own, so item state
// visible to user code cannot drift out of sync with the ordering the
// queue uses. The item is required only to track its heap position via
// the [Item] interface, which user code should treat as opaque queue
// bookkeeping.
//
// Typical use:
//
//	var q delayq.Queue[*myItem]
//	q.Init(wakeIdleWorker)
//
//	// Producer side
//	q.Schedule(item, deadline)
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
// / SetPosition pair is opaque queue bookkeeping: implementations
// store an int field and surface it through these methods; only the
// queue ever mutates it.
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

// Schedule records that item should be returned by a [Queue.Drain]
// once now reaches deadline. Calling Schedule again on an item
// already in the queue replaces its deadline. Safe for concurrent
// callers.
func (q *Queue[T]) Schedule(item T, deadline time.Time) {
	q.updates.PushBack(update[T]{item: item, deadline: deadline})
	q.lowerDeadline(nanosSinceEpoch(deadline))
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

	// Fold queued updates into the heap. heap.Push handles both fresh
	// inserts and in-place updates: it reads the item's Position and
	// either pushes a new entry or overwrites the slot at p-1 and
	// calls Fix.
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

	nowNanos := nanosSinceEpoch(now)

	for q.h.Len() > 0 {
		if nanosSinceEpoch(q.h.Peek().deadline) > nowNanos {
			break
		}
		ready = append(ready, q.h.Pop().item)
	}

	// Publish the new earliest deadline via a single CAS against the
	// pre-drain snapshot. If a concurrent Schedule changed the atomic
	// while Drain ran, the CAS fails — Schedule's lower value stands,
	// and its item (now in the updates nbcq) will surface on the next
	// Drain.
	newNext := noDeadline
	if q.h.Len() > 0 {
		newNext = nanosSinceEpoch(q.h.Peek().deadline)
	}
	q.nextDeadline.CompareAndSwap(pre, newNext)

	return ready, timeFromNanos(newNext)
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
