// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"
)

type PinnedGenericWorkflow[C any] struct {
	wf *GenericWorkflow[C]
}

type PinnedWorkflow = PinnedGenericWorkflow[context.Context]

func Pin[C any](wf *GenericWorkflow[C]) PinnedGenericWorkflow[C] {
	wf.ref()
	return PinnedGenericWorkflow[C]{wf: wf}
}

func (p *PinnedGenericWorkflow[C]) Get() *GenericWorkflow[C] {
	return p.wf
}

func (p *PinnedGenericWorkflow[C]) Release(ctx context.Context) {
	wf := p.wf
	if wf != nil {
		p.wf = nil
		wf.unref(ctx)
	}
}
