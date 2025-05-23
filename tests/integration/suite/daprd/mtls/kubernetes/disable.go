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

package kubernetes

import (
	"context"
	"os"
	"path/filepath"
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
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(disable))
}

// disable tests Kubernetes daprd with mTLS disabled.
type disable struct {
	daprd        *procdaprd.Daprd
	sentry       *sentry.Sentry
	placement    *placement.Placement
	operator     *operator.Operator
	scheduler    *scheduler.Scheduler
	trustAnchors []byte
}

func (e *disable) Setup(t *testing.T) []framework.Option {
	e.sentry = sentry.New(t)

	bundle := e.sentry.CABundle()
	e.trustAnchors = bundle.X509.TrustAnchors

	// Control plane services always serves with mTLS in kubernetes mode.
	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.X509.TrustAnchors, 0o600))

	e.placement = placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(e.sentry.Address()),
	)

	e.scheduler = scheduler.New(t,
		scheduler.WithSentry(e.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	e.operator = operator.New(t, operator.WithSentry(e.sentry))

	e.daprd = procdaprd.New(t,
		procdaprd.WithAppID("my-app"),
		procdaprd.WithMode("kubernetes"),
		procdaprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.X509.TrustAnchors))),
		procdaprd.WithSentryAddress(e.sentry.Address()),
		procdaprd.WithControlPlaneAddress(e.operator.Address(t)),
		procdaprd.WithDisableK8sSecretStore(true),
		procdaprd.WithPlacementAddresses(e.placement.Address()),
		procdaprd.WithSchedulerAddresses(e.scheduler.Address()),

		// Disable mTLS
		procdaprd.WithEnableMTLS(false),
	)

	return []framework.Option{
		framework.WithProcesses(e.sentry, e.placement, e.operator, e.scheduler, e.daprd),
	}
}

func (e *disable) Run(t *testing.T, ctx context.Context) {
	e.sentry.WaitUntilRunning(t, ctx)
	e.placement.WaitUntilRunning(t, ctx)
	e.scheduler.WaitUntilRunning(t, ctx)
	e.daprd.WaitUntilRunning(t, ctx)

	t.Run("trying plain text connection to Dapr API should succeed", func(t *testing.T) {
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, e.daprd.InternalGRPCAddress(),
			grpc.WithReturnConnectionError(),
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
			//nolint:staticcheck
			_, err = grpc.DialContext(gctx, e.daprd.InternalGRPCAddress(), sec.GRPCDialOptionMTLS(myAppID),
				grpc.WithReturnConnectionError())
			require.Error(t, err)
			if runtime.GOOS == "windows" {
				return !strings.Contains(err.Error(), "An existing connection was forcibly closed by the remote host.")
			}
			return true
		}, 5*time.Second, 10*time.Millisecond)
		require.ErrorContains(t, err, "tls: first record does not look like a TLS handshake")
	})
}
