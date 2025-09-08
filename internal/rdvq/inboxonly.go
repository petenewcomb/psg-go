// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/petenewcomb/psg-go/internal/nbcq"
	"github.com/petenewcomb/psg-go/internal/trace"
)

type inboxQueueQueue[T any] = inboxOnlyQueue[T, inboxQueue[T], inboxQueueTrait[T]]
type inboxStackQueue[T any] = inboxOnlyQueue[T, inboxStack[T], inboxStackTrait[T]]

// inboxOnlyQueue implements the base layer of rdvq's two-tier architecture,
// providing direct sender-receiver rendezvous without overflow handling.
// It serves as the foundation for [Queue] and [Waiters].
//
// inboxOnlyQueue uses lock-free operations and eliminates channel contention
// by giving each receiver a dedicated inbox channel. Senders attempt
// direct handoff to waiting receivers, failing immediately if none are available.
//
// The type parameter C represents the concrete collection type (either inboxQueue
// or inboxStack), while CT is the trait type that provides operations on C.
type inboxOnlyQueue[T any, C any, CT emptyInboxesTrait[T, C]] struct {
	emptyInboxes C
}

// emptyInboxesTrait is the internal interface for managing collections of
// waiting consumer inboxes. It provides a unified abstraction over FIFO and LIFO
// consumer selection - the queue itself always delivers items in FIFO order, but
// this trait controls which waiting consumer receives the next item.
type emptyInboxesTrait[T any, C any] interface {
	// Init initializes the collection. Must be called before first use.
	Init(c *C)

	// Push adds an inbox to the collection.
	Push(c *C, inbox *Inbox[T])

	// TryPop attempts to remove and return an inbox from the collection.
	// Returns false if the collection is empty.
	TryPop(c *C) (*Inbox[T], bool)
}

// Init initializes the queue. Must be called before first use.
func (q *inboxOnlyQueue[T, C, CT]) Init() {
	traceRegion := "rdvq.inboxOnlyQueue.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "inboxOnlyQueue=%p, emptyInboxes=%p", q, &q.emptyInboxes)

	var ct CT
	ct.Init(&q.emptyInboxes)
}

//nolint:contextcheck // background context used only for tracing
func (q *inboxOnlyQueue[T, C, CT]) TryPushBack(value T) bool {
	traceRegion := "rdvq.inboxOnlyQueue.TryPushBack"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "inboxOnlyQueue=%p", q)

	var ct CT

	// Loop through available inboxes
	for {
		inbox, ok := ct.TryPop(&q.emptyInboxes)
		if !ok {
			trace.Logf(context.Background(), traceRegion, "no empty inboxes to try, returning false")
			// No waiting empty inboxes
			return false
		}

		inboxCh := inbox.ch // must be non-nil given that it was in the queue
		// Loop to (re)attempt sending to the inbox channel
		for {
			trace.Logf(context.Background(), traceRegion, "entering select: inbox=%p, inboxCh=%p", inbox, inboxCh)
			select {
			case inboxCh <- value:
				trace.Logf(context.Background(), traceRegion, "delivered value to inbox=%p inboxCh=%p, returning true",
					inbox, inboxCh)
				// Successfully delivered
				return true
			default:
			}

			// inboxCh is full which means that the receiver abandoned it. Drain
			// to notify the inbox that the channel is no longer in queue, then
			// loop and try another.
			select {
			case <-inboxCh:
				trace.Logf(context.Background(), traceRegion, "inboxCh=%p was full, trying next", inboxCh)
				inboxCh = nil
			default:
				// Channel was emptied since last attempt to send, so must about to be reused in PopFront
				trace.Logf(context.Background(), traceRegion, "inboxCh=%p was full but became empty, retrying delivery", inboxCh)
			}

			if inboxCh == nil {
				break
			}
		}
	}
}

// inboxOnlyPopSelectFunc handles the select operation for PopFrontFunc.
// It should select on the inbox channel. The callback MUST call inbox.Emptied()
// if a value is received from the inbox.
type inboxOnlyPopSelectFunc[T any] = func(inbox *Inbox[T])

func basicInboxOnlyPopSelect[T any](ctx context.Context, inbox *Inbox[T], processFn ProcessValueFunc[T]) error {
	traceRegion := "rdvq.basicInboxOnlyPopSelect"
	inboxCh := inbox.Ch()
	trace.Logf(ctx, traceRegion, "entering select: inbox=%p, inboxCh=%p", inbox, inboxCh)
	select {
	case value := <-inboxCh:
		inbox.Emptied()
		trace.Logf(ctx, traceRegion, "received value from inbox=%p, inboxCh=%p", inbox, inboxCh)
		processFn(value)
		return nil
	case <-ctx.Done():
		trace.Logf(ctx, traceRegion, "received context done signal")
		return ctx.Err()
	}
}

//nolint:contextcheck // background context used only for tracing
func (q *inboxOnlyQueue[T, C, CT]) PopFrontFunc(
	inbox *Inbox[T],
	processOrphanFn ProcessValueFunc[T],
	selectFn inboxOnlyPopSelectFunc[T],
) {
	traceRegion := "rdvq.inboxOnlyQueue.PopFrontFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	if processOrphanFn == nil {
		panic("processOrphanFn is nil")
	}

	var ct CT

	inboxCh := inbox.ch
	if inboxCh == nil {
		// New inbox, allocate a channel
		inboxCh = make(chan T, 1)
		inbox.ch = inboxCh
		trace.Logf(context.Background(), traceRegion, "inboxOnlyQueue=%p inbox=%p allocated inboxCh=%p", q, inbox, inboxCh)
		ct.Push(&q.emptyInboxes, inbox)
	} else {
		// Reuse the existing inbox channel, but must check to see if it needs
		// draining or requeuing.
		select {
		case <-inboxCh:
			// We drained the abandonment marker, which confirms that the
			// channel has not yet been seen by TryPushBack. We can reuse it
			// without requeuing.
			trace.Logf(context.Background(), traceRegion,
				"inboxOnlyQueue=%p inbox=%p reusing still-queued inboxCh=%p",
				q, inbox, inboxCh)
		default:
			// Channel was must have been drained by TryPushBack already. We can
			// reuse it but need to requeue.
			trace.Logf(context.Background(), traceRegion,
				"inboxOnlyQueue=%p inbox=%p reusing and requeuing inboxCh=%p",
				q, inbox, inboxCh)
			ct.Push(&q.emptyInboxes, inbox)
		}
	}

	// Call the custom selecting function
	inbox.emptyPending()
	selectFn(inbox)
	if !inbox.wasEmptied {
		// The channel may still be in the queue or contain an orphaned value,
		// so we must mark it abandoned or deal with the orphaned value.
		select {
		case inboxCh <- *new(T):
			// Marked channel as abandoned, will be ignored by TryPushBack
			// unless subsequently drained by the reuse logic above.
			trace.Logf(context.Background(), traceRegion, "marked inboxCh=%p abandoned", inboxCh)
		default:
			// Channel is full, drain the orphaned value and process it.
			orphan := <-inboxCh
			inbox.Emptied()
			trace.Logf(context.Background(), traceRegion, "drained orphan from inboxCh=%p", inboxCh)
			processOrphanFn(orphan)
		}
	}
}

func (q *inboxOnlyQueue[T, C, CT]) PopFront(ctx context.Context, inbox *Inbox[T], processFn ProcessValueFunc[T]) error {
	var err error
	q.PopFrontFunc(inbox, processFn, func(inbox *Inbox[T]) {
		err = basicInboxOnlyPopSelect(ctx, inbox, processFn)
	})
	return err
}

// inboxQueue is a FIFO collection of waiting consumer inboxes, implemented
// using a lock-free queue. This provides fair consumer selection - the
// consumer that has been waiting longest gets the next item.
type inboxQueue[T any] = nbcq.Queue[*Inbox[T]]

type inboxQueueTrait[T any] struct{}

func (inboxQueueTrait[T]) Init(q *nbcq.Queue[*Inbox[T]]) {
	q.Init()
}

func (inboxQueueTrait[T]) Push(q *nbcq.Queue[*Inbox[T]], inbox *Inbox[T]) {
	q.PushBack(inbox)
}

func (inboxQueueTrait[T]) TryPop(q *nbcq.Queue[*Inbox[T]]) (*Inbox[T], bool) {
	return q.TryPopFront()
}

// inboxStack is a LIFO collection of waiting consumer inboxes, implemented
// using a mutex-protected slice with an atomic empty flag. This provides
// "hot" consumer selection - the most recently active consumer gets the
// next item, enabling natural worker scaling through timeout.
type inboxStack[T any] struct {
	mu      sync.Mutex
	inboxes []*Inbox[T]
	empty   atomic.Bool // Atomic flag for lock-free empty check
}

type inboxStackTrait[T any] struct{}

func (inboxStackTrait[T]) Init(*inboxStack[T]) {}

func (inboxStackTrait[T]) Push(s *inboxStack[T], inbox *Inbox[T]) {
	s.mu.Lock()
	s.inboxes = append(s.inboxes, inbox)
	s.empty.Store(false)
	s.mu.Unlock()
}

func (inboxStackTrait[T]) TryPop(s *inboxStack[T]) (*Inbox[T], bool) {
	// Fast path: check if empty without acquiring lock
	if s.empty.Load() {
		return nil, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	i := len(s.inboxes) - 1
	if i < 0 {
		return nil, false
	}

	inbox := s.inboxes[i]
	s.inboxes[i] = nil // clear reference
	s.inboxes = s.inboxes[:i]

	if i == 0 {
		// We just removed the last item
		s.empty.Store(true)
	}

	return inbox, true
}
