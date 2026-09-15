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

package trustanchors

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

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(file))
}

// file tests that daprd consumes trust anchors from a file given with
// --trust-anchors-file, and hot reloads appended trust anchors without a
// restart.
type file struct {
	daprd   *daprd.Daprd
	sentryA *sentry.Sentry
	sentryB *sentry.Sentry
	taFile  string
}

func (f *file) Setup(t *testing.T) []framework.Option {
	f.sentryA = sentry.New(t)
	f.sentryB = sentry.New(t)

	f.taFile = filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(f.taFile, f.sentryA.CABundle().X509.TrustAnchors, 0o600))

	f.daprd = daprd.New(t,
		daprd.WithAppID("my-app"),
		daprd.WithMode("standalone"),
		daprd.WithTrustAnchorsFile(f.taFile),
		daprd.WithSentryAddress(f.sentryA.Address()),
		daprd.WithEnableMTLS(true),
	)

	return []framework.Option{
		framework.WithProcesses(f.sentryA, f.sentryB, f.daprd),
	}
}

func (f *file) Run(t *testing.T, ctx context.Context) {
	f.sentryA.WaitUntilRunning(t, ctx)
	f.sentryB.WaitUntilRunning(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)

	// A client whose certificate chains to sentry A must always be accepted.
	dialFromSentry := func(t *testing.T, ctx context.Context, sntry *sentry.Sentry, appID string) connectivity.State {
		t.Helper()

		sctx, cancel := context.WithCancel(ctx)

		// The client trusts both roots so it can always verify daprd's serving
		// certificate (chained to A); what is under test is daprd's verification
		// of the client certificate.
		anchorsA := f.sentryA.CABundle().X509.TrustAnchors
		anchorsB := f.sentryB.CABundle().X509.TrustAnchors
		trustAnchors := make([]byte, 0, len(anchorsA)+len(anchorsB))
		trustAnchors = append(trustAnchors, anchorsA...)
		trustAnchors = append(trustAnchors, anchorsB...)

		secProv, err := security.New(sctx, security.Options{
			SentryAddress:           sntry.Address(),
			ControlPlaneTrustDomain: "localhost",
			ControlPlaneNamespace:   "default",
			TrustAnchors:            trustAnchors,
			AppID:                   appID,
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
			case <-time.After(time.Second * 5):
				t.Fatal("timed out waiting for security provider to stop")
			case cerr := <-secProvErr:
				require.NoError(t, cerr)
			}
		})

		sec, err := secProv.Handler(sctx)
		require.NoError(t, err)

		myAppID, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString("public"), "ns", "default", "my-app")
		require.NoError(t, err)

		gctx, gcancel := context.WithTimeout(ctx, time.Second*5)
		defer gcancel()
		//nolint:staticcheck
		conn, err := grpc.DialContext(gctx, f.daprd.InternalGRPCAddress(), sec.GRPCDialOptionMTLS(myAppID))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		conn.Connect()

		for {
			state := conn.GetState()
			if state == connectivity.Ready || state == connectivity.TransientFailure {
				return state
			}
			if !conn.WaitForStateChange(gctx, state) {
				return conn.GetState()
			}
		}
	}

	t.Run("client chained to sentry A is accepted", func(t *testing.T) {
		assert.Equal(t, connectivity.Ready, dialFromSentry(t, ctx, f.sentryA, "client-a"))
	})

	t.Run("client chained to sentry B is rejected before append", func(t *testing.T) {
		assert.Equal(t, connectivity.TransientFailure, dialFromSentry(t, ctx, f.sentryB, "client-b"))
	})

	t.Run("client chained to sentry B is accepted after append, without restart", func(t *testing.T) {
		anchorsA := f.sentryA.CABundle().X509.TrustAnchors
		anchorsB := f.sentryB.CABundle().X509.TrustAnchors
		combined := make([]byte, 0, len(anchorsA)+len(anchorsB))
		combined = append(combined, anchorsA...)
		combined = append(combined, anchorsB...)
		require.NoError(t, os.WriteFile(f.taFile, combined, 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, connectivity.Ready, dialFromSentry(t, ctx, f.sentryB, "client-b2"))
		}, time.Second*20, time.Millisecond*300)
	})
}
