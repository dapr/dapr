/*
Copyright 2023 The Dapr Authors
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

package authz

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mtls))
}

// mtls tests placement can find quorum with tls disabled.
type mtls struct {
	sentry *sentry.Sentry
	place  *placement.Placement
}

func (m *mtls) Setup(t *testing.T) []framework.Option {
	m.sentry = sentry.New(t)

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, m.sentry.CABundle().TrustAnchors, 0o600))
	m.place = placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithSentryAddress(m.sentry.Address()),
		placement.WithTrustAnchorsFile(taFile),
	)

	return []framework.Option{
		framework.WithProcesses(m.sentry, m.place),
	}
}

func (m *mtls) Run(t *testing.T, ctx context.Context) {
	m.sentry.WaitUntilRunning(t, ctx)
	m.place.WaitUntilRunning(t, ctx)

	secProv, err := security.New(ctx, security.Options{
		SentryAddress:           m.sentry.Address(),
		ControlPlaneTrustDomain: "localhost",
		ControlPlaneNamespace:   "default",
		TrustAnchors:            m.sentry.CABundle().TrustAnchors,
		AppID:                   "app-1",
		MTLSEnabled:             true,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(ctx)

	errCh := make(chan error, 1)
	go func() {
		errCh <- secProv.Run(ctx)
	}()
	t.Cleanup(func() { cancel(); require.NoError(t, <-errCh) })

	sec, err := secProv.Handler(ctx)
	require.NoError(t, err)

	placeID, err := spiffeid.FromSegments(sec.ControlPlaneTrustDomain(), "ns", "default", "dapr-placement")
	require.NoError(t, err)

	host := m.place.Address()

	conn, err := grpc.DialContext(ctx, host, grpc.WithBlock(), sec.GRPCDialOptionMTLS(placeID))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := v1pb.NewPlacementClient(conn)

	// Can only create hosts where the app ID match.
	stream := establishStream(t, ctx, client)
	require.NoError(t, stream.Send(&v1pb.Host{
		Id: "app-1",
	}))
	waitForUnlock(t, stream)
	_, err = stream.Recv()
	require.NoError(t, err)

	stream = establishStream(t, ctx, client)
	require.NoError(t, stream.Send(&v1pb.Host{
		Id: "app-2",
	}))
	waitForUnlock(t, stream)
	_, err = stream.Recv()
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func waitForUnlock(t *testing.T, stream v1pb.Placement_ReportDaprStatusClient) {
	t.Helper()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := stream.Recv()
		//nolint:testifylint
		if assert.NoError(c, err) {
			assert.Equal(c, "unlock", resp.GetOperation())
		}
	}, time.Second*5, time.Millisecond*100)
}

func establishStream(t *testing.T, ctx context.Context, client v1pb.PlacementClient) v1pb.Placement_ReportDaprStatusClient {
	t.Helper()
	var stream v1pb.Placement_ReportDaprStatusClient
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		var err error
		stream, err = client.ReportDaprStatus(ctx)
		//nolint:testifylint
		if !assert.NoError(c, err) {
			return
		}
		//nolint:testifylint
		if assert.NoError(c, stream.Send(&v1pb.Host{
			Id: "app-1",
		})) {
			_, err = stream.Recv()
			//nolint:testifylint
			assert.NoError(c, err)
		}
	}, time.Second*5, time.Millisecond*100)
	return stream
}
