// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package cerr

type Error string

func (e Error) Error() string {
	return string(e)
}
