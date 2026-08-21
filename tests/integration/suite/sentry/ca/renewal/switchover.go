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
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(switchover))
}

// switchover tests the full zero-downtime rollover: renewal appends a trust
// anchor, and after the propagation grace elapses sentry promotes the pending
// issuer and signs with the new key. Certificates from both eras verify
// against the final trust anchor set.
type switchover struct {
	sentry *procsentry.Sentry
	bundle bundle.Bundle
}

func (s *switchover) Setup(t *testing.T) []framework.Option {
	// Renewal fires ~5s after start (threshold 0.15 of the ~70s lifetime);
	// the switchover happens ~6s later plus the reload debounce.
	s.bundle = genBundle(t, "localhost", time.Second*65)

	s.sentry = procsentry.New(t,
		procsentry.WithConfiguration(configuration),
		procsentry.WithCABundle(s.bundle),
		procsentry.WithCATTL(time.Hour*24*365),
		procsentry.WithCARenewalThreshold(0.15),
		procsentry.WithPropagationGrace(time.Second*6),
	)

	return []framework.Option{
		framework.WithProcesses(s.sentry),
	}
}

func (s *switchover) Run(t *testing.T, ctx context.Context) {
	s.sentry.WaitUntilRunning(t, ctx)

	credDir := s.sentry.CredentialsDir()
	caPath := filepath.Join(credDir, "ca.crt")
	oldIssuer := s.bundle.X509.IssChain[0]

	var earlyLeaf *x509.Certificate
	t.Run("certificate signed before the rollover chains to the old issuer", func(t *testing.T) {
		conn := s.sentry.DialGRPC(t, ctx, "spiffe://localhost/ns/default/dapr-sentry")
		client := sentrypbv1.NewCAClient(conn)
		var err error
		earlyLeaf, err = signCert(t, ctx, client, "myapp", "default")
		require.NoError(t, err)
		assert.True(t, chainsTo(earlyLeaf, oldIssuer))
	})

	var newIssuerPEM []byte
	t.Run("pending issuer is eventually promoted", func(t *testing.T) {
		nextCertPath := filepath.Join(credDir, "issuer.next.crt")

		// Wait for the renewal to append the pending pair, remember it, then
		// wait for the promotion to remove it again.
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			pem, err := os.ReadFile(nextCertPath)
			if err == nil {
				newIssuerPEM = pem
			}
			issuerPEM, ierr := os.ReadFile(filepath.Join(credDir, "issuer.crt"))
			if !assert.NoError(c, ierr) {
				return
			}
			assert.NotEqual(c, s.bundle.X509.IssChainPEM, issuerPEM, "issuer should eventually be promoted")
			assert.NoFileExists(c, nextCertPath)
			assert.NoFileExists(c, filepath.Join(credDir, "issuer.next.key"))
		}, time.Second*40, time.Millisecond*100)

		require.NotEmpty(t, newIssuerPEM, "the pending issuer must have been observed before promotion")

		issuerPEM, err := os.ReadFile(filepath.Join(credDir, "issuer.crt"))
		require.NoError(t, err)
		assert.Equal(t, newIssuerPEM, issuerPEM, "the promoted issuer is the previously pending issuer")
	})

	t.Run("switchover metrics are recorded", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics, merr := metricsSnapshot(ctx, s.sentry.MetricsPort())
			if !assert.NoError(c, merr) {
				return
			}
			assert.GreaterOrEqual(c, metrics["dapr_sentry_ca_renewal_total"], float64(1))
			assert.GreaterOrEqual(c, metrics["dapr_sentry_ca_switchover_total"], float64(1))
			assert.GreaterOrEqual(c, metrics["dapr_sentry_issuercert_changed_total"], float64(1))
			assert.Zero(c, metrics["dapr_sentry_ca_renewal_pending"])
			assert.Zero(c, metrics["dapr_sentry_ca_switchover_timestamp"])
		}, time.Second*20, time.Millisecond*100)
	})

	t.Run("certificates signed after the rollover chain to the new issuer", func(t *testing.T) {
		newIssuers := parseAnchors(t, newIssuerPEM)
		require.Len(t, newIssuers, 1)
		newIssuer := newIssuers[0]

		var lateLeaf *x509.Certificate
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			// Redial on each attempt with the current on-disk anchors: the
			// server restarts around the switchover and then serves with a
			// certificate chained to the new issuer.
			anchorsPEM, err := os.ReadFile(caPath)
			if !assert.NoError(c, err) {
				return
			}
			client, closeConn, err := dialSentry(ctx, s.sentry.Port(), anchorsPEM, "spiffe://localhost/ns/default/dapr-sentry")
			if !assert.NoError(c, err) {
				return
			}
			defer closeConn()
			leaf, err := signCert(c, ctx, client, "myapp", "default")
			if !assert.NoError(c, err) {
				return
			}
			if !assert.True(c, chainsTo(leaf, newIssuer)) {
				return
			}
			lateLeaf = leaf
		}, time.Second*30, time.Millisecond*500)

		t.Run("both eras verify against the final trust anchor set", func(t *testing.T) {
			anchorsPEM, err := os.ReadFile(caPath)
			require.NoError(t, err)
			require.Len(t, parseAnchors(t, anchorsPEM), 2, "trust anchors are append only")
			pool := poolOf(t, anchorsPEM)

			_, err = earlyLeaf.Verify(x509CertVerifyOptions(pool, oldIssuer))
			require.NoError(t, err, "pre-rollover certificate must still verify")
			_, err = lateLeaf.Verify(x509CertVerifyOptions(pool, newIssuer))
			require.NoError(t, err, "post-rollover certificate must verify")
		})
	})
}
