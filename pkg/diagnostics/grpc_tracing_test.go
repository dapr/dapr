// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"testing"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

func TestSpanAttributesMapFromGRPC(t *testing.T) {
	var tests = []struct {
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

			got := spanAttributesMapFromGRPC(req, tt.rpcMethod)
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
