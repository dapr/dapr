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

// TraceSpanFromFastHTTPRequest creates a tracing span from a fasthttp request
func TraceSpanFromFastHTTPRequest(r *fasthttp.Request, spec config.TracingSpec) (TracerSpan, TracerSpan) {
	uri := string(r.Header.RequestURI())
	return getTraceSpan(r, uri, spec)
}

// TraceSpanFromFastHTTPContext creates a tracing span from a fasthttp request context
func TraceSpanFromFastHTTPContext(c *fasthttp.RequestCtx, spec config.TracingSpec) (TracerSpan, TracerSpan) {
	uri := string(c.Path())
	return getTraceSpan(&c.Request, uri, spec)
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

func getTraceSpan(r *fasthttp.Request, uri string, spec config.TracingSpec) (TracerSpan, TracerSpan) {
	var ctx context.Context
	var span *trace.Span
	var ctxc context.Context
	var spanc *trace.Span

	corID := string(r.Header.Peek(CorrelationID))
	rate := diag_utils.GetTraceSamplingRate(spec.SamplingRate)

	// TODO : Continue using ProbabilitySampler till Go SDK starts supporting RateLimiting sampler
	probSamplerOption := trace.WithSampler(trace.ProbabilitySampler(rate))
	serverKindOption := trace.WithSpanKind(trace.SpanKindServer)
	clientKindOption := trace.WithSpanKind(trace.SpanKindClient)
	spanName := createSpanName(uri)
	if corID != "" {
		spanContext := DeserializeSpanContext(corID)
		ctx, span = trace.StartSpanWithRemoteParent(context.Background(), uri, spanContext, serverKindOption, probSamplerOption)
		ctxc, spanc = trace.StartSpanWithRemoteParent(ctx, spanName, span.SpanContext(), clientKindOption, probSamplerOption)
	} else {
		ctx, span = trace.StartSpan(context.Background(), uri, serverKindOption, probSamplerOption)
		ctxc, spanc = trace.StartSpanWithRemoteParent(ctx, spanName, span.SpanContext(), clientKindOption, probSamplerOption)
	}

	addAnnotationsFromHTTPMetadata(r, span)

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

// UpdateSpanPairStatusesFromHTTPResponse updates tracer span statuses based on HTTP response
func UpdateSpanPairStatusesFromHTTPResponse(span, spanc TracerSpan, resp *fasthttp.Response) {
	spanc.Span.SetStatus(trace.Status{
		Code:    projectStatusCode(resp.StatusCode()),
		Message: strconv.Itoa(resp.StatusCode()),
	})
	span.Span.SetStatus(trace.Status{
		Code:    projectStatusCode(resp.StatusCode()),
		Message: strconv.Itoa(resp.StatusCode()),
	})
}
