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

package actortype

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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/healthz"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(first))
}

type first struct {
	sentry *sentry.Sentry
	place  *placement.Placement
}

func (f *first) Setup(t *testing.T) []framework.Option {
	f.sentry = sentry.New(t)

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, f.sentry.CABundle().X509.TrustAnchors, 0o600))
	f.place = placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithSentryAddress(f.sentry.Address()),
		placement.WithTrustAnchorsFile(taFile),
	)

	return []framework.Option{
		framework.WithProcesses(f.sentry, f.place),
	}
}

func (f *first) Run(t *testing.T, ctx context.Context) {
	f.sentry.WaitUntilRunning(t, ctx)
	f.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return f.place.HasLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	secProv, err := security.New(ctx, security.Options{
		SentryAddress:           f.sentry.Address(),
		ControlPlaneTrustDomain: "localhost",
		ControlPlaneNamespace:   "default",
		TrustAnchors:            f.sentry.CABundle().X509.TrustAnchors,
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

	tests := []struct {
		entities    []string
		exp         bool
		errorString string
	}{
		{
			entities: []string{"allowed.entity.type"},
			exp:      true,
		},
		{
			entities: []string{"dapr.internal.default.app-1"},
			exp:      true,
		},
		{
			entities: []string{"dapr.internal.default.app-1.foo"},
			exp:      true,
		},
		{
			entities: []string{"dapr.internal.default.app-1.bar.foo"},
			exp:      true,
		},
		{
			entities: []string{
				"dapr.internal.default.app-1",
				"dapr.internal.default.app-1.bar",
				"dapr.internal.default.app-1.bar.foo",
			},
			exp: true,
		},
		{
			entities:    []string{"dapr.internal.foo.bar"},
			exp:         false,
			errorString: "entity dapr.internal.foo.bar is not allowed for app ID app-1 in namespace default",
		},
		{
			entities:    []string{"dapr.internal.default.app-2"},
			exp:         false,
			errorString: "entity dapr.internal.default.app-2 is not allowed for app ID app-1 in namespace default",
		},
		{
			entities:    []string{"dapr.internal"},
			exp:         false,
			errorString: "entity dapr.internal is not allowed for app ID app-1 in namespace default",
		},
		{
			entities:    []string{"dapr.internal."},
			exp:         false,
			errorString: "entity dapr.internal. is not allowed for app ID app-1 in namespace default",
		},
		{
			entities:    []string{"dapr.internal.default."},
			exp:         false,
			errorString: "entity dapr.internal.default. is not allowed for app ID app-1 in namespace default",
		},
		{
			entities: []string{
				"dapr.internal.default.app-1",
				"dapr.internal.foo.bar",
			},
			exp:         false,
			errorString: "entity dapr.internal.foo.bar is not allowed for app ID app-1 in namespace default",
		},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("entities=%v", tc.entities), func(t *testing.T) {
			//nolint:staticcheck
			conn, err := grpc.DialContext(ctx, f.place.Address(), grpc.WithBlock(), sec.GRPCDialOptionMTLS(placeID))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			client := v1pb.NewPlacementClient(conn)

			stream, err := client.ReportDaprStatus(ctx)
			require.NoError(t, err)

			err = stream.Send(&v1pb.Host{
				Id:        "app-1",
				Namespace: "default",
				Entities:  tc.entities,
			})
			require.NoError(t, err)
			_, err = stream.Recv()

			if tc.exp {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)

			s, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.PermissionDenied.String(), s.Code().String())
			assert.Equal(t, s.Message(), tc.errorString)
		})
	}
}
