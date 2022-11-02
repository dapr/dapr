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
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	apitrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	isemconv "github.com/dapr/dapr/pkg/diagnostics/semconv"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// GRPCTraceUnaryServerInterceptor sets the trace context or starts the trace client span based on request.
func GRPCTraceUnaryServerInterceptor(appID string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		var (
			span        apitrace.Span
			spanKind    apitrace.SpanStartOption
			reqSpanAttr []attribute.KeyValue
		)
		sc, _ := SpanContextFromIncomingGRPCMetadata(ctx)
		// This middleware is shared by internal gRPC for service invocation and api
		// so that it needs to handle separately.
		if isInternalCalls(info.FullMethod) {
			// For internals package, this generates ServerSpan.
			spanKind = apitrace.WithSpanKind(apitrace.SpanKindServer)
		} else {
			// For runtime package, this generates ClientSpan.
			spanKind = apitrace.WithSpanKind(apitrace.SpanKindClient)
		}

		ctx = apitrace.ContextWithRemoteSpanContext(ctx, sc)
		ctx, span = defaultTracer.Start(ctx, info.FullMethod, spanKind)
		reqSpanAttr = spanAttributesMapFromGRPC(appID, req, info.FullMethod)

		resp, err := handler(ctx, req)
		span.SetAttributes(reqSpanAttr...)

		// Add grpc-trace-bin header for all non-invocation api's
		if info.FullMethod != "/dapr.proto.runtime.v1.Dapr/InvokeService" {
			sc := span.SpanContext()
			traceparent := diagUtils.TraceparentToW3CString(sc)
			tracestate := diagUtils.TraceStateToW3CString(sc)
			pairs := metadata.Pairs(
				diagUtils.TraceparentHeader, traceparent,
				diagUtils.TracestateHeader, tracestate)
			grpc.SetHeader(ctx, pairs)
		}

		UpdateSpanStatusFromGRPCError(span, err)
		span.End()

		return resp, err
	}
}

// GRPCTraceStreamServerInterceptor sets the trace context or starts the trace client span based on request.
func GRPCTraceStreamServerInterceptor(appID string) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		var span apitrace.Span
		spanName := info.FullMethod

		if ss == nil {
			return nil
		}
		ctx := ss.Context()
		md, _ := metadata.FromIncomingContext(ctx)
		vals := md.Get(DaprAppIDKey)
		if len(vals) == 0 {
			return errors.Errorf("cannot proxy request: missing %s metadata", DaprAppIDKey)
		}

		targetID := vals[0]
		wrapped := grpcMiddleware.WrapServerStream(ss)
		sc, _ := SpanContextFromIncomingGRPCMetadata(ctx)
		var spanKind apitrace.SpanStartOption

		if appID == targetID {
			spanKind = apitrace.WithSpanKind(apitrace.SpanKindServer)
		} else {
			spanKind = apitrace.WithSpanKind(apitrace.SpanKindClient)
		}

		ctx = apitrace.ContextWithRemoteSpanContext(ctx, sc)
		ctx, span = defaultTracer.Start(ctx, spanName, spanKind)
		wrapped.WrappedContext = ctx
		err := handler(srv, wrapped)

		addSpanMetadataAndUpdateStatus(span, info.FullMethod, appID, nil, true)

		UpdateSpanStatusFromGRPCError(span, err)
		span.End()

		return err
	}
}

func addSpanMetadataAndUpdateStatus(span apitrace.Span, fullMethod, appID string, req interface{}, stream bool) {
	var attrs []attribute.KeyValue
	if !stream {
		attrs = spanAttributesMapFromGRPC(appID, req, fullMethod)
		span.SetAttributes(attrs...)
	}
}

func StartGRPCProducerSpanChildFromParent(ct context.Context, sc apitrace.SpanContext, spanName string) (context.Context, apitrace.Span) {
	netCtx := apitrace.ContextWithRemoteSpanContext(ct, sc)
	spanKind := apitrace.WithSpanKind(apitrace.SpanKindProducer)

	ctx, span := defaultTracer.Start(netCtx, spanName, spanKind)

	return ctx, span
}

// UpdateSpanStatusFromGRPCError updates tracer span status based on error object.
func UpdateSpanStatusFromGRPCError(span apitrace.Span, err error) {
	if span == nil || err == nil {
		return
	}

	_, ok := status.FromError(err)
	if ok {
		span.SetStatus(codes.Ok, "")
	} else {
		span.SetStatus(codes.Error, err.Error())
	}
}

// SpanContextFromIncomingGRPCMetadata returns the SpanContext stored in incoming metadata of context, or empty if there isn't one.
func SpanContextFromIncomingGRPCMetadata(ctx context.Context) (apitrace.SpanContext, bool) {
	var (
		sc apitrace.SpanContext
		md metadata.MD
		ok bool
	)
	if md, ok = metadata.FromIncomingContext(ctx); !ok {
		return sc, false
	}
	_, sc = otelgrpc.Extract(ctx, &md)

	return sc, ok
}

// SpanContextToGRPCMetadata appends binary serialized SpanContext to the outgoing GRPC context.
func SpanContextToGRPCMetadata(ctx context.Context) context.Context {
	md := metadata.MD{}
	otelgrpc.Inject(ctx, &md)

	var kv []string
	for k, vals := range md {
		for _, v := range vals {
			kv = append(kv, k, v)
		}
	}
	if kv != nil {
		ctx = metadata.AppendToOutgoingContext(ctx, kv...)
	}
	return ctx
}

func isInternalCalls(method string) bool {
	return strings.HasPrefix(method, "/dapr.proto.internals.")
}

// spanAttributesMapFromGRPC builds the span trace attributes map for gRPC calls based on given parameters as per open-telemetry specs.
func spanAttributesMapFromGRPC(appID string, req interface{}, rpcMethod string) []attribute.KeyValue {
	// RPC Span Attribute reference https://github.com/open-telemetry/opentelemetry-specification/blob/master/specification/trace/semantic_conventions/rpc.md
	m := []attribute.KeyValue{}

	var isExistComponent bool
	switch s := req.(type) {
	// Internal service invocation request
	case *internalv1pb.InternalInvokeRequest:
		m = append(m, semconv.RPCServiceKey.String(daprRPCServiceInvocationService))

		internalV := fmt.Sprintf("CallLocal/%s/%s", appID, s.Message.GetMethod())
		m = append(m, daprAPISpanNameInternalKey.String(internalV),
			isemconv.APIInvokeMethodKey.String(s.Message.GetMethod()))

	// Dapr APIs
	case *runtimev1pb.InvokeServiceRequest:
		m = append(m,
			semconv.RPCServiceKey.String(daprRPCServiceInvocationService),
			semconv.NetPeerNameKey.String(s.GetId()))
		internalV := fmt.Sprintf("CallLocal/%s/%s", s.GetId(), s.Message.GetMethod())
		m = append(m, daprAPISpanNameInternalKey.String(internalV))

	case *runtimev1pb.PublishEventRequest:
		m = append(m, semconv.RPCServiceKey.String(daprRPCDaprService),
			semconv.MessagingSystemKey.String("pubsub"),
			semconv.MessagingDestinationKey.String(s.GetTopic()),
			semconv.MessagingDestinationKindTopic)

	case *runtimev1pb.BulkPublishRequest:
		m = append(m, semconv.RPCServiceKey.String(daprRPCDaprService),
			semconv.MessagingSystemKey.String("pubsub"),
			semconv.MessagingDestinationKey.String(s.GetTopic()),
			semconv.MessagingDestinationKindTopic)
	case *runtimev1pb.InvokeBindingRequest:
		isExistComponent = true
		m = append(m, isemconv.ComponentBindings,
			isemconv.ComponentNameKey.String(s.GetName()))

	case *runtimev1pb.GetStateRequest:
		isExistComponent = true
		m = append(m, isemconv.ComponentStates,
			isemconv.ComponentNameKey.String(s.GetStoreName()))

	case *runtimev1pb.SaveStateRequest:
		isExistComponent = true
		m = append(m, isemconv.ComponentStates,
			isemconv.ComponentNameKey.String(s.GetStoreName()))

	case *runtimev1pb.DeleteStateRequest:
		isExistComponent = true
		m = append(m, isemconv.ComponentStates,
			isemconv.ComponentNameKey.String(s.GetStoreName()))

	case *runtimev1pb.GetSecretRequest:
		isExistComponent = true
		m = append(m, isemconv.ComponentSecrets,
			isemconv.ComponentNameKey.String(s.GetStoreName()))
	}

	if isExistComponent {
		m = append(m, semconv.RPCServiceKey.String(daprRPCDaprService),
			isemconv.ComponentMethodKey.String(rpcMethod))
	} else {
		m = append(m, isemconv.APIKey.String(rpcMethod))
	}
	m = append(m, isemconv.APIProtocolGRPC)

	return m
}
