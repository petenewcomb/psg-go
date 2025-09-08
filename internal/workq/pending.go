// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"github.com/petenewcomb/psg-go/internal/rdvq"
)

// Specialization of [rdvq.Queue] for [Work].
type Pending = rdvq.Queue[Work]

// See [rdvq.Outbox]
type Outbox = rdvq.Outbox[Work]

// See [rdvq.PushSelectFunc]
type PushSelectFunc = rdvq.PushSelectFunc[Work]

// See [rdvq.PopSelectFunc]
type PopSelectFunc = rdvq.PopSelectFunc[Work]

// See [rdvq.PushSelectFunc]
type WaitSelectFunc = rdvq.WaitSelectFunc
