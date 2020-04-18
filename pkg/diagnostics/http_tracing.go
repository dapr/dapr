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
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
)

// StartClientSpanTracing creates a tracing span from a http request
func StartClientSpanTracing(r *fasthttp.Request, spec config.TracingSpec) (context.Context, *trace.Span) {
	corID := string(r.Header.Peek(CorrelationID))
	uri := string(r.Header.RequestURI())
	var ctx = context.Background()
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
