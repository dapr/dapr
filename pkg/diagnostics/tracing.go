package diagnostics

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/actionscore/actions/pkg/config"
	"github.com/actionscore/actions/pkg/exporters"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
	grpc_go "google.golang.org/grpc"
)

const (
	// CorrelationID is an opencensus corellation id
	CorrelationID = "correlation-id"
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

func TraceSpanFromFastHTTPContext(c *fasthttp.RequestCtx, spec config.TracingSpec) TracerSpan {
	var ctx context.Context
	var span *trace.Span

	corID := string(c.Request.Header.Peek(CorrelationID))
	if corID != "" {
		spanContext := DeserializeSpanContext(corID)
		ctx, span = trace.StartSpanWithRemoteParent(context.Background(), string(c.Path()), spanContext)
	} else {
		ctx, span = trace.StartSpan(context.Background(), string(c.Path()))
	}

	addAnnotations(c, span, spec.ExpandParams, spec.IncludeBody)

	var context *trace.SpanContext
	context = &trace.SpanContext{}
	*context = span.SpanContext()
	return TracerSpan{Context: ctx, Span: span, SpanContext: context}
}

func addAnnotations(ctx *fasthttp.RequestCtx, span *trace.Span, expandParams bool, includeBody bool) {
	if expandParams {
		ctx.VisitUserValues(func(key []byte, value interface{}) {
			span.AddAttributes(trace.StringAttribute(string(key), value.(string)))
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
		ctx.Request.Header.Set(CorrelationID, SerializeSpanContext(*span.SpanContext))
		next(ctx)
		span.Span.SetStatus(trace.Status{
			Code:    projectStatusCode(ctx.Response.StatusCode()),
			Message: ctx.Response.String(),
		})
	}
}

func CreateExporter(action_id string, host_port string, spec config.TracingSpec, buffer *string) {
	switch spec.ExporterType {
	case "zipkin":
		ex := exporters.ZipkinExporter{}
		ex.Init(action_id, host_port, spec.ExporterAddress)
	case "string":
		es := exporters.StringExporter{Buffer: buffer}
		es.Init(action_id, host_port, spec.ExporterAddress)
	}
}

func TracingGRPCMiddleware(spec config.TracingSpec) grpc_go.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		span := TracingSpanFromGRPCContext(stream.Context(), info.FullMethod, spec)
		wrappedStream := grpc_middleware.WrapServerStream(stream)
		wrappedStream.WrappedContext = context.WithValue(span.Context, CorrelationID, SerializeSpanContext(*span.SpanContext))
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

func TracingGRPCMiddlewareUnary(spec config.TracingSpec) grpc_go.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		span := TracingSpanFromGRPCContext(ctx, info.FullMethod, spec)
		defer span.Span.End()
		newCtx := context.WithValue(span.Context, CorrelationID, SerializeSpanContext(*span.SpanContext))
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

func TracingSpanFromGRPCContext(c context.Context, method string, spec config.TracingSpec) TracerSpan {
	var ctx context.Context
	var span *trace.Span

	md := metautils.ExtractIncoming(c)
	corID := md.Get(CorrelationID)

	if corID != "" {
		spanContext := DeserializeSpanContext(corID)
		ctx, span = trace.StartSpanWithRemoteParent(context.Background(), method, spanContext)
	} else {
		ctx, span = trace.StartSpan(context.Background(), method)
	}

	addAnnotationsFromMD(md, span, spec.ExpandParams, spec.IncludeBody)

	var context *trace.SpanContext
	context = &trace.SpanContext{}
	*context = span.SpanContext()
	return TracerSpan{Context: ctx, Span: span, SpanContext: context}
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
