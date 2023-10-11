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
	"strconv"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	procplacement "github.com/dapr/dapr/tests/integration/framework/process/placement"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(enable))
}

// enable tests Kubernetes daprd with mTLS enabled.
type enable struct {
	daprd        *procdaprd.Daprd
	sentry       *procsentry.Sentry
	placement    *procplacement.Placement
	operator     *procgrpc.GRPC
	trustAnchors []byte
}

func (e *enable) Setup(t *testing.T) []framework.Option {
	e.sentry = procsentry.New(t)

	bundle := e.sentry.CABundle()
	e.trustAnchors = bundle.TrustAnchors

	// Control plane services always serves with mTLS in kubernetes mode.
	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.TrustAnchors, 0o600))
	e.placement = procplacement.New(t,
		procplacement.WithEnableTLS(true),
		procplacement.WithTrustAnchorsFile(taFile),
		procplacement.WithSentryAddress("localhost:"+strconv.Itoa(e.sentry.Port())),
	)

	e.operator = newOperator(t, bundle.TrustAnchors, "localhost:"+strconv.Itoa(e.sentry.Port()))

	e.daprd = procdaprd.New(t,
		procdaprd.WithAppID("my-app"),
		procdaprd.WithMode("kubernetes"),
		procdaprd.WithExecOptions(exec.WithEnvVars("DAPR_TRUST_ANCHORS", string(bundle.TrustAnchors))),
		procdaprd.WithSentryAddress("localhost:"+strconv.Itoa(e.sentry.Port())),
		procdaprd.WithControlPlaneAddress("localhost:"+strconv.Itoa(e.operator.Port())),
		procdaprd.WithDisableK8sSecretStore(true),
		procdaprd.WithPlacementAddresses("localhost:"+strconv.Itoa(e.placement.Port())),

		// Enable mTLS
		procdaprd.WithEnableMTLS(true),
	)

	return []framework.Option{
		framework.WithProcesses(e.sentry, e.placement, e.operator, e.daprd),
	}
}

func (e *enable) Run(t *testing.T, ctx context.Context) {
	e.sentry.WaitUntilRunning(t, ctx)
	e.placement.WaitUntilRunning(t, ctx)
	e.daprd.WaitUntilRunning(t, ctx)

	t.Run("trying plain text connection to Dapr API should fail", func(t *testing.T) {
		gctx, gcancel := context.WithTimeout(ctx, time.Second*2)
		t.Cleanup(gcancel)
		_, err := grpc.DialContext(gctx, "localhost:"+strconv.Itoa(e.daprd.InternalGRPCPort()),
			grpc.WithReturnConnectionError(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		require.ErrorContains(t, err, "error reading server preface")
	})

	t.Run("trying mTLS connection to Dapr API should succeed", func(t *testing.T) {
		sctx, cancel := context.WithCancel(ctx)

		secProv, err := security.New(sctx, security.Options{
			SentryAddress:           "localhost:" + strconv.Itoa(e.sentry.Port()),
			ControlPlaneTrustDomain: "localhost",
			ControlPlaneNamespace:   "default",
			TrustAnchors:            e.trustAnchors,
			AppID:                   "another-app",
			MTLSEnabled:             true,
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
				assert.NoError(t, err)
			}
		})

		sec, err := secProv.Handler(sctx)
		require.NoError(t, err)

		myAppID, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString("public"), "ns", "default", "my-app")
		require.NoError(t, err)

		conn, err := grpc.DialContext(ctx, "localhost:"+strconv.Itoa(e.daprd.InternalGRPCPort()), sec.GRPCDialOptionMTLS(myAppID),
			grpc.WithReturnConnectionError())
		require.NoError(t, err)
		conn.Connect()
		assert.Equal(t, connectivity.Ready, conn.GetState())
		assert.NoError(t, conn.Close())
	})
}
