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
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	testpb "github.com/dapr/dapr/tests/integration/framework/process/grpc/app/proto"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(internalrelay))
}

// internalrelay ensures a peer cannot use a daprd's internal gRPC server to
// proxy a call to another app under that daprd's identity.
type internalrelay struct {
	sentry *sentry.Sentry
	relay  *daprd.Daprd
	target *daprd.Daprd
	ch     chan metadata.MD
}

func (i *internalrelay) Setup(t *testing.T) []framework.Option {
	i.ch = make(chan metadata.MD, 1)
	srv := app.New(t,
		app.WithPingFn(func(ctx context.Context, _ *testpb.PingRequest) (*testpb.PingResponse, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			select {
			case i.ch <- md:
			default:
			}
			return new(testpb.PingResponse), nil
		}),
	)

	i.sentry = sentry.New(t)
	i.relay = daprd.New(t,
		daprd.WithSentry(t, i.sentry),
		daprd.WithAppProtocol("grpc"),
	)
	i.target = daprd.New(t,
		daprd.WithSentry(t, i.sentry),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(srv, i.sentry, i.relay, i.target),
	}
}

func (i *internalrelay) Run(t *testing.T, ctx context.Context) {
	i.sentry.WaitUntilRunning(t, ctx)
	i.relay.WaitUntilRunning(t, ctx)
	i.target.WaitUntilRunning(t, ctx)

	sctx, cancel := context.WithCancel(ctx)
	secProv, err := security.New(sctx, security.Options{
		SentryAddress:           i.sentry.Address(),
		ControlPlaneTrustDomain: "localhost",
		ControlPlaneNamespace:   "default",
		TrustAnchors:            i.sentry.CABundle().X509.TrustAnchors,
		AppID:                   "internalrelay-peer",
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

	relayID, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString("public"), "ns", i.relay.Namespace(), i.relay.AppID())
	require.NoError(t, err)
	conn, err := grpc.NewClient(i.relay.InternalGRPCAddress(), sec.GRPCDialOptionMTLS(relayID))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	pctx := metadata.AppendToOutgoingContext(ctx, "dapr-callee-app-id", i.target.AppID())
	_, err = testpb.NewTestServiceClient(conn).Ping(pctx, new(testpb.PingRequest))
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "error: %v", err)

	select {
	case md := <-i.ch:
		assert.Failf(t, "target app received the relayed call", "caller app id: %v", md.Get("dapr-caller-app-id"))
	default:
	}
}
