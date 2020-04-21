// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"strconv"
	"strings"

	"github.com/dapr/dapr/pkg/config"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
)

type contextKey struct{}

// StartClientSpanTracing creates a tracing span from a http request
func StartClientSpanTracing(ctx context.Context, r *fasthttp.Request, spec config.TracingSpec) (context.Context, *trace.Span) {
	corID := string(r.Header.Peek(CorrelationID))
	uri := string(r.Header.RequestURI())
	var span *trace.Span

	ctx, span = startTracingSpan(ctx, corID, uri, spec.SamplingRate, trace.SpanKindClient)
	addAnnotationsToSpan(r, span)

	return ctx, span
}

// StartServerSpanTracing plugs tracing into http middleware pipeline
func StartServerSpanTracing(spec config.TracingSpec, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		corID := string(ctx.Request.Header.Peek(CorrelationID))
		uri := string(ctx.Path())
		_, span := startTracingSpan(ctx, corID, uri, spec.SamplingRate, trace.SpanKindServer)

		addAnnotationsToSpan(&ctx.Request, span)
		defer span.End()

		// Pass on the correlation id further in the request header
		ctx.Request.Header.Set(CorrelationID, SerializeSpanContext(span.SpanContext()))

		next(ctx)
		UpdateSpanStatus(span, &ctx.Response)
	}
}

// SetTracingSpanContext sets the trace spancontext in the request context
func SetTracingSpanContext(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		c := WithSpanContext(ctx)
		ctx = c.(*fasthttp.RequestCtx)
		next(ctx)
	}
}

func addAnnotationsToSpan(req *fasthttp.Request, span *trace.Span) {
	req.Header.VisitAll(func(key []byte, value []byte) {
		headerKey := string(key)
		headerKey = strings.ToLower(headerKey)
		if strings.HasPrefix(headerKey, daprHeaderPrefix) {
			span.AddAttributes(trace.StringAttribute(headerKey, string(value)))
		}
	})
}

// UpdateSpanStatus updates trace span status based on HTTP response
func UpdateSpanStatus(span *trace.Span, resp *fasthttp.Response) {
	span.SetStatus(trace.Status{
		Code:    projectStatusCode(resp.StatusCode()),
		Message: strconv.Itoa(resp.StatusCode()),
	})
}

func WithSpanContext(ctx context.Context) context.Context {
	spanContext := FromContext(ctx)

	gen := tracingConfig.Load().(*traceIDGenerator)

	if (spanContext == trace.SpanContext{}) {
		spanContext = trace.SpanContext{}
		spanContext.TraceID = gen.NewTraceID()
		spanContext.SpanID = gen.NewSpanID()
	} else {
		spanContext.SpanID = gen.NewSpanID()
	}

	return NewContext(ctx, spanContext)
}

// FromContext returns the SpanContext stored in a context, or empty if there isn't one.
func FromContext(ctx context.Context) trace.SpanContext {
	s, _ := ctx.Value(contextKey{}).(string)
	return DeserializeSpanContext(s)
}

// NewContext returns a new context with the given SpanContext attached in serialized value
func NewContext(parent context.Context, spanContext trace.SpanContext) context.Context {
	return context.WithValue(parent, contextKey{}, SerializeSpanContext(spanContext))
}

func startTracingSpan(ctx context.Context, corID, uri, samplingRate string, spanKind int) (context.Context, *trace.Span) {
	var span *trace.Span
	name := createSpanName(uri)

	rate := diag_utils.GetTraceSamplingRate(samplingRate)

	// TODO : Continue using ProbabilitySampler till Go SDK starts supporting RateLimiting sampler
	probSamplerOption := trace.WithSampler(trace.ProbabilitySampler(rate))
	kindOption := trace.WithSpanKind(spanKind)

	if corID != "" {
		sc := DeserializeSpanContext(corID)
		// Note that if parent span context is provided which is sc in this case then ctx will be ignored
		ctx, span = trace.StartSpanWithRemoteParent(ctx, name, sc, kindOption, probSamplerOption)
	} else {
		ctx, span = trace.StartSpan(ctx, name, kindOption, probSamplerOption)
	}

	return ctx, span
}
