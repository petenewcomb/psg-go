// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package psgopt provides options for configuring PSG components.
//
// This package contains all the With* functions used to configure Jobs, TaskPools,
// FunnelPools, and FunnelOps. Use this package to access configuration options:
//
//	job := psg.New(ctx,
//		psgopt.WithTaskWorkerIdleTimeout(200*time.Millisecond),
//		psgopt.WithMaxGCTimeRatioThreshold(0.3),
//	)
package psgopt
