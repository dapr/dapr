// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"strings"

	"github.com/dapr/dapr/pkg/config"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const grpcTraceContextKey = "grpc-trace-bin"

// SetTracingInGRPCMiddlewareUnary sets the trace context or starts the trace client span based on request
func SetTracingInGRPCMiddlewareUnary(appID string, spec config.TracingSpec) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		sc := GetSpanContextFromGRPC(ctx, spec)
		newCtx := NewContext(ctx, sc)

		var err error
		var resp interface{}

		// 1. check if tracing is enabled or not
		// 2. if tracing is disabled, set the trace context and call the handler
		// 3. if tracing is enabled, start the client or server spans based on the request and call the handler with appropriate span context
		if !diag_utils.IsTracingEnabled(spec.SamplingRate) {
			resp, err = handler(newCtx, req)
			return resp, err
		}

		var span *trace.Span
		spanName := info.FullMethod

		if isInternalCalls(info.FullMethod) {
			_, span = StartTracingServerSpanFromGRPCContext(newCtx, spanName, spec)
		} else {
			// Instead of generating the span in direct_messaging, dapr changes the spanname
			// to CallLocal.
			if spanName == "/dapr.proto.runtime.v1.Dapr/InvokeService" {
				spanName = daprServiceInvocationFullMethod
			}
			_, span = StartTracingClientSpanFromGRPCContext(newCtx, spanName, spec)
		}

		// build new context now on top of passed root context with started span context
		newCtx = NewContext(ctx, span.SpanContext())
		resp, err = handler(newCtx, req)

		// add span attributes
		if span.SpanContext().TraceOptions.IsSampled() {
			m := GetSpanAttributesMapFromGRPC(req, info.FullMethod)
			AddAttributesToSpan(span, m)
		}

		UpdateSpanStatusFromGRPCError(span, err)

		span.End()

		return resp, err
	}
}

// UpdateSpanStatusFromGRPCError updates tracer span status based on error object
func UpdateSpanStatusFromGRPCError(span *trace.Span, err error) {
	if span == nil || err == nil {
		return
	}

	s, ok := status.FromError(err)
	if ok {
		span.SetStatus(trace.Status{Code: int32(s.Code()), Message: s.Message()})
	} else {
		span.SetStatus(trace.Status{Code: int32(codes.Internal), Message: err.Error()})
	}
}

// StartTracingServerSpanFromGRPCContext creates a span on receiving an incoming gRPC method call from remote client
func StartTracingServerSpanFromGRPCContext(ctx context.Context, method string, spec config.TracingSpec) (context.Context, *trace.Span) {
	var span *trace.Span
	ctx, span = startTracingSpanInternal(ctx, method, spec.SamplingRate, trace.SpanKindServer)
	addAnnotationsToSpanFromGRPCMetadata(ctx, span)

	return ctx, span
}

// StartTracingClientSpanFromGRPCContext creates a client span before invoking gRPC method call
func StartTracingClientSpanFromGRPCContext(ctx context.Context, method string, spec config.TracingSpec) (context.Context, *trace.Span) {
	var span *trace.Span
	ctx, span = startTracingSpanInternal(ctx, method, spec.SamplingRate, trace.SpanKindClient)
	addAnnotationsToSpanFromGRPCMetadata(ctx, span)

	return ctx, span
}

func GetSpanContextFromGRPC(ctx context.Context, spec config.TracingSpec) trace.SpanContext {
	spanContext, ok := FromGRPCContext(ctx)

	if !ok {
		spanContext = GetDefaultSpanContext(spec)
	}

	return spanContext
}

// UpdateResponseTraceHeadersGRPC updates Dapr generated trace headers in the response if no trace headers found in the context
func UpdateResponseTraceHeadersGRPC(ctx context.Context, sc trace.SpanContext) {
	spanContext, ok := FromGRPCContext(ctx)
	if !ok {
		spanContext = sc
	}
	AppendToOutgoingGRPCContext(ctx, spanContext)
}

// FromGRPCContext returns the SpanContext stored in a context, or empty if there isn't one.
func FromGRPCContext(ctx context.Context) (trace.SpanContext, bool) {
	var sc trace.SpanContext
	var ok bool
	md, _ := metadata.FromIncomingContext(ctx)
	traceContext := md[grpcTraceContextKey]
	if len(traceContext) > 0 {
		traceContextBinary := []byte(traceContext[0])
		sc, ok = propagation.FromBinary(traceContextBinary)
	}
	return sc, ok
}

// AppendToOutgoingGRPCContext appends binary serialized SpanContext to the outgoing GRPC context
func AppendToOutgoingGRPCContext(ctx context.Context, spanContext trace.SpanContext) context.Context {
	traceContextBinary := propagation.Binary(spanContext)
	return metadata.AppendToOutgoingContext(ctx, grpcTraceContextKey, string(traceContextBinary))
}

// FromOutgoingGRPCContext returns the SpanContext stored in a context, or empty if there isn't one.
func FromOutgoingGRPCContext(ctx context.Context) (trace.SpanContext, bool) {
	var sc trace.SpanContext
	var ok bool
	md, _ := metadata.FromOutgoingContext(ctx)
	traceContext := md[grpcTraceContextKey]
	if len(traceContext) > 0 {
		traceContextBinary := []byte(traceContext[0])
		sc, ok = propagation.FromBinary(traceContextBinary)
	}
	return sc, ok
}

func addAnnotationsToSpanFromGRPCMetadata(ctx context.Context, span *trace.Span) {
	md := extractDaprMetadata(ctx)

	// md metadata must only have dapr prefixed headers metadata
	// still extra check for dapr headers to avoid, it might be micro performance hit but that is ok
	for k, vv := range md {
		if !strings.HasPrefix(strings.ToLower(k), daprHeaderPrefix) {
			continue
		}

		for _, v := range vv {
			span.AddAttributes(trace.StringAttribute(k, v))
		}
	}
}

func isInternalCalls(method string) bool {
	return strings.HasPrefix(method, "/dapr.proto.internals.")
}

// GetSpanAttributesMapFromGRPC builds the span trace attributes map for gRPC calls based on given parameters as per open-telemetry specs
func GetSpanAttributesMapFromGRPC(req interface{}, rpcMethod string) map[string]string {
	// RPC Span Attribute reference https://github.com/open-telemetry/opentelemetry-specification/blob/master/specification/trace/semantic_conventions/rpc.md
	// gRPC method /package.service/method
	m := make(map[string]string)

	var dbType string
	switch s := req.(type) {
	// Internal service invocation request
	case *internalv1pb.InternalInvokeRequest:
		m[gRPCServiceSpanAttributeKey] = daprGRPCServiceInvocationService
		m[daprAPIInvokeMethod] = s.Message.GetMethod()
		// TODO: actor support

	case *runtimev1pb.InvokeServiceRequest:
		m[gRPCServiceSpanAttributeKey] = daprGRPCServiceInvocationService
		m[netPeerNameSpanAttributeKey] = s.GetId()

	case *runtimev1pb.PublishEventRequest:
		m[gRPCServiceSpanAttributeKey] = daprGRPCDaprService
		m[messagingSystemSpanAttributeKey] = pubsubBuildingBlockType
		m[messagingDestinationSpanAttributeKey] = s.GetTopic()
		m[messagingDestinationKindSpanAttributeKey] = messagingDestinationTopicKind

	case *runtimev1pb.InvokeBindingRequest:
		dbType = bindingBuildingBlockType
		m[dbInstanceSpanAttributeKey] = s.GetName()

	case *runtimev1pb.GetStateRequest:
		dbType = stateBuildingBlockType
		m[dbInstanceSpanAttributeKey] = s.GetStoreName()

	case *runtimev1pb.SaveStateRequest:
		dbType = stateBuildingBlockType
		m[dbInstanceSpanAttributeKey] = s.GetStoreName()

	case *runtimev1pb.DeleteStateRequest:
		dbType = stateBuildingBlockType
		m[dbInstanceSpanAttributeKey] = s.GetStoreName()

	case *runtimev1pb.GetSecretRequest:
		dbType = secretBuildingBlockType
		m[dbInstanceSpanAttributeKey] = s.GetStoreName()
	}

	if _, ok := m[dbInstanceSpanAttributeKey]; ok {
		m[gRPCServiceSpanAttributeKey] = daprGRPCDaprService
		m[dbTypeSpanAttributeKey] = dbType
		m[dbStatementSpanAttributeKey] = rpcMethod
		m[dbURLSpanAttributeKey] = dbType
	}

	m[daprAPIProtocolSpanAttributeKey] = daprAPIGRPCSpanAttrValue
	m[daprAPISpanAttributeKey] = rpcMethod

	return m
}
