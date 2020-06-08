// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/dapr/dapr/pkg/config"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/tracestate"
)

const (
	daprHeaderPrefix    = "dapr-"
	daprHeaderBinSuffix = "-bin"

	// daprInternalSpanAttrPrefix is the internal span attribution prefix.
	// Middleware will not populate it if the span key starts with this prefix.
	daprInternalSpanAttrPrefix = "__dapr."
	// daprAPISpanNameInternal is the internal attributation, but not populated
	// to span attribution.
	daprAPISpanNameInternal = daprInternalSpanAttrPrefix + "spanname"

	// span attribute keys
	// Reference trace semantics https://github.com/open-telemetry/opentelemetry-specification/tree/master/specification/trace/semantic_conventions
	dbTypeSpanAttributeKey      = "db.type"
	dbInstanceSpanAttributeKey  = "db.instance"
	dbStatementSpanAttributeKey = "db.statement"
	dbURLSpanAttributeKey       = "db.url"

	messagingDestinationTopicKind            = "topic"
	messagingSystemSpanAttributeKey          = "messaging.system"
	messagingDestinationSpanAttributeKey     = "messaging.destination"
	messagingDestinationKindSpanAttributeKey = "messaging.destination_kind"

	gRPCServiceSpanAttributeKey = "rpc.service"
	netPeerNameSpanAttributeKey = "net.peer.name"

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
)

// SpanContextToW3CString returns the SpanContext string representation
func SpanContextToW3CString(sc trace.SpanContext) string {
	return fmt.Sprintf("%x-%x-%x-%x",
		[]byte{supportedVersion},
		sc.TraceID[:],
		sc.SpanID[:],
		[]byte{byte(sc.TraceOptions)})
}

// SpanContextFromW3CString extracts a span context from given string which got earlier from SpanContextToW3CString format
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
	tid, err := hex.DecodeString(sections[1])
	if err != nil {
		return trace.SpanContext{}, false
	}
	copy(sc.TraceID[:], tid)

	if len(sections[2]) != 16 {
		return trace.SpanContext{}, false
	}
	sid, err := hex.DecodeString(sections[2])
	if err != nil {
		return trace.SpanContext{}, false
	}
	copy(sc.SpanID[:], sid)

	opts, err := hex.DecodeString(sections[3])
	if err != nil || len(opts) < 1 {
		return trace.SpanContext{}, false
	}
	sc.TraceOptions = trace.TraceOptions(opts[0])

	// Don't allow all zero trace or span ID.
	if sc.TraceID == [16]byte{} || sc.SpanID == [8]byte{} {
		return trace.SpanContext{}, false
	}

	return sc, true
}

// TraceStateFromW3CString extracts a span tracestate from given string which got earlier from TraceStateFromW3CString format
func TraceStateFromW3CString(h string) *tracestate.Tracestate {
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

// AddAttributesToSpan adds the given attributes in the span
func AddAttributesToSpan(span *trace.Span, attributes map[string]string) {
	if span == nil {
		return
	}

	for k, v := range attributes {
		// Skip if key is for internal use.
		if !strings.HasPrefix(k, daprInternalSpanAttrPrefix) && v != "" {
			span.AddAttributes(trace.StringAttribute(k, v))
		}
	}
}

// ConstructInputBindingSpanAttributes creates span attributes for InputBindings.
func ConstructInputBindingSpanAttributes(bindingName, url string) map[string]string {
	return map[string]string{
		dbInstanceSpanAttributeKey:  bindingName,
		gRPCServiceSpanAttributeKey: daprGRPCDaprService,
		dbTypeSpanAttributeKey:      bindingBuildingBlockType,
		dbURLSpanAttributeKey:       url,
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
func StartInternalCallbackSpan(spanName string, parent trace.SpanContext, spec config.TracingSpec) (context.Context, *trace.Span) {
	traceEnabled := diag_utils.IsTracingEnabled(spec.SamplingRate)
	ctx := context.Background()
	if !traceEnabled {
		return ctx, nil
	}

	sampler := diag_utils.TraceSampler(spec.SamplingRate)
	return trace.StartSpanWithRemoteParent(ctx, spanName, parent, sampler, trace.WithSpanKind(trace.SpanKindServer))
}
