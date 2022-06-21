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

package grpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"

	"github.com/dapr/components-contrib/lock"

	"github.com/agrea/ptr"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opencensus.io/trace"
	epb "google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	components_v1alpha "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	state_loader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	runtime_pubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	daprt "github.com/dapr/dapr/pkg/testing"
	testtrace "github.com/dapr/dapr/pkg/testing/trace"
	"github.com/dapr/kit/logger"
)

const (
	maxGRPCServerUptime = 100 * time.Millisecond
	goodStoreKey        = "fakeAPI||good-key"
	errorStoreKey       = "fakeAPI||error-key"
	goodKey             = "good-key"
	goodKey2            = "good-key2"
	mockSubscribeID     = "mockId"
)

var testResiliency = &v1alpha1.Resiliency{
	Spec: v1alpha1.ResiliencySpec{
		Policies: v1alpha1.Policies{
			Retries: map[string]v1alpha1.Retry{
				"singleRetry": {
					MaxRetries:  1,
					MaxInterval: "100ms",
					Policy:      "constant",
					Duration:    "10ms",
				},
				"tenRetries": {
					MaxRetries:  10,
					MaxInterval: "100ms",
					Policy:      "constant",
					Duration:    "10ms",
				},
			},
			Timeouts: map[string]string{
				"fast": "100ms",
			},
			CircuitBreakers: map[string]v1alpha1.CircuitBreaker{
				"simpleCB": {
					MaxRequests: 1,
					Timeout:     "1s",
					Trip:        "consecutiveFailures > 4",
				},
			},
		},
		Targets: v1alpha1.Targets{
			Apps: map[string]v1alpha1.EndpointPolicyNames{
				"failingApp": {
					Retry:   "singleRetry",
					Timeout: "fast",
				},
				"circuitBreakerApp": {
					Retry:          "tenRetries",
					CircuitBreaker: "simpleCB",
				},
			},
			Components: map[string]v1alpha1.ComponentPolicyNames{
				"failSecret": {
					Outbound: v1alpha1.PolicyNames{
						Retry:   "singleRetry",
						Timeout: "fast",
					},
				},
				"failStore": {
					Outbound: v1alpha1.PolicyNames{
						Retry:   "singleRetry",
						Timeout: "fast",
					},
				},
				"failConfig": {
					Outbound: v1alpha1.PolicyNames{
						Retry:   "singleRetry",
						Timeout: "fast",
					},
				},
			},
		},
	},
}

type mockGRPCAPI struct{}

func (m *mockGRPCAPI) CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	resp := invokev1.NewInvokeMethodResponse(0, "", nil)
	resp.WithRawData(ExtractSpanContext(ctx), "text/plains")
	return resp.Proto(), nil
}

func (m *mockGRPCAPI) CallActor(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	resp := invokev1.NewInvokeMethodResponse(0, "", nil)
	resp.WithRawData(ExtractSpanContext(ctx), "text/plains")
	return resp.Proto(), nil
}

func (m *mockGRPCAPI) PublishEvent(ctx context.Context, in *runtimev1pb.PublishEventRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (m *mockGRPCAPI) InvokeService(ctx context.Context, in *runtimev1pb.InvokeServiceRequest) (*commonv1pb.InvokeResponse, error) {
	return &commonv1pb.InvokeResponse{}, nil
}

func (m *mockGRPCAPI) InvokeBinding(ctx context.Context, in *runtimev1pb.InvokeBindingRequest) (*runtimev1pb.InvokeBindingResponse, error) {
	return &runtimev1pb.InvokeBindingResponse{}, nil
}

func (m *mockGRPCAPI) GetState(ctx context.Context, in *runtimev1pb.GetStateRequest) (*runtimev1pb.GetStateResponse, error) {
	return &runtimev1pb.GetStateResponse{}, nil
}

func (m *mockGRPCAPI) GetBulkState(ctx context.Context, in *runtimev1pb.GetBulkStateRequest) (*runtimev1pb.GetBulkStateResponse, error) {
	return &runtimev1pb.GetBulkStateResponse{}, nil
}

func (m *mockGRPCAPI) SaveState(ctx context.Context, in *runtimev1pb.SaveStateRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (m *mockGRPCAPI) QueryStateAlpha1(ctx context.Context, in *runtimev1pb.QueryStateRequest) (*runtimev1pb.QueryStateResponse, error) {
	return &runtimev1pb.QueryStateResponse{}, nil
}

func (m *mockGRPCAPI) DeleteState(ctx context.Context, in *runtimev1pb.DeleteStateRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (m *mockGRPCAPI) GetSecret(ctx context.Context, in *runtimev1pb.GetSecretRequest) (*runtimev1pb.GetSecretResponse, error) {
	return &runtimev1pb.GetSecretResponse{}, nil
}

func (m *mockGRPCAPI) ExecuteStateTransaction(ctx context.Context, in *runtimev1pb.ExecuteStateTransactionRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (m *mockGRPCAPI) RegisterActorTimer(ctx context.Context, in *runtimev1pb.RegisterActorTimerRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func ExtractSpanContext(ctx context.Context) []byte {
	span := diag_utils.SpanFromContext(ctx)
	return []byte(SerializeSpanContext(span.SpanContext()))
}

// SerializeSpanContext serializes a span context into a simple string.
func SerializeSpanContext(ctx trace.SpanContext) string {
	return fmt.Sprintf("%s;%s;%d", ctx.SpanID.String(), ctx.TraceID.String(), ctx.TraceOptions)
}

func configureTestTraceExporter(buffer *string) {
	exporter := testtrace.NewStringExporter(buffer, logger.NewLogger("fakeLogger"))
	exporter.Register("fakeID")
}

func startTestServerWithTracing(port int) (*grpc.Server, *string) {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

	buffer := ""
	configureTestTraceExporter(&buffer)

	spec := config.TracingSpec{SamplingRate: "1"}
	server := grpc.NewServer(
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(diag.GRPCTraceUnaryServerInterceptor("id", spec))),
	)

	go func() {
		internalv1pb.RegisterServiceInvocationServer(server, &mockGRPCAPI{})
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	// wait until server starts
	time.Sleep(maxGRPCServerUptime)

	return server, &buffer
}

func startTestServerAPI(port int, srv runtimev1pb.DaprServer) *grpc.Server {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

	server := grpc.NewServer()
	go func() {
		runtimev1pb.RegisterDaprServer(server, srv)
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	// wait until server starts
	time.Sleep(maxGRPCServerUptime)

	return server
}

func startInternalServer(port int, testAPIServer *api) *grpc.Server {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

	server := grpc.NewServer()
	go func() {
		internalv1pb.RegisterServiceInvocationServer(server, testAPIServer)
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	// wait until server starts
	time.Sleep(maxGRPCServerUptime)

	return server
}

func startDaprAPIServer(port int, testAPIServer *api, token string) *grpc.Server {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

	opts := []grpc.ServerOption{}
	if token != "" {
		opts = append(opts,
			grpc.UnaryInterceptor(setAPIAuthenticationMiddlewareUnary(token, "dapr-api-token")),
		)
	}

	server := grpc.NewServer(opts...)
	go func() {
		runtimev1pb.RegisterDaprServer(server, testAPIServer)
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	// wait until server starts
	time.Sleep(maxGRPCServerUptime)

	return server
}

func createTestClient(port int) *grpc.ClientConn {
	conn, err := grpc.Dial(fmt.Sprintf("localhost:%d", port), grpc.WithInsecure())
	if err != nil {
		panic(err)
	}
	return conn
}

func TestCallActorWithTracing(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server, _ := startTestServerWithTracing(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := internalv1pb.NewServiceInvocationClient(clientConn)

	request := invokev1.NewInvokeMethodRequest("method")
	request.WithActor("test-actor", "actor-1")

	resp, err := client.CallActor(context.Background(), request.Proto())
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.GetMessage(), "failed to generate trace context with actor call")
}

func TestCallRemoteAppWithTracing(t *testing.T) {
	port, _ := freeport.GetFreePort()

	server, _ := startTestServerWithTracing(port)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := internalv1pb.NewServiceInvocationClient(clientConn)
	request := invokev1.NewInvokeMethodRequest("method").Proto()

	resp, err := client.CallLocal(context.Background(), request)
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.GetMessage(), "failed to generate trace context with app call")
}

func TestCallLocal(t *testing.T) {
	t.Run("appchannel is not ready", func(t *testing.T) {
		port, _ := freeport.GetFreePort()

		fakeAPI := &api{
			id:         "fakeAPI",
			appChannel: nil,
		}
		server := startInternalServer(port, fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := internalv1pb.NewServiceInvocationClient(clientConn)
		request := invokev1.NewInvokeMethodRequest("method").Proto()

		_, err := client.CallLocal(context.Background(), request)
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("parsing InternalInvokeRequest is failed", func(t *testing.T) {
		port, _ := freeport.GetFreePort()

		mockAppChannel := new(channelt.MockAppChannel)
		fakeAPI := &api{
			id:         "fakeAPI",
			appChannel: mockAppChannel,
		}
		server := startInternalServer(port, fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := internalv1pb.NewServiceInvocationClient(clientConn)
		request := &internalv1pb.InternalInvokeRequest{
			Message: nil,
		}

		_, err := client.CallLocal(context.Background(), request)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("invokemethod returns error", func(t *testing.T) {
		port, _ := freeport.GetFreePort()

		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(nil, status.Error(codes.Unknown, "unknown error"))
		fakeAPI := &api{
			id:         "fakeAPI",
			appChannel: mockAppChannel,
		}
		server := startInternalServer(port, fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := internalv1pb.NewServiceInvocationClient(clientConn)
		request := invokev1.NewInvokeMethodRequest("method").Proto()

		_, err := client.CallLocal(context.Background(), request)
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func mustMarshalAny(msg proto.Message) *anypb.Any {
	any, err := anypb.New(msg)
	if err != nil {
		panic(fmt.Sprintf("anypb.New((%+v) failed: %v", msg, err))
	}
	return any
}

func TestAPIToken(t *testing.T) {
	mockDirectMessaging := new(daprt.MockDirectMessaging)

	// Setup Dapr API server
	fakeAPI := &api{
		id:              "fakeAPI",
		directMessaging: mockDirectMessaging,
		resiliency:      resiliency.New(nil),
	}

	t.Run("valid token", func(t *testing.T) {
		token := "1234"

		fakeResp := invokev1.NewInvokeMethodResponse(404, "NotFound", nil)
		fakeResp.WithRawData([]byte("fakeDirectMessageResponse"), "application/json")

		// Set up direct messaging mock
		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.AnythingOfType("*context.valueCtx"),
			"fakeAppID",
			mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil).Once()

		// Run test server
		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, fakeAPI, token)
		defer server.Stop()

		// Create gRPC test client
		clientConn := createTestClient(port)
		defer clientConn.Close()

		// act
		client := runtimev1pb.NewDaprClient(clientConn)
		req := &runtimev1pb.InvokeServiceRequest{
			Id: "fakeAppID",
			Message: &commonv1pb.InvokeRequest{
				Method: "fakeMethod",
				Data:   &anypb.Any{Value: []byte("testData")},
			},
		}
		md := metadata.Pairs("dapr-api-token", token)
		ctx := metadata.NewOutgoingContext(context.Background(), md)
		_, err := client.InvokeService(ctx, req)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		s, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.NotFound, s.Code())
		assert.Equal(t, "Not Found", s.Message())

		errInfo := s.Details()[0].(*epb.ErrorInfo)
		assert.Equal(t, 1, len(s.Details()))
		assert.Equal(t, "404", errInfo.Metadata["http.code"])
		assert.Equal(t, "fakeDirectMessageResponse", errInfo.Metadata["http.error_message"])
	})

	t.Run("invalid token", func(t *testing.T) {
		token := "1234"

		fakeResp := invokev1.NewInvokeMethodResponse(404, "NotFound", nil)
		fakeResp.WithRawData([]byte("fakeDirectMessageResponse"), "application/json")

		// Set up direct messaging mock
		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.AnythingOfType("*context.valueCtx"),
			"fakeAppID",
			mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil).Once()

		// Run test server
		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, fakeAPI, token)
		defer server.Stop()

		// Create gRPC test client
		clientConn := createTestClient(port)
		defer clientConn.Close()

		// act
		client := runtimev1pb.NewDaprClient(clientConn)
		req := &runtimev1pb.InvokeServiceRequest{
			Id: "fakeAppID",
			Message: &commonv1pb.InvokeRequest{
				Method: "fakeMethod",
				Data:   &anypb.Any{Value: []byte("testData")},
			},
		}
		md := metadata.Pairs("dapr-api-token", "4567")
		ctx := metadata.NewOutgoingContext(context.Background(), md)
		_, err := client.InvokeService(ctx, req)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 0)
		s, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, s.Code())
	})

	t.Run("missing token", func(t *testing.T) {
		token := "1234"

		fakeResp := invokev1.NewInvokeMethodResponse(404, "NotFound", nil)
		fakeResp.WithRawData([]byte("fakeDirectMessageResponse"), "application/json")

		// Set up direct messaging mock
		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.AnythingOfType("*context.valueCtx"),
			"fakeAppID",
			mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil).Once()

		// Run test server
		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, fakeAPI, token)
		defer server.Stop()

		// Create gRPC test client
		clientConn := createTestClient(port)
		defer clientConn.Close()

		// act
		client := runtimev1pb.NewDaprClient(clientConn)
		req := &runtimev1pb.InvokeServiceRequest{
			Id: "fakeAppID",
			Message: &commonv1pb.InvokeRequest{
				Method: "fakeMethod",
				Data:   &anypb.Any{Value: []byte("testData")},
			},
		}
		_, err := client.InvokeService(context.Background(), req)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 0)
		s, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, s.Code())
	})
}

func TestInvokeServiceFromHTTPResponse(t *testing.T) {
	mockDirectMessaging := new(daprt.MockDirectMessaging)

	// Setup Dapr API server
	fakeAPI := &api{
		id:              "fakeAPI",
		directMessaging: mockDirectMessaging,
		resiliency:      resiliency.New(nil),
	}

	httpResponseTests := []struct {
		status         int
		statusMessage  string
		grpcStatusCode codes.Code
		grpcMessage    string
		errHTTPCode    string
		errHTTPMessage string
	}{
		{
			status:         200,
			statusMessage:  "OK",
			grpcStatusCode: codes.OK,
			grpcMessage:    "",
			errHTTPCode:    "",
			errHTTPMessage: "",
		},
		{
			status:         201,
			statusMessage:  "Accepted",
			grpcStatusCode: codes.OK,
			grpcMessage:    "",
			errHTTPCode:    "",
			errHTTPMessage: "",
		},
		{
			status:         204,
			statusMessage:  "No Content",
			grpcStatusCode: codes.OK,
			grpcMessage:    "",
			errHTTPCode:    "",
			errHTTPMessage: "",
		},
		{
			status:         404,
			statusMessage:  "NotFound",
			grpcStatusCode: codes.NotFound,
			grpcMessage:    "Not Found",
			errHTTPCode:    "404",
			errHTTPMessage: "fakeDirectMessageResponse",
		},
	}

	for _, tt := range httpResponseTests {
		t.Run(fmt.Sprintf("handle http %d response code", tt.status), func(t *testing.T) {
			fakeResp := invokev1.NewInvokeMethodResponse(int32(tt.status), tt.statusMessage, nil)
			fakeResp.WithRawData([]byte(tt.errHTTPMessage), "application/json")

			// Set up direct messaging mock
			mockDirectMessaging.Calls = nil // reset call count
			mockDirectMessaging.On("Invoke",
				mock.AnythingOfType("*context.valueCtx"),
				"fakeAppID",
				mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil).Once()

			// Run test server
			port, _ := freeport.GetFreePort()
			server := startDaprAPIServer(port, fakeAPI, "")
			defer server.Stop()

			// Create gRPC test client
			clientConn := createTestClient(port)
			defer clientConn.Close()

			// act
			client := runtimev1pb.NewDaprClient(clientConn)
			req := &runtimev1pb.InvokeServiceRequest{
				Id: "fakeAppID",
				Message: &commonv1pb.InvokeRequest{
					Method: "fakeMethod",
					Data:   &anypb.Any{Value: []byte("testData")},
				},
			}
			var header metadata.MD
			_, err := client.InvokeService(context.Background(), req, grpc.Header(&header))

			// assert
			mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
			s, ok := status.FromError(err)
			assert.True(t, ok)
			statusHeader := header.Get(daprHTTPStatusHeader)
			assert.Equal(t, strconv.Itoa(tt.status), statusHeader[0])
			assert.Equal(t, tt.grpcStatusCode, s.Code())
			assert.Equal(t, tt.grpcMessage, s.Message())

			if tt.errHTTPCode != "" {
				errInfo := s.Details()[0].(*epb.ErrorInfo)
				assert.Equal(t, 1, len(s.Details()))
				assert.Equal(t, tt.errHTTPCode, errInfo.Metadata["http.code"])
				assert.Equal(t, tt.errHTTPMessage, errInfo.Metadata["http.error_message"])
			}
		})
	}
}

func TestInvokeServiceFromGRPCResponse(t *testing.T) {
	mockDirectMessaging := new(daprt.MockDirectMessaging)

	// Setup Dapr API server
	fakeAPI := &api{
		id:              "fakeAPI",
		directMessaging: mockDirectMessaging,
		resiliency:      resiliency.New(nil),
	}

	t.Run("handle grpc response code", func(t *testing.T) {
		fakeResp := invokev1.NewInvokeMethodResponse(
			int32(codes.Unimplemented), "Unimplemented",
			[]*anypb.Any{
				mustMarshalAny(&epb.ResourceInfo{
					ResourceType: "sidecar",
					ResourceName: "invoke/service",
					Owner:        "Dapr",
				}),
			},
		)
		fakeResp.WithRawData([]byte("fakeDirectMessageResponse"), "application/json")

		// Set up direct messaging mock
		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.AnythingOfType("*context.valueCtx"),
			"fakeAppID",
			mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil).Once()

		// Run test server
		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, fakeAPI, "")
		defer server.Stop()

		// Create gRPC test client
		clientConn := createTestClient(port)
		defer clientConn.Close()

		// act
		client := runtimev1pb.NewDaprClient(clientConn)
		req := &runtimev1pb.InvokeServiceRequest{
			Id: "fakeAppID",
			Message: &commonv1pb.InvokeRequest{
				Method: "fakeMethod",
				Data:   &anypb.Any{Value: []byte("testData")},
			},
		}
		_, err := client.InvokeService(context.Background(), req)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		s, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unimplemented, s.Code())
		assert.Equal(t, "Unimplemented", s.Message())

		errInfo := s.Details()[0].(*epb.ResourceInfo)
		assert.Equal(t, 1, len(s.Details()))
		assert.Equal(t, "sidecar", errInfo.GetResourceType())
		assert.Equal(t, "invoke/service", errInfo.GetResourceName())
		assert.Equal(t, "Dapr", errInfo.GetOwner())
	})
}

func TestSecretStoreNotConfigured(t *testing.T) {
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, &api{id: "fakeAPI"}, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err := client.GetSecret(context.Background(), &runtimev1pb.GetSecretRequest{})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestGetSecret(t *testing.T) {
	fakeStore := daprt.FakeSecretStore{}
	fakeStores := map[string]secretstores.SecretStore{
		"store1": fakeStore,
		"store2": fakeStore,
		"store3": fakeStore,
		"store4": fakeStore,
	}
	secretsConfiguration := map[string]config.SecretsScope{
		"store1": {
			DefaultAccess: config.AllowAccess,
			DeniedSecrets: []string{"not-allowed"},
		},
		"store2": {
			DefaultAccess:  config.DenyAccess,
			AllowedSecrets: []string{goodKey},
		},
		"store3": {
			DefaultAccess:  config.AllowAccess,
			AllowedSecrets: []string{"error-key", goodKey},
		},
	}
	expectedResponse := "life is good"
	storeName := "store1"
	deniedStoreName := "store2"
	restrictedStore := "store3"
	unrestrictedStore := "store4"     // No configuration defined for the store
	nonExistingStore := "nonexistent" // Non-existing store

	testCases := []struct {
		testName         string
		storeName        string
		key              string
		errorExcepted    bool
		expectedResponse string
		expectedError    codes.Code
	}{
		{
			testName:         "Good Key from unrestricted store",
			storeName:        unrestrictedStore,
			key:              goodKey,
			errorExcepted:    false,
			expectedResponse: expectedResponse,
		},
		{
			testName:         "Good Key default access",
			storeName:        storeName,
			key:              goodKey,
			errorExcepted:    false,
			expectedResponse: expectedResponse,
		},
		{
			testName:         "Good Key restricted store access",
			storeName:        restrictedStore,
			key:              goodKey,
			errorExcepted:    false,
			expectedResponse: expectedResponse,
		},
		{
			testName:         "Error Key restricted store access",
			storeName:        restrictedStore,
			key:              "error-key",
			errorExcepted:    true,
			expectedResponse: "",
			expectedError:    codes.Internal,
		},
		{
			testName:         "Random Key restricted store access",
			storeName:        restrictedStore,
			key:              "random",
			errorExcepted:    true,
			expectedResponse: "",
			expectedError:    codes.PermissionDenied,
		},
		{
			testName:         "Random Key accessing a store denied access by default",
			storeName:        deniedStoreName,
			key:              "random",
			errorExcepted:    true,
			expectedResponse: "",
			expectedError:    codes.PermissionDenied,
		},
		{
			testName:         "Random Key accessing a store denied access by default",
			storeName:        deniedStoreName,
			key:              "random",
			errorExcepted:    true,
			expectedResponse: "",
			expectedError:    codes.PermissionDenied,
		},
		{
			testName:         "Store doesn't exist",
			storeName:        nonExistingStore,
			key:              "key",
			errorExcepted:    true,
			expectedResponse: "",
			expectedError:    codes.InvalidArgument,
		},
	}
	// Setup Dapr API server
	fakeAPI := &api{
		id:                   "fakeAPI",
		secretStores:         fakeStores,
		secretsConfiguration: secretsConfiguration,
		resiliency:           resiliency.New(nil),
	}
	// Run test server
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	// Create gRPC test client
	clientConn := createTestClient(port)
	defer clientConn.Close()

	// act
	client := runtimev1pb.NewDaprClient(clientConn)

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetSecretRequest{
				StoreName: tt.storeName,
				Key:       tt.key,
			}
			resp, err := client.GetSecret(context.Background(), req)

			if !tt.errorExcepted {
				assert.NoError(t, err, "Expected no error")
				assert.Equal(t, resp.Data[tt.key], tt.expectedResponse, "Expected responses to be same")
			} else {
				assert.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestGetBulkSecret(t *testing.T) {
	fakeStore := daprt.FakeSecretStore{}
	fakeStores := map[string]secretstores.SecretStore{
		"store1": fakeStore,
	}
	secretsConfiguration := map[string]config.SecretsScope{
		"store1": {
			DefaultAccess: config.AllowAccess,
			DeniedSecrets: []string{"not-allowed"},
		},
	}
	expectedResponse := "life is good"

	testCases := []struct {
		testName         string
		storeName        string
		key              string
		errorExcepted    bool
		expectedResponse string
		expectedError    codes.Code
	}{
		{
			testName:         "Good Key from unrestricted store",
			storeName:        "store1",
			key:              goodKey,
			errorExcepted:    false,
			expectedResponse: expectedResponse,
		},
	}
	// Setup Dapr API server
	fakeAPI := &api{
		id:                   "fakeAPI",
		secretStores:         fakeStores,
		secretsConfiguration: secretsConfiguration,
		resiliency:           resiliency.New(nil),
	}
	// Run test server
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	// Create gRPC test client
	clientConn := createTestClient(port)
	defer clientConn.Close()

	// act
	client := runtimev1pb.NewDaprClient(clientConn)

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetBulkSecretRequest{
				StoreName: tt.storeName,
			}
			resp, err := client.GetBulkSecret(context.Background(), req)

			if !tt.errorExcepted {
				assert.NoError(t, err, "Expected no error")
				assert.Equal(t, resp.Data[tt.key].Secrets[tt.key], tt.expectedResponse, "Expected responses to be same")
			} else {
				assert.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestGetStateWhenStoreNotConfigured(t *testing.T) {
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, &api{id: "fakeAPI", resiliency: resiliency.New(nil)}, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err := client.GetState(context.Background(), &runtimev1pb.GetStateRequest{})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestSaveState(t *testing.T) {
	fakeStore := &daprt.MockStateStore{}
	fakeStore.On("BulkSet", mock.MatchedBy(func(reqs []state.SetRequest) bool {
		if len(reqs) == 0 {
			return false
		}
		return reqs[0].Key == goodStoreKey
	})).Return(nil)
	fakeStore.On("BulkSet", mock.MatchedBy(func(reqs []state.SetRequest) bool {
		if len(reqs) == 0 {
			return false
		}
		return reqs[0].Key == errorStoreKey
	})).Return(errors.New("failed to save state with error-key"))

	// Setup dapr api server
	fakeAPI := &api{
		id:          "fakeAPI",
		stateStores: map[string]state.Store{"store1": fakeStore},
		resiliency:  resiliency.New(nil),
	}
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	// create client
	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName      string
		storeName     string
		key           string
		value         string
		errorExcepted bool
		expectedError codes.Code
	}{
		{
			testName:      "save state",
			storeName:     "store1",
			key:           goodKey,
			value:         "value",
			errorExcepted: false,
			expectedError: codes.OK,
		},
		{
			testName:      "save state with non-existing store",
			storeName:     "store2",
			key:           goodKey,
			value:         "value",
			errorExcepted: true,
			expectedError: codes.InvalidArgument,
		},
		{
			testName:      "save state but error occurs",
			storeName:     "store1",
			key:           "error-key",
			value:         "value",
			errorExcepted: true,
			expectedError: codes.Internal,
		},
	}

	// test and assert
	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.SaveStateRequest{
				StoreName: tt.storeName,
				States: []*commonv1pb.StateItem{
					{
						Key:   tt.key,
						Value: []byte(tt.value),
					},
				},
			}

			_, err := client.SaveState(context.Background(), req)
			if !tt.errorExcepted {
				assert.NoError(t, err, "Expected no error")
			} else {
				assert.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestGetState(t *testing.T) {
	// Setup mock store
	fakeStore := &daprt.MockStateStore{}
	fakeStore.On("Get", mock.MatchedBy(func(req *state.GetRequest) bool {
		return req.Key == goodStoreKey
	})).Return(
		&state.GetResponse{
			Data: []byte("test-data"),
			ETag: ptr.String("test-etag"),
		}, nil)
	fakeStore.On("Get", mock.MatchedBy(func(req *state.GetRequest) bool {
		return req.Key == errorStoreKey
	})).Return(
		nil,
		errors.New("failed to get state with error-key"))

	fakeAPI := &api{
		id:          "fakeAPI",
		stateStores: map[string]state.Store{"store1": fakeStore},
		resiliency:  resiliency.New(nil),
	}
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName         string
		storeName        string
		key              string
		errorExcepted    bool
		expectedResponse *runtimev1pb.GetStateResponse
		expectedError    codes.Code
	}{
		{
			testName:      "get state",
			storeName:     "store1",
			key:           goodKey,
			errorExcepted: false,
			expectedResponse: &runtimev1pb.GetStateResponse{
				Data: []byte("test-data"),
				Etag: "test-etag",
			},
			expectedError: codes.OK,
		},
		{
			testName:         "get store with non-existing store",
			storeName:        "no-store",
			key:              goodKey,
			errorExcepted:    true,
			expectedResponse: &runtimev1pb.GetStateResponse{},
			expectedError:    codes.InvalidArgument,
		},
		{
			testName:         "get store with key but error occurs",
			storeName:        "store1",
			key:              "error-key",
			errorExcepted:    true,
			expectedResponse: &runtimev1pb.GetStateResponse{},
			expectedError:    codes.Internal,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetStateRequest{
				StoreName: tt.storeName,
				Key:       tt.key,
			}

			resp, err := client.GetState(context.Background(), req)
			if !tt.errorExcepted {
				assert.NoError(t, err, "Expected no error")
				assert.Equal(t, resp.Data, tt.expectedResponse.Data, "Expected response Data to be same")
				assert.Equal(t, resp.Etag, tt.expectedResponse.Etag, "Expected response Etag to be same")
			} else {
				assert.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestGetConfiguration(t *testing.T) {
	fakeConfigurationStore := &daprt.MockConfigurationStore{}
	fakeConfigurationStore.On("Get",
		mock.AnythingOfType("*context.valueCtx"),
		mock.MatchedBy(func(req *configuration.GetRequest) bool {
			return req.Keys[0] == goodKey
		})).Return(
		&configuration.GetResponse{
			Items: []*configuration.Item{
				{
					Key:   goodKey,
					Value: "test-data",
				},
			},
		}, nil)
	fakeConfigurationStore.On("Get",
		mock.AnythingOfType("*context.valueCtx"),
		mock.MatchedBy(func(req *configuration.GetRequest) bool {
			return req.Keys[0] == "good-key1" && req.Keys[1] == goodKey2 && req.Keys[2] == "good-key3"
		})).Return(
		&configuration.GetResponse{
			Items: []*configuration.Item{
				{
					Key:   "good-key1",
					Value: "test-data",
				},
				{
					Key:   goodKey2,
					Value: "test-data",
				},
				{
					Key:   "good-key3",
					Value: "test-data",
				},
			},
		}, nil)
	fakeConfigurationStore.On("Get",
		mock.AnythingOfType("*context.valueCtx"),
		mock.MatchedBy(func(req *configuration.GetRequest) bool {
			return req.Keys[0] == "error-key"
		})).Return(
		nil,
		errors.New("failed to get state with error-key"))
	fakeAPI := &api{
		id:                  "fakeAPI",
		configurationStores: map[string]configuration.Store{"store1": fakeConfigurationStore},
		resiliency:          resiliency.New(nil),
	}
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName         string
		storeName        string
		keys             []string
		errorExcepted    bool
		expectedResponse *runtimev1pb.GetConfigurationResponse
		expectedError    codes.Code
	}{
		{
			testName:      "get state",
			storeName:     "store1",
			keys:          []string{goodKey},
			errorExcepted: false,
			expectedResponse: &runtimev1pb.GetConfigurationResponse{
				Items: []*commonv1pb.ConfigurationItem{
					{
						Key:   goodKey,
						Value: "test-data",
					},
				},
			},
			expectedError: codes.OK,
		},
		{
			testName:      "get state",
			storeName:     "store1",
			keys:          []string{"good-key1", goodKey2, "good-key3"},
			errorExcepted: false,
			expectedResponse: &runtimev1pb.GetConfigurationResponse{
				Items: []*commonv1pb.ConfigurationItem{
					{
						Key:   "good-key1",
						Value: "test-data",
					},
					{
						Key:   goodKey2,
						Value: "test-data",
					},
					{
						Key:   "good-key3",
						Value: "test-data",
					},
				},
			},
			expectedError: codes.OK,
		},
		{
			testName:         "get store with non-existing store",
			storeName:        "no-store",
			keys:             []string{goodKey},
			errorExcepted:    true,
			expectedResponse: &runtimev1pb.GetConfigurationResponse{},
			expectedError:    codes.InvalidArgument,
		},
		{
			testName:         "get store with key but error occurs",
			storeName:        "store1",
			keys:             []string{"error-key"},
			errorExcepted:    true,
			expectedResponse: &runtimev1pb.GetConfigurationResponse{},
			expectedError:    codes.Internal,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetConfigurationRequest{
				StoreName: tt.storeName,
				Keys:      tt.keys,
			}

			resp, err := client.GetConfigurationAlpha1(context.Background(), req)
			if !tt.errorExcepted {
				assert.NoError(t, err, "Expected no error")
				assert.Equal(t, resp.Items, tt.expectedResponse.Items, "Expected response items to be same")
			} else {
				assert.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestSubscribeConfiguration(t *testing.T) {
	fakeConfigurationStore := &daprt.MockConfigurationStore{}
	var tempReq *configuration.SubscribeRequest
	fakeConfigurationStore.On("Subscribe",
		mock.AnythingOfType("*context.cancelCtx"),
		mock.MatchedBy(func(req *configuration.SubscribeRequest) bool {
			tempReq = req
			return len(tempReq.Keys) == 1 && tempReq.Keys[0] == goodKey
		}),
		mock.MatchedBy(func(f configuration.UpdateHandler) bool {
			if len(tempReq.Keys) == 1 && tempReq.Keys[0] == goodKey {
				_ = f(context.Background(), &configuration.UpdateEvent{
					Items: []*configuration.Item{
						{
							Key:   goodKey,
							Value: "test-data",
						},
					},
				})
			}
			return true
		})).Return("id", nil)
	fakeConfigurationStore.On("Subscribe",
		mock.AnythingOfType("*context.cancelCtx"),
		mock.MatchedBy(func(req *configuration.SubscribeRequest) bool {
			tempReq = req
			return len(req.Keys) == 2 && req.Keys[0] == goodKey && req.Keys[1] == goodKey2
		}),
		mock.MatchedBy(func(f configuration.UpdateHandler) bool {
			if len(tempReq.Keys) == 2 && tempReq.Keys[0] == goodKey && tempReq.Keys[1] == goodKey2 {
				_ = f(context.Background(), &configuration.UpdateEvent{
					Items: []*configuration.Item{
						{
							Key:   goodKey,
							Value: "test-data",
						},
						{
							Key:   goodKey2,
							Value: "test-data2",
						},
					},
				})
			}
			return true
		})).Return("id", nil)
	fakeConfigurationStore.On("Subscribe",
		mock.AnythingOfType("*context.cancelCtx"),
		mock.MatchedBy(func(req *configuration.SubscribeRequest) bool {
			return req.Keys[0] == "error-key"
		}),
		mock.AnythingOfType("configuration.UpdateHandler")).Return(nil, errors.New("failed to get state with error-key"))

	fakeAPI := &api{
		configurationSubscribe: make(map[string]chan struct{}),
		id:                     "fakeAPI",
		configurationStores:    map[string]configuration.Store{"store1": fakeConfigurationStore},
		resiliency:             resiliency.New(nil),
	}
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName         string
		storeName        string
		keys             []string
		errorExcepted    bool
		expectedResponse []*commonv1pb.ConfigurationItem
		expectedError    codes.Code
	}{
		{
			testName:      "get store with single key",
			storeName:     "store1",
			keys:          []string{goodKey},
			errorExcepted: false,
			expectedResponse: []*commonv1pb.ConfigurationItem{
				{
					Key:   goodKey,
					Value: "test-data",
				},
			},
			expectedError: codes.OK,
		},
		{
			testName:         "get store with non-existing store",
			storeName:        "no-store",
			keys:             []string{goodKey},
			errorExcepted:    true,
			expectedResponse: []*commonv1pb.ConfigurationItem{},
			expectedError:    codes.InvalidArgument,
		},
		{
			testName:         "get store with key but error occurs",
			storeName:        "store1",
			keys:             []string{"error-key"},
			errorExcepted:    true,
			expectedResponse: []*commonv1pb.ConfigurationItem{},
			expectedError:    codes.InvalidArgument,
		},
		{
			testName:      "get store with multi keys",
			storeName:     "store1",
			keys:          []string{goodKey, goodKey2},
			errorExcepted: false,
			expectedResponse: []*commonv1pb.ConfigurationItem{
				{
					Key:   goodKey,
					Value: "test-data",
				},
				{
					Key:   goodKey2,
					Value: "test-data2",
				},
			},
			expectedError: codes.OK,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.SubscribeConfigurationRequest{
				StoreName: tt.storeName,
				Keys:      tt.keys,
			}

			resp, _ := client.SubscribeConfigurationAlpha1(context.Background(), req)
			if !tt.errorExcepted {
				rsp, err := resp.Recv()
				assert.NoError(t, err, "Expected no error")
				assert.Equal(t, rsp.Items, tt.expectedResponse, "Expected response items to be same")
			} else {
				retry := 3
				count := 0
				_, err := resp.Recv()
				for {
					if err != nil {
						break
					}
					if count > retry {
						break
					}
					count++
					time.Sleep(time.Millisecond * 10)
					_, err = resp.Recv()
				}
				assert.Equal(t, tt.expectedError, status.Code(err))
				assert.Error(t, err, "Expected error")
			}
		})
	}
}

func TestUnSubscribeConfiguration(t *testing.T) {
	fakeConfigurationStore := &daprt.MockConfigurationStore{}
	stop := make(chan struct{})
	defer close(stop)
	var tempReq *configuration.SubscribeRequest
	fakeConfigurationStore.On("Unsubscribe",
		mock.AnythingOfType("*context.valueCtx"),
		mock.MatchedBy(func(req *configuration.UnsubscribeRequest) bool {
			return true
		})).Return(nil)
	fakeConfigurationStore.On("Subscribe",
		mock.AnythingOfType("*context.cancelCtx"),
		mock.MatchedBy(func(req *configuration.SubscribeRequest) bool {
			tempReq = req
			return len(req.Keys) == 1 && req.Keys[0] == goodKey
		}),
		mock.MatchedBy(func(f configuration.UpdateHandler) bool {
			if !(len(tempReq.Keys) == 1 && tempReq.Keys[0] == goodKey) {
				return true
			}
			go func() {
				for {
					select {
					case <-stop:
						return
					default:
					}
					if err := f(context.Background(), &configuration.UpdateEvent{
						Items: []*configuration.Item{
							{
								Key:   goodKey,
								Value: "test-data",
							},
						},
						ID: mockSubscribeID,
					}); err != nil {
						return
					}
					time.Sleep(time.Millisecond * 10)
				}
			}()
			return true
		})).Return(mockSubscribeID, nil)
	fakeConfigurationStore.On("Subscribe",
		mock.AnythingOfType("*context.cancelCtx"),
		mock.MatchedBy(func(req *configuration.SubscribeRequest) bool {
			tempReq = req
			return len(req.Keys) == 2 && req.Keys[0] == goodKey && req.Keys[1] == goodKey2
		}),
		mock.MatchedBy(func(f configuration.UpdateHandler) bool {
			if !(len(tempReq.Keys) == 2 && tempReq.Keys[0] == goodKey && tempReq.Keys[1] == goodKey2) {
				return true
			}
			go func() {
				for {
					select {
					case <-stop:
						return
					default:
					}
					if err := f(context.Background(), &configuration.UpdateEvent{
						Items: []*configuration.Item{
							{
								Key:   goodKey,
								Value: "test-data",
							},
							{
								Key:   goodKey2,
								Value: "test-data2",
							},
						},
						ID: mockSubscribeID,
					}); err != nil {
						return
					}
					time.Sleep(time.Millisecond * 10)
				}
			}()
			return true
		})).Return(mockSubscribeID, nil)

	fakeAPI := &api{
		configurationSubscribe: make(map[string]chan struct{}),
		id:                     "fakeAPI",
		configurationStores:    map[string]configuration.Store{"store1": fakeConfigurationStore},
		resiliency:             resiliency.New(nil),
	}
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName         string
		storeName        string
		keys             []string
		expectedResponse []*commonv1pb.ConfigurationItem
		expectedError    codes.Code
	}{
		{
			testName:  "Test unsubscribe",
			storeName: "store1",
			keys:      []string{goodKey},
			expectedResponse: []*commonv1pb.ConfigurationItem{
				{
					Key:   goodKey,
					Value: "test-data",
				},
			},
			expectedError: codes.OK,
		},
		{
			testName:  "Test unsubscribe with multi keys",
			storeName: "store1",
			keys:      []string{goodKey, goodKey2},
			expectedResponse: []*commonv1pb.ConfigurationItem{
				{
					Key:   goodKey,
					Value: "test-data",
				},
				{
					Key:   goodKey2,
					Value: "test-data2",
				},
			},
			expectedError: codes.OK,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.SubscribeConfigurationRequest{
				StoreName: tt.storeName,
				Keys:      tt.keys,
			}

			resp, err := client.SubscribeConfigurationAlpha1(context.Background(), req)
			assert.Nil(t, err, "Error should be nil")
			retry := 3
			count := 0
			var subscribeID string
			for {
				if count > retry {
					break
				}
				count++
				time.Sleep(time.Millisecond * 10)
				rsp, recvErr := resp.Recv()
				assert.NotNil(t, rsp)
				assert.Nil(t, recvErr)
				assert.Equal(t, tt.expectedResponse, rsp.Items)
				subscribeID = rsp.Id
			}
			assert.Nil(t, err, "Error should be nil")
			_, err = client.UnsubscribeConfigurationAlpha1(context.Background(), &runtimev1pb.UnsubscribeConfigurationRequest{
				StoreName: tt.storeName,
				Id:        subscribeID,
			})
			assert.Nil(t, err, "Error should be nil")
			count = 0
			for {
				if err != nil && err.Error() == "EOF" {
					break
				}
				if count > retry {
					break
				}
				count++
				time.Sleep(time.Millisecond * 10)
				_, err = resp.Recv()
			}
			assert.Error(t, err, "Unsubscribed channel should returns EOF")
		})
	}
}

func TestGetBulkState(t *testing.T) {
	fakeStore := &daprt.MockStateStore{}
	fakeStore.On("Get", mock.MatchedBy(func(req *state.GetRequest) bool {
		return req.Key == goodStoreKey
	})).Return(
		&state.GetResponse{
			Data: []byte("test-data"),
			ETag: ptr.String("test-etag"),
		}, nil)
	fakeStore.On("Get", mock.MatchedBy(func(req *state.GetRequest) bool {
		return req.Key == errorStoreKey
	})).Return(
		nil,
		errors.New("failed to get state with error-key"))

	fakeAPI := &api{
		id:          "fakeAPI",
		stateStores: map[string]state.Store{"store1": fakeStore},
		resiliency:  resiliency.New(nil),
	}
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName         string
		storeName        string
		keys             []string
		errorExcepted    bool
		expectedResponse []*runtimev1pb.BulkStateItem
		expectedError    codes.Code
	}{
		{
			testName:      "get state",
			storeName:     "store1",
			keys:          []string{goodKey, goodKey},
			errorExcepted: false,
			expectedResponse: []*runtimev1pb.BulkStateItem{
				{
					Data: []byte("test-data"),
					Etag: "test-etag",
				},
				{
					Data: []byte("test-data"),
					Etag: "test-etag",
				},
			},
			expectedError: codes.OK,
		},
		{
			testName:         "get store with non-existing store",
			storeName:        "no-store",
			keys:             []string{goodKey, goodKey},
			errorExcepted:    true,
			expectedResponse: []*runtimev1pb.BulkStateItem{},
			expectedError:    codes.InvalidArgument,
		},
		{
			testName:      "get store with key but error occurs",
			storeName:     "store1",
			keys:          []string{"error-key", "error-key"},
			errorExcepted: false,
			expectedResponse: []*runtimev1pb.BulkStateItem{
				{
					Error: "failed to get state with error-key",
				},
				{
					Error: "failed to get state with error-key",
				},
			},
			expectedError: codes.OK,
		},
		{
			testName:         "get store with empty keys",
			storeName:        "store1",
			keys:             []string{},
			errorExcepted:    false,
			expectedResponse: []*runtimev1pb.BulkStateItem{},
			expectedError:    codes.OK,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetBulkStateRequest{
				StoreName: tt.storeName,
				Keys:      tt.keys,
			}

			resp, err := client.GetBulkState(context.Background(), req)
			if !tt.errorExcepted {
				assert.NoError(t, err, "Expected no error")

				if len(tt.expectedResponse) == 0 {
					assert.Equal(t, len(resp.Items), 0, "Expected response to be empty")
				} else {
					for i := 0; i < len(resp.Items); i++ {
						if tt.expectedResponse[i].Error == "" {
							assert.Equal(t, resp.Items[i].Data, tt.expectedResponse[i].Data, "Expected response Data to be same")
							assert.Equal(t, resp.Items[i].Etag, tt.expectedResponse[i].Etag, "Expected response Etag to be same")
						} else {
							assert.Equal(t, resp.Items[i].Error, tt.expectedResponse[i].Error, "Expected response error to be same")
						}
					}
				}
			} else {
				assert.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestDeleteState(t *testing.T) {
	fakeStore := &daprt.MockStateStore{}
	fakeStore.On("Delete", mock.MatchedBy(func(req *state.DeleteRequest) bool {
		return req.Key == goodStoreKey
	})).Return(nil)
	fakeStore.On("Delete", mock.MatchedBy(func(req *state.DeleteRequest) bool {
		return req.Key == errorStoreKey
	})).Return(errors.New("failed to delete state with key2"))

	fakeAPI := &api{
		id:          "fakeAPI",
		stateStores: map[string]state.Store{"store1": fakeStore},
		resiliency:  resiliency.New(nil),
	}
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName      string
		storeName     string
		key           string
		errorExcepted bool
		expectedError codes.Code
	}{
		{
			testName:      "delete state",
			storeName:     "store1",
			key:           goodKey,
			errorExcepted: false,
			expectedError: codes.OK,
		},
		{
			testName:      "delete store with non-existing store",
			storeName:     "no-store",
			key:           goodKey,
			errorExcepted: true,
			expectedError: codes.InvalidArgument,
		},
		{
			testName:      "delete store with key but error occurs",
			storeName:     "store1",
			key:           "error-key",
			errorExcepted: true,
			expectedError: codes.Internal,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.DeleteStateRequest{
				StoreName: tt.storeName,
				Key:       tt.key,
			}

			_, err := client.DeleteState(context.Background(), req)
			if !tt.errorExcepted {
				assert.NoError(t, err, "Expected no error")
			} else {
				assert.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestPublishTopic(t *testing.T) {
	port, _ := freeport.GetFreePort()

	srv := &api{
		pubsubAdapter: &daprt.MockPubSubAdapter{
			PublishFn: func(req *pubsub.PublishRequest) error {
				if req.Topic == "error-topic" {
					return errors.New("error when publish")
				}

				if req.Topic == "err-not-found" {
					return runtime_pubsub.NotFoundError{PubsubName: "errnotfound"}
				}

				if req.Topic == "err-not-allowed" {
					return runtime_pubsub.NotAllowedError{Topic: req.Topic, ID: "test"}
				}

				return nil
			},
			GetPubSubFn: func(pubsubName string) pubsub.PubSub {
				return &daprt.MockPubSub{}
			},
		},
	}
	server := startTestServerAPI(port, srv)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	_, err := client.PublishEvent(context.Background(), &runtimev1pb.PublishEventRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = client.PublishEvent(context.Background(), &runtimev1pb.PublishEventRequest{
		PubsubName: "pubsub",
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = client.PublishEvent(context.Background(), &runtimev1pb.PublishEventRequest{
		PubsubName: "pubsub",
		Topic:      "topic",
	})
	assert.Nil(t, err)

	_, err = client.PublishEvent(context.Background(), &runtimev1pb.PublishEventRequest{
		PubsubName: "pubsub",
		Topic:      "error-topic",
	})
	assert.Equal(t, codes.Internal, status.Code(err))

	_, err = client.PublishEvent(context.Background(), &runtimev1pb.PublishEventRequest{
		PubsubName: "pubsub",
		Topic:      "err-not-found",
	})
	assert.Equal(t, codes.NotFound, status.Code(err))

	_, err = client.PublishEvent(context.Background(), &runtimev1pb.PublishEventRequest{
		PubsubName: "pubsub",
		Topic:      "err-not-allowed",
	})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestShutdownEndpoints(t *testing.T) {
	port, _ := freeport.GetFreePort()

	m := mock.Mock{}
	m.On("shutdown", mock.Anything).Return()
	srv := &api{
		shutdown: func() {
			m.MethodCalled("shutdown")
		},
	}
	server := startTestServerAPI(port, srv)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	t.Run("Shutdown successfully - 204", func(t *testing.T) {
		_, err := client.Shutdown(context.Background(), &emptypb.Empty{})
		assert.NoError(t, err, "Expected no error")
		for i := 0; i < 5 && len(m.Calls) == 0; i++ {
			<-time.After(200 * time.Millisecond)
		}
		m.AssertCalled(t, "shutdown")
	})
}

func TestInvokeBinding(t *testing.T) {
	port, _ := freeport.GetFreePort()
	srv := &api{
		sendToOutputBindingFn: func(name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
			if name == "error-binding" {
				return nil, errors.New("error when invoke binding")
			}
			return &bindings.InvokeResponse{Data: []byte("ok")}, nil
		},
	}
	server := startTestServerAPI(port, srv)
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err := client.InvokeBinding(context.Background(), &runtimev1pb.InvokeBindingRequest{})
	assert.Nil(t, err)
	_, err = client.InvokeBinding(context.Background(), &runtimev1pb.InvokeBindingRequest{Name: "error-binding"})
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestTransactionStateStoreNotConfigured(t *testing.T) {
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, &api{id: "fakeAPI"}, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err := client.ExecuteStateTransaction(context.Background(), &runtimev1pb.ExecuteStateTransactionRequest{})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestTransactionStateStoreNotImplemented(t *testing.T) {
	fakeStore := &daprt.MockStateStore{}
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, &api{
		id:          "fakeAPI",
		stateStores: map[string]state.Store{"store1": fakeStore},
	}, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err := client.ExecuteStateTransaction(context.Background(), &runtimev1pb.ExecuteStateTransactionRequest{
		StoreName: "store1",
	})
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestExecuteStateTransaction(t *testing.T) {
	fakeStore := &daprt.TransactionalStoreMock{}
	matchKeyFn := func(req *state.TransactionalStateRequest, key string) bool {
		if len(req.Operations) == 1 {
			if rr, ok := req.Operations[0].Request.(state.SetRequest); ok {
				if rr.Key == "fakeAPI||"+key {
					return true
				}
			} else {
				return true
			}
		}
		return false
	}
	fakeStore.On("Multi", mock.MatchedBy(func(req *state.TransactionalStateRequest) bool {
		return matchKeyFn(req, goodKey)
	})).Return(nil)
	fakeStore.On("Multi", mock.MatchedBy(func(req *state.TransactionalStateRequest) bool {
		return matchKeyFn(req, "error-key")
	})).Return(errors.New("error to execute with key2"))

	var fakeTransactionalStore state.TransactionalStore = fakeStore
	fakeAPI := &api{
		id:          "fakeAPI",
		stateStores: map[string]state.Store{"store1": fakeStore},
		transactionalStateStores: map[string]state.TransactionalStore{
			"store1": fakeTransactionalStore,
		},
		resiliency: resiliency.New(nil),
	}
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	stateOptions, _ := GenerateStateOptionsTestCase()
	testCases := []struct {
		testName      string
		storeName     string
		operation     state.OperationType
		key           string
		value         []byte
		options       *commonv1pb.StateOptions
		errorExcepted bool
		expectedError codes.Code
	}{
		{
			testName:      "upsert operation",
			storeName:     "store1",
			operation:     state.Upsert,
			key:           goodKey,
			value:         []byte("1"),
			errorExcepted: false,
			expectedError: codes.OK,
		},
		{
			testName:      "delete operation",
			storeName:     "store1",
			operation:     state.Upsert,
			key:           goodKey,
			errorExcepted: false,
			expectedError: codes.OK,
		},
		{
			testName:      "unknown operation",
			storeName:     "store1",
			operation:     state.OperationType("unknown"),
			key:           goodKey,
			errorExcepted: true,
			expectedError: codes.Unimplemented,
		},
		{
			testName:      "error occurs when multi execute",
			storeName:     "store1",
			operation:     state.Upsert,
			key:           "error-key",
			errorExcepted: true,
			expectedError: codes.Internal,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.ExecuteStateTransactionRequest{
				StoreName: tt.storeName,
				Operations: []*runtimev1pb.TransactionalStateOperation{
					{
						OperationType: string(tt.operation),
						Request: &commonv1pb.StateItem{
							Key:     tt.key,
							Value:   tt.value,
							Options: stateOptions,
						},
					},
				},
			}

			_, err := client.ExecuteStateTransaction(context.Background(), req)
			if !tt.errorExcepted {
				assert.NoError(t, err, "Expected no error")
			} else {
				assert.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestGetMetadata(t *testing.T) {
	port, _ := freeport.GetFreePort()
	fakeComponent := components_v1alpha.Component{}
	fakeComponent.Name = "testComponent"
	fakeAPI := &api{
		id:         "fakeAPI",
		components: []components_v1alpha.Component{fakeComponent},
		getComponentsCapabilitesFn: func() map[string][]string {
			capsMap := make(map[string][]string)
			capsMap["testComponent"] = []string{"mock.feat.testComponent"}
			return capsMap
		},
	}
	fakeAPI.extendedMetadata.Store("testKey", "testValue")
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	response, err := client.GetMetadata(context.Background(), &emptypb.Empty{})
	assert.NoError(t, err, "Expected no error")
	assert.Len(t, response.RegisteredComponents, 1, "One component should be returned")
	assert.Equal(t, response.RegisteredComponents[0].Name, "testComponent")
	assert.Contains(t, response.ExtendedMetadata, "testKey")
	assert.Equal(t, response.ExtendedMetadata["testKey"], "testValue")
	assert.Len(t, response.RegisteredComponents[0].Capabilities, 1, "One capabilities should be returned")
	assert.Equal(t, response.RegisteredComponents[0].Capabilities[0], "mock.feat.testComponent")
}

func TestSetMetadata(t *testing.T) {
	port, _ := freeport.GetFreePort()
	fakeComponent := components_v1alpha.Component{}
	fakeComponent.Name = "testComponent"
	fakeAPI := &api{
		id: "fakeAPI",
	}
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	req := &runtimev1pb.SetMetadataRequest{
		Key:   "testKey",
		Value: "testValue",
	}
	_, err := client.SetMetadata(context.Background(), req)
	assert.NoError(t, err, "Expected no error")
	temp := make(map[string]string)

	// Copy synchronously so it can be serialized to JSON.
	fakeAPI.extendedMetadata.Range(func(key, value interface{}) bool {
		temp[key.(string)] = value.(string)
		return true
	})

	assert.Contains(t, temp, "testKey")
	assert.Equal(t, temp["testKey"], "testValue")
}

func TestStateStoreErrors(t *testing.T) {
	t.Run("save etag mismatch", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagMismatch, errors.New("error"))
		err2 := a.stateErrorResponse(err, messages.ErrStateSave, "a", err.Error())

		assert.Equal(t, "rpc error: code = Aborted desc = failed saving state in state store a: possible etag mismatch. error from state store: error", err2.Error())
	})

	t.Run("save etag invalid", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagInvalid, errors.New("error"))
		err2 := a.stateErrorResponse(err, messages.ErrStateSave, "a", err.Error())

		assert.Equal(t, "rpc error: code = InvalidArgument desc = failed saving state in state store a: invalid etag value: error", err2.Error())
	})

	t.Run("save non etag", func(t *testing.T) {
		a := &api{}
		err := errors.New("error")
		err2 := a.stateErrorResponse(err, messages.ErrStateSave, "a", err.Error())

		assert.Equal(t, "rpc error: code = Internal desc = failed saving state in state store a: error", err2.Error())
	})

	t.Run("delete etag mismatch", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagMismatch, errors.New("error"))
		err2 := a.stateErrorResponse(err, messages.ErrStateDelete, "a", err.Error())

		assert.Equal(t, "rpc error: code = Aborted desc = failed deleting state with key a: possible etag mismatch. error from state store: error", err2.Error())
	})

	t.Run("delete etag invalid", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagInvalid, errors.New("error"))
		err2 := a.stateErrorResponse(err, messages.ErrStateDelete, "a", err.Error())

		assert.Equal(t, "rpc error: code = InvalidArgument desc = failed deleting state with key a: invalid etag value: error", err2.Error())
	})

	t.Run("delete non etag", func(t *testing.T) {
		a := &api{}
		err := errors.New("error")
		err2 := a.stateErrorResponse(err, messages.ErrStateDelete, "a", err.Error())

		assert.Equal(t, "rpc error: code = Internal desc = failed deleting state with key a: error", err2.Error())
	})
}

func TestExtractEtag(t *testing.T) {
	t.Run("no etag present", func(t *testing.T) {
		ok, etag := extractEtag(&commonv1pb.StateItem{})
		assert.False(t, ok)
		assert.Empty(t, etag)
	})

	t.Run("empty etag exists", func(t *testing.T) {
		ok, etag := extractEtag(&commonv1pb.StateItem{
			Etag: &commonv1pb.Etag{},
		})
		assert.True(t, ok)
		assert.Empty(t, etag)
	})

	t.Run("non-empty etag exists", func(t *testing.T) {
		ok, etag := extractEtag(&commonv1pb.StateItem{
			Etag: &commonv1pb.Etag{
				Value: "a",
			},
		})
		assert.True(t, ok)
		assert.Equal(t, "a", etag)
	})
}

func GenerateStateOptionsTestCase() (*commonv1pb.StateOptions, state.SetStateOption) {
	concurrencyOption := commonv1pb.StateOptions_CONCURRENCY_FIRST_WRITE
	consistencyOption := commonv1pb.StateOptions_CONSISTENCY_STRONG

	testOptions := commonv1pb.StateOptions{
		Concurrency: concurrencyOption,
		Consistency: consistencyOption,
	}
	expected := state.SetStateOption{
		Concurrency: "first-write",
		Consistency: "strong",
	}
	return &testOptions, expected
}

type mockStateStoreQuerier struct {
	daprt.MockStateStore
	daprt.MockQuerier
}

const (
	queryTestRequestOK = `{
	"filter": {
		"EQ": { "a": "b" }
	},
	"sort": [
		{ "key": "a" }
	],
	"page": {
		"limit": 2
	}
}`
	queryTestRequestNoRes = `{
	"filter": {
		"EQ": { "a": "b" }
	},
	"page": {
		"limit": 2
	}
}`
	queryTestRequestErr = `{
	"filter": {
		"EQ": { "a": "b" }
	},
	"sort": [
		{ "key": "a" }
	]
}`
	queryTestRequestSyntaxErr = `syntax error`
)

func TestQueryState(t *testing.T) {
	port, err := freeport.GetFreePort()
	assert.NoError(t, err)

	fakeStore := &mockStateStoreQuerier{}
	// simulate full result
	fakeStore.MockQuerier.On("Query", mock.MatchedBy(func(req *state.QueryRequest) bool {
		return len(req.Query.Sort) != 0 && req.Query.Page.Limit != 0
	})).Return(
		&state.QueryResponse{
			Results: []state.QueryItem{
				{
					Key:  "1",
					Data: []byte(`{"a":"b"}`),
				},
			},
		}, nil)
	// simulate empty data
	fakeStore.MockQuerier.On("Query", mock.MatchedBy(func(req *state.QueryRequest) bool {
		return len(req.Query.Sort) == 0 && req.Query.Page.Limit != 0
	})).Return(
		&state.QueryResponse{
			Results: []state.QueryItem{},
		}, nil)
	// simulate error
	fakeStore.MockQuerier.On("Query", mock.MatchedBy(func(req *state.QueryRequest) bool {
		return len(req.Query.Sort) != 0 && req.Query.Page.Limit == 0
	})).Return(nil, errors.New("Query error"))

	server := startTestServerAPI(port, &api{
		id:          "fakeAPI",
		stateStores: map[string]state.Store{"store1": fakeStore},
		resiliency:  resiliency.New(nil),
	})
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	resp, err := client.QueryStateAlpha1(context.Background(), &runtimev1pb.QueryStateRequest{
		StoreName: "store1",
		Query:     queryTestRequestOK,
	})
	assert.Equal(t, 1, len(resp.Results))
	assert.Equal(t, codes.OK, status.Code(err))
	if len(resp.Results) > 0 {
		assert.NotNil(t, resp.Results[0].Data)
	}

	resp, err = client.QueryStateAlpha1(context.Background(), &runtimev1pb.QueryStateRequest{
		StoreName: "store1",
		Query:     queryTestRequestNoRes,
	})
	assert.Equal(t, 0, len(resp.Results))
	assert.Equal(t, codes.OK, status.Code(err))

	_, err = client.QueryStateAlpha1(context.Background(), &runtimev1pb.QueryStateRequest{
		StoreName: "store1",
		Query:     queryTestRequestErr,
	})
	assert.Equal(t, codes.Internal, status.Code(err))

	_, err = client.QueryStateAlpha1(context.Background(), &runtimev1pb.QueryStateRequest{
		StoreName: "store1",
		Query:     queryTestRequestSyntaxErr,
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestStateStoreQuerierNotImplemented(t *testing.T) {
	port, err := freeport.GetFreePort()
	assert.NoError(t, err)

	server := startDaprAPIServer(
		port,
		&api{
			id:          "fakeAPI",
			stateStores: map[string]state.Store{"store1": &daprt.MockStateStore{}},
			resiliency:  resiliency.New(nil),
		},
		"")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err = client.QueryStateAlpha1(context.Background(), &runtimev1pb.QueryStateRequest{
		StoreName: "store1",
	})
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestStateStoreQuerierEncrypted(t *testing.T) {
	port, err := freeport.GetFreePort()
	assert.NoError(t, err)

	storeName := "encrypted-store1"
	encryption.AddEncryptedStateStore(storeName, encryption.ComponentEncryptionKeys{})

	server := startDaprAPIServer(
		port,
		&api{
			id:          "fakeAPI",
			stateStores: map[string]state.Store{storeName: &mockStateStoreQuerier{}},
			resiliency:  resiliency.New(nil),
		},
		"")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err = client.QueryStateAlpha1(context.Background(), &runtimev1pb.QueryStateRequest{
		StoreName: storeName,
	})
	assert.Equal(t, codes.Aborted, status.Code(err))
}

func TestGetConfigurationAlpha1(t *testing.T) {
	t.Run("get configuration item", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		assert.NoError(t, err)

		server := startDaprAPIServer(
			port,
			&api{
				id:                  "fakeAPI",
				configurationStores: map[string]configuration.Store{"store1": &mockConfigStore{}},
				resiliency:          resiliency.New(nil),
			},
			"")
		defer server.Stop()

		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)
		r, err := client.GetConfigurationAlpha1(context.TODO(), &runtimev1pb.GetConfigurationRequest{
			StoreName: "store1",
			Keys: []string{
				"key1",
			},
		})

		assert.NoError(t, err)
		assert.NotNil(t, r.Items)
		assert.Len(t, r.Items, 1)
		assert.Equal(t, "key1", r.Items[0].Key)
		assert.Equal(t, "val1", r.Items[0].Value)
	})
}

func TestSubscribeConfigurationAlpha1(t *testing.T) {
	t.Run("get configuration item", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		assert.NoError(t, err)

		server := startDaprAPIServer(
			port,
			&api{
				id:                         "fakeAPI",
				configurationStores:        map[string]configuration.Store{"store1": &mockConfigStore{}},
				configurationSubscribe:     make(map[string]chan struct{}),
				configurationSubscribeLock: sync.Mutex{},
				resiliency:                 resiliency.New(nil),
			},
			"")
		defer server.Stop()

		clientConn := createTestClient(port)
		defer clientConn.Close()

		ctx := context.TODO()
		client := runtimev1pb.NewDaprClient(clientConn)
		s, err := client.SubscribeConfigurationAlpha1(ctx, &runtimev1pb.SubscribeConfigurationRequest{
			StoreName: "store1",
			Keys: []string{
				"key1",
			},
		})

		assert.NoError(t, err)

		r := &runtimev1pb.SubscribeConfigurationResponse{}

		for {
			update, err := s.Recv()
			if err == io.EOF {
				break
			}

			if update != nil {
				r = update
				break
			}
		}

		assert.NotNil(t, r)
		assert.Len(t, r.Items, 1)
		assert.Equal(t, "key1", r.Items[0].Key)
		assert.Equal(t, "val1", r.Items[0].Value)
	})

	t.Run("get all configuration item for empty list", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		assert.NoError(t, err)

		server := startDaprAPIServer(
			port,
			&api{
				id:                         "fakeAPI",
				configurationStores:        map[string]configuration.Store{"store1": &mockConfigStore{}},
				configurationSubscribe:     make(map[string]chan struct{}),
				configurationSubscribeLock: sync.Mutex{},
				resiliency:                 resiliency.New(nil),
			},
			"")
		defer server.Stop()

		clientConn := createTestClient(port)
		defer clientConn.Close()

		ctx := context.TODO()
		client := runtimev1pb.NewDaprClient(clientConn)
		s, err := client.SubscribeConfigurationAlpha1(ctx, &runtimev1pb.SubscribeConfigurationRequest{
			StoreName: "store1",
			Keys:      []string{},
		})

		assert.NoError(t, err)

		r := &runtimev1pb.SubscribeConfigurationResponse{}

		for {
			update, err := s.Recv()
			if err == io.EOF {
				break
			}

			if update != nil {
				r = update
				break
			}
		}

		assert.NotNil(t, r)
		assert.Len(t, r.Items, 2)
		assert.Equal(t, "key1", r.Items[0].Key)
		assert.Equal(t, "val1", r.Items[0].Value)
		assert.Equal(t, "key2", r.Items[1].Key)
		assert.Equal(t, "val2", r.Items[1].Value)
	})
}

func TestStateAPIWithResiliency(t *testing.T) {
	failingStore := &daprt.FailingStatestore{
		Failure: daprt.Failure{
			Fails: map[string]int{
				"failingGetKey":        1,
				"failingSetKey":        1,
				"failingDeleteKey":     1,
				"failingBulkGetKey":    1,
				"failingBulkSetKey":    1,
				"failingBulkDeleteKey": 1,
				"failingMultiKey":      1,
				"failingQueryKey":      1,
			},
			Timeouts: map[string]time.Duration{
				"timeoutGetKey":        time.Second * 10,
				"timeoutSetKey":        time.Second * 10,
				"timeoutDeleteKey":     time.Second * 10,
				"timeoutBulkGetKey":    time.Second * 10,
				"timeoutBulkSetKey":    time.Second * 10,
				"timeoutBulkDeleteKey": time.Second * 10,
				"timeoutMultiKey":      time.Second * 10,
				"timeoutQueryKey":      time.Second * 10,
			},
			CallCount: map[string]int{},
		},
	}

	state_loader.SaveStateConfiguration("failStore", map[string]string{"keyPrefix": "none"})

	fakeAPI := &api{
		id:                       "fakeAPI",
		stateStores:              map[string]state.Store{"failStore": failingStore},
		transactionalStateStores: map[string]state.TransactionalStore{"failStore": failingStore},
		resiliency:               resiliency.FromConfigurations(logger.NewLogger("grpc.api.test"), testResiliency),
	}
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	t.Run("get state request retries with resiliency", func(t *testing.T) {
		_, err := client.GetState(context.Background(), &runtimev1pb.GetStateRequest{
			StoreName: "failStore",
			Key:       "failingGetKey",
		})
		assert.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["failingGetKey"])
	})

	t.Run("get state request times out with resiliency", func(t *testing.T) {
		start := time.Now()
		_, err := client.GetState(context.Background(), &runtimev1pb.GetStateRequest{
			StoreName: "failStore",
			Key:       "timeoutGetKey",
		})
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["timeoutGetKey"])
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("set state request retries with resiliency", func(t *testing.T) {
		_, err := client.SaveState(context.Background(), &runtimev1pb.SaveStateRequest{
			StoreName: "failStore",
			States: []*commonv1pb.StateItem{
				{
					Key:   "failingSetKey",
					Value: []byte("TestData"),
				},
			},
		})
		assert.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["failingSetKey"])
	})

	t.Run("set state request times out with resiliency", func(t *testing.T) {
		start := time.Now()
		_, err := client.SaveState(context.Background(), &runtimev1pb.SaveStateRequest{
			StoreName: "failStore",
			States: []*commonv1pb.StateItem{
				{
					Key:   "timeoutSetKey",
					Value: []byte("TestData"),
				},
			},
		})
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["timeoutSetKey"])
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("delete state request retries with resiliency", func(t *testing.T) {
		_, err := client.DeleteState(context.Background(), &runtimev1pb.DeleteStateRequest{
			StoreName: "failStore",
			Key:       "failingDeleteKey",
		})
		assert.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["failingDeleteKey"])
	})

	t.Run("delete state request times out with resiliency", func(t *testing.T) {
		start := time.Now()
		_, err := client.DeleteState(context.Background(), &runtimev1pb.DeleteStateRequest{
			StoreName: "failStore",
			Key:       "timeoutDeleteKey",
		})
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["timeoutDeleteKey"])
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("bulk state get can recover from one bad key with resiliency retries", func(t *testing.T) {
		_, err := client.GetBulkState(context.Background(), &runtimev1pb.GetBulkStateRequest{
			StoreName: "failStore",
			Keys:      []string{"failingBulkGetKey", "goodBulkGetKey"},
		})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["failingBulkGetKey"])
		assert.Equal(t, 1, failingStore.Failure.CallCount["goodBulkGetKey"])
	})

	t.Run("bulk state get times out on single with resiliency", func(t *testing.T) {
		start := time.Now()
		resp, err := client.GetBulkState(context.Background(), &runtimev1pb.GetBulkStateRequest{
			StoreName: "failStore",
			Keys:      []string{"timeoutBulkGetKey", "goodTimeoutBulkGetKey"},
		})
		end := time.Now()

		assert.NoError(t, err)
		assert.Len(t, resp.Items, 2)
		for _, item := range resp.Items {
			if item.Key == "timeoutBulkGetKey" {
				assert.NotEmpty(t, item.Error)
				assert.Contains(t, item.Error, "context deadline exceeded")
			} else {
				assert.Empty(t, item.Error)
			}
		}
		assert.Equal(t, 2, failingStore.Failure.CallCount["timeoutBulkGetKey"])
		assert.Equal(t, 1, failingStore.Failure.CallCount["goodTimeoutBulkGetKey"])
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("bulk state set recovers from single key failure with resiliency", func(t *testing.T) {
		_, err := client.SaveState(context.Background(), &runtimev1pb.SaveStateRequest{
			StoreName: "failStore",
			States: []*commonv1pb.StateItem{
				{
					Key:   "failingBulkSetKey",
					Value: []byte("TestData"),
				},
				{
					Key:   "goodBulkSetKey",
					Value: []byte("TestData"),
				},
			},
		})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["failingBulkSetKey"])
		assert.Equal(t, 1, failingStore.Failure.CallCount["goodBulkSetKey"])
	})

	t.Run("bulk state set times out with resiliency", func(t *testing.T) {
		start := time.Now()
		_, err := client.SaveState(context.Background(), &runtimev1pb.SaveStateRequest{
			StoreName: "failStore",
			States: []*commonv1pb.StateItem{
				{
					Key:   "timeoutBulkSetKey",
					Value: []byte("TestData"),
				},
				{
					Key:   "goodTimeoutBulkSetKey",
					Value: []byte("TestData"),
				},
			},
		})
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["timeoutBulkSetKey"])
		assert.Equal(t, 0, failingStore.Failure.CallCount["goodTimeoutBulkSetKey"])
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("state transaction passes after retries with resiliency", func(t *testing.T) {
		_, err := client.ExecuteStateTransaction(context.Background(), &runtimev1pb.ExecuteStateTransactionRequest{
			StoreName: "failStore",
			Operations: []*runtimev1pb.TransactionalStateOperation{
				{
					OperationType: string(state.Delete),
					Request: &commonv1pb.StateItem{
						Key: "failingMultiKey",
					},
				},
			},
		})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["failingMultiKey"])
	})

	t.Run("state transaction times out with resiliency", func(t *testing.T) {
		_, err := client.ExecuteStateTransaction(context.Background(), &runtimev1pb.ExecuteStateTransactionRequest{
			StoreName: "failStore",
			Operations: []*runtimev1pb.TransactionalStateOperation{
				{
					OperationType: string(state.Delete),
					Request: &commonv1pb.StateItem{
						Key: "timeoutMultiKey",
					},
				},
			},
		})

		assert.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["timeoutMultiKey"])
	})

	t.Run("state query retries with resiliency", func(t *testing.T) {
		_, err := client.QueryStateAlpha1(context.Background(), &runtimev1pb.QueryStateRequest{
			StoreName: "failStore",
			Query:     queryTestRequestOK,
			Metadata:  map[string]string{"key": "failingQueryKey"},
		})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["failingQueryKey"])
	})

	t.Run("state query times out with resiliency", func(t *testing.T) {
		_, err := client.QueryStateAlpha1(context.Background(), &runtimev1pb.QueryStateRequest{
			StoreName: "failStore",
			Query:     queryTestRequestOK,
			Metadata:  map[string]string{"key": "timeoutQueryKey"},
		})

		assert.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["timeoutQueryKey"])
	})
}

func TestConfigurationAPIWithResiliency(t *testing.T) {
	failingConfigStore := daprt.FailingConfigurationStore{
		Failure: daprt.Failure{
			Fails: map[string]int{
				"failingGetKey":         1,
				"failingSubscribeKey":   1,
				"failingUnsubscribeKey": 1,
			},
			Timeouts: map[string]time.Duration{
				"timeoutGetKey":         time.Second * 10,
				"timeoutSubscribeKey":   time.Second * 10,
				"timeoutUnsubscribeKey": time.Second * 10,
			},
			CallCount: map[string]int{},
		},
	}

	fakeAPI := &api{
		id:                     "fakeAPI",
		configurationStores:    map[string]configuration.Store{"failConfig": &failingConfigStore},
		configurationSubscribe: map[string]chan struct{}{},
		resiliency:             resiliency.FromConfigurations(logger.NewLogger("grpc.api.test"), testResiliency),
	}
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	t.Run("test get configuration retries with resiliency", func(t *testing.T) {
		_, err := client.GetConfigurationAlpha1(context.Background(), &runtimev1pb.GetConfigurationRequest{
			StoreName: "failConfig",
			Keys:      []string{},
			Metadata:  map[string]string{"key": "failingGetKey"},
		})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingConfigStore.Failure.CallCount["failingGetKey"])
	})

	t.Run("test get configuration fails due to timeout with resiliency", func(t *testing.T) {
		_, err := client.GetConfigurationAlpha1(context.Background(), &runtimev1pb.GetConfigurationRequest{
			StoreName: "failConfig",
			Keys:      []string{},
			Metadata:  map[string]string{"key": "timeoutGetKey"},
		})

		assert.Error(t, err)
		assert.Equal(t, 2, failingConfigStore.Failure.CallCount["timeoutGetKey"])
	})

	t.Run("test subscribe configuration retries with resiliency", func(t *testing.T) {
		resp, err := client.SubscribeConfigurationAlpha1(context.Background(), &runtimev1pb.SubscribeConfigurationRequest{
			StoreName: "failConfig",
			Keys:      []string{},
			Metadata:  map[string]string{"key": "failingSubscribeKey"},
		})
		assert.NoError(t, err)

		_, err = resp.Recv()
		assert.NoError(t, err)
		// Subscribe now calls Get first so we have an extra call.
		assert.Equal(t, 3, failingConfigStore.Failure.CallCount["failingSubscribeKey"])
	})

	t.Run("test subscribe configuration fails due to timeout with resiliency", func(t *testing.T) {
		resp, err := client.SubscribeConfigurationAlpha1(context.Background(), &runtimev1pb.SubscribeConfigurationRequest{
			StoreName: "failConfig",
			Keys:      []string{},
			Metadata:  map[string]string{"key": "timeoutSubscribeKey"},
		})
		assert.NoError(t, err)

		_, err = resp.Recv()
		assert.Error(t, err)
		assert.Equal(t, 2, failingConfigStore.Failure.CallCount["timeoutSubscribeKey"])
	})

	t.Run("test unsubscribe configuration retries with resiliency", func(t *testing.T) {
		fakeAPI.configurationSubscribe["failingUnsubscribeKey"] = make(chan struct{})

		_, err := client.UnsubscribeConfigurationAlpha1(context.Background(), &runtimev1pb.UnsubscribeConfigurationRequest{
			StoreName: "failConfig",
			Id:        "failingUnsubscribeKey",
		})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingConfigStore.Failure.CallCount["failingUnsubscribeKey"])
	})

	t.Run("test unsubscribe configuration fails due to timeout with resiliency", func(t *testing.T) {
		fakeAPI.configurationSubscribe["timeoutUnsubscribeKey"] = make(chan struct{})

		_, err := client.UnsubscribeConfigurationAlpha1(context.Background(), &runtimev1pb.UnsubscribeConfigurationRequest{
			StoreName: "failConfig",
			Id:        "timeoutUnsubscribeKey",
		})

		assert.Error(t, err)
		assert.Equal(t, 2, failingConfigStore.Failure.CallCount["timeoutUnsubscribeKey"])
	})
}

func TestSecretAPIWithResiliency(t *testing.T) {
	failingStore := daprt.FailingSecretStore{
		Failure: daprt.Failure{
			Fails:     map[string]int{"key": 1, "bulk": 1},
			Timeouts:  map[string]time.Duration{"timeout": time.Second * 10, "bulkTimeout": time.Second * 10},
			CallCount: map[string]int{},
		},
	}

	// Setup Dapr API server
	fakeAPI := &api{
		id:           "fakeAPI",
		secretStores: map[string]secretstores.SecretStore{"failSecret": failingStore},
		resiliency:   resiliency.FromConfigurations(logger.NewLogger("grpc.api.test"), testResiliency),
	}
	// Run test server
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	// Create gRPC test client
	clientConn := createTestClient(port)
	defer clientConn.Close()

	// act
	client := runtimev1pb.NewDaprClient(clientConn)

	t.Run("Get secret - retries on initial failure with resiliency", func(t *testing.T) {
		_, err := client.GetSecret(context.Background(), &runtimev1pb.GetSecretRequest{
			StoreName: "failSecret",
			Key:       "key",
		})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["key"])
	})

	t.Run("Get secret - timeout before request ends", func(t *testing.T) {
		// Store sleeps for 10 seconds, let's make sure our timeout takes less time than that.
		start := time.Now()
		_, err := client.GetSecret(context.Background(), &runtimev1pb.GetSecretRequest{
			StoreName: "failSecret",
			Key:       "timeout",
		})
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["timeout"])
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("Get bulk secret - retries on initial failure with resiliency", func(t *testing.T) {
		_, err := client.GetBulkSecret(context.Background(), &runtimev1pb.GetBulkSecretRequest{
			StoreName: "failSecret",
			Metadata:  map[string]string{"key": "bulk"},
		})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["bulk"])
	})

	t.Run("Get bulk secret - timeout before request ends", func(t *testing.T) {
		start := time.Now()
		_, err := client.GetBulkSecret(context.Background(), &runtimev1pb.GetBulkSecretRequest{
			StoreName: "failSecret",
			Metadata:  map[string]string{"key": "bulkTimeout"},
		})
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount["bulkTimeout"])
		assert.Less(t, end.Sub(start), time.Second*10)
	})
}

func TestServiceInvocationWithResiliency(t *testing.T) {
	failingDirectMessaging := &daprt.FailingDirectMessaging{
		Failure: daprt.Failure{
			Fails: map[string]int{
				"failingKey":        1,
				"extraFailingKey":   3,
				"circuitBreakerKey": 10,
			},
			Timeouts: map[string]time.Duration{
				"timeoutKey": time.Second * 10,
			},
			CallCount: map[string]int{},
		},
	}

	// Setup Dapr API server
	fakeAPI := &api{
		id:              "fakeAPI",
		directMessaging: failingDirectMessaging,
		resiliency:      resiliency.FromConfigurations(logger.NewLogger("grpc.api.test"), testResiliency),
	}

	// Run test server
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	// Create gRPC test client
	clientConn := createTestClient(port)
	defer clientConn.Close()

	// act
	client := runtimev1pb.NewDaprClient(clientConn)

	t.Run("Test invoke direct message retries with resiliency", func(t *testing.T) {
		_, err := client.InvokeService(context.Background(), &runtimev1pb.InvokeServiceRequest{
			Id: "failingApp",
			Message: &commonv1pb.InvokeRequest{
				Method: "test",
				Data:   &anypb.Any{Value: []byte("failingKey")},
			},
		})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingDirectMessaging.Failure.CallCount["failingKey"])
	})

	t.Run("Test invoke direct message fails with timeout", func(t *testing.T) {
		start := time.Now()
		_, err := client.InvokeService(context.Background(), &runtimev1pb.InvokeServiceRequest{
			Id: "failingApp",
			Message: &commonv1pb.InvokeRequest{
				Method: "test",
				Data:   &anypb.Any{Value: []byte("timeoutKey")},
			},
		})
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 2, failingDirectMessaging.Failure.CallCount["timeoutKey"])
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("Test invoke direct messages fails after exhausting retries", func(t *testing.T) {
		_, err := client.InvokeService(context.Background(), &runtimev1pb.InvokeServiceRequest{
			Id: "failingApp",
			Message: &commonv1pb.InvokeRequest{
				Method: "test",
				Data:   &anypb.Any{Value: []byte("extraFailingKey")},
			},
		})

		assert.Error(t, err)
		assert.Equal(t, 2, failingDirectMessaging.Failure.CallCount["extraFailingKey"])
	})

	t.Run("Test invoke direct messages opens circuit breaker after consecutive failures", func(t *testing.T) {
		// Circuit breaker trips on the 5th request, ending the retries.
		_, err := client.InvokeService(context.Background(), &runtimev1pb.InvokeServiceRequest{
			Id: "circuitBreakerApp",
			Message: &commonv1pb.InvokeRequest{
				Method: "test",
				Data:   &anypb.Any{Value: []byte("circuitBreakerKey")},
			},
		})
		assert.Error(t, err)
		assert.Equal(t, 5, failingDirectMessaging.Failure.CallCount["circuitBreakerKey"])

		// Additional requests should fail due to the circuit breaker.
		_, err = client.InvokeService(context.Background(), &runtimev1pb.InvokeServiceRequest{
			Id: "circuitBreakerApp",
			Message: &commonv1pb.InvokeRequest{
				Method: "test",
				Data:   &anypb.Any{Value: []byte("circuitBreakerKey")},
			},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "circuit breaker is open")
		assert.Equal(t, 5, failingDirectMessaging.Failure.CallCount["circuitBreakerKey"])
	})
}

type mockConfigStore struct{}

func (m *mockConfigStore) Init(metadata configuration.Metadata) error {
	return nil
}

func (m *mockConfigStore) Get(ctx context.Context, req *configuration.GetRequest) (*configuration.GetResponse, error) {
	items := []*configuration.Item{
		{
			Key:   "key1",
			Value: "val1",
		}, {
			Key:   "key2",
			Value: "val2",
		},
	}

	res := make([]*configuration.Item, 0, 16)

	if len(req.Keys) == 0 {
		res = items
	} else {
		for _, item := range items {
			for _, key := range req.Keys {
				if item.Key == key {
					res = append(res, item)
				}
			}
		}
	}

	return &configuration.GetResponse{
		Items: res,
	}, nil
}

func (m *mockConfigStore) Subscribe(ctx context.Context, req *configuration.SubscribeRequest, handler configuration.UpdateHandler) (string, error) {
	items := []*configuration.Item{
		{
			Key:   "key1",
			Value: "val1",
		}, {
			Key:   "key2",
			Value: "val2",
		},
	}

	res := make([]*configuration.Item, 0, 16)

	if len(req.Keys) == 0 {
		res = items
	} else {
		for _, item := range items {
			for _, key := range req.Keys {
				if item.Key == key {
					res = append(res, item)
				}
			}
		}
	}

	handler(ctx, &configuration.UpdateEvent{
		Items: res,
	})

	return "", nil
}

func (m *mockConfigStore) Unsubscribe(ctx context.Context, req *configuration.UnsubscribeRequest) error {
	return nil
}

func TestTryLockRequestToComponentRequest(t *testing.T) {
	req := TryLockRequestToComponentRequest(&runtimev1pb.TryLockRequest{
		StoreName:       "redis",
		ResourceId:      "resourceId",
		LockOwner:       "owner1",
		ExpiryInSeconds: 1000,
	})
	assert.True(t, req.ResourceID == "resourceId")
	assert.True(t, req.LockOwner == "owner1")
	assert.True(t, req.ExpiryInSeconds == 1000)
	req = TryLockRequestToComponentRequest(nil)
	assert.NotNil(t, req)
}

func TestTryLockResponseToGrpcResponse(t *testing.T) {
	resp := TryLockResponseToGrpcResponse(&lock.TryLockResponse{
		Success: true,
	})
	assert.True(t, resp.Success)
	resp2 := TryLockResponseToGrpcResponse(nil)
	assert.NotNil(t, resp2)
}

func TestUnlockGrpcToComponentRequest(t *testing.T) {
	req := UnlockGrpcToComponentRequest(&runtimev1pb.UnlockRequest{
		StoreName:  "redis",
		ResourceId: "resourceId",
		LockOwner:  "owner1",
	})
	assert.True(t, req.ResourceID == "resourceId")
	assert.True(t, req.LockOwner == "owner1")
	req = UnlockGrpcToComponentRequest(nil)
	assert.NotNil(t, req)
}

func TestUnlockResponseToGrpcResponse(t *testing.T) {
	resp := UnlockResponseToGrpcResponse(&lock.UnlockResponse{Status: lock.Success})
	assert.True(t, resp.Status == runtimev1pb.UnlockResponse_SUCCESS)
	resp2 := UnlockResponseToGrpcResponse(nil)
	assert.NotNil(t, resp2)
}

func TestTryLock(t *testing.T) {
	t.Run("error when lock store not configured", func(t *testing.T) {
		api := NewAPI("", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, config.TracingSpec{}, nil, "", nil, nil, nil)
		req := &runtimev1pb.TryLockRequest{
			StoreName: "abc",
		}
		_, err := api.TryLockAlpha1(context.Background(), req)
		assert.Equal(t, "rpc error: code = FailedPrecondition desc = lock store is not configured", err.Error())
	})

	t.Run("InvalidArgument: ResourceID empty", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		mockLockStore := daprt.NewMockStore(ctl)
		api := NewAPI("", nil, nil, nil, nil, nil, nil, map[string]lock.Store{"mock": mockLockStore}, nil, nil, nil, nil, config.TracingSpec{}, nil, "", nil, nil, nil)
		req := &runtimev1pb.TryLockRequest{
			StoreName: "abc",
		}
		_, err := api.TryLockAlpha1(context.Background(), req)
		assert.Equal(t, "rpc error: code = InvalidArgument desc = ResourceId is empty in lock store abc", err.Error())
	})

	t.Run("InvalidArgument: LockOwner empty", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		mockLockStore := daprt.NewMockStore(ctl)
		api := NewAPI("", nil, nil, nil, nil, nil, nil, map[string]lock.Store{"abc": mockLockStore}, nil, nil, nil, nil, config.TracingSpec{}, nil, "", nil, nil, nil)
		req := &runtimev1pb.TryLockRequest{
			StoreName:  "abc",
			ResourceId: "resource",
		}
		_, err := api.TryLockAlpha1(context.Background(), req)
		assert.Equal(t, "rpc error: code = InvalidArgument desc = LockOwner is empty in lock store abc", err.Error())
	})

	t.Run("InvalidArgument: ExpiryInSeconds is not positive", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		mockLockStore := daprt.NewMockStore(ctl)
		api := NewAPI("", nil, nil, nil, nil, nil, nil, map[string]lock.Store{"abc": mockLockStore}, nil, nil, nil, nil, config.TracingSpec{}, nil, "", nil, nil, nil)

		req := &runtimev1pb.TryLockRequest{
			StoreName:  "abc",
			ResourceId: "resource",
			LockOwner:  "owner",
		}
		_, err := api.TryLockAlpha1(context.Background(), req)
		assert.Equal(t, "rpc error: code = InvalidArgument desc = ExpiryInSeconds is not positive in lock store abc", err.Error())
	})

	t.Run("InvalidArgument: lock store not found", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		mockLockStore := daprt.NewMockStore(ctl)
		api := NewAPI("", nil, nil, nil, nil, nil, nil, map[string]lock.Store{"mock": mockLockStore}, nil, nil, nil, nil, config.TracingSpec{}, nil, "", nil, nil, nil)

		req := &runtimev1pb.TryLockRequest{
			StoreName:       "abc",
			ResourceId:      "resource",
			LockOwner:       "owner",
			ExpiryInSeconds: 1,
		}
		_, err := api.TryLockAlpha1(context.Background(), req)
		assert.Equal(t, "rpc error: code = InvalidArgument desc = lock store abc not found", err.Error())
	})

	t.Run("Success", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		mockLockStore := daprt.NewMockStore(ctl)

		mockLockStore.EXPECT().TryLock(gomock.Any()).DoAndReturn(func(req *lock.TryLockRequest) (*lock.TryLockResponse, error) {
			assert.Equal(t, "lock||resource", req.ResourceID)
			assert.Equal(t, "owner", req.LockOwner)
			assert.Equal(t, int32(1), req.ExpiryInSeconds)
			return &lock.TryLockResponse{
				Success: true,
			}, nil
		})
		api := NewAPI("", nil, nil, nil, nil, nil, nil, map[string]lock.Store{"mock": mockLockStore}, nil, nil, nil, nil, config.TracingSpec{}, nil, "", nil, nil, nil)
		req := &runtimev1pb.TryLockRequest{
			StoreName:       "mock",
			ResourceId:      "resource",
			LockOwner:       "owner",
			ExpiryInSeconds: 1,
		}
		resp, err := api.TryLockAlpha1(context.Background(), req)
		assert.Nil(t, err)
		assert.Equal(t, true, resp.Success)
	})
}

func TestUnlock(t *testing.T) {
	t.Run("error when lock store not configured", func(t *testing.T) {
		api := NewAPI("", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, config.TracingSpec{}, nil, "", nil, nil, nil)

		req := &runtimev1pb.UnlockRequest{
			StoreName: "abc",
		}
		_, err := api.UnlockAlpha1(context.Background(), req)
		assert.Equal(t, "rpc error: code = FailedPrecondition desc = lock store is not configured", err.Error())
	})

	t.Run("InvalidArgument: ResourceId empty", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		mockLockStore := daprt.NewMockStore(ctl)
		api := NewAPI("", nil, nil, nil, nil, nil, nil, map[string]lock.Store{"mock": mockLockStore}, nil, nil, nil, nil, config.TracingSpec{}, nil, "", nil, nil, nil)

		req := &runtimev1pb.UnlockRequest{
			StoreName: "abc",
		}
		_, err := api.UnlockAlpha1(context.Background(), req)
		assert.Equal(t, "rpc error: code = InvalidArgument desc = ResourceId is empty in lock store abc", err.Error())
	})

	t.Run("InvalidArgument: lock owner empty", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		mockLockStore := daprt.NewMockStore(ctl)
		api := NewAPI("", nil, nil, nil, nil, nil, nil, map[string]lock.Store{"mock": mockLockStore}, nil, nil, nil, nil, config.TracingSpec{}, nil, "", nil, nil, nil)
		req := &runtimev1pb.UnlockRequest{
			StoreName:  "abc",
			ResourceId: "resource",
		}
		_, err := api.UnlockAlpha1(context.Background(), req)
		assert.Equal(t, "rpc error: code = InvalidArgument desc = LockOwner is empty in lock store abc", err.Error())
	})

	t.Run("InvalidArgument: lock store not found", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		mockLockStore := daprt.NewMockStore(ctl)
		api := NewAPI("", nil, nil, nil, nil, nil, nil, map[string]lock.Store{"mock": mockLockStore}, nil, nil, nil, nil, config.TracingSpec{}, nil, "", nil, nil, nil)

		req := &runtimev1pb.UnlockRequest{
			StoreName:  "abc",
			ResourceId: "resource",
			LockOwner:  "owner",
		}
		_, err := api.UnlockAlpha1(context.Background(), req)
		assert.Equal(t, "rpc error: code = InvalidArgument desc = lock store abc not found", err.Error())
	})

	t.Run("normal", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		mockLockStore := daprt.NewMockStore(ctl)

		mockLockStore.EXPECT().Unlock(gomock.Any()).DoAndReturn(func(req *lock.UnlockRequest) (*lock.UnlockResponse, error) {
			assert.Equal(t, "lock||resource", req.ResourceID)
			assert.Equal(t, "owner", req.LockOwner)
			return &lock.UnlockResponse{
				Status: lock.Success,
			}, nil
		})
		api := NewAPI("", nil, nil, nil, nil, nil, nil, map[string]lock.Store{"mock": mockLockStore}, nil, nil, nil, nil, config.TracingSpec{}, nil, "", nil, nil, nil)
		req := &runtimev1pb.UnlockRequest{
			StoreName:  "mock",
			ResourceId: "resource",
			LockOwner:  "owner",
		}
		resp, err := api.UnlockAlpha1(context.Background(), req)
		assert.Nil(t, err)
		assert.Equal(t, runtimev1pb.UnlockResponse_SUCCESS, resp.Status)
	})
}
