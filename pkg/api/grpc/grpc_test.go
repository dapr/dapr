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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	epb "google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcMetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/components-contrib/lock"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/api/grpc/metadata"
	"github.com/dapr/dapr/pkg/api/universal"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpEndpointsV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/expr"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	outboxfake "github.com/dapr/dapr/pkg/outbox/fake"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	wfenginefake "github.com/dapr/dapr/pkg/runtime/wfengine/fake"
	daprt "github.com/dapr/dapr/pkg/testing"
	testtrace "github.com/dapr/dapr/pkg/testing/trace"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

const (
	goodStoreKey    = "fakeAPI||good-key"
	errorStoreKey   = "fakeAPI||error-key"
	etagMismatchKey = "fakeAPI||etag-mismatch"
	etagInvalidKey  = "fakeAPI||etag-invalid"
	goodKey         = "good-key"
	goodKey2        = "good-key2"
	mockSubscribeID = "mockId"
	bufconnBufSize  = 1 << 20 // 1MB
)

var testResiliency = &v1alpha1.Resiliency{
	Spec: v1alpha1.ResiliencySpec{
		Policies: v1alpha1.Policies{
			Retries: map[string]v1alpha1.Retry{
				"singleRetry": {
					MaxRetries:  ptr.Of(1),
					MaxInterval: "100ms",
					Policy:      "constant",
					Duration:    "10ms",
				},
				"tenRetries": {
					MaxRetries:  ptr.Of(10),
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
	resp := invokev1.NewInvokeMethodResponse(0, "", nil).
		WithRawDataBytes(ExtractSpanContext(ctx)).
		WithContentType("text/plain")
	defer resp.Close()
	return resp.ProtoWithData()
}

func (m *mockGRPCAPI) CallLocalStream(stream internalv1pb.ServiceInvocation_CallLocalStreamServer) error { //nolint:nosnakecase
	resp := invokev1.NewInvokeMethodResponse(0, "", nil).
		WithRawDataBytes(ExtractSpanContext(stream.Context())).
		WithContentType("text/plain")
	defer resp.Close()

	pd, err := resp.ProtoWithData()
	if err != nil {
		return err
	}

	stream.Send(&internalv1pb.InternalInvokeResponseStream{
		Response: resp.Proto(),
		Payload: &commonv1pb.StreamPayload{
			Data: pd.GetMessage().GetData().GetValue(),
			Seq:  0,
		},
	})

	return nil
}

func (m *mockGRPCAPI) CallActor(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	resp := invokev1.NewInvokeMethodResponse(0, "", nil).
		WithRawDataBytes(ExtractSpanContext(ctx)).
		WithContentType("text/plain")
	defer resp.Close()
	return resp.ProtoWithData()
}

func (m *mockGRPCAPI) CallActorStream(req *internalv1pb.InternalInvokeRequest, stream internalv1pb.ServiceInvocation_CallActorStreamServer) error {
	return nil
}

func (m *mockGRPCAPI) CallActorReminder(ctx context.Context, in *internalv1pb.Reminder) (*emptypb.Empty, error) {
	return new(emptypb.Empty), nil
}

func (m *mockGRPCAPI) PublishEvent(ctx context.Context, in *runtimev1pb.PublishEventRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (m *mockGRPCAPI) BulkPublishEventAlpha1(ctx context.Context, in *runtimev1pb.BulkPublishRequest) (*runtimev1pb.BulkPublishResponse, error) {
	return &runtimev1pb.BulkPublishResponse{}, nil
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
	span := diagUtils.SpanFromContext(ctx)
	return []byte(SerializeSpanContext(span.SpanContext()))
}

// SerializeSpanContext serializes a span context into a simple string.
func SerializeSpanContext(ctx trace.SpanContext) string {
	return fmt.Sprintf("%s;%s;%d", ctx.SpanID(), ctx.TraceID(), ctx.TraceFlags())
}

func configureTestTraceExporter(buffer *string) {
	exporter := testtrace.NewStringExporter(buffer, logger.NewLogger("fakeLogger"))
	exporter.Register("fakeID")
}

func startTestServerWithTracing() (*grpc.Server, *string, *bufconn.Listener) {
	lis := bufconn.Listen(bufconnBufSize)

	var buffer string
	configureTestTraceExporter(&buffer)

	spec := config.TracingSpec{SamplingRate: "1"}
	server := grpc.NewServer(
		grpc.UnaryInterceptor(grpcMiddleware.ChainUnaryServer(diag.GRPCTraceUnaryServerInterceptor("id", spec))),
	)

	go func() {
		internalv1pb.RegisterServiceInvocationServer(server, &mockGRPCAPI{})
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	return server, &buffer, lis
}

func startTestServerAPI(t *testing.T, srv runtimev1pb.DaprServer) *bufconn.Listener {
	lis := bufconn.Listen(bufconnBufSize)

	server := grpc.NewServer(
		grpc.UnaryInterceptor(metadata.SetMetadataInContextUnary),
	)
	runtimev1pb.RegisterDaprServer(server, srv)

	errCh := make(chan error)
	t.Cleanup(func() {
		server.Stop()
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			t.Fatalf("timeout waiting for server to stop")
		}
	})
	go func() {
		errCh <- server.Serve(lis)
	}()

	return lis
}

func startInternalServer(testAPIServer *api) (*grpc.Server, *bufconn.Listener) {
	lis := bufconn.Listen(bufconnBufSize)

	server := grpc.NewServer()
	go func() {
		internalv1pb.RegisterServiceInvocationServer(server, testAPIServer)
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	return server, lis
}

func startDaprAPIServer(t *testing.T, testAPIServer *api, token string) *bufconn.Listener {
	lis := bufconn.Listen(bufconnBufSize)

	interceptors := []grpc.UnaryServerInterceptor{
		metadata.SetMetadataInContextUnary,
	}
	streamInterceptors := []grpc.StreamServerInterceptor{}
	if token != "" {
		unary, stream := getAPIAuthenticationMiddlewares(token, "dapr-api-token")
		interceptors = append(interceptors, unary)
		streamInterceptors = append(streamInterceptors, stream)
	}
	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(interceptors...),
		grpc.ChainStreamInterceptor(streamInterceptors...),
		grpc.InTapHandle(metadata.SetMetadataInTapHandle),
	}

	server := grpc.NewServer(opts...)
	errCh := make(chan error, 1)
	t.Cleanup(func() {
		server.Stop()
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for server to stop")
		}
	})
	go func() {
		runtimev1pb.RegisterDaprServer(server, testAPIServer)
		errCh <- server.Serve(lis)
	}()

	return lis
}

func createTestClient(lis *bufconn.Listener) *grpc.ClientConn {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext( //nolint:staticcheck
		ctx, "bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck
	)
	if err != nil {
		panic(err)
	}
	return conn
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
	log := logger.NewLogger("api")
	fakeAPI := &api{
		logger: log,
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Resiliency: resiliency.New(nil),
			Logger:     log,
			Actors: fake.New().WithRouter(func(context.Context) (router.Interface, error) {
				return nil, messages.ErrActorRuntimeNotFound
			}),
			WorkflowEngine: wfenginefake.New(),
		}),
		directMessaging: mockDirectMessaging,
	}

	t.Run("valid token", func(t *testing.T) {
		token := "1234"

		fakeResp := invokev1.NewInvokeMethodResponse(404, "NotFound", nil).
			WithRawDataString("fakeDirectMessageResponse").
			WithContentType("application/json")
		defer fakeResp.Close()

		// Set up direct messaging mock
		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(matchContextInterface),
			"fakeAppID",
			mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil).Once()

		// Run test server
		lis := startDaprAPIServer(t, fakeAPI, token)

		// Create gRPC test client
		clientConn := createTestClient(lis)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)
		md := grpcMetadata.Pairs("dapr-api-token", token)
		ctx := grpcMetadata.NewOutgoingContext(t.Context(), md)

		t.Run("unary", func(t *testing.T) {
			// act
			req := &runtimev1pb.InvokeServiceRequest{
				Id: "fakeAppID",
				Message: &commonv1pb.InvokeRequest{
					Method: "fakeMethod",
					Data:   &anypb.Any{Value: []byte("testData")},
				},
			}
			_, err := client.InvokeService(ctx, req)

			// assert
			mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
			s, ok := status.FromError(err)
			assert.True(t, ok)
			assert.Equal(t, codes.NotFound, s.Code())
			assert.Equal(t, "Not Found", s.Message())

			errInfo := s.Details()[0].(*epb.ErrorInfo)
			assert.Len(t, s.Details(), 1)
			assert.Equal(t, "404", errInfo.GetMetadata()["http.code"])
			assert.Equal(t, "fakeDirectMessageResponse", errInfo.GetMetadata()["http.error_message"])
		})

		t.Run("stream", func(t *testing.T) {
			// We use a low-level gRPC method to invoke a method as a stream (even unary methods are streams, internally)
			stream, err := clientConn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}, "/"+runtimev1pb.Dapr_ServiceDesc.ServiceName+"/InvokeActor")
			require.NoError(t, err)
			defer stream.CloseSend()

			// Send a message in the stream since it will be waiting for our input
			err = stream.SendMsg(&emptypb.Empty{})
			require.NoError(t, err)

			// The request was invalid so we should get an error about the actor runtime (which means we passed the auth middleware and are hitting the API as expected)
			var m any
			err = stream.RecvMsg(&m)
			require.Error(t, err)
			require.ErrorContains(t, err, "actor runtime")
		})
	})

	t.Run("invalid token", func(t *testing.T) {
		token := "1234"

		fakeResp := invokev1.NewInvokeMethodResponse(404, "NotFound", nil).
			WithRawDataString("fakeDirectMessageResponse").
			WithContentType("application/json")
		defer fakeResp.Close()

		// Set up direct messaging mock
		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(matchContextInterface),
			"fakeAppID",
			mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil).Once()

		lis := startDaprAPIServer(t, fakeAPI, token)

		// Create gRPC test client
		clientConn := createTestClient(lis)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)
		md := grpcMetadata.Pairs("dapr-api-token", "bad, bad token")
		ctx := grpcMetadata.NewOutgoingContext(t.Context(), md)

		t.Run("unary", func(t *testing.T) {
			// act
			req := &runtimev1pb.InvokeServiceRequest{
				Id: "fakeAppID",
				Message: &commonv1pb.InvokeRequest{
					Method: "fakeMethod",
					Data:   &anypb.Any{Value: []byte("testData")},
				},
			}
			_, err := client.InvokeService(ctx, req)

			// assert
			mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 0)
			s, ok := status.FromError(err)
			assert.True(t, ok)
			assert.Equal(t, codes.Unauthenticated, s.Code())
		})

		t.Run("stream", func(t *testing.T) {
			// We use a low-level gRPC method to invoke a method as a stream (even unary methods are streams, internally)
			stream, err := clientConn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}, "/"+runtimev1pb.Dapr_ServiceDesc.ServiceName+"/InvokeActor")
			require.NoError(t, err)
			defer stream.CloseSend()

			// Send a message in the stream since it will be waiting for our input
			err = stream.SendMsg(&emptypb.Empty{})
			require.NoError(t, err)

			// We should get an Unauthenticated error
			var m any
			err = stream.RecvMsg(&m)
			require.Error(t, err)
			s, ok := status.FromError(err)
			assert.True(t, ok)
			assert.Equal(t, codes.Unauthenticated, s.Code())
		})
	})

	t.Run("missing token", func(t *testing.T) {
		token := "1234"

		fakeResp := invokev1.NewInvokeMethodResponse(404, "NotFound", nil).
			WithRawDataString("fakeDirectMessageResponse").
			WithContentType("application/json")
		defer fakeResp.Close()

		// Set up direct messaging mock
		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(matchContextInterface),
			"fakeAppID",
			mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil).Once()

		// Run test server
		lis := startDaprAPIServer(t, fakeAPI, token)

		// Create gRPC test client
		clientConn := createTestClient(lis)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)
		ctx := t.Context()

		t.Run("unary", func(t *testing.T) {
			// act
			req := &runtimev1pb.InvokeServiceRequest{
				Id: "fakeAppID",
				Message: &commonv1pb.InvokeRequest{
					Method: "fakeMethod",
					Data:   &anypb.Any{Value: []byte("testData")},
				},
			}
			_, err := client.InvokeService(ctx, req)

			// assert
			mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 0)
			s, ok := status.FromError(err)
			assert.True(t, ok)
			assert.Equal(t, codes.Unauthenticated, s.Code())
		})

		t.Run("stream", func(t *testing.T) {
			// We use a low-level gRPC method to invoke a method as a stream (even unary methods are streams, internally)
			stream, err := clientConn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}, "/"+runtimev1pb.Dapr_ServiceDesc.ServiceName+"/InvokeActor")
			require.NoError(t, err)
			defer stream.CloseSend()

			// Send a message in the stream since it will be waiting for our input
			err = stream.SendMsg(&emptypb.Empty{})
			require.NoError(t, err)

			// We should get an Unauthenticated error
			var m any
			err = stream.RecvMsg(&m)
			require.Error(t, err)
			s, ok := status.FromError(err)
			assert.True(t, ok)
			assert.Equal(t, codes.Unauthenticated, s.Code())
		})
	})
}

func TestInvokeServiceFromHTTPResponse(t *testing.T) {
	mockDirectMessaging := new(daprt.MockDirectMessaging)

	// Setup Dapr API server
	fakeAPI := &api{
		logger: logger.NewLogger("test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Resiliency: resiliency.New(nil),
		}),
		directMessaging: mockDirectMessaging,
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
			// TODO: fix types
			//nolint:gosec
			fakeResp := invokev1.NewInvokeMethodResponse(int32(tt.status), tt.statusMessage, nil).
				WithRawDataString(tt.errHTTPMessage).
				WithContentType("application/json")
			defer fakeResp.Close()

			// Set up direct messaging mock
			mockDirectMessaging.Calls = nil // reset call count
			mockDirectMessaging.On("Invoke",
				mock.MatchedBy(matchContextInterface),
				"fakeAppID",
				mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil).Once()

			lis := startDaprAPIServer(t, fakeAPI, "")

			// Create gRPC test client
			clientConn := createTestClient(lis)
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
			var header grpcMetadata.MD
			_, err := client.InvokeService(t.Context(), req, grpc.Header(&header))

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
				assert.Len(t, s.Details(), 1)
				assert.Equal(t, tt.errHTTPCode, errInfo.GetMetadata()["http.code"])
				assert.Equal(t, tt.errHTTPMessage, errInfo.GetMetadata()["http.error_message"])
			}
		})
	}
}

func TestInvokeServiceFromGRPCResponse(t *testing.T) {
	mockDirectMessaging := new(daprt.MockDirectMessaging)

	// Setup Dapr API server
	fakeAPI := &api{
		logger: logger.NewLogger("test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Resiliency: resiliency.New(nil),
		}),
		directMessaging: mockDirectMessaging,
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
		).
			WithRawDataString("fakeDirectMessageResponse").
			WithContentType("application/json")
		defer fakeResp.Close()

		// Set up direct messaging mock
		mockDirectMessaging.Calls = nil // reset call count
		mockDirectMessaging.On("Invoke",
			mock.MatchedBy(matchContextInterface),
			"fakeAppID",
			mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil).Once()

		// Run test server
		lis := startDaprAPIServer(t, fakeAPI, "")

		// Create gRPC test client
		clientConn := createTestClient(lis)
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
		_, err := client.InvokeService(t.Context(), req)

		// assert
		mockDirectMessaging.AssertNumberOfCalls(t, "Invoke", 1)
		s, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Unimplemented, s.Code())
		assert.Equal(t, "Unimplemented", s.Message())

		errInfo := s.Details()[0].(*epb.ResourceInfo)
		assert.Len(t, s.Details(), 1)
		assert.Equal(t, "sidecar", errInfo.GetResourceType())
		assert.Equal(t, "invoke/service", errInfo.GetResourceName())
		assert.Equal(t, "Dapr", errInfo.GetOwner())
	})
}

func TestSecretStoreNotConfigured(t *testing.T) {
	lis := startDaprAPIServer(t, &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			Logger:    logger.NewLogger("grpc.api.test"),
			AppID:     "fakeAPI",
			CompStore: compstore.New(),
		}),
	}, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err := client.GetSecret(t.Context(), &runtimev1pb.GetSecretRequest{})
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

	compStore := compstore.New()
	for name, store := range fakeStores {
		compStore.AddSecretStore(name, store)
	}
	for name, conf := range secretsConfiguration {
		compStore.AddSecretsConfiguration(name, conf)
	}

	// Setup Dapr API server
	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			Resiliency: resiliency.New(nil),
			CompStore:  compStore,
		}),
	}
	// Run test server
	lis := startDaprAPIServer(t, fakeAPI, "")

	// Create gRPC test client
	clientConn := createTestClient(lis)
	defer clientConn.Close()

	// act
	client := runtimev1pb.NewDaprClient(clientConn)

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetSecretRequest{
				StoreName: tt.storeName,
				Key:       tt.key,
			}
			resp, err := client.GetSecret(t.Context(), req)

			if !tt.errorExcepted {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResponse, resp.GetData()[tt.key], "Expected responses to be same")
			} else {
				require.Error(t, err, "Expected error")
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

	compStore := compstore.New()
	for name, store := range fakeStores {
		compStore.AddSecretStore(name, store)
	}
	for name, conf := range secretsConfiguration {
		compStore.AddSecretsConfiguration(name, conf)
	}

	// Setup Dapr API server
	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			Resiliency: resiliency.New(nil),
			CompStore:  compStore,
		}),
	}
	// Run test server
	lis := startDaprAPIServer(t, fakeAPI, "")

	// Create gRPC test client
	clientConn := createTestClient(lis)
	defer clientConn.Close()

	// act
	client := runtimev1pb.NewDaprClient(clientConn)

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetBulkSecretRequest{
				StoreName: tt.storeName,
			}
			resp, err := client.GetBulkSecret(t.Context(), req)

			if !tt.errorExcepted {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResponse, resp.GetData()[tt.key].GetSecrets()[tt.key], "Expected responses to be same")
			} else {
				require.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestGetStateWhenStoreNotConfigured(t *testing.T) {
	lis := startDaprAPIServer(t, &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			CompStore:  compstore.New(),
			Resiliency: resiliency.New(nil),
		}),
	}, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err := client.GetState(t.Context(), &runtimev1pb.GetStateRequest{})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestSaveState(t *testing.T) {
	etagMismatchErr := state.NewETagError(state.ETagMismatch, errors.New("simulated"))
	etagInvalidErr := state.NewETagError(state.ETagInvalid, errors.New("simulated"))

	fakeStore := &daprt.MockStateStore{}
	fakeStore.On("Set",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.SetRequest) bool {
			return req.Key == goodStoreKey
		}),
	).Return(nil)
	fakeStore.On("Set",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.SetRequest) bool {
			return req.Key == errorStoreKey
		}),
	).Return(errors.New("failed to save state with error-key"))
	fakeStore.On("Set",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.SetRequest) bool {
			return req.Key == etagMismatchKey
		}),
	).Return(etagMismatchErr)
	fakeStore.On("Set",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.SetRequest) bool {
			return req.Key == etagInvalidKey
		}),
	).Return(etagInvalidErr)
	fakeStore.On("BulkSet",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req []state.SetRequest) bool {
			return len(req) == 2 &&
				req[0].Key == goodStoreKey &&
				req[1].Key == goodStoreKey+"2"
		}),
		mock.Anything,
	).Return(nil)
	fakeStore.On("BulkSet",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req []state.SetRequest) bool {
			return len(req) == 2 &&
				req[0].Key == goodStoreKey &&
				req[1].Key == etagMismatchKey
		}),
		mock.Anything,
	).Return(errors.Join(state.NewBulkStoreError(etagMismatchKey, etagMismatchErr)))
	fakeStore.On("BulkSet",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req []state.SetRequest) bool {
			return len(req) == 3 &&
				req[0].Key == goodStoreKey &&
				req[1].Key == etagMismatchKey &&
				req[2].Key == etagInvalidKey
		}),
		mock.Anything,
	).Return(errors.Join(
		state.NewBulkStoreError(etagMismatchKey, etagMismatchErr),
		state.NewBulkStoreError(etagInvalidKey, etagInvalidErr),
	))

	compStore := compstore.New()
	compStore.AddStateStore("store1", fakeStore)

	// Setup dapr api server
	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	lis := startDaprAPIServer(t, fakeAPI, "")

	// create client
	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName     string
		storeName    string
		states       []*commonv1pb.StateItem
		expectedCode codes.Code
	}{
		{
			testName:     "save state with non-existing store",
			storeName:    "store2",
			expectedCode: codes.InvalidArgument,
		},
		{
			testName:  "single - save state",
			storeName: "store1",
			states: []*commonv1pb.StateItem{
				{
					Key:   goodKey,
					Value: []byte("value"),
				},
			},
			expectedCode: codes.OK,
		},
		{
			testName:  "single - save state but error occurs",
			storeName: "store1",
			states: []*commonv1pb.StateItem{
				{
					Key:   "error-key",
					Value: []byte("value"),
				},
			},
			expectedCode: codes.Internal,
		},
		{
			testName:  "single - save state with etag mismatch",
			storeName: "store1",
			states: []*commonv1pb.StateItem{
				{
					Key:   "etag-mismatch",
					Value: []byte("value"),
				},
			},
			expectedCode: codes.Aborted,
		},
		{
			testName:  "single - save state with etag invalid",
			storeName: "store1",
			states: []*commonv1pb.StateItem{
				{
					Key:   "etag-invalid",
					Value: []byte("value"),
				},
			},
			expectedCode: codes.InvalidArgument,
		},
		{
			testName:  "multi - save state",
			storeName: "store1",
			states: []*commonv1pb.StateItem{
				{
					Key:   goodKey,
					Value: []byte("value"),
				},
				{
					Key:   goodKey + "2",
					Value: []byte("value"),
				},
			},
			expectedCode: codes.OK,
		},
		{
			testName:  "multi - save state with etag mismatch",
			storeName: "store1",
			states: []*commonv1pb.StateItem{
				{
					Key:   goodKey,
					Value: []byte("value"),
				},
				{
					Key:   "etag-mismatch",
					Value: []byte("value"),
				},
			},
			expectedCode: codes.Aborted,
		},
		{
			testName:  "multi - save state with etag mismatch and etag invalid",
			storeName: "store1",
			states: []*commonv1pb.StateItem{
				{
					Key:   goodKey,
					Value: []byte("value"),
				},
				{
					Key:   "etag-mismatch",
					Value: []byte("value"),
				},
				{
					Key:   "etag-invalid",
					Value: []byte("value"),
				},
			},
			expectedCode: codes.InvalidArgument,
		},
	}

	// test and assert
	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			_, err := client.SaveState(t.Context(), &runtimev1pb.SaveStateRequest{
				StoreName: tt.storeName,
				States:    tt.states,
			})
			if tt.expectedCode == codes.OK {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.expectedCode, status.Code(err))
			}
		})
	}
}

func TestGetState(t *testing.T) {
	// Setup mock store
	fakeStore := &daprt.MockStateStore{}
	fakeStore.On("Get", mock.MatchedBy(matchContextInterface), mock.MatchedBy(func(req *state.GetRequest) bool {
		return req.Key == goodStoreKey
	})).Return(
		&state.GetResponse{
			Data: []byte("test-data"),
			ETag: ptr.Of("test-etag"),
		}, nil)
	fakeStore.On("Get", mock.MatchedBy(matchContextInterface), mock.MatchedBy(func(req *state.GetRequest) bool {
		return req.Key == errorStoreKey
	})).Return(
		nil,
		errors.New("failed to get state with error-key"))

	compStore := compstore.New()
	compStore.AddStateStore("store1", fakeStore)

	// Setup dapr api server
	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	lis := startDaprAPIServer(t, fakeAPI, "")

	clientConn := createTestClient(lis)
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

			resp, err := client.GetState(t.Context(), req)
			if !tt.errorExcepted {
				require.NoError(t, err)
				assert.Equal(t, resp.GetData(), tt.expectedResponse.GetData(), "Expected response Data to be same")
				assert.Equal(t, resp.GetEtag(), tt.expectedResponse.GetEtag(), "Expected response Etag to be same")
			} else {
				require.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestGetConfiguration(t *testing.T) {
	fakeConfigurationStore := &daprt.MockConfigurationStore{}
	fakeConfigurationStore.On("Get",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *configuration.GetRequest) bool {
			return req.Keys[0] == goodKey
		})).Return(
		&configuration.GetResponse{
			Items: map[string]*configuration.Item{
				goodKey: {
					Value: "test-data",
				},
			},
		}, nil)
	fakeConfigurationStore.On("Get",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *configuration.GetRequest) bool {
			return req.Keys[0] == "good-key1" && req.Keys[1] == goodKey2 && req.Keys[2] == "good-key3"
		})).Return(
		&configuration.GetResponse{
			Items: map[string]*configuration.Item{
				"good-key1": {
					Value: "test-data",
				},
				goodKey2: {
					Value: "test-data",
				},
				"good-key3": {
					Value: "test-data",
				},
			},
		}, nil)
	fakeConfigurationStore.On("Get",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *configuration.GetRequest) bool {
			return req.Keys[0] == "error-key"
		})).Return(
		nil,
		errors.New("failed to get state with error-key"))

	compStore := compstore.New()
	compStore.AddConfiguration("store1", fakeConfigurationStore)
	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	lis := startDaprAPIServer(t, fakeAPI, "")

	clientConn := createTestClient(lis)
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
				Items: map[string]*commonv1pb.ConfigurationItem{
					goodKey: {
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
				Items: map[string]*commonv1pb.ConfigurationItem{
					"good-key1": {
						Value: "test-data",
					},
					goodKey2: {
						Value: "test-data",
					},
					"good-key3": {
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
		// Testing alpha1 endpoint
		t.Run(tt.testName+"-alpha1", func(t *testing.T) {
			req := &runtimev1pb.GetConfigurationRequest{
				StoreName: tt.storeName,
				Keys:      tt.keys,
			}

			resp, err := client.GetConfigurationAlpha1(t.Context(), req)
			if !tt.errorExcepted {
				require.NoError(t, err)
				assert.Equal(t, resp.GetItems(), tt.expectedResponse.GetItems(), "Expected response items to be same")
			} else {
				require.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
		// Testing stable endpoint
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetConfigurationRequest{
				StoreName: tt.storeName,
				Keys:      tt.keys,
			}

			resp, err := client.GetConfiguration(t.Context(), req)
			if !tt.errorExcepted {
				require.NoError(t, err)
				assert.Equal(t, resp.GetItems(), tt.expectedResponse.GetItems(), "Expected response items to be same")
			} else {
				require.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestSubscribeConfiguration(t *testing.T) {
	fakeConfigurationStore := &daprt.MockConfigurationStore{}
	var tempReq *configuration.SubscribeRequest

	fakeConfigurationStore.On("Subscribe",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *configuration.SubscribeRequest) bool {
			tempReq = req
			return len(req.Keys) == 1 && req.Keys[0] == goodKey
		}),
		mock.MatchedBy(func(f configuration.UpdateHandler) bool {
			if len(tempReq.Keys) == 1 && tempReq.Keys[0] == goodKey {
				go f(t.Context(), &configuration.UpdateEvent{
					Items: map[string]*configuration.Item{
						goodKey: {
							Value: "test-data",
						},
					},
				})
			}
			return true
		}),
	).Return("id1", nil)
	fakeConfigurationStore.On("Unsubscribe",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *configuration.UnsubscribeRequest) bool {
			return req.ID == "id1"
		}),
	).Return(nil)

	fakeConfigurationStore.On("Subscribe",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *configuration.SubscribeRequest) bool {
			tempReq = req
			return len(req.Keys) == 2 && req.Keys[0] == goodKey && req.Keys[1] == goodKey2
		}),
		mock.MatchedBy(func(f configuration.UpdateHandler) bool {
			if len(tempReq.Keys) == 2 && tempReq.Keys[0] == goodKey && tempReq.Keys[1] == goodKey2 {
				go f(t.Context(), &configuration.UpdateEvent{
					Items: map[string]*configuration.Item{
						goodKey: {
							Value: "test-data",
						},
						goodKey2: {
							Value: "test-data2",
						},
					},
				})
			}
			return true
		}),
	).Return("id2", nil)
	fakeConfigurationStore.On("Unsubscribe",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *configuration.UnsubscribeRequest) bool {
			return req.ID == "id2"
		}),
	).Return(nil)

	fakeConfigurationStore.On("Subscribe",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *configuration.SubscribeRequest) bool {
			return req.Keys[0] == "error-key"
		}),
		mock.AnythingOfType("configuration.UpdateHandler"),
	).Return(nil, errors.New("failed to get state with error-key"))

	compStore := compstore.New()
	compStore.AddConfiguration("store1", fakeConfigurationStore)

	// Setup dapr api server
	fakeAPI := &api{
		logger: logger.NewLogger("test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	lis := startDaprAPIServer(t, fakeAPI, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName         string
		storeName        string
		keys             []string
		errorExcepted    bool
		expectedResponse map[string]*commonv1pb.ConfigurationItem
		expectedError    codes.Code
	}{
		{
			testName:      "get store with single key",
			storeName:     "store1",
			keys:          []string{goodKey},
			errorExcepted: false,
			expectedResponse: map[string]*commonv1pb.ConfigurationItem{
				goodKey: {
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
			expectedResponse: map[string]*commonv1pb.ConfigurationItem{},
			expectedError:    codes.InvalidArgument,
		},
		{
			testName:         "get store with key but error occurs",
			storeName:        "store1",
			keys:             []string{"error-key"},
			errorExcepted:    true,
			expectedResponse: map[string]*commonv1pb.ConfigurationItem{},
			expectedError:    codes.InvalidArgument,
		},
		{
			testName:      "get store with multi keys",
			storeName:     "store1",
			keys:          []string{goodKey, goodKey2},
			errorExcepted: false,
			expectedResponse: map[string]*commonv1pb.ConfigurationItem{
				goodKey: {
					Value: "test-data",
				},
				goodKey2: {
					Value: "test-data2",
				},
			},
			expectedError: codes.OK,
		},
	}

	for _, tt := range testCases {
		testFn := func(subscribeFn subscribeConfigurationFn) func(t *testing.T) {
			return func(t *testing.T) {
				req := &runtimev1pb.SubscribeConfigurationRequest{
					StoreName: tt.storeName,
					Keys:      tt.keys,
				}

				resp, _ := subscribeFn(t.Context(), req)

				if !tt.errorExcepted {
					// First message should contain the ID only
					rsp, err := resp.Recv()
					require.NoError(t, err)
					require.NotNil(t, rsp)
					require.NotEmpty(t, rsp.GetId())
					require.Empty(t, rsp.GetItems())

					rsp, err = resp.Recv()
					require.NoError(t, err)
					assert.Equal(t, tt.expectedResponse, rsp.GetItems(), "Expected response items to be same")
				} else {
					const retry = 3
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
					require.Error(t, err, "Expected error")
				}
			}
		}

		t.Run(tt.testName+"-alpha1", testFn(func(ctx context.Context, in *runtimev1pb.SubscribeConfigurationRequest, opts ...grpc.CallOption) (interface {
			Recv() (*runtimev1pb.SubscribeConfigurationResponse, error)
		}, error,
		) {
			return client.SubscribeConfigurationAlpha1(ctx, in, opts...)
		}))

		t.Run(tt.testName, testFn(func(ctx context.Context, in *runtimev1pb.SubscribeConfigurationRequest, opts ...grpc.CallOption) (interface {
			Recv() (*runtimev1pb.SubscribeConfigurationResponse, error)
		}, error,
		) {
			return client.SubscribeConfiguration(ctx, in, opts...)
		}))
	}
}

func TestUnSubscribeConfiguration(t *testing.T) {
	fakeConfigurationStore := &daprt.MockConfigurationStore{}
	stop := make(chan struct{})
	defer close(stop)

	var tempReq *configuration.SubscribeRequest
	fakeConfigurationStore.On("Unsubscribe",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *configuration.UnsubscribeRequest) bool {
			return true
		}),
	).Return(nil)

	fakeConfigurationStore.On("Subscribe",
		mock.MatchedBy(matchContextInterface),
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
					if err := f(t.Context(), &configuration.UpdateEvent{
						Items: map[string]*configuration.Item{
							goodKey: {
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
		}),
	).Return(mockSubscribeID, nil)

	fakeConfigurationStore.On("Subscribe",
		mock.MatchedBy(matchContextInterface),
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
					if err := f(t.Context(), &configuration.UpdateEvent{
						Items: map[string]*configuration.Item{
							goodKey: {
								Value: "test-data",
							},
							goodKey2: {
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
		}),
	).Return(mockSubscribeID, nil)

	compStore := compstore.New()
	compStore.AddConfiguration("store1", fakeConfigurationStore)

	// Setup dapr api server
	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			CompStore:  compStore,
			Logger:     logger.NewLogger("grpc.api.test"),
			Resiliency: resiliency.New(nil),
		}),
	}
	lis := startDaprAPIServer(t, fakeAPI, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName         string
		storeName        string
		keys             []string
		expectedResponse map[string]*commonv1pb.ConfigurationItem
		expectedError    codes.Code
	}{
		{
			testName:  "Test unsubscribe",
			storeName: "store1",
			keys:      []string{goodKey},
			expectedResponse: map[string]*commonv1pb.ConfigurationItem{
				goodKey: {
					Value: "test-data",
				},
			},
			expectedError: codes.OK,
		},
		{
			testName:  "Test unsubscribe with multi keys",
			storeName: "store1",
			keys:      []string{goodKey, goodKey2},
			expectedResponse: map[string]*commonv1pb.ConfigurationItem{
				goodKey: {
					Value: "test-data",
				},
				goodKey2: {
					Value: "test-data2",
				},
			},
			expectedError: codes.OK,
		},
	}

	for _, tt := range testCases {
		// Testing alpha1 endpoint
		t.Run(tt.testName+"-alpha1", func(t *testing.T) {
			req := &runtimev1pb.SubscribeConfigurationRequest{
				StoreName: tt.storeName,
				Keys:      tt.keys,
			}

			resp, err := client.SubscribeConfigurationAlpha1(t.Context(), req)
			require.NoError(t, err, "Error should be nil")
			const retry = 3
			count := 0
			var subscribeID string
			for {
				if count > retry {
					break
				}
				count++
				time.Sleep(time.Millisecond * 10)
				rsp, recvErr := resp.Recv()
				require.NoError(t, recvErr)
				require.NotNil(t, rsp)
				if rsp.GetItems() != nil {
					assert.Equal(t, tt.expectedResponse, rsp.GetItems())
				} else {
					assert.Equal(t, mockSubscribeID, rsp.GetId())
				}
				subscribeID = rsp.GetId()
			}
			require.NoError(t, err, "Error should be nil")
			_, err = client.UnsubscribeConfigurationAlpha1(t.Context(), &runtimev1pb.UnsubscribeConfigurationRequest{
				StoreName: tt.storeName,
				Id:        subscribeID,
			})
			require.NoError(t, err, "Error should be nil")
			count = 0
			for {
				if errors.Is(err, io.EOF) {
					break
				}
				if count > retry {
					break
				}
				count++
				time.Sleep(time.Millisecond * 10)
				_, err = resp.Recv()
			}
			require.ErrorIs(t, err, io.EOF, "Unsubscribed channel should returns EOF")
		})

		// Testing stable endpoint
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.SubscribeConfigurationRequest{
				StoreName: tt.storeName,
				Keys:      tt.keys,
			}

			resp, err := client.SubscribeConfiguration(t.Context(), req)
			require.NoError(t, err, "Error should be nil")
			const retry = 3
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
				require.NoError(t, recvErr)
				if rsp.GetItems() != nil {
					assert.Equal(t, tt.expectedResponse, rsp.GetItems())
				} else {
					assert.Equal(t, mockSubscribeID, rsp.GetId())
				}
				subscribeID = rsp.GetId()
			}
			require.NoError(t, err, "Error should be nil")
			_, err = client.UnsubscribeConfiguration(t.Context(), &runtimev1pb.UnsubscribeConfigurationRequest{
				StoreName: tt.storeName,
				Id:        subscribeID,
			})
			require.NoError(t, err, "Error should be nil")
			count = 0
			for {
				if errors.Is(err, io.EOF) {
					break
				}
				if count > retry {
					break
				}
				count++
				time.Sleep(time.Millisecond * 10)
				_, err = resp.Recv()
			}
			require.ErrorIs(t, err, io.EOF, "Unsubscribed channel should returns EOF")
		})
	}
}

func TestUnsubscribeConfigurationErrScenario(t *testing.T) {
	fakeConfigurationStore := &daprt.MockConfigurationStore{}
	fakeConfigurationStore.On("Unsubscribe",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *configuration.UnsubscribeRequest) bool {
			return req.ID == mockSubscribeID
		}),
	).Return(nil)

	compStore := compstore.New()
	compStore.AddConfiguration("store1", fakeConfigurationStore)

	// Setup dapr api server
	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			CompStore:  compStore,
			Logger:     logger.NewLogger("grpc.api.test"),
			Resiliency: resiliency.New(nil),
		}),
	}
	lis := startDaprAPIServer(t, fakeAPI, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName         string
		storeName        string
		id               string
		expectedResponse bool
		expectedError    bool
	}{
		{
			testName:         "Test unsubscribe",
			storeName:        "store1",
			id:               "",
			expectedResponse: true,
			expectedError:    false,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.testName+"-alpha1", func(t *testing.T) {
			req := &runtimev1pb.UnsubscribeConfigurationRequest{
				StoreName: tt.storeName,
				Id:        tt.id,
			}

			resp, err := client.UnsubscribeConfigurationAlpha1(t.Context(), req)
			assert.Equal(t, tt.expectedResponse, resp != nil)
			assert.Equal(t, tt.expectedError, err != nil)
		})
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.UnsubscribeConfigurationRequest{
				StoreName: tt.storeName,
				Id:        tt.id,
			}

			resp, err := client.UnsubscribeConfiguration(t.Context(), req)
			assert.Equal(t, tt.expectedResponse, resp != nil)
			assert.Equal(t, tt.expectedError, err != nil)
		})
	}
}

func TestGetBulkState(t *testing.T) {
	fakeStore := &daprt.MockStateStore{}
	fakeStore.On("Get",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.GetRequest) bool {
			return req.Key == goodStoreKey
		})).Return(
		&state.GetResponse{
			Data: []byte("test-data"),
			ETag: ptr.Of("test-etag"),
		}, nil)
	fakeStore.On("Get",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.GetRequest) bool {
			return req.Key == errorStoreKey
		})).Return(
		nil,
		errors.New("failed to get state with error-key"))

	compStore := compstore.New()
	compStore.AddStateStore("store1", fakeStore)

	// Setup dapr api server
	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	lis := startDaprAPIServer(t, fakeAPI, "")

	clientConn := createTestClient(lis)
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

			resp, err := client.GetBulkState(t.Context(), req)
			if !tt.errorExcepted {
				require.NoError(t, err)

				if len(tt.expectedResponse) == 0 {
					assert.Empty(t, resp.GetItems(), "Expected response to be empty")
				} else {
					for i := range resp.GetItems() {
						if tt.expectedResponse[i].GetError() == "" {
							assert.Equal(t, resp.GetItems()[i].GetData(), tt.expectedResponse[i].GetData(), "Expected response Data to be same")
							assert.Equal(t, resp.GetItems()[i].GetEtag(), tt.expectedResponse[i].GetEtag(), "Expected response Etag to be same")
						} else {
							assert.Equal(t, resp.GetItems()[i].GetError(), tt.expectedResponse[i].GetError(), "Expected response error to be same")
						}
					}
				}
			} else {
				require.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestDeleteState(t *testing.T) {
	etagMismatchErr := state.NewETagError(state.ETagMismatch, errors.New("simulated"))
	etagInvalidErr := state.NewETagError(state.ETagInvalid, errors.New("simulated"))

	fakeStore := &daprt.MockStateStore{}
	fakeStore.On("Delete",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.DeleteRequest) bool {
			return req.Key == goodStoreKey
		}),
	).Return(nil)
	fakeStore.On("Delete",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.DeleteRequest) bool {
			return req.Key == errorStoreKey
		}),
	).Return(errors.New("failed to delete state with key2"))
	fakeStore.On("Delete",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.DeleteRequest) bool {
			return req.Key == etagMismatchKey
		}),
	).Return(etagMismatchErr)
	fakeStore.On("Delete",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.DeleteRequest) bool {
			return req.Key == etagInvalidKey
		}),
	).Return(etagInvalidErr)

	compStore := compstore.New()
	compStore.AddStateStore("store1", fakeStore)

	// Setup dapr api server
	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	lis := startDaprAPIServer(t, fakeAPI, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName     string
		storeName    string
		key          string
		expectedCode codes.Code
	}{
		{
			testName:     "delete state",
			storeName:    "store1",
			key:          goodKey,
			expectedCode: codes.OK,
		},
		{
			testName:     "delete state with non-existing store",
			storeName:    "no-store",
			key:          goodKey,
			expectedCode: codes.InvalidArgument,
		},
		{
			testName:     "delete state with key but error occurs",
			storeName:    "store1",
			key:          "error-key",
			expectedCode: codes.Internal,
		},
		{
			testName:     "delete state with etag mismatch",
			storeName:    "store1",
			key:          "etag-mismatch",
			expectedCode: codes.Aborted,
		},
		{
			testName:     "delete state with etag invalid",
			storeName:    "store1",
			key:          "etag-invalid",
			expectedCode: codes.InvalidArgument,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			_, err := client.DeleteState(t.Context(), &runtimev1pb.DeleteStateRequest{
				StoreName: tt.storeName,
				Key:       tt.key,
			})
			if tt.expectedCode == codes.OK {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.expectedCode, status.Code(err))
			}
		})
	}
}

func TestDeleteBulkState(t *testing.T) {
	etagMismatchErr := state.NewETagError(state.ETagMismatch, errors.New("simulated"))
	etagInvalidErr := state.NewETagError(state.ETagInvalid, errors.New("simulated"))

	fakeStore := &daprt.MockStateStore{}
	fakeStore.On("BulkDelete",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req []state.DeleteRequest) bool {
			return len(req) == 2 &&
				req[0].Key == goodStoreKey &&
				req[1].Key == goodStoreKey+"2"
		}),
		mock.Anything,
	).Return(nil)
	fakeStore.On("BulkDelete",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req []state.DeleteRequest) bool {
			return len(req) == 2 &&
				req[0].Key == goodStoreKey &&
				req[1].Key == etagMismatchKey
		}),
		mock.Anything,
	).Return(errors.Join(state.NewBulkStoreError(etagMismatchKey, etagMismatchErr)))
	fakeStore.On("BulkDelete",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req []state.DeleteRequest) bool {
			return len(req) == 3 &&
				req[0].Key == goodStoreKey &&
				req[1].Key == etagMismatchKey &&
				req[2].Key == etagInvalidKey
		}),
		mock.Anything,
	).Return(errors.Join(
		state.NewBulkStoreError(etagMismatchKey, etagMismatchErr),
		state.NewBulkStoreError(etagInvalidKey, etagInvalidErr),
	))

	compStore := compstore.New()
	compStore.AddStateStore("store1", fakeStore)

	// Setup dapr api server
	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}
	lis := startDaprAPIServer(t, fakeAPI, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testCases := []struct {
		testName     string
		storeName    string
		states       []*commonv1pb.StateItem
		expectedCode codes.Code
	}{
		{
			testName:     "delete state with non-existing store",
			storeName:    "store2",
			expectedCode: codes.InvalidArgument,
		},
		{
			testName:  "delete state",
			storeName: "store1",
			states: []*commonv1pb.StateItem{
				{
					Key: goodKey,
				},
				{
					Key: goodKey + "2",
				},
			},
			expectedCode: codes.OK,
		},
		{
			testName:  "delete state with etag mismatch",
			storeName: "store1",
			states: []*commonv1pb.StateItem{
				{
					Key: goodKey,
				},
				{
					Key: "etag-mismatch",
				},
			},
			expectedCode: codes.Aborted,
		},
		{
			testName:  "delete state with etag mismatch and etag invalid",
			storeName: "store1",
			states: []*commonv1pb.StateItem{
				{
					Key: goodKey,
				},
				{
					Key: "etag-mismatch",
				},
				{
					Key: "etag-invalid",
				},
			},
			expectedCode: codes.InvalidArgument,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			_, err := client.DeleteBulkState(t.Context(), &runtimev1pb.DeleteBulkStateRequest{
				StoreName: tt.storeName,
				States:    tt.states,
			})
			if tt.expectedCode == codes.OK {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.expectedCode, status.Code(err))
			}
		})
	}
}

func TestPublishTopic(t *testing.T) {
	srv := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:     "fakeAPI",
			CompStore: compstore.New(),
		}),
		pubsubAdapter: &daprt.MockPubSubAdapter{
			PublishFn: func(ctx context.Context, req *pubsub.PublishRequest) error {
				if req.Topic == "error-topic" {
					return errors.New("error when publish")
				}

				if req.Topic == "err-not-found" {
					return runtimePubsub.NotFoundError{PubsubName: "errnotfound"}
				}

				if req.Topic == "err-not-allowed" {
					return runtimePubsub.NotAllowedError{Topic: req.Topic, ID: "test"}
				}

				return nil
			},
			BulkPublishFn: func(ctx context.Context, req *pubsub.BulkPublishRequest) (pubsub.BulkPublishResponse, error) {
				switch req.Topic {
				case "error-topic":
					return pubsub.BulkPublishResponse{}, errors.New("error when publish")

				case "err-not-found":
					return pubsub.BulkPublishResponse{}, runtimePubsub.NotFoundError{PubsubName: "errnotfound"}

				case "err-not-allowed":
					return pubsub.BulkPublishResponse{}, runtimePubsub.NotAllowedError{Topic: req.Topic, ID: "test"}
				}
				return pubsub.BulkPublishResponse{}, nil
			},
		},
	}

	mock := daprt.MockPubSub{}
	mock.On("Features").Return([]pubsub.Feature{})
	srv.Universal.CompStore().AddPubSub("pubsub", &runtimePubsub.PubsubItem{Component: &mock})

	lis := startTestServerAPI(t, srv)

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	t.Run("err: empty publish event request", func(t *testing.T) {
		_, err := client.PublishEvent(t.Context(), &runtimev1pb.PublishEventRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("err: publish event request with empty topic", func(t *testing.T) {
		_, err := client.PublishEvent(t.Context(), &runtimev1pb.PublishEventRequest{
			PubsubName: "pubsub",
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("no err: publish event request with topic and pubsub alone", func(t *testing.T) {
		_, err := client.PublishEvent(t.Context(), &runtimev1pb.PublishEventRequest{
			PubsubName: "pubsub",
			Topic:      "topic",
		})
		require.NoError(t, err)
	})

	t.Run("no err: publish event request with topic, pubsub and ce metadata override", func(t *testing.T) {
		_, err := client.PublishEvent(t.Context(), &runtimev1pb.PublishEventRequest{
			PubsubName: "pubsub",
			Topic:      "topic",
			Metadata: map[string]string{
				"cloudevent.source": "unit-test",
				"cloudevent.topic":  "overridetopic",  // noop -- if this modified the envelope the test would fail
				"cloudevent.pubsub": "overridepubsub", // noop -- if this modified the envelope the test would fail
			},
		})
		require.NoError(t, err)
	})

	t.Run("err: publish event request with error-topic and pubsub", func(t *testing.T) {
		_, err := client.PublishEvent(t.Context(), &runtimev1pb.PublishEventRequest{
			PubsubName: "pubsub",
			Topic:      "error-topic",
		})
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("err: publish event request with err-not-found topic and pubsub", func(t *testing.T) {
		_, err := client.PublishEvent(t.Context(), &runtimev1pb.PublishEventRequest{
			PubsubName: "pubsub",
			Topic:      "err-not-found",
		})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("err: publish event request with err-not-allowed topic and pubsub", func(t *testing.T) {
		_, err := client.PublishEvent(t.Context(), &runtimev1pb.PublishEventRequest{
			PubsubName: "pubsub",
			Topic:      "err-not-allowed",
		})
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})

	t.Run("err: empty bulk publish event request", func(t *testing.T) {
		_, err := client.BulkPublishEventAlpha1(t.Context(), &runtimev1pb.BulkPublishRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("err: bulk publish event request with duplicate entry Ids", func(t *testing.T) {
		_, err := client.BulkPublishEventAlpha1(t.Context(), &runtimev1pb.BulkPublishRequest{
			PubsubName: "pubsub",
			Topic:      "topic",
			Entries: []*runtimev1pb.BulkPublishRequestEntry{
				{
					Event:       []byte("data"),
					EntryId:     "1",
					ContentType: "text/plain",
					Metadata:    map[string]string{},
				},
				{
					Event:       []byte("data 2"),
					EntryId:     "1",
					ContentType: "text/plain",
					Metadata:    map[string]string{},
				},
			},
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Contains(t, err.Error(), "entryId is duplicated")
	})

	t.Run("err: bulk publish event request with missing entry Ids", func(t *testing.T) {
		_, err := client.BulkPublishEventAlpha1(t.Context(), &runtimev1pb.BulkPublishRequest{
			PubsubName: "pubsub",
			Topic:      "topic",
			Entries: []*runtimev1pb.BulkPublishRequestEntry{
				{
					Event:       []byte("data"),
					ContentType: "text/plain",
					Metadata:    map[string]string{},
				},
				{
					Event:       []byte("data 2"),
					EntryId:     "1",
					ContentType: "text/plain",
					Metadata:    map[string]string{},
				},
			},
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Contains(t, err.Error(), "not present for entry")
	})
	t.Run("err: bulk publish event request with pubsub and empty topic", func(t *testing.T) {
		_, err := client.BulkPublishEventAlpha1(t.Context(), &runtimev1pb.BulkPublishRequest{
			PubsubName: "pubsub",
		})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("no err: bulk publish event request with pubsub, topic and empty entries", func(t *testing.T) {
		_, err := client.BulkPublishEventAlpha1(t.Context(), &runtimev1pb.BulkPublishRequest{
			PubsubName: "pubsub",
			Topic:      "topic",
		})
		require.NoError(t, err)
	})

	t.Run("err: bulk publish event request with error-topic and pubsub", func(t *testing.T) {
		_, err := client.BulkPublishEventAlpha1(t.Context(), &runtimev1pb.BulkPublishRequest{
			PubsubName: "pubsub",
			Topic:      "error-topic",
		})
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("err: bulk publish event request with err-not-found topic and pubsub", func(t *testing.T) {
		_, err := client.BulkPublishEventAlpha1(t.Context(), &runtimev1pb.BulkPublishRequest{
			PubsubName: "pubsub",
			Topic:      "err-not-found",
		})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("err: bulk publish event request with err-not-allowed topic and pubsub", func(t *testing.T) {
		_, err := client.BulkPublishEventAlpha1(t.Context(), &runtimev1pb.BulkPublishRequest{
			PubsubName: "pubsub",
			Topic:      "err-not-allowed",
		})
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})
}

func TestBulkPublish(t *testing.T) {
	fakeAPI := &api{
		logger: logger.NewLogger("test"),
		Universal: universal.New(universal.Options{
			AppID:     "fakeAPI",
			CompStore: compstore.New(),
		}),
		pubsubAdapter: &daprt.MockPubSubAdapter{
			BulkPublishFn: func(ctx context.Context, req *pubsub.BulkPublishRequest) (pubsub.BulkPublishResponse, error) {
				entries := []pubsub.BulkPublishResponseFailedEntry{}
				// Construct sample response from the broker.
				if req.Topic == "error-topic" {
					for _, e := range req.Entries {
						entry := pubsub.BulkPublishResponseFailedEntry{
							EntryId: e.EntryId,
						}
						entry.Error = errors.New("error on publish")
						entries = append(entries, entry)
					}
				} else if req.Topic == "even-error-topic" {
					for i, e := range req.Entries {
						if i%2 == 0 {
							entry := pubsub.BulkPublishResponseFailedEntry{
								EntryId: e.EntryId,
							}
							entry.Error = errors.New("error on publish")
							entries = append(entries, entry)
						}
					}
				}
				// Mock simulates only partial failures or total success, so error is always nil.
				return pubsub.BulkPublishResponse{FailedEntries: entries}, nil
			},
		},
	}

	mock := daprt.MockPubSub{}
	mock.On("Features").Return([]pubsub.Feature{})
	fakeAPI.Universal.CompStore().AddPubSub("pubsub", &runtimePubsub.PubsubItem{Component: &mock})

	lis := startDaprAPIServer(t, fakeAPI, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	sampleEntries := []*runtimev1pb.BulkPublishRequestEntry{
		{EntryId: "1", Event: []byte("data1")},
		{EntryId: "2", Event: []byte("data2")},
		{EntryId: "3", Event: []byte("data3")},
		{EntryId: "4", Event: []byte("data4")},
	}

	t.Run("no failures", func(t *testing.T) {
		res, err := client.BulkPublishEventAlpha1(t.Context(), &runtimev1pb.BulkPublishRequest{
			PubsubName: "pubsub",
			Topic:      "topic",
			Entries:    sampleEntries,
		})
		require.NoError(t, err)
		assert.Empty(t, res.GetFailedEntries())
	})

	t.Run("no failures with ce metadata override", func(t *testing.T) {
		res, err := client.BulkPublishEventAlpha1(t.Context(), &runtimev1pb.BulkPublishRequest{
			PubsubName: "pubsub",
			Topic:      "topic",
			Entries:    sampleEntries,
			Metadata: map[string]string{
				"cloudevent.source": "unit-test",
				"cloudevent.topic":  "overridetopic",  // noop -- if this modified the envelope the test would fail
				"cloudevent.pubsub": "overridepubsub", // noop -- if this modified the envelope the test would fail
			},
		})
		require.NoError(t, err)
		assert.Empty(t, res.GetFailedEntries())
	})

	t.Run("all failures from component", func(t *testing.T) {
		res, err := client.BulkPublishEventAlpha1(t.Context(), &runtimev1pb.BulkPublishRequest{
			PubsubName: "pubsub",
			Topic:      "error-topic",
			Entries:    sampleEntries,
		})
		t.Log(res)
		// Full failure from component, so expecting no error
		require.NoError(t, err)
		assert.NotNil(t, res)
		assert.Len(t, res.GetFailedEntries(), 4)
	})

	t.Run("partial failures from component", func(t *testing.T) {
		res, err := client.BulkPublishEventAlpha1(t.Context(), &runtimev1pb.BulkPublishRequest{
			PubsubName: "pubsub",
			Topic:      "even-error-topic",
			Entries:    sampleEntries,
		})
		// Partial failure, so expecting no error
		require.NoError(t, err)
		assert.NotNil(t, res)
		assert.Len(t, res.GetFailedEntries(), 2)
	})
}

func TestInvokeBinding(t *testing.T) {
	srv := &api{
		logger: logger.NewLogger("test"),
		sendToOutputBindingFn: func(ctx context.Context, name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
			if name == "error-binding" {
				return nil, errors.New("error invoking binding")
			}
			return &bindings.InvokeResponse{Data: []byte("ok"), Metadata: req.Metadata}, nil
		},
	}
	lis := startTestServerAPI(t, srv)

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err := client.InvokeBinding(t.Context(), &runtimev1pb.InvokeBindingRequest{})
	require.NoError(t, err)
	_, err = client.InvokeBinding(t.Context(), &runtimev1pb.InvokeBindingRequest{Name: "error-binding"})
	assert.Equal(t, codes.Internal, status.Code(err))

	ctx := grpcMetadata.AppendToOutgoingContext(t.Context(), "traceparent", "Test", "userMetadata", "overwrited", "additional", "val2")
	resp, err := client.InvokeBinding(ctx, &runtimev1pb.InvokeBindingRequest{Metadata: map[string]string{"userMetadata": "val1"}})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Contains(t, resp.GetMetadata(), "traceparent")
	assert.Equal(t, "Test", resp.GetMetadata()["traceparent"])
	assert.Contains(t, resp.GetMetadata(), "userMetadata")
	assert.Equal(t, "val1", resp.GetMetadata()["userMetadata"])
	assert.Contains(t, resp.GetMetadata(), "additional")
	assert.Equal(t, "val2", resp.GetMetadata()["additional"])
}

func TestTransactionStateStoreNotConfigured(t *testing.T) {
	lis := startDaprAPIServer(t, &api{
		Universal: universal.New(universal.Options{
			AppID:     "fakeAPI",
			Logger:    logger.NewLogger("grpc.api.test"),
			CompStore: compstore.New(),
		}),
	}, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err := client.ExecuteStateTransaction(t.Context(), &runtimev1pb.ExecuteStateTransactionRequest{})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestTransactionStateStoreNotImplemented(t *testing.T) {
	compStore := compstore.New()
	compStore.AddStateStore("store1", &daprt.MockStateStore{})
	lis := startDaprAPIServer(t, &api{
		Universal: universal.New(universal.Options{
			AppID:     "fakeAPI",
			Logger:    logger.NewLogger("grpc.api.test"),
			CompStore: compStore,
		}),
	}, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err := client.ExecuteStateTransaction(t.Context(), &runtimev1pb.ExecuteStateTransactionRequest{
		StoreName: "store1",
	})
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestExecuteStateTransaction(t *testing.T) {
	fakeStore := &daprt.TransactionalStoreMock{}
	fakeStore.MaxOperations = 10
	matchKeyFn := func(_ context.Context, req *state.TransactionalStateRequest, key string) bool {
		if len(req.Operations) == 1 {
			if rr, ok := req.Operations[0].(state.SetRequest); ok {
				if rr.Key == "fakeAPI||"+key {
					return true
				}
			} else {
				return true
			}
		}
		return false
	}
	fakeStore.On("Multi",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.TransactionalStateRequest) bool {
			return matchKeyFn(t.Context(), req, goodKey)
		})).Return(nil)
	fakeStore.On("Multi",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.TransactionalStateRequest) bool {
			return matchKeyFn(t.Context(), req, "error-key")
		})).Return(errors.New("error to execute with key2"))

	compStore := compstore.New()
	compStore.AddStateStore("store1", fakeStore)

	// Setup dapr api server
	fakeAPI := &api{
		Universal: universal.New(universal.Options{
			Logger:     logger.NewLogger("grpc.api.test"),
			AppID:      "fakeAPI",
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
		pubsubAdapter: &daprt.MockPubSubAdapter{},
		outbox:        outboxfake.New(),
	}
	lis := startDaprAPIServer(t, fakeAPI, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	tooManyOperations := make([]*runtimev1pb.TransactionalStateOperation, 20)
	for i := range tooManyOperations {
		tooManyOperations[i] = &runtimev1pb.TransactionalStateOperation{
			OperationType: string(state.OperationUpsert),
			Request: &commonv1pb.StateItem{
				Key:   fmt.Sprintf("op%d", i),
				Value: []byte("1"),
			},
		}
	}

	stateOptions, _ := GenerateStateOptionsTestCase()
	testCases := []struct {
		testName      string
		storeName     string
		operation     state.OperationType
		key           string
		value         []byte
		ops           []*runtimev1pb.TransactionalStateOperation
		options       *commonv1pb.StateOptions
		errorExcepted bool
		expectedError codes.Code
	}{
		{
			testName:      "upsert operation",
			storeName:     "store1",
			operation:     state.OperationUpsert,
			key:           goodKey,
			value:         []byte("1"),
			errorExcepted: false,
			expectedError: codes.OK,
		},
		{
			testName:      "delete operation",
			storeName:     "store1",
			operation:     state.OperationUpsert,
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
			operation:     state.OperationUpsert,
			key:           "error-key",
			errorExcepted: true,
			expectedError: codes.Internal,
		},
		{
			testName:      "fails with too many operations",
			storeName:     "store1",
			ops:           tooManyOperations,
			errorExcepted: true,
			expectedError: codes.InvalidArgument,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.ExecuteStateTransactionRequest{
				StoreName:  tt.storeName,
				Operations: tt.ops,
			}
			if tt.ops == nil {
				req.Operations = []*runtimev1pb.TransactionalStateOperation{
					{
						OperationType: string(tt.operation),
						Request: &commonv1pb.StateItem{
							Key:     tt.key,
							Value:   tt.value,
							Options: stateOptions,
						},
					},
				}
			}

			_, err := client.ExecuteStateTransaction(t.Context(), req)
			if !tt.errorExcepted {
				require.NoError(t, err)
			} else {
				require.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestStateStoreErrors(t *testing.T) {
	t.Run("save etag mismatch", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagMismatch, errors.New("error"))
		eTagCode := a.getStateErrorCode(err)

		assert.Equal(t, codes.Aborted, eTagCode)
	})

	t.Run("save etag invalid", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagInvalid, errors.New("error"))
		eTagCode := a.getStateErrorCode(err)
		assert.Equal(t, codes.InvalidArgument, eTagCode)
	})

	t.Run("save non etag", func(t *testing.T) {
		a := &api{}
		err := errors.New("error")
		eTagCode := a.getStateErrorCode(err)
		assert.Equal(t, codes.Internal, eTagCode)
	})

	t.Run("delete etag mismatch", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagMismatch, errors.New("error"))
		eTagCode := a.getStateErrorCode(err)
		assert.Equal(t, codes.Aborted, eTagCode)
	})

	t.Run("delete etag invalid", func(t *testing.T) {
		a := &api{}
		err := state.NewETagError(state.ETagInvalid, errors.New("error"))
		eTagCode := a.getStateErrorCode(err)
		assert.Equal(t, codes.InvalidArgument, eTagCode)
	})

	t.Run("delete non etag", func(t *testing.T) {
		a := &api{}
		err := errors.New("error")
		eTagCode := a.getStateErrorCode(err)
		assert.Equal(t, codes.Internal, eTagCode)
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

//nolint:nosnakecase
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
	fakeStore := &mockStateStoreQuerier{}
	// simulate full result
	fakeStore.MockQuerier.On("Query",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.QueryRequest) bool {
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
	fakeStore.MockQuerier.On("Query",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.QueryRequest) bool {
			return len(req.Query.Sort) == 0 && req.Query.Page.Limit != 0
		})).Return(
		&state.QueryResponse{
			Results: []state.QueryItem{},
		}, nil)
	// simulate error
	fakeStore.MockQuerier.On("Query",
		mock.MatchedBy(matchContextInterface),
		mock.MatchedBy(func(req *state.QueryRequest) bool {
			return len(req.Query.Sort) != 0 && req.Query.Page.Limit == 0
		})).Return(nil, errors.New("Query error"))

	compStore := compstore.New()
	compStore.AddStateStore("store1", fakeStore)
	lis := startTestServerAPI(t, &api{
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	})

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	resp, err := client.QueryStateAlpha1(t.Context(), &runtimev1pb.QueryStateRequest{
		StoreName: "store1",
		Query:     queryTestRequestOK,
	})
	assert.Len(t, resp.GetResults(), 1)
	assert.Equal(t, codes.OK, status.Code(err))
	if len(resp.GetResults()) > 0 {
		assert.NotNil(t, resp.GetResults()[0].GetData())
	}

	resp, err = client.QueryStateAlpha1(t.Context(), &runtimev1pb.QueryStateRequest{
		StoreName: "store1",
		Query:     queryTestRequestNoRes,
	})
	assert.Empty(t, resp.GetResults())
	assert.Equal(t, codes.OK, status.Code(err))

	_, err = client.QueryStateAlpha1(t.Context(), &runtimev1pb.QueryStateRequest{
		StoreName: "store1",
		Query:     queryTestRequestErr,
	})
	assert.Equal(t, codes.Internal, status.Code(err))

	_, err = client.QueryStateAlpha1(t.Context(), &runtimev1pb.QueryStateRequest{
		StoreName: "store1",
		Query:     queryTestRequestSyntaxErr,
	})
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestStateStoreQuerierNotImplemented(t *testing.T) {
	compStore := compstore.New()
	compStore.AddStateStore("store1", &daprt.MockStateStore{})

	lis := startDaprAPIServer(t, &api{
		Universal: universal.New(universal.Options{
			Logger:     logger.NewLogger("grpc.api.test"),
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err := client.QueryStateAlpha1(t.Context(), &runtimev1pb.QueryStateRequest{
		StoreName: "store1",
	})
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestStateStoreQuerierEncrypted(t *testing.T) {
	storeName := "encrypted-store1"
	encryption.AddEncryptedStateStore(storeName, encryption.ComponentEncryptionKeys{})
	compStore := compstore.New()
	compStore.AddStateStore(storeName, &mockStateStoreQuerier{})
	lis := startDaprAPIServer(t, &api{
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)
	_, err := client.QueryStateAlpha1(t.Context(), &runtimev1pb.QueryStateRequest{
		StoreName: storeName,
	})
	assert.Equal(t, codes.Internal, status.Code(err))
}

// Interface that applies to both SubscribeConfigurationAlpha1 and SubscribeConfiguration
type subscribeConfigurationFn func(ctx context.Context, in *runtimev1pb.SubscribeConfigurationRequest, opts ...grpc.CallOption) (interface {
	Recv() (*runtimev1pb.SubscribeConfigurationResponse, error)
}, error)

// Interface that applies to both GetConfigurationAlpha1 and GetConfiguration
type getConfigurationFn func(ctx context.Context, in *runtimev1pb.GetConfigurationRequest, opts ...grpc.CallOption) (*runtimev1pb.GetConfigurationResponse, error)

func TestGetConfigurationAPI(t *testing.T) {
	compStore := compstore.New()
	compStore.AddConfiguration("store1", &mockConfigStore{})
	lis := startDaprAPIServer(t, &api{
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	testFn := func(getFn getConfigurationFn) func(t *testing.T) {
		return func(t *testing.T) {
			r, err := getFn(t.Context(), &runtimev1pb.GetConfigurationRequest{
				StoreName: "store1",
				Keys: []string{
					"key1",
				},
			})

			require.NoError(t, err)
			require.NotNil(t, r.GetItems())
			assert.Len(t, r.GetItems(), 1)
			assert.Equal(t, "val1", r.GetItems()["key1"].GetValue())
		}
	}

	t.Run("get configuration item - alpha1", testFn(client.GetConfigurationAlpha1))

	t.Run("get configuration item", testFn(client.GetConfiguration))
}

func TestSubscribeConfigurationAPI(t *testing.T) {
	compStore := compstore.New()
	compStore.AddConfiguration("store1", &mockConfigStore{})

	lis := startDaprAPIServer(t, &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	getConfigurationItemTest := func(subscribeFn subscribeConfigurationFn) func(t *testing.T) {
		return func(t *testing.T) {
			s, err := subscribeFn(t.Context(), &runtimev1pb.SubscribeConfigurationRequest{
				StoreName: "store1",
				Keys: []string{
					"key1",
				},
			})
			require.NoError(t, err)

			r := &runtimev1pb.SubscribeConfigurationResponse{}

			for {
				update, err := s.Recv()
				if errors.Is(err, io.EOF) {
					break
				}

				if update != nil && len(update.GetItems()) > 0 {
					r = update
					break
				}
			}

			require.NotNil(t, r)
			assert.Len(t, r.GetItems(), 1)
			assert.Equal(t, "val1", r.GetItems()["key1"].GetValue())
		}
	}

	t.Run("get configuration item - alpha1", getConfigurationItemTest(func(ctx context.Context, in *runtimev1pb.SubscribeConfigurationRequest, opts ...grpc.CallOption) (interface {
		Recv() (*runtimev1pb.SubscribeConfigurationResponse, error)
	}, error,
	) {
		return client.SubscribeConfigurationAlpha1(ctx, in)
	}))

	t.Run("get configuration item", getConfigurationItemTest(func(ctx context.Context, in *runtimev1pb.SubscribeConfigurationRequest, opts ...grpc.CallOption) (interface {
		Recv() (*runtimev1pb.SubscribeConfigurationResponse, error)
	}, error,
	) {
		return client.SubscribeConfiguration(ctx, in)
	}))

	getAllConfigurationItemTest := func(subscribeFn subscribeConfigurationFn) func(t *testing.T) {
		return func(t *testing.T) {
			s, err := subscribeFn(t.Context(), &runtimev1pb.SubscribeConfigurationRequest{
				StoreName: "store1",
				Keys:      []string{},
			})
			require.NoError(t, err)

			r := &runtimev1pb.SubscribeConfigurationResponse{}

			for {
				update, err := s.Recv()
				if errors.Is(err, io.EOF) {
					break
				}

				if update != nil && len(update.GetItems()) > 0 {
					r = update
					break
				}
			}

			require.NotNil(t, r)
			assert.Len(t, r.GetItems(), 2)
			assert.Equal(t, "val1", r.GetItems()["key1"].GetValue())
			assert.Equal(t, "val2", r.GetItems()["key2"].GetValue())
		}
	}

	t.Run("get all configuration item for empty list - alpha1", getAllConfigurationItemTest(func(ctx context.Context, in *runtimev1pb.SubscribeConfigurationRequest, opts ...grpc.CallOption) (interface {
		Recv() (*runtimev1pb.SubscribeConfigurationResponse, error)
	}, error,
	) {
		return client.SubscribeConfigurationAlpha1(ctx, in)
	}))

	t.Run("get all configuration item for empty list", getAllConfigurationItemTest(func(ctx context.Context, in *runtimev1pb.SubscribeConfigurationRequest, opts ...grpc.CallOption) (interface {
		Recv() (*runtimev1pb.SubscribeConfigurationResponse, error)
	}, error,
	) {
		return client.SubscribeConfiguration(ctx, in)
	}))
}

func TestStateAPIWithResiliency(t *testing.T) {
	failingStore := &daprt.FailingStatestore{
		Failure: daprt.NewFailure(
			map[string]int{
				"failingGetKey":        1,
				"failingSetKey":        1,
				"failingDeleteKey":     1,
				"failingBulkGetKey":    1,
				"failingBulkSetKey":    1,
				"failingBulkDeleteKey": 1,
				"failingMultiKey":      1,
				"failingQueryKey":      1,
			},
			map[string]time.Duration{
				"timeoutGetKey":         time.Second * 30,
				"timeoutSetKey":         time.Second * 30,
				"timeoutDeleteKey":      time.Second * 30,
				"timeoutBulkGetKey":     time.Second * 30,
				"timeoutBulkGetKeyBulk": time.Second * 30,
				"timeoutBulkSetKey":     time.Second * 30,
				"timeoutBulkDeleteKey":  time.Second * 30,
				"timeoutMultiKey":       time.Second * 30,
				"timeoutQueryKey":       time.Second * 30,
			},
			map[string]int{},
		),
	}

	stateLoader.SaveStateConfiguration("failStore", map[string]string{"keyPrefix": "none"})

	compStore := compstore.New()
	compStore.AddStateStore("failStore", failingStore)
	res := resiliency.FromConfigurations(logger.NewLogger("grpc.api.test"), testResiliency)

	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			CompStore:  compStore,
			Resiliency: res,
		}),
		pubsubAdapter: &daprt.MockPubSubAdapter{},
		outbox:        outboxfake.New(),
	}
	lis := startDaprAPIServer(t, fakeAPI, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	t.Run("get state request retries with resiliency", func(t *testing.T) {
		_, err := client.GetState(t.Context(), &runtimev1pb.GetStateRequest{
			StoreName: "failStore",
			Key:       "failingGetKey",
		})
		require.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("failingGetKey"))
	})

	t.Run("get state request times out with resiliency", func(t *testing.T) {
		start := time.Now()
		_, err := client.GetState(t.Context(), &runtimev1pb.GetStateRequest{
			StoreName: "failStore",
			Key:       "timeoutGetKey",
		})
		end := time.Now()

		require.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeoutGetKey"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})

	t.Run("set state request retries with resiliency", func(t *testing.T) {
		_, err := client.SaveState(t.Context(), &runtimev1pb.SaveStateRequest{
			StoreName: "failStore",
			States: []*commonv1pb.StateItem{
				{
					Key:   "failingSetKey",
					Value: []byte("TestData"),
				},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("failingSetKey"))
	})

	t.Run("set state request times out with resiliency", func(t *testing.T) {
		start := time.Now()
		_, err := client.SaveState(t.Context(), &runtimev1pb.SaveStateRequest{
			StoreName: "failStore",
			States: []*commonv1pb.StateItem{
				{
					Key:   "timeoutSetKey",
					Value: []byte("TestData"),
				},
			},
		})
		end := time.Now()

		require.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeoutSetKey"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})

	t.Run("delete state request retries with resiliency", func(t *testing.T) {
		_, err := client.DeleteState(t.Context(), &runtimev1pb.DeleteStateRequest{
			StoreName: "failStore",
			Key:       "failingDeleteKey",
		})
		require.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("failingDeleteKey"))
	})

	t.Run("delete state request times out with resiliency", func(t *testing.T) {
		start := time.Now()
		_, err := client.DeleteState(t.Context(), &runtimev1pb.DeleteStateRequest{
			StoreName: "failStore",
			Key:       "timeoutDeleteKey",
		})
		end := time.Now()

		require.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeoutDeleteKey"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})

	t.Run("bulk state get fails with bulk support", func(t *testing.T) {
		// Adding this will make the bulk operation fail
		failingStore.BulkFailKey.Store(ptr.Of("timeoutBulkGetKeyBulk"))
		t.Cleanup(func() {
			failingStore.BulkFailKey.Store(ptr.Of(""))
		})

		_, err := client.GetBulkState(t.Context(), &runtimev1pb.GetBulkStateRequest{
			StoreName: "failStore",
			Keys:      []string{"failingBulkGetKey", "goodBulkGetKey"},
		})

		require.Error(t, err)
	})

	t.Run("bulk state set recovers from single key failure with resiliency", func(t *testing.T) {
		_, err := client.SaveState(t.Context(), &runtimev1pb.SaveStateRequest{
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

		require.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("failingBulkSetKey"))
		assert.Equal(t, 1, failingStore.Failure.CallCount("goodBulkSetKey"))
	})

	t.Run("bulk state set times out with resiliency", func(t *testing.T) {
		start := time.Now()
		_, err := client.SaveState(t.Context(), &runtimev1pb.SaveStateRequest{
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

		require.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeoutBulkSetKey"))
		assert.Equal(t, 0, failingStore.Failure.CallCount("goodTimeoutBulkSetKey"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})

	t.Run("state transaction passes after retries with resiliency", func(t *testing.T) {
		_, err := client.ExecuteStateTransaction(t.Context(), &runtimev1pb.ExecuteStateTransactionRequest{
			StoreName: "failStore",
			Operations: []*runtimev1pb.TransactionalStateOperation{
				{
					OperationType: string(state.OperationDelete),
					Request: &commonv1pb.StateItem{
						Key: "failingMultiKey",
					},
				},
			},
		})

		require.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("failingMultiKey"))
	})

	t.Run("state transaction times out with resiliency", func(t *testing.T) {
		_, err := client.ExecuteStateTransaction(t.Context(), &runtimev1pb.ExecuteStateTransactionRequest{
			StoreName: "failStore",
			Operations: []*runtimev1pb.TransactionalStateOperation{
				{
					OperationType: string(state.OperationDelete),
					Request: &commonv1pb.StateItem{
						Key: "timeoutMultiKey",
					},
				},
			},
		})

		require.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeoutMultiKey"))
	})

	t.Run("state query retries with resiliency", func(t *testing.T) {
		_, err := client.QueryStateAlpha1(t.Context(), &runtimev1pb.QueryStateRequest{
			StoreName: "failStore",
			Query:     queryTestRequestOK,
			Metadata:  map[string]string{"key": "failingQueryKey"},
		})

		require.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("failingQueryKey"))
	})

	t.Run("state query times out with resiliency", func(t *testing.T) {
		_, err := client.QueryStateAlpha1(t.Context(), &runtimev1pb.QueryStateRequest{
			StoreName: "failStore",
			Query:     queryTestRequestOK,
			Metadata:  map[string]string{"key": "timeoutQueryKey"},
		})

		require.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeoutQueryKey"))
	})
}

func TestConfigurationAPIWithResiliency(t *testing.T) {
	failingConfigStore := daprt.FailingConfigurationStore{
		Failure: daprt.NewFailure(
			map[string]int{
				"failingGetKey":         1,
				"failingSubscribeKey":   1,
				"failingUnsubscribeKey": 1,
			},
			map[string]time.Duration{
				"timeoutGetKey":         time.Second * 30,
				"timeoutSubscribeKey":   time.Second * 30,
				"timeoutUnsubscribeKey": time.Second * 30,
			},
			map[string]int{},
		),
	}

	compStore := compstore.New()
	compStore.AddConfiguration("failConfig", &failingConfigStore)

	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			CompStore:  compStore,
			Resiliency: resiliency.FromConfigurations(logger.NewLogger("grpc.api.test"), testResiliency),
		}),
	}
	lis := startDaprAPIServer(t, fakeAPI, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	t.Run("test get configuration retries with resiliency", func(t *testing.T) {
		_, err := client.GetConfiguration(t.Context(), &runtimev1pb.GetConfigurationRequest{
			StoreName: "failConfig",
			Keys:      []string{},
			Metadata:  map[string]string{"key": "failingGetKey"},
		})

		require.NoError(t, err)
		assert.Equal(t, 2, failingConfigStore.Failure.CallCount("failingGetKey"))
	})

	t.Run("test get configuration fails due to timeout with resiliency", func(t *testing.T) {
		_, err := client.GetConfiguration(t.Context(), &runtimev1pb.GetConfigurationRequest{
			StoreName: "failConfig",
			Keys:      []string{},
			Metadata:  map[string]string{"key": "timeoutGetKey"},
		})

		require.Error(t, err)
		assert.Equal(t, 2, failingConfigStore.Failure.CallCount("timeoutGetKey"))
	})

	t.Run("test subscribe configuration retries with resiliency", func(t *testing.T) {
		resp, err := client.SubscribeConfiguration(t.Context(), &runtimev1pb.SubscribeConfigurationRequest{
			StoreName: "failConfig",
			Keys:      []string{},
			Metadata:  map[string]string{"key": "failingSubscribeKey"},
		})
		require.NoError(t, err)

		_, err = resp.Recv()
		require.NoError(t, err)

		assert.Equal(t, 2, failingConfigStore.Failure.CallCount("failingSubscribeKey"))
	})

	t.Run("test subscribe configuration fails due to timeout with resiliency", func(t *testing.T) {
		resp, err := client.SubscribeConfiguration(t.Context(), &runtimev1pb.SubscribeConfigurationRequest{
			StoreName: "failConfig",
			Keys:      []string{},
			Metadata:  map[string]string{"key": "timeoutSubscribeKey"},
		})
		require.NoError(t, err)

		_, err = resp.Recv()
		require.Error(t, err)
		assert.Equal(t, 2, failingConfigStore.Failure.CallCount("timeoutSubscribeKey"))
	})
}

func TestSecretAPIWithResiliency(t *testing.T) {
	failingStore := daprt.FailingSecretStore{
		Failure: daprt.NewFailure(
			map[string]int{"key": 1, "bulk": 1},
			map[string]time.Duration{"timeout": time.Second * 30, "bulkTimeout": time.Second * 30},
			map[string]int{},
		),
	}

	compStore := compstore.New()
	compStore.AddSecretStore("failSecret", failingStore)

	// Setup Dapr API server
	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			Resiliency: resiliency.FromConfigurations(logger.NewLogger("grpc.api.test"), testResiliency),
			CompStore:  compStore,
		}),
	}
	// Run test server
	lis := startDaprAPIServer(t, fakeAPI, "")

	// Create gRPC test client
	clientConn := createTestClient(lis)
	defer clientConn.Close()

	// act
	client := runtimev1pb.NewDaprClient(clientConn)

	t.Run("Get secret - retries on initial failure with resiliency", func(t *testing.T) {
		_, err := client.GetSecret(t.Context(), &runtimev1pb.GetSecretRequest{
			StoreName: "failSecret",
			Key:       "key",
		})

		require.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("key"))
	})

	t.Run("Get secret - timeout before request ends", func(t *testing.T) {
		// Store sleeps for 30 seconds, let's make sure our timeout takes less time than that.
		start := time.Now()
		_, err := client.GetSecret(t.Context(), &runtimev1pb.GetSecretRequest{
			StoreName: "failSecret",
			Key:       "timeout",
		})
		end := time.Now()

		require.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeout"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})

	t.Run("Get bulk secret - retries on initial failure with resiliency", func(t *testing.T) {
		_, err := client.GetBulkSecret(t.Context(), &runtimev1pb.GetBulkSecretRequest{
			StoreName: "failSecret",
			Metadata:  map[string]string{"key": "bulk"},
		})

		require.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("bulk"))
	})

	t.Run("Get bulk secret - timeout before request ends", func(t *testing.T) {
		start := time.Now()
		_, err := client.GetBulkSecret(t.Context(), &runtimev1pb.GetBulkSecretRequest{
			StoreName: "failSecret",
			Metadata:  map[string]string{"key": "bulkTimeout"},
		})
		end := time.Now()

		require.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("bulkTimeout"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})
}

func TestServiceInvocationWithResiliency(t *testing.T) {
	failingDirectMessaging := &daprt.FailingDirectMessaging{
		Failure: daprt.NewFailure(
			map[string]int{
				"failingKey":        1,
				"extraFailingKey":   3,
				"circuitBreakerKey": 10,
			},
			map[string]time.Duration{
				"timeoutKey": time.Second * 30,
			},
			map[string]int{},
		),
	}

	// Setup Dapr API server
	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Resiliency: resiliency.FromConfigurations(logger.NewLogger("grpc.api.test"), testResiliency),
		}),
		directMessaging: failingDirectMessaging,
	}

	// Run test server
	lis := startDaprAPIServer(t, fakeAPI, "")

	// Create gRPC test client
	clientConn := createTestClient(lis)
	defer clientConn.Close()

	// act
	client := runtimev1pb.NewDaprClient(clientConn)

	t.Run("Test invoke direct message retries with resiliency", func(t *testing.T) {
		val := []byte("failingKey")
		res, err := client.InvokeService(t.Context(), &runtimev1pb.InvokeServiceRequest{
			Id: "failingApp",
			Message: &commonv1pb.InvokeRequest{
				Method: "test",
				Data:   &anypb.Any{Value: val},
			},
		})

		require.NoError(t, err)
		assert.Equal(t, 2, failingDirectMessaging.Failure.CallCount("failingKey"))
		require.NotNil(t, res)
		require.NotNil(t, res.GetData())
		assert.Equal(t, val, res.GetData().GetValue())
	})

	t.Run("Test invoke direct message fails with timeout", func(t *testing.T) {
		start := time.Now()
		_, err := client.InvokeService(t.Context(), &runtimev1pb.InvokeServiceRequest{
			Id: "failingApp",
			Message: &commonv1pb.InvokeRequest{
				Method: "test",
				Data:   &anypb.Any{Value: []byte("timeoutKey")},
			},
		})
		end := time.Now()

		require.Error(t, err)
		assert.Equal(t, 2, failingDirectMessaging.Failure.CallCount("timeoutKey"))
		assert.Less(t, end.Sub(start), time.Second*30)
	})

	t.Run("Test invoke direct messages fails after exhausting retries", func(t *testing.T) {
		_, err := client.InvokeService(t.Context(), &runtimev1pb.InvokeServiceRequest{
			Id: "failingApp",
			Message: &commonv1pb.InvokeRequest{
				Method: "test",
				Data:   &anypb.Any{Value: []byte("extraFailingKey")},
			},
		})

		require.Error(t, err)
		assert.Equal(t, 2, failingDirectMessaging.Failure.CallCount("extraFailingKey"))
	})

	t.Run("Test invoke direct messages opens circuit breaker after consecutive failures", func(t *testing.T) {
		// Circuit breaker trips on the 5th request, ending the retries.
		_, err := client.InvokeService(t.Context(), &runtimev1pb.InvokeServiceRequest{
			Id: "circuitBreakerApp",
			Message: &commonv1pb.InvokeRequest{
				Method: "test",
				Data:   &anypb.Any{Value: []byte("circuitBreakerKey")},
			},
		})
		require.Error(t, err)
		assert.Equal(t, 5, failingDirectMessaging.Failure.CallCount("circuitBreakerKey"))

		// Additional requests should fail due to the circuit breaker.
		_, err = client.InvokeService(t.Context(), &runtimev1pb.InvokeServiceRequest{
			Id: "circuitBreakerApp",
			Message: &commonv1pb.InvokeRequest{
				Method: "test",
				Data:   &anypb.Any{Value: []byte("circuitBreakerKey")},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "circuit breaker is open")
		assert.Equal(t, 5, failingDirectMessaging.Failure.CallCount("circuitBreakerKey"))
	})
}

type mockConfigStore struct{}

func (m *mockConfigStore) Init(ctx context.Context, metadata configuration.Metadata) error {
	return nil
}

func (m *mockConfigStore) Get(ctx context.Context, req *configuration.GetRequest) (*configuration.GetResponse, error) {
	items := map[string]*configuration.Item{
		"key1": {Value: "val1"},
		"key2": {Value: "val2"},
	}

	res := make(map[string]*configuration.Item)

	if len(req.Keys) == 0 {
		res = items
	} else {
		for _, key := range req.Keys {
			if val, ok := items[key]; ok {
				res[key] = val
			}
		}
	}

	return &configuration.GetResponse{
		Items: res,
	}, nil
}

func (m *mockConfigStore) Subscribe(ctx context.Context, req *configuration.SubscribeRequest, handler configuration.UpdateHandler) (string, error) {
	items := map[string]*configuration.Item{
		"key1": {Value: "val1"},
		"key2": {Value: "val2"},
	}

	res := make(map[string]*configuration.Item)

	if len(req.Keys) == 0 {
		res = items
	} else {
		for _, key := range req.Keys {
			if val, ok := items[key]; ok {
				res[key] = val
			}
		}
	}

	go handler(ctx, &configuration.UpdateEvent{
		Items: res,
	})

	return "", nil
}

func (m *mockConfigStore) Unsubscribe(ctx context.Context, req *configuration.UnsubscribeRequest) error {
	return nil
}

func (m *mockConfigStore) Close() error {
	return nil
}

func TestTryLock(t *testing.T) {
	l := logger.NewLogger("fakeLogger")
	resiliencyConfig := resiliency.FromConfigurations(l, testResiliency)

	t.Run("error when lock store not configured", func(t *testing.T) {
		api := NewAPI(APIOpts{
			Universal: universal.New(universal.Options{
				Resiliency: resiliencyConfig,
				Logger:     l,
				CompStore:  compstore.New(),
			}),
		})
		req := &runtimev1pb.TryLockRequest{
			StoreName:       "abc",
			ExpiryInSeconds: 10,
		}
		_, err := api.TryLockAlpha1(t.Context(), req)
		assert.Equal(t, "api error: code = FailedPrecondition desc = lock store is not configured", err.Error())
	})

	t.Run("InvalidArgument: ResourceID empty", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		compStore := compstore.New()
		compStore.AddLock("mock", daprt.NewMockStore(ctl))
		api := NewAPI(APIOpts{
			Universal: universal.New(universal.Options{
				Resiliency: resiliencyConfig,
				Logger:     l,
				CompStore:  compStore,
			}),
		})
		req := &runtimev1pb.TryLockRequest{
			StoreName:       "mock",
			ExpiryInSeconds: 10,
		}
		_, err := api.TryLockAlpha1(t.Context(), req)
		assert.Equal(t, "api error: code = InvalidArgument desc = ResourceId is empty in lock store mock", err.Error())
	})

	t.Run("InvalidArgument: LockOwner empty", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		compStore := compstore.New()
		compStore.AddLock("mock", daprt.NewMockStore(ctl))
		api := NewAPI(APIOpts{
			Universal: universal.New(universal.Options{
				Resiliency: resiliencyConfig,
				Logger:     l,
				CompStore:  compStore,
			}),
			TracingSpec: config.TracingSpec{},
		})
		req := &runtimev1pb.TryLockRequest{
			StoreName:       "mock",
			ResourceId:      "resource",
			ExpiryInSeconds: 10,
		}
		_, err := api.TryLockAlpha1(t.Context(), req)
		assert.Equal(t, "api error: code = InvalidArgument desc = LockOwner is empty in lock store mock", err.Error())
	})

	t.Run("InvalidArgument: ExpiryInSeconds is not positive", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		compStore := compstore.New()
		compStore.AddLock("mock", daprt.NewMockStore(ctl))
		api := NewAPI(APIOpts{
			Universal: universal.New(universal.Options{
				Resiliency: resiliencyConfig,
				Logger:     l,
				CompStore:  compStore,
			}),
			TracingSpec: config.TracingSpec{},
		})

		req := &runtimev1pb.TryLockRequest{
			StoreName:       "mock",
			ResourceId:      "resource",
			LockOwner:       "owner",
			ExpiryInSeconds: 0,
		}
		_, err := api.TryLockAlpha1(t.Context(), req)
		assert.Equal(t, "api error: code = InvalidArgument desc = ExpiryInSeconds is not positive in lock store mock", err.Error())
	})

	t.Run("InvalidArgument: lock store not found", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		compStore := compstore.New()
		compStore.AddLock("mock", daprt.NewMockStore(ctl))
		api := NewAPI(APIOpts{
			Universal: universal.New(universal.Options{
				Resiliency: resiliencyConfig,
				Logger:     l,
				CompStore:  compStore,
			}),
			TracingSpec: config.TracingSpec{},
		})

		req := &runtimev1pb.TryLockRequest{
			StoreName:       "abc",
			ResourceId:      "resource",
			LockOwner:       "owner",
			ExpiryInSeconds: 1,
		}
		_, err := api.TryLockAlpha1(t.Context(), req)
		assert.Equal(t, "api error: code = InvalidArgument desc = lock store abc not found", err.Error())
	})

	t.Run("successful", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		mockLockStore := daprt.NewMockStore(ctl)

		mockLockStore.EXPECT().TryLock(t.Context(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *lock.TryLockRequest) (*lock.TryLockResponse, error) {
			assert.Equal(t, "lock||resource", req.ResourceID)
			assert.Equal(t, "owner", req.LockOwner)
			assert.Equal(t, int32(1), req.ExpiryInSeconds)
			return &lock.TryLockResponse{
				Success: true,
			}, nil
		})
		compStore := compstore.New()
		compStore.AddLock("mock", mockLockStore)
		api := NewAPI(APIOpts{
			Universal: universal.New(universal.Options{
				Resiliency: resiliencyConfig,
				Logger:     l,
				CompStore:  compStore,
			}),
			TracingSpec: config.TracingSpec{},
		})
		req := &runtimev1pb.TryLockRequest{
			StoreName:       "mock",
			ResourceId:      "resource",
			LockOwner:       "owner",
			ExpiryInSeconds: 1,
		}
		resp, err := api.TryLockAlpha1(t.Context(), req)
		require.NoError(t, err)
		assert.True(t, resp.GetSuccess())
	})
}

func TestUnlock(t *testing.T) {
	l := logger.NewLogger("fakeLogger")
	resiliencyConfig := resiliency.FromConfigurations(l, testResiliency)

	t.Run("error when lock store not configured", func(t *testing.T) {
		api := NewAPI(APIOpts{
			Universal: universal.New(universal.Options{
				Resiliency: resiliencyConfig,
				Logger:     l,
				CompStore:  compstore.New(),
			}),
			TracingSpec: config.TracingSpec{},
		})

		req := &runtimev1pb.UnlockRequest{
			StoreName: "abc",
		}
		_, err := api.UnlockAlpha1(t.Context(), req)
		assert.Equal(t, "api error: code = FailedPrecondition desc = lock store is not configured", err.Error())
	})

	t.Run("InvalidArgument: ResourceId empty", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		compStore := compstore.New()
		compStore.AddLock("mock", daprt.NewMockStore(ctl))
		api := NewAPI(APIOpts{
			Universal: universal.New(universal.Options{
				Resiliency: resiliencyConfig,
				Logger:     l,
				CompStore:  compStore,
			}),
		})

		req := &runtimev1pb.UnlockRequest{
			StoreName: "abc",
		}
		_, err := api.UnlockAlpha1(t.Context(), req)
		assert.Equal(t, "api error: code = InvalidArgument desc = ResourceId is empty in lock store abc", err.Error())
	})

	t.Run("InvalidArgument: lock owner empty", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		compStore := compstore.New()
		compStore.AddLock("mock", daprt.NewMockStore(ctl))
		api := NewAPI(APIOpts{
			Universal: universal.New(universal.Options{
				Resiliency: resiliencyConfig,
				Logger:     l,
				CompStore:  compStore,
			}),
		})
		req := &runtimev1pb.UnlockRequest{
			StoreName:  "abc",
			ResourceId: "resource",
		}
		_, err := api.UnlockAlpha1(t.Context(), req)
		assert.Equal(t, "api error: code = InvalidArgument desc = LockOwner is empty in lock store abc", err.Error())
	})

	t.Run("InvalidArgument: lock store not found", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		compStore := compstore.New()
		compStore.AddLock("mock", daprt.NewMockStore(ctl))
		api := NewAPI(APIOpts{
			Universal: universal.New(universal.Options{
				Resiliency: resiliencyConfig,
				Logger:     l,
				CompStore:  compStore,
			}),
		})

		req := &runtimev1pb.UnlockRequest{
			StoreName:  "abc",
			ResourceId: "resource",
			LockOwner:  "owner",
		}
		_, err := api.UnlockAlpha1(t.Context(), req)
		assert.Equal(t, "api error: code = InvalidArgument desc = lock store abc not found", err.Error())
	})

	t.Run("successful", func(t *testing.T) {
		ctl := gomock.NewController(t)
		defer ctl.Finish()

		mockLockStore := daprt.NewMockStore(ctl)

		mockLockStore.EXPECT().Unlock(t.Context(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *lock.UnlockRequest) (*lock.UnlockResponse, error) {
			assert.Equal(t, "lock||resource", req.ResourceID)
			assert.Equal(t, "owner", req.LockOwner)
			return &lock.UnlockResponse{
				Status: lock.Success,
			}, nil
		})
		compStore := compstore.New()
		compStore.AddLock("mock", mockLockStore)
		api := NewAPI(APIOpts{
			Universal: universal.New(universal.Options{
				Resiliency: resiliencyConfig,
				Logger:     l,
				CompStore:  compStore,
			}),
		})
		req := &runtimev1pb.UnlockRequest{
			StoreName:  "mock",
			ResourceId: "resource",
			LockOwner:  "owner",
		}
		resp, err := api.UnlockAlpha1(t.Context(), req)
		require.NoError(t, err)
		assert.Equal(t, runtimev1pb.UnlockResponse_SUCCESS, resp.GetStatus()) //nolint:nosnakecase
	})
}

func TestMetadata(t *testing.T) {
	compStore := compstore.New()
	require.NoError(t, compStore.AddPendingComponentForCommit(componentsV1alpha1.Component{
		ObjectMeta: metaV1.ObjectMeta{
			Name: "MockComponent1Name",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "mock.component1Type",
			Version: "v1.0",
			Metadata: []commonapi.NameValuePair{
				{
					Name: "actorMockComponent1",
					Value: commonapi.DynamicValue{
						JSON: v1.JSON{Raw: []byte("true")},
					},
				},
			},
		},
	}))
	require.NoError(t, compStore.CommitPendingComponent())
	require.NoError(t, compStore.AddPendingComponentForCommit(componentsV1alpha1.Component{
		ObjectMeta: metaV1.ObjectMeta{
			Name: "MockComponent2Name",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "mock.component2Type",
			Version: "v1.0",
			Metadata: []commonapi.NameValuePair{
				{
					Name: "actorMockComponent2",
					Value: commonapi.DynamicValue{
						JSON: v1.JSON{Raw: []byte("true")},
					},
				},
			},
		},
	}))
	require.NoError(t, compStore.CommitPendingComponent())
	compStore.SetProgramaticSubscriptions(runtimePubsub.Subscription{
		PubsubName:      "test",
		Topic:           "topic",
		DeadLetterTopic: "dead",
		Metadata:        map[string]string{},
		Rules: []*runtimePubsub.Rule{
			{
				Match: &expr.Expr{},
				Path:  "path",
			},
		},
	})
	compStore.AddHTTPEndpoint(httpEndpointsV1alpha1.HTTPEndpoint{
		ObjectMeta: metaV1.ObjectMeta{
			Name: "MockHTTPEndpoint",
		},
		Spec: httpEndpointsV1alpha1.HTTPEndpointSpec{
			BaseURL: "api.test.com",
			Headers: []commonapi.NameValuePair{
				{
					Name: "Accept-Language",
					Value: commonapi.DynamicValue{
						JSON: v1.JSON{Raw: []byte("en-US")},
					},
				},
			},
		},
	})

	a := &api{
		Universal: universal.New(universal.Options{
			AppID: "fakeAPI",
			Actors: fake.New().WithRuntimeStatus(func() *runtimev1pb.ActorRuntime {
				return &runtimev1pb.ActorRuntime{
					RuntimeStatus: runtimev1pb.ActorRuntime_RUNNING,
					HostReady:     true,
					ActiveActors: []*runtimev1pb.ActiveActorsCount{
						{Type: "abcd", Count: 10},
						{Type: "xyz", Count: 5},
					},
				}
			}),
			Logger:    logger.NewLogger("grpc.api.test"),
			CompStore: compStore,
			GetComponentsCapabilitiesFn: func() map[string][]string {
				capsMap := make(map[string][]string)
				capsMap["MockComponent1Name"] = []string{"mock.feat.MockComponent1Name"}
				capsMap["MockComponent2Name"] = []string{"mock.feat.MockComponent2Name"}
				return capsMap
			},
			Resiliency: resiliency.New(nil),
			ExtendedMetadata: map[string]string{
				"test": "value",
			},
			AppConnectionConfig: config.AppConnectionConfig{
				ChannelAddress:      "1.2.3.4",
				MaxConcurrency:      10,
				Port:                5000,
				Protocol:            "grpc",
				HealthCheckHTTPPath: "/healthz",
				HealthCheck: &config.AppHealthConfig{
					ProbeInterval: 10 * time.Second,
					ProbeTimeout:  5 * time.Second,
					ProbeOnly:     true,
					Threshold:     3,
				},
			},
			GlobalConfig: &config.Configuration{},
			WorkflowEngine: wfenginefake.New().WithRuntimeMetadata(func() *runtimev1pb.MetadataWorkflows {
				return &runtimev1pb.MetadataWorkflows{
					ConnectedWorkers: 1,
				}
			}),
		}),
	}

	lis := startDaprAPIServer(t, a, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	t.Run("Set Metadata", func(t *testing.T) {
		_, err := client.SetMetadata(t.Context(), &runtimev1pb.SetMetadataRequest{
			Key:   "foo",
			Value: "bar",
		})
		require.NoError(t, err)
	})

	t.Run("Get Metadata", func(t *testing.T) {
		res, err := client.GetMetadata(t.Context(), &runtimev1pb.GetMetadataRequest{})
		require.NoError(t, err)

		assert.Equal(t, "fakeAPI", res.GetId())

		bytes, err := json.Marshal(res)
		require.NoError(t, err)

		expectedResponse := `{"id":"fakeAPI","active_actors_count":[{"type":"abcd","count":10},{"type":"xyz","count":5}],"registered_components":[{"name":"MockComponent1Name","type":"mock.component1Type","version":"v1.0","capabilities":["mock.feat.MockComponent1Name"]},{"name":"MockComponent2Name","type":"mock.component2Type","version":"v1.0","capabilities":["mock.feat.MockComponent2Name"]}],"extended_metadata":{"daprRuntimeVersion":"edge","foo":"bar","test":"value"},"subscriptions":[{"pubsub_name":"test","topic":"topic","rules":{"rules":[{"path":"path"}]},"dead_letter_topic":"dead","type":2}],"http_endpoints":[{"name":"MockHTTPEndpoint"}],"app_connection_properties":{"port":5000,"protocol":"grpc","channel_address":"1.2.3.4","max_concurrency":10,"health":{"health_probe_interval":"10s","health_probe_timeout":"5s","health_threshold":3}},"runtime_version":"edge","actor_runtime":{"runtime_status":2,"active_actors":[{"type":"abcd","count":10},{"type":"xyz","count":5}],"host_ready":true},"workflows":{"connected_workers":1}}`
		assert.Equal(t, expectedResponse, string(bytes))
	})
}

func matchContextInterface(v any) bool {
	_, ok := v.(context.Context)
	return ok
}
