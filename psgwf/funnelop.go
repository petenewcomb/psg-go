// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"

	"github.com/petenewcomb/psg-go"
)

type GenericFunnelOp[T, C any] psg.Funnel[result[T, C]]
type FunnelOp[T any] = GenericFunnelOp[T, context.Context]

// NewFunnelOp creates a psg.Funnel that propagates workflow contexts
// through the funnel chain. The user's psg.Accumulator body routes
// downstream submissions itself; the Workflow ref/unref balancing
// happens inside the wrapped psg.Accumulator.
//
// Named NewFunnelOp (not NewFunnel) within psgwf to avoid clashing
// with the distinct Funnel interface alias in psgwf/funnel.go.
// Both psgwf names will be revisited when psgwf consolidates into Flow
// (Wave 7).
func NewFunnelOp[T, C any](
	wave *psg.Wave,
	funnelFactory GenericFunnelFactory[T, C],
) GenericFunnelOp[T, C] {
	return GenericFunnelOp[T, C](psg.NewFunnel(
		wave,
		wrapFunnelFactory(funnelFactory),
	))
}

func (c GenericFunnelOp[T, C]) inner() *psg.Funnel[result[T, C]] {
	return (*psg.Funnel[result[T, C]])(&c)
}
