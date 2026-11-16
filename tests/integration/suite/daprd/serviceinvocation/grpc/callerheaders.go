/*
Copyright 2026 The Dapr Authors
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
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/healthz"
	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	testpb "github.com/dapr/dapr/tests/integration/framework/process/grpc/app/proto"
	httpapp "github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(callerheaders))
}

type callerheaders struct {
	sentry      *sentry.Sentry
	mtlsCaller  *daprd.Daprd
	mtlsCallee  *daprd.Daprd
	plainCaller *daprd.Daprd
	plainCallee *daprd.Daprd
	ch          chan metadata.MD

	sched       *scheduler.Scheduler
	actorHost   *daprd.Daprd
	actorCaller *daprd.Daprd
	actorCh     chan http.Header
}

func (c *callerheaders) Setup(t *testing.T) []framework.Option {
	c.ch = make(chan metadata.MD, 1)
	c.actorCh = make(chan http.Header, 1)
	srv := app.New(t,
		app.WithPingFn(func(ctx context.Context, _ *testpb.PingRequest) (*testpb.PingResponse, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			c.ch <- md
			return new(testpb.PingResponse), nil
		}),
		app.WithOnInvokeFn(func(ctx context.Context, _ *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			c.ch <- md
			return new(commonv1.InvokeResponse), nil
		}),
	)

	c.sentry = sentry.New(t)
	c.mtlsCaller = daprd.New(t,
		daprd.WithSentry(t, c.sentry),
		daprd.WithAppProtocol("grpc"),
	)
	c.mtlsCallee = daprd.New(t,
		daprd.WithSentry(t, c.sentry),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
	)
	c.plainCaller = daprd.New(t, daprd.WithAppProtocol("grpc"))
	c.plainCallee = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
	)

	actorApp := httpapp.New(t,
		httpapp.WithConfig(`{"entities": ["mytype"]}`),
		httpapp.WithHandlerFunc("/actors/mytype/", func(w http.ResponseWriter, r *http.Request) {
			c.actorCh <- r.Header
		}),
	)
	c.sched = scheduler.New(t,
		scheduler.WithSentry(c.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
		scheduler.WithPlacementEnabled(true),
	)
	c.actorHost = daprd.New(t,
		daprd.WithSentry(t, c.sentry),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(actorApp.Port()),
		daprd.WithScheduler(c.sched),
	)
	c.actorCaller = daprd.New(t,
		daprd.WithSentry(t, c.sentry),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithScheduler(c.sched),
	)

	return []framework.Option{
		framework.WithProcesses(srv, actorApp, c.sentry, c.sched,
			c.mtlsCaller, c.mtlsCallee, c.plainCaller, c.plainCallee,
			c.actorHost, c.actorCaller,
		),
	}
}

func (c *callerheaders) Run(t *testing.T, ctx context.Context) {
	c.sentry.WaitUntilRunning(t, ctx)
	c.sched.WaitUntilRunning(t, ctx)
	for _, d := range []*daprd.Daprd{c.mtlsCaller, c.mtlsCallee, c.plainCaller, c.plainCallee, c.actorHost, c.actorCaller} {
		d.WaitUntilRunning(t, ctx)
	}

	recv := func(t *testing.T) metadata.MD {
		t.Helper()
		select {
		case md := <-c.ch:
			return md
		case <-time.After(10 * time.Second):
			require.Fail(t, "timed out waiting for app to receive request")
			return nil
		}
	}

	recvActor := func(t *testing.T) http.Header {
		t.Helper()
		select {
		case h := <-c.actorCh:
			return h
		case <-time.After(10 * time.Second):
			require.Fail(t, "timed out waiting for actor app to receive request")
			return nil
		}
	}

	// Wait for the actor host to be registered before asserting on headers.
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		_, err := c.actorCaller.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "mytype", ActorId: "1", Method: "foo",
		})
		assert.NoError(col, err)
	}, 20*time.Second, 10*time.Millisecond)
	recvActor(t)

	assertIdentity := func(t *testing.T, md metadata.MD, callerAppID, callerNamespace, calleeAppID string) {
		t.Helper()
		assert.Equal(t, []string{callerAppID}, md.Get("dapr-caller-app-id"))
		assert.Equal(t, []string{callerNamespace}, md.Get("dapr-caller-namespace"))
		assert.Equal(t, []string{calleeAppID}, md.Get("dapr-callee-app-id"))
	}

	httpClient := client.HTTP(t)

	for name, pair := range map[string]struct{ caller, callee *daprd.Daprd }{
		"mtls":    {c.mtlsCaller, c.mtlsCallee},
		"no mtls": {c.plainCaller, c.plainCallee},
	} {
		t.Run(name, func(t *testing.T) {
			assertCallerIdentity := func(t *testing.T, md metadata.MD) {
				t.Helper()
				assertIdentity(t, md, pair.caller.AppID(), pair.caller.Namespace(), pair.callee.AppID())
			}

			reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/hello", pair.caller.HTTPPort(), pair.callee.AppID())
			for hname, headers := range map[string]map[string]string{
				"no identity headers": nil,
				"spoofed identity headers in canonical casing": {
					"Dapr-Caller-App-Id":    "admin",
					"Dapr-Caller-Namespace": "kube-system",
					"Dapr-Callee-App-Id":    "other",
				},
				"spoofed identity headers in lowercase": {
					"dapr-caller-app-id":    "admin",
					"dapr-caller-namespace": "kube-system",
					"dapr-callee-app-id":    "other",
				},
			} {
				t.Run("HTTP invoke with "+hname, func(t *testing.T) {
					req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
					require.NoError(t, err)
					for k, v := range headers {
						req.Header[k] = []string{v}
					}
					resp, err := httpClient.Do(req)
					require.NoError(t, err)
					require.NoError(t, resp.Body.Close())
					require.Equal(t, http.StatusOK, resp.StatusCode)
					assertCallerIdentity(t, recv(t))
				})
			}

			proxy := testpb.NewTestServiceClient(pair.caller.GRPCConn(t, ctx))

			t.Run("gRPC proxy with no identity metadata", func(t *testing.T) {
				pctx := metadata.AppendToOutgoingContext(ctx, "dapr-app-id", pair.callee.AppID())
				_, err := proxy.Ping(pctx, new(testpb.PingRequest))
				require.NoError(t, err)
				assertCallerIdentity(t, recv(t))
			})

			t.Run("gRPC proxy with spoofed identity metadata", func(t *testing.T) {
				pctx := metadata.AppendToOutgoingContext(ctx,
					"dapr-app-id", pair.callee.AppID(),
					"dapr-caller-app-id", "admin",
					"dapr-caller-namespace", "kube-system",
					// dapr-callee-app-id is also a routing key, so the first
					// value must be the real target.
					"dapr-callee-app-id", pair.callee.AppID(),
					"dapr-callee-app-id", "other",
				)
				_, err := proxy.Ping(pctx, new(testpb.PingRequest))
				require.NoError(t, err)
				assertCallerIdentity(t, recv(t))
			})

			// The app invoking itself through its own daprd's proxy must see
			// caller == callee == itself, as HTTP self-invocation does.
			self := testpb.NewTestServiceClient(pair.callee.GRPCConn(t, ctx))
			assertSelfIdentity := func(t *testing.T, md metadata.MD) {
				t.Helper()
				assertIdentity(t, md, pair.callee.AppID(), pair.callee.Namespace(), pair.callee.AppID())
			}

			t.Run("gRPC proxy self-invocation with no identity metadata", func(t *testing.T) {
				pctx := metadata.AppendToOutgoingContext(ctx, "dapr-app-id", pair.callee.AppID())
				_, err := self.Ping(pctx, new(testpb.PingRequest))
				require.NoError(t, err)
				assertSelfIdentity(t, recv(t))
			})

			t.Run("gRPC proxy self-invocation with spoofed identity metadata", func(t *testing.T) {
				pctx := metadata.AppendToOutgoingContext(ctx,
					"dapr-app-id", pair.callee.AppID(),
					"dapr-caller-app-id", "admin",
					"dapr-caller-namespace", "kube-system",
					"dapr-callee-app-id", pair.callee.AppID(),
					"dapr-callee-app-id", "other",
				)
				_, err := self.Ping(pctx, new(testpb.PingRequest))
				require.NoError(t, err)
				assertSelfIdentity(t, recv(t))
			})
		})
	}

	// An older caller daprd stamps the lowercase identity headers but also
	// forwards whatever its app sent. The callee daprd must re-stamp the caller
	// identity from the mTLS peer certificate.
	t.Run("mtls peer forwarding spoofed identity headers", func(t *testing.T) {
		const peerAppID = "callerheaders-older-caller"

		sctx, cancel := context.WithCancel(ctx)
		secProv, err := security.New(sctx, security.Options{
			SentryAddress:           c.sentry.Address(),
			ControlPlaneTrustDomain: "localhost",
			ControlPlaneNamespace:   "default",
			TrustAnchors:            c.sentry.CABundle().X509.TrustAnchors,
			AppID:                   peerAppID,
			MTLSEnabled:             true,
			Healthz:                 healthz.New(),
		})
		require.NoError(t, err)
		secErr := make(chan error)
		go func() { secErr <- secProv.Run(sctx) }()
		t.Cleanup(func() {
			cancel()
			select {
			case runErr := <-secErr:
				require.NoError(t, runErr)
			case <-time.After(5 * time.Second):
				assert.Fail(t, "timed out waiting for security provider to stop")
			}
		})
		sec, err := secProv.Handler(sctx)
		require.NoError(t, err)

		calleeID, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString("public"), "ns", c.mtlsCallee.Namespace(), c.mtlsCallee.AppID())
		require.NoError(t, err)
		conn, err := grpc.NewClient(c.mtlsCallee.InternalGRPCAddress(), sec.GRPCDialOptionMTLS(calleeID))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		assertPeerIdentity := func(t *testing.T, md metadata.MD) {
			t.Helper()
			assertIdentity(t, md, peerAppID, "default", c.mtlsCallee.AppID())
		}

		t.Run("CallLocal", func(t *testing.T) {
			_, err := internalv1pb.NewServiceInvocationClient(conn).CallLocal(ctx, &internalv1pb.InternalInvokeRequest{
				Ver:     internalv1pb.APIVersion_V1,
				Message: &commonv1.InvokeRequest{Method: "hello", Data: new(anypb.Any)},
				Metadata: map[string]*internalv1pb.ListStringValue{
					"Dapr-Caller-App-Id":    {Values: []string{"admin"}},
					"Dapr-Caller-Namespace": {Values: []string{"kube-system"}},
					"Dapr-Callee-App-Id":    {Values: []string{"other"}},
					"dapr-caller-app-id":    {Values: []string{"not-the-peer"}},
					"dapr-caller-namespace": {Values: []string{"not-the-peer-namespace"}},
					"dapr-callee-app-id":    {Values: []string{c.mtlsCallee.AppID()}},
				},
			})
			require.NoError(t, err)
			assertPeerIdentity(t, recv(t))
		})

		t.Run("gRPC proxy", func(t *testing.T) {
			pctx := metadata.AppendToOutgoingContext(ctx,
				"dapr-callee-app-id", c.mtlsCallee.AppID(),
				"dapr-callee-app-id", "other",
				"dapr-caller-app-id", "admin",
				"dapr-caller-app-id", "not-the-peer",
				"dapr-caller-namespace", "kube-system",
			)
			_, err := testpb.NewTestServiceClient(conn).Ping(pctx, new(testpb.PingRequest))
			require.NoError(t, err)
			assertPeerIdentity(t, recv(t))
		})

		t.Run("CallActor", func(t *testing.T) {
			hostID, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString("public"), "ns", c.actorHost.Namespace(), c.actorHost.AppID())
			require.NoError(t, err)
			hostConn, err := grpc.NewClient(c.actorHost.InternalGRPCAddress(), sec.GRPCDialOptionMTLS(hostID))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, hostConn.Close()) })

			_, err = internalv1pb.NewServiceInvocationClient(hostConn).CallActor(ctx, &internalv1pb.InternalInvokeRequest{
				Ver:     internalv1pb.APIVersion_V1,
				Actor:   &internalv1pb.Actor{ActorType: "mytype", ActorId: "1"},
				Message: &commonv1.InvokeRequest{Method: "foo", Data: new(anypb.Any)},
				Metadata: map[string]*internalv1pb.ListStringValue{
					"Dapr-Caller-App-Id":    {Values: []string{"admin"}},
					"Dapr-Caller-Namespace": {Values: []string{"kube-system"}},
					"dapr-caller-app-id":    {Values: []string{"not-the-peer"}},
					"dapr-caller-namespace": {Values: []string{"not-the-peer-namespace"}},
				},
			})
			require.NoError(t, err)
			header := recvActor(t)
			assert.Equal(t, []string{peerAppID}, header.Values("Dapr-Caller-App-Id"))
			assert.Equal(t, []string{"default"}, header.Values("Dapr-Caller-Namespace"))
		})
	})

	// An app invoking an actor hosted by another app, through its own daprd,
	// cannot inject identity headers in any casing.
	t.Run("actor invocation with spoofed identity headers", func(t *testing.T) {
		assertActorIdentity := func(t *testing.T, header http.Header) {
			t.Helper()
			assert.Equal(t, []string{c.actorCaller.AppID()}, header.Values("Dapr-Caller-App-Id"))
			assert.Equal(t, []string{c.actorCaller.Namespace()}, header.Values("Dapr-Caller-Namespace"))
		}

		for hname, headers := range map[string]map[string]string{
			"canonical casing": {
				"Dapr-Caller-App-Id":    "admin",
				"Dapr-Caller-Namespace": "kube-system",
			},
			"lowercase": {
				"dapr-caller-app-id":    "admin",
				"dapr-caller-namespace": "kube-system",
			},
		} {
			t.Run("gRPC InvokeActor with "+hname, func(t *testing.T) {
				actx := metadata.NewOutgoingContext(ctx, metadata.New(headers))
				_, err := c.actorCaller.GRPCClient(t, ctx).InvokeActor(actx, &rtv1.InvokeActorRequest{
					ActorType: "mytype", ActorId: "1", Method: "foo",
				})
				require.NoError(t, err)
				assertActorIdentity(t, recvActor(t))
			})

			t.Run("HTTP actor invoke with "+hname, func(t *testing.T) {
				reqURL := fmt.Sprintf("http://%s/v1.0/actors/mytype/1/method/foo", c.actorCaller.HTTPAddress())
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
				require.NoError(t, err)
				for k, v := range headers {
					req.Header[k] = []string{v}
				}
				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				require.Equal(t, http.StatusOK, resp.StatusCode)
				assertActorIdentity(t, recvActor(t))
			})
		}
	})
}
