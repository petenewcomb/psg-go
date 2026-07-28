// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package otpsg

import (
	"context"

	"github.com/petenewcomb/streampool"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// flowSpanKey carries the flow's span as a path-scoped flow value: it is
// inherited verbatim along every dispatch chain descending from the [Traced]
// scope, so every body — synchronous or async — can read it back with
// [FlowSpan] (or activate it with [Correlate]) for child-span and log
// correlation. Being path-scoped it SEVERS at a funnel fan-in, where merged
// data has no truthful value; see [Traced].
var flowSpanKey = streampool.NewFlowKey[trace.Span]()

// Traced starts a span named name and returns a ctx carrying it plus the
// [streampool.FlowOption]s that bind the span's lifetime to a flow. Open the
// flow with the returned options and let the framework end the span:
//
//	ctx, flow := otpsg.Traced(ctx, "process-request")
//	err := streampool.WithFlow(ctx, func(ctx context.Context) error {
//		// dispatch tasks/skims/funnels here; all inherit the span
//		return wave.CloseAndSkimAll(ctx)
//	}, flow...)
//
// The two options play distinct roles, split by whether a merge operator
// exists at fan-in:
//
//   - flowSpanKey.Value(span) is PATH-scoped. It rides every dispatch chain so
//     child-span parenting and log correlation work in every body, including
//     async work that outlives the handler that spawned it. It SEVERS at a
//     funnel fan-in — merged items descend from independent parents, so no
//     single span is the truthful value below the merge.
//
//   - FlowFollowUpFn(span.End) is a DAG-scoped, anonymous follow-up. It fires
//     EXACTLY ONCE at the flow's TRUE end — after WithFlow returns AND after
//     all work in the flow completes, across funnels and covering async work
//     that outlives the handler. That is why the span is NOT ended at any op
//     boundary: an op's defer would end it while spawned work is still in
//     flight. The span's lifetime IS the flow.
//
// The returned ctx also has the span set as OpenTelemetry's active span
// (trace.ContextWithSpan), so ordinary otel calls on it parent correctly
// before the flow is opened.
//
// Post-fan-in correlation is unavailable: because the path-scoped value severs
// at a funnel, [FlowSpan]/[Correlate] read absent in work downstream of a
// funnel flush. If you need the span (or its context) downstream of a fan-in,
// carry the [trace.SpanContext] yourself as part of the accumulated data.
func Traced(
	ctx context.Context,
	name string,
	opts ...trace.SpanStartOption,
) (context.Context, []streampool.FlowOption) {
	ctx, span := otel.Tracer("otpsg").Start(ctx, name, opts...)
	return trace.ContextWithSpan(ctx, span), []streampool.FlowOption{
		flowSpanKey.Value(span),
		streampool.FlowFollowUpFn(func(context.Context) error {
			span.End()
			return nil
		}),
	}
}

// Correlate makes the ambient flow span (the one [Traced] attached) the active
// OpenTelemetry span on the returned ctx, so a subsequent
// tracer.Start(Correlate(ctx), ...) creates a CHILD of the flow span:
//
//	_, child := otel.Tracer("otpsg").Start(otpsg.Correlate(ctx), "process-data")
//	defer child.End()
//
// It is needed in async bodies (task/skim handlers dispatched onto a wave):
// such bodies do NOT inherit OpenTelemetry's active-span from the context that
// dispatched them — only the flow rider propagates — so trace.SpanFromContext
// would read the no-op span. Correlate republishes the flow span as active. If
// ctx carries no flow span (never [Traced], or below a funnel fan-in), it
// returns ctx unchanged.
func Correlate(ctx context.Context) context.Context {
	if s, ok := flowSpanKey.From(ctx); ok {
		return trace.ContextWithSpan(ctx, s)
	}
	return ctx
}

// FlowSpan returns the flow span attached by [Traced], if ctx is part of such a
// flow. It reports (nil-ish span, false) on a ctx that was never [Traced] and
// below a funnel fan-in (the path-scoped value severs there). It is the
// read-only counterpart to [Correlate]: use it to read span attributes or its
// [trace.SpanContext] without making it the active span.
func FlowSpan(ctx context.Context) (trace.Span, bool) {
	return flowSpanKey.From(ctx)
}
