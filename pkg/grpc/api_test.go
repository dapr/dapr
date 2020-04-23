// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/dapr/components-contrib/exporters"
	"github.com/dapr/components-contrib/exporters/stringexporter"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/logger"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	daprv1pb "github.com/dapr/dapr/pkg/proto/dapr/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/daprinternal/v1"
	"github.com/golang/protobuf/ptypes/any"
	"github.com/golang/protobuf/ptypes/empty"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	grpc_go "google.golang.org/grpc"
)

const maxGRPCServerUptime = 100 * time.Millisecond

type mockGRPCAPI struct {
}

func (m *mockGRPCAPI) CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	return &internalv1pb.InternalInvokeResponse{}, nil
}

func (m *mockGRPCAPI) CallActor(ctx context.Context, in *internalv1pb.CallActorRequest) (*internalv1pb.CallActorResponse, error) {
	return &internalv1pb.CallActorResponse{}, nil
}

func (m *mockGRPCAPI) PublishEvent(ctx context.Context, in *daprv1pb.PublishEventEnvelope) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) InvokeService(ctx context.Context, in *daprv1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error) {
	return &commonv1pb.InvokeResponse{}, nil
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

func configureTestTraceExporter(meta exporters.Metadata) {
	exporter := stringexporter.NewStringExporter(logger.NewLogger("fakeLogger"))
	exporter.Init("fakeID", "fakeAddress", meta)
}

func startTestServerWithTracing(port int) (*grpc_go.Server, *string) {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

	var buffer = ""
	configureTestTraceExporter(exporters.Metadata{
		Buffer: &buffer,
		Properties: map[string]string{
			"Enabled": "true",
		},
	})

	// sampling is always turn on for testing
	spec := config.TracingSpec{SamplingRate: "1"}
	server := grpc_go.NewServer(
		grpc_go.StreamInterceptor(grpc_middleware.ChainStreamServer(diag.TracingGRPCMiddlewareStream(spec))),
		grpc_go.UnaryInterceptor(grpc_middleware.ChainUnaryServer(diag.TracingGRPCMiddlewareUnary(spec))),
	)

	go func() {
		internalv1pb.RegisterDaprInternalServer(server, &mockGRPCAPI{})
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	// wait until server starts
	time.Sleep(maxGRPCServerUptime)

	return server, &buffer
}

func startTestServer(port int) *grpc_go.Server {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

	server := grpc_go.NewServer()
	go func() {
		daprv1pb.RegisterDaprServer(server, &mockGRPCAPI{})
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	// wait until server starts
	time.Sleep(maxGRPCServerUptime)

	return server
}

func createTestClient(port int) *grpc_go.ClientConn {
	var opts []grpc_go.DialOption
	opts = append(opts, grpc_go.WithInsecure())
	conn, err := grpc_go.Dial(fmt.Sprintf("localhost:%d", port), opts...)
	if err != nil {
		panic(err)
	}
	return conn
}

func TestCallActorWithTracing(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server, buffer := startTestServerWithTracing(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := internalv1pb.NewDaprInternalClient(clientConn)
	request := &internalv1pb.CallActorRequest{
		ActorId:   "actor-1",
		ActorType: "test-actor",
		Method:    "what",
	}

	_, err := client.CallActor(context.Background(), request)
	assert.NoError(t, err)
	assert.Equal(t, "0", *buffer, "failed to generate proper traces with actor call")
}

func TestCallRemoteAppWithTracing(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server, buffer := startTestServerWithTracing(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := internalv1pb.NewDaprInternalClient(clientConn)
	request := invokev1.NewInvokeMethodRequest("method").Proto()

	_, err := client.CallLocal(context.Background(), request)
	assert.NoError(t, err)
	assert.Equal(t, "0", *buffer, "failed to generate proper traces with app call")
}

func TestSaveState(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server := startTestServer(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := daprv1pb.NewDaprClient(clientConn)
	request := &daprv1pb.SaveStateEnvelope{
		Requests: []*daprv1pb.StateRequest{
			{
				Key:   "1",
				Value: &any.Any{Value: []byte("2")},
			},
		},
	}

	_, err := client.SaveState(context.Background(), request)
	assert.Nil(t, err)
}

func TestGetState(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server := startTestServer(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := daprv1pb.NewDaprClient(clientConn)
	_, err := client.GetState(context.Background(), &daprv1pb.GetStateEnvelope{})
	assert.Nil(t, err)
}

func TestDeleteState(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server := startTestServer(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := daprv1pb.NewDaprClient(clientConn)
	_, err := client.DeleteState(context.Background(), &daprv1pb.DeleteStateEnvelope{})
	assert.Nil(t, err)
}

func TestPublishTopic(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server := startTestServer(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := daprv1pb.NewDaprClient(clientConn)
	_, err := client.PublishEvent(context.Background(), &daprv1pb.PublishEventEnvelope{})
	assert.Nil(t, err)
}

func TestInvokeBinding(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server := startTestServer(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := daprv1pb.NewDaprClient(clientConn)
	_, err := client.InvokeBinding(context.Background(), &daprv1pb.InvokeBindingEnvelope{})
	assert.Nil(t, err)
}
