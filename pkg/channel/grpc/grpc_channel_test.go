// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/golang/protobuf/ptypes/any"
	empty "github.com/golang/protobuf/ptypes/empty"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// The Implementation of fake user app server
type mockServer struct {
}

func (m *mockServer) OnInvoke(ctx context.Context, in *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	dt := map[string]string{
		"method": in.Method,
	}

	for k, v := range md {
		dt[k] = v[0]
	}

	dt["httpverb"] = in.HttpExtension.GetVerb().String()
	serialized, _ := json.Marshal(in.HttpExtension.Querystring)
	dt["querystring"] = string(serialized)

	ds, _ := json.Marshal(dt)
	return &commonv1pb.InvokeResponse{Data: &any.Any{Value: ds}, ContentType: "application/json"}, nil
}

func (m *mockServer) ListTopicSubscriptions(ctx context.Context, in *empty.Empty) (*runtimev1pb.ListTopicSubscriptionsResponse, error) {
	return &runtimev1pb.ListTopicSubscriptionsResponse{}, nil
}

func (m *mockServer) ListInputBindings(ctx context.Context, in *empty.Empty) (*runtimev1pb.ListInputBindingsResponse, error) {
	return &runtimev1pb.ListInputBindingsResponse{}, nil
}

func (m *mockServer) OnBindingEvent(ctx context.Context, in *runtimev1pb.BindingEventRequest) (*runtimev1pb.BindingEventResponse, error) {
	return &runtimev1pb.BindingEventResponse{}, nil
}

func (m *mockServer) OnTopicEvent(ctx context.Context, in *runtimev1pb.TopicEventRequest) (*runtimev1pb.TopicEventResponse, error) {
	return &runtimev1pb.TopicEventResponse{}, nil
}

// TODO: Add APIVersion testing

func TestInvokeMethod(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:9998")
	assert.NoError(t, err)

	grpcServer := grpc.NewServer()
	go func() {
		runtimev1pb.RegisterAppCallbackServer(grpcServer, &mockServer{})
		grpcServer.Serve(lis)
	}()

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())
	conn, err := grpc.Dial("localhost:9998", opts...)
	defer close(t, conn)
	assert.NoError(t, err)

	c := Channel{baseAddress: "localhost:9998", client: conn}
	req := invokev1.NewInvokeMethodRequest("method")
	req.WithHTTPExtension(http.MethodPost, "param1=val1&param2=val2")
	response, err := c.InvokeMethod(context.Background(), req)
	assert.NoError(t, err)
	contentType, body := response.RawData()
	grpcServer.Stop()

	assert.Equal(t, "application/json", contentType)

	actual := map[string]string{}
	json.Unmarshal(body, &actual)

	assert.Equal(t, "POST", actual["httpverb"])
	assert.Equal(t, "method", actual["method"])
	assert.Equal(t, "{\"param1\":\"val1\",\"param2\":\"val2\"}", actual["querystring"])
}

func close(t *testing.T, c io.Closer) {
	err := c.Close()
	if err != nil {
		assert.Fail(t, fmt.Sprintf("unable to close %s", err))
	}
}
