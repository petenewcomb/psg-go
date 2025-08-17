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

func (om *outboxMap) Reset() {
	// Reset the map to allow reuse without reallocating
	for _, v := range om.m {
		v.(interface{ Free() }).Free() // Free the outbox
	}
	clear(om.m)
}

// OutboxFor returns the outbox for the given key, creating one if it doesn't exist.
func OutboxFor[T any](om *outboxMap, key outboxKey[T]) *rdvq.Outbox[T] {
	if om.m == nil {
		om.m = make(map[any]any)
	}

	outboxAny := om.m[key]
	if outboxAny == nil {
		outbox := rdvq.NewOutbox[T]()
		om.m[key] = outbox
		return outbox
	}
	return outboxAny.(*rdvq.Outbox[T])
}
