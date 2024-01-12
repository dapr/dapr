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
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/api/universal"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
)

func TestCallLocal(t *testing.T) {
	t.Run("appchannel is not ready", func(t *testing.T) {
		fakeAPI := &api{
			Universal: universal.New(universal.Options{
				AppID: "fakeAPI",
			}),
			channels: new(channels.Channels),
		}
		server, lis := startInternalServer(fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(lis)
		defer clientConn.Close()

		client := internalv1pb.NewServiceInvocationClient(clientConn)
		request := invokev1.NewInvokeMethodRequest("method")
		defer request.Close()

		_, err := client.CallLocal(context.Background(), request.Proto())
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("parsing InternalInvokeRequest is failed", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		fakeAPI := &api{
			Universal: universal.New(universal.Options{
				AppID: "fakeAPI",
			}),
			channels: (new(channels.Channels)).WithAppChannel(mockAppChannel),
		}
		server, lis := startInternalServer(fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(lis)
		defer clientConn.Close()

		client := internalv1pb.NewServiceInvocationClient(clientConn)
		request := &internalv1pb.InternalInvokeRequest{
			Message: nil,
		}

		_, err := client.CallLocal(context.Background(), request)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("invokemethod returns error", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.On("InvokeMethod",
			mock.MatchedBy(matchContextInterface),
			mock.AnythingOfType("*v1.InvokeMethodRequest"),
		).Return(nil, status.Error(codes.Unknown, "unknown error"))
		fakeAPI := &api{
			Universal: universal.New(universal.Options{
				AppID: "fakeAPI",
			}),
			channels: (new(channels.Channels)).WithAppChannel(mockAppChannel),
		}
		server, lis := startInternalServer(fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(lis)
		defer clientConn.Close()

		client := internalv1pb.NewServiceInvocationClient(clientConn)
		request := invokev1.NewInvokeMethodRequest("method")
		defer request.Close()

		_, err := client.CallLocal(context.Background(), request.Proto())
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestCallLocalStream(t *testing.T) {
	t.Run("appchannel is not ready", func(t *testing.T) {
		fakeAPI := &api{
			Universal: universal.New(universal.Options{
				AppID: "fakeAPI",
			}),
			channels: new(channels.Channels),
		}
		server, lis := startInternalServer(fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(lis)
		defer clientConn.Close()

		client := internalv1pb.NewServiceInvocationClient(clientConn)
		st, err := client.CallLocalStream(context.Background())
		require.NoError(t, err)

		request := invokev1.NewInvokeMethodRequest("method")
		defer request.Close()
		err = st.Send(&internalv1pb.InternalInvokeRequestStream{
			Request: request.Proto(),
		})
		require.True(t, err == nil || errors.Is(err, io.EOF))
		err = st.CloseSend()
		require.NoError(t, err)

		_, err = st.Recv()
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("parsing InternalInvokeRequest is failed", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		fakeAPI := &api{
			Universal: universal.New(universal.Options{
				AppID: "fakeAPI",
			}),
			channels: (new(channels.Channels)).WithAppChannel(mockAppChannel),
		}
		server, lis := startInternalServer(fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(lis)
		defer clientConn.Close()

		client := internalv1pb.NewServiceInvocationClient(clientConn)
		st, err := client.CallLocalStream(context.Background())
		require.NoError(t, err)

		err = st.Send(&internalv1pb.InternalInvokeRequestStream{
			Request: &internalv1pb.InternalInvokeRequest{
				Message: nil,
			},
		})
		require.NoError(t, err)
		err = st.CloseSend()
		require.NoError(t, err)

		_, err = st.Recv()
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("invokemethod returns error", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.
			On(
				"InvokeMethod",
				mock.MatchedBy(matchContextInterface),
				mock.AnythingOfType("*v1.InvokeMethodRequest"),
			).
			Return(nil, status.Error(codes.Unknown, "unknown error"))
		fakeAPI := &api{
			Universal: universal.New(universal.Options{
				AppID: "fakeAPI",
			}),
			channels: (new(channels.Channels)).WithAppChannel(mockAppChannel),
		}
		server, lis := startInternalServer(fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(lis)
		defer clientConn.Close()

		client := internalv1pb.NewServiceInvocationClient(clientConn)
		st, err := client.CallLocalStream(context.Background())
		require.NoError(t, err)

		request := invokev1.NewInvokeMethodRequest("method").
			WithMetadata(map[string][]string{invokev1.DestinationIDHeader: {"foo"}})
		defer request.Close()

		pd, err := request.ProtoWithData()
		require.NoError(t, err)
		require.NotNil(t, pd.GetMessage().GetData())

		err = st.Send(&internalv1pb.InternalInvokeRequestStream{
			Request: request.Proto(),
			Payload: &commonv1pb.StreamPayload{
				Data: pd.GetMessage().GetData().GetValue(),
				Seq:  0,
			},
		})
		require.NoError(t, err)
		err = st.CloseSend()
		require.NoError(t, err)

		_, err = st.Recv()
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestCallRemoteAppWithTracing(t *testing.T) {
	server, _, lis := startTestServerWithTracing()
	defer server.Stop()

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := internalv1pb.NewServiceInvocationClient(clientConn)
	request := invokev1.NewInvokeMethodRequest("method")
	defer request.Close()

	resp, err := client.CallLocal(context.Background(), request.Proto())
	require.NoError(t, err)
	assert.NotEmpty(t, resp.GetMessage(), "failed to generate trace context with app call")
}

func TestCallActorWithTracing(t *testing.T) {
	server, _, lis := startTestServerWithTracing()
	defer server.Stop()

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := internalv1pb.NewServiceInvocationClient(clientConn)

	request := invokev1.NewInvokeMethodRequest("method").
		WithActor("test-actor", "actor-1")
	defer request.Close()

	resp, err := client.CallActor(context.Background(), request.Proto())
	require.NoError(t, err)
	assert.NotEmpty(t, resp.GetMessage(), "failed to generate trace context with actor call")
}
