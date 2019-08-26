package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/actionscore/actions/pkg/config"
	diag "github.com/actionscore/actions/pkg/diagnostics"
	pb "github.com/actionscore/actions/pkg/proto"
	"github.com/golang/protobuf/ptypes/empty"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/stretchr/testify/assert"
	grpc_go "google.golang.org/grpc"
)

type mockGRPCAPI struct {
}

func (m *mockGRPCAPI) CallActor(ctx context.Context, in *pb.CallActorEnvelope) (*pb.InvokeResponse, error) {
	return nil, nil
}
func (m *mockGRPCAPI) CallRemoteApp(ctx context.Context, in *pb.CallRemoteAppEnvelope) (*pb.InvokeResponse, error) {
	return nil, nil
}

func (m *mockGRPCAPI) CallLocal(ctx context.Context, in *pb.LocalCallEnvelope) (*pb.InvokeResponse, error) {
	return nil, nil
}
func (m *mockGRPCAPI) UpdateComponent(ctx context.Context, in *pb.Component) (*empty.Empty, error) {
	return nil, nil
}

func TestCallActorWithTracing(t *testing.T) {
	lis, err := net.Listen("tcp", ":9998")
	assert.NoError(t, err)

	buffer := ""
	spec := config.TracingSpec{ExporterType: "string"}
	diag.CreateExporter("", "", spec, &buffer)

	server := grpc_go.NewServer(
		grpc_go.StreamInterceptor(grpc_middleware.ChainStreamServer(diag.TracingGRPCMiddleware(spec))),
		grpc_go.UnaryInterceptor(grpc_middleware.ChainUnaryServer(diag.TracingGRPCMiddlewareUnary(spec))),
	)

	go func() {
		pb.RegisterActionsServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(10 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial("localhost:9998", opts...)
	defer conn.Close()
	assert.NoError(t, err)

	client := pb.NewActionsClient(conn)

	request := &pb.CallActorEnvelope{
		ActorID:   "actor-1",
		ActorType: "test-actor",
		Method:    "what",
	}

	client.CallActor(context.Background(), request)

	server.Stop()
	assert.Equal(t, "200", buffer, "failed to generate proper traces with actor call")
}

func TestCallRemoteAppWithTracing(t *testing.T) {
	lis, err := net.Listen("tcp", ":9998")
	assert.NoError(t, err)

	buffer := ""
	spec := config.TracingSpec{ExporterType: "string"}
	diag.CreateExporter("", "", spec, &buffer)

	server := grpc_go.NewServer(
		grpc_go.StreamInterceptor(grpc_middleware.ChainStreamServer(diag.TracingGRPCMiddleware(spec))),
		grpc_go.UnaryInterceptor(grpc_middleware.ChainUnaryServer(diag.TracingGRPCMiddlewareUnary(spec))),
	)

	go func() {
		pb.RegisterActionsServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(10 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial("localhost:9998", opts...)
	defer conn.Close()
	assert.NoError(t, err)

	client := pb.NewActionsClient(conn)

	request := &pb.CallRemoteAppEnvelope{
		Target: "actor-1",
		Method: "what",
	}

	client.CallRemoteApp(context.Background(), request)

	server.Stop()
	assert.Equal(t, "200", buffer, "failed to generate proper traces with actor call")
}
