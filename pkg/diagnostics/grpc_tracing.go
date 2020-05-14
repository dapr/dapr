// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"strings"

	"github.com/dapr/dapr/pkg/config"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const grpcTraceContextKey = "grpc-trace-bin"

// SetTracingInGRPCMiddlewareStream sets the trace context or starts the trace client span based on request
func SetTracingInGRPCMiddlewareStream(spec config.TracingSpec) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := stream.Context()
		sc := GetSpanContextFromGRPC(ctx, spec)
		method := info.FullMethod
		newCtx := NewContext(ctx, sc)
		wrappedStream := grpc_middleware.WrapServerStream(stream)
		wrappedStream.WrappedContext = newCtx
		var err error

		// do not start the client span if the request is service invocation or actors call
		if isServiceInvocationMethod(method) {
			err = handler(srv, wrappedStream)
		} else {
			_, span := StartTracingClientSpanFromGRPCContext(newCtx, method, spec)
			defer span.End()

			// build new context now on top of passed root context with started span context
			newCtx = NewContext(ctx, span.SpanContext())
			wrappedStream.WrappedContext = newCtx
			err = handler(srv, wrappedStream)

			UpdateSpanStatusFromError(span, err, method)
		}

		return err
	}
}

// SetTracingInGRPCMiddlewareUnary sets the trace context or starts the trace client span based on request
func SetTracingInGRPCMiddlewareUnary(spec config.TracingSpec) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		sc := GetSpanContextFromGRPC(ctx, spec)
		method := info.FullMethod
		newCtx := NewContext(ctx, sc)
		var err error
		var resp interface{}

		// do not start the client span if the request is service invocation or actors call
		if isServiceInvocationMethod(method) || isActorsMethod(method) {
			resp, err = handler(newCtx, req)
		} else {
			_, span := StartTracingClientSpanFromGRPCContext(newCtx, method, spec)
			defer span.End()

			// build new context now on top of passed root context with started span context
			newCtx = NewContext(ctx, span.SpanContext())
			resp, err = handler(newCtx, req)

			UpdateSpanStatusFromError(span, err, method)
		}

		return resp, err
	}
}

// StartTracingServerSpanFromGRPCContext creates a span on receiving an incoming gRPC method call from remote client
func StartTracingServerSpanFromGRPCContext(ctx context.Context, method string, spec config.TracingSpec) (context.Context, *trace.Span) {
	var span *trace.Span
	ctx, span = startTracingSpanInternal(ctx, method, spec.SamplingRate, trace.SpanKindServer)
	addAnnotationsToSpanFromGRPCMetadata(ctx, span)

	return ctx, span
}

// StartTracingClientSpanFromGRPCContext creates a client span before invoking gRPC method call
func StartTracingClientSpanFromGRPCContext(ctx context.Context, method string, spec config.TracingSpec) (context.Context, *trace.Span) {
	var span *trace.Span
	ctx, span = startTracingSpanInternal(ctx, method, spec.SamplingRate, trace.SpanKindClient)
	addAnnotationsToSpanFromGRPCMetadata(ctx, span)

	return ctx, span
}

func GetSpanContextFromGRPC(ctx context.Context, spec config.TracingSpec) trace.SpanContext {
	spanContext, ok := FromGRPCContext(ctx)

	if !ok {
		spanContext = GetDefaultSpanContext(spec)
	}

	return spanContext
}

// FromGRPCContext returns the SpanContext stored in a context, or empty if there isn't one.
func FromGRPCContext(ctx context.Context) (trace.SpanContext, bool) {
	var sc trace.SpanContext
	var ok bool
	md, _ := metadata.FromIncomingContext(ctx)
	traceContext := md[grpcTraceContextKey]
	if len(traceContext) > 0 {
		traceContextBinary := []byte(traceContext[0])
		sc, ok = propagation.FromBinary(traceContextBinary)
	}
	return sc, ok
}

// AppendToOutgoingGRPCContext appends binary serialized SpanContext to the outgoing GRPC context
func AppendToOutgoingGRPCContext(ctx context.Context, spanContext trace.SpanContext) context.Context {
	traceContextBinary := propagation.Binary(spanContext)
	return metadata.AppendToOutgoingContext(ctx, grpcTraceContextKey, string(traceContextBinary))
}

// FromOutgoingGRPCContext returns the SpanContext stored in a context, or empty if there isn't one.
func FromOutgoingGRPCContext(ctx context.Context) (trace.SpanContext, bool) {
	var sc trace.SpanContext
	var ok bool
	md, _ := metadata.FromOutgoingContext(ctx)
	traceContext := md[grpcTraceContextKey]
	if len(traceContext) > 0 {
		traceContextBinary := []byte(traceContext[0])
		sc, ok = propagation.FromBinary(traceContextBinary)
	}
	return sc, ok
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

func isServiceInvocationMethod(method string) bool {
	return strings.Contains(method, "InvokeService")
}

func isActorsMethod(method string) bool {
	return strings.Contains(method, "CallActor")
}
