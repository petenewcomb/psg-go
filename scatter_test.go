// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"context"
	"testing"

	"github.com/petenewcomb/psg-go"
	"github.com/stretchr/testify/require"
)

func TestScatterFromTask(t *testing.T) {
	chk := require.New(t)
	ctx := context.Background()
	pool := psg.NewPool(1)
	job := psg.NewJob(ctx, pool)
	err := psg.Scatter(
		ctx,
		pool,
		func(ctx context.Context) (int, error) {
			chk.PanicsWithValue("psg.Scatter called from within TaskFunc; move call to GatherFunc instead", func() {
				chk.NoError(psg.Scatter(
					ctx,
					pool,
					func(ctx context.Context) (int, error) {
						chk.Fail("should not get here")
						return 0, nil
					},
					func(ctx context.Context, result int, err error) error {
						chk.NoError(err)
						chk.Fail("should not get here")
						return nil
					},
				))
			})
			return 0, nil
		},
		func(ctx context.Context, result int, err error) error {
			chk.NoError(err)
			return nil
		},
	)
	chk.NoError(err)
	chk.NoError(job.GatherAll(ctx))
}
