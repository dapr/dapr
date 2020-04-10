// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dapr/dapr/pkg/config"
	dapr_pb "github.com/dapr/dapr/pkg/proto/dapr"
	daprclient_pb "github.com/dapr/dapr/pkg/proto/daprclient"
	daprinternal_pb "github.com/dapr/dapr/pkg/proto/daprinternal"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type key string

const (
	// CorrelationID is the header key name of correlation id for trace
	CorrelationID        = "X-Correlation-ID"
	correlationKey   key = CorrelationID
	daprHeaderPrefix     = "dapr-"
)

// TracerSpan defines a tracing span that a tracer users to keep track of call scopes
type TracerSpan struct {
	Context     context.Context
	Span        *trace.Span
	SpanContext *trace.SpanContext
}

// SerializeSpanContext serializes a span context into a simple string
func SerializeSpanContext(ctx trace.SpanContext) string {
	return fmt.Sprintf("%s;%s;%d", ctx.SpanID.String(), ctx.TraceID.String(), ctx.TraceOptions)
}

// DeserializeSpanContext deserializes a span context from a string
func DeserializeSpanContext(ctx string) trace.SpanContext {
	parts := strings.Split(ctx, ";")
	spanID, _ := hex.DecodeString(parts[0])
	traceID, _ := hex.DecodeString(parts[1])
	traceOptions, _ := strconv.ParseUint(parts[2], 10, 32)
	ret := trace.SpanContext{}
	copy(ret.SpanID[:], spanID)
	copy(ret.TraceID[:], traceID)
	ret.TraceOptions = trace.TraceOptions(traceOptions)
	return ret
}

// DeserializeSpanContextPointer deserializes a span context from a trace pointer
func DeserializeSpanContextPointer(ctx string) *trace.SpanContext {
	if ctx == "" {
		return nil
	}
	context := &trace.SpanContext{}
	*context = DeserializeSpanContext(ctx)
	return context
}

// TraceSpanFromFastHTTPRequest creates a tracing span form a fasthttp request
func TraceSpanFromFastHTTPRequest(r *fasthttp.Request, spec config.TracingSpec) (TracerSpan, TracerSpan) {
	var ctx context.Context
	var span *trace.Span
	var ctxc context.Context
	var spanc *trace.Span

	corID := string(r.Header.Peek(CorrelationID))
	uriSpanName := string(r.Header.RequestURI())
	if corID != "" {
		spanContext := DeserializeSpanContext(corID)
		ctx, span = trace.StartSpanWithRemoteParent(context.Background(), uriSpanName, spanContext, trace.WithSpanKind(trace.SpanKindServer))
		ctxc, spanc = trace.StartSpanWithRemoteParent(ctx, createSpanName(uriSpanName), span.SpanContext(), trace.WithSpanKind(trace.SpanKindClient))
	} else {
		ctx, span = trace.StartSpan(context.Background(), uriSpanName, trace.WithSpanKind(trace.SpanKindServer))
		ctxc, spanc = trace.StartSpanWithRemoteParent(ctx, createSpanName(uriSpanName), span.SpanContext(), trace.WithSpanKind(trace.SpanKindClient))
	}

	addAnnotationsFromHTTPMetadata(r, span)

	context := span.SpanContext()
	contextc := spanc.SpanContext()
	return TracerSpan{Context: ctx, Span: span, SpanContext: &context}, TracerSpan{Context: ctxc, Span: spanc, SpanContext: &contextc}
}

// TraceSpanFromFastHTTPContext creates a tracing span form a fasthttp request context
func TraceSpanFromFastHTTPContext(c *fasthttp.RequestCtx, spec config.TracingSpec) (TracerSpan, TracerSpan) {
	var ctx context.Context
	var span *trace.Span
	var ctxc context.Context
	var spanc *trace.Span

	corID := string(c.Request.Header.Peek(CorrelationID))
	if corID != "" {
		spanContext := DeserializeSpanContext(corID)
		ctx, span = trace.StartSpanWithRemoteParent(context.Background(), string(c.Path()), spanContext, trace.WithSpanKind(trace.SpanKindServer))
		ctxc, spanc = trace.StartSpanWithRemoteParent(ctx, createSpanName(string(c.Path())), span.SpanContext(), trace.WithSpanKind(trace.SpanKindClient))
	} else {
		ctx, span = trace.StartSpan(context.Background(), string(c.Path()), trace.WithSpanKind(trace.SpanKindServer))
		ctxc, spanc = trace.StartSpanWithRemoteParent(ctx, createSpanName(string(c.Path())), span.SpanContext(), trace.WithSpanKind(trace.SpanKindClient))
	}

	addAnnotationsFromHTTPMetadata(&c.Request, span)

	context := span.SpanContext()
	contextc := spanc.SpanContext()
	return TracerSpan{Context: ctx, Span: span, SpanContext: &context}, TracerSpan{Context: ctxc, Span: spanc, SpanContext: &contextc}
}

func addAnnotationsFromHTTPMetadata(req *fasthttp.Request, span *trace.Span) {
	req.Header.VisitAll(func(key []byte, value []byte) {
		headerKey := string(key)
		headerKey = strings.ToLower(headerKey)
		if strings.HasPrefix(headerKey, daprHeaderPrefix) {
			span.AddAttributes(trace.StringAttribute(headerKey, string(value)))
		}
	})
}

// TracingHTTPMiddleware plugs tracer into fasthttp pipeline
func TracingHTTPMiddleware(spec config.TracingSpec, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		span, spanc := TraceSpanFromFastHTTPContext(ctx, spec)
		defer span.Span.End()
		defer spanc.Span.End()
		ctx.Request.Header.Set(CorrelationID, SerializeSpanContext(*spanc.SpanContext))
		next(ctx)
		UpdateSpanPairStatusesFromHTTPResponse(span, spanc, &ctx.Response)
	}
}

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

// UpdateSpanPairStatusesFromHTTPResponse updates tracer span statuses based on HTTP response
func UpdateSpanPairStatusesFromHTTPResponse(span, spanc TracerSpan, resp *fasthttp.Response) {
	spanc.Span.SetStatus(trace.Status{
		Code:    ProjectStatusCode(resp.StatusCode()),
		Message: strconv.Itoa(resp.StatusCode()),
	})
	span.Span.SetStatus(trace.Status{
		Code:    ProjectStatusCode(resp.StatusCode()),
		Message: strconv.Itoa(resp.StatusCode()),
	})
}

// UpdateSpanPairStatusesFromError updates tracer span statuses based on error object
func UpdateSpanPairStatusesFromError(span, spanc TracerSpan, err error, method string) {
	if err != nil {
		spanc.Span.SetStatus(trace.Status{
			Code:    trace.StatusCodeInternal,
			Message: fmt.Sprintf("method %s failed - %s", method, err.Error()),
		})
		span.Span.SetStatus(trace.Status{
			Code:    trace.StatusCodeInternal,
			Message: fmt.Sprintf("method %s failed - %s", method, err.Error()),
		})
	} else {
		spanc.Span.SetStatus(trace.Status{
			Code:    trace.StatusCodeOK,
			Message: fmt.Sprintf("method %s succeeded", method),
		})
		span.Span.SetStatus(trace.Status{
			Code:    trace.StatusCodeOK,
			Message: fmt.Sprintf("method %s succeeded", method),
		})
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
	headers := extractHeaders(req)
	re := regexp.MustCompile(`(?i)(&__header_delim__&)?X-Correlation-ID&__header_equals__&[0-9a-fA-F]+;[0-9a-fA-F]+;[0-9a-fA-F]+`)
	corID := strings.Replace(re.FindString(headers), "&__header_delim__&", "", 1)
	if len(corID) > 35 { //to remove the prefix "X-Correlation-Id&__header_equals__&", which may in different casing
		corID = corID[35:]
	}

	if corID != "" {
		spanContext := DeserializeSpanContext(corID)
		ctx, span = trace.StartSpanWithRemoteParent(c, method, spanContext, trace.WithSpanKind(trace.SpanKindServer))
		ctxc, spanc = trace.StartSpanWithRemoteParent(ctx, createSpanName(method), span.SpanContext(), trace.WithSpanKind(trace.SpanKindClient))
	} else {
		ctx, span = trace.StartSpan(context.Background(), method, trace.WithSpanKind(trace.SpanKindServer))
		ctxc, spanc = trace.StartSpanWithRemoteParent(ctx, createSpanName(method), span.SpanContext(), trace.WithSpanKind(trace.SpanKindClient))
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

func ProjectStatusCode(code int) int32 {
	switch code {
	case 200:
		return trace.StatusCodeOK
	case 201:
		return trace.StatusCodeOK
	case 400:
		return trace.StatusCodeInvalidArgument
	case 500:
		return trace.StatusCodeInternal
	case 404:
		return trace.StatusCodeNotFound
	case 403:
		return trace.StatusCodePermissionDenied
	default:
		return int32(code)
	}
}

func extractHeaders(req interface{}) string {
	if req == nil {
		return ""
	}
	if s, ok := req.(*daprinternal_pb.LocalCallEnvelope); ok {
		return s.Metadata["headers"]
	}
	if s, ok := req.(*daprclient_pb.InvokeEnvelope); ok {
		return s.Metadata["headers"]
	}
	if s, ok := req.(*dapr_pb.InvokeServiceEnvelope); ok {
		return s.Metadata["headers"]
	}
	return ""
}

func createSpanName(name string) string {
	i := strings.Index(name, "/invoke/")
	if i > 0 {
		j := strings.Index(name[i+8:], "/")
		if j > 0 {
			return name[i+8 : i+8+j]
		}
	}
	return name
}

func extractDaprMetadata(ctx context.Context) map[string][]string {
	daprMetadata := make(map[string][]string)
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return daprMetadata
	}

	for k, v := range md {
		k = strings.ToLower(k)
		if strings.HasPrefix(k, daprHeaderPrefix) {
			daprMetadata[k] = v
		}
	}

	return daprMetadata
}
