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
		span, spanc := TracingSpanFromGRPCContext(stream.Context(), nil, info.FullMethod, spec)
		wrappedStream := grpc_middleware.WrapServerStream(stream)
		wrappedStream.WrappedContext = context.WithValue(span.Context, correlationKey, SerializeSpanContext(*spanc.SpanContext))
		defer span.Span.End()
		defer spanc.Span.End()
		err := handler(srv, wrappedStream)
		UpdateSpanPairStatusesFromError(span, spanc, err, info.FullMethod)
		return err
	}
}

// TracingGRPCMiddlewareUnary plugs tracer into gRPC unary calls
func TracingGRPCMiddlewareUnary(spec config.TracingSpec) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		span, spanc := TracingSpanFromGRPCContext(ctx, req, info.FullMethod, spec)
		defer span.Span.End()
		defer spanc.Span.End()
		newCtx := context.WithValue(span.Context, correlationKey, SerializeSpanContext(*spanc.SpanContext))
		resp, err := handler(newCtx, req)
		UpdateSpanPairStatusesFromError(span, spanc, err, info.FullMethod)
		return resp, err
	}
}

// TracingSpanFromGRPCContext creates a span from an incoming gRPC method call
func TracingSpanFromGRPCContext(c context.Context, req interface{}, method string, spec config.TracingSpec) (TracerSpan, TracerSpan) {
	var ctx context.Context
	var span *trace.Span
	var ctxc context.Context
	var spanc *trace.Span

	md := extractDaprMetadata(c)
	headers := ""
	re := regexp.MustCompile(`(?i)(&__header_delim__&)?X-Correlation-ID&__header_equals__&[0-9a-fA-F]+;[0-9a-fA-F]+;[0-9a-fA-F]+`)
	corID := strings.Replace(re.FindString(headers), "&__header_delim__&", "", 1)
	if len(corID) > 35 { //to remove the prefix "X-Correlation-Id&__header_equals__&", which may in different casing
		corID = corID[35:]
	}

	rate := diag_utils.GetTraceSamplingRate(spec.SamplingRate)

	// TODO : Continue using ProbabilitySampler till Go SDK starts supporting RateLimiting sampler
	probSamplerOption := trace.WithSampler(trace.ProbabilitySampler(rate))
	serverKindOption := trace.WithSpanKind(trace.SpanKindServer)
	clientKindOption := trace.WithSpanKind(trace.SpanKindClient)
	spanName := createSpanName(method)
	if corID != "" {
		spanContext := DeserializeSpanContext(corID)
		ctx, span = trace.StartSpanWithRemoteParent(c, method, spanContext, serverKindOption, probSamplerOption)
		ctxc, spanc = trace.StartSpanWithRemoteParent(ctx, spanName, span.SpanContext(), clientKindOption, probSamplerOption)
	} else {
		ctx, span = trace.StartSpan(context.Background(), method, serverKindOption, probSamplerOption)
		ctxc, spanc = trace.StartSpanWithRemoteParent(ctx, spanName, span.SpanContext(), clientKindOption, probSamplerOption)
	}

	addAnnotationsFromGRPCMetadata(md, span)

	context := span.SpanContext()
	contextc := spanc.SpanContext()
	return TracerSpan{Context: ctx, Span: span, SpanContext: &context}, TracerSpan{Context: ctxc, Span: spanc, SpanContext: &contextc}
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
