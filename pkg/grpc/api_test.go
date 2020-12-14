// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/logger"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	runtime_pubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	daprt "github.com/dapr/dapr/pkg/testing"
	testtrace "github.com/dapr/dapr/pkg/testing/trace"
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/any"
	"github.com/golang/protobuf/ptypes/empty"
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
)

const maxGRPCServerUptime = 100 * time.Millisecond

type mockGRPCAPI struct {
}

func (m *mockGRPCAPI) CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	var resp = invokev1.NewInvokeMethodResponse(0, "", nil)
	resp.WithRawData(ExtractSpanContext(ctx), "text/plains")
	return resp.Proto(), nil
}

func (m *mockGRPCAPI) CallActor(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	var resp = invokev1.NewInvokeMethodResponse(0, "", nil)
	resp.WithRawData(ExtractSpanContext(ctx), "text/plains")
	return resp.Proto(), nil
}

func (m *mockGRPCAPI) PublishEvent(ctx context.Context, in *runtimev1pb.PublishEventRequest) (*empty.Empty, error) {
	return &empty.Empty{}, nil
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

func (m *mockGRPCAPI) SaveState(ctx context.Context, in *runtimev1pb.SaveStateRequest) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) DeleteState(ctx context.Context, in *runtimev1pb.DeleteStateRequest) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) GetSecret(ctx context.Context, in *runtimev1pb.GetSecretRequest) (*runtimev1pb.GetSecretResponse, error) {
	return &runtimev1pb.GetSecretResponse{}, nil
}

func (m *mockGRPCAPI) ExecuteStateTransaction(ctx context.Context, in *runtimev1pb.ExecuteStateTransactionRequest) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (m *mockGRPCAPI) RegisterActorTimer(ctx context.Context, in *runtimev1pb.RegisterActorTimerRequest) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func ExtractSpanContext(ctx context.Context) []byte {
	span := diag_utils.SpanFromContext(ctx)
	return []byte(SerializeSpanContext(span.SpanContext()))
}

// SerializeSpanContext serializes a span context into a simple string
func SerializeSpanContext(ctx trace.SpanContext) string {
	return fmt.Sprintf("%s;%s;%d", ctx.SpanID.String(), ctx.TraceID.String(), ctx.TraceOptions)
}

func configureTestTraceExporter(buffer *string) {
	exporter := testtrace.NewStringExporter(buffer, logger.NewLogger("fakeLogger"))
	exporter.Register("fakeID")
}

func startTestServerWithTracing(port int) (*grpc.Server, *string) {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

	var buffer = ""
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

func mustMarshalAny(msg proto.Message) *any.Any {
	any, err := ptypes.MarshalAny(msg)
	if err != nil {
		panic(fmt.Sprintf("ptypes.MarshalAny(%+v) failed: %v", msg, err))
	}
	return any
}

func TestAPIToken(t *testing.T) {
	mockDirectMessaging := new(daprt.MockDirectMessaging)

	// Setup Dapr API server
	fakeAPI := &api{
		id:              "fakeAPI",
		directMessaging: mockDirectMessaging,
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
				Data:   &any.Any{Value: []byte("testData")},
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
				Data:   &any.Any{Value: []byte("testData")},
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
				Data:   &any.Any{Value: []byte("testData")},
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
	}

	var httpResponseTests = []struct {
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
					Data:   &any.Any{Value: []byte("testData")},
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
	}

	t.Run("handle grpc response code", func(t *testing.T) {
		fakeResp := invokev1.NewInvokeMethodResponse(
			int32(codes.Unimplemented), "Unimplemented",
			[]*any.Any{
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
				Data:   &any.Any{Value: []byte("testData")},
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
			AllowedSecrets: []string{"good-key"},
		},
		"store3": {
			DefaultAccess:  config.AllowAccess,
			AllowedSecrets: []string{"error-key", "good-key"},
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
			key:              "good-key",
			errorExcepted:    false,
			expectedResponse: expectedResponse,
		},
		{
			testName:         "Good Key default access",
			storeName:        storeName,
			key:              "good-key",
			errorExcepted:    false,
			expectedResponse: expectedResponse,
		},
		{
			testName:         "Good Key restricted store access",
			storeName:        restrictedStore,
			key:              "good-key",
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
			key:              "good-key",
			errorExcepted:    false,
			expectedResponse: expectedResponse,
		},
	}
	// Setup Dapr API server
	fakeAPI := &api{
		id:                   "fakeAPI",
		secretStores:         fakeStores,
		secretsConfiguration: secretsConfiguration,
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
				assert.Equal(t, resp.Data[tt.key], tt.expectedResponse, "Expected responses to be same")
			} else {
				assert.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestGetStateWhenStoreNotConfigured(t *testing.T) {
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, &api{id: "fakeAPI"}, "")
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
		return reqs[0].Key == "fakeAPI||good-key"
	})).Return(nil)
	fakeStore.On("BulkSet", mock.MatchedBy(func(reqs []state.SetRequest) bool {
		if len(reqs) == 0 {
			return false
		}
		return reqs[0].Key == "fakeAPI||error-key"
	})).Return(errors.New("failed to save state with error-key"))

	fakeAPI := &api{
		id:          "fakeAPI",
		stateStores: map[string]state.Store{"store1": fakeStore},
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
		value         string
		errorExcepted bool
		expectedError codes.Code
	}{
		{
			testName:      "save state",
			storeName:     "store1",
			key:           "good-key",
			value:         "value",
			errorExcepted: false,
			expectedError: codes.OK,
		},
		{
			testName:      "save state with non-existing store",
			storeName:     "store2",
			key:           "good-key",
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
	fakeStore := &daprt.MockStateStore{}
	fakeStore.On("Get", mock.MatchedBy(func(req *state.GetRequest) bool {
		return req.Key == "fakeAPI||good-key"
	})).Return(
		&state.GetResponse{
			Data: []byte("test-data"),
			ETag: "test-etag",
		}, nil)
	fakeStore.On("Get", mock.MatchedBy(func(req *state.GetRequest) bool {
		return req.Key == "fakeAPI||error-key"
	})).Return(
		nil,
		errors.New("failed to get state with error-key"))

	fakeAPI := &api{
		id:          "fakeAPI",
		stateStores: map[string]state.Store{"store1": fakeStore},
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
			key:           "good-key",
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
			key:              "good-key",
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

func TestGetBulkState(t *testing.T) {
	fakeStore := &daprt.MockStateStore{}
	fakeStore.On("Get", mock.MatchedBy(func(req *state.GetRequest) bool {
		return req.Key == "fakeAPI||good-key"
	})).Return(
		&state.GetResponse{
			Data: []byte("test-data"),
			ETag: "test-etag",
		}, nil)
	fakeStore.On("Get", mock.MatchedBy(func(req *state.GetRequest) bool {
		return req.Key == "fakeAPI||error-key"
	})).Return(
		nil,
		errors.New("failed to get state with error-key"))

	fakeAPI := &api{
		id:          "fakeAPI",
		stateStores: map[string]state.Store{"store1": fakeStore},
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
			keys:          []string{"good-key", "good-key"},
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
			keys:             []string{"good-key", "good-key"},
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
		return req.Key == "fakeAPI||good-key"
	})).Return(nil)
	fakeStore.On("Delete", mock.MatchedBy(func(req *state.DeleteRequest) bool {
		return req.Key == "fakeAPI||error-key"
	})).Return(errors.New("failed to delete state with key2"))

	fakeAPI := &api{
		id:          "fakeAPI",
		stateStores: map[string]state.Store{"store1": fakeStore},
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
			key:           "good-key",
			errorExcepted: false,
			expectedError: codes.OK,
		},
		{
			testName:      "delete store with non-existing store",
			storeName:     "no-store",
			key:           "good-key",
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
		publishFn: func(req *pubsub.PublishRequest) error {
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
		return matchKeyFn(req, "good-key")
	})).Return(nil)
	fakeStore.On("Multi", mock.MatchedBy(func(req *state.TransactionalStateRequest) bool {
		return matchKeyFn(req, "error-key")
	})).Return(errors.New("error to execute with key2"))

	fakeAPI := &api{
		id:          "fakeAPI",
		stateStores: map[string]state.Store{"store1": fakeStore},
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
			key:           "good-key",
			value:         []byte("1"),
			errorExcepted: false,
			expectedError: codes.OK,
		},
		{
			testName:      "delete operation",
			storeName:     "store1",
			operation:     state.Upsert,
			key:           "good-key",
			errorExcepted: false,
			expectedError: codes.OK,
		},
		{
			testName:      "unknown operation",
			storeName:     "store1",
			operation:     state.OperationType("unknown"),
			key:           "good-key",
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
