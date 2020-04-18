// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"regexp"
	"strings"

	"github.com/dapr/dapr/pkg/config"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
)

// StartTracingGRPCMiddlewareStream plugs tracer into gRPC stream
func StartTracingGRPCMiddlewareStream(spec config.TracingSpec) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		corID := getCorrelationId(nil)
		ctx, span := startTracingSpan(stream.Context(), corID, info.FullMethod, spec.SamplingRate, trace.SpanKindServer)

		addAnnotationsToSpanFromGRPCMetadata(ctx, span)

		wrappedStream := grpc_middleware.WrapServerStream(stream)
		wrappedStream.WrappedContext = ctx
		defer span.End()

		err := handler(srv, wrappedStream)
		UpdateSpanPairStatusesFromError(span, err, info.FullMethod)
		return err
	}
}

// StartTracingGRPCMiddlewareUnary plugs tracer into gRPC unary calls
func StartTracingGRPCMiddlewareUnary(spec config.TracingSpec) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		corID := getCorrelationId(req)
		ctx, span := startTracingSpan(ctx, corID, info.FullMethod, spec.SamplingRate, trace.SpanKindServer)

		addAnnotationsToSpanFromGRPCMetadata(ctx, span)
		defer span.End()

		resp, err := handler(ctx, req)
		UpdateSpanPairStatusesFromError(span, err, info.FullMethod)
		return resp, err
	}
}

// StartTracingClientSpanFromGRPCContext creates a span from an incoming gRPC method call
func StartTracingClientSpanFromGRPCContext(ctx context.Context, req interface{}, method string, spec config.TracingSpec) (context.Context, *trace.Span) {
	var span *trace.Span

	headers := extractHeaders(req)
	re := regexp.MustCompile(`(?i)(&__header_delim__&)?X-Correlation-ID&__header_equals__&[0-9a-fA-F]+;[0-9a-fA-F]+;[0-9a-fA-F]+`)
	corID := strings.Replace(re.FindString(headers), "&__header_delim__&", "", 1)
	if len(corID) > 35 { //to remove the prefix "X-Correlation-Id&__header_equals__&", which may in different casing
		corID = corID[35:]
	}

	ctx, span = startTracingSpan(ctx, corID, method, spec.SamplingRate, trace.SpanKindClient)
	addAnnotationsToSpanFromGRPCMetadata(ctx, span)

	return ctx, span
}

func getCorrelationId(req interface{}) string {
	headers := extractHeaders(req)
	re := regexp.MustCompile(`(?i)(&__header_delim__&)?X-Correlation-ID&__header_equals__&[0-9a-fA-F]+;[0-9a-fA-F]+;[0-9a-fA-F]+`)
	corID := strings.Replace(re.FindString(headers), "&__header_delim__&", "", 1)
	if len(corID) > 35 { //to remove the prefix "X-Correlation-Id&__header_equals__&", which may in different casing
		corID = corID[35:]
	}

	return corID
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
