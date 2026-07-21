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

package selfhosted

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(signing))
}

// signing ensures that once the propagation window has elapsed, signing
// switches to the new issuer while both root CAs remain trusted.
type signing struct {
	sentry *procsentry.Sentry
	bundle bundle.Bundle
}

func (s *signing) Setup(t *testing.T) []framework.Option {
	// Near-expiry root CA triggers rotation immediately; the short propagation
	// window lets the rotation progress to the signing phase, where it stays as
	// the old root CA is still valid for the duration of the test.
	s.bundle = genBundle(t, time.Hour)
	// The workload cert TTL must not exceed the propagation window, or the
	// rotator clamps the window up to the TTL.
	s.sentry = procsentry.New(t,
		procsentry.WithTrustDomain(trustDomain),
		procsentry.WithCABundle(s.bundle),
		procsentry.WithRotationCheckInterval(time.Second),
		procsentry.WithRotationPropagationWindow(time.Second*2),
		procsentry.WithConfiguration(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sentryconfig
spec:
  mtls:
    workloadCertTTL: "2s"
    allowedClockSkew: "1s"
`),
	)

	return []framework.Option{framework.WithProcesses(s.sentry)}
}

func (s *signing) Run(t *testing.T, ctx context.Context) {
	s.sentry.WaitUntilRunning(t, ctx)
	dir := s.sentry.BundleDirectory()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		state, ok := readRotationState(dir)
		if assert.True(c, ok, "rotation state must be persisted") {
			assert.Equal(c, string(bundle.RotationPhaseSigning), state.Phase)
		}
	}, time.Second*30, time.Millisecond*100)

	oldRoot := certsFromPEM(t, s.bundle.X509.TrustAnchors)[0]
	oldIssuer := s.bundle.X509.IssChain[0]
	newRoot := certsFromFile(t, dir, "rotation-ca.crt")[0]
	newIssuer := certsFromFile(t, dir, "rotation-issuer.crt")[0]

	// The active issuer on disk must now be the new issuer.
	issuer := certsFromFile(t, dir, "issuer.crt")
	assert.True(t, issuer[0].Equal(newIssuer), "the pending issuer must be promoted to the active issuer")
	assert.False(t, issuer[0].Equal(oldIssuer))

	// Both root CAs must remain in the on-disk trust anchors.
	anchors := certsFromFile(t, dir, "ca.crt")
	require.Len(t, anchors, 2, "both root CAs must remain in the trust anchors")
	assert.True(t, anchors[0].Equal(oldRoot))
	assert.True(t, anchors[1].Equal(newRoot))

	// New workload certs must chain to the new root.
	leaf, chain, respAnchors := signWorkloadCert(t, ctx, s.sentry, diskAnchorsPEM(t, dir))
	require.NotEmpty(t, chain)
	assert.True(t, chain[0].Equal(newIssuer), "workload certs must be signed by the new issuer")
	require.NoError(t, leaf.CheckSignatureFrom(chain[0]))
	require.NoError(t, chain[0].CheckSignatureFrom(newRoot))

	// The served trust anchors must still contain both root CAs so workload
	// certs signed by the old issuer keep verifying until they expire.
	require.Len(t, respAnchors, 2, "served trust anchors must contain both root CAs")
	assert.True(t, respAnchors[0].Equal(oldRoot))
	assert.True(t, respAnchors[1].Equal(newRoot))
	require.NoError(t, oldIssuer.CheckSignatureFrom(respAnchors[0]),
		"certs signed by the old issuer must still verify against the served trust anchors")

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics, merr := scrapeMetrics(t, ctx, s.sentry)
		if !assert.NoError(c, merr) {
			return
		}
		assert.Contains(c, metrics, "dapr_sentry_rootcert_rotation_total")
		assert.Contains(c, metrics, `phase="signing"`)
	}, time.Second*10, time.Millisecond*100)
}
