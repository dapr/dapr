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
		method := info.FullMethod

		if isInternalCalls(method) {
			_, span = StartTracingServerSpanFromGRPCContext(newCtx, method, spec)
		} else {
			_, span = StartTracingClientSpanFromGRPCContext(newCtx, method, spec)
		}

		// build new context now on top of passed root context with started span context
		newCtx = NewContext(ctx, span.SpanContext())
		resp, err = handler(newCtx, req)

		// add span attributes
		m := GetSpanAttributesMapFromGRPC(req, info.FullMethod)
		AddAttributesToSpan(span, m)

		UpdateSpanStatusFromGRPCError(span, err, method)

		span.End()

		return resp, err
	}
}

// UpdateSpanStatusFromGRPCError updates tracer span status based on error object
func UpdateSpanStatusFromGRPCError(span *trace.Span, err error, method string) {
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
	var serviceName string

	p := rpcMethod
	if p[0] == '/' {
		p = rpcMethod[1:]
	}

	// Split up to 3 delimiters in '/package.service/method'
	// example for internal call : "/dapr.proto.internals.v1.ServiceInvocation/CallLocal"
	// example for non-internal call : "/dapr.proto.runtime.v1.Dapr/InvokeService"

	tokens := strings.SplitN(p, "/", 3)
	if len(tokens) >= 2 {
		if isInternalCalls(rpcMethod) {
			if t := strings.SplitN(tokens[0], ".", 5); len(t) >= 4 {
				serviceName = t[4]
			}
		} else {
			// TODO : need to revisit /package.service/method format in runtime calls, currently service name is "Dapr"
			// so instead of taking general "Dapr", taking method name as service name to give better insights
			serviceName = tokens[1]
		}
	} else {
		// default service name to full method name, after removing first "/"
		serviceName = p
	}

	m := make(map[string]string)
	m[gRPCServiceSpanAttributeKey] = serviceName

	// TODO: populate equivalent attributes with HTTP

	// below attribute is not as per open temetery spec, adding custom dapr attribute to have additional details
	m[gRPCDaprInstanceSpanAttributeKey] = extractComponentValueFromGRPCRequest(req)
	return m
}

func extractComponentValueFromGRPCRequest(req interface{}) string {
	switch s := req.(type) {
	case *internalv1pb.InternalInvokeRequest:
		return s.Message.GetMethod()
	case *runtimev1pb.InvokeServiceRequest:
		return s.Message.GetMethod()
	case *runtimev1pb.PublishEventRequest:
		return s.GetTopic()
	case *runtimev1pb.InvokeBindingRequest:
		return s.GetName()
	case *runtimev1pb.GetStateRequest:
		return s.GetStoreName()
	case *runtimev1pb.SaveStateRequest:
		return s.GetStoreName()
	case *runtimev1pb.DeleteStateRequest:
		return s.GetStoreName()
	case *runtimev1pb.GetSecretRequest:
		return s.GetStoreName()
	case *runtimev1pb.TopicEventRequest:
		return s.GetTopic()
	case *runtimev1pb.BindingEventRequest:
		return s.GetName()
	default:
		return ""
	}
}
