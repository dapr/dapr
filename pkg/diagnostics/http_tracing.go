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
func TraceSpanFromFastHTTPRequest(r *fasthttp.Request, spec config.TracingSpec) TracerSpan {
	uri := string(r.Header.RequestURI())
	return getTraceSpan(r, uri, spec)
}

// TraceSpanFromFastHTTPContext creates a tracing span from a fasthttp request context
func TraceSpanFromFastHTTPContext(c *fasthttp.RequestCtx, spec config.TracingSpec) TracerSpan {
	uri := string(c.Path())
	return getTraceSpan(&c.Request, uri, spec)
}

// TracingHTTPMiddleware plugs tracer into fasthttp pipeline
func TracingHTTPMiddleware(spec config.TracingSpec, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		span := TraceSpanFromFastHTTPContext(ctx, spec)
		defer span.Span.End()
		ctx.Request.Header.Set(CorrelationID, SerializeSpanContext(span.Span.SpanContext()))

		next(ctx)
		UpdateSpanPairStatusesFromHTTPResponse(span, &ctx.Response)
	}
}

func getTraceSpan(r *fasthttp.Request, uri string, spec config.TracingSpec) TracerSpan {
	var ctx = context.Background()
	var span *trace.Span

	corID := string(r.Header.Peek(CorrelationID))
	rate := diag_utils.GetTraceSamplingRate(spec.SamplingRate)

	// TODO : Continue using ProbabilitySampler till Go SDK starts supporting RateLimiting sampler
	probSamplerOption := trace.WithSampler(trace.ProbabilitySampler(rate))
	serverKindOption := trace.WithSpanKind(trace.SpanKindServer)
	spanName := createSpanName(uri)
	if corID != "" {
		spanContext := DeserializeSpanContext(corID)
		ctx, span = trace.StartSpanWithRemoteParent(ctx, spanName, spanContext, serverKindOption, probSamplerOption)
	} else {
		ctx, span = trace.StartSpan(ctx, spanName, serverKindOption, probSamplerOption)
	}

	addAnnotationsFromHTTPMetadata(r, span)

	return TracerSpan{Context: ctx, Span: span}
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
func UpdateSpanPairStatusesFromHTTPResponse(span TracerSpan, resp *fasthttp.Response) {
	span.Span.SetStatus(trace.Status{
		Code:    projectStatusCode(resp.StatusCode()),
		Message: strconv.Itoa(resp.StatusCode()),
	})
}
