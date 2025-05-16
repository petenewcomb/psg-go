// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"testing"

	"github.com/petenewcomb/psg-go"
	"github.com/stretchr/testify/require"
)

func TestTaskPoolNilJobPanic(t *testing.T) {
	chk := require.New(t)

	// Try to create a pool with a nil job
	chk.PanicsWithValue("job must be non-nil", func() {
		_ = psg.NewTaskPool(nil, 1)
	})
}
