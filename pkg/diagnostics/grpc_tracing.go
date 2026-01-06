/*
Copyright 2023 The Dapr Authors
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

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	otelBaggage "go.opentelemetry.io/otel/baggage"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcMetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/api/grpc/metadata"
	"github.com/dapr/dapr/pkg/config"
	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

const (
	daprInternalPrefix        = "/dapr.proto.internals."
	daprRuntimePrefix         = "/dapr.proto.runtime."
	daprInvokeServiceMethod   = "/dapr.proto.runtime.v1.Dapr/InvokeService"
	daprCallLocalStreamMethod = "/dapr.proto.internals.v1.ServiceInvocation/CallLocalStream"
	daprWorkflowPrefix        = "/TaskHubSidecarService"
)

// handleBaggage extracts baggage from the incoming metadata and forwards that along as metadata,
// and checks for context baggage via otel context checking and propagates that along in the ctx.
// There are 2 separate streams in which baggage can come in and flow out and each is respected
// and NOT combined for security reasons.
func handleBaggage(ctx context.Context) (context.Context, string, error) {
	// metadata baggage (do not inject into context)
	md, ok := grpcMetadata.FromIncomingContext(ctx)
	if !ok {
		md = make(grpcMetadata.MD)
	}

	var validBaggage string
	if baggageValues := md.Get(diagConsts.BaggageHeader); len(baggageValues) > 0 {
		metadataBaggageHeader := strings.Join(baggageValues, ",")
		var baggage otelBaggage.Baggage
		var err error
		if baggage, err = otelBaggage.Parse(metadataBaggageHeader); err != nil {
			return ctx, "", status.Error(codes.InvalidArgument, fmt.Sprintf("invalid baggage header: %v", err))
		}
		validBaggage = baggage.String()
	}

	// context baggage (do not inject into metadata)
	contextBaggage := otelBaggage.FromContext(ctx)
	if len(contextBaggage.Members()) > 0 {
		if _, err := otelBaggage.Parse(contextBaggage.String()); err != nil {
			return ctx, "", status.Error(codes.InvalidArgument, fmt.Sprintf("invalid context baggage: %v", err))
		}
	}

	return grpcMetadata.NewIncomingContext(ctx, md), validBaggage, nil
}

// GRPCTraceUnaryServerInterceptor sets the trace context or starts the trace client span based on request.
func GRPCTraceUnaryServerInterceptor(appID string, spec config.TracingSpec) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		var (
			span             trace.Span
			spanKind         trace.SpanStartOption
			prefixedMetadata map[string]string
			reqSpanAttr      map[string]string
		)

		// Handle incoming baggage
		ctx, validBaggage, err := handleBaggage(ctx)
		if err != nil {
			return nil, err
		}

		if validBaggage != "" {
			grpc.SetHeader(ctx, grpcMetadata.Pairs(diagConsts.BaggageHeader, validBaggage))
		}

		sc, _ := SpanContextFromIncomingGRPCMetadata(ctx)
		// This middleware is shared by internal gRPC for service invocation and API
		// so that it needs to handle separately.
		if strings.HasPrefix(info.FullMethod, daprInternalPrefix) {
			// For the dapr.proto.internals package, this generates ServerSpan.
			// This is invoked by other Dapr runtimes during service invocation.
			spanKind = trace.WithSpanKind(trace.SpanKindServer)
		} else {
			// For the dapr.proto.runtime package, this generates ClientSpan.
			// This is invoked by clients (apps) while invoking Dapr APIs.
			spanKind = trace.WithSpanKind(trace.SpanKindClient)
		}

		ctx = trace.ContextWithRemoteSpanContext(ctx, sc)
		ctx, span = tracer.Start(ctx, info.FullMethod, spanKind)

		resp, err := handler(ctx, req)

		if span.SpanContext().IsSampled() {
			// users can add dapr- prefix if they want to see the header values in span attributes.
			prefixedMetadata = userDefinedMetadata(ctx)
			reqSpanAttr = spanAttributesMapFromGRPC(appID, req, info.FullMethod)

			// Populates dapr- prefixed header first
			for key, value := range reqSpanAttr {
				prefixedMetadata[key] = value
			}
			AddAttributesToSpan(span, prefixedMetadata)

			// Correct the span name based on API.
			if sname, ok := reqSpanAttr[diagConsts.DaprAPISpanNameInternal]; ok {
				span.SetName(sname)
			}
		}

		// Add grpc-trace-bin header for all non-invocation api's
		if info.FullMethod != daprInvokeServiceMethod {
			traceContextBinary := diagUtils.BinaryFromSpanContext(span.SpanContext())
			grpc.SetHeader(ctx, grpcMetadata.Pairs(diagConsts.GRPCTraceContextKey, string(traceContextBinary)))
		}

		UpdateSpanStatusFromGRPCError(span, err)
		span.End()

		return resp, err
	}
}

// GRPCTraceStreamServerInterceptor sets the trace context or starts the trace client span based on request.
// This is used by proxy requests too.
func GRPCTraceStreamServerInterceptor(appID string, spec config.TracingSpec) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		var (
			span      trace.Span
			spanKind  trace.SpanStartOption
			isProxied bool
		)

		ctx := ss.Context()

		// Handle incoming baggage
		ctx, validBaggage, err := handleBaggage(ctx)
		if err != nil {
			return err
		}

		if validBaggage != "" {
			ss.SetHeader(grpcMetadata.Pairs(diagConsts.BaggageHeader, validBaggage))
		}

		// This middleware is shared by multiple services and proxied requests, which need to be handled separately
		switch {
		// For gRPC service invocation, this generates ServerSpan
		case strings.HasPrefix(info.FullMethod, daprInternalPrefix):
			spanKind = trace.WithSpanKind(trace.SpanKindServer)

		// For gRPC API, this generates ClientSpan
		case strings.HasPrefix(info.FullMethod, daprRuntimePrefix):
			spanKind = trace.WithSpanKind(trace.SpanKindClient)

		// For Dapr Workflow APIs, this generates ServerSpan
		case strings.HasPrefix(info.FullMethod, daprWorkflowPrefix):
			spanKind = trace.WithSpanKind(trace.SpanKindServer)

		// For proxied requests, this generates a span depending on whether this is the server (target) or client
		default:
			isProxied = true
			md, _ := metadata.FromIncomingContext(ctx)
			vals := md.Get(diagConsts.GRPCProxyCalleeIDKey)
			if len(vals) == 0 {
				log.Debugf("cannot proxy request: missing %s metadata, fallback to %s", diagConsts.GRPCProxyCalleeIDKey, diagConsts.GRPCProxyAppIDKey)
				vals = md.Get(diagConsts.GRPCProxyAppIDKey)
				if len(vals) == 0 {
					return fmt.Errorf("cannot proxy request: missing %s or %s metadata", diagConsts.GRPCProxyCalleeIDKey, diagConsts.GRPCProxyAppIDKey)
				}
			}
			// vals[0] is the target app ID
			if appID == vals[0] {
				spanKind = trace.WithSpanKind(trace.SpanKindServer)
			} else {
				spanKind = trace.WithSpanKind(trace.SpanKindClient)
			}
		}

		// Overwrite context
		sc, _ := SpanContextFromIncomingGRPCMetadata(ctx)
		ctx = trace.ContextWithRemoteSpanContext(ctx, sc)
		ctx, span = tracer.Start(ctx, info.FullMethod, spanKind)
		wrapped := grpcMiddleware.WrapServerStream(ss)
		wrapped.WrappedContext = ctx

		err = handler(srv, wrapped)

		if span.SpanContext().IsSampled() {
			var (
				prefixedMetadata map[string]string
				reqSpanAttr      map[string]string
			)

			// users can add dapr- prefix if they want to see the header values in span attributes.
			prefixedMetadata = userDefinedMetadata(ctx)
			if isProxied {
				reqSpanAttr = map[string]string{
					diagConsts.DaprAPISpanNameInternal: info.FullMethod,
				}
			} else {
				reqSpanAttr = spanAttributesMapFromGRPC(appID, ss.Context(), info.FullMethod)
			}

			// Populates dapr- prefixed header first
			for key, value := range reqSpanAttr {
				prefixedMetadata[key] = value
			}
			AddAttributesToSpan(span, prefixedMetadata)

			// Correct the span name based on API.
			if sname, ok := reqSpanAttr[diagConsts.DaprAPISpanNameInternal]; ok {
				span.SetName(sname)
			}
		}

		// Add grpc-trace-bin header for all non-invocation api's
		if !isProxied && info.FullMethod != daprInvokeServiceMethod {
			traceContextBinary := diagUtils.BinaryFromSpanContext(span.SpanContext())
			grpc.SetHeader(ctx, grpcMetadata.Pairs(diagConsts.GRPCTraceContextKey, string(traceContextBinary)))
		}

		UpdateSpanStatusFromGRPCError(span, err)
		span.End()

		return err
	}
}

// userDefinedMetadata returns dapr- prefixed header from incoming metadata.
// Users can add dapr- prefixed headers that they want to see in span attributes.
func userDefinedMetadata(ctx context.Context) map[string]string {
	md, ok := metadata.FromIncomingContext(ctx)
	daprMetadata := make(map[string]string, len(md))
	if !ok {
		return daprMetadata
	}

	for k, v := range md {
		if strings.HasPrefix(k, daprHeaderPrefix) && !strings.HasSuffix(k, daprHeaderBinSuffix) {
			daprMetadata[k] = v[0]
		}
	}

	return daprMetadata
}

func StartGRPCProducerSpanChildFromParent(ct context.Context, parentSpan trace.Span, spanName string) (context.Context, trace.Span) {
	netCtx := trace.ContextWithRemoteSpanContext(ct, parentSpan.SpanContext())
	spanKind := trace.WithSpanKind(trace.SpanKindProducer)

	//nolint:spancheck
	ctx, span := tracer.Start(netCtx, spanName, spanKind)

	//nolint:spancheck
	return ctx, span
}

// UpdateSpanStatusFromGRPCError updates tracer span status based on error object.
func UpdateSpanStatusFromGRPCError(span trace.Span, err error) {
	if span == nil || err == nil {
		return
	}

	if e, ok := status.FromError(err); ok {
		span.SetStatus(otelcodes.Error, e.Message())
	} else {
		span.SetStatus(otelcodes.Error, err.Error())
	}
}

// SpanContextFromIncomingGRPCMetadata returns the SpanContext stored in incoming metadata of context, or empty if there isn't one.
func SpanContextFromIncomingGRPCMetadata(ctx context.Context) (trace.SpanContext, bool) {
	var (
		sc trace.SpanContext
		md metadata.MD
		ok bool
	)
	if md, ok = metadata.FromIncomingContext(ctx); !ok {
		return sc, false
	}
	traceContext := md[diagConsts.GRPCTraceContextKey]
	if len(traceContext) > 0 {
		sc, ok = diagUtils.SpanContextFromBinary([]byte(traceContext[0]))
	} else {
		// add workaround to fallback on checking traceparent header
		// as grpc-trace-bin is not yet there in OpenTelemetry unlike OpenCensus , tracking issue https://github.com/open-telemetry/opentelemetry-specification/issues/639
		// and grpc-dotnet client adheres to OpenTelemetry Spec which only supports http based traceparent header in gRPC path
		// TODO : Remove this workaround fix once grpc-dotnet supports grpc-trace-bin header. Tracking issue https://github.com/dapr/dapr/issues/1827
		traceContext = md[diagConsts.TraceparentHeader]
		if len(traceContext) > 0 {
			sc, ok = SpanContextFromW3CString(traceContext[0])
			if ok && len(md[diagConsts.TracestateHeader]) > 0 {
				ts := TraceStateFromW3CString(md[diagConsts.TracestateHeader][0])
				sc.WithTraceState(*ts)
			}
		}
	}
	return sc, ok
}

// SpanContextToGRPCMetadata appends binary serialized SpanContext to the outgoing GRPC context.
func SpanContextToGRPCMetadata(ctx context.Context, spanContext trace.SpanContext) context.Context {
	traceContextBinary := diagUtils.BinaryFromSpanContext(spanContext)
	if len(traceContextBinary) == 0 {
		return ctx
	}

	traceparent := SpanContextToW3CString(spanContext)
	ctx = grpcMetadata.AppendToOutgoingContext(ctx, contribpubsub.TraceParentField, traceparent)
	return grpcMetadata.AppendToOutgoingContext(ctx, diagConsts.GRPCTraceContextKey, string(traceContextBinary))
}

// spanAttributesMapFromGRPC builds the span trace attributes map for gRPC calls based on given parameters as per open-telemetry specs.
// RPC Span Attribute reference https://github.com/open-telemetry/opentelemetry-specification/blob/master/specification/trace/semantic_conventions/rpc.md .
func spanAttributesMapFromGRPC(appID string, req any, rpcMethod string) map[string]string {
	// Allocating this map with an initial capacity of 8 which seems to be the "worst case" scenario due to possible unique keys below (note this is an initial capacity and not a hard limit).
	// Using an explicit capacity reduces the risk the map will need to be re-allocated multiple times.
	m := make(map[string]string, 8)

	switch s := req.(type) {
	// Context from a server stream
	// This is a special case that is used for streaming requests
	case context.Context:
		md, ok := metadata.FromIncomingContext(s)
		if !ok {
			break
		}
		switch rpcMethod {
		// Internal service invocation request (with streaming)
		case daprCallLocalStreamMethod:
			m[diagConsts.GrpcServiceSpanAttributeKey] = diagConsts.DaprGRPCServiceInvocationService
			var method string
			if len(md[diagConsts.DaprCallLocalStreamMethodKey]) > 0 {
				method = md[diagConsts.DaprCallLocalStreamMethodKey][0]
			}
			m[diagConsts.DaprAPISpanNameInternal] = "CallLocal/" + appID + "/" + method
			m[diagConsts.DaprAPIInvokeMethod] = method
		}

	// Internal service invocation request
	case *internalv1pb.InternalInvokeRequest:
		m[diagConsts.GrpcServiceSpanAttributeKey] = diagConsts.DaprGRPCServiceInvocationService

		// Rename spanname
		if s.GetActor() == nil {
			m[diagConsts.DaprAPISpanNameInternal] = "CallLocal/" + appID + "/" + s.GetMessage().GetMethod()
			m[diagConsts.DaprAPIInvokeMethod] = s.GetMessage().GetMethod()
		} else {
			m[diagConsts.DaprAPISpanNameInternal] = "CallActor/" + s.GetActor().GetActorType() + "/" + s.GetMessage().GetMethod()
			m[diagConsts.DaprAPIActorTypeID] = s.GetActor().GetActorType() + "." + s.GetActor().GetActorId()
		}

	// Dapr APIs
	case diagConsts.GrpcAppendSpanAttributesFn:
		s.AppendSpanAttributes(rpcMethod, m)
	}

	m[diagConsts.DaprAPIProtocolSpanAttributeKey] = diagConsts.DaprAPIGRPCSpanAttrValue
	m[diagConsts.DaprAPISpanAttributeKey] = rpcMethod

	return m
}
