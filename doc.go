// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package psg provides an API for launching (scattering) tasks and aggregating
// (gathering) their results. It separates these stages so that the tasks can
// run concurrently while aggregation remains sequential. This reduces the
// wall-clock time required for an overall operation (job) as compared to
// executing the tasks serially, without adding synchronization complexity to
// the aggregation logic.
//
// Since tasks require resources to execute, and those resources are limited,
// psg also provides a way to model pools of resources as limits on the number
// of tasks allowed to run at the same time. Different classes of task, for
// instance compute-bound or I/O-bound, can be executed in the context of
// different pools and therefore subject to different concurrency constraints.
//
// Non-trivial tasks often involve different stages that use different kinds of
// resources, for instance I/O to retrieve a chunk of data followed by compute
// to process it. The psg package therefore allows gather functions to launch
// new tasks into the same or different pools all within the context of the same
// job, creating incremental pipelines that are free of unnecessary
// synchronization barriers between stages and that neither over- nor
// under-utilize multiple distinct groups of resources.
package psg

//go:generate go run -C internal/cmd/charts ./... ../../../bench.txt
