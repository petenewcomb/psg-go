// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

// ProcessValueFunc is called to process a value retrieved from a queue.
type ProcessValueFunc[T any] = func(value T)
