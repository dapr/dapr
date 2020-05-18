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
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const grpcTraceContextKey = "grpc-trace-bin"

// SetTracingInGRPCMiddlewareUnary sets the trace context or starts the trace client span based on request
func SetTracingInGRPCMiddlewareUnary(appID string, spec config.TracingSpec) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		sc := GetSpanContextFromGRPC(ctx, spec)
		method := info.FullMethod
		newCtx := NewContext(ctx, sc)
		var err error
		var resp interface{}
		var span *trace.Span

		// do not start the client/server spans, just set the trace context when the sampling rate is 0
		if !diag_utils.IsTracingEnabled(spec.SamplingRate) {
			resp, err = handler(newCtx, req)
			return resp, err
		}

		// do not start the client span if the request is local service invocation call
		if isLocalServiceInvocationMethod(method) {
			if m, ok := req.(*internalv1pb.InternalInvokeRequest); ok {
				method = m.Message.Method
			}
			_, span = StartTracingServerSpanFromGRPCContext(newCtx, method, spec)
		} else {
			_, span = StartTracingClientSpanFromGRPCContext(newCtx, method, spec)
		}

		// build new context now on top of passed root context with started span context
		newCtx = NewContext(ctx, span.SpanContext())
		resp, err = handler(newCtx, req)

		UpdateSpanStatusFromError(span, err, method)

		defer span.End()

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

func isLocalServiceInvocationMethod(method string) bool {
	return strings.Contains(method, "/CallLocal")
}
