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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/channel"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/kit/logger"
)

func TestDestinationHeaders(t *testing.T) {
	t.Run("destination header present", func(t *testing.T) {
		const appID = "test1"
		req := invokev1.NewInvokeMethodRequest("GET").
			WithMetadata(map[string][]string{})
		defer req.Close()

		dm := &directMessaging{}
		dm.addDestinationAppIDHeaderToMetadata(appID, req)
		md := req.Metadata()[invokev1.DestinationIDHeader]
		assert.Equal(t, appID, md.GetValues()[0])
	})
}

func TestCallerAndCalleeHeaders(t *testing.T) {
	t.Run("caller and callee header present", func(t *testing.T) {
		callerAppID := "caller-app"
		calleeAppID := "callee-app"
		req := invokev1.NewInvokeMethodRequest("GET").
			WithMetadata(map[string][]string{})
		defer req.Close()

		dm := &directMessaging{}
		dm.addCallerAndCalleeAppIDHeaderToMetadata(callerAppID, calleeAppID, req)
		actualCallerAppID := req.Metadata()[invokev1.CallerIDHeader]
		actualCalleeAppID := req.Metadata()[invokev1.CalleeIDHeader]
		assert.Equal(t, callerAppID, actualCallerAppID.GetValues()[0])
		assert.Equal(t, calleeAppID, actualCalleeAppID.GetValues()[0])
	})
}

func TestForwardedHeaders(t *testing.T) {
	t.Run("forwarded headers present", func(t *testing.T) {
		req := invokev1.NewInvokeMethodRequest("GET").
			WithMetadata(map[string][]string{})
		defer req.Close()

		dm := &directMessaging{}
		dm.hostAddress = "1"
		dm.hostName = "2"

		dm.addForwardedHeadersToMetadata(req)

		md := req.Metadata()[fasthttp.HeaderXForwardedFor]
		assert.Equal(t, "1", md.GetValues()[0])

		md = req.Metadata()[fasthttp.HeaderXForwardedHost]
		assert.Equal(t, "2", md.GetValues()[0])

		md = req.Metadata()[fasthttp.HeaderForwarded]
		assert.Equal(t, "for=1;by=1;host=2", md.GetValues()[0])
	})

	t.Run("forwarded headers get appended", func(t *testing.T) {
		req := invokev1.NewInvokeMethodRequest("GET").
			WithMetadata(map[string][]string{
				fasthttp.HeaderXForwardedFor:  {"originalXForwardedFor"},
				fasthttp.HeaderXForwardedHost: {"originalXForwardedHost"},
				fasthttp.HeaderForwarded:      {"originalForwarded"},
			})
		defer req.Close()

		dm := &directMessaging{}
		dm.hostAddress = "1"
		dm.hostName = "2"

		dm.addForwardedHeadersToMetadata(req)

		md := req.Metadata()[fasthttp.HeaderXForwardedFor]
		assert.Equal(t, "originalXForwardedFor", md.GetValues()[0])
		assert.Equal(t, "1", md.GetValues()[1])

		md = req.Metadata()[fasthttp.HeaderXForwardedHost]
		assert.Equal(t, "originalXForwardedHost", md.GetValues()[0])
		assert.Equal(t, "2", md.GetValues()[1])

		md = req.Metadata()[fasthttp.HeaderForwarded]
		assert.Equal(t, "originalForwarded", md.GetValues()[0])
		assert.Equal(t, "for=1;by=1;host=2", md.GetValues()[1])
	})
}

func TestKubernetesNamespace(t *testing.T) {
	t.Run("no namespace", func(t *testing.T) {
		appID := "app1"

		dm := &directMessaging{}
		id, ns, err := dm.requestAppIDAndNamespace(appID)

		require.NoError(t, err)
		assert.Empty(t, ns)
		assert.Equal(t, appID, id)
	})

	t.Run("with namespace", func(t *testing.T) {
		appID := "app1.ns1"

		dm := &directMessaging{}
		id, ns, err := dm.requestAppIDAndNamespace(appID)

		require.NoError(t, err)
		assert.Equal(t, "ns1", ns)
		assert.Equal(t, "app1", id)
	})

	t.Run("invalid namespace", func(t *testing.T) {
		appID := "app1.ns1.ns2"

		dm := &directMessaging{}
		_, _, err := dm.requestAppIDAndNamespace(appID)

		require.Error(t, err)
	})
}

func TestInvokeRemote(t *testing.T) {
	log.SetOutputLevel(logger.FatalLevel)
	defer log.SetOutputLevel(logger.InfoLevel)

	socketDir := t.TempDir()

	prepareEnvironment := func(t *testing.T, enableStreaming bool, chunks []string) (*directMessaging, func()) {
		// Generate a random file name
		name := make([]byte, 8)
		_, err := io.ReadFull(rand.Reader, name)
		require.NoError(t, err)

		socket := filepath.Join(socketDir, hex.EncodeToString(name))
		server := startInternalServer(socket, enableStreaming, chunks)
		clientConn := createTestClient(socket)

		messaging := NewDirectMessaging(NewDirectMessagingOpts{
			MaxRequestBodySize: 10 << 20,
			ClientConnFn: func(ctx context.Context, address string, id string, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(destroy bool), error) {
				return clientConn, func(_ bool) {}, nil
			},
		}).(*directMessaging)

		teardown := func() {
			messaging.Close()
			server.Stop()
			clientConn.Close()
		}

		return messaging, teardown
	}

	t.Run("streaming with no data", func(t *testing.T) {
		messaging, teardown := prepareEnvironment(t, true, nil)
		defer teardown()

		request := invokev1.
			NewInvokeMethodRequest("method").
			WithMetadata(map[string][]string{invokev1.DestinationIDHeader: {"app1"}})
		defer request.Close()

		res, _, err := messaging.invokeRemote(context.Background(), "app1", "namespace1", "addr1", request)
		require.NoError(t, err)

		pd, err := res.ProtoWithData()
		require.NoError(t, err)
		if err != nil {
			return
		}
		assert.True(t, pd.GetMessage().GetData() == nil || len(pd.GetMessage().GetData().GetValue()) == 0)
	})

	t.Run("streaming with single chunk", func(t *testing.T) {
		messaging, teardown := prepareEnvironment(t, true, []string{"🐱"})
		defer teardown()

		request := invokev1.
			NewInvokeMethodRequest("method").
			WithMetadata(map[string][]string{invokev1.DestinationIDHeader: {"app1"}})
		defer request.Close()

		res, _, err := messaging.invokeRemote(context.Background(), "app1", "namespace1", "addr1", request)
		require.NoError(t, err)

		pd, err := res.ProtoWithData()
		require.NoError(t, err)

		assert.Equal(t, "🐱", string(pd.GetMessage().GetData().GetValue()))
	})

	t.Run("streaming with multiple chunks", func(t *testing.T) {
		chunks := []string{
			"Sempre caro mi fu quest'ermo colle ",
			"e questa siepe, che da tanta parte ",
			"dell'ultimo orizzonte il guardo esclude. ",
			"… ",
			"E il naufragar m'è dolce in questo mare.",
		}
		messaging, teardown := prepareEnvironment(t, true, chunks)
		defer teardown()

		request := invokev1.
			NewInvokeMethodRequest("method").
			WithMetadata(map[string][]string{invokev1.DestinationIDHeader: {"app1"}})
		defer request.Close()

		res, _, err := messaging.invokeRemote(context.Background(), "app1", "namespace1", "addr1", request)
		require.NoError(t, err)

		pd, err := res.ProtoWithData()
		require.NoError(t, err)

		assert.Equal(t, "Sempre caro mi fu quest'ermo colle e questa siepe, che da tanta parte dell'ultimo orizzonte il guardo esclude. … E il naufragar m'è dolce in questo mare.", string(pd.GetMessage().GetData().GetValue()))
	})

	t.Run("target does not support streaming - request is not replayable", func(t *testing.T) {
		messaging, teardown := prepareEnvironment(t, false, nil)
		defer teardown()

		request := invokev1.
			NewInvokeMethodRequest("method").
			WithMetadata(map[string][]string{invokev1.DestinationIDHeader: {"app1"}})
		defer request.Close()

		res, _, err := messaging.invokeRemote(context.Background(), "app1", "namespace1", "addr1", request)
		require.Error(t, err)
		assert.Equal(t, fmt.Sprintf(streamingUnsupportedErr, "app1"), err.Error())
		assert.Nil(t, res)
	})

	t.Run("target does not support streaming - request is replayable", func(t *testing.T) {
		messaging, teardown := prepareEnvironment(t, false, nil)
		defer teardown()

		request := invokev1.
			NewInvokeMethodRequest("method").
			WithMetadata(map[string][]string{invokev1.DestinationIDHeader: {"app1"}}).
			WithReplay(true)
		defer request.Close()

		res, _, err := messaging.invokeRemote(context.Background(), "app1", "namespace1", "addr1", request)
		require.NoError(t, err)

		pd, err := res.ProtoWithData()
		require.NoError(t, err)

		assert.Equal(t, "🐶", string(pd.GetMessage().GetData().GetValue()))
	})

	t.Run("target does not support streaming - request has data in-memory", func(t *testing.T) {
		messaging, teardown := prepareEnvironment(t, false, nil)
		defer teardown()

		request := invokev1.
			FromInvokeRequestMessage(&commonv1pb.InvokeRequest{
				Method: "method",
				Data: &anypb.Any{
					Value: []byte("nel blu dipinto di blu"),
				},
				ContentType: "text/plain",
			}).
			WithMetadata(map[string][]string{invokev1.DestinationIDHeader: {"app1"}})
		defer request.Close()

		res, _, err := messaging.invokeRemote(context.Background(), "app1", "namespace1", "addr1", request)
		require.NoError(t, err)

		pd, err := res.ProtoWithData()
		require.NoError(t, err)

		assert.Equal(t, "🐶", string(pd.GetMessage().GetData().GetValue()))
	})
}

func createTestClient(socket string) *grpc.ClientConn {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(
		ctx,
		socket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return net.Dial("unix", addr)
		}),
	)
	if err != nil {
		panic(err)
	}
	return conn
}

func startInternalServer(socket string, enableStreaming bool, chunks []string) *grpc.Server {
	lis, _ := net.Listen("unix", socket)

	server := grpc.NewServer()

	if enableStreaming {
		stream := &mockGRPCServerStream{
			chunks: chunks,
		}
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
	chunks []string
}

func (m *mockGRPCServerStream) CallLocalStream(stream internalv1pb.ServiceInvocation_CallLocalStreamServer) error { //nolint:nosnakecase
	// Send the first chunk
	resp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
		WithContentType("text/plain").
		WithHTTPHeaders(map[string][]string{"foo": {"bar"}})
	defer resp.Close()

	chunksLen := uint64(len(m.chunks))
	var payload *commonv1pb.StreamPayload
	if chunksLen > 0 {
		payload = &commonv1pb.StreamPayload{
			Data: []byte(m.chunks[0]),
			Seq:  0,
		}
	}
	stream.Send(&internalv1pb.InternalInvokeResponseStream{
		Response: resp.Proto(),
		Payload:  payload,
	})

	// Send the next chunks if needed
	// Note this starts from index 1 on purpose
	for i := uint64(1); i < chunksLen; i++ {
		stream.Send(&internalv1pb.InternalInvokeResponseStream{
			Payload: &commonv1pb.StreamPayload{
				Data: []byte(m.chunks[i]),
				Seq:  i,
			},
		})
	}

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

func TestInvokeRemoteUnaryForHTTPEndpoint(t *testing.T) {
	t.Run("channel found", func(t *testing.T) {
		d := directMessaging{
			channels: (new(channels.Channels)).WithEndpointChannels(map[string]channel.HTTPEndpointAppChannel{"abc": &mockChannel{}}),
		}

		_, err := d.invokeRemoteUnaryForHTTPEndpoint(context.Background(), nil, "abc")
		require.NoError(t, err)
	})

	t.Run("channel not found", func(t *testing.T) {
		d := directMessaging{
			channels: new(channels.Channels),
		}

		_, err := d.invokeRemoteUnaryForHTTPEndpoint(context.Background(), nil, "abc")
		require.Error(t, err)
	})
}

type mockChannel struct{}

func (m *mockChannel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (*invokev1.InvokeMethodResponse, error) {
	return nil, nil
}
