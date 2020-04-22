// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"strings"

	"github.com/dapr/dapr/pkg/config"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const grpcTraceContextKey = "grpc-trace-bin"

// StartTracingGRPCMiddlewareStream plugs tracer into gRPC stream
func StartTracingGRPCMiddlewareStream(spec config.TracingSpec) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, span := startTracingGRPCSpan(stream.Context(), info.FullMethod, spec.SamplingRate, trace.SpanKindServer)

		addAnnotationsToSpanFromGRPCMetadata(ctx, span)

		wrappedStream := grpc_middleware.WrapServerStream(stream)
		wrappedStream.WrappedContext = ctx
		defer span.End()

		err := handler(srv, wrappedStream)
		UpdateSpanPairStatusesFromError(span, err, info.FullMethod)
		return err
	}
}

// SetTracingSpanContextGRPCMiddlewareStream sets the trace spancontext into gRPC stream
func SetTracingSpanContextGRPCMiddlewareStream() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := WithGRPCSpanContext(stream.Context())

		wrappedStream := grpc_middleware.WrapServerStream(stream)
		wrappedStream.WrappedContext = ctx

		err := handler(srv, wrappedStream)

		return err
	}
}

// StartTracingGRPCMiddlewareUnary plugs tracer into gRPC unary calls
func StartTracingGRPCMiddlewareUnary(spec config.TracingSpec) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		ctx, span := startTracingGRPCSpan(ctx, info.FullMethod, spec.SamplingRate, trace.SpanKindServer)

		addAnnotationsToSpanFromGRPCMetadata(ctx, span)
		defer span.End()

		resp, err := handler(ctx, req)
		UpdateSpanPairStatusesFromError(span, err, info.FullMethod)
		return resp, err
	}
}

// SetTracingSpanContextGRPCMiddlewareUnary sets the trace spancontext into gRPC unary calls
func SetTracingSpanContextGRPCMiddlewareUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		ctx = WithGRPCSpanContext(ctx)

		resp, err := handler(ctx, req)

		return resp, err
	}
}

// StartTracingServerSpanFromGRPCContext creates a span from an incoming gRPC method call
func StartTracingServerSpanFromGRPCContext(ctx context.Context, method string, spec config.TracingSpec) (context.Context, *trace.Span) {
	var span *trace.Span
	ctx, span = startTracingGRPCSpan(ctx, method, spec.SamplingRate, trace.SpanKindServer)
	addAnnotationsToSpanFromGRPCMetadata(ctx, span)

	return ctx, span
}

// StartTracingClientSpanFromGRPCContext creates a span from an incoming gRPC method call
func StartTracingClientSpanFromGRPCContext(ctx context.Context, method string, spec config.TracingSpec) (context.Context, *trace.Span) {
	var span *trace.Span
	ctx, span = startTracingGRPCSpan(ctx, method, spec.SamplingRate, trace.SpanKindClient)
	addAnnotationsToSpanFromGRPCMetadata(ctx, span)

	return ctx, span
}

// StartTracingClientSpanWithCorID creates a span from an incoming gRPC method call
func StartTracingClientSpanWithCorID(ctx context.Context, corID, method string, spec config.TracingSpec) (context.Context, *trace.Span) {
	var span *trace.Span
	ctx, span = startTracingSpan(ctx, corID, method, spec.SamplingRate, trace.SpanKindClient)
	addAnnotationsToSpanFromGRPCMetadata(ctx, span)

	return ctx, span
}

func addAnnotationsToSpanFromGRPCMetadata(ctx context.Context, span *trace.Span) {
	md := extractDaprMetadata(ctx)

	// md metadata must only have dapr prefixed headers metadata
	// still extra check for dapr headers to avoid, it might be micro performance hit but that is ok
	for k, vv := range md {
		if !strings.HasPrefix(strings.ToLower(k), daprHeaderPrefix) {
			continue
		}

		for _, v := range vv {
			span.AddAttributes(trace.StringAttribute(k, v))
		}
	}
}

func WithGRPCSpanContext(ctx context.Context) context.Context {
	spanContext := FromGRPCContext(ctx)

	gen := tracingConfig.Load().(*traceIDGenerator)

	if (spanContext == trace.SpanContext{}) {
		spanContext = trace.SpanContext{}
		spanContext.TraceID = gen.NewTraceID()
		spanContext.SpanID = gen.NewSpanID()
	} else {
		spanContext.SpanID = gen.NewSpanID()
	}

	return AppendToOutgoingContext(ctx, spanContext)
}

// FromGRPCContext returns the SpanContext stored in a context, or empty if there isn't one.
func FromGRPCContext(ctx context.Context) trace.SpanContext {
	var sc trace.SpanContext
	md, _ := metadata.FromIncomingContext(ctx)
	traceContext := md[grpcTraceContextKey]
	if len(traceContext) > 0 {
		traceContextBinary := []byte(traceContext[0])
		sc, _ = propagation.FromBinary(traceContextBinary)
	}
	return sc
}

// AppendToOutgoingContext appends binary serialized SpanContext to the outgoing context
func AppendToOutgoingContext(ctx context.Context, spanContext trace.SpanContext) context.Context {
	traceContextBinary := propagation.Binary(spanContext)
	return metadata.AppendToOutgoingContext(ctx, grpcTraceContextKey, string(traceContextBinary))
}

func startTracingGRPCSpan(ctx context.Context, uri, samplingRate string, spanKind int) (context.Context, *trace.Span) {
	var span *trace.Span
	name := createSpanName(uri)

	rate := diag_utils.GetTraceSamplingRate(samplingRate)

	// TODO : Continue using ProbabilitySampler till Go SDK starts supporting RateLimiting sampler
	probSamplerOption := trace.WithSampler(trace.ProbabilitySampler(rate))
	kindOption := trace.WithSpanKind(spanKind)

	sc := FromGRPCContext(ctx)

	if (sc != trace.SpanContext{}) {
		// Note that if parent span context is provided which is sc in this case then ctx will be ignored
		ctx, span = trace.StartSpanWithRemoteParent(ctx, name, sc, kindOption, probSamplerOption)
	} else {
		ctx, span = trace.StartSpan(ctx, name, kindOption, probSamplerOption)
	}

	return ctx, span
}
