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
	"context"
	"fmt"
	"strings"

	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	apitrace "go.opentelemetry.io/otel/trace"

	"github.com/dapr/dapr/pkg/diagnostics/propagation"
	isemconv "github.com/dapr/dapr/pkg/diagnostics/semconv"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

// HTTPTraceMiddleware sets the trace context or starts the trace client span based on request.
func HTTPTraceMiddleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		path := string(ctx.Request.URI().Path())
		if isHealthzRequest(path) {
			next(ctx)
			return
		}

		supplier := propagation.NewFasthttpSupplier(&ctx.Request.Header)
		newCtx, span := startTracingClientSpanFromCarrier(supplier, path)
		otel.GetTextMapPropagator().Inject(newCtx, supplier)
		next(ctx)

		attrs := spanAttributesMapFromHTTPContext(ctx)
		span.SetAttributes(attrs...)

		// Check if response has traceparent header and add if absent
		if ctx.Response.Header.Peek(diagUtils.TraceparentHeader) == nil {
			diagUtils.SpanContextToMetadata(span.SpanContext(), ctx.Response.Header.Set)
		}
		UpdateSpanStatusFromHTTPStatus(span,
			apitrace.SpanKindClient,
			ctx.Response.StatusCode())
		span.End()
	}
}

func startTracingClientSpanFromCarrier(supplier *propagation.FasthttpSupplier, spanName string) (context.Context, apitrace.Span) {
	ctx := context.Background()
	ctx = otel.GetTextMapPropagator().Extract(ctx, supplier)
	sc := apitrace.SpanContextFromContext(ctx)

	spanKind := apitrace.WithSpanKind(apitrace.SpanKindClient)
	ctx = apitrace.ContextWithRemoteSpanContext(ctx, sc)
	newCtx, span := defaultTracer.Start(ctx, spanName, spanKind)
	return newCtx, span
}

func StartProducerSpanChildFromParent(ctx *fasthttp.RequestCtx, sc apitrace.SpanContext) apitrace.Span {
	path := string(ctx.Request.URI().Path())
	netCtx := apitrace.ContextWithRemoteSpanContext(ctx, sc)
	kindOption := apitrace.WithSpanKind(apitrace.SpanKindProducer)
	_, span := defaultTracer.Start(netCtx, path, kindOption)
	return span
}

func isHealthzRequest(name string) bool {
	return strings.Contains(name, "/healthz")
}

// UpdateSpanStatusFromHTTPStatus updates trace span status based on response code.
func UpdateSpanStatusFromHTTPStatus(span apitrace.Span, kind apitrace.SpanKind, httpCode int) {
	if span == nil {
		return
	}

	code, desc := semconv.SpanStatusFromHTTPStatusCodeAndSpanKind(httpCode, kind)
	span.SetStatus(code, desc)
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

func spanAttributesMapFromHTTPContext(ctx *fasthttp.RequestCtx) []attribute.KeyValue {
	var isExistComponent bool
	path := string(ctx.Request.URI().Path())
	method := string(ctx.Request.Header.Method())
	statusCode := ctx.Response.StatusCode()

	m := []attribute.KeyValue{}
	_, componentType := getAPIComponent(path)

	switch componentType {
	case "state":
		isExistComponent = true
		m = append(m, isemconv.ComponentStates)
		name := getContextValue(ctx, "storeName")
		m = append(m, isemconv.ComponentNameKey.String(name))

	case "secrets":
		isExistComponent = true
		m = append(m, isemconv.ComponentSecrets)
		name := getContextValue(ctx, "secretStoreName")
		m = append(m, isemconv.ComponentNameKey.String(name))

	case "bindings":
		isExistComponent = true
		m = append(m, isemconv.ComponentBindings)
		name := getContextValue(ctx, "name")
		m = append(m, isemconv.ComponentNameKey.String(name))

	case "invoke":
		m = append(m, semconv.RPCServiceKey.String(daprRPCServiceInvocationService))
		targetID := getContextValue(ctx, "id")
		m = append(m, semconv.NetPeerNameKey.String(targetID))
		method = fmt.Sprintf("CallLocal/%s/%s", targetID, getContextValue(ctx, "method"))
		m = append(m, isemconv.APIKey.String(method))

	case "publish":
		isExistComponent = true
		m = append(m, isemconv.ComponentPubsub,
			semconv.MessagingDestinationKindTopic)
		topic := getContextValue(ctx, "topic")
		m = append(m, semconv.MessagingDestinationKey.String(topic))
	}

	// Populate the rest of database attributes.
	if isExistComponent {
		tmpMethod := fmt.Sprintf("%s %s", method, path)
		m = append(m, isemconv.ComponentMethodKey.String(tmpMethod))
	} else {
		tmpMethod := fmt.Sprintf("%s %s", method, path)
		m = append(m, isemconv.APIKey.String(tmpMethod))
	}

	// Populate dapr original api attributes.
	m = append(m, isemconv.APIProtocolHTTP,
		isemconv.APIStatusCodeKey.Int(statusCode))

	return m
}
