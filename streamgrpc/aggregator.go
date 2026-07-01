// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package streamgrpc shows how gRPC pairs with streampool. The lesson is the same
// one HTTP/2 taught: a mature stack owns the transport (grpc-go does its own
// HTTP/2 framing, HPACK, and flow control — never hand-roll it), and streampool
// owns the *work* inside each method.
//
// The integration points:
//
//   - A unary method body runs on grpc-go's per-stream goroutine. It fans out
//     into a streampool sub-wave and block-drains it (Model A) — safe because
//     the pool scales with demand.
//   - The fan-out is governed by a streampool.Limiter that models a real
//     external constraint on the downstream dependency (e.g. "this backend
//     allows ≤64 concurrent calls") — not request admission, which streampool's
//     backpressure paces on its own. That SAME Limiter value can be shared with
//     an edge.App's FanOut (bound there via WithLimits too), so HTTP and gRPC
//     callers hitting the same dependency draw from one collective cap on it,
//     regardless of which transport they arrived on.
//
// To stay toolchain-free (no modern protoc in this environment) the service is
// registered manually with a JSON codec instead of protoc-generated stubs. In
// production you would use generated stubs and the proto codec; none of that
// changes the streampool integration below.
package streamgrpc

import (
	"context"
	"encoding/json"

	"github.com/petenewcomb/streampool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
)

// ── Messages (would normally be protoc-generated) ───────────────────────────

type AggregateRequest struct {
	Keys []string `json:"keys"`
}

type AggregateResponse struct {
	Values []string `json:"values"`
}

// AggregatorServer is the service interface (normally generated).
type AggregatorServer interface {
	Aggregate(context.Context, *AggregateRequest) (*AggregateResponse, error)
}

// ── Service implementation: gRPC method that fans out via streampool ─────────

// Service implements AggregatorServer. Gate is the shared concurrency limiter;
// Fetch is the per-key downstream call that fan-out parallelizes.
type Service struct {
	Gate  streampool.Limiter
	Fetch func(ctx context.Context, key string) (string, error)
}

// Aggregate fans out one downstream Fetch per key on a child Wave, rate-limited
// by the shared Gate, and returns the collected values. The gRPC per-stream
// goroutine blocks on the drain — fine, because streampool's pool scales.
func (s *Service) Aggregate(ctx context.Context, in *AggregateRequest) (*AggregateResponse, error) {
	var sub streampool.Wave

	// The skimmer body runs only on the draining goroutine — this one, via the
	// Submit loop's help-skim and CloseAndSkimAll below — never on the pool
	// workers that Fetch runs on. So vals needs no lock (cf. the streampool
	// examples, which append in a skimmer the same way).
	vals := make([]string, 0, len(in.Keys))

	collect := streampool.NewFnSkimmer(
		func(_ context.Context, v string, err error) error {
			if err != nil {
				return err
			}
			vals = append(vals, v)
			return nil
		},
	)

	run := streampool.NewLauncher[string](
		streampool.HandlerFunc[string](func(ctx context.Context, key string, _ error) error {
			v, err := s.Fetch(ctx, key)
			return collect.SubmitResult(ctx, v, err)
		}),
		streampool.WithLimits(s.Gate),
	)

	for _, k := range in.Keys {
		if err := run.In(&sub).Submit(ctx, k); err != nil {
			return nil, err
		}
	}
	if err := sub.CloseAndSkimAll(ctx); err != nil {
		return nil, err
	}
	return &AggregateResponse{Values: vals}, nil
}

// ── Manual service registration (stand-in for generated code) ────────────────

// AggregateFullMethod is the wire method name.
const AggregateFullMethod = "/streamgrpc.Aggregator/Aggregate"

func aggregateHandler(
	srv any,
	ctx context.Context,
	dec func(any) error,
	interceptor grpc.UnaryServerInterceptor,
) (any, error) {
	in := new(AggregateRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AggregatorServer).Aggregate(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: AggregateFullMethod}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(AggregatorServer).Aggregate(ctx, req.(*AggregateRequest))
	}
	return interceptor(ctx, in, info, handler)
}

var aggregatorServiceDesc = grpc.ServiceDesc{
	ServiceName: "streamgrpc.Aggregator",
	HandlerType: (*AggregatorServer)(nil),
	Methods:     []grpc.MethodDesc{{MethodName: "Aggregate", Handler: aggregateHandler}},
	Streams:     []grpc.StreamDesc{},
	Metadata:    "streamgrpc/manual",
}

// RegisterAggregatorServer registers impl on a grpc.Server (or any registrar).
func RegisterAggregatorServer(r grpc.ServiceRegistrar, impl AggregatorServer) {
	r.RegisterService(&aggregatorServiceDesc, impl)
}

// ── JSON codec (stand-in for the proto codec) ────────────────────────────────

// CodecName is the content-subtype clients select via grpc.CallContentSubtype.
const CodecName = "json"

type jsonCodec struct{}

func (jsonCodec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (jsonCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
func (jsonCodec) Name() string                       { return CodecName }

func init() { encoding.RegisterCodec(jsonCodec{}) }
