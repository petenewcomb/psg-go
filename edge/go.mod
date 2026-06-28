// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

module github.com/petenewcomb/streampool/edge

go 1.25.0

require (
	github.com/lxzan/gws v1.9.1
	github.com/petenewcomb/streampool v0.0.1
	github.com/valyala/fasthttp v1.71.0
	golang.org/x/net v0.56.0
)

require (
	github.com/andybalholm/brotli v1.2.1 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/petenewcomb/atomic128-go v0.0.3 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)

replace github.com/petenewcomb/streampool => ../
