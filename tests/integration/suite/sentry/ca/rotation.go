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

package ca

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/tests/integration/framework"
	sentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(rotation))
}

// rotation tests that sentry will rotate issuer certificates when they are updated on disk.
type rotation struct {
	sentry         *sentry.Sentry
	issuerCredPath string
	bundle1        ca.Bundle
	bundle2        ca.Bundle
}

func (r *rotation) Setup(t *testing.T) []framework.Option {
	pk1, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	pk2, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	bundle1, err := ca.GenerateBundle(pk1, "integration.test.dapr.io", time.Second*5, nil)
	require.NoError(t, err)
	bundle2, err := ca.GenerateBundle(pk2, "integration.test.dapr.io", time.Second*5, nil)
	require.NoError(t, err)

	r.issuerCredPath = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(r.issuerCredPath, "issuer.crt"), bundle1.IssChainPEM, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(r.issuerCredPath, "issuer.key"), bundle1.IssKeyPEM, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(r.issuerCredPath, "ca.crt"), bundle1.TrustAnchors, 0o600))

	r.sentry = sentry.New(t,
		sentry.WithIssuerCredentialsPath(r.issuerCredPath),
		sentry.WithTrustDomain("integration.test.dapr.io"),
		sentry.WithWriteTrustBundle(false),
	)
	r.bundle1 = bundle1
	r.bundle2 = bundle2

	return []framework.Option{
		framework.WithProcesses(r.sentry),
	}
}

func (r *rotation) Run(t *testing.T, ctx context.Context) {
	r.sentry.WaitUntilRunning(t, ctx)

	sentryID, err := spiffeid.FromString("spiffe://integration.test.dapr.io/ns/default/dapr-sentry")
	require.NoError(t, err)

	t.Run("sentry should be serving on bundle 1 trust anchors", func(t *testing.T) {
		x509bundle, err := x509bundle.Parse(sentryID.TrustDomain(), r.bundle1.TrustAnchors)
		require.NoError(t, err)
		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx,
			fmt.Sprintf("localhost:%d", r.sentry.Port()),
			grpc.WithTransportCredentials(
				grpccredentials.TLSClientCredentials(x509bundle, tlsconfig.AuthorizeID(sentryID)),
			),
			grpc.WithReturnConnectionError(), grpc.WithBlock(),
		)
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	})

	t.Run("sentry should begin to serve on the second bundle after writing", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(r.issuerCredPath, "issuer.crt"), r.bundle2.IssChainPEM, 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(r.issuerCredPath, "issuer.key"), r.bundle2.IssKeyPEM, 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(r.issuerCredPath, "ca.crt"), r.bundle2.TrustAnchors, 0o600))

		x509bundle, err := x509bundle.Parse(sentryID.TrustDomain(), r.bundle2.TrustAnchors)
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(t *assert.CollectT) {
			sctx, cancel := context.WithTimeout(ctx, time.Second/4)
			defer cancel()
			//nolint:staticcheck
			conn, err := grpc.DialContext(sctx,
				fmt.Sprintf("localhost:%d", r.sentry.Port()),
				grpc.WithTransportCredentials(
					grpccredentials.TLSClientCredentials(x509bundle, tlsconfig.AuthorizeID(sentryID)),
				),
				grpc.WithReturnConnectionError(), grpc.WithBlock(),
			)
			//nolint:testifylint
			if assert.NoError(t, err) {
				require.NoError(t, conn.Close())
			}
		}, time.Second*5, time.Millisecond*100)
	})
}
