package grpc

import (
	"context"
	"net"
	"testing"

	"github.com/dapr/dapr/pkg/channel"
	pb "github.com/dapr/dapr/pkg/proto"
	any "github.com/golang/protobuf/ptypes/any"
	empty "github.com/golang/protobuf/ptypes/empty"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

type mockServer struct {
}

func (m *mockServer) OnMethodCall(context context.Context, envelope *pb.AppMethodCallEnvelope) (*any.Any, error) {
	ret := ""
	for k, v := range envelope.Metadata {
		ret += k + "=" + v + "&"
	}
	return &any.Any{Value: []byte(ret)}, nil
}
func (m *mockServer) RestoreState(context.Context, *pb.State) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}
func (m *mockServer) GetConfig(context.Context, *empty.Empty) (*pb.ApplicationConfig, error) {
	return &pb.ApplicationConfig{}, nil
}

func TestInvokeMethod(t *testing.T) {
	lis, err := net.Listen("tcp", ":9998")
	assert.NoError(t, err)

	grpcServer := grpc.NewServer()
	go func() {
		pb.RegisterAppServer(grpcServer, &mockServer{})
		grpcServer.Serve(lis)
	}()

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())
	conn, err := grpc.Dial("localhost:9998", opts...)
	defer conn.Close()
	assert.NoError(t, err)

	c := Channel{baseAddress: "localhost:9998", client: conn}
	request := &channel.InvokeRequest{
		Metadata: map[string]string{"http.query_string": "param1=val1&param2=val2"},
	}
	response, err := c.InvokeMethod(request)
	grpcServer.Stop()

	assert.NoError(t, err)
	assert.True(t, "param1=val1&param2=val2&" == string(response.Data) ||
		"param2=val2&param1=val1&" == string(response.Data))
}
