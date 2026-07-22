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
	"crypto/x509"
	"os"
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
	secpem "github.com/dapr/kit/crypto/pem"
)

func init() {
	suite.Register(new(cleanup))
}

// cleanup ensures a full rotation cycle completes: once the old root CA and
// all workload certs signed by the old issuer have expired, the old root CA
// is removed from the trust anchors and the rotation state is cleared.
type cleanup struct {
	sentry *procsentry.Sentry
	bundle bundle.Bundle
}

func (e *cleanup) Setup(t *testing.T) []framework.Option {
	// The root CA expires 20s in, so the full cycle runs during the test:
	// distributing on the first check, signing ~3s in (propagation window,
	// which must be at least the workload cert TTL), cleanup once the old
	// root CA has expired (~20s) and the workload cert grace period
	// (workloadCertTTL + allowedClockSkew = 4s) has elapsed.
	e.bundle = cert.GenerateCABundle(t, trustDomain, time.Second*20)
	e.sentry = procsentry.New(t,
		procsentry.WithTrustDomain(trustDomain),
		procsentry.WithCABundle(e.bundle),
		procsentry.WithRotationEnabled(true),
		procsentry.WithRotationCheckInterval(time.Second),
		procsentry.WithRotationPropagationWindow(time.Second*3),
		procsentry.WithConfiguration(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sentryconfig
spec:
  mtls:
    workloadCertTTL: "3s"
    allowedClockSkew: "1s"
`),
	)

	return []framework.Option{framework.WithProcesses(e.sentry)}
}

func (e *cleanup) Run(t *testing.T, ctx context.Context) {
	e.sentry.WaitUntilRunning(t, ctx)
	dir := e.sentry.BundleDirectory()

	// Capture the pending new CA while the rotation is still in progress; the
	// rotation files are removed on cleanup.
	var newRoot, newIssuer *x509.Certificate
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		state, ok := e.sentry.RotationState()
		if assert.True(c, ok, "rotation state must be persisted") {
			assert.Equal(c, string(bundle.RotationPhaseSigning), state.Phase)
		}
	}, time.Second*15, time.Millisecond*10)
	newRoot = cert.DecodePEMFile(t, filepath.Join(dir, "rotation-ca.crt"))[0]
	newIssuer = cert.DecodePEMFile(t, filepath.Join(dir, "rotation-issuer.crt"))[0]

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ok := e.sentry.RotationState()
		if !assert.False(c, ok, "rotation state must be cleared") {
			return
		}
		data, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
		if !assert.NoError(c, err) {
			return
		}
		anchors, err := secpem.DecodePEMCertificates(data)
		if !assert.NoError(c, err) {
			return
		}
		assert.Len(c, anchors, 1, "only the new root CA must remain in the trust anchors")
	}, time.Second*60, time.Millisecond*10)

	// Only the new root CA and issuer must remain.
	anchors := cert.DecodePEMFile(t, filepath.Join(dir, "ca.crt"))
	require.Len(t, anchors, 1)
	assert.True(t, anchors[0].Equal(newRoot), "the remaining trust anchor must be the new root CA")
	issuer := cert.DecodePEMFile(t, filepath.Join(dir, "issuer.crt"))
	assert.True(t, issuer[0].Equal(newIssuer), "the active issuer must be the new issuer")

	// All rotation files must be removed.
	for _, file := range []string{
		"rotation-state.json",
		"rotation-ca.crt",
		"rotation-issuer.crt",
		"rotation-issuer.key",
	} {
		assert.NoFileExists(t, filepath.Join(dir, file))
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics, merr := e.sentry.Metrics(t, ctx)
		if !assert.NoError(c, merr) {
			return
		}
		assert.Contains(c, metrics, "dapr_sentry_rootcert_rotation_total")
		assert.Contains(c, metrics, `phase="complete"`)
	}, time.Second*10, time.Millisecond*10)
}
