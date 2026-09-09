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

package renewal

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
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(daprd))
}

// daprd tests the end-to-end zero-downtime rollover contract: a daprd
// consuming trust anchors from a hot-reloadable file keeps accepting mTLS
// peers across the whole CA renewal, including peers whose certificates chain
// to the renewed issuer after the switchover, all without a daprd restart.
type daprd struct {
	sentry *procsentry.Sentry
	daprd  *procdaprd.Daprd
	bundle bundle.Bundle
	taFile string
}

func (d *daprd) Setup(t *testing.T) []framework.Option {
	// Renewal fires ~5s after start (threshold 0.15 of the ~70s lifetime),
	// giving daprd time to bootstrap before sentry's first reload; the
	// switchover happens ~6s later.
	d.bundle = genBundle(t, "localhost", time.Second*65)

	d.sentry = procsentry.New(t,
		procsentry.WithConfiguration(configuration),
		procsentry.WithCABundle(d.bundle),
		procsentry.WithCATTL(time.Hour*24*365),
		procsentry.WithCARenewalThreshold(0.15),
		procsentry.WithPropagationGrace(time.Second*6),
	)

	// daprd reads, and watches, the trust anchors directly from sentry's live
	// credentials file, standing in for the mounted per-namespace ConfigMap
	// distributed by the operator.
	d.taFile = filepath.Join(d.sentry.CredentialsDir(), "ca.crt")

	d.daprd = procdaprd.New(t,
		procdaprd.WithAppID("my-app"),
		procdaprd.WithMode("standalone"),
		procdaprd.WithTrustAnchorsFile(d.taFile),
		procdaprd.WithSentryAddress(d.sentry.Address()),
		procdaprd.WithEnableMTLS(true),
	)

	return []framework.Option{
		framework.WithProcesses(d.sentry, d.daprd),
	}
}

func (d *daprd) Run(t *testing.T, ctx context.Context) {
	d.sentry.WaitUntilRunning(t, ctx)
	d.daprd.WaitUntilRunning(t, ctx)

	// dialDaprd obtains a fresh identity from sentry and dials daprd's
	// internal mTLS API, returning the resulting connectivity state. Trust
	// anchors are consumed from the same hot-reloadable file as daprd's.
	dialDaprd := func(t *testing.T, ctx context.Context, appID string) (connectivity.State, error) {
		t.Helper()

		sctx, cancel := context.WithCancel(ctx)

		secProv, err := security.New(sctx, security.Options{
			SentryAddress:           d.sentry.Address(),
			ControlPlaneTrustDomain: "localhost",
			ControlPlaneNamespace:   "default",
			TrustAnchorsFile:        &d.taFile,
			AppID:                   appID,
			MTLSEnabled:             true,
			Healthz:                 healthz.New(),
		})
		if err != nil {
			cancel()
			return connectivity.Shutdown, err
		}

		secProvErr := make(chan error)
		go func() {
			secProvErr <- secProv.Run(sctx)
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case <-time.After(time.Second * 5):
				t.Error("timed out waiting for security provider to stop")
			case cerr := <-secProvErr:
				assert.NoError(t, cerr)
			}
		})

		hctx, hcancel := context.WithTimeout(sctx, time.Second*10)
		defer hcancel()
		sec, err := secProv.Handler(hctx)
		if err != nil {
			return connectivity.Shutdown, err
		}

		myAppID, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString("public"), "ns", "default", "my-app")
		if err != nil {
			return connectivity.Shutdown, err
		}

		gctx, gcancel := context.WithTimeout(ctx, time.Second*5)
		defer gcancel()
		//nolint:staticcheck
		conn, err := grpc.DialContext(gctx, d.daprd.InternalGRPCAddress(), sec.GRPCDialOptionMTLS(myAppID))
		if err != nil {
			return connectivity.Shutdown, err
		}
		t.Cleanup(func() { conn.Close() })
		conn.Connect()

		for {
			state := conn.GetState()
			if state == connectivity.Ready || state == connectivity.TransientFailure {
				return state, nil
			}
			if !conn.WaitForStateChange(gctx, state) {
				return conn.GetState(), nil
			}
		}
	}

	t.Run("mTLS works before the rollover", func(t *testing.T) {
		state, err := dialDaprd(t, ctx, "client-before")
		require.NoError(t, err)
		assert.Equal(t, connectivity.Ready, state)
	})

	t.Run("wait for the switchover to the renewed issuer", func(t *testing.T) {
		issuerPath := filepath.Join(d.sentry.CredentialsDir(), "issuer.crt")
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			issuerPEM, err := os.ReadFile(issuerPath)
			if !assert.NoError(c, err) {
				return
			}
			assert.NotEqual(c, string(d.bundle.X509.IssChainPEM), string(issuerPEM))
			assert.NoFileExists(c, filepath.Join(d.sentry.CredentialsDir(), "issuer.next.crt"))
		}, time.Second*40, time.Millisecond*200)
	})

	t.Run("peer with a certificate from the renewed issuer is accepted without daprd restart", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			// Retries cover the brief sentry restart around the switchover.
			state, err := dialDaprd(t, ctx, "client-after")
			if !assert.NoError(c, err) {
				return
			}
			assert.Equal(c, connectivity.Ready, state)
		}, time.Second*30, time.Second)
	})
}
