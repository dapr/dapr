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
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dapr/dapr/pkg/config"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func TestSpanAttributesMapFromGRPC(t *testing.T) {
	tests := []struct {
		rpcMethod                    string
		requestType                  string
		expectedServiceNameAttribute string
		expectedCustomAttribute      string
	}{
		{"/dapr.proto.internals.v1.ServiceInvocation/CallLocal", "InternalInvokeRequest", "ServiceInvocation", "mymethod"},
		// InvokeService will be ServiceInvocation because this call will be treated as client call
		// of service invocation.
		{"/dapr.proto.runtime.v1.Dapr/InvokeService", "InvokeServiceRequest", "ServiceInvocation", "mymethod"},
		{"/dapr.proto.runtime.v1.Dapr/GetState", "GetStateRequest", "Dapr", "mystore"},
		{"/dapr.proto.runtime.v1.Dapr/SaveState", "SaveStateRequest", "Dapr", "mystore"},
		{"/dapr.proto.runtime.v1.Dapr/DeleteState", "DeleteStateRequest", "Dapr", "mystore"},
		{"/dapr.proto.runtime.v1.Dapr/GetSecret", "GetSecretRequest", "Dapr", "mysecretstore"},
		{"/dapr.proto.runtime.v1.Dapr/InvokeBinding", "InvokeBindingRequest", "Dapr", "mybindings"},
		{"/dapr.proto.runtime.v1.Dapr/PublishEvent", "PublishEventRequest", "Dapr", "mytopic"},
	}
	var req interface{}
	for _, tt := range tests {
		t.Run(tt.rpcMethod, func(t *testing.T) {
			switch tt.requestType {
			case "InvokeServiceRequest":
				req = &runtimev1pb.InvokeServiceRequest{Message: &commonv1pb.InvokeRequest{Method: "mymethod"}}
			case "GetStateRequest":
				req = &runtimev1pb.GetStateRequest{StoreName: "mystore"}
			case "SaveStateRequest":
				req = &runtimev1pb.SaveStateRequest{StoreName: "mystore"}
			case "DeleteStateRequest":
				req = &runtimev1pb.DeleteStateRequest{StoreName: "mystore"}
			case "GetSecretRequest":
				req = &runtimev1pb.GetSecretRequest{StoreName: "mysecretstore"}
			case "InvokeBindingRequest":
				req = &runtimev1pb.InvokeBindingRequest{Name: "mybindings"}
			case "PublishEventRequest":
				req = &runtimev1pb.PublishEventRequest{Topic: "mytopic"}
			case "TopicEventRequest":
				req = &runtimev1pb.TopicEventRequest{Topic: "mytopic"}
			case "BindingEventRequest":
				req = &runtimev1pb.BindingEventRequest{Name: "mybindings"}
			case "InternalInvokeRequest":
				req = &internalv1pb.InternalInvokeRequest{Message: &commonv1pb.InvokeRequest{Method: "mymethod"}}
			}

			got := spanAttributesMapFromGRPC("fakeAppID", req, tt.rpcMethod)
			assert.Equal(t, tt.expectedServiceNameAttribute, got[gRPCServiceSpanAttributeKey], "servicename attribute should be equal")
		})
	}
}

func TestUserDefinedMetadata(t *testing.T) {
	md := metadata.MD{
		"dapr-userdefined-1": []string{"value1"},
		"dapr-userdefined-2": []string{"value2", "value3"},
		"no-attr":            []string{"value3"},
	}

	testCtx := metadata.NewIncomingContext(context.Background(), md)

	m := userDefinedMetadata(testCtx)

	assert.Equal(t, 2, len(m))
	assert.Equal(t, "value1", m["dapr-userdefined-1"])
	assert.Equal(t, "value2", m["dapr-userdefined-2"])
}

func TestSpanContextToGRPCMetadata(t *testing.T) {
	t.Run("empty span context", func(t *testing.T) {
		ctx := context.Background()
		newCtx := SpanContextToGRPCMetadata(ctx, trace.SpanContext{})

		assert.Equal(t, ctx, newCtx)
	})
}

func TestGRPCTraceUnaryServerInterceptor(t *testing.T) {
	rate := config.TracingSpec{SamplingRate: "1"}

	exp := newOtelFakeExporter()

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)

	interceptor := GRPCTraceUnaryServerInterceptor("fakeAppID", rate)

	testTraceParent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	testSpanContext, _ := SpanContextFromW3CString(testTraceParent)
	testTraceBinary := diag_utils.BinaryFromSpanContext(testSpanContext)
	ctx := context.Background()

	t.Run("grpc-trace-bin is given", func(t *testing.T) {
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("grpc-trace-bin", string(testTraceBinary)))
		fakeInfo := &grpc.UnaryServerInfo{
			FullMethod: "/dapr.proto.runtime.v1.Dapr/GetState",
		}
		fakeReq := &runtimev1pb.GetStateRequest{
			StoreName: "statestore",
			Key:       "state",
		}

		var span trace.Span
		assertHandler := func(ctx context.Context, req interface{}) (interface{}, error) {
			span = diag_utils.SpanFromContext(ctx)
			return nil, errors.New("fake error")
		}

		interceptor(ctx, fakeReq, fakeInfo, assertHandler)

		sc := span.SpanContext()
		traceID := sc.TraceID()
		assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", fmt.Sprintf("%x", traceID[:]))
		spanID := sc.SpanID()
		assert.NotEqual(t, "00f067aa0ba902b7", fmt.Sprintf("%x", spanID[:]))
	})

	t.Run("grpc-trace-bin is not given", func(t *testing.T) {
		fakeInfo := &grpc.UnaryServerInfo{
			FullMethod: "/dapr.proto.runtime.v1.Dapr/GetState",
		}
		fakeReq := &runtimev1pb.GetStateRequest{
			StoreName: "statestore",
			Key:       "state",
		}

		var span trace.Span
		assertHandler := func(ctx context.Context, req interface{}) (interface{}, error) {
			span = diag_utils.SpanFromContext(ctx)
			return nil, errors.New("fake error")
		}

		interceptor(ctx, fakeReq, fakeInfo, assertHandler)

		sc := span.SpanContext()
		traceID := sc.TraceID()
		spanID := sc.SpanID()
		assert.NotEmpty(t, fmt.Sprintf("%x", traceID[:]))
		assert.NotEmpty(t, fmt.Sprintf("%x", spanID[:]))
	})

	t.Run("InvokeService call", func(t *testing.T) {
		fakeInfo := &grpc.UnaryServerInfo{
			FullMethod: "/dapr.proto.runtime.v1.Dapr/InvokeService",
		}
		fakeReq := &runtimev1pb.InvokeServiceRequest{
			Id:      "targetID",
			Message: &commonv1pb.InvokeRequest{Method: "method1"},
		}

		var span trace.Span
		assertHandler := func(ctx context.Context, req interface{}) (interface{}, error) {
			span = diag_utils.SpanFromContext(ctx)
			return nil, errors.New("fake error")
		}

		interceptor(ctx, fakeReq, fakeInfo, assertHandler)

		sc := span.SpanContext()
		spanString := fmt.Sprintf("%v", span)
		assert.True(t, strings.Contains(spanString, "CallLocal/targetID/method1"))
		traceID := sc.TraceID()
		spanID := sc.SpanID()
		assert.NotEmpty(t, fmt.Sprintf("%x", traceID[:]))
		assert.NotEmpty(t, fmt.Sprintf("%x", spanID[:]))
	})
}

func TestGRPCTraceStreamServerInterceptor(t *testing.T) {
	interceptor := GRPCTraceStreamServerInterceptor("test", config.TracingSpec{})

	t.Run("invalid proxy request, return nil", func(t *testing.T) {
		ctx := context.TODO()
		fakeInfo := &grpc.StreamServerInfo{
			FullMethod: "/dapr.proto.runtime.v1.Dapr/GetState",
		}

		h := func(srv interface{}, stream grpc.ServerStream) error {
			return nil
		}

		err := interceptor(ctx, nil, fakeInfo, h)
		assert.Nil(t, err)
	})

	t.Run("valid proxy request without app id, return error", func(t *testing.T) {
		ctx := context.TODO()
		fakeInfo := &grpc.StreamServerInfo{
			FullMethod: "/myapp.v1.DoSomething",
		}

		err := interceptor(ctx, &fakeStream{}, fakeInfo, nil)
		assert.Error(t, err)
	})
}

type fakeStream struct{}

func (f *fakeStream) Context() context.Context {
	return context.TODO()
}

func (f *fakeStream) SetHeader(metadata.MD) error {
	return nil
}

func (f *fakeStream) SendHeader(metadata.MD) error {
	return nil
}

func (f *fakeStream) SetTrailer(metadata.MD) {
}

func (f *fakeStream) SendMsg(m interface{}) error {
	return nil
}

func (f *fakeStream) RecvMsg(m interface{}) error {
	return nil
}

func TestSpanContextSerialization(t *testing.T) {
	wantScConfig := trace.SpanContextConfig{
		TraceID:    trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
		SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
		TraceFlags: trace.TraceFlags(1),
	}
	wantSc := trace.NewSpanContext(wantScConfig)
	passedOverWire := diag_utils.BinaryFromSpanContext(wantSc)
	storedInDapr := base64.StdEncoding.EncodeToString(passedOverWire)
	decoded, _ := base64.StdEncoding.DecodeString(storedInDapr)
	gotSc, _ := diag_utils.SpanContextFromBinary(decoded)
	assert.Equal(t, wantSc, gotSc)
}
