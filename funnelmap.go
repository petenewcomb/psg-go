// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"

	"github.com/petenewcomb/psg-go/internal/delayq"
	"github.com/petenewcomb/psg-go/internal/rdvq"
)

// funnelFlusher is the interface FunnelPool's flushQ holds. Each
// halfBoundFunnel instance satisfies it; the [delayq.Item] embedding
// provides the heap-position bookkeeping the queue needs to dedupe
// re-Schedules and locate entries for Remove.
type funnelFlusher interface {
	delayq.Item

	InstanceID() funnelInstanceID
	InstanceCount() int
	Ref()
	Unref()

	// Flush is assumed to also Unref()
	Flush(ctx context.Context, sender *rdvq.Sender)
}
