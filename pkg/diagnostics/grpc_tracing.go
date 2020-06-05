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
		var span *trace.Span
		spanName := info.FullMethod

		sc, _ := SpanContextFromGRPCMetadata(ctx)
		sampler := diag_utils.TraceSampler(spec.SamplingRate)

		var spanKind trace.StartOption

		// This middleware is shared by internal gRPC for service invocation and api
		// so that it needs to handle separately.
		if isInternalCalls(info.FullMethod) {
			// For dapr.proto.internals package, this generates ServerSpan.
			spanKind = trace.WithSpanKind(trace.SpanKindServer)
		} else {
			// For dapr.proto.runtime package, this generates ClientSpan because each Dapr service API
			// is treat as dependency or internal calls.
			if strings.HasSuffix(spanName, "Dapr/InvokeService") {
				// Instead of generating the span in direct_messaging, dapr changes the spanname
				// to CallLocal.
				spanName = daprServiceInvocationFullMethod
			}
			spanKind = trace.WithSpanKind(trace.SpanKindClient)
		}

		ctx, span = trace.StartSpanWithRemoteParent(ctx, spanName, sc, sampler, spanKind)

		var prefixedMetadata map[string]string
		if span.SpanContext().TraceOptions.IsSampled() {
			// users can add dapr- prefix if they want to see the header values in span attributes.
			prefixedMetadata = userDefinedMetadata(ctx)
		}

		resp, err := handler(ctx, req)

		if span.SpanContext().TraceOptions.IsSampled() {
			// Populates dapr- prefixed header first
			AddAttributesToSpan(span, prefixedMetadata)
			AddAttributesToSpan(span, spanAttributesMapFromGRPC(req, info.FullMethod))
		}

		UpdateSpanStatusFromGRPCError(span, err)
		span.End()

		return resp, err
	}
}

// userDefinedMetadata returns dapr- prefixed header from incoming metdata.
// Users can add dapr- prefixed headers that they want to see in span attributes.
func userDefinedMetadata(ctx context.Context) map[string]string {
	var daprMetadata = map[string]string{}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return daprMetadata
	}

	for k, v := range md {
		k = strings.ToLower(k)
		if strings.HasPrefix(k, daprHeaderPrefix) && !strings.HasSuffix(k, daprHeaderBinSuffix) {
			daprMetadata[k] = v[0]
		}
	}

	return daprMetadata
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

// SpanContextFromGRPCMetadata returns the SpanContext stored in a context, or empty if there isn't one.
func SpanContextFromGRPCMetadata(ctx context.Context) (trace.SpanContext, bool) {
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

// SpanContextToGRPCMetadata appends binary serialized SpanContext to the outgoing GRPC context
func SpanContextToGRPCMetadata(ctx context.Context, spanContext trace.SpanContext) context.Context {
	// if span context is empty, no ops
	if (trace.SpanContext{}) == spanContext {
		return ctx
	}
	traceContextBinary := propagation.Binary(spanContext)
	return metadata.AppendToOutgoingContext(ctx, grpcTraceContextKey, string(traceContextBinary))
}

func isInternalCalls(method string) bool {
	return strings.HasPrefix(method, "/dapr.proto.internals.")
}

// spanAttributesMapFromGRPC builds the span trace attributes map for gRPC calls based on given parameters as per open-telemetry specs
func spanAttributesMapFromGRPC(req interface{}, rpcMethod string) map[string]string {
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
