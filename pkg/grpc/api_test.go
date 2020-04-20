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
	"time"

	"github.com/dapr/components-contrib/exporters"
	"github.com/dapr/components-contrib/exporters/stringexporter"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/logger"
	daprv1pb "github.com/dapr/dapr/pkg/proto/dapr/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/daprinternal/v1"
	"github.com/golang/protobuf/ptypes/any"
	"github.com/golang/protobuf/ptypes/empty"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	grpc_go "google.golang.org/grpc"
)

type mockGRPCAPI struct {
}

func (m *mockGRPCAPI) CallLocal(ctx context.Context, in *internalv1pb.LocalCallEnvelope) (*internalv1pb.InvokeResponse, error) {
	return &internalv1pb.InvokeResponse{}, nil
}

func (m *mockGRPCAPI) CallActor(ctx context.Context, in *internalv1pb.CallActorEnvelope) (*internalv1pb.InvokeResponse, error) {
	return &internalv1pb.InvokeResponse{}, nil
}

func (m *mockGRPCAPI) PublishEvent(ctx context.Context, in *daprv1pb.PublishEventEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) InvokeService(ctx context.Context, in *daprv1pb.InvokeServiceEnvelope) (*daprv1pb.InvokeServiceResponseEnvelope, error) {
	return &daprv1pb.InvokeServiceResponseEnvelope{}, nil
}

func (m *mockGRPCAPI) InvokeBinding(ctx context.Context, in *daprv1pb.InvokeBindingEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) GetState(ctx context.Context, in *daprv1pb.GetStateEnvelope) (*daprv1pb.GetStateResponseEnvelope, error) {
	return &daprv1pb.GetStateResponseEnvelope{}, nil
}

func (m *mockGRPCAPI) SaveState(ctx context.Context, in *daprv1pb.SaveStateEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) DeleteState(ctx context.Context, in *daprv1pb.DeleteStateEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) GetSecret(ctx context.Context, in *daprv1pb.GetSecretEnvelope) (*daprv1pb.GetSecretResponseEnvelope, error) {
	return &daprv1pb.GetSecretResponseEnvelope{}, nil
}

func createExporters(meta exporters.Metadata) {
	exporter := stringexporter.NewStringExporter(logger.NewLogger("fakeLogger"))
	exporter.Init("fakeID", "fakeAddress", meta)
}

func TestCallActorWithTracing(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	buffer := ""
	spec := config.TracingSpec{SamplingRate: "1"}

	meta := exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	}
	createExporters(meta)

	server := grpc_go.NewServer(
		grpc_go.StreamInterceptor(grpc_middleware.ChainStreamServer(diag.TracingGRPCMiddlewareStream(spec))),
		grpc_go.UnaryInterceptor(grpc_middleware.ChainUnaryServer(diag.TracingGRPCMiddlewareUnary(spec))),
	)

	go func() {
		internalv1pb.RegisterDaprInternalServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := internalv1pb.NewDaprInternalClient(conn)
	request := &internalv1pb.CallActorEnvelope{
		ActorID:   "actor-1",
		ActorType: "test-actor",
		Method:    "what",
	}

	client.CallActor(context.Background(), request)

	server.Stop()
	assert.Equal(t, "0", buffer, "failed to generate proper traces with actor call")
}

func TestCallRemoteAppWithTracing(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	buffer := ""
	spec := config.TracingSpec{SamplingRate: "1"}

	meta := exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	}
	createExporters(meta)

	server := grpc_go.NewServer(
		grpc_go.StreamInterceptor(grpc_middleware.ChainStreamServer(diag.TracingGRPCMiddlewareStream(spec))),
		grpc_go.UnaryInterceptor(grpc_middleware.ChainUnaryServer(diag.TracingGRPCMiddlewareUnary(spec))),
	)

	go func() {
		internalv1pb.RegisterDaprInternalServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := internalv1pb.NewDaprInternalClient(conn)
	request := &internalv1pb.LocalCallEnvelope{
		Method: "what",
	}

	client.CallLocal(context.Background(), request)

	server.Stop()
	assert.Equal(t, "0", buffer, "failed to generate proper traces with app call")
}

func TestSaveState(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	server := grpc_go.NewServer()
	go func() {
		daprv1pb.RegisterDaprServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := daprv1pb.NewDaprClient(conn)
	request := &daprv1pb.SaveStateEnvelope{
		Requests: []*daprv1pb.StateRequest{
			{
				Key:   "1",
				Value: &any.Any{Value: []byte("2")},
			},
		},
	}

	_, err = client.SaveState(context.Background(), request)
	server.Stop()
	assert.Nil(t, err)
}

func TestGetState(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	server := grpc_go.NewServer()
	go func() {
		daprv1pb.RegisterDaprServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := daprv1pb.NewDaprClient(conn)
	_, err = client.GetState(context.Background(), &daprv1pb.GetStateEnvelope{})
	server.Stop()
	assert.Nil(t, err)
}

func TestDeleteState(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	server := grpc_go.NewServer()
	go func() {
		daprv1pb.RegisterDaprServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := daprv1pb.NewDaprClient(conn)
	_, err = client.DeleteState(context.Background(), &daprv1pb.DeleteStateEnvelope{})
	server.Stop()
	assert.Nil(t, err)
}

func TestPublishTopic(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	server := grpc_go.NewServer()
	go func() {
		daprv1pb.RegisterDaprServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := daprv1pb.NewDaprClient(conn)
	_, err = client.PublishEvent(context.Background(), &daprv1pb.PublishEventEnvelope{})
	server.Stop()
	assert.Nil(t, err)
}

func TestInvokeBinding(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	server := grpc_go.NewServer()
	go func() {
		daprv1pb.RegisterDaprServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := daprv1pb.NewDaprClient(conn)
	_, err = client.InvokeBinding(context.Background(), &daprv1pb.InvokeBindingEnvelope{})
	server.Stop()
	assert.Nil(t, err)
}

func doClose(t *testing.T, c io.Closer) {
	err := c.Close()
	if err != nil {
		assert.Fail(t, fmt.Sprintf("unable to close %s", err))
	}
}
*/
