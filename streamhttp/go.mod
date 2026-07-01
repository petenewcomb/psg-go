// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

module github.com/petenewcomb/streampool/streamhttp

go 1.25.0

require (
	github.com/lxzan/gws v1.9.1
	github.com/petenewcomb/streampool v0.0.1
	github.com/petenewcomb/streampool/streamgrpc v0.0.0-00010101000000-000000000000
	golang.org/x/net v0.56.0
	google.golang.org/grpc v1.81.1
)

require (
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/petenewcomb/atomic128-go v0.0.3 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/petenewcomb/streampool => ../

replace github.com/petenewcomb/streampool/streamgrpc => ../streamgrpc
