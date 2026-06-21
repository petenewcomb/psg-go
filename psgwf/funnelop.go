// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/streampool"
)

type GenericFunnelOp[T, C any] streampool.Funnel[result[T, C]]
type FunnelOp[T any] = GenericFunnelOp[T, context.Context]

// NewFunnelOp creates a streampool.Funnel that propagates workflow contexts
// through the funnel chain. The user's streampool.Accumulator body routes
// downstream submissions itself; the Workflow ref/unref balancing
// happens inside the wrapped streampool.Accumulator.
//
// Named NewFunnelOp (not NewFunnel) within psgwf to avoid clashing
// with the distinct Funnel interface alias in psgwf/funnel.go.
// Both psgwf names will be revisited when psgwf consolidates into Flow
// (Wave 7).
func NewFunnelOp[T, C any](
	wave *streampool.Wave,
	funnelFactory GenericFunnelFactory[T, C],
) GenericFunnelOp[T, C] {
	return GenericFunnelOp[T, C](streampool.NewFunnel(
		wave,
		wrapFunnelFactory(funnelFactory),
	))
}

func (c GenericFunnelOp[T, C]) inner() *streampool.Funnel[result[T, C]] {
	return (*streampool.Funnel[result[T, C]])(&c)
}
