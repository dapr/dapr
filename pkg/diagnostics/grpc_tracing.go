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

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/pkg/errors"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/config"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

const (
	GRPCTraceContextKey     = "grpc-trace-bin"
	GRPCProxyAppIDKey       = "dapr-app-id"
	daprInternalPrefix      = "/dapr.proto.internals."
	daprRuntimePrefix       = "/dapr.proto.runtime."
	daprInvokeServiceMethod = "/dapr.proto.runtime.v1.Dapr/InvokeService"
)

// GRPCTraceUnaryServerInterceptor sets the trace context or starts the trace client span based on request.
func GRPCTraceUnaryServerInterceptor(appID string, spec config.TracingSpec) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		var (
			span             trace.Span
			spanKind         trace.SpanStartOption
			prefixedMetadata map[string]string
			reqSpanAttr      map[string]string
		)
		sc, _ := SpanContextFromIncomingGRPCMetadata(ctx)
		// This middleware is shared by internal gRPC for service invocation and API
		// so that it needs to handle separately.
		if strings.HasPrefix(info.FullMethod, daprInternalPrefix) {
			// For gRPC server invocation, this generates ServerSpan.
			spanKind = trace.WithSpanKind(trace.SpanKindServer)
		} else {
			// For gRPC API, this generates ClientSpan.
			spanKind = trace.WithSpanKind(trace.SpanKindClient)
		}

		ctx = trace.ContextWithRemoteSpanContext(ctx, sc)
		ctx, span = tracer.Start(ctx, info.FullMethod, spanKind)

		isSampled := span.SpanContext().IsSampled()
		if isSampled {
			// users can add dapr- prefix if they want to see the header values in span attributes.
			prefixedMetadata = userDefinedMetadata(ctx)
			reqSpanAttr = spanAttributesMapFromGRPC(appID, req, info.FullMethod)
		}

		resp, err := handler(ctx, req)

		if isSampled {
			// Populates dapr- prefixed header first
			for key, value := range reqSpanAttr {
				prefixedMetadata[key] = value
			}
			AddAttributesToSpan(span, prefixedMetadata)

			// Correct the span name based on API.
			if sname, ok := reqSpanAttr[daprAPISpanNameInternal]; ok {
				span.SetName(sname)
			}
		}

		// Add grpc-trace-bin header for all non-invocation api's
		if info.FullMethod != daprInvokeServiceMethod {
			traceContextBinary := diagUtils.BinaryFromSpanContext(span.SpanContext())
			grpc.SetHeader(ctx, metadata.Pairs(GRPCTraceContextKey, string(traceContextBinary)))
		}

		UpdateSpanStatusFromGRPCError(span, err)
		span.End()

		return resp, err
	}
}

// GRPCTraceStreamServerInterceptor sets the trace context or starts the trace client span based on request.
// This is used by proxy requests too.
func GRPCTraceStreamServerInterceptor(appID string, spec config.TracingSpec) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		var (
			span     trace.Span
			spanKind trace.SpanStartOption
		)

		ctx := ss.Context()

		isProxied := false

		// This middleware is shared by multiple services and proxied requests, which need to be handled separately
		if strings.HasPrefix(info.FullMethod, daprInternalPrefix) {
			// For gRPC server invocation, this generates ServerSpan
			spanKind = trace.WithSpanKind(trace.SpanKindServer)
		} else if strings.HasPrefix(info.FullMethod, daprRuntimePrefix) {
			// For gRPC API, this generates ClientSpan
			spanKind = trace.WithSpanKind(trace.SpanKindClient)
		} else {
			// For proxied requests, this generates a span depending on whether this is the server (target) or client
			isProxied = true
			md, _ := metadata.FromIncomingContext(ctx)
			vals := md.Get(GRPCProxyAppIDKey)
			if len(vals) == 0 {
				return errors.Errorf("cannot proxy request: missing %s metadata", GRPCProxyAppIDKey)
			}

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

		err := handler(srv, wrapped)

		addSpanMetadataAndUpdateStatus(ctx, span, info.FullMethod, appID, nil, true)

		// Add grpc-trace-bin header for all non-invocation api's
		if !isProxied && info.FullMethod != daprInvokeServiceMethod {
			traceContextBinary := diagUtils.BinaryFromSpanContext(span.SpanContext())
			grpc.SetHeader(ctx, metadata.Pairs(GRPCTraceContextKey, string(traceContextBinary)))
		}

		UpdateSpanStatusFromGRPCError(span, err)
		span.End()

		return err
	}
}

func addSpanMetadataAndUpdateStatus(ctx context.Context, span trace.Span, fullMethod, appID string, req interface{}, stream bool) {
	var prefixedMetadata map[string]string
	if span.SpanContext().IsSampled() {
		// users can add dapr- prefix if they want to see the header values in span attributes.
		prefixedMetadata = userDefinedMetadata(ctx)

		// Populates dapr- prefixed header first
		AddAttributesToSpan(span, prefixedMetadata)

		spanAttr := map[string]string{}
		if !stream {
			spanAttr = spanAttributesMapFromGRPC(appID, req, fullMethod)
			AddAttributesToSpan(span, spanAttr)
		} else {
			spanAttr[daprAPISpanNameInternal] = fullMethod
		}

		// Correct the span name based on API.
		if sname, ok := spanAttr[daprAPISpanNameInternal]; ok {
			span.SetName(sname)
		}
	}
}

// userDefinedMetadata returns dapr- prefixed header from incoming metadata.
// Users can add dapr- prefixed headers that they want to see in span attributes.
func userDefinedMetadata(ctx context.Context) map[string]string {
	daprMetadata := map[string]string{}
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
	traceContext := md[GRPCTraceContextKey]
	if len(traceContext) > 0 {
		sc, ok = diagUtils.SpanContextFromBinary([]byte(traceContext[0]))
	} else {
		// add workaround to fallback on checking traceparent header
		// as grpc-trace-bin is not yet there in OpenTelemetry unlike OpenCensus , tracking issue https://github.com/open-telemetry/opentelemetry-specification/issues/639
		// and grpc-dotnet client adheres to OpenTelemetry Spec which only supports http based traceparent header in gRPC path
		// TODO : Remove this workaround fix once grpc-dotnet supports grpc-trace-bin header. Tracking issue https://github.com/dapr/dapr/issues/1827
		traceContext = md[TraceparentHeader]
		if len(traceContext) > 0 {
			sc, ok = SpanContextFromW3CString(traceContext[0])
			if ok && len(md[TracestateHeader]) > 0 {
				ts := TraceStateFromW3CString(md[TracestateHeader][0])
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

	return metadata.AppendToOutgoingContext(ctx, GRPCTraceContextKey, string(traceContextBinary))
}

// spanAttributesMapFromGRPC builds the span trace attributes map for gRPC calls based on given parameters as per open-telemetry specs.
func spanAttributesMapFromGRPC(appID string, req interface{}, rpcMethod string) map[string]string {
	// RPC Span Attribute reference https://github.com/open-telemetry/opentelemetry-specification/blob/master/specification/trace/semantic_conventions/rpc.md
	m := map[string]string{}

	var dbType string
	switch s := req.(type) {
	// Internal service invocation request
	case *internalv1pb.InternalInvokeRequest:
		m[gRPCServiceSpanAttributeKey] = daprGRPCServiceInvocationService

		// Rename spanname
		if s.GetActor() == nil {
			m[daprAPISpanNameInternal] = fmt.Sprintf("CallLocal/%s/%s", appID, s.Message.GetMethod())
			m[daprAPIInvokeMethod] = s.Message.GetMethod()
		} else {
			m[daprAPISpanNameInternal] = fmt.Sprintf("CallActor/%s/%s", s.GetActor().GetActorType(), s.Message.GetMethod())
			m[daprAPIActorTypeID] = fmt.Sprintf("%s.%s", s.GetActor().GetActorType(), s.GetActor().GetActorId())
		}

	// Dapr APIs
	case *runtimev1pb.InvokeServiceRequest:
		m[gRPCServiceSpanAttributeKey] = daprGRPCServiceInvocationService
		m[netPeerNameSpanAttributeKey] = s.GetId()
		m[daprAPISpanNameInternal] = fmt.Sprintf("CallLocal/%s/%s", s.GetId(), s.Message.GetMethod())

	case *runtimev1pb.PublishEventRequest:
		m[gRPCServiceSpanAttributeKey] = daprGRPCDaprService
		m[messagingSystemSpanAttributeKey] = pubsubBuildingBlockType
		m[messagingDestinationSpanAttributeKey] = s.GetTopic()
		m[messagingDestinationKindSpanAttributeKey] = messagingDestinationTopicKind

	case *runtimev1pb.InvokeBindingRequest:
		dbType = bindingBuildingBlockType
		m[dbNameSpanAttributeKey] = s.GetName()

	case *runtimev1pb.GetStateRequest:
		dbType = stateBuildingBlockType
		m[dbNameSpanAttributeKey] = s.GetStoreName()

	case *runtimev1pb.SaveStateRequest:
		dbType = stateBuildingBlockType
		m[dbNameSpanAttributeKey] = s.GetStoreName()

	case *runtimev1pb.DeleteStateRequest:
		dbType = stateBuildingBlockType
		m[dbNameSpanAttributeKey] = s.GetStoreName()

	case *runtimev1pb.GetSecretRequest:
		dbType = secretBuildingBlockType
		m[dbNameSpanAttributeKey] = s.GetStoreName()
	}

	if _, ok := m[dbNameSpanAttributeKey]; ok {
		m[gRPCServiceSpanAttributeKey] = daprGRPCDaprService
		m[dbSystemSpanAttributeKey] = dbType
		m[dbStatementSpanAttributeKey] = rpcMethod
		m[dbConnectionStringSpanAttributeKey] = dbType
	}

	m[daprAPIProtocolSpanAttributeKey] = daprAPIGRPCSpanAttrValue
	m[daprAPISpanAttributeKey] = rpcMethod

	return m
}
