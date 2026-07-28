// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streampool

import "time"

// Forever is the deadline-sentinel value for "block until success."
// Pass it to TrySubmit / TrySubmitErr / TrySubmitResult /
// TrySubmitResult / TryStart to express the same behavior as
// Submit / SubmitErr / SubmitResult / Start (without the bool drop).
//
// The framework treats Forever specially: the blocking layer
// installs no timeout, so dispatch waits indefinitely for the
// condition to be met (until context cancellation or system
// shutdown).
//
// Deadline semantics across all Try* methods:
//
//   - Forever                          → block until success (no timer)
//   - time.Time{} (zero value)         → attempt once; fail-fast on contention
//   - past time (time.Now() ≥ deadline) → fail-fast; no attempt
//   - future time                       → bounded wait until deadline
//
// The "fail-fast" cases return (false, nil) — they signal "couldn't
// dispatch within the allotted time" rather than an error. Use a
// non-nil err to distinguish genuine failures (ctx cancellation,
// pool shutdown, handler errors) from the deadline path.
var Forever = time.Date(9999, time.January, 1, 0, 0, 0, 0, time.UTC)

// isForever reports whether deadline is the [Forever] sentinel.
// Internal helper for blocking layers that need to skip timer
// installation when block-forever semantics are requested.
func isForever(deadline time.Time) bool {
	return deadline.Equal(Forever)
}
