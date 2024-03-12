/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package diagnostics

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/valyala/fasthttp"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	"github.com/dapr/dapr/pkg/config"
	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/responsewriter"
)

// We have leveraged the code from opencensus-go plugin to adhere the w3c trace context.
// Reference : https://github.com/census-instrumentation/opencensus-go/blob/master/plugin/ochttp/propagation/tracecontext/propagation.go
const (
	supportedVersion  = 0
	maxVersion        = 254
	maxTracestateLen  = 512
	TraceparentHeader = "traceparent"
	TracestateHeader  = "tracestate"
)

// HTTPTraceMiddleware sets the trace context or starts the trace client span based on request.
func HTTPTraceMiddleware(next http.Handler, appID string, spec config.TracingSpec) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if isHealthzRequest(path) {
			next.ServeHTTP(w, r)
			return
		}

		span := startTracingClientSpanFromHTTPRequest(r, path, spec)

		// Wrap the writer in a ResponseWriter so we can collect stats such as status code and size
		rw := responsewriter.EnsureResponseWriter(w)

		// Before the response is written, we need to add the tracing headers
		rw.Before(func(rw responsewriter.ResponseWriter) {
			// Add span attributes only if it is sampled, which reduced the perf impact.
			if span.SpanContext().IsSampled() {
				AddAttributesToSpan(span, userDefinedHTTPHeaders(r))
				spanAttr := spanAttributesMapFromHTTPContext(rw, r)
				AddAttributesToSpan(span, spanAttr)

				// Correct the span name based on API.
				if sname, ok := spanAttr[diagConsts.DaprAPISpanNameInternal]; ok {
					span.SetName(sname)
				}
			}

			// Check if response has traceparent header and add if absent
			if rw.Header().Get(TraceparentHeader) == "" {
				span = diagUtils.SpanFromContext(r.Context())
				// Using Header.Set here because we know the traceparent header isn't set
				SpanContextToHTTPHeaders(span.SpanContext(), rw.Header().Set)
			}

			UpdateSpanStatusFromHTTPStatus(span, rw.Status())
			span.End()
		})

		next.ServeHTTP(rw, r)
	})
}

// userDefinedHTTPHeaders returns dapr- prefixed header from incoming metadata.
// Users can add dapr- prefixed headers that they want to see in span attributes.
func userDefinedHTTPHeaders(r *http.Request) map[string]string {
	// Allocate this with enough memory for a pessimistic case
	m := make(map[string]string, len(r.Header))

	for key, vSlice := range r.Header {
		if len(vSlice) < 1 || len(key) < (len(daprHeaderBinSuffix)+1) {
			continue
		}

		key = strings.ToLower(key)
		if strings.HasPrefix(key, daprHeaderPrefix) {
			// Get the last value for each key
			m[key] = vSlice[len(vSlice)-1]
		}
	}

	return m
}

func startTracingClientSpanFromHTTPRequest(r *http.Request, spanName string, spec config.TracingSpec) trace.Span {
	sc := SpanContextFromRequest(r)
	ctx := trace.ContextWithRemoteSpanContext(r.Context(), sc)
	kindOption := trace.WithSpanKind(trace.SpanKindClient)
	_, span := tracer.Start(ctx, spanName, kindOption)
	diagUtils.AddSpanToRequest(r, span)
	return span
}

func StartProducerSpanChildFromParent(ctx *fasthttp.RequestCtx, parentSpan trace.Span) trace.Span {
	path := string(ctx.Request.URI().Path())
	netCtx := trace.ContextWithRemoteSpanContext(ctx, parentSpan.SpanContext())
	kindOption := trace.WithSpanKind(trace.SpanKindProducer)
	_, span := tracer.Start(netCtx, path, kindOption)
	return span
}

// SpanContextFromRequest extracts a span context from incoming requests.
func SpanContextFromRequest(r *http.Request) (sc trace.SpanContext) {
	h := r.Header.Get(TraceparentHeader)
	if h == "" {
		return trace.SpanContext{}
	}
	sc, ok := SpanContextFromW3CString(h)
	if ok {
		ts := tracestateFromRequest(r)
		sc = sc.WithTraceState(*ts)
	}
	return sc
}

func isHealthzRequest(name string) bool {
	return strings.Contains(name, "/healthz")
}

// UpdateSpanStatusFromHTTPStatus updates trace span status based on response code.
func UpdateSpanStatusFromHTTPStatus(span trace.Span, code int) {
	if span != nil {
		statusCode, statusDescription := traceStatusFromHTTPCode(code)
		span.SetStatus(statusCode, statusDescription)
	}
}

// https://github.com/open-telemetry/opentelemetry-specification/blob/master/specification/trace/semantic_conventions/http.md#status
func traceStatusFromHTTPCode(httpCode int) (otelcodes.Code, string) {
	code := otelcodes.Unset

	if httpCode >= 400 {
		code = otelcodes.Error
		statusText := http.StatusText(httpCode)
		if statusText == "" {
			statusText = "Unknown"
		}
		codeDescription := "Code(" + strconv.FormatInt(int64(httpCode), 10) + "): " + statusText
		return code, codeDescription
	}
	return code, ""
}

func tracestateFromRequest(r *http.Request) *trace.TraceState {
	h := r.Header.Get(TracestateHeader)
	return TraceStateFromW3CString(h)
}

// SpanContextToHTTPHeaders adds the spancontext in traceparent and tracestate headers.
func SpanContextToHTTPHeaders(sc trace.SpanContext, setHeader func(string, string)) {
	// if sc is empty context, no ops.
	if sc.Equal(trace.SpanContext{}) {
		return
	}
	h := SpanContextToW3CString(sc)
	setHeader(TraceparentHeader, h)
	tracestateToHeader(sc, setHeader)
}

func tracestateToHeader(sc trace.SpanContext, setHeader func(string, string)) {
	if h := TraceStateToW3CString(sc); h != "" && len(h) <= maxTracestateLen {
		setHeader(TracestateHeader, h)
	}
}

func spanAttributesMapFromHTTPContext(rw responsewriter.ResponseWriter, r *http.Request) map[string]string {
	// Init with a worst-case initial capacity of 7, which is the maximum number of unique keys we expect to add.
	// This is just a "hint" to the compiler so when the map is allocated, it has an initial capacity for 7 elements.
	// It's a (minor) perf improvement that allows us to avoid re-allocations which are wasteful on the allocator and GC both.
	m := make(map[string]string, 7)

	// Check if the context contains an AppendSpanAttributes method
	endpointData, _ := r.Context().Value(endpoints.EndpointCtxKey{}).(*endpoints.EndpointCtxData)
	if endpointData != nil && endpointData.Group != nil && endpointData.Group.AppendSpanAttributes != nil {
		endpointData.Group.AppendSpanAttributes(r, m)
	}

	// Populate dapr original api attributes.
	m[diagConsts.DaprAPIProtocolSpanAttributeKey] = diagConsts.DaprAPIHTTPSpanAttrValue
	m[diagConsts.DaprAPISpanAttributeKey] = r.Method + " " + r.URL.Path
	m[diagConsts.DaprAPIStatusCodeSpanAttributeKey] = strconv.Itoa(rw.Status())

	return m
}
