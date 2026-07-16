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

package standalone

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(disable))
}

// disable tests standalone with mTLS disabled.
type disable struct {
	daprd        *daprd.Daprd
	sentry       *sentry.Sentry
	placement    *placement.Placement
	scheduler    *scheduler.Scheduler
	trustAnchors []byte
}

func (e *disable) Setup(t *testing.T) []framework.Option {
	e.sentry = sentry.New(t)
	e.trustAnchors = e.sentry.CABundle().X509.TrustAnchors

	e.placement = placement.New(t,
		placement.WithEnableTLS(false),
		placement.WithSentryAddress(e.sentry.Address()),
	)

	e.scheduler = scheduler.New(t)

	e.daprd = daprd.New(t,
		daprd.WithAppID("my-app"),
		daprd.WithMode("standalone"),
		daprd.WithSentryAddress(e.sentry.Address()),
		daprd.WithPlacementAddresses(e.placement.Address()),
		daprd.WithSchedulerAddresses(e.scheduler.Address()),

		// Disable mTLS
		daprd.WithEnableMTLS(false),
	)

	return []framework.Option{
		framework.WithProcesses(e.sentry, e.placement, e.scheduler, e.daprd),
	}
}

func (e *disable) Run(t *testing.T, ctx context.Context) {
	e.placement.WaitUntilRunning(t, ctx)
	e.scheduler.WaitUntilRunning(t, ctx)
	e.daprd.WaitUntilRunning(t, ctx)

	t.Run("trying plain text connection to Dapr API should succeed", func(t *testing.T) {
		conn, err := grpc.DialContext(ctx, e.daprd.InternalGRPCAddress(), //nolint:staticcheck
			grpc.WithReturnConnectionError(), //nolint:staticcheck
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		require.NoError(t, err)
		conn.Connect()
		assert.Equal(t, connectivity.Ready, conn.GetState())
		require.NoError(t, conn.Close())
	})

	t.Run("trying mTLS connection to Dapr API should fail", func(t *testing.T) {
		sctx, cancel := context.WithCancel(ctx)

		secProv, err := security.New(sctx, security.Options{
			SentryAddress:           e.sentry.Address(),
			ControlPlaneTrustDomain: "localhost",
			ControlPlaneNamespace:   "default",
			TrustAnchors:            e.trustAnchors,
			AppID:                   "another-app",
			MTLSEnabled:             true,
			Healthz:                 healthz.New(),
		})
		require.NoError(t, err)

		secProvErr := make(chan error)
		go func() {
			secProvErr <- secProv.Run(sctx)
		}()

		t.Cleanup(func() {
			cancel()
			select {
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for security provider to stop")
			case err = <-secProvErr:
				require.NoError(t, err)
			}
		})

		sec, err := secProv.Handler(sctx)
		require.NoError(t, err)

		myAppID, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString("public"), "ns", "default", "my-app")
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			gctx, gcancel := context.WithTimeout(ctx, time.Second)
			t.Cleanup(gcancel)
			_, err = grpc.DialContext(gctx, e.daprd.InternalGRPCAddress(), sec.GRPCDialOptionMTLS(myAppID), //nolint:staticcheck
				grpc.WithReturnConnectionError()) //nolint:staticcheck
			require.Error(t, err)
			if runtime.GOOS == "windows" {
				return !strings.Contains(err.Error(), "An existing connection was forcibly closed by the remote host.")
			}
			return true
		}, 5*time.Second, 10*time.Millisecond)
		require.ErrorContains(t, err, "tls: first record does not look like a TLS handshake")
	})
}
