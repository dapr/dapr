package diagnostics

import (
	"context"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/actionscore/actions/pkg/config"
	"github.com/actionscore/actions/pkg/exporters"
	actions "github.com/actionscore/actions/pkg/proto"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
	grpc_go "google.golang.org/grpc"
)

const (
	correlationID = "X-Correlation-ID"
)

// TracerSpan defines a tracing span that a tracer users to keep track of call scopes
type TracerSpan struct {
	Context     context.Context
	Span        *trace.Span
	SpanContext *trace.SpanContext
}

//SerializeSpanContext seralizes a span context into a simple string
func SerializeSpanContext(ctx trace.SpanContext) string {
	return fmt.Sprintf("%s;%s;%d", ctx.SpanID.String(), ctx.TraceID.String(), ctx.TraceOptions)
}

//DeserializeSpanContext deseralizes a span cotnext from a string
func DeserializeSpanContext(ctx string) trace.SpanContext {
	parts := strings.Split(ctx, ";")
	spanID, _ := hex.DecodeString(parts[0])
	traceID, _ := hex.DecodeString(parts[1])
	traceOptions, _ := strconv.ParseUint(parts[2], 10, 32)
	ret := trace.SpanContext{}
	copy(ret.SpanID[:], spanID[:])
	copy(ret.TraceID[:], traceID[:])
	ret.TraceOptions = trace.TraceOptions(traceOptions)
	return ret
}

// DeserializeSpanContextPointer deseralizes a span context from a trace pointer
func DeserializeSpanContextPointer(ctx string) *trace.SpanContext {
	if ctx == "" {
		return nil
	}
	context := &trace.SpanContext{}
	*context = DeserializeSpanContext(ctx)
	return context
}

// TraceSpanFromFastHTTPContext creates a tracing span form a fasthttp request context
func TraceSpanFromFastHTTPContext(c *fasthttp.RequestCtx, spec config.TracingSpec) TracerSpan {
	var ctx context.Context
	var span *trace.Span

	corID := string(c.Request.Header.Peek(correlationID))
	if corID != "" {
		spanContext := DeserializeSpanContext(corID)
		ctx, span = trace.StartSpanWithRemoteParent(context.Background(), string(c.Path()), spanContext)
	} else {
		ctx, span = trace.StartSpan(context.Background(), string(c.Path()))
	}

	addAnnotations(c, span, spec.ExpandParams, spec.IncludeBody)

	context := span.SpanContext()
	return TracerSpan{Context: ctx, Span: span, SpanContext: &context}
}

func addAnnotations(ctx *fasthttp.RequestCtx, span *trace.Span, expandParams bool, includeBody bool) {
	if expandParams {
		ctx.VisitUserValues(func(key []byte, value interface{}) {
			span.AddAttributes(trace.StringAttribute(string(key), value.(string)))
		})
		ctx.Request.Header.VisitAll(func(key []byte, value []byte) {
			span.AddAttributes(trace.StringAttribute(string(key), string(value)))
		})
	}
	if includeBody {
		span.AddAttributes(trace.StringAttribute("data", string(ctx.PostBody())))
	}
}

// TracingHTTPMiddleware plugs tracer into fasthttp pipeline
func TracingHTTPMiddleware(spec config.TracingSpec, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		span := TraceSpanFromFastHTTPContext(ctx, spec)
		defer span.Span.End()
		ctx.Request.Header.Set(correlationID, SerializeSpanContext(*span.SpanContext))
		next(ctx)
		span.Span.SetStatus(trace.Status{
			Code:    projectStatusCode(ctx.Response.StatusCode()),
			Message: ctx.Response.String(),
		})
	}
}

// CreateExporter creates Opencensus exported as per specified in the tracing spec
func CreateExporter(actionID string, hostAddress string, spec config.TracingSpec, buffer *string) {
	switch spec.ExporterType {
	case "zipkin":
		ex := exporters.ZipkinExporter{}
		ex.Init(actionID, hostAddress, spec.ExporterAddress)
	case "string":
		es := exporters.StringExporter{Buffer: buffer}
		es.Init(actionID, hostAddress, spec.ExporterAddress)
	}
}

// TracingGRPCMiddleware plugs tracer into gRPC stream
func TracingGRPCMiddleware(spec config.TracingSpec) grpc_go.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		span := TracingSpanFromGRPCContext(stream.Context(), nil, info.FullMethod, spec)
		wrappedStream := grpc_middleware.WrapServerStream(stream)
		wrappedStream.WrappedContext = context.WithValue(span.Context, correlationID, SerializeSpanContext(*span.SpanContext))
		defer span.Span.End()
		err := handler(srv, wrappedStream)
		if err != nil {
			span.Span.SetStatus(trace.Status{
				Code:    trace.StatusCodeInternal,
				Message: fmt.Sprintf("method %s failed - %s", info.FullMethod, err.Error()),
			})
		} else {
			span.Span.SetStatus(trace.Status{
				Code:    trace.StatusCodeOK,
				Message: fmt.Sprintf("method %s succeeded", info.FullMethod),
			})
		}
		return err
	}
}

// TracingGRPCMiddlewareUnary plugs tracer into gRPC unary calls
func TracingGRPCMiddlewareUnary(spec config.TracingSpec) grpc_go.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		span := TracingSpanFromGRPCContext(ctx, req, info.FullMethod, spec)
		defer span.Span.End()
		newCtx := context.WithValue(span.Context, correlationID, SerializeSpanContext(*span.SpanContext))
		resp, err := handler(newCtx, req)
		if err != nil {
			span.Span.SetStatus(trace.Status{
				Code:    trace.StatusCodeInternal,
				Message: fmt.Sprintf("method %s failed - %s", info.FullMethod, err.Error()),
			})
		} else {
			span.Span.SetStatus(trace.Status{
				Code:    trace.StatusCodeOK,
				Message: fmt.Sprintf("method %s succeeded", info.FullMethod),
			})
		}
		return resp, err
	}
}

func TracingSpanFromGRPCContext(c context.Context, req interface{}, method string, spec config.TracingSpec) TracerSpan {
	var ctx context.Context
	var span *trace.Span

	md := metautils.ExtractIncoming(c)
	headers := extractHeaders(req)
	re := regexp.MustCompile(`(?i)(&__header_delim__&)?X-Correlation-ID&__header_equals__&[0-9a-fA-F]+;[0-9a-fA-F]+;[0-9a-fA-F]+`)
	corID := strings.Replace(re.FindString(headers), "&__header_delim__&", "", 1)
	if len(corID) > 35 {
		corID = corID[35:]
	}

	if corID != "" {
		spanContext := DeserializeSpanContext(corID)
		ctx, span = trace.StartSpanWithRemoteParent(context.Background(), method, spanContext)
	} else {
		ctx, span = trace.StartSpan(context.Background(), method)
	}

	addAnnotationsFromMD(md, span, spec.ExpandParams, spec.IncludeBody)

	context := span.SpanContext()
	return TracerSpan{Context: ctx, Span: span, SpanContext: &context}
}

func addAnnotationsFromMD(md metautils.NiceMD, span *trace.Span, expandParams bool, includeBody bool) {
	if expandParams {
		for k, vv := range md {
			for _, v := range vv {
				span.AddAttributes(trace.StringAttribute(string(k), v))
			}
		}
	}
	if includeBody {
		//TODO: get request body?
	}
}

func projectStatusCode(code int) int32 {
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
	if s, ok := req.(*actions.LocalCallEnvelope); ok {
		return s.Metadata["headers"]
	}
	if s, ok := req.(*actions.AppMethodCallEnvelope); ok {
		return s.Metadata["headers"]
	}
	if s, ok := req.(*actions.CallRemoteAppEnvelope); ok {
		return s.Metadata["headers"]
	}
	return ""
}
