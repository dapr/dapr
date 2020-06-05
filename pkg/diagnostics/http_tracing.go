// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
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
		path := string(ctx.Request.URI().Path())
		if isHealthzRequest(path) {
			next(ctx)
			return
		}

		var spanName = path
		// Instead of generating the span in direct_messaging, dapr changes the spanname
		// to CallLocal.
		if strings.HasPrefix(path, "/v1.0/invoke/") {
			spanName = daprServiceInvocationFullMethod
		}

		// TODO: set actor invocation spanname
		ctx, span := startTracingClientSpanFromHTTPContext(ctx, spanName, spec)
		next(ctx)

		// add span attributes
		if span.SpanContext().TraceOptions.IsSampled() {
			m := spanAttributesMapFromHTTPContext(ctx)
			AddAttributesToSpan(span, m)
		}

		UpdateSpanStatusFromHTTPStatus(span, ctx.Response.StatusCode())
		span.End()
	}
}

func startTracingClientSpanFromHTTPContext(ctx *fasthttp.RequestCtx, spanName string, spec config.TracingSpec) (*fasthttp.RequestCtx, *trace.Span) {
	sc, _ := SpanContextFromRequest(&ctx.Request)
	probSamplerOption := diag_utils.TraceSampler(spec.SamplingRate)
	kindOption := trace.WithSpanKind(trace.SpanKindClient)

	_, span := trace.StartSpanWithRemoteParent(ctx, spanName, sc, kindOption, probSamplerOption)
	diag_utils.SpanToFastHTTPContext(ctx, span)
	return ctx, span
}

// SpanContextFromRequest extracts a span context from incoming requests.
func SpanContextFromRequest(req *fasthttp.Request) (sc trace.SpanContext, ok bool) {
	h, ok := getRequestHeader(req, traceparentHeader)
	if !ok {
		return trace.SpanContext{}, false
	}
	sc, ok = SpanContextFromW3CString(h)
	if ok {
		sc.Tracestate = tracestateFromRequest(req)
	}
	return sc, ok
}

// SpanContextToRequest modifies the given request to include traceparent and tracestate headers.
func SpanContextToRequest(sc trace.SpanContext, req *fasthttp.Request) {
	if (sc != trace.SpanContext{}) {
		h := SpanContextToW3CString(sc)
		req.Header.Set(traceparentHeader, h)
		tracestateToRequest(sc, req)
	}
}

func isHealthzRequest(name string) bool {
	return strings.Contains(name, "/healthz")
}

// UpdateSpanStatusFromHTTPStatus updates trace span status based on response code
func UpdateSpanStatusFromHTTPStatus(span *trace.Span, code int) {
	if span != nil {
		span.SetStatus(traceStatusFromHTTPCode(code))
	}
}

// https://github.com/open-telemetry/opentelemetry-specification/blob/master/specification/trace/semantic_conventions/http.md#status
func traceStatusFromHTTPCode(httpCode int) trace.Status {
	var code codes.Code = codes.Unknown
	switch httpCode {
	case http.StatusUnauthorized:
		code = codes.Unauthenticated
	case http.StatusForbidden:
		code = codes.PermissionDenied
	case http.StatusNotFound:
		code = codes.NotFound
	case http.StatusTooManyRequests:
		code = codes.ResourceExhausted
	case http.StatusNotImplemented:
		code = codes.Unimplemented
	case http.StatusServiceUnavailable:
		code = codes.Unavailable
	case http.StatusGatewayTimeout:
		code = codes.DeadlineExceeded
	}

	if code == codes.Unknown {
		if httpCode >= 100 && httpCode < 300 {
			code = codes.OK
		} else if httpCode >= 300 && httpCode < 400 {
			code = codes.DeadlineExceeded
		} else if httpCode >= 400 && httpCode < 500 {
			code = codes.InvalidArgument
		} else if httpCode >= 500 {
			code = codes.Internal
		}
	}

	return trace.Status{Code: int32(code), Message: code.String()}
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

func tracestateToRequest(sc trace.SpanContext, req *fasthttp.Request) {
	var pairs = make([]string, 0, len(sc.Tracestate.Entries()))
	if sc.Tracestate != nil {
		for _, entry := range sc.Tracestate.Entries() {
			pairs = append(pairs, strings.Join([]string{entry.Key, entry.Value}, "="))
		}
		h := strings.Join(pairs, ",")

		if h != "" && len(h) <= maxTracestateLen {
			req.Header.Set(tracestateHeader, h)
		}
	}
}

func getContextValue(ctx *fasthttp.RequestCtx, key string) string {
	if ctx.UserValue(key) == nil {
		return ""
	}
	return ctx.UserValue(key).(string)
}

func getAPIComponent(apiPath string) (string, string) {
	// Dapr API reference : https://github.com/dapr/docs/tree/master/reference/api
	// example : apiPath /v1.0/state/statestore
	if apiPath == "" {
		return "", ""
	}

	// Split up to 4 delimiters in '/v1.0/state/statestore/key' to get component api type and value
	var tokens = strings.SplitN(apiPath, "/", 4)
	if len(tokens) < 3 {
		return "", ""
	}

	// return 'state', 'statestore' from the parsed tokens in apiComponent type
	return tokens[1], tokens[2]
}

func spanAttributesMapFromHTTPContext(ctx *fasthttp.RequestCtx) map[string]string {
	// Span Attribute reference https://github.com/open-telemetry/opentelemetry-specification/tree/master/specification/trace/semantic_conventions
	path := string(ctx.Request.URI().Path())
	method := string(ctx.Request.Header.Method())
	statusCode := ctx.Response.StatusCode()

	m := make(map[string]string)

	_, componentType := getAPIComponent(path)

	var dbType string
	switch componentType {
	case "state":
		dbType = stateBuildingBlockType
		m[dbInstanceSpanAttributeKey] = getContextValue(ctx, "storeName")

	case "secrets":
		dbType = secretBuildingBlockType
		m[dbInstanceSpanAttributeKey] = getContextValue(ctx, "secretStoreName")

	case "bindings":
		dbType = bindingBuildingBlockType
		m[dbInstanceSpanAttributeKey] = getContextValue(ctx, "name")

	case "invoke":
		m[gRPCServiceSpanAttributeKey] = daprGRPCServiceInvocationService
		m[netPeerNameSpanAttributeKey] = getContextValue(ctx, "id")

	case "publish":
		m[messagingSystemSpanAttributeKey] = pubsubBuildingBlockType
		m[messagingDestinationSpanAttributeKey] = getContextValue(ctx, "topic")
		m[messagingDestinationKindSpanAttributeKey] = messagingDestinationTopicKind

	case "actor":
		// TODO: support later
	}

	// Populate the rest of database attributes.
	if _, ok := m[dbInstanceSpanAttributeKey]; ok {
		m[dbTypeSpanAttributeKey] = dbType
		m[dbStatementSpanAttributeKey] = fmt.Sprintf("%s %s", method, path)
		m[dbURLSpanAttributeKey] = dbType
	}

	// Populate dapr original api attributes.
	m[daprAPIProtocolSpanAttributeKey] = daprAPIHTTPSpanAttrValue
	m[daprAPISpanAttributeKey] = fmt.Sprintf("%s %s", method, path)
	m[daprAPIStatusCodeSpanAttributeKey] = strconv.Itoa(statusCode)

	return m
}
