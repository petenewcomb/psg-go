// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"github.com/petenewcomb/psg-go/internal/rdvq"
)

type outboxKey[T any] any

type outboxMap struct {
	m map[any]any
}

// OutboxFor returns the outbox for the given key, creating one if it doesn't exist.
func OutboxFor[T any](om *outboxMap, key outboxKey[T]) *rdvq.Outbox[T] {
	if om.m == nil {
		om.m = make(map[any]any)
	}

	outbox := om.m[key]
	if outbox == nil {
		outbox = &rdvq.Outbox[T]{}
		om.m[key] = outbox
	}

	return outbox.(*rdvq.Outbox[T])
}
