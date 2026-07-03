// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package rdvq

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The chained bit rides both delivery styles, and ProbeOrigin re-delivers a fresh
// chained wake at a listener-style notification's origin Notifier — the wake-chain
// rule-2 plumbing (a productive consumer pays the chain one probe). Ordinary wakes
// carry no chain debt: Chained is false and ProbeOrigin is a no-op.
func TestNotifyChainedBitAndProbeOrigin(t *testing.T) {
	var n Notifier
	n.Init()

	// Listener-style delivery carries the bit and the origin.
	var got Notification
	lis := &Listener{Notify: func(m Notification) bool { got = m; return true }}
	lis.AddTo(&n.Listeners)
	n.NotifyChained(nil)
	require.True(t, got.Received())
	require.True(t, got.Chained(), "the chained bit must ride listener delivery")

	// ProbeOrigin on the consumed chain wake re-delivers a fresh chained wake at the
	// origin — the next registered consumer sees it.
	var got2 Notification
	lis2 := &Listener{Notify: func(m Notification) bool { got2 = m; return true }}
	lis2.AddTo(&n.Listeners)
	got.ProbeOrigin()
	require.True(t, got2.Received(), "ProbeOrigin must emit a fresh wake at the origin")
	require.True(t, got2.Chained(), "the probe itself is chained — the chain propagates")

	// An ordinary Notify carries no chain debt; ProbeOrigin is a no-op for it.
	var got3 Notification
	lis3 := &Listener{Notify: func(m Notification) bool { got3 = m; return true }}
	lis3.AddTo(&n.Listeners)
	n.Notify(nil)
	require.True(t, got3.Received())
	require.False(t, got3.Chained(), "plain wakes are unchained — no probe churn on the hot path")
	delivered := false
	lis4 := &Listener{Notify: func(Notification) bool { delivered = true; return true }}
	lis4.AddTo(&n.Listeners)
	got3.ProbeOrigin()
	require.False(t, delivered, "ProbeOrigin on an unchained wake is a no-op")
}
