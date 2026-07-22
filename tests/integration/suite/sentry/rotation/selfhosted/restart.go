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
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/cert"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(restart))
}

// restart ensures a sentry restarted mid-rotation resumes the persisted
// rotation instead of starting a new one.
type restart struct {
	fresh     *procsentry.Sentry
	restarted *procsentry.Sentry
	bundle    bundle.Bundle
	dir       string
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	r.dir = t.TempDir()
	r.bundle = cert.GenerateCABundle(t, trustDomain, time.Hour*24)

	// The first sentry starts the rotation and parks in the distributing phase
	// (default 24h propagation window).
	r.fresh = procsentry.New(t,
		procsentry.WithTrustDomain(trustDomain),
		procsentry.WithCABundle(r.bundle),
		procsentry.WithCredentialsDirectory(r.dir),
		procsentry.WithRotationEnabled(true),
		procsentry.WithRotationCheckInterval(time.Second),
	)

	// The second sentry shares the credentials directory. Its short
	// propagation window (paired with a matching workload cert TTL so it is
	// not clamped) has already elapsed relative to the persisted
	// DistributedAt, so on resume it must progress the rotation to signing.
	r.restarted = procsentry.New(t,
		procsentry.WithTrustDomain(trustDomain),
		procsentry.WithCABundle(r.bundle),
		procsentry.WithCredentialsDirectory(r.dir),
		procsentry.WithWriteTrustBundle(false),
		procsentry.WithRotationEnabled(true),
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

	return []framework.Option{framework.WithProcesses(r.fresh)}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.fresh.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		state, ok := r.fresh.RotationState()
		if assert.True(c, ok, "rotation state must be persisted") {
			assert.Equal(c, string(bundle.RotationPhaseDistributing), state.Phase)
		}
	}, time.Second*20, time.Millisecond*10)

	stateBefore, ok := r.fresh.RotationState()
	require.True(t, ok)
	pendingRoot := cert.DecodePEMFile(t, filepath.Join(r.dir, "rotation-ca.crt"))[0]
	pendingIssuer := cert.DecodePEMFile(t, filepath.Join(r.dir, "rotation-issuer.crt"))[0]

	// Stop the first sentry mid-rotation and start the second one from the
	// same credentials directory.
	r.fresh.Cleanup(t)
	r.restarted.Run(t, ctx)
	t.Cleanup(func() { r.restarted.Cleanup(t) })
	r.restarted.WaitUntilRunning(t, ctx)

	// The restarted sentry must resume the persisted rotation and, with the
	// propagation window already elapsed, switch signing to the pending
	// issuer.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		state, ok := r.fresh.RotationState()
		if assert.True(c, ok, "rotation state must be persisted") {
			assert.Equal(c, string(bundle.RotationPhaseSigning), state.Phase)
		}
	}, time.Second*20, time.Millisecond*10)

	stateAfter, ok := r.fresh.RotationState()
	require.True(t, ok)
	assert.True(t, stateAfter.DistributedAt.Equal(stateBefore.DistributedAt),
		"the restarted sentry must resume the rotation begun before the restart, not start a new one")

	// The pending CA generated before the restart must be the one promoted.
	assert.True(t, cert.DecodePEMFile(t, filepath.Join(r.dir, "rotation-ca.crt"))[0].Equal(pendingRoot))
	issuer := cert.DecodePEMFile(t, filepath.Join(r.dir, "issuer.crt"))
	assert.True(t, issuer[0].Equal(pendingIssuer), "the pending issuer from before the restart must be promoted")

	// Both root CAs must remain in the trust anchors across the restart.
	anchors := cert.DecodePEMFile(t, filepath.Join(r.dir, "ca.crt"))
	require.Len(t, anchors, 2, "both root CAs must remain in the trust anchors")
	assert.True(t, anchors[0].Equal(cert.DecodePEM(t, r.bundle.X509.TrustAnchors)[0]))
	assert.True(t, anchors[1].Equal(pendingRoot))
}
