// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/trace"

	"github.com/petenewcomb/psg-go/internal/nbcq"
)

// Optional implements the base layer of rdvq's two-tier architecture,
// providing direct sender-receiver rendezvous without overflow handling.
// It serves as the foundation for Required[T] and can be used standalone
// for simple rendezvous scenarios.
//
// Optional uses lock-free operations and eliminates channel contention
// by giving each receiver a dedicated inbox channel. Senders attempt
// direct handoff to waiting receivers, failing immediately if none are available.
type Optional[T any] struct {
	emptyInboxes nbcq.Queue[*Inbox[T]]
}

// Init initializes the queue. Must be called before first use.
func (q *Optional[T]) Init() {
	traceRegion := "rdvq.Optional.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Optional=%p, emptyInboxes=%p", q, &q.emptyInboxes)

	q.emptyInboxes.Init()
}

//nolint:contextcheck // background context used only for tracing
func (q *Optional[T]) TryPushBack(value T) bool {
	traceRegion := "rdvq.Optional.TryPushBack"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Optional=%p", q)

	// Loop through available inboxes
	for {
		inbox, ok := q.emptyInboxes.PopFront()
		if !ok {
			trace.Logf(context.Background(), traceRegion, "no empty inboxes to try, returning false")
			// No waiting emptyInboxes
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

// OptionalPopSelectFunc handles the select operation for PopFrontFunc.
// It should select on the inbox channel. The callback MUST call inbox.Emptied()
// if a value is received from the inbox.
type OptionalPopSelectFunc[T any] = func(inbox *Inbox[T])

func BasicOptionalPopSelect[T any](ctx context.Context, inbox *Inbox[T], processFn ProcessValueFunc[T]) error {
	traceRegion := "rdvq.BasicOptionalPopSelect"
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
func (q *Optional[T]) PopFrontFunc(
	inbox *Inbox[T],
	processOrphanFn ProcessValueFunc[T],
	selectFn OptionalPopSelectFunc[T],
) {
	traceRegion := "rdvq.Optional.PopFrontFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	if processOrphanFn == nil {
		panic("processOrphanFn is nil")
	}

	inboxCh := inbox.ch
	if inboxCh == nil {
		// New inbox, allocate a channel
		inboxCh = make(chan T, 1)
		inbox.ch = inboxCh
		trace.Logf(context.Background(), traceRegion, "Optional=%p inbox=%p allocated inboxCh=%p", q, inbox, inboxCh)
		q.emptyInboxes.PushBack(inbox)
	} else {
		// Reuse the existing inbox channel, but must check to see if it needs
		// draining or requeuing.
		select {
		case <-inboxCh:
			// We drained the abandonment marker, which confirms that the
			// channel has not yet been seen by TryPushBack. We can reuse it
			// without requeuing.
			trace.Logf(context.Background(), traceRegion,
				"Optional=%p inbox=%p reusing still-queued inboxCh=%p",
				q, inbox, inboxCh)
		default:
			// Channel was must have been drained by TryPushBack already. We can
			// reuse it but need to requeue.
			trace.Logf(context.Background(), traceRegion,
				"Optional=%p inbox=%p reusing and requeuing inboxCh=%p",
				q, inbox, inboxCh)
			q.emptyInboxes.PushBack(inbox)
		}
	}

	// Call the custom selecting function
	inbox.emptyPending()
	selectFn(inbox)
	if !inbox.WasEmptied() {
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

func (q *Optional[T]) PopFront(ctx context.Context, inbox *Inbox[T], processFn ProcessValueFunc[T]) error {
	var err error
	q.PopFrontFunc(inbox, processFn, func(inbox *Inbox[T]) {
		err = BasicOptionalPopSelect(ctx, inbox, processFn)
	})
	return err
}
