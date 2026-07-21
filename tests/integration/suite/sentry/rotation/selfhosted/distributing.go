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
	suite.Register(new(distributing))
}

// distributing ensures a near-expiry root CA starts a rotation which parks in
// the distributing phase: both root CAs are appended to the trust anchors
// while signing still uses the old issuer.
type distributing struct {
	sentry *procsentry.Sentry
	bundle bundle.Bundle
}

func (d *distributing) Setup(t *testing.T) []framework.Option {
	// The root CA expires within the default 30 day trigger window so rotation
	// starts on sentry's first check. The default 24h propagation window keeps
	// the rotation parked in the distributing phase.
	d.bundle = genBundle(t, time.Hour*24)
	d.sentry = procsentry.New(t,
		procsentry.WithTrustDomain(trustDomain),
		procsentry.WithCABundle(d.bundle),
		procsentry.WithRotationCheckInterval(time.Second),
	)

	return []framework.Option{framework.WithProcesses(d.sentry)}
}

func (d *distributing) Run(t *testing.T, ctx context.Context) {
	d.sentry.WaitUntilRunning(t, ctx)
	dir := d.sentry.BundleDirectory()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		state, ok := readRotationState(dir)
		if assert.True(c, ok, "rotation state must be persisted") {
			assert.Equal(c, string(bundle.RotationPhaseDistributing), state.Phase)
		}
	}, time.Second*20, time.Millisecond*100)

	oldRoot := certsFromPEM(t, d.bundle.X509.TrustAnchors)[0]
	newRoot := certsFromFile(t, dir, "rotation-ca.crt")[0]
	assert.False(t, newRoot.Equal(oldRoot))

	// The on-disk trust anchors must contain the old and new root CAs,
	// appended.
	anchors := certsFromFile(t, dir, "ca.crt")
	require.Len(t, anchors, 2, "trust anchors must contain both root CAs")
	assert.True(t, anchors[0].Equal(oldRoot))
	assert.True(t, anchors[1].Equal(newRoot))

	// Signing must still use the old issuer during distribution.
	issuer := certsFromFile(t, dir, "issuer.crt")
	assert.True(t, issuer[0].Equal(d.bundle.X509.IssChain[0]))

	leaf, chain, respAnchors := signWorkloadCert(t, ctx, d.sentry, diskAnchorsPEM(t, dir))
	require.NotEmpty(t, chain)
	assert.True(t, chain[0].Equal(d.bundle.X509.IssChain[0]), "workload certs must still be signed by the old issuer")
	require.NoError(t, leaf.CheckSignatureFrom(chain[0]))

	// The served trust anchors must contain both root CAs so workloads begin
	// trusting the new root before signing switches.
	require.Len(t, respAnchors, 2, "served trust anchors must contain both root CAs")
	assert.True(t, respAnchors[0].Equal(oldRoot))
	assert.True(t, respAnchors[1].Equal(newRoot))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics, merr := scrapeMetrics(t, ctx, d.sentry)
		if !assert.NoError(c, merr) {
			return
		}
		assert.Contains(c, metrics, "dapr_sentry_rootcert_rotation_total")
		assert.Contains(c, metrics, `phase="distributing"`)
	}, time.Second*10, time.Millisecond*100)
}
