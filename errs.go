// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

type constError string

func (e constError) Error() string {
	return string(e)
}

const ErrTaskPanic = constError("task panicked")
const ErrCombinePanic = constError("combine panicked")
const ErrCombinerFlushPanic = constError("combiner flush panicked")
const ErrCombinerFactoryPanic = constError("combiner factory panicked")

const errJobDone = constError("job done")
