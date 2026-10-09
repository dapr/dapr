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

package kubernetes

import (
	"context"
	"crypto/x509"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/cert"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	sentryutils "github.com/dapr/dapr/tests/integration/suite/sentry/utils"
)

func init() {
	suite.Register(new(cleanup))
}

// cleanup ensures a full rotation cycle completes in Kubernetes mode: once
// the old root CA and all workload certs signed by the old issuer have
// expired, the old root CA is removed from the Secret and ConfigMap and the
// rotation state keys are deleted.
type cleanup struct {
	sentry *procsentry.Sentry
	bndl   bundle.Bundle
	tb     sentryutils.TrustBundleRW
}

func (e *cleanup) Setup(t *testing.T) []framework.Option {
	// The root CA expires 20s in, so the full cycle runs during the test:
	// distributing on the first check, signing ~3s in (propagation window,
	// which must be at least the workload cert TTL), cleanup once the old
	// root CA has expired (~20s) and the workload cert grace period
	// (workloadCertTTL + allowedClockSkew = 4s) has elapsed.
	e.bndl = cert.GenerateCABundle(t, trustDomain, time.Second*20)

	kubeAPI, tb := sentryutils.KubeAPIRW(t, sentryutils.KubeAPIOptions{
		Bundle:           e.bndl,
		Namespace:        "mynamespace",
		ServiceAccount:   "myserviceaccount",
		AppID:            "myappid",
		WorkloadCertTTL:  "3s",
		AllowedClockSkew: "1s",
	})
	e.tb = tb
	e.sentry = procsentry.New(t,
		procsentry.WithKubeAPI(t, kubeAPI, "sentrynamespace"),
		procsentry.WithCABundle(e.bndl),
		procsentry.WithTrustDomain(trustDomain),
		procsentry.WithRotationEnabled(true),
		procsentry.WithRotationCheckInterval(time.Second),
		procsentry.WithRotationPropagationWindow(time.Second*3),
	)

	return []framework.Option{framework.WithProcesses(kubeAPI, e.sentry)}
}

func (e *cleanup) Run(t *testing.T, ctx context.Context) {
	e.sentry.WaitUntilRunning(t, ctx)

	// Capture the pending new CA while the rotation is still in progress; the
	// rotation keys are deleted on cleanup.
	var newRoot, newIssuer *x509.Certificate
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		secret := e.tb.Secret.Current(t)
		assert.Equal(c, string(bundle.RotationPhaseSigning), string(secret.Data[procsentry.RotationPhaseSecretKey]))
	}, time.Second*15, time.Millisecond*10)
	secret := e.tb.Secret.Current(t)
	newRoot = cert.DecodePEM(t, secret.Data[procsentry.RotationNewCACertSecretKey])[0]
	newIssuer = cert.DecodePEM(t, secret.Data[procsentry.RotationNewIssCertSecretKey])[0]

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ok := e.tb.Secret.Current(t).Data[procsentry.RotationPhaseSecretKey]
		assert.False(c, ok, "rotation state must be cleared")
	}, time.Second*60, time.Millisecond*10)

	// All rotation keys must be deleted.
	secret = e.tb.Secret.Current(t)
	for _, key := range procsentry.RotationSecretKeys {
		_, ok := secret.Data[key]
		assert.False(t, ok, "rotation state key %q must be deleted", key)
	}

	// Only the new root CA and issuer must remain, in both the Secret and the
	// ConfigMap.
	anchors := cert.DecodePEM(t, secret.Data["ca.crt"])
	require.Len(t, anchors, 1, "only the new root CA must remain in the Secret trust anchors")
	assert.True(t, anchors[0].Equal(newRoot))
	issuer := cert.DecodePEM(t, secret.Data["issuer.crt"])[0]
	assert.True(t, issuer.Equal(newIssuer), "the active issuer must be the new issuer")

	configMap := e.tb.ConfigMap.Current(t)
	cmAnchors := cert.DecodePEM(t, []byte(configMap.Data["ca.crt"]))
	require.Len(t, cmAnchors, 1, "only the new root CA must remain in the ConfigMap trust anchors")
	assert.True(t, cmAnchors[0].Equal(newRoot))
}
