// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psgwf

import (
	"context"
	"errors"
	"time"

	"github.com/petenewcomb/psg-go/psgfn"
)

// Combiner instances perform partial aggregation of workflow task results
// before final gathering. They are used by [psg.CombinerPool] to enable
// scalable concurrent and parallel result aggregation without requiring
// multiple gathering goroutines or use of thread-safe data structures and
// algorithms. This interface mirrors [psg.Combiner] but includes a Workflow
// parameter.
type GenericCombiner[I, O, C any] interface {
	// Combine processes a single input task result and optionally emits an
	// output to be gathered.
	Combine(ctx context.Context, wf *GenericWorkflow[C], input I, inputErr error) (time.Time, error)

	// Flush emits combined results to be gathered unless the returned error is
	// ErrDoNotGather. Since calls to Flush are independent of any specific
	// workflow, it does not receive a [Workflow] parameter. It must either
	// create a new Workflow use one previously retained using [Pin]. A common
	// pattern is to retain Workflow instances passed to Combine as their
	// associated results are aggregated and then release them all after the
	// aggregated result set has been emitted.
	Flush(ctx context.Context) (*GenericWorkflow[C], O, error)
}

type Combiner[I, O any] = GenericCombiner[I, O, Context]

type GenericCombinerFactory[I, O, C any] func() GenericCombiner[I, O, C]
type CombinerFactory[I, O any] = GenericCombinerFactory[I, O, Context]

func wrapCombinerFactory[I, O, C any](
	combinerFactory GenericCombinerFactory[I, O, C],
) psgfn.CombinerFactory[result[I, C], result[O, C]] {
	return func() psgfn.Combiner[result[I, C], result[O, C]] {
		innerCombiner := combinerFactory()
		return psgfn.FuncCombiner[result[I, C], result[O, C]]{
			CombineFn: func(ctx context.Context, input result[I, C], inputErr error) (time.Time, error) {
				defer input.Workflow.unref(ctx)
				return innerCombiner.Combine(ctx, input.Workflow, input.Value, inputErr)
			},
			FlushFn: func(ctx context.Context) (result[O, C], error) {
				wf, v, err := innerCombiner.Flush(ctx)
				if errors.Is(err, psgfn.ErrDoNotGather) {
					return result[O, C]{}, err
				}
				wf.ref()
				return result[O, C]{Workflow: wf, Value: v}, err
			},
		}
	}
}
