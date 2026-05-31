// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package sim provides a way to generate and execute simulated streampool
// jobs. It generates a Plan for each job — a static description of a unit
// of work, modeled as a DAG of operations (Launchers, Funnels,
// Skimmers) tied together by explicit Submit and StartTask steps inside
// their function bodies. Plans are constructed via property-based
// generators against a Config, then executed by a runtime adapter that
// translates the new Plan vocabulary onto the current psg API.
//
// The Plan vocabulary anchors to the destination streampool API even
// while the runtime adapter sits on top of the pre-reshape psg API. As
// reshape waves land, only the adapter layer changes; Plan generators,
// Step definitions, and assertion contracts stay stable.
//
// New Plan generators produce the full destination-API expressive range
// (multi-sink Submit, multi-StartTask bodies, zero-output paths). A
// Deterministic Config mode forces all Probs to 1.0 and SelfTime
// distributions to fixed for exact-bound assertions; probabilistic mode
// relaxes assertions to Max-only bounds in exchange for richer
// race-exposure surface.
package sim
