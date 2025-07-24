// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

module github.com/petenewcomb/psg-go

// Go 1.24 is the latest stable release at time of writing and at least 1.23
// with GOEXPERIMENT=aliastypeparams is required for generic type aliases
go 1.24

require (
	github.com/influxdata/tdigest v0.0.2-0.20210216194612-fc98d27c9e8b
	github.com/petenewcomb/atomic128-go v0.0.3
	github.com/stretchr/testify v1.10.0
	pgregory.net/rapid v1.2.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/klauspost/cpuid/v2 v2.0.8 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
