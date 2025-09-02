// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package workq

import (
	"github.com/petenewcomb/psg-go/internal/rdvq"
)

// Specialization of [rdvq.Required] for [Work].
type Pending = rdvq.Required[Work]

// See [rdvq.Receiver]
type Receiver = rdvq.Receiver[Work]

// See [rdvq.Outbox]
type Outbox = rdvq.Outbox[Work]

// See [rdvq.PushSelectFunc]
type PushSelectFunc = rdvq.PushSelectFunc[Work]

// See [rdvq.RequiredPopSelectFunc]
type PopSelectFunc = rdvq.RequiredPopSelectFunc[Work]

// See [rdvq.WaiterOrReceiver]
type WaiterOrReceiver = rdvq.WaiterOrReceiver

// See [rdvq.PushSelectFunc]
type WaitSelectFunc = rdvq.WaitSelectFunc
