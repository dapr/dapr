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

package quorum

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(insecure))
}

// insecure tests placement can find quorum with tls (insecure) enabled.
type insecure struct {
	places []*placement.Placement
	sentry *sentry.Sentry
}

func (i *insecure) Setup(t *testing.T) []framework.Option {
	i.sentry = sentry.New(t)
	bundle := i.sentry.CABundle()

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.TrustAnchors, 0o600))

	fp := util.ReservePorts(t, 3)
	opts := []placement.Option{
		placement.WithInitialCluster(fmt.Sprintf("p1=localhost:%d,p2=localhost:%d,p3=localhost:%d", fp.Port(t, 0), fp.Port(t, 1), fp.Port(t, 2))),
		placement.WithInitialClusterPorts(fp.Port(t, 0), fp.Port(t, 1), fp.Port(t, 2)),
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(i.sentry.Address()),
	}
	i.places = []*placement.Placement{
		placement.New(t, append(opts, placement.WithID("p1"))...),
		placement.New(t, append(opts, placement.WithID("p2"))...),
		placement.New(t, append(opts, placement.WithID("p3"))...),
	}

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(i.sentry, i.places[0], i.places[1], i.places[2]),
	}
}

func (i *insecure) Run(t *testing.T, ctx context.Context) {
	i.sentry.WaitUntilRunning(t, ctx)
	i.places[0].WaitUntilRunning(t, ctx)
	i.places[1].WaitUntilRunning(t, ctx)
	i.places[2].WaitUntilRunning(t, ctx)

	secProv, err := security.New(ctx, security.Options{
		SentryAddress:           i.sentry.Address(),
		ControlPlaneTrustDomain: "localhost",
		ControlPlaneNamespace:   "default",
		TrustAnchors:            i.sentry.CABundle().TrustAnchors,
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

	var stream v1pb.Placement_ReportDaprStatusClient

	j := -1
	require.Eventually(t, func() bool {
		j++
		if j >= 3 {
			j = 0
		}
		host := i.places[j].Address()
		conn, cerr := grpc.DialContext(ctx, host, grpc.WithBlock(),
			grpc.WithReturnConnectionError(), sec.GRPCDialOptionMTLS(placeID),
		)
		if cerr != nil {
			return false
		}
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		client := v1pb.NewPlacementClient(conn)

		stream, err = client.ReportDaprStatus(ctx)
		if err != nil {
			return false
		}
		err = stream.Send(&v1pb.Host{Id: "app-1"})
		if err != nil {
			return false
		}
		_, err = stream.Recv()
		if err != nil {
			return false
		}
		return true
	}, time.Second*10, time.Millisecond*100)

	err = stream.Send(&v1pb.Host{
		Name:     "app-1",
		Port:     1234,
		Load:     1,
		Entities: []string{"entity-1", "entity-2"},
		Id:       "app-1",
		Pod:      "pod-1",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		o, err := stream.Recv()
		require.NoError(t, err)
		assert.Equal(c, "update", o.GetOperation())
		if assert.NotNil(c, o.GetTables()) {
			assert.Len(c, o.GetTables().GetEntries(), 2)
			assert.Contains(c, o.GetTables().GetEntries(), "entity-1")
			assert.Contains(c, o.GetTables().GetEntries(), "entity-2")
		}
	}, time.Second*20, time.Millisecond*100)
}
