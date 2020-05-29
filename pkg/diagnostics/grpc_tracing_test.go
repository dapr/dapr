// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/config"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/stretchr/testify/assert"
	"go.opencensus.io/trace"
	"google.golang.org/grpc/metadata"
)

func TestStartTracingClientSpanFromGRPCContext(t *testing.T) {
	spec := config.TracingSpec{SamplingRate: "0.5"}
	ctx := context.Background()
	ctx = metadata.NewIncomingContext(ctx, metadata.MD{"dapr-headerKey": {"v3", "v4"}})

	StartTracingClientSpanFromGRPCContext(ctx, "invoke", spec)
}

func TestWithGRPCSpanContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
	defer cancel()
	wantSc := trace.SpanContext{
		TraceID:      trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
		SpanID:       trace.SpanID{0, 0, 0, 0, 0, 0, 0, 0},
		TraceOptions: trace.TraceOptions(1),
	}
	ctx = AppendToOutgoingGRPCContext(ctx, wantSc)

	gotSc, _ := FromOutgoingGRPCContext(ctx)

	assert.Equalf(t, gotSc, wantSc, "WithGRPCSpanContext gotSc = %v, want %v", gotSc, wantSc)
}

func TestWithGRPCWithNoSpanContext(t *testing.T) {
	t.Run("No SpanContext with always sampling rate", func(t *testing.T) {
		ctx := context.Background()
		spec := config.TracingSpec{SamplingRate: "1"}
		sc := GetSpanContextFromGRPC(ctx, spec)
		assert.NotEmpty(t, sc, "Should get default span context")
		assert.NotEmpty(t, sc.TraceID, "Should get default traceID")
		assert.NotEmpty(t, sc.SpanID, "Should get default spanID")
		assert.Equal(t, 1, int(sc.TraceOptions), "Should be sampled")
	})

	t.Run("No SpanContext with non-zero sampling rate", func(t *testing.T) {
		ctx := context.Background()
		spec := config.TracingSpec{SamplingRate: "0.5"}
		sc := GetSpanContextFromGRPC(ctx, spec)
		assert.NotEmpty(t, sc.TraceID, "Should get default traceID")
		assert.NotEmpty(t, sc.SpanID, "Should get default spanID")
		assert.NotEmpty(t, sc, "Should get default span context")
	})

	t.Run("No SpanContext with zero sampling rate", func(t *testing.T) {
		ctx := context.Background()
		spec := config.TracingSpec{SamplingRate: "0"}
		sc := GetSpanContextFromGRPC(ctx, spec)
		assert.NotEmpty(t, sc, "Should get default span context")
		assert.NotEmpty(t, sc.TraceID, "Should get default traceID")
		assert.NotEmpty(t, sc.SpanID, "Should get default spanID")
		assert.Equal(t, 0, int(sc.TraceOptions), "Should not be sampled")
	})
}

func TestGetSpanAttributesMapFromGRPC(t *testing.T) {
	var tests = []struct {
		rpcMethod                    string
		requestType                  string
		expectedServiceNameAttribute string
		expectedCustomAttribute      string
	}{
		{"/dapr.proto.internals.v1.ServiceInvocation/CallLocal", "InternalInvokeRequest", "ServiceInvocation", "mymethod"},
		{"/dapr.proto.runtime.v1.Dapr/InvokeService", "InvokeServiceRequest", "InvokeService", "mymethod"},
		{"/dapr.proto.runtime.v1.Dapr/GetState", "GetStateRequest", "GetState", "mystore"},
		{"/dapr.proto.runtime.v1.Dapr/SaveState", "SaveStateRequest", "SaveState", "mystore"},
		{"/dapr.proto.runtime.v1.Dapr/DeleteState", "DeleteStateRequest", "DeleteState", "mystore"},
		{"/dapr.proto.runtime.v1.Dapr/GetSecret", "GetSecretRequest", "GetSecret", "mysecretstore"},
		{"/dapr.proto.runtime.v1.Dapr/InvokeBinding", "InvokeBindingRequest", "InvokeBinding", "mybindings"},
		{"/dapr.proto.runtime.v1.Dapr/PublishEvent", "PublishEventRequest", "PublishEvent", "mytopic"},
		{"/invalid.rpcMethodformat", "InvokeServiceRequest", "", "mymethod"},
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
			case "InternalInvokeRequest":
				req = &internalv1pb.InternalInvokeRequest{Message: &commonv1pb.InvokeRequest{Method: "mymethod"}}
			}

			got := getSpanAttributesMapFromGRPC(req, tt.rpcMethod)
			assert.Equal(t, tt.expectedServiceNameAttribute, got[gRPCServiceSpanAttributeKey], "servicename attribute should be equal")
			assert.Equal(t, tt.expectedCustomAttribute, got[gRPCDaprInstanceSpanAttributeKey], "custom attribute should be equal")
		})
	}
}
