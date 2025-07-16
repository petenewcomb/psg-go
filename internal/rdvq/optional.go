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
	emptyInboxes nbcq.Queue[chan T]
}

// Init initializes the queue. Must be called before first use.
func (q *Optional[T]) Init(p *Pool[T]) {
	traceRegion := "rdvq.Optional.Init"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Optional=%p, emptyInboxes=%p", q, &q.emptyInboxes)

	q.emptyInboxes.Init(&p.nodePool)
}

//nolint:contextcheck // background context used only for tracing
func (q *Optional[T]) TryPushBack(p *Pool[T], value T) bool {
	traceRegion := "rdvq.Optional.TryPushBack"
	defer trace.StartRegion(context.Background(), traceRegion).End()
	trace.Logf(context.Background(), traceRegion, "Optional=%p", q)

	for {
		inboxCh, ok := q.emptyInboxes.PopFront(&p.nodePool)
		if !ok {
			trace.Logf(context.Background(), traceRegion, "no empty inboxes to try, returning false")
			// No waiting emptyInboxes
			return false
		}

		// Try to send to this receiver
		trace.Logf(context.Background(), traceRegion, "entering select: inboxCh=%p", inboxCh)
		select {
		case inboxCh <- value:
			trace.Logf(context.Background(), traceRegion, "delivered value to inboxCh=%p, returning true", inboxCh)
			// Successfully delivered
			return true
		default:
			trace.Logf(context.Background(), traceRegion, "inboxCh=%p was full, trying next", inboxCh)
			// inboxCh is full which means that the receiver abandoned it.
			// Drain and put back in the pool, then loop and try getting
			// another.
			<-inboxCh
			p.putChan(inboxCh)
		}
	}
}

// OptionalPopSelectFunc handles the select operation for PopFrontFunc.
// It should select on the inbox channel, returning the appropriate result.
type OptionalPopSelectFunc[T any] = func(inboxCh <-chan T) SelectResult

//nolint:contextcheck // background context used only for tracing
func (q *Optional[T]) PopFrontFunc(p *Pool[T], processOrphanFn ProcessValueFunc[T], selectFn OptionalPopSelectFunc[T]) {
	traceRegion := "rdvq.Optional.PopFrontFunc"
	defer trace.StartRegion(context.Background(), traceRegion).End()

	// Register ourselves as a waiting receiver.
	inboxCh := p.getChan()
	q.emptyInboxes.PushBack(&p.nodePool, inboxCh)

	trace.Logf(context.Background(), traceRegion, "Optional=%p, inboxCh=%p", q, inboxCh)

	// Call the custom selecting function
	result := selectFn(inboxCh)
	if result == SelectInboxEmptied {
		// PushBack must have pulled it out of the queue and the select just
		// emptied it, so it's safe to return to the pool.
		p.putChan(inboxCh)
		return // Done!
	}

	// Clean up the dedicated channel, which may still be in the queue or
	// contain an orphaned value.
	select {
	case inboxCh <- *new(T):
		// Successfully marked channel as abandoned, but it's still in
		// the queue so we can't put it back in the pool.
		trace.Logf(context.Background(), traceRegion, "marked inboxCh=%p abandoned", inboxCh)
	default:
		// Channel is full, drain the value and process it.
		orphan := <-inboxCh
		trace.Logf(context.Background(), traceRegion, "received orphan from inboxCh=%p", inboxCh)
		processOrphanFn(orphan)
		// PushBack must have pulled it out of the queue and we just
		// emptied it, so it's safe to return to the pool.
		p.putChan(inboxCh)
	}
}

//nolint:contextcheck // background context used only for tracing
func (q *Optional[T]) TryPopFront(p *Pool[T], processFn ProcessValueFunc[T]) {
	traceRegion := "rdvq.Optional.TryPopFront"
	q.PopFrontFunc(p, processFn, func(inboxCh <-chan T) SelectResult {
		trace.Logf(context.Background(), traceRegion, "entering select: inboxCh=%p", inboxCh)
		select {
		case value := <-inboxCh:
			trace.Logf(context.Background(), traceRegion, "value received from inboxCh=%p", inboxCh)
			processFn(value)
			return SelectInboxEmptied
		default:
			trace.Logf(context.Background(), traceRegion, "no value available from inboxCh=%p", inboxCh)
		}
		return SelectAborted
	})
}

func (q *Optional[T]) PopFront(ctx context.Context, p *Pool[T], processFn ProcessValueFunc[T]) error {
	traceRegion := "rdvq.Optional.PopFront"
	var err error
	q.PopFrontFunc(p, processFn, func(inboxCh <-chan T) SelectResult {
		trace.Logf(ctx, traceRegion, "entering select: inboxCh=%p", inboxCh)
		select {
		case value := <-inboxCh:
			trace.Logf(ctx, traceRegion, "received value from inboxCh=%p", inboxCh)
			processFn(value)
			return SelectInboxEmptied
		case <-ctx.Done():
			trace.Logf(ctx, traceRegion, "received context done signal")
			err = ctx.Err()
			return SelectAborted
		}
	})
	return err
}
