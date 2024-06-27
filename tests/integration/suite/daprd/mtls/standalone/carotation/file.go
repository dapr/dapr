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

package carotation

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
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(flag))
}

// flag tests that Daprd will trust a new CA bundle when it is rotated on disk.
// Sets trust anchor file with --trust-anchor-file flag.
type flag struct {
	daprd   *daprd.Daprd
	sentry1 *sentry.Sentry
	sentry2 *sentry.Sentry
	taFile  string
}

func (f *flag) Setup(t *testing.T) []framework.Option {
	f.sentry1 = sentry.New(t)
	f.sentry2 = sentry.New(t)

	f.taFile = filepath.Join(t.TempDir(), "trust_anchors.pem")
	require.NoError(t, os.WriteFile(f.taFile, f.sentry1.CABundle().TrustAnchors, 0o600))

	f.daprd = daprd.New(t,
		daprd.WithAppID("my-app"),
		daprd.WithMode("standalone"),
		daprd.WithSentryAddress(f.sentry1.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithTrustAnchorsFile(f.taFile),
	)

	return []framework.Option{
		framework.WithProcesses(f.sentry1, f.sentry2, f.daprd),
	}
}

func (f *flag) Run(t *testing.T, ctx context.Context) {
	f.sentry1.WaitUntilRunning(t, ctx)
	f.sentry2.WaitUntilRunning(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)

	securityFromSentry := func(t *testing.T, sentry *sentry.Sentry) security.Handler {
		t.Helper()

		sctx, cancel := context.WithCancel(ctx)

		secProv, err := security.New(sctx, security.Options{
			SentryAddress:           sentry.Address(),
			ControlPlaneTrustDomain: "localhost",
			ControlPlaneNamespace:   "default",
			TrustAnchors:            append(f.sentry1.CABundle().TrustAnchors, f.sentry2.CABundle().TrustAnchors...),
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
				require.NoError(t, err)
			}
		})

		sec, err := secProv.Handler(sctx)
		require.NoError(t, err)

		return sec
	}

	t.Run("trying plain text connection to Dapr API should fail", func(t *testing.T) {
		assert.EventuallyWithT(t, func(t *assert.CollectT) {
			gctx, gcancel := context.WithTimeout(ctx, time.Second/4)
			defer gcancel()
			//nolint:staticcheck
			_, err := grpc.DialContext(gctx, f.daprd.InternalGRPCAddress(),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithReturnConnectionError(),
			)
			//nolint:testifylint
			assert.ErrorContains(t, err, "error reading server preface:")
		}, time.Second*5, 100*time.Millisecond)
	})

	myAppID, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString("public"), "ns", "default", "my-app")
	require.NoError(t, err)

	t.Run("trying mTLS connection to Dapr API with same trust anchor should succeed", func(t *testing.T) {
		sec := securityFromSentry(t, f.sentry1)
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, f.daprd.InternalGRPCAddress(), sec.GRPCDialOptionMTLS(myAppID),
			grpc.WithReturnConnectionError())
		require.NoError(t, err)
		conn.Connect()
		assert.Equal(t, connectivity.Ready, conn.GetState())
		require.NoError(t, conn.Close())
	})

	t.Run("trying mTLS connection to Dapr API with new trust domain should succeed when CA is updated on file", func(t *testing.T) {
		sec := securityFromSentry(t, f.sentry2)

		assert.EventuallyWithT(t, func(t *assert.CollectT) {
			gctx, gcancel := context.WithTimeout(ctx, time.Second/4)
			defer gcancel()
			//nolint:staticcheck
			_, err := grpc.DialContext(gctx, f.daprd.InternalGRPCAddress(),
				sec.GRPCDialOptionMTLS(myAppID),
				grpc.WithReturnConnectionError(),
			)
			//nolint:testifylint
			assert.ErrorContains(t, err, "error reading server preface:")
		}, time.Second*5, time.Millisecond*100)

		// Update CA file on disk
		require.NoError(t, os.WriteFile(f.taFile,
			append(f.sentry1.CABundle().TrustAnchors, f.sentry2.CABundle().TrustAnchors...),
			0o600),
		)

		// Eventually, the connection should succeed because the target Daprd
		// accepts the new CA.
		assert.EventuallyWithT(t, func(t *assert.CollectT) {
			//nolint:staticcheck
			conn, err := grpc.DialContext(ctx, f.daprd.InternalGRPCAddress(),
				sec.GRPCDialOptionMTLS(myAppID),
				grpc.WithReturnConnectionError())
			//nolint:testifylint
			if assert.NoError(t, err) {
				conn.Connect()
				assert.Equal(t, connectivity.Ready, conn.GetState())
				require.NoError(t, conn.Close())
			}
		}, time.Second*5, time.Millisecond*100)
	})
}
