// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package sim provides a way to generate and execute simulated streampool
// jobs. It generates a Plan for each job — a static description of a unit
// of work, modeled as a DAG of operations (Launchers, Funnels,
// Skimmers) tied together by explicit Submit and StartTask steps inside
// their function bodies. Plans are constructed via property-based
// generators against a Config, then executed by a runtime adapter that
// translates the Plan vocabulary onto the psg API; only the adapter layer
// touches that API, so Plan generators, Step definitions, and assertion
// contracts are independent of its shape.
//
// Plan generators produce the full expressive range
// (multi-sink Submit, multi-StartTask bodies, zero-output paths). Generation
// emits only Prob=1.0 and the runtime draws SelfTime durations at their median,
// so invocation-count and path-duration bounds are exact.
package sim
