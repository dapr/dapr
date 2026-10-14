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

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/api/universal"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/kit/crypto/test"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
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

		_, err := client.CallLocal(t.Context(), request.Proto())
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

		_, err := client.CallLocal(t.Context(), request)
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

		_, err := client.CallLocal(t.Context(), request.Proto())
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("drops non-lowercase identity headers without mTLS", func(t *testing.T) {
		got := make(map[string][]string)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.On("InvokeMethod",
			mock.MatchedBy(matchContextInterface),
			mock.AnythingOfType("*v1.InvokeMethodRequest"),
		).Run(func(args mock.Arguments) {
			for k, v := range args.Get(1).(*invokev1.InvokeMethodRequest).Metadata() {
				got[k] = v.GetValues()
			}
		}).Return(invokev1.NewInvokeMethodResponse(200, "OK", nil), nil)
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
		request := invokev1.NewInvokeMethodRequest("method").
			WithMetadata(map[string][]string{
				"Dapr-Caller-App-Id":           {"admin"},
				"Dapr-Caller-Namespace":        {"kube-system"},
				"Dapr-Callee-App-Id":           {"other"},
				invokev1.CallerIDHeader:        {"caller"},
				invokev1.CallerNamespaceHeader: {"caller-ns"},
				invokev1.CalleeIDHeader:        {"fakeAPI"},
				"other":                        {"kept"},
			})
		defer request.Close()

		_, err := client.CallLocal(t.Context(), request.Proto())
		require.NoError(t, err)
		assert.Equal(t, map[string][]string{
			invokev1.CallerIDHeader:        {"caller"},
			invokev1.CallerNamespaceHeader: {"caller-ns"},
			invokev1.CalleeIDHeader:        {"fakeAPI"},
			"other":                        {"kept"},
		}, got)
	})
}

func TestCallActorIdentityMetadata(t *testing.T) {
	spoofed := func() map[string]*internalv1pb.ListStringValue {
		return internalv1pb.MetadataToInternalMetadata(map[string][]string{
			"Dapr-Caller-App-Id":           {"admin"},
			"Dapr-Caller-Namespace":        {"kube-system"},
			"DAPR-CALLER-APP-ID":           {"admin"},
			invokev1.CallerIDHeader:        {"caller"},
			invokev1.CallerNamespaceHeader: {"caller-ns"},
			"Dapr-Callee-App-Id":           {"other"},
			"other":                        {"kept"},
		})
	}
	values := func(md map[string]*internalv1pb.ListStringValue) map[string][]string {
		got := make(map[string][]string, len(md))
		for k, v := range md {
			got[k] = v.GetValues()
		}
		return got
	}

	t.Run("drops non-lowercase caller identity without mTLS", func(t *testing.T) {
		var got map[string][]string
		fakeAPI := &api{
			Universal: universal.New(universal.Options{
				AppID: "fakeAPI",
				Actors: actorsfake.New().WithRouter(func(context.Context) (router.Interface, error) {
					return routerfake.New().WithCallFn(func(_ context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
						got = values(req.GetMetadata())
						return &internalv1pb.InternalInvokeResponse{Status: &internalv1pb.Status{Code: 200}}, nil
					}), nil
				}),
			}),
		}
		server, lis := startInternalServer(fakeAPI)
		defer server.Stop()
		clientConn := createTestClient(lis)
		defer clientConn.Close()

		_, err := internalv1pb.NewServiceInvocationClient(clientConn).CallActor(t.Context(), &internalv1pb.InternalInvokeRequest{
			Ver:      internalv1pb.APIVersion_V1,
			Actor:    &internalv1pb.Actor{ActorType: "mytype", ActorId: "1"},
			Message:  &commonv1pb.InvokeRequest{Method: "method"},
			Metadata: spoofed(),
		})
		require.NoError(t, err)
		assert.Equal(t, map[string][]string{
			invokev1.CallerIDHeader:        {"caller"},
			invokev1.CallerNamespaceHeader: {"caller-ns"},
			"Dapr-Callee-App-Id":           {"other"},
			"other":                        {"kept"},
			"X-Dapr-Remote":                {"true"},
		}, got)
	})

	t.Run("stamps caller identity from the SPIFFE ID", func(t *testing.T) {
		id, err := spiffe.FromStrings(spiffeid.RequireTrustDomainFromString("public"), "peer-ns", "peer")
		require.NoError(t, err)
		req := &internalv1pb.InternalInvokeRequest{Metadata: spoofed()}
		setIdentityMetadata(req, id, "")
		assert.Equal(t, map[string][]string{
			invokev1.CallerIDHeader:        {"peer"},
			invokev1.CallerNamespaceHeader: {"peer-ns"},
			"Dapr-Callee-App-Id":           {"other"},
			"other":                        {"kept"},
		}, values(req.GetMetadata()))
	})
}

func TestSetIdentityMetadata(t *testing.T) {
	id, err := spiffe.FromStrings(spiffeid.RequireTrustDomainFromString("public"), "caller-ns", "caller")
	require.NoError(t, err)

	t.Run("replaces all identity headers from the SPIFFE ID", func(t *testing.T) {
		req := invokev1.NewInvokeMethodRequest("method").
			WithMetadata(map[string][]string{
				"Dapr-Caller-App-Id":           {"admin"},
				"Dapr-Caller-Namespace":        {"kube-system"},
				"Dapr-Callee-App-Id":           {"other"},
				invokev1.CallerIDHeader:        {"not-caller"},
				invokev1.CallerNamespaceHeader: {"not-caller-ns"},
				invokev1.CalleeIDHeader:        {"not-callee"},
				"other":                        {"kept"},
			})
		defer req.Close()

		setIdentityMetadata(req.Proto(), id, "callee")
		got := make(map[string][]string)
		for k, v := range req.Metadata() {
			got[k] = v.GetValues()
		}
		assert.Equal(t, map[string][]string{
			invokev1.CallerIDHeader:        {"caller"},
			invokev1.CallerNamespaceHeader: {"caller-ns"},
			invokev1.CalleeIDHeader:        {"callee"},
			"other":                        {"kept"},
		}, got)
	})

	t.Run("stamps a request with no metadata", func(t *testing.T) {
		req := &internalv1pb.InternalInvokeRequest{}
		setIdentityMetadata(req, id, "callee")
		assert.Equal(t, []string{"caller"}, req.GetMetadata()[invokev1.CallerIDHeader].GetValues())
		assert.Equal(t, []string{"caller-ns"}, req.GetMetadata()[invokev1.CallerNamespaceHeader].GetValues())
		assert.Equal(t, []string{"callee"}, req.GetMetadata()[invokev1.CalleeIDHeader].GetValues())
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
		st, err := client.CallLocalStream(t.Context())
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
		st, err := client.CallLocalStream(t.Context())
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
		st, err := client.CallLocalStream(t.Context())
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

	resp, err := client.CallLocal(t.Context(), request.Proto())
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

	resp, err := client.CallActor(t.Context(), request.Proto())
	require.NoError(t, err)
	assert.NotEmpty(t, resp.GetMessage(), "failed to generate trace context with actor call")
}

// A forwarded reminder's creator is trusted only from a replica of this app,
// which relays what the Scheduler verified; any other peer is stamped as the
// creator itself; with no peer identity the claim is taken as is.
func TestCallActorReminderSourceAppID(t *testing.T) {
	serverID := spiffeid.RequireFromString("spiffe://example.org/ns/default/target")
	peerCtx := func(t *testing.T, id string) context.Context {
		t.Helper()
		return test.GenPKI(t, test.PKIOptions{LeafID: serverID, ClientID: spiffeid.RequireFromString(id)}).ClientGRPCCtx(t)
	}

	tests := map[string]struct {
		ctx       func(*testing.T) context.Context
		claimed   *string
		expSource string
		expCode   codes.Code
	}{
		"same app replica relays the verified creator": {
			ctx:     func(t *testing.T) context.Context { return peerCtx(t, "spiffe://example.org/ns/default/target") },
			claimed: ptr.Of("creator"), expSource: "creator",
		},
		"same app replica with no creator stays unknown": {
			ctx:       func(t *testing.T) context.Context { return peerCtx(t, "spiffe://example.org/ns/default/target") },
			expSource: "",
		},
		"another app is stamped as the creator whatever it claims": {
			ctx:     func(t *testing.T) context.Context { return peerCtx(t, "spiffe://example.org/ns/default/other") },
			claimed: ptr.Of("target"), expSource: "other",
		},
		"same app id in another namespace is denied": {
			ctx:     func(t *testing.T) context.Context { return peerCtx(t, "spiffe://example.org/ns/ns2/target") },
			claimed: ptr.Of("creator"), expCode: codes.PermissionDenied,
		},
		"no peer identity takes the claim as is": {
			ctx:     func(*testing.T) context.Context { return t.Context() },
			claimed: ptr.Of("creator"), expSource: "creator",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var got *actorapi.Reminder
			rtr := routerfake.New().WithCallReminderFn(func(_ context.Context, r *actorapi.Reminder) error {
				got = r
				return nil
			})
			fakeAPI := &api{
				logger: logger.NewLogger("test"),
				Universal: universal.New(universal.Options{
					AppID:     "target",
					Namespace: "default",
					Actors: actorsfake.New().WithRouter(func(context.Context) (router.Interface, error) {
						return rtr, nil
					}),
				}),
			}

			_, err := fakeAPI.CallActorReminder(tc.ctx(t), &internalv1pb.Reminder{
				ActorType:   "abc",
				ActorId:     "id",
				Name:        "activity-result-abc",
				SourceAppId: tc.claimed,
			})
			if tc.expCode != codes.OK {
				require.Equal(t, tc.expCode, status.Code(err), "err: %v", err)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tc.expSource, got.SourceAppID)
			assert.True(t, got.IsRemote)
		})
	}
}
