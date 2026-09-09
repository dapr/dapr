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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(selfhosted))
}

// selfhosted tests that sentry automatically renews the CA when the issuer
// certificate's remaining validity drops below the threshold: the new trust
// anchor is appended (old anchor retained), the pending issuer pair is
// written beside the active pair, and sentry keeps signing with the OLD
// issuer during the propagation grace.
type selfhosted struct {
	sentry *procsentry.Sentry
	bundle bundle.Bundle
}

func (s *selfhosted) Setup(t *testing.T) []framework.Option {
	// Renewal fires ~5s after start (threshold 0.15 of the ~70s lifetime).
	// The propagation grace of 55s keeps the pending state stable for the
	// duration of the assertions.
	s.bundle = genBundle(t, "localhost", time.Second*65)

	s.sentry = procsentry.New(t,
		procsentry.WithConfiguration(configuration),
		procsentry.WithCABundle(s.bundle),
		procsentry.WithCATTL(time.Hour*24*365),
		procsentry.WithCARenewalThreshold(0.15),
		procsentry.WithPropagationGrace(time.Second*55),
	)

	return []framework.Option{
		framework.WithProcesses(s.sentry),
	}
}

func (s *selfhosted) Run(t *testing.T, ctx context.Context) {
	s.sentry.WaitUntilRunning(t, ctx)

	credDir := s.sentry.CredentialsDir()
	caPath := filepath.Join(credDir, "ca.crt")
	nextCertPath := filepath.Join(credDir, "issuer.next.crt")
	nextKeyPath := filepath.Join(credDir, "issuer.next.key")

	oldAnchors := s.bundle.X509.TrustAnchors
	oldIssuer := s.bundle.X509.IssChain[0]

	t.Run("trust anchor is appended and pending issuer pair written", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			anchorsPEM, err := os.ReadFile(caPath)
			if !assert.NoError(c, err) {
				return
			}
			assert.Len(c, parseAnchors(c, anchorsPEM), 2)
			assert.FileExists(c, nextCertPath)
			assert.FileExists(c, nextKeyPath)
		}, time.Second*30, time.Millisecond*100)

		anchorsPEM, err := os.ReadFile(caPath)
		require.NoError(t, err)
		require.Greater(t, len(anchorsPEM), len(oldAnchors))
		assert.Equal(t, oldAnchors, anchorsPEM[:len(oldAnchors)], "old anchor retained byte for byte")
	})

	t.Run("renewal metrics are recorded while pending", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics, merr := metricsSnapshot(ctx, s.sentry.MetricsPort())
			if !assert.NoError(c, merr) {
				return
			}
			assert.InDelta(c, 1, metrics["dapr_sentry_ca_renewal_total"], 0)
			assert.InDelta(c, 1, metrics["dapr_sentry_ca_renewal_pending"], 0)
			assert.Zero(c, metrics["dapr_sentry_ca_switchover_total"])
			// The switchover is scheduled roughly a propagation grace from the
			// renewal.
			assert.InDelta(c, time.Now().Add(time.Second*55).Unix(), metrics["dapr_sentry_ca_switchover_timestamp"], 90)
		}, time.Second*20, time.Millisecond*100)
	})

	t.Run("active issuer is unchanged and still signs during the grace", func(t *testing.T) {
		issuerPEM, err := os.ReadFile(filepath.Join(credDir, "issuer.crt"))
		require.NoError(t, err)
		assert.Equal(t, s.bundle.X509.IssChainPEM, issuerPEM)

		conn := s.sentry.DialGRPC(t, ctx, "spiffe://localhost/ns/default/dapr-sentry")
		client := sentrypbv1.NewCAClient(conn)
		leaf, err := signCert(t, ctx, client, "myapp", "default")
		require.NoError(t, err)
		assert.True(t, chainsTo(leaf, oldIssuer), "certificates signed during the grace must chain to the old issuer")

		// The signed certificate must verify against the full appended anchor
		// set.
		anchorsPEM, err := os.ReadFile(caPath)
		require.NoError(t, err)
		pool := poolOf(t, anchorsPEM)
		_, err = leaf.Verify(x509CertVerifyOptions(pool, oldIssuer))
		require.NoError(t, err)
	})
}
