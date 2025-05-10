// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package sim provides a way to generate and execute simulated psg jobs. It
// generates a plan for each job, which it models as a directed acyclic graph. A
// job plan starts with a set of root tasks to execute. Each task has a
// corresponding gather or combine which may scatter additional child tasks and
// their gathers or combines. Some combines are also followed by gathers, and
// each combiner also has a final gather. New plans are generated according to a
// set of configuration parameters that determine the size and complexity of the
// graph.
package sim
