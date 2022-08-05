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
	"fmt"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/valyala/fasthttp"

	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/dapr/dapr/pkg/config"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

// We have leveraged the code from opencensus-go plugin to adhere the w3c trace context.
// Reference : https://github.com/census-instrumentation/opencensus-go/blob/master/plugin/ochttp/propagation/tracecontext/propagation.go
const (
	supportedVersion  = 0
	maxVersion        = 254
	maxTracestateLen  = 512
	traceparentHeader = "traceparent"
	tracestateHeader  = "tracestate"
)

// HTTPTraceMiddleware sets the trace context or starts the trace client span based on request.
func HTTPTraceMiddleware(next fasthttp.RequestHandler, appID string, spec config.TracingSpec) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		path := string(ctx.Request.URI().Path())
		if isHealthzRequest(path) {
			next(ctx)
			return
		}

		ctx, span := startTracingClientSpanFromHTTPContext(ctx, path, spec)
		next(ctx)

		// Add span attributes only if it is sampled, which reduced the perf impact.
		if span.SpanContext().IsSampled() {
			AddAttributesToSpan(span, userDefinedHTTPHeaders(ctx))
			spanAttr := spanAttributesMapFromHTTPContext(ctx)
			AddAttributesToSpan(span, spanAttr)

			// Correct the span name based on API.
			if sname, ok := spanAttr[daprAPISpanNameInternal]; ok {
				span.SetName(sname)
			}
		}

		// Check if response has traceparent header and add if absent
		if ctx.Response.Header.Peek(traceparentHeader) == nil {
			span = diag_utils.SpanFromContext(ctx)
			SpanContextToHTTPHeaders(span.SpanContext(), ctx.Response.Header.Set)
		}

		UpdateSpanStatusFromHTTPStatus(span, ctx.Response.StatusCode())
		span.End()
	}
}

// userDefinedHTTPHeaders returns dapr- prefixed header from incoming metadata.
// Users can add dapr- prefixed headers that they want to see in span attributes.
func userDefinedHTTPHeaders(reqCtx *fasthttp.RequestCtx) map[string]string {
	m := map[string]string{}

	reqCtx.Request.Header.VisitAll(func(key []byte, value []byte) {
		k := strings.ToLower(string(key))
		if strings.HasPrefix(k, daprHeaderPrefix) {
			m[k] = string(value)
		}
	})

	return m
}

func startTracingClientSpanFromHTTPContext(ctx *fasthttp.RequestCtx, spanName string, spec config.TracingSpec) (*fasthttp.RequestCtx, trace.Span) {
	sc, _ := SpanContextFromRequest(&ctx.Request)
	netCtx := trace.ContextWithRemoteSpanContext(ctx, sc)
	kindOption := trace.WithSpanKind(trace.SpanKindClient)
	_, span := tracer.Start(netCtx, spanName, kindOption)
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
		ts := tracestateFromRequest(req)
		sc = sc.WithTraceState(*ts)
	}
	return sc, ok
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

func getRequestHeader(req *fasthttp.Request, name string) (string, bool) {
	s := string(req.Header.Peek(textproto.CanonicalMIMEHeaderKey(name)))
	if s == "" {
		return "", false
	}

	return s, true
}

func tracestateFromRequest(req *fasthttp.Request) *trace.TraceState {
	h, _ := getRequestHeader(req, tracestateHeader)
	return TraceStateFromW3CString(h)
}

// SpanContextToHTTPHeaders adds the spancontext in traceparent and tracestate headers.
func SpanContextToHTTPHeaders(sc trace.SpanContext, setHeader func(string, string)) {
	// if sc is empty context, no ops.
	if sc.Equal(trace.SpanContext{}) {
		return
	}
	h := SpanContextToW3CString(sc)
	setHeader(traceparentHeader, h)
	tracestateToHeader(sc, setHeader)
}

func tracestateToHeader(sc trace.SpanContext, setHeader func(string, string)) {
	if h := TraceStateToW3CString(sc); h != "" && len(h) <= maxTracestateLen {
		setHeader(tracestateHeader, h)
	}
}

func getContextValue(ctx *fasthttp.RequestCtx, key string) string {
	if ctx.UserValue(key) == nil {
		return ""
	}
	return ctx.UserValue(key).(string)
}

func getAPIComponent(apiPath string) (string, string) {
	// Dapr API reference : https://docs.dapr.io/reference/api/
	// example : apiPath /v1.0/state/statestore
	if apiPath == "" {
		return "", ""
	}

	// Split up to 4 delimiters in '/v1.0/state/statestore/key' to get component api type and value
	tokens := strings.SplitN(apiPath, "/", 4)
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

	m := map[string]string{}
	_, componentType := getAPIComponent(path)

	var dbType string
	switch componentType {
	case "state":
		dbType = stateBuildingBlockType
		m[dbNameSpanAttributeKey] = getContextValue(ctx, "storeName")

	case "secrets":
		dbType = secretBuildingBlockType
		m[dbNameSpanAttributeKey] = getContextValue(ctx, "secretStoreName")

	case "bindings":
		dbType = bindingBuildingBlockType
		m[dbNameSpanAttributeKey] = getContextValue(ctx, "name")

	case "invoke":
		m[gRPCServiceSpanAttributeKey] = daprGRPCServiceInvocationService
		targetID := getContextValue(ctx, "id")
		m[netPeerNameSpanAttributeKey] = targetID
		m[daprAPISpanNameInternal] = fmt.Sprintf("CallLocal/%s/%s", targetID, getContextValue(ctx, "method"))

	case "publish":
		m[messagingSystemSpanAttributeKey] = pubsubBuildingBlockType
		m[messagingDestinationSpanAttributeKey] = getContextValue(ctx, "topic")
		m[messagingDestinationKindSpanAttributeKey] = messagingDestinationTopicKind

	case "actors":
		dbType = populateActorParams(ctx, m)
	}

	// Populate the rest of database attributes.
	if _, ok := m[dbNameSpanAttributeKey]; ok {
		m[dbSystemSpanAttributeKey] = dbType
		m[dbStatementSpanAttributeKey] = fmt.Sprintf("%s %s", method, path)
		m[dbConnectionStringSpanAttributeKey] = dbType
	}

	// Populate dapr original api attributes.
	m[daprAPIProtocolSpanAttributeKey] = daprAPIHTTPSpanAttrValue
	m[daprAPISpanAttributeKey] = fmt.Sprintf("%s %s", method, path)
	m[daprAPIStatusCodeSpanAttributeKey] = strconv.Itoa(statusCode)

	return m
}

func populateActorParams(ctx *fasthttp.RequestCtx, m map[string]string) string {
	actorType := getContextValue(ctx, "actorType")
	actorID := getContextValue(ctx, "actorId")
	if actorType == "" || actorID == "" {
		return ""
	}

	path := string(ctx.Request.URI().Path())
	// Split up to 7 delimiters in '/v1.0/actors/{actorType}/{actorId}/method/{method}'
	// to get component api type and value
	tokens := strings.SplitN(path, "/", 7)
	if len(tokens) < 7 {
		return ""
	}

	m[daprAPIActorTypeID] = fmt.Sprintf("%s.%s", actorType, actorID)

	dbType := ""
	switch tokens[5] {
	case "method":
		m[gRPCServiceSpanAttributeKey] = daprGRPCServiceInvocationService
		m[netPeerNameSpanAttributeKey] = m[daprAPIActorTypeID]
		m[daprAPISpanNameInternal] = fmt.Sprintf("CallActor/%s/%s", actorType, getContextValue(ctx, "method"))

	case "state":
		dbType = stateBuildingBlockType
		m[dbNameSpanAttributeKey] = "actor"
	}

	return dbType
}
