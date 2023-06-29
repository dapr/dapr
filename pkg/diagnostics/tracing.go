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
	"encoding/hex"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/dapr/dapr/pkg/config"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"

	// We currently don't depend on the Otel SDK since it has not GAed.
	// This package, however, only contains the conventions from the Otel Spec,
	// which we do depend on.
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
)

const (
	daprHeaderPrefix    = "dapr-"
	daprHeaderBinSuffix = "-bin"

	// daprInternalSpanAttrPrefix is the internal span attribution prefix.
	// Middleware will not populate it if the span key starts with this prefix.
	daprInternalSpanAttrPrefix = "__dapr."
	// daprAPISpanNameInternal is the internal attribution, but not populated
	// to span attribution.
	daprAPISpanNameInternal = daprInternalSpanAttrPrefix + "spanname"

	// span attribute keys
	// Reference trace semantics https://github.com/open-telemetry/opentelemetry-specification/tree/master/specification/trace/semantic_conventions
	//
	// The upstream constants may be used directly, but that would
	// proliferate the imports of go.opentelemetry.io/otel/... packages,
	// which we don't want to do widely before upstream goes GA.
	dbSystemSpanAttributeKey           = string(semconv.DBSystemKey)
	dbNameSpanAttributeKey             = string(semconv.DBNameKey)
	dbStatementSpanAttributeKey        = string(semconv.DBStatementKey)
	dbConnectionStringSpanAttributeKey = string(semconv.DBConnectionStringKey)

	messagingSystemSpanAttributeKey          = string(semconv.MessagingSystemKey)
	messagingDestinationSpanAttributeKey     = string(semconv.MessagingDestinationKey)
	messagingDestinationKindSpanAttributeKey = string(semconv.MessagingDestinationKindKey)

	gRPCServiceSpanAttributeKey = string(semconv.RPCServiceKey)
	netPeerNameSpanAttributeKey = string(semconv.NetPeerNameKey)

	daprAPISpanAttributeKey           = "dapr.api"
	daprAPIStatusCodeSpanAttributeKey = "dapr.status_code"
	daprAPIProtocolSpanAttributeKey   = "dapr.protocol"
	daprAPIInvokeMethod               = "dapr.invoke_method"
	daprAPIActorTypeID                = "dapr.actor"

	daprAPIHTTPSpanAttrValue = "http"
	daprAPIGRPCSpanAttrValue = "grpc"

	stateBuildingBlockType   = "state"
	secretBuildingBlockType  = "secrets"
	bindingBuildingBlockType = "bindings"
	pubsubBuildingBlockType  = "pubsub"

	daprGRPCServiceInvocationService = "ServiceInvocation"
	daprGRPCDaprService              = "Dapr"

	tracerName = "dapr-diagnostics"
)

var tracer trace.Tracer = otel.Tracer(tracerName)

// Effectively const, but isn't a const from upstream.
var messagingDestinationTopicKind = semconv.MessagingDestinationKindTopic.Value.AsString()

// SpanContextToW3CString returns the SpanContext string representation.
func SpanContextToW3CString(sc trace.SpanContext) string {
	traceID := sc.TraceID()
	spanID := sc.SpanID()
	traceFlags := sc.TraceFlags()
	return fmt.Sprintf("%x-%x-%x-%x",
		[]byte{supportedVersion},
		traceID[:],
		spanID[:],
		[]byte{byte(traceFlags)})
}

// TraceStateToW3CString extracts the TraceState from given SpanContext and returns its string representation.
func TraceStateToW3CString(sc trace.SpanContext) string {
	return sc.TraceState().String()
}

// SpanContextFromW3CString extracts a span context from given string which got earlier from SpanContextToW3CString format.
func SpanContextFromW3CString(h string) (sc trace.SpanContext, ok bool) {
	if h == "" {
		return trace.SpanContext{}, false
	}
	sections := strings.Split(h, "-")
	if len(sections) < 4 {
		return trace.SpanContext{}, false
	}

	if len(sections[0]) != 2 {
		return trace.SpanContext{}, false
	}
	ver, err := hex.DecodeString(sections[0])
	if err != nil {
		return trace.SpanContext{}, false
	}
	version := int(ver[0])
	if version > maxVersion {
		return trace.SpanContext{}, false
	}

	if version == 0 && len(sections) != 4 {
		return trace.SpanContext{}, false
	}

	if len(sections[1]) != 32 {
		return trace.SpanContext{}, false
	}
	tid, err := trace.TraceIDFromHex(sections[1])
	if err != nil {
		return trace.SpanContext{}, false
	}
	sc = sc.WithTraceID(tid)

	if len(sections[2]) != 16 {
		return trace.SpanContext{}, false
	}
	sid, err := trace.SpanIDFromHex(sections[2])
	if err != nil {
		return trace.SpanContext{}, false
	}
	sc = sc.WithSpanID(sid)

	opts, err := hex.DecodeString(sections[3])
	if err != nil || len(opts) < 1 {
		return trace.SpanContext{}, false
	}
	sc = sc.WithTraceFlags(trace.TraceFlags(opts[0]))

	// Don't allow all zero trace or span ID.
	if sc.TraceID() == [16]byte{} || sc.SpanID() == [8]byte{} {
		return trace.SpanContext{}, false
	}

	return sc, true
}

// TraceStateFromW3CString extracts a span tracestate from given string which got earlier from TraceStateFromW3CString format.
func TraceStateFromW3CString(h string) *trace.TraceState {
	if h == "" {
		ts := trace.TraceState{}
		return &ts
	}

	ts, err := trace.ParseTraceState(h)
	if err != nil {
		ts = trace.TraceState{}
		return &ts
	}

	return &ts
}

// AddAttributesToSpan adds the given attributes in the span.
func AddAttributesToSpan(span trace.Span, attributes map[string]string) {
	if span == nil {
		return
	}
	var attrs []attribute.KeyValue
	for k, v := range attributes {
		// Skip if key is for internal use.
		if !strings.HasPrefix(k, daprInternalSpanAttrPrefix) && v != "" {
			attrs = append(attrs, attribute.String(k, v))
		}
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// ConstructInputBindingSpanAttributes creates span attributes for InputBindings.
func ConstructInputBindingSpanAttributes(bindingName, url string) map[string]string {
	return map[string]string{
		dbNameSpanAttributeKey:             bindingName,
		gRPCServiceSpanAttributeKey:        daprGRPCDaprService,
		dbSystemSpanAttributeKey:           bindingBuildingBlockType,
		dbConnectionStringSpanAttributeKey: url,
	}
}

// ConstructSubscriptionSpanAttributes creates span attributes for Pubsub subscription.
func ConstructSubscriptionSpanAttributes(topic string) map[string]string {
	return map[string]string{
		messagingSystemSpanAttributeKey:          pubsubBuildingBlockType,
		messagingDestinationSpanAttributeKey:     topic,
		messagingDestinationKindSpanAttributeKey: messagingDestinationTopicKind,
	}
}

// StartInternalCallbackSpan starts trace span for internal callback such as input bindings and pubsub subscription.
func StartInternalCallbackSpan(ctx context.Context, spanName string, parent trace.SpanContext, spec *config.TracingSpec) (context.Context, trace.Span) {
	if spec == nil || !diagUtils.IsTracingEnabled(spec.SamplingRate) {
		return ctx, nil
	}

	ctx = trace.ContextWithRemoteSpanContext(ctx, parent)
	ctx, span := tracer.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindClient))

	return ctx, span
}
