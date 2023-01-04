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

package messaging

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/logger"
)

const maxGRPCServerUptime = 100 * time.Millisecond

func newDirectMessaging() *directMessaging {
	return &directMessaging{}
}

func TestDestinationHeaders(t *testing.T) {
	t.Run("destination header present", func(t *testing.T) {
		appID := "test1"
		req := invokev1.NewInvokeMethodRequest("GET").
			WithMetadata(map[string][]string{})
		defer req.Close()

		dm := newDirectMessaging()
		dm.addDestinationAppIDHeaderToMetadata(appID, req)
		md := req.Metadata()[invokev1.DestinationIDHeader]
		assert.Equal(t, appID, md.Values[0])
	})
}

func TestCallerAndCalleeHeaders(t *testing.T) {
	t.Run("caller and callee header present", func(t *testing.T) {
		callerAppID := "caller-app"
		calleeAppID := "callee-app"
		req := invokev1.NewInvokeMethodRequest("GET").
			WithMetadata(map[string][]string{})
		defer req.Close()

		dm := newDirectMessaging()
		dm.addCallerAndCalleeAppIDHeaderToMetadata(callerAppID, calleeAppID, req)
		actualCallerAppID := req.Metadata()[invokev1.CallerIDHeader]
		actualCalleeAppID := req.Metadata()[invokev1.CalleeIDHeader]
		assert.Equal(t, callerAppID, actualCallerAppID.Values[0])
		assert.Equal(t, calleeAppID, actualCalleeAppID.Values[0])
	})
}

func TestForwardedHeaders(t *testing.T) {
	t.Run("forwarded headers present", func(t *testing.T) {
		req := invokev1.NewInvokeMethodRequest("GET").
			WithMetadata(map[string][]string{})
		defer req.Close()

		dm := newDirectMessaging()
		dm.hostAddress = "1"
		dm.hostName = "2"

		dm.addForwardedHeadersToMetadata(req)

		md := req.Metadata()[fasthttp.HeaderXForwardedFor]
		assert.Equal(t, "1", md.Values[0])

		md = req.Metadata()[fasthttp.HeaderXForwardedHost]
		assert.Equal(t, "2", md.Values[0])

		md = req.Metadata()[fasthttp.HeaderForwarded]
		assert.Equal(t, "for=1;by=1;host=2", md.Values[0])
	})

	t.Run("forwarded headers get appended", func(t *testing.T) {
		req := invokev1.NewInvokeMethodRequest("GET").
			WithMetadata(map[string][]string{
				fasthttp.HeaderXForwardedFor:  {"originalXForwardedFor"},
				fasthttp.HeaderXForwardedHost: {"originalXForwardedHost"},
				fasthttp.HeaderForwarded:      {"originalForwarded"},
			})
		defer req.Close()

		dm := newDirectMessaging()
		dm.hostAddress = "1"
		dm.hostName = "2"

		dm.addForwardedHeadersToMetadata(req)

		md := req.Metadata()[fasthttp.HeaderXForwardedFor]
		assert.Equal(t, "originalXForwardedFor", md.Values[0])
		assert.Equal(t, "1", md.Values[1])

		md = req.Metadata()[fasthttp.HeaderXForwardedHost]
		assert.Equal(t, "originalXForwardedHost", md.Values[0])
		assert.Equal(t, "2", md.Values[1])

		md = req.Metadata()[fasthttp.HeaderForwarded]
		assert.Equal(t, "originalForwarded", md.Values[0])
		assert.Equal(t, "for=1;by=1;host=2", md.Values[1])
	})
}

func TestKubernetesNamespace(t *testing.T) {
	t.Run("no namespace", func(t *testing.T) {
		appID := "app1"

		dm := newDirectMessaging()
		id, ns, err := dm.requestAppIDAndNamespace(appID)

		assert.NoError(t, err)
		assert.Empty(t, ns)
		assert.Equal(t, appID, id)
	})

	t.Run("with namespace", func(t *testing.T) {
		appID := "app1.ns1"

		dm := newDirectMessaging()
		id, ns, err := dm.requestAppIDAndNamespace(appID)

		assert.NoError(t, err)
		assert.Equal(t, "ns1", ns)
		assert.Equal(t, "app1", id)
	})

	t.Run("invalid namespace", func(t *testing.T) {
		appID := "app1.ns1.ns2"

		dm := newDirectMessaging()
		_, _, err := dm.requestAppIDAndNamespace(appID)

		assert.Error(t, err)
	})
}

func TestInvokeRemote(t *testing.T) {
	log.SetOutputLevel(logger.FatalLevel)
	defer log.SetOutputLevel(logger.InfoLevel)

	prepareEnvironment := func(t *testing.T, enableStreaming bool) *internalv1pb.InternalInvokeResponse {
		port, err := freeport.GetFreePort()
		require.NoError(t, err)
		server := startInternalServer(port, enableStreaming)
		defer server.Stop()
		clientConn := createTestClient(port)
		defer clientConn.Close()

		messaging := NewDirectMessaging(NewDirectMessagingOpts{
			MaxRequestBodySize: 10 << 20,
			ClientConnFn: func(ctx context.Context, address string, id string, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(destroy bool), error) {
				return clientConn, func(_ bool) {}, nil
			},
		}).(*directMessaging)

		request := invokev1.NewInvokeMethodRequest("method").
			WithMetadata(map[string][]string{invokev1.DestinationIDHeader: {"app1"}})
		defer request.Close()

		res, _, err := messaging.invokeRemote(context.Background(), "app1", "namespace1", "addr1", request)
		require.NoError(t, err)

		pd, err := res.ProtoWithData()
		require.NoError(t, err)

		return pd
	}

	t.Run("target supports streaming", func(t *testing.T) {
		pd := prepareEnvironment(t, true)

		assert.Equal(t, "🐱", string(pd.Message.Data.Value))
	})

	t.Run("target does not support streaming", func(t *testing.T) {
		pd := prepareEnvironment(t, false)

		assert.Equal(t, "🐶", string(pd.Message.Data.Value))
	})
}

func createTestClient(port int) *grpc.ClientConn {
	conn, err := grpc.Dial(
		fmt.Sprintf("localhost:%d", port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		panic(err)
	}
	return conn
}

func startInternalServer(port int, enableStreaming bool) *grpc.Server {
	lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

	server := grpc.NewServer()

	if enableStreaming {
		stream := &mockGRPCServerStream{}
		server.RegisterService(&grpc.ServiceDesc{
			ServiceName: "dapr.proto.internals.v1.ServiceInvocation",
			HandlerType: (*mockGRPCServerStreamI)(nil),
			Methods:     stream.methods(),
			Streams:     stream.streams(),
		}, stream)
	} else {
		unary := &mockGRPCServerUnary{}
		server.RegisterService(&grpc.ServiceDesc{
			ServiceName: "dapr.proto.internals.v1.ServiceInvocation",
			HandlerType: (*mockGRPCServerUnaryI)(nil),
			Methods:     unary.methods(),
		}, unary)
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			panic(err)
		}
	}()

	// wait until server starts
	time.Sleep(maxGRPCServerUptime)

	return server
}

type mockGRPCServerUnaryI interface {
	CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
}

type mockGRPCServerUnary struct{}

func (m *mockGRPCServerUnary) CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	resp := invokev1.NewInvokeMethodResponse(0, "", nil).
		WithRawDataString("🐶").
		WithContentType("text/plain")
	return resp.ProtoWithData()
}

func (m *mockGRPCServerUnary) methods() []grpc.MethodDesc {
	return []grpc.MethodDesc{
		{
			MethodName: "CallLocal",
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				in := new(internalv1pb.InternalInvokeRequest)
				if err := dec(in); err != nil {
					return nil, err
				}
				if interceptor == nil {
					return srv.(mockGRPCServerUnaryI).CallLocal(ctx, in)
				}
				info := &grpc.UnaryServerInfo{
					Server:     srv,
					FullMethod: "/dapr.proto.internals.v1.ServiceInvocation/CallLocal",
				}
				handler := func(ctx context.Context, req any) (any, error) {
					return srv.(mockGRPCServerUnaryI).CallLocal(ctx, req.(*internalv1pb.InternalInvokeRequest))
				}
				return interceptor(ctx, in, info, handler)
			},
		},
	}
}

type mockGRPCServerStreamI interface {
	mockGRPCServerUnaryI

	CallLocalStream(stream internalv1pb.ServiceInvocation_CallLocalStreamServer) error //nolint:nosnakecase
}

type mockGRPCServerStream struct {
	mockGRPCServerUnary
}

func (m *mockGRPCServerStream) CallLocalStream(stream internalv1pb.ServiceInvocation_CallLocalStreamServer) error { //nolint:nosnakecase
	resp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
		WithRawDataString("🐱").
		WithContentType("text/plain").
		WithHTTPHeaders(map[string][]string{"foo": {"bar"}})
	defer resp.Close()
	pd, err := resp.ProtoWithData()
	if err != nil {
		return err
	}
	var data []byte
	if pd.Message != nil && pd.Message.Data != nil {
		data = pd.Message.Data.Value
	}
	stream.Send(&internalv1pb.InternalInvokeResponseStream{
		Response: resp.Proto(),
		Payload: &commonv1pb.StreamPayload{
			Data:     data,
			Complete: true,
		},
	})
	return nil
}

func (m *mockGRPCServerStream) streams() []grpc.StreamDesc {
	return []grpc.StreamDesc{
		{
			StreamName: "CallLocalStream",
			Handler: func(srv any, stream grpc.ServerStream) error {
				return srv.(mockGRPCServerStreamI).CallLocalStream(&serviceInvocationCallLocalStreamServer{stream})
			},
			ServerStreams: true,
			ClientStreams: true,
		},
	}
}

type serviceInvocationCallLocalStreamServer struct {
	grpc.ServerStream
}

func (x *serviceInvocationCallLocalStreamServer) Send(m *internalv1pb.InternalInvokeResponseStream) error {
	return x.ServerStream.SendMsg(m)
}

func (x *serviceInvocationCallLocalStreamServer) Recv() (*internalv1pb.InternalInvokeRequestStream, error) {
	m := new(internalv1pb.InternalInvokeRequestStream)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}
