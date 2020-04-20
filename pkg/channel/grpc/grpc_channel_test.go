// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

/*
import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/dapr/dapr/pkg/channel"
	daprclientv1pb "github.com/dapr/dapr/pkg/proto/daprclient/v1"
	any "github.com/golang/protobuf/ptypes/any"
	empty "github.com/golang/protobuf/ptypes/empty"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

type mockServer struct {
}

func (m *mockServer) OnInvoke(ctx context.Context, in *daprclientv1pb.InvokeEnvelope) (*any.Any, error) {
	ret := ""
	for k, v := range in.Metadata {
		ret += k + "=" + v + "&"
	}
	return &any.Any{Value: []byte(ret)}, nil
}
func (m *mockServer) GetTopicSubscriptions(ctx context.Context, in *empty.Empty) (*daprclientv1pb.GetTopicSubscriptionsEnvelope, error) {
	return &daprclientv1pb.GetTopicSubscriptionsEnvelope{}, nil
}
func (m *mockServer) GetBindingsSubscriptions(ctx context.Context, in *empty.Empty) (*daprclientv1pb.GetBindingsSubscriptionsEnvelope, error) {
	return &daprclientv1pb.GetBindingsSubscriptionsEnvelope{}, nil
}
func (m *mockServer) OnBindingEvent(ctx context.Context, in *daprclientv1pb.BindingEventEnvelope) (*daprclientv1pb.BindingResponseEnvelope, error) {
	return &daprclientv1pb.BindingResponseEnvelope{}, nil
}
func (m *mockServer) OnTopicEvent(ctx context.Context, in *daprclientv1pb.CloudEventEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func TestInvokeMethod(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:9998")
	assert.NoError(t, err)

	grpcServer := grpc.NewServer()
	go func() {
		daprclientv1pb.RegisterDaprClientServer(grpcServer, &mockServer{})
		grpcServer.Serve(lis)
	}()

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())
	conn, err := grpc.Dial("localhost:9998", opts...)
	defer close(t, conn)
	assert.NoError(t, err)

	c := Channel{baseAddress: "localhost:9998", client: conn}
	request := &channel.InvokeRequest{
		Metadata: map[string]string{"http.query_string": "param1=val1&param2=val2"},
	}
	response, err := c.InvokeMethod(request)
	grpcServer.Stop()

	assert.NoError(t, err)
	assert.True(t, string(response.Data) == "param1=val1&param2=val2&" ||
		string(response.Data) == "param2=val2&param1=val1&")
}

func close(t *testing.T, c io.Closer) {
	err := c.Close()
	if err != nil {
		assert.Fail(t, fmt.Sprintf("unable to close %s", err))
	}
}
*/
