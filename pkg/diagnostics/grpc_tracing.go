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
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
)

// TracingGRPCMiddlewareStream plugs tracer into gRPC stream
func TracingGRPCMiddlewareStream(spec config.TracingSpec) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, span := TracingSpanFromGRPCContext(stream.Context(), nil, info.FullMethod, spec)
		wrappedStream := grpc_middleware.WrapServerStream(stream)
		wrappedStream.WrappedContext = context.WithValue(ctx, correlationKey, SerializeSpanContext(span.SpanContext()))
		defer span.End()

		err := handler(srv, wrappedStream)
		UpdateSpanPairStatusesFromError(span, err, info.FullMethod)
		return err
	}
}

// TracingGRPCMiddlewareUnary plugs tracer into gRPC unary calls
func TracingGRPCMiddlewareUnary(spec config.TracingSpec) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		ctx, span := TracingSpanFromGRPCContext(ctx, req, info.FullMethod, spec)
		defer span.End()

		newCtx := context.WithValue(ctx, correlationKey, SerializeSpanContext(span.SpanContext()))
		resp, err := handler(newCtx, req)
		UpdateSpanPairStatusesFromError(span, err, info.FullMethod)
		return resp, err
	}
}

// TracingSpanFromGRPCContext creates a span from an incoming gRPC method call
func TracingSpanFromGRPCContext(c context.Context, req interface{}, method string, spec config.TracingSpec) (context.Context, *trace.Span) {
	var ctx = context.Background()
	var span *trace.Span

	md := extractDaprMetadata(c)
	headers := extractHeaders(req)
	re := regexp.MustCompile(`(?i)(&__header_delim__&)?X-Correlation-ID&__header_equals__&[0-9a-fA-F]+;[0-9a-fA-F]+;[0-9a-fA-F]+`)
	corID := strings.Replace(re.FindString(headers), "&__header_delim__&", "", 1)
	if len(corID) > 35 { //to remove the prefix "X-Correlation-Id&__header_equals__&", which may in different casing
		corID = corID[35:]
	}

	rate := diag_utils.GetTraceSamplingRate(spec.SamplingRate)

	// TODO : Continue using ProbabilitySampler till Go SDK starts supporting RateLimiting sampler
	probSamplerOption := trace.WithSampler(trace.ProbabilitySampler(rate))
	serverKindOption := trace.WithSpanKind(trace.SpanKindServer)

	spanName := createSpanName(method)
	if corID != "" {
		spanContext := DeserializeSpanContext(corID)
		ctx, span = trace.StartSpanWithRemoteParent(c, spanName, spanContext, serverKindOption, probSamplerOption)
	} else {
		ctx, span = trace.StartSpan(ctx, spanName, serverKindOption, probSamplerOption)
	}

	addAnnotationsFromGRPCMetadata(md, span)

	return ctx, span
}

func addAnnotationsFromGRPCMetadata(md map[string][]string, span *trace.Span) {
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
