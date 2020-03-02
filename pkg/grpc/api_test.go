// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

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
	dapr_pb "github.com/dapr/dapr/pkg/proto/dapr"
	daprinternal_pb "github.com/dapr/dapr/pkg/proto/daprinternal"
	"github.com/golang/protobuf/ptypes/any"
	"github.com/golang/protobuf/ptypes/empty"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	grpc_go "google.golang.org/grpc"
)

type mockGRPCAPI struct {
}

func (m *mockGRPCAPI) CallLocal(ctx context.Context, in *daprinternal_pb.LocalCallEnvelope) (*daprinternal_pb.InvokeResponse, error) {
	return &daprinternal_pb.InvokeResponse{}, nil
}

func (m *mockGRPCAPI) CallActor(ctx context.Context, in *daprinternal_pb.CallActorEnvelope) (*daprinternal_pb.InvokeResponse, error) {
	return &daprinternal_pb.InvokeResponse{}, nil
}

func (m *mockGRPCAPI) UpdateComponent(ctx context.Context, in *daprinternal_pb.Component) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) PublishEvent(ctx context.Context, in *dapr_pb.PublishEventEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) InvokeService(ctx context.Context, in *dapr_pb.InvokeServiceEnvelope) (*dapr_pb.InvokeServiceResponseEnvelope, error) {
	return &dapr_pb.InvokeServiceResponseEnvelope{}, nil
}

func (m *mockGRPCAPI) InvokeBinding(ctx context.Context, in *dapr_pb.InvokeBindingEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) GetState(ctx context.Context, in *dapr_pb.GetStateEnvelope) (*dapr_pb.GetStateResponseEnvelope, error) {
	return &dapr_pb.GetStateResponseEnvelope{}, nil
}

func (m *mockGRPCAPI) SaveState(ctx context.Context, in *dapr_pb.SaveStateEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) DeleteState(ctx context.Context, in *dapr_pb.DeleteStateEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) GetSecret(ctx context.Context, in *dapr_pb.GetSecretEnvelope) (*dapr_pb.GetSecretResponseEnvelope, error) {
	return &dapr_pb.GetSecretResponseEnvelope{}, nil
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
	spec := config.TracingSpec{Enabled: true}

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
		daprinternal_pb.RegisterDaprInternalServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := daprinternal_pb.NewDaprInternalClient(conn)
	request := &daprinternal_pb.CallActorEnvelope{
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
	spec := config.TracingSpec{Enabled: true}

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
		daprinternal_pb.RegisterDaprInternalServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := daprinternal_pb.NewDaprInternalClient(conn)
	request := &daprinternal_pb.LocalCallEnvelope{
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
		dapr_pb.RegisterDaprServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := dapr_pb.NewDaprClient(conn)
	request := &dapr_pb.SaveStateEnvelope{
		Requests: []*dapr_pb.StateRequest{
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
		dapr_pb.RegisterDaprServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := dapr_pb.NewDaprClient(conn)
	_, err = client.GetState(context.Background(), &dapr_pb.GetStateEnvelope{})
	server.Stop()
	assert.Nil(t, err)
}

func TestDeleteState(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	server := grpc_go.NewServer()
	go func() {
		dapr_pb.RegisterDaprServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := dapr_pb.NewDaprClient(conn)
	_, err = client.DeleteState(context.Background(), &dapr_pb.DeleteStateEnvelope{})
	server.Stop()
	assert.Nil(t, err)
}

func TestPublishTopic(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	server := grpc_go.NewServer()
	go func() {
		dapr_pb.RegisterDaprServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := dapr_pb.NewDaprClient(conn)
	_, err = client.PublishEvent(context.Background(), &dapr_pb.PublishEventEnvelope{})
	server.Stop()
	assert.Nil(t, err)
}

func TestInvokeBinding(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	server := grpc_go.NewServer()
	go func() {
		dapr_pb.RegisterDaprServer(server, &mockGRPCAPI{})
		server.Serve(lis)
	}()

	time.Sleep(5 * time.Second)

	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	defer doClose(t, conn)
	assert.NoError(t, err)

	client := dapr_pb.NewDaprClient(conn)
	_, err = client.InvokeBinding(context.Background(), &dapr_pb.InvokeBindingEnvelope{})
	server.Stop()
	assert.Nil(t, err)
}

func doClose(t *testing.T, c io.Closer) {
	err := c.Close()
	if err != nil {
		assert.Fail(t, fmt.Sprintf("unable to close %s", err))
	}
}
