package diagnostics

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/valyala/fasthttp"

	"github.com/actionscore/actions/pkg/config"
	"github.com/actionscore/actions/pkg/exporters"
	"go.opencensus.io/trace"
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
			Code:    int32(ctx.Response.StatusCode()),
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
