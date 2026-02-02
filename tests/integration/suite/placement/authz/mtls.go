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

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/healthz"
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
	require.NoError(t, os.WriteFile(taFile, m.sentry.CABundle().X509.TrustAnchors, 0o600))
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

	// Can only create hosts where the app ID match.
	// When no namespace is sent in the message, and tls is enabled
	// the placement service will infer the namespace from the SPIFFE ID.
	_, err := m.establishStream(t, ctx, &v1pb.Host{
		Id:        "app-1",
		Namespace: "default",
	})
	require.NoError(t, err)

	_, err = m.establishStream(t, ctx, &v1pb.Host{
		Id: "app-2",
	})
	require.Error(t, err)

	_, err = m.establishStream(t, ctx, &v1pb.Host{
		Id:        "app-1",
		Namespace: "foo",
	})
	require.Error(t, err)
}

func (m *mtls) establishStream(t *testing.T, ctx context.Context, firstMessage *v1pb.Host) (v1pb.Placement_ReportDaprStatusClient, error) {
	t.Helper()

	secProv, err := security.New(ctx, security.Options{
		SentryAddress:           m.sentry.Address(),
		ControlPlaneTrustDomain: "localhost",
		ControlPlaneNamespace:   "default",
		TrustAnchors:            m.sentry.CABundle().X509.TrustAnchors,
		AppID:                   "app-1",
		MTLSEnabled:             true,
		Healthz:                 healthz.New(),
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
	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, host, grpc.WithBlock(), sec.GRPCDialOptionMTLS(placeID))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := v1pb.NewPlacementClient(conn)

	stream, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)

	err = stream.Send(firstMessage)
	if err != nil {
		return nil, err
	}
	_, err = stream.Recv()

	_, err = stream.Recv()

	return stream, err
}
