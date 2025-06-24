// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg_test

import (
	"testing"

	"github.com/petenewcomb/psg-go"
	"github.com/stretchr/testify/assert"
)

func TestTaskPoolNilJobPanic(t *testing.T) {
	// Try to create a pool with a nil job
	assert.PanicsWithValue(t, "job must be non-nil", func() {
		_ = psg.NewTaskPool(nil)
	})
}
