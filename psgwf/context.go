// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import "context"

type Context interface {
	context.Context
	Cancel(error)
}

type baseContext struct {
	context.Context //nolint:containedctx // baseContext embeds context.Context to provide a cancelable context
	cancel          context.CancelCauseFunc
}

func newContext(ctx context.Context) Context {
	ctx, cancel := context.WithCancelCause(ctx)
	return &baseContext{
		Context: ctx,
		cancel:  cancel,
	}
}

func (c *baseContext) Cancel(cause error) {
	c.cancel(cause)
}

type childContext struct {
	context.Context //nolint:containedctx // childContext embeds context.Context to extend a cancelable context
	parent          Context
}

func newChildContext(parent Context, ctx context.Context) Context {
	return &childContext{
		Context: ctx,
		parent:  parent,
	}
}

func (c *childContext) Cancel(cause error) {
	c.parent.Cancel(cause)
}
