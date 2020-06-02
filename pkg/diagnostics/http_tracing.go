// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"fmt"
	"net/http"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"

	"github.com/dapr/dapr/pkg/config"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/tracestate"
	"google.golang.org/grpc/codes"
)

// We have leveraged the code from opencensus-go plugin to adhere the w3c trace context.
// Reference : https://github.com/census-instrumentation/opencensus-go/blob/master/plugin/ochttp/propagation/tracecontext/propagation.go
const (
	supportedVersion  = 0
	maxVersion        = 254
	maxTracestateLen  = 512
	traceparentHeader = "traceparent"
	tracestateHeader  = "tracestate"
	trimOWSRegexFmt   = `^[\x09\x20]*(.*[^\x20\x09])[\x09\x20]*$`
)

var trimOWSRegExp = regexp.MustCompile(trimOWSRegexFmt)

// SetTracingInHTTPMiddleware sets the trace context or starts the trace client span based on request
func SetTracingInHTTPMiddleware(next fasthttp.RequestHandler, appID string, spec config.TracingSpec) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		sc := GetSpanContextFromRequestContext(ctx, spec)
		path := string(ctx.Request.URI().Path())

		// 1. check if tracing is enabled or not, and if request is health request
		// 2. if tracing is disabled or health request, set the trace context and call the handler
		// 3. if tracing is enabled, start the client or server spans based on the request and call the handler with appropriate span context
		if isHealthzRequest(path) || !diag_utils.IsTracingEnabled(spec.SamplingRate) {
			SpanContextToHTTPHeaders(sc, ctx.Request.Header.Set)
			next(ctx)
		} else {
			newCtx := NewContext((context.Context)(ctx), sc)
			_, span := StartTracingClientSpanFromHTTPContext(newCtx, path, spec)

			SpanContextToHTTPHeaders(span.SpanContext(), ctx.Request.Header.Set)

			next(ctx)

			// add span attributes
			m := getSpanAttributesMapFromHTTPContext(ctx)
			AddAttributesToSpan(span, m)

			UpdateSpanStatusFromHTTPStatus(span, ctx.Response.StatusCode())
			UpdateResponseTraceHeadersHTTP(ctx, span.SpanContext())

			span.End()
		}
	}
}

// StartTracingClientSpanFromHTTPContext creates a client span before invoking http method call
func StartTracingClientSpanFromHTTPContext(ctx context.Context, spanName string, spec config.TracingSpec) (context.Context, *trace.Span) {
	var span *trace.Span
	ctx, span = startTracingSpanInternal(ctx, spanName, spec.SamplingRate, trace.SpanKindClient)
	return ctx, span
}

func GetSpanContextFromRequestContext(ctx *fasthttp.RequestCtx, spec config.TracingSpec) trace.SpanContext {
	spanContext, ok := SpanContextFromRequest(&ctx.Request)

	if !ok {
		spanContext = GetDefaultSpanContext(spec)
	}

	return spanContext
}

// SpanContextFromRequest extracts a span context from incoming requests.
func SpanContextFromRequest(req *fasthttp.Request) (sc trace.SpanContext, ok bool) {
	h, ok := getRequestHeader(req, traceparentHeader)
	if !ok {
		return trace.SpanContext{}, false
	}

	sc, ok = SpanContextFromString(h)

	if ok {
		sc.Tracestate = tracestateFromRequest(req)
	}
	return sc, ok
}

func isHealthzRequest(name string) bool {
	return strings.Contains(name, "/healthz")
}

// UpdateSpanStatusFromHTTPStatus updates trace span status based on response code
func UpdateSpanStatusFromHTTPStatus(span *trace.Span, code int) {
	if span != nil {
		code := codeFromHTTPStatus(code)
		span.SetStatus(trace.Status{Code: int32(code), Message: code.String()})
	}
}

func getRequestHeader(req *fasthttp.Request, name string) (string, bool) {
	s := string(req.Header.Peek(textproto.CanonicalMIMEHeaderKey(name)))
	if s == "" {
		return "", false
	}

	return s, true
}

func tracestateFromRequest(req *fasthttp.Request) *tracestate.Tracestate {
	h, _ := getRequestHeader(req, tracestateHeader)
	return TraceStateFromString(h)
}

func TraceStateFromString(h string) *tracestate.Tracestate {
	if h == "" {
		return nil
	}

	entries := make([]tracestate.Entry, 0, len(h))
	pairs := strings.Split(h, ",")
	hdrLenWithoutOWS := len(pairs) - 1 // Number of commas
	for _, pair := range pairs {
		matches := trimOWSRegExp.FindStringSubmatch(pair)
		if matches == nil {
			return nil
		}
		pair = matches[1]
		hdrLenWithoutOWS += len(pair)
		if hdrLenWithoutOWS > maxTracestateLen {
			return nil
		}
		kv := strings.Split(pair, "=")
		if len(kv) != 2 {
			return nil
		}
		entries = append(entries, tracestate.Entry{Key: kv[0], Value: kv[1]})
	}
	ts, err := tracestate.New(nil, entries...)
	if err != nil {
		return nil
	}

	return ts
}

// UpdateResponseTraceHeadersHTTP updates Dapr generated trace headers in the response if no trace headers found in the response
func UpdateResponseTraceHeadersHTTP(ctx *fasthttp.RequestCtx, sc trace.SpanContext) {
	_, ok := getResponseHeader(&ctx.Response, traceparentHeader)
	// if there is no response headers found, add the Dapr generated SpanContext in the response header
	if !ok {
		SpanContextToHTTPHeaders(sc, ctx.Response.Header.Set)
	}
}

// SpanContextToHTTPHeaders adds the spancontect in traceparent and tracestate headers.
func SpanContextToHTTPHeaders(sc trace.SpanContext, setHeader func(string, string)) {
	h := SpanContextToString(sc)
	setHeader(traceparentHeader, h)
	tracestateToHeader(sc, setHeader)
}

func getResponseHeader(resp *fasthttp.Response, name string) (string, bool) {
	s := string(resp.Header.Peek(textproto.CanonicalMIMEHeaderKey(name)))
	if s == "" {
		return "", false
	}

	return s, true
}

func tracestateToHeader(sc trace.SpanContext, setHeader func(string, string)) {
	var pairs = make([]string, 0, len(sc.Tracestate.Entries()))
	if sc.Tracestate != nil {
		for _, entry := range sc.Tracestate.Entries() {
			pairs = append(pairs, strings.Join([]string{entry.Key, entry.Value}, "="))
		}
		h := strings.Join(pairs, ",")

		if h != "" && len(h) <= maxTracestateLen {
			setHeader(tracestateHeader, h)
		}
	}
}

// GetSpanAttributesMapFromHTTP builds the span trace attributes map for HTTP calls based on given parameters as per open-telemetry specs
func GetSpanAttributesMapFromHTTP(componentType, componentValue, method, route, uri string, statusCode int) map[string]string {
	// Span Attribute reference https://github.com/open-telemetry/opentelemetry-specification/tree/master/specification/trace/semantic_conventions
	m := make(map[string]string)
	switch componentType {
	case "state", "secrets", "bindings":
		m[dbTypeSpanAttributeKey] = componentType
		m[dbInstanceSpanAttributeKey] = componentValue
		// TODO: not possible currently to get the route {state_store} , so using path instead of route
		m[dbStatementSpanAttributeKey] = fmt.Sprintf("%s %s", method, route)
		m[dbURLSpanAttributeKey] = route
	case "invoke", "actors":
		m[httpMethodSpanAttributeKey] = method
		m[httpURLSpanAttributeKey] = uri
		code := codeFromHTTPStatus(statusCode)
		m[httpStatusCodeSpanAttributeKey] = strconv.Itoa(statusCode)
		m[httpStatusTextSpanAttributeKey] = code.String()
	case "publish":
		m[messagingSystemSpanAttributeKey] = componentType
		m[messagingDestinationSpanAttributeKey] = componentValue
		m[messagingDestinationKindSpanAttributeKey] = messagingDestinationKind
	}
	return m
}

func getSpanAttributesMapFromHTTPContext(ctx *fasthttp.RequestCtx) map[string]string {
	// Span Attribute reference https://github.com/open-telemetry/opentelemetry-specification/tree/master/specification/trace/semantic_conventions
	route := string(ctx.Request.URI().Path())
	method := string(ctx.Request.Header.Method())
	uri := ctx.Request.URI().String()
	statusCode := ctx.Response.StatusCode()
	r := getAPIComponent(route)
	return GetSpanAttributesMapFromHTTP(r.componentType, r.componentValue, method, route, uri, statusCode)
}

// CodeFromHTTPStatus converts http status code to gRPC status code
// See: https://github.com/grpc/grpc/blob/master/doc/http-grpc-status-mapping.md
func codeFromHTTPStatus(httpStatusCode int) codes.Code {
	switch httpStatusCode {
	case http.StatusOK:
		return codes.OK
	case http.StatusRequestTimeout:
		return codes.Canceled
	case http.StatusInternalServerError:
		return codes.Unknown
	case http.StatusBadRequest:
		return codes.Internal
	case http.StatusGatewayTimeout:
		return codes.DeadlineExceeded
	case http.StatusNotFound:
		return codes.NotFound
	case http.StatusConflict:
		return codes.AlreadyExists
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusTooManyRequests:
		return codes.ResourceExhausted
	case http.StatusNotImplemented:
		return codes.Unimplemented
	case http.StatusServiceUnavailable:
		return codes.Unavailable
	}

	return codes.Unknown
}
