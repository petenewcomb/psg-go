// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import "fmt"

// ProcessValueFunc is called to process a value retrieved from a queue.
type ProcessValueFunc[T any] func(value T)

// SelectResult indicates the outcome of a select operation in rdvq.
// This enumeration provides type-safe, explicit results for all select
// operations throughout the rdvq package, replacing inconsistent boolean
// returns and making operation outcomes clear.
type SelectResult int

const (
	// SelectAborted indicates the operation was cancelled or interrupted,
	// typically due to context cancellation or other external factors.
	SelectAborted SelectResult = iota

	// SelectInboxEmptied indicates an inbox channel was successfully read from,
	// meaning a value was received from a sender's dedicated channel.
	SelectInboxEmptied

	// SelectOutboxFilled indicates an outbox channel was successfully written to,
	// meaning a value was successfully sent to a sender's outbox buffer.
	SelectOutboxFilled

	// SelectWaitSignaled indicates a wait channel was signaled,
	// meaning new work became available and the operation should retry.
	SelectWaitSignaled
)

func (sr SelectResult) String() string {
	switch sr {
	case SelectAborted:
		return "SelectAborted"
	case SelectInboxEmptied:
		return "SelectInboxEmptied"
	case SelectOutboxFilled:
		return "SelectOutboxFilled"
	case SelectWaitSignaled:
		return "SelectWaitSignaled"
	default:
		return fmt.Sprintf("SelectResult(%d)", sr)
	}
}
